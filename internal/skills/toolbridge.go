package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/cache"
	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/plugin"
)

// SkipToolCallCallbackKey 是 context key，用于主会话直接执行工具时跳过 onToolCall 回调，
// 避免与 master.executeTool 自身广播的事件重复。
type SkipToolCallCallbackKey struct{}

// ToolBridge 连接技能系统到 MCP 工具执行层
type ToolBridge struct {
	host          *mcphost.Host
	logger        *zap.Logger
	onToolCall    func(toolName string, args string) // 工具调用回调
	pluginMgr     *plugin.Manager                    // 插件管理器（可选）
	fileCache     *cache.FileCache                   // 文件缓存
	metrics       *Metrics                           // 指标收集（可选）
	executionGate ExecutionGate                      // 执行层 gate（可选）
}

// ExecutionGate 在真正执行工具前做运行时边界检查。
// 它用于把 Master 的 RouteDecision / plan mode 边界下沉到 sub-agent
// 的 ToolBridge.CallTool 路径，避免委托执行绕过父会话决策。
type ExecutionGate func(ctx context.Context, toolName string, input json.RawMessage) error

// DirectExecutionGate 保持旧调用点命名兼容；语义与 ExecutionGate 相同。
type DirectExecutionGate = ExecutionGate

type directExecutionOptions struct {
	gate DirectExecutionGate
}

type DirectExecutionOption func(*directExecutionOptions)

func WithDirectExecutionGate(gate DirectExecutionGate) DirectExecutionOption {
	return func(opts *directExecutionOptions) {
		opts.gate = gate
	}
}

// NewToolBridge 创建新的 ToolBridge
func NewToolBridge(host *mcphost.Host, logger *zap.Logger) *ToolBridge {
	fileCache, _ := cache.NewFileCache(100, 5*time.Minute)
	return &ToolBridge{
		host:      host,
		logger:    logger,
		fileCache: fileCache,
	}
}

// SetMetrics 设置指标收集器
func (b *ToolBridge) SetMetrics(m *Metrics) {
	b.metrics = m
}

// SetPluginManager 设置插件管理器
func (b *ToolBridge) SetPluginManager(mgr *plugin.Manager) {
	b.pluginMgr = mgr
}

// SetOnToolCall 设置工具调用回调
func (b *ToolBridge) SetOnToolCall(fn func(toolName string, args string)) {
	b.onToolCall = fn
}

// SetExecutionGate 设置工具执行层 gate。
func (b *ToolBridge) SetExecutionGate(gate ExecutionGate) {
	b.executionGate = gate
}

