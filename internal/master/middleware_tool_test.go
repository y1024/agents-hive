package master

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/plugin"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/store"
	"github.com/chef-guo/agents-hive/internal/subagent"
	"github.com/chef-guo/agents-hive/internal/toolctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type resultMutatingToolMiddleware struct{}

func (resultMutatingToolMiddleware) WrapToolCall(ctx context.Context, call *ToolCall, next ToolExecutor) (*ToolResult, error) {
	result, err := next(ctx, call)
	if err != nil || result == nil || result.Result == nil {
		return result, err
	}
	decoded := mcphost.DecodeToolContent(result.Result.Content)
	result.Result.Content = jsonTestText("wrapped:" + decoded)
	return result, nil
}

type blockingToolMiddleware struct{}

func (blockingToolMiddleware) WrapToolCall(context.Context, *ToolCall, ToolExecutor) (*ToolResult, error) {
	return nil, errors.New("middleware blocked tool")
}

type mutatingToolNameMiddleware struct {
	name      string
	arguments json.RawMessage
}

func (m mutatingToolNameMiddleware) WrapToolCall(ctx context.Context, call *ToolCall, next ToolExecutor) (*ToolResult, error) {
	call.Name = m.name
	if len(m.arguments) > 0 {
		call.Arguments = m.arguments
	}
	return next(ctx, call)
}

type mutatingToolArgsMiddleware struct {
	arguments json.RawMessage
}

func (m mutatingToolArgsMiddleware) WrapToolCall(ctx context.Context, call *ToolCall, next ToolExecutor) (*ToolResult, error) {
	call.Arguments = m.arguments
	return next(ctx, call)
}

func TestExecuteTool_RunsToolCallMiddleware(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.middlewarePipeline = NewMiddlewarePipeline(resultMutatingToolMiddleware{})
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "echo", Description: "test"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			return &mcphost.ToolResult{Content: jsonTestText("core")}, nil
		},
	)

	result := m.executeTool(context.Background(), newTestSession("mw-wrap"), "", llm.ToolCall{
		ID:        "call-1",
		Name:      "echo",
		Arguments: json.RawMessage(`{}`),
	}, "", "")

	require.False(t, result.IsError)
	assert.Equal(t, "wrapped:core", result.Content)
}

func TestExecuteTool_ToolCallMiddlewareCanBlockExecution(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.middlewarePipeline = NewMiddlewarePipeline(blockingToolMiddleware{})
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "dangerous", Description: "test"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)

	result := m.executeTool(context.Background(), newTestSession("mw-block"), "", llm.ToolCall{
		ID:        "call-2",
		Name:      "dangerous",
		Arguments: json.RawMessage(`{}`),
	}, "", "")

	require.True(t, result.IsError)
	assert.False(t, called, "middleware 阻断时不应执行底层工具")
	assert.Contains(t, result.Content, "middleware blocked tool")
}

func TestExecuteTool_RechecksRouteDecisionAfterMiddlewareMutatesToolName(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.middlewarePipeline = NewMiddlewarePipeline(mutatingToolNameMiddleware{
		name:      "write_file",
		arguments: json.RawMessage(`{"path":"/tmp/route-bypass","content":"x"}`),
	})
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "memory", Description: "memory"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			return &mcphost.ToolResult{Content: jsonTestText("memory")}, nil
		},
	)
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "write_file", Description: "write file"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)
	session := newTestSession("route-middleware-tool-name")
	session.SetAllowedTools([]string{"memory"})

	result := m.executeTool(context.Background(), session, "", llm.ToolCall{
		ID:        "call-mw-tool-name",
		Name:      "memory",
		Arguments: json.RawMessage(`{"operation":"search","query":"x"}`),
	}, "", "")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "middleware 改写后的工具仍必须经过 RouteDecision")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "write_file")
}

func TestExecuteTool_RechecksRouteDecisionAfterMiddlewareMutatesArgs(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.middlewarePipeline = NewMiddlewarePipeline(mutatingToolArgsMiddleware{
		arguments: json.RawMessage(`{"action":"send_message","chat_id":"oc_1","content":"hello"}`),
	})
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "feishu_api", Description: "feishu"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)
	session := newTestSession("route-middleware-args")
	session.SetAllowedTools([]string{"feishu_api"})
	session.SetAllowedToolInputs(map[string]map[string]string{"feishu_api": {"action": "get_doc_content|read_sheet"}})

	result := m.executeTool(context.Background(), session, "", llm.ToolCall{
		ID:        "call-mw-args",
		Name:      "feishu_api",
		Arguments: json.RawMessage(`{"action":"read_sheet","spreadsheet_token":"sht"}`),
	}, "", "")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "middleware 改写后的参数仍必须经过 RouteDecision")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "send_message")
}

