package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/observability"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/toolctx"
)

// LLMClient 接口定义 LLM 客户端的必要方法
type LLMClient interface {
	ChatWithTools(ctx context.Context, req llm.ChatWithToolsRequest) (*llm.ChatWithToolsResponse, error)
	ChatWithToolsStream(ctx context.Context, req llm.ChatWithToolsRequest, onChunk llm.StreamCallback) (*llm.ChatWithToolsResponse, error)
}

// StreamCallback sub-agent 流式内容回调（agentID, sessionID, content, reasoningContent）
// BREAKING (subagent-session-scoping): 新增 sessionID 第 2 参，强制 sessionID 流过 callback
// 链路，使 master 端 BroadcastSessionMessage 能拿到正确的 sessionID 做 cross-tenant 隔离。
type StreamCallback func(agentID string, sessionID string, content string, reasoning string)

// ToolBridge 接口定义工具桥接的必要方法
type ToolBridge interface {
	CallTool(ctx context.Context, filter *skills.ToolFilter, perm *skills.PermissionManager, toolName string, input json.RawMessage) (*mcphost.ToolResult, error)
	AvailableTools(filter *skills.ToolFilter) []mcphost.ToolDefinition
}

// ProgressEvent SubAgent 进度事件
// BREAKING (subagent-session-scoping): 新增 SessionID 字段，emitProgress 会自动注入
// AgentLoop.sessionID，使 master 端 BroadcastSessionMessage 能拿到正确的 sessionID。
type ProgressEvent struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id,omitempty"`
	Turn      int    `json:"turn"`
	MaxTurns  int    `json:"max_turns"`
	ToolName  string `json:"tool_name,omitempty"`
	Status    string `json:"status"` // "tool_start", "tool_done", "turn_done"
	Error     string `json:"error,omitempty"`
}

// ProgressCallback 进度回调函数类型
type ProgressCallback func(event ProgressEvent)

// LLMCompleteCallback LLM 调用完成回调（用于成本追踪等）
type LLMCompleteCallback func(agentID, sessionID, userID, model string, usage llm.Usage)

// LLMClientResolver 动态获取 LLM 客户端的函数类型。
// Sub-Agent 在每次执行时调用 resolver 获取当前最优的 LLM client，
// 而非在构造时绑定静态 client。这使得 session 模型切换和 task-type 选路生效。
type LLMClientResolver func() *llm.Client

// StaticLLMResolver 将静态 *llm.Client 包装为 LLMClientResolver（向后兼容）。
func StaticLLMResolver(client *llm.Client) LLMClientResolver {
	return func() *llm.Client { return client }
}

// AgentCallbacks 聚合 AgentLoop 的所有可选回调
type AgentCallbacks struct {
	ProgressFn    ProgressCallback
	StreamFn      StreamCallback
	LLMCompleteFn LLMCompleteCallback
}

// AgentLoop 运行迭代的 LLM → tool-call → LLM 循环
type AgentLoop struct {
	llmClient     LLMClient
	llmResolver   LLMClientResolver // 动态获取 LLM client（优先于 llmClient）
	toolBridge    ToolBridge
	permMgr       *skills.PermissionManager
	logger        *zap.Logger
	maxTurns      int                 // 默认 50
	agentID       string              // SubAgent ID（用于 ToolContext）
	sessionID     string              // 关联的会话 ID（可选，用于权限审批上下文）
	callerType    toolctx.CallerType  // 调用者类型（默认 CallerSubAgent，固定 Agent 设为 CallerFixedAgent）
	progressFn    ProgressCallback    // 进度回调（可选）
	streamFn      StreamCallback      // 流式内容回调（可选）
	llmCompleteFn LLMCompleteCallback // LLM 调用完成回调（可选，用于成本追踪）
	userID        string              // 用户 ID（用于成本追踪，从 session.UserID 获取）
}

// NewAgentLoop 创建新的 AgentLoop
func NewAgentLoop(agentID string, client *llm.Client, bridge *skills.ToolBridge, perm *skills.PermissionManager, logger *zap.Logger) *AgentLoop {
	var llmClient LLMClient
	if client != nil {
		llmClient = client
	}
	var toolBridge ToolBridge
	if bridge != nil {
		toolBridge = bridge
	}
	return NewAgentLoopWithLLMClient(agentID, llmClient, toolBridge, perm, logger)
}