// CallTool 执行工具,遵守 ToolFilter 限制和权限检查
func (b *ToolBridge) CallTool(ctx context.Context, filter *ToolFilter, perm *PermissionManager, toolName string, input json.RawMessage) (*mcphost.ToolResult, error) {
	// 1. ToolFilter 检查
	if filter != nil {
		if err := filter.CheckAllowed(toolName); err != nil {
			return nil, err
		}
	}

	// 2. 执行层 gate 检查。必须在权限审批前执行，避免 RouteDecision 已拒绝的
	// 操作先触发无意义的 HITL 审批。
	if b.executionGate != nil {
		if err := b.executionGate(ctx, toolName, input); err != nil {
			return nil, err
		}
	}

	// 3. Permission 检查
	if perm != nil {
		if err := perm.CheckPermission(ctx, toolName, input); err != nil {
			return nil, err
		}
	}

	// 4. 插件 ToolExecuteBefore hook
	checkedInput := cloneRawMessage(input)
	if b.pluginMgr != nil {
		hookInput := &plugin.ToolExecuteInput{
			ToolName: toolName,
			Args:     input,
		}
		if err := b.pluginMgr.TriggerToolBefore(ctx, hookInput); err != nil {
			return nil, err
		}
		if hookInput.ToolName != toolName {
			return nil, errs.New(errs.CodePermissionDenied, fmt.Sprintf("插件不允许改写工具名: %s -> %s", toolName, hookInput.ToolName))
		}
		// hook 可能修改了参数
		input = hookInput.Args
	}
	if !sameRawJSON(input, checkedInput) {
		if b.executionGate != nil {
			if err := b.executionGate(ctx, toolName, input); err != nil {
				return nil, err
			}
		}
		if perm != nil {
			if err := perm.CheckPermission(ctx, toolName, input); err != nil {
				return nil, err
			}
		}
	}

	b.logger.Debug("执行工具",
		zap.String("tool", toolName),
	)

	// 触发工具调用回调（主会话直接调用时跳过，由 master.executeTool 自行广播）
	if b.onToolCall != nil && ctx.Value(SkipToolCallCallbackKey{}) == nil {
		b.onToolCall(toolName, string(input))
	}

	// 4. 检查 read_file 缓存
	if toolName == "read_file" && b.fileCache != nil {
		var params struct {
			Path            string `json:"path"`
			Offset          int    `json:"offset"`
			Limit           int    `json:"limit"`
			ShowLineNumbers bool   `json:"show_line_numbers"`
		}
		if json.Unmarshal(input, &params) == nil && params.Path != "" {
			cacheKey := fmt.Sprintf("%s:%d:%d:%t", params.Path, params.Offset, params.Limit, params.ShowLineNumbers)
			if cached, ok := b.fileCache.Get(cacheKey); ok {
				b.logger.Debug("使用文件缓存", zap.String("path", params.Path))
				data, _ := json.Marshal(cached)
				return &mcphost.ToolResult{Content: data, IsError: false}, nil
			}
		}
	}

	// 5. 执行工具（捕获不存在的工具名，返回友好提示而非硬错误）
	start := time.Now()
	result, err := b.host.ExecuteTool(ctx, toolName, input)
	if b.metrics != nil {
		b.metrics.RecordToolCall(toolName, time.Since(start), err)
	}
	if err != nil && errs.GetCode(err) == errs.CodeMCPToolNotFound {
		// 收集可用工具名列表，帮助 LLM 自纠正
		available := b.host.ListTools()
		names := make([]string, 0, len(available))
		for _, t := range available {
			names = append(names, t.Name)
		}
		hint := fmt.Sprintf("工具 %q 不存在。可用工具: %s", toolName, strings.Join(names, ", "))
		b.logger.Warn("LLM 调用了不存在的工具",
			zap.String("tool", toolName),
		)
		data, _ := json.Marshal(hint)
		return &mcphost.ToolResult{
			Content: data,
			IsError: true,
		}, nil
	}

	// 补丁：部分 MCP 服务（如 wenyan-mcp）在抛出异常时会捕获并返回普通内容（而非 isError: true），
	// 但其文本中包含明显的错误标志。拦截并强制标记为错误。
	if err == nil && result != nil && strings.HasPrefix(toolName, "wenyan__") {
		contentStr := mcphost.DecodeToolContent(result.Content)
		// 记录 wenyan 工具调用的完整返回内容，方便排查公众号发布等问题
		b.logger.Debug("wenyan 工具调用结果",
			zap.String("tool", toolName),
			zap.String("input", string(input)),
			zap.Bool("original_is_error", result.IsError),
			zap.String("content", contentStr),
		)
		if !result.IsError {
			if strings.Contains(contentStr, "执行工具失败") || strings.Contains(contentStr, "Failed to") || strings.Contains(contentStr, "Error:") {
				b.logger.Warn("wenyan 工具返回了伪装成功的错误，已强制标记为 isError",
					zap.String("tool", toolName),
					zap.String("content", contentStr),
				)
				result.IsError = true
			}
		}
	}

	// 5. 插件 ToolExecuteAfter hook
	if err == nil && result != nil && b.pluginMgr != nil {
		hookOutput := &plugin.ToolExecuteOutput{
			Output: mcphost.DecodeToolContent(result.Content),
		}
		if afterErr := b.pluginMgr.TriggerToolAfter(ctx, plugin.ToolExecuteInput{
			ToolName: toolName,
			Args:     input,
		}, hookOutput); afterErr != nil {
			b.logger.Warn("插件 ToolExecuteAfter hook 失败", zap.Error(afterErr))
		} else {
			encoded, _ := json.Marshal(hookOutput.Output)
			result.Content = encoded
		}
	}

	// 6. 缓存 read_file 结果
	if err == nil && result != nil && !result.IsError && toolName == "read_file" && b.fileCache != nil {
		var params struct {
			Path            string `json:"path"`
			Offset          int    `json:"offset"`
			Limit           int    `json:"limit"`
			ShowLineNumbers bool   `json:"show_line_numbers"`
		}
		if json.Unmarshal(input, &params) == nil && params.Path != "" {
			if stat, statErr := os.Stat(params.Path); statErr == nil {
				cacheKey := fmt.Sprintf("%s:%d:%d:%t", params.Path, params.Offset, params.Limit, params.ShowLineNumbers)
				b.fileCache.Set(cacheKey, mcphost.DecodeToolContent(result.Content), stat.ModTime())
			}
		}
	}

	return result, err
}

