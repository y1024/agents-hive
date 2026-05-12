package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/subagent"
)

// Agent 生成会话摘要（PR 风格）
type Agent struct {
	*subagent.BaseAgent
	llm           *llm.Client
	llmResolver   subagent.LLMClientResolver // 动态获取 LLM client（优先于静态 llm）
	llmCompleteFn subagent.LLMCompleteCallback
	promptLoader  any    // PromptLoader（可选，用于 prompt 外部化）
	sessionID     string // 当前任务的会话 ID（由 handleTask 从 TaskRequest 中提取）
	userID        string // 用户 ID（由 handleTask 从 TaskRequest 中提取）
}

// SetPromptLoader 设置 Prompt 外部化加载器（可选，nil 时使用硬编码默认值）
func (a *Agent) SetPromptLoader(loader any) {
	a.promptLoader = loader
}

// loadPrompt 从 PromptLoader 加载 prompt，nil 时返回 defaultVal
func (a *Agent) loadPrompt(key, defaultVal string) string {
	if a.promptLoader == nil {
		return defaultVal
	}
	type loader interface {
		LoadOrDefault(string, string) string
	}
	if l, ok := a.promptLoader.(loader); ok {
		return l.LoadOrDefault(key, defaultVal)
	}
	return defaultVal
}

// SummaryRequest 摘要生成请求
type SummaryRequest struct {
	Messages []llm.MessageWithTools `json:"messages"`
}

// SummaryResult 摘要生成结果
type SummaryResult struct {
	Summary string `json:"summary"`
}

const lowTemperature = 0.3

// New 创建一个新的 SummaryAgent
// callbacks 可选：传入 AgentCallbacks 以启用 LLM 用量统计回调
func New(llmClient *llm.Client, logger *zap.Logger, callbacks ...subagent.AgentCallbacks) *Agent {
	card := subagent.AgentCard{
		ID:          "summary",
		Name:        "Summary Agent",
		Description: "生成 PR 风格的会话摘要",
	}

	agent := &Agent{
		llm: llmClient,
	}
	if len(callbacks) > 0 && callbacks[0].LLMCompleteFn != nil {
		agent.llmCompleteFn = callbacks[0].LLMCompleteFn
	}

	agent.BaseAgent = subagent.NewBaseAgent(card, agent.handleTask, nil, logger)
	return agent
}

// NewWithResolver 创建使用动态 LLM 路由的 SummaryAgent。
func NewWithResolver(resolver subagent.LLMClientResolver, logger *zap.Logger, callbacks ...subagent.AgentCallbacks) *Agent {
	card := subagent.AgentCard{
		ID:          "summary",
		Name:        "Summary Agent",
		Description: "生成 PR 风格的会话摘要",
	}

	agent := &Agent{
		llmResolver: resolver,
	}
	if len(callbacks) > 0 && callbacks[0].LLMCompleteFn != nil {
		agent.llmCompleteFn = callbacks[0].LLMCompleteFn
	}

	agent.BaseAgent = subagent.NewBaseAgent(card, agent.handleTask, nil, logger)
	return agent
}

// resolveLLM 获取当前 LLM client（优先 resolver，fallback 到静态 client）
func (a *Agent) resolveLLM() *llm.Client {
	if a.llmResolver != nil {
		if c := a.llmResolver(); c != nil {
			return c
		}
	}
	return a.llm
}

// handleTask 处理摘要生成任务
func (a *Agent) handleTask(ctx context.Context, req subagent.TaskRequest) subagent.TaskResponse {
	ctx = subagent.ContextFromTaskRequest(ctx, req)
	a.sessionID = req.SessionID
	a.userID = req.UserID
	payload, _ := subagent.ExtractPayload(req)

	var summaryReq SummaryRequest
	if err := json.Unmarshal(payload, &summaryReq); err != nil {
		return subagent.TaskResponse{
			Status: "failed",
			Error:  fmt.Sprintf("解析摘要请求失败: %v", err),
		}
	}

	summary, err := a.GenerateSummary(ctx, summaryReq.Messages)
	if err != nil {
		return subagent.TaskResponse{
			Status: "failed",
			Error:  fmt.Sprintf("生成摘要失败: %v", err),
		}
	}

	result := SummaryResult{Summary: summary}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return subagent.TaskResponse{
			Status: "failed",
			Error:  fmt.Sprintf("序列化摘要结果失败: %v", err),
		}
	}
	return subagent.TaskResponse{
		Status: "completed",
		Result: resultJSON,
	}
}

// GenerateSummary 生成会话摘要（PR 风格）
func (a *Agent) GenerateSummary(ctx context.Context, messages []llm.MessageWithTools) (string, error) {
	if len(messages) == 0 {
		return "", errs.New(errs.CodeInvalidInput, "消息列表为空")
	}

	// 最多取最近 30 条消息
	n := len(messages)
	if n > 30 {
		messages = messages[n-30:]
	}

	// 格式化对话内容
	conversationText := a.formatMessages(messages)

	promptTemplate := a.loadPrompt("subagents/summary",
		`分析以下会话内容，生成一个简洁的 PR 风格摘要。

会话内容:
{{CONVERSATION}}

输出格式：
## 摘要
[1-3 句话概述本次会话完成了什么]

## 主要变更
- [变更点 1]
- [变更点 2]
- ...

## 涉及文件
- [文件路径列表，如果有的话]

## 待办事项
- [未完成的工作，如果有的话]

要求：
1. 内容简洁，每个部分不超过 5 个条目
2. 使用中文
3. 只输出摘要内容，不要额外解释`)
	prompt := strings.NewReplacer("{{CONVERSATION}}", conversationText).Replace(promptTemplate)

	client := a.resolveLLM()
	if client == nil {
		return "", errs.New(errs.CodeLLMError, "无可用 LLM 客户端")
	}
	result, usage, err := client.GenerateWithTemperature(ctx, prompt, lowTemperature)
	if err != nil {
		return "", errs.Wrap(errs.CodeLLMError, "LLM 生成摘要失败", err)
	}
	if a.llmCompleteFn != nil {
		a.llmCompleteFn("summary", a.sessionID, a.userID, client.Model(), usage)
	}

	return strings.TrimSpace(result), nil
}

// formatMessages 格式化消息为文本
func (a *Agent) formatMessages(messages []llm.MessageWithTools) string {
	var sb strings.Builder

	for i, msg := range messages {
		role := msg.Role
		content := a.extractTextContent(msg)

		if content == "" {
			continue
		}

		sb.WriteString(fmt.Sprintf("[%d] %s: %s\n\n", i+1, role, content))
	}

	return sb.String()
}

// extractTextContent 从消息中提取文本内容
func (a *Agent) extractTextContent(msg llm.MessageWithTools) string {
	// 如果 Content 是多模态，提取所有文本部分
	if msg.Content.IsMultimodal() {
		var parts []string
		for _, part := range msg.Content.Parts() {
			if part.Type == llm.ContentText && part.Text != "" {
				parts = append(parts, part.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	// 纯文本内容
	return msg.Content.Text()
}