// NewAgentLoopWithLLMClient 使用已抽象的 LLMClient / ToolBridge 创建 AgentLoop。
// 它主要用于固定 Agent 和测试注入，不要求调用方持有具体 *llm.Client。
func NewAgentLoopWithLLMClient(agentID string, client LLMClient, bridge ToolBridge, perm *skills.PermissionManager, logger *zap.Logger) *AgentLoop {
	return &AgentLoop{
		llmClient:  client,
		toolBridge: bridge,
		permMgr:    perm,
		logger:     logger,
		maxTurns:   50,
		agentID:    agentID,
		callerType: toolctx.CallerSubAgent, // 默认为动态 SubAgent
	}
}

// NewAgentLoopWithResolver 创建使用动态 LLM 路由的 AgentLoop。
// resolver 在每次 LLM 调用前被调用，获取当前最优的 client。
func NewAgentLoopWithResolver(agentID string, resolver LLMClientResolver, bridge *skills.ToolBridge, perm *skills.PermissionManager, logger *zap.Logger) *AgentLoop {
	var toolBridge ToolBridge
	if bridge != nil {
		toolBridge = bridge
	}
	return &AgentLoop{
		llmResolver: resolver,
		toolBridge:  toolBridge,
		permMgr:     perm,
		logger:      logger,
		maxTurns:    50,
		agentID:     agentID,
		callerType:  toolctx.CallerSubAgent,
	}
}

// resolveLLMClient 获取当前 LLM client（优先 resolver，fallback 到静态 client）
func (a *AgentLoop) resolveLLMClient() LLMClient {
	if a.llmResolver != nil {
		if c := a.llmResolver(); c != nil {
			return c
		}
	}
	return a.llmClient
}

// SetProgressCallback 设置进度回调函数
func (a *AgentLoop) SetProgressCallback(fn ProgressCallback) {
	a.progressFn = fn
}

// SetStreamCallback 设置流式内容回调函数
func (a *AgentLoop) SetStreamCallback(fn StreamCallback) {
	a.streamFn = fn
}

// SetLLMCompleteCallback 设置 LLM 调用完成回调（用于成本追踪等）
func (a *AgentLoop) SetLLMCompleteCallback(fn LLMCompleteCallback) {
	a.llmCompleteFn = fn
}

// SetUserID 设置用户 ID（用于成本追踪回调 + 工具侧 ctx 注入）
func (a *AgentLoop) SetUserID(userID string) {
	a.userID = userID
}

// UserID 返回当前用户 ID（§9.1 继承校验 / 测试可观测性）
func (a *AgentLoop) UserID() string {
	return a.userID
}

// SetSessionID 设置关联的会话 ID，用于权限审批时关联到正确的会话。
// 不设置时权限审批仍可工作，但审批卡片无法关联到具体会话。
func (a *AgentLoop) SetSessionID(sessionID string) {
	a.sessionID = sessionID
}

// SetCallerType 设置调用者类型。固定 Agent 应设为 CallerFixedAgent，
// 使其可以使用 task/parallel_dispatch 工具进行受控委托。
func (a *AgentLoop) SetCallerType(ct toolctx.CallerType) {
	a.callerType = ct
}

// emitProgress 触发进度事件（回调为 nil 时不触发）
// subagent-session-scoping: 单写点注入 SessionID，避免每个 emit callsite 重复
// 设置；调用方只需保证 AgentLoop.sessionID 已通过 SetSessionID 设置。
func (a *AgentLoop) emitProgress(event ProgressEvent) {
	if a.progressFn == nil {
		return
	}
	if event.SessionID == "" {
		event.SessionID = a.sessionID
	}
	a.progressFn(event)
}