// ExecuteDirect 直接执行工具，跳过 ToolFilter 和权限检查。
// 供 Master.executeTool 使用：Master 自己已完成权限检查和策略过滤，
// 但仍需要 ToolBridge 提供的插件 hooks、read_file 缓存、指标收集和 tool-not-found 友好提示。
func (b *ToolBridge) ExecuteDirect(ctx context.Context, toolName string, input json.RawMessage, execOpts ...DirectExecutionOption) (*mcphost.ToolResult, error) {
	var opts directExecutionOptions
	for _, opt := range execOpts {
		if opt != nil {
			opt(&opts)
		}
	}

	// 1. 插件 ToolExecuteBefore hook
	checkedInput := cloneRawMessage(input)
	if b.pluginMgr != nil {
		hookInput := &plugin.ToolExecuteInput{
			ToolName: toolName,
			Args:     input,
		}
		if err := b.pluginMgr.TriggerToolBefore(ctx, hookInput); err != nil {
			return nil, err
		}
		if hookInput.ToolName != toolName {
			return nil, errs.New(errs.CodePermissionDenied, fmt.Sprintf("插件不允许改写工具名: %s -> %s", toolName, hookInput.ToolName))
		}
		input = hookInput.Args
	}
	if opts.gate != nil && !sameRawJSON(input, checkedInput) {
		if err := opts.gate(ctx, toolName, input); err != nil {
			return nil, err
		}
	}

	// 2. 检查 read_file 缓存
	if toolName == "read_file" && b.fileCache != nil {
		var params struct {
			Path            string `json:"path"`
			Offset          int    `json:"offset"`
			Limit           int    `json:"limit"`
			ShowLineNumbers bool   `json:"show_line_numbers"`
		}
		if json.Unmarshal(input, &params) == nil && params.Path != "" {
			cacheKey := fmt.Sprintf("%s:%d:%d:%t", params.Path, params.Offset, params.Limit, params.ShowLineNumbers)
			if cached, ok := b.fileCache.Get(cacheKey); ok {
				b.logger.Debug("使用文件缓存", zap.String("path", params.Path))
				data, _ := json.Marshal(cached)
				return &mcphost.ToolResult{Content: data, IsError: false}, nil
			}
		}
	}

	// 3. 执行工具（tool-not-found 友好提示）
	start := time.Now()
	result, err := b.host.ExecuteTool(ctx, toolName, input)
	if b.metrics != nil {
		b.metrics.RecordToolCall(toolName, time.Since(start), err)
	}
	if err != nil && errs.GetCode(err) == errs.CodeMCPToolNotFound {
		available := b.host.ListTools()
		names := make([]string, 0, len(available))
		for _, t := range available {
			names = append(names, t.Name)
		}
		hint := fmt.Sprintf("工具 %q 不存在。可用工具: %s", toolName, strings.Join(names, ", "))
		b.logger.Warn("LLM 调用了不存在的工具", zap.String("tool", toolName))
		data, _ := json.Marshal(hint)
		return &mcphost.ToolResult{Content: data, IsError: true}, nil
	}

	// 补丁：拦截类似 wenyan-mcp 的错误包装行为（返回 isError: false，但文本带有明确错误特征）
	if err == nil && result != nil && strings.HasPrefix(toolName, "wenyan__") {
		contentStr := mcphost.DecodeToolContent(result.Content)
		b.logger.Debug("wenyan 工具调用结果",
			zap.String("tool", toolName),
			zap.String("input", string(input)),
			zap.Bool("original_is_error", result.IsError),
			zap.String("content", contentStr),
		)
		if !result.IsError {
			if strings.Contains(contentStr, "执行工具失败") || strings.Contains(contentStr, "Failed to") || strings.Contains(contentStr, "Error:") {
				b.logger.Warn("wenyan 工具返回了伪装成功的错误，已强制标记为 isError",
					zap.String("tool", toolName),
					zap.String("content", contentStr),
				)
				result.IsError = true
			}
		}
	}

	// 4. 插件 ToolExecuteAfter hook
	if err == nil && result != nil && b.pluginMgr != nil {
		hookOutput := &plugin.ToolExecuteOutput{
			Output: mcphost.DecodeToolContent(result.Content),
		}
		if afterErr := b.pluginMgr.TriggerToolAfter(ctx, plugin.ToolExecuteInput{
			ToolName: toolName,
			Args:     input,
		}, hookOutput); afterErr != nil {
			b.logger.Warn("插件 ToolExecuteAfter hook 失败", zap.Error(afterErr))
		} else {
			encoded, _ := json.Marshal(hookOutput.Output)
			result.Content = encoded
		}
	}

	// 5. 缓存 read_file 结果
	if err == nil && result != nil && !result.IsError && toolName == "read_file" && b.fileCache != nil {
		var params struct {
			Path            string `json:"path"`
			Offset          int    `json:"offset"`
			Limit           int    `json:"limit"`
			ShowLineNumbers bool   `json:"show_line_numbers"`
		}
		if json.Unmarshal(input, &params) == nil && params.Path != "" {
			if stat, statErr := os.Stat(params.Path); statErr == nil {
				cacheKey := fmt.Sprintf("%s:%d:%d:%t", params.Path, params.Offset, params.Limit, params.ShowLineNumbers)
				b.fileCache.Set(cacheKey, mcphost.DecodeToolContent(result.Content), stat.ModTime())
			}
		}
	}

	return result, err
}

func cloneRawMessage(in json.RawMessage) json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), in...)
}

func sameRawJSON(left, right json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(left), bytes.TrimSpace(right))
}

// AvailableTools 返回经 ToolFilter 过滤的工具列表
func (b *ToolBridge) AvailableTools(filter *ToolFilter) []mcphost.ToolDefinition {
	all := b.host.ListTools()
	if filter == nil || filter.IsEmpty() {
		return all
	}
	return filter.FilterTools(all)
}