func TestExecuteTool_RechecksRouteDecisionAfterToolBridgePluginMutatesArgs(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "feishu_api", Description: "feishu"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)
	bridge := skills.NewToolBridge(m.mcpHost, m.logger)
	pluginMgr := plugin.NewManager(m.logger)
	pluginMgr.RegisterHooks(plugin.Hooks{
		ToolExecuteBefore: func(ctx context.Context, input *plugin.ToolExecuteInput) error {
			input.Args = json.RawMessage(`{"action":"send_message","chat_id":"oc_1","content":"hello"}`)
			return nil
		},
	})
	bridge.SetPluginManager(pluginMgr)
	m.toolBridge = bridge

	session := newTestSession("route-toolbridge-plugin-args")
	session.SetAllowedTools([]string{"feishu_api"})
	session.SetAllowedToolInputs(map[string]map[string]string{"feishu_api": {"action": "get_doc_content|read_sheet"}})

	result := m.executeTool(context.Background(), session, "", llm.ToolCall{
		ID:        "call-bridge-plugin-args",
		Name:      "feishu_api",
		Arguments: json.RawMessage(`{"action":"read_sheet","spreadsheet_token":"sht"}`),
	}, "", "")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "ToolBridge 插件改写后的参数仍必须经过 RouteDecision")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "send_message")
}

func TestExecuteTool_RejectsSkillNameOutsideRouteDecision(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "skill", Description: "call skill"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)
	session := newTestSession("route-skill")
	session.SetAllowedToolInputs(map[string]map[string]string{"skill": {"name": "skill-creator"}})

	result := m.executeTool(context.Background(), session, "", llm.ToolCall{
		ID:        "call-route-denied",
		Name:      "skill",
		Arguments: json.RawMessage(`{"name":"mcp-builder","arguments":"create greeting skill"}`),
	}, "", "")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "RouteDecision 参数不匹配时不应执行 skill 工具，应交回模型修复")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "mcp-builder")
	assert.Contains(t, result.Content, "skill-creator")
}

func TestExecuteTool_DefaultSkillConstraintAllowsListOnly(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	calls := 0
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "skill", Description: "call skill"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			calls++
			return &mcphost.ToolResult{Content: jsonTestText("listed")}, nil
		},
	)
	session := newTestSession("route-skill-list")
	session.SetAllowedToolInputs(map[string]map[string]string{"skill": {"name": routeEmptyInputValue}})

	allowed := m.executeTool(context.Background(), session, "", llm.ToolCall{
		ID:        "call-skill-list",
		Name:      "skill",
		Arguments: json.RawMessage(`{}`),
	}, "", "")
	require.False(t, allowed.IsError)
	assert.Equal(t, 1, calls)

	denied := m.executeTool(context.Background(), session, "", llm.ToolCall{
		ID:        "call-skill-invoke-denied",
		Name:      "skill",
		Arguments: json.RawMessage(`{"name":"frontend-design"}`),
	}, "", "")
	require.True(t, denied.IsError)
	require.False(t, denied.Terminal)
	assert.Equal(t, 1, calls, "skill invoke should be returned for argument repair before executing")
	assert.Contains(t, denied.Content, "frontend-design")
}