// Run 执行 agent 循环,返回最终文本响应
func (a *AgentLoop) Run(ctx context.Context, systemPrompt string, messages []llm.MessageWithTools, filter *skills.ToolFilter) (string, error) {
	tools := a.toolBridge.AvailableTools(filter)

	// 注意：ToolContext 功能已移除，直接执行循环

	for turn := 0; turn < a.maxTurns; turn++ {
		// 检查 context 是否已取消
		if ctx.Err() != nil {
			a.logger.Info("SubAgent 被取消", zap.String("agent", a.agentID), zap.Error(ctx.Err()))
			return "", errs.Wrap(errs.CodeCanceled, "agent 循环已取消", ctx.Err())
		}

		// 每次轮次开始时动态获取 LLM client（支持 session 模型切换和 task-type 选路）
		llmClient := a.resolveLLMClient()
		if llmClient == nil {
			return "", errs.New(errs.CodeLLMError, "无可用 LLM 客户端")
		}

		a.logger.Info("SubAgent 轮次开始",
			zap.String("agent", a.agentID),
			zap.Int("turn", turn+1),
			zap.Int("max_turns", a.maxTurns),
			zap.Int("tool_count", len(tools)),
		)

		llmStart := time.Now()
		resp, err := llmClient.ChatWithToolsStream(ctx, llm.ChatWithToolsRequest{
			SystemPrompt: systemPrompt,
			Messages:     messages,
			Tools:        tools,
			Temperature:  0.1,
			MaxTokens:    4096,
		}, func(chunk llm.StreamChunk) error {
			if chunk.Done {
				return nil
			}
			if a.streamFn != nil && (chunk.ContentSoFar != "" || chunk.ReasoningContent != "") {
				a.streamFn(a.agentID, a.sessionID, chunk.ContentSoFar, chunk.ReasoningContent)
			}
			return nil
		})
		if err != nil {
			// 区分 context 取消和真正的 LLM 错误
			if ctx.Err() != nil {
				return "", errs.Wrap(errs.CodeCanceled, fmt.Sprintf("SubAgent %s 调用期间被取消", a.agentID), err)
			}
			return "", errs.Wrap(errs.CodeLLMError, fmt.Sprintf("SubAgent %s LLM 调用失败", a.agentID), err)
		}

		a.logger.Info("SubAgent LLM 调用完成",
			zap.String("agent", a.agentID),
			zap.Int("turn", turn+1),
			zap.Duration("duration", time.Since(llmStart)),
			zap.String("finish_reason", resp.FinishReason),
			zap.Int("tool_calls", len(resp.ToolCalls)),
			zap.Int64("prompt_tokens", resp.Usage.PromptTokens),
			zap.Int64("completion_tokens", resp.Usage.CompletionTokens),
		)

		// 成本追踪回调（nil 安全）
		if a.llmCompleteFn != nil && (resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0) {
			model := ""
			if c, ok := llmClient.(*llm.Client); ok {
				model = c.Model()
			}
			a.llmCompleteFn(a.agentID, a.sessionID, a.userID, model, resp.Usage)
		}

		// 无 tool calls - 返回文本
		if len(resp.ToolCalls) == 0 {
			a.logger.Info("SubAgent 循环完成",
				zap.String("agent", a.agentID),
				zap.Int("turns_used", turn+1),
			)
			return resp.Content, nil
		}

		// 将 assistant 的 tool calls 添加到消息历史
		messages = append(messages, llm.MessageWithTools{
			Role:      "assistant",
			Content:   llm.NewTextContent(resp.Content),
			ToolCalls: resp.ToolCalls,
		})

		// 执行 tool calls
		for _, tc := range resp.ToolCalls {
			// 每个 tool call 前检查 context
			if ctx.Err() != nil {
				a.logger.Info("SubAgent 被取消", zap.String("agent", a.agentID), zap.Error(ctx.Err()))
				return "", errs.Wrap(errs.CodeCanceled, "agent 循环已取消", ctx.Err())
			}

			a.logger.Info("SubAgent 调用工具",
				zap.String("agent", a.agentID),
				zap.String("tool", tc.Name),
				zap.Int("turn", turn+1),
			)

			// 触发 tool_start 事件
			a.emitProgress(ProgressEvent{
				AgentID:  a.agentID,
				Turn:     turn,
				MaxTurns: a.maxTurns,
				ToolName: tc.Name,
				Status:   "tool_start",
			})

			// 工具调用超时：question 工具需要等待用户回复，使用更长超时
			toolStart := time.Now()
			toolTimeout := 2 * time.Minute
			if tc.Name == "question" {
				toolTimeout = 60 * time.Minute
			}
			toolCtx, toolCancel := context.WithTimeout(ctx, toolTimeout)
			nextToolCtx := inheritedToolContextForCall(ctx, a.callerType, a.agentID, turn, tc.ID)
			toolCtx = toolctx.WithToolContext(toolCtx, &nextToolCtx)
			// 注入 sessionID，供权限审批时关联到正确的会话
			// 优先从传入的 ctx 取（并发安全），其次从实例字段取（向后兼容）
			if sid := toolctx.GetSessionID(ctx); sid != "" {
				toolCtx = toolctx.WithSessionID(toolCtx, sid)
			} else if a.sessionID != "" {
				toolCtx = toolctx.WithSessionID(toolCtx, a.sessionID)
			}
			// §9.1 注入 userID：若 ctx 已携带 user 则保留（最外层 Master 路径），
			// 否则以 SubAgent 实例字段为准（spawn 时从 parent 继承 SetUserID）。
			// 保证 skill_install / skill_search 等下游工具能 auth.UserIDFrom(ctx) 拿到正确 userID。
			if auth.UserIDFrom(toolCtx) == "" && a.userID != "" {
				toolCtx = auth.WithUser(toolCtx, &auth.User{ID: a.userID, Role: "user", Status: "active"})
			}
			result, execErr := a.toolBridge.CallTool(toolCtx, filter, a.permMgr, tc.Name, tc.Arguments)
			toolCancel()

			content := ""
			toolError := ""
			isErr := false
			if execErr != nil {
				content = fmt.Sprintf("Error: %v", execErr)
				toolError = execErr.Error()
				isErr = true
			} else if result.IsError {
				content = fmt.Sprintf("Tool error: %s", mcphost.DecodeToolContent(result.Content))
				toolError = mcphost.DecodeToolContent(result.Content)
				isErr = true
			} else {
				content = mcphost.DecodeToolContent(result.Content)
			}

			a.logger.Info("SubAgent 工具执行完成",
				zap.String("agent", a.agentID),
				zap.String("tool", tc.Name),
				zap.Int("turn", turn+1),
				zap.Duration("duration", time.Since(toolStart)),
				zap.Bool("is_error", isErr),
				zap.Int("result_bytes", len(content)),
			)

			// 触发 tool_done 事件
			a.emitProgress(ProgressEvent{
				AgentID:  a.agentID,
				Turn:     turn,
				MaxTurns: a.maxTurns,
				ToolName: tc.Name,
				Status:   "tool_done",
				Error:    toolError,
			})

			// 将 tool 结果添加到消息历史
			messages = append(messages, llm.MessageWithTools{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    llm.NewTextContent(content),
				IsError:    isErr,
			})

			a.logger.Debug("工具调用已执行",
				zap.String("tool", tc.Name),
				zap.Bool("error", execErr != nil || result.IsError),
				zap.Int("turn", turn),
			)
		}

		// 触发 turn_done 事件
		a.emitProgress(ProgressEvent{
			AgentID:  a.agentID,
			Turn:     turn,
			MaxTurns: a.maxTurns,
			Status:   "turn_done",
		})
	}

	a.logger.Warn("SubAgent 超出最大轮次",
		zap.String("agent", a.agentID),
		zap.Int("max_turns", a.maxTurns),
	)
	return "", errs.New(errs.CodeAgentTimeout, fmt.Sprintf("agent 循环超过最大轮次 (%d)", a.maxTurns))
}

// SetMaxTurns 设置最大迭代轮次
func (a *AgentLoop) SetMaxTurns(maxTurns int) {
	a.maxTurns = maxTurns
}

func inheritedToolContextForCall(ctx context.Context, callerType toolctx.CallerType, callerName string, depth int, toolCallID string) toolctx.ToolContext {
	parent := toolctx.GetToolContext(ctx)
	next := *parent
	next.CallerType = callerType
	next.CallerName = callerName
	next.Depth = depth
	if next.TraceID == "" {
		next.TraceID = callerName
	}
	if parent.SpanID != "" {
		next.ParentSpanID = parent.SpanID
	}
	next.SpanID = observability.NewSpanID()
	if next.TurnID == "" {
		next.TurnID = next.TraceID
	}
	next.ToolCallID = toolCallID
	return next
}