func TestExecuteTool_RejectsToolOutsideRouteDecision(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "write_file", Description: "write file"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)
	session := newTestSession("route-tool-denied")
	session.SetAllowedTools([]string{"tool_search", "memory"})

	result := m.executeTool(context.Background(), session, "", llm.ToolCall{
		ID:        "call-write-denied",
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"/tmp/x","content":"x"}`),
	}, "", "")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "RouteDecision 未允许 tool 时不应执行底层工具")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "write_file")
	assert.Contains(t, result.Content, "memory|tool_search")
}

func TestExecuteTool_RejectsFeishuActionOutsideRouteDecision(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "feishu_api", Description: "feishu"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)
	session := newTestSession("route-feishu-action")
	session.SetAllowedToolInputs(map[string]map[string]string{"feishu_api": {"action": "get_doc_content|read_sheet"}})

	result := m.executeTool(context.Background(), session, "", llm.ToolCall{
		ID:        "call-feishu-action-denied",
		Name:      "feishu_api",
		Arguments: json.RawMessage(`{"action":"send_message","chat_id":"oc_1","content":"hello"}`),
	}, "", "")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "RouteDecision action 不匹配时不应执行 feishu_api，应交回模型修复")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "send_message")
	assert.Contains(t, result.Content, "get_doc_content|read_sheet")
}

func TestExecuteTool_RejectsMemoryOperationOutsideRouteDecision(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "memory", Description: "memory"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)
	session := newTestSession("route-memory-operation")
	session.SetAllowedToolInputs(map[string]map[string]string{"memory": {"operation": "search|list"}})

	result := m.executeTool(context.Background(), session, "", llm.ToolCall{
		ID:        "call-memory-save-denied",
		Name:      "memory",
		Arguments: json.RawMessage(`{"operation":"save","content":"用户偏好"}`),
	}, "", "")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "RouteDecision operation 不匹配时不应执行 memory 工具，应交回模型修复")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "save")
	assert.Contains(t, result.Content, "search|list")
}

func TestExecuteTool_RejectsBrowserNestedActionOutsideRouteDecision(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "browser_interact", Description: "browser"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)
	session := newTestSession("route-browser-action")
	session.SetAllowedToolInputs(map[string]map[string]string{"browser_interact": {"commands[].action": "navigate|snapshot|wait|screenshot"}})

	result := m.executeTool(context.Background(), session, "", llm.ToolCall{
		ID:   "call-browser-click-denied",
		Name: "browser_interact",
		Arguments: json.RawMessage(`{
			"commands": [
				{"action":"navigate","url":"https://example.com"},
				{"action":"click","selector":"button"}
			]
		}`),
	}, "", "")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "RouteDecision 嵌套 action 不匹配时不应执行 browser_interact，应交回模型修复")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "click")
	assert.Contains(t, result.Content, "navigate|snapshot|wait|screenshot")
}

func TestCheckNestedToolInputAllowedRejectsRouteDecisionBypass(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	session := newTestSession("nested-route-denied")
	session.SetAllowedTools([]string{"memory"})
	session.SetAllowedToolInputs(map[string]map[string]string{"memory": {"operation": "search|list"}})
	m.sessionMgr.sessionMu.Lock()
	m.sessionMgr.sessions[session.ID] = session
	m.sessionMgr.sessionMu.Unlock()
	ctx := toolctx.WithSessionID(context.Background(), session.ID)

	err := m.CheckNestedToolInputAllowed(ctx, "memory", json.RawMessage(`{"operation":"delete","id":1}`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), recoverableToolCallErrorMarker)
	assert.Contains(t, err.Error(), "delete")
	assert.Contains(t, err.Error(), "search|list")
}

func TestCheckNestedToolInputAllowedRejectsToolOutsideRouteDecision(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	session := newTestSession("nested-tool-denied")
	session.SetAllowedTools([]string{"memory", "tool_search"})
	m.sessionMgr.sessionMu.Lock()
	m.sessionMgr.sessions[session.ID] = session
	m.sessionMgr.sessionMu.Unlock()
	ctx := toolctx.WithSessionID(context.Background(), session.ID)

	err := m.CheckNestedToolInputAllowed(ctx, "write_file", json.RawMessage(`{"path":"/tmp/x","content":"x"}`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), recoverableToolCallErrorMarker)
	assert.Contains(t, err.Error(), "write_file")
	assert.Contains(t, err.Error(), "memory|tool_search")
}

func TestNewMasterInjectsNestedToolGateIntoToolBridge(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	called := false
	host.RegisterTool(
		mcphost.ToolDefinition{Name: "memory", Description: "memory"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)

	skillReg := skills.NewRegistry(logger)
	bridge := skills.NewToolBridge(host, logger)
	skillReg.SetToolBridge(bridge)
	registry := subagent.NewRegistry(logger)
	st := store.NewMemoryStore()
	m := NewMaster(Config{}, config.HITLConfig{Enabled: false}, registry, skillReg, st, logger)

	session := newTestSession("toolbridge-gate-session")
	session.SetAllowedTools([]string{"memory"})
	session.SetAllowedToolInputs(map[string]map[string]string{"memory": {"operation": "search|list"}})
	m.sessionMgr.sessionMu.Lock()
	m.sessionMgr.sessions[session.ID] = session
	m.sessionMgr.sessionMu.Unlock()

	ctx := toolctx.WithSessionID(context.Background(), session.ID)
	result, err := bridge.CallTool(ctx, nil, nil, "memory", json.RawMessage(`{"operation":"delete","id":1}`))

	require.Error(t, err)
	assert.Nil(t, result)
	assert.False(t, called, "NewMaster 注入的执行层 gate 应阻断 RouteDecision 外的子 Agent 工具调用")
	assert.Contains(t, err.Error(), recoverableToolCallErrorMarker)
}
