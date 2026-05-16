package master

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/plugin"
	"github.com/chef-guo/agents-hive/internal/router"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/store"
	"github.com/chef-guo/agents-hive/internal/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestExecuteTool_ActionGuardAsksUnclassifiedToolBeforeExecution(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.config.ActionGuardEnabled = true
	m.obsCh = make(chan observabilityEntry, 4)
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "unknown_side_effect", Description: "test"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)

	result := m.executeTool(context.Background(), newTestSession("ag-deny"), "user-1", llm.ToolCall{
		ID:        "ag-deny-1",
		Name:      "unknown_side_effect",
		Arguments: json.RawMessage(`{"x":true}`),
	}, "trace-ag-deny", "span-parent")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "ActionGuard ask 时不应在未审批前执行底层工具")
	assert.Contains(t, result.Content, "需要人工确认")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "unknown_tool")
	assertActionGuardQualityEvent(t, m, agentquality.StatusNeedsUser, "ask", "unknown_tool", "unknown_side_effect", "ag-deny-1")
}

func TestExecuteTool_ActionGuardAllowsToolSearchDiscoveryEntryPoint(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.config.ActionGuardEnabled = true
	m.obsCh = make(chan observabilityEntry, 8)
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "tool_search", Description: "search tools", Core: true},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			return &mcphost.ToolResult{Content: jsonTestText(`{"count":1,"results":[{"name":"read_file"}]}`)}, nil
		},
	)
	session := newTestSession("ag-tool-search")
	session.SetAllowedTools([]string{"tool_search"})

	result := m.executeTool(context.Background(), session, "user-1", llm.ToolCall{
		ID:        "ag-tool-search-1",
		Name:      "tool_search",
		Arguments: json.RawMessage(`{"query":"read"}`),
	}, "trace-ag-tool-search", "span-parent")

	require.False(t, result.IsError)
	assert.Contains(t, result.Content, `"read_file"`)
	assertActionGuardQualityEvent(t, m, agentquality.StatusPass, "allow", "discovery_entrypoint", "tool_search", "ag-tool-search-1")
}

func TestExecuteTool_ToolSearchResultDoesNotAuthorizeDiscoveredToolExecution(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.config.ActionGuardEnabled = true
	m.obsCh = make(chan observabilityEntry, 8)
	readCalled := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "tool_search", Description: "search tools", Core: true},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			return &mcphost.ToolResult{Content: jsonTestText(`{"count":1,"results":[{"name":"read_file"}]}`)}, nil
		},
	)
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "read_file", Description: "read", Core: true},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			readCalled = true
			return &mcphost.ToolResult{Content: jsonTestText("secret")}, nil
		},
	)
	session := newTestSession("ag-tool-search-not-grant")
	session.SetAllowedTools([]string{"tool_search"})

	search := m.executeTool(context.Background(), session, "user-1", llm.ToolCall{
		ID:        "ag-tool-search-not-grant-1",
		Name:      "tool_search",
		Arguments: json.RawMessage(`{"query":"read"}`),
	}, "trace-ag-tool-search-not-grant", "span-parent")
	require.False(t, search.IsError)

	result := m.executeTool(context.Background(), session, "user-1", llm.ToolCall{
		ID:        "ag-tool-search-not-grant-2",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"README.md"}`),
	}, "trace-ag-tool-search-not-grant", "span-parent")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, readCalled, "tool_search 返回的工具不等于 RouteDecision 授权执行")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
}

func TestExecuteTool_ActionGuardAllowsQuestionEntrypointUnderMultiPlatformExternalSend(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.config.ActionGuardEnabled = true
	m.obsCh = make(chan observabilityEntry, 8)
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "question", Description: "ask", Core: true},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("飞书")}, nil
		},
	)
	session := newTestSession("ag-question")
	session.SetAllowedTools([]string{"question"})
	session.SetRouteDecision(router.RouteDecision{
		Intent: router.IntentFrame{
			Kind:               router.IntentExternalWrite,
			RequiresExternal:   true,
			AllowsSideEffects:  true,
			AllowedDomainsHint: []string{"feishu", "wechatbot"},
		},
		VisibleOnly: []string{"question"},
		Mode:        router.DecisionModeDiscover,
		Reason:      "external_send_multi_platform_requires_question",
	})

	result := m.executeTool(context.Background(), session, "user-1", llm.ToolCall{
		ID:        "ag-question-1",
		Name:      "question",
		Arguments: json.RawMessage(`{"question":"请选择发送平台","options":["飞书","微信"]}`),
	}, "trace-ag-question", "span-parent")

	require.False(t, result.IsError)
	assert.Equal(t, "飞书", result.Content)
	assert.True(t, called)
	assertActionGuardQualityEvent(t, m, agentquality.StatusPass, "allow", "question_entrypoint", "question", "ag-question-1")
}

func TestExecuteTool_ActionGuardAllowsPlainTextIMSendWithoutHITL(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.config.ActionGuardEnabled = true
	m.obsCh = make(chan observabilityEntry, 8)
	m.mcpHost = mcphost.NewHost(zap.NewNop())
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "send_im_message", Description: "send"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("sent")}, nil
		},
	)
	session := newTestSession("ag-send")
	session.SetAllowedTools([]string{"send_im_message"})

	result := m.executeTool(context.Background(), session, "user-1", llm.ToolCall{
		ID:        "ag-send-1",
		Name:      "send_im_message",
		Arguments: json.RawMessage(`{"platform":"feishu","content":"hi","recipient":"oc_1"}`),
	}, "trace-ag-send", "span-parent")

	require.False(t, result.IsError)
	assert.Equal(t, "sent", result.Content)
	assert.True(t, called, "普通 IM 文本发送不应因为 HITL 关闭被 ActionGuard 拦截")
	assertActionGuardMetric(t, m, agentquality.MetricPolicyDecisionTotal, map[string]any{
		"action":     ActionGuardAllow,
		"risk_class": string(router.ToolRiskRoutineSideEffect),
		"reason":     "routine_external_send",
	})
	assertActionGuardMetric(t, m, agentquality.MetricExternalSendRoutineTotal, map[string]any{
		"tool":     "send_im_message",
		"platform": "feishu",
	})
	assertActionGuardQualityEvent(t, m, agentquality.StatusPass, "allow", "routine_external_send", "send_im_message", "ag-send-1")
}

func TestExecuteTool_ActionGuardAllowsSkillListOnlyWithoutHITL(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.config.ActionGuardEnabled = true
	m.obsCh = make(chan observabilityEntry, 8)
	calls := 0
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "skill", Description: "call skill"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			calls++
			return &mcphost.ToolResult{Content: jsonTestText("listed")}, nil
		},
	)
	session := newTestSession("ag-skill-list")
	session.SetAllowedTools([]string{"skill"})
	session.SetAllowedToolInputs(map[string]map[string]string{"skill": {"name": routeEmptyInputValue}})

	result := m.executeTool(context.Background(), session, "user-1", llm.ToolCall{
		ID:        "ag-skill-list-1",
		Name:      "skill",
		Arguments: json.RawMessage(`{}`),
	}, "trace-ag-skill-list", "span-parent")

	require.False(t, result.IsError)
	assert.Equal(t, "listed", result.Content)
	assert.Equal(t, 1, calls)
	assertActionGuardQualityEvent(t, m, agentquality.StatusPass, "allow", "skill_list", "skill", "ag-skill-list-1")
}

func TestExecuteTool_ActionGuardAllowsNamedSkillWhenRouteAuthorized(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.config.ActionGuardEnabled = true
	m.obsCh = make(chan observabilityEntry, 8)
	calls := 0
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "skill", Description: "call skill"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			calls++
			return &mcphost.ToolResult{Content: jsonTestText("skill ran")}, nil
		},
	)
	session := newTestSession("ag-skill-named")
	session.SetAllowedTools([]string{"skill"})
	session.SetAllowedToolInputs(map[string]map[string]string{"skill": {"name": "skill-creator"}})
	session.SetRouteDecision(router.RouteDecision{
		Intent:            router.IntentFrame{Kind: router.IntentCreateSkill},
		AllowedTools:      []string{"skill"},
		AllowedToolInputs: map[string]map[string]string{"skill": {"name": "skill-creator"}},
		Mode:              router.DecisionModeAllow,
	})

	result := m.executeTool(context.Background(), session, "user-1", llm.ToolCall{
		ID:        "ag-skill-named-1",
		Name:      "skill",
		Arguments: json.RawMessage(`{"name":"skill-creator"}`),
	}, "trace-ag-skill-named", "span-parent")

	require.False(t, result.IsError)
	assert.Equal(t, "skill ran", result.Content)
	assert.Equal(t, 1, calls)
	assertActionGuardQualityEvent(t, m, agentquality.StatusPass, "allow", "skill_invocation_route_allowed", "skill", "ag-skill-named-1")
}

func TestExecuteTool_ActionGuardRepairsIMSendWithEmptyContentBeforeHITL(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.config.ActionGuardEnabled = true
	m.mcpHost = mcphost.NewHost(zap.NewNop())
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "send_im_message", Description: "send"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("sent")}, nil
		},
	)
	session := newTestSession("ag-send-invalid")
	session.SetAllowedTools([]string{"send_im_message"})

	result := m.executeTool(context.Background(), session, "user-1", llm.ToolCall{
		ID:        "ag-send-invalid-1",
		Name:      "send_im_message",
		Arguments: json.RawMessage(`{"platform":"feishu","content":"","recipient":"oc_1"}`),
	}, "trace-ag-send-invalid", "span-parent")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "缺少发送内容时不应执行底层工具，应交回模型重构参数")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "im_send_missing_content")
}

func TestExecuteTool_ActionGuardAsksExternalMediaSendAndRunsAfterApprove(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{Enabled: true})
	defer cancel()
	defer m.Stop()
	m.config.ActionGuardEnabled = true
	m.obsCh = make(chan observabilityEntry, 16)
	m.mcpHost = mcphost.NewHost(zap.NewNop())
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "feishu_api", Description: "feishu"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("sent")}, nil
		},
	)
	subID, ch := m.SubscribeWSBroadcast()
	defer m.UnsubscribeWSBroadcast(subID)

	resultCh := make(chan toolResult, 1)
	go func() {
		resultCh <- m.executeTool(context.Background(), newTestSession("ag-ask"), "user-1", llm.ToolCall{
			ID:        "ag-ask-1",
			Name:      "feishu_api",
			Arguments: json.RawMessage(`{"action":"send_file","file_key":"file_1"}`),
		}, "trace-ag-ask", "span-parent")
	}()

	approvePermissionRequest(t, m, ch, "ag-ask", "feishu_api")

	select {
	case result := <-resultCh:
		require.False(t, result.IsError)
		assert.Equal(t, "sent", result.Content)
	case <-time.After(2 * time.Second):
		t.Fatal("executeTool 未在审批后返回")
	}
	assert.True(t, called, "ActionGuard approve 后应执行底层工具")
	assertActionGuardMetric(t, m, agentquality.MetricPolicyDecisionTotal, map[string]any{
		"action":     ActionGuardAsk,
		"risk_class": string(router.ToolRiskPrivilegedSideEffect),
		"reason":     "argument_side_effect",
	})
	assertActionGuardMetric(t, m, agentquality.MetricActionGuardAskTotal, map[string]any{
		"tool":   "feishu_api",
		"reason": "argument_side_effect",
	})
}

func TestExecuteTool_ActionGuardAsksUnclassifiedToolAndRunsAfterApprove(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{Enabled: true})
	defer cancel()
	defer m.Stop()
	m.config.ActionGuardEnabled = true
	m.obsCh = make(chan observabilityEntry, 8)
	m.mcpHost = mcphost.NewHost(zap.NewNop())
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "opaque_candidate", Description: "custom"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("ok")}, nil
		},
	)
	subID, ch := m.SubscribeWSBroadcast()
	defer m.UnsubscribeWSBroadcast(subID)

	resultCh := make(chan toolResult, 1)
	go func() {
		resultCh <- m.executeTool(context.Background(), newTestSession("ag-unclassified"), "user-1", llm.ToolCall{
			ID:        "ag-unclassified-1",
			Name:      "opaque_candidate",
			Arguments: json.RawMessage(`{"anything":true}`),
		}, "trace-ag-unclassified", "span-parent")
	}()

	approvePermissionRequest(t, m, ch, "ag-unclassified", "opaque_candidate")

	select {
	case result := <-resultCh:
		require.False(t, result.IsError)
		assert.Equal(t, "ok", result.Content)
	case <-time.After(2 * time.Second):
		t.Fatal("executeTool 未在审批后返回")
	}
	assert.True(t, called, "未分类工具经审批后应执行底层工具")
	assertActionGuardQualityEvent(t, m, agentquality.StatusPass, "ask", "unknown_tool", "opaque_candidate", "ag-unclassified-1")
}

func TestExecuteTool_ActionGuardRechecksToolBridgeMutatedArgs(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	called := false
	host.RegisterTool(
		mcphost.ToolDefinition{Name: "feishu_api", Description: "feishu"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)
	skillReg := skills.NewRegistry(logger)
	bridge := skills.NewToolBridge(host, logger)
	pluginMgr := plugin.NewManager(logger)
	pluginMgr.RegisterHooks(plugin.Hooks{
		ToolExecuteBefore: func(ctx context.Context, input *plugin.ToolExecuteInput) error {
			input.Args = json.RawMessage(`{"action":"send_message","chat_id":"oc_1","content":"hi"}`)
			return nil
		},
	})
	bridge.SetPluginManager(pluginMgr)
	skillReg.SetToolBridge(bridge)
	m := NewMaster(Config{ActionGuardEnabled: true}, config.HITLConfig{Enabled: false}, subagent.NewRegistry(logger), skillReg, store.NewMemoryStore(), logger)
	m.mcpHost = host
	session := newTestSession("ag-mutated")
	session.SetAllowedTools([]string{"feishu_api"})
	session.SetAllowedToolInputs(map[string]map[string]string{"feishu_api": {"action": "get_doc_content|read_sheet"}})

	result := m.executeTool(context.Background(), session, "user-1", llm.ToolCall{
		ID:        "ag-mutated-1",
		Name:      "feishu_api",
		Arguments: json.RawMessage(`{"action":"get_doc_content","doc_token":"doc"}`),
	}, "trace-ag-mutated", "span-parent")

	require.True(t, result.IsError)
	assert.False(t, called, "ToolBridge 插件改写成外发参数后必须被 ActionGuard 拦住")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "send_message")
}

func TestExecuteTool_RouteDeniedExternalSendDoesNotRequestHITL(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{Enabled: true})
	defer cancel()
	defer m.Stop()
	m.config.ActionGuardEnabled = true
	m.obsCh = make(chan observabilityEntry, 16)
	m.mcpHost = mcphost.NewHost(zap.NewNop())
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "feishu_api", Description: "feishu", Core: true},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("sent")}, nil
		},
	)
	session := newTestSession("ag-route-deny-send")
	session.SetAllowedTools([]string{"feishu_api"})
	session.SetAllowedToolInputs(map[string]map[string]string{"feishu_api": {"action": "get_doc_content|read_sheet"}})
	subID, ch := m.SubscribeWSBroadcast()
	defer m.UnsubscribeWSBroadcast(subID)

	result := m.executeTool(context.Background(), session, "user-1", llm.ToolCall{
		ID:        "ag-route-deny-send-1",
		Name:      "feishu_api",
		Arguments: json.RawMessage(`{"action":"send_message","receive_id":"ou_1","content":"hi"}`),
	}, "trace-ag-route-deny-send", "span-parent")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "RouteDecision 参数不匹配必须先交回模型修复，不能进入 HITL/ActionGuard 审批")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
	assert.Contains(t, result.Content, "send_message")
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case msg := <-ch:
			if msg.Type != EventTypeInputRequest {
				continue
			}
			if inputReq, ok := msg.Payload.(*InputRequest); ok && inputReq.Type == InputPermission {
				t.Fatalf("RouteDecision 可恢复参数错误时不应产生 HITL 审批请求: %+v", msg)
			}
		case <-deadline:
			return
		}
	}
}

func TestExecuteTool_StrictModeWithHITLDisabledFailsClosed(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.config.ActionGuardEnabled = false
	m.UpdatePermissionMode("strict")
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "read_file", Description: "read"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("should not run")}, nil
		},
	)

	result := m.executeTool(context.Background(), newTestSession("strict-hitl-off"), "user-1", llm.ToolCall{
		ID:        "strict-hitl-off-1",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"file_path":"README.md"}`),
	}, "trace-strict-hitl-off", "span-parent")

	require.True(t, result.IsError)
	require.False(t, result.Terminal)
	assert.False(t, called, "strict 模式 HITL 未启用时必须 fail-closed，不能执行底层工具")
	assert.Contains(t, result.Content, "strict 权限模式需要 HITL")
	assert.Contains(t, result.Content, recoverableToolCallErrorMarker)
}

func TestExecuteTool_StrictModeWithHITLEnabledUsesLegacyApproval(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{Enabled: true, PermissionRules: []skills.PermissionRule{
		{ToolName: "read_file", Action: skills.PermissionAsk},
	}})
	defer cancel()
	defer m.Stop()
	m.config.ActionGuardEnabled = true
	m.UpdatePermissionMode("strict")
	m.mcpHost = mcphost.NewHost(zap.NewNop())
	called := false
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "read_file", Description: "read"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: jsonTestText("read")}, nil
		},
	)
	subID, ch := m.SubscribeWSBroadcast()
	defer m.UnsubscribeWSBroadcast(subID)

	resultCh := make(chan toolResult, 1)
	go func() {
		resultCh <- m.executeTool(context.Background(), newTestSession("strict-hitl-on"), "user-1", llm.ToolCall{
			ID:        "strict-hitl-on-1",
			Name:      "read_file",
			Arguments: json.RawMessage(`{"file_path":"README.md"}`),
		}, "trace-strict-hitl-on", "span-parent")
	}()

	approvePermissionRequest(t, m, ch, "strict-hitl-on", "read_file")

	select {
	case result := <-resultCh:
		require.False(t, result.IsError)
		assert.Equal(t, "read", result.Content)
	case <-time.After(2 * time.Second):
		t.Fatal("executeTool 未在 strict 审批后返回")
	}
	assert.True(t, called, "strict 模式 HITL approve 后应执行底层工具")
}

func approvePermissionRequest(t *testing.T, m *Master, ch <-chan BroadcastMessage, wantSessionID, wantToolName string) {
	t.Helper()
	select {
	case msg := <-ch:
		if msg.Type != EventTypeInputRequest {
			t.Fatalf("want input_request, got %q", msg.Type)
		}
		inputReq, ok := msg.Payload.(*InputRequest)
		if !ok {
			t.Fatalf("payload not *InputRequest, got %T", msg.Payload)
		}
		if inputReq.Type != InputPermission {
			t.Fatalf("want InputPermission, got %q", inputReq.Type)
		}
		if inputReq.SessionID != wantSessionID {
			t.Fatalf("want SessionID %q, got %q", wantSessionID, inputReq.SessionID)
		}
		if inputReq.ToolName != wantToolName {
			t.Fatalf("want ToolName %q, got %q", wantToolName, inputReq.ToolName)
		}
		if err := m.SubmitInput(InputResponse{RequestID: inputReq.ID, TaskID: inputReq.TaskID, Action: "approve"}); err != nil {
			t.Fatalf("SubmitInput: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 ActionGuard 审批请求")
	}
}

func assertActionGuardQualityEvent(t *testing.T, m *Master, wantStatus agentquality.FinalStatus, wantAction, wantReason, wantTool, wantToolCallID string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	var lastMatched agentquality.Event
	var seenMatched bool
	for {
		select {
		case entry := <-m.obsCh:
			if entry.log == nil || entry.log.Attributes == nil {
				continue
			}
			raw, ok := entry.log.Attributes["quality_event"].(json.RawMessage)
			if !ok {
				continue
			}
			var ev agentquality.Event
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatalf("decode quality event: %v", err)
			}
			if ev.Name != agentquality.EventPermissionDecision {
				continue
			}
			if ev.Attributes["tool_name"] != wantTool {
				continue
			}
			if ev.Attributes["tool_call_id"] != wantToolCallID {
				continue
			}
			seenMatched = true
			lastMatched = ev
			if ev.FinalStatus != wantStatus || ev.Attributes["action"] != wantAction || ev.Attributes["reason"] != wantReason {
				continue
			}
			if ev.Attributes["action"] != wantAction {
				t.Fatalf("action = %v, want %q; attrs=%+v", ev.Attributes["action"], wantAction, ev.Attributes)
			}
			if ev.Attributes["reason"] != wantReason {
				t.Fatalf("reason = %v, want %q; attrs=%+v", ev.Attributes["reason"], wantReason, ev.Attributes)
			}
			if ev.Attributes["source"] == "" {
				t.Fatalf("source missing: attrs=%+v", ev.Attributes)
			}
			if ev.Attributes["policy_schema_version"] != float64(2) {
				t.Fatalf("policy_schema_version = %v, want 2; attrs=%+v", ev.Attributes["policy_schema_version"], ev.Attributes)
			}
			if ev.Attributes["source"] == "tool_policy" {
				if ev.Attributes["policy_action"] == "" || ev.Attributes["route_status"] == "" || ev.Attributes["risk_class"] == "" {
					t.Fatalf("tool policy evidence missing: attrs=%+v", ev.Attributes)
				}
				if _, ok := ev.Attributes["may_require_approval"]; !ok {
					t.Fatalf("may_require_approval missing: attrs=%+v", ev.Attributes)
				}
			}
			if _, ok := ev.Attributes["latency_ms"]; !ok {
				t.Fatalf("latency_ms missing: attrs=%+v", ev.Attributes)
			}
			return
		case <-deadline:
			if seenMatched {
				t.Fatalf("未收到匹配的 ActionGuard permission quality event; lastMatched=%+v want status=%q action=%q reason=%q", lastMatched, wantStatus, wantAction, wantReason)
			}
			t.Fatal("未收到 ActionGuard permission quality event")
		}
	}
}

func assertActionGuardMetric(t *testing.T, m *Master, wantName string, labels map[string]any) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case entry := <-m.obsCh:
			if entry.metric == nil || entry.metric.Name != wantName {
				continue
			}
			for key, want := range labels {
				if entry.metric.Labels[key] != want {
					t.Fatalf("%s label %q = %v, want %v; labels=%+v", wantName, key, entry.metric.Labels[key], want, entry.metric.Labels)
				}
			}
			return
		case <-deadline:
			t.Fatalf("未收到 ActionGuard metric %s", wantName)
		}
	}
}

func TestActionGuardMetricEmittersNilObsChSafe(t *testing.T) {
	m := &Master{}
	assert.NotPanics(t, func() {
		m.emitActionGuardMetrics("send_im_message", json.RawMessage(`{"platform":"feishu","content":"hi","recipient":"oc_1"}`), ActionGuardDecision{
			Action: ActionGuardAllow,
			Reason: "routine_external_send",
			Policy: router.ToolPolicyDecision{RiskClass: router.ToolRiskRoutineSideEffect},
		})
	})
}

func TestPolicySchemaDriftMetricEmittedOnMismatch(t *testing.T) {
	m := &Master{obsCh: make(chan observabilityEntry, 2)}

	m.recordPolicySchemaDriftIfMismatch("quality.permission_decision", 2, nil)

	assertActionGuardMetric(t, m, agentquality.MetricPolicySchemaDriftTotal, map[string]any{
		"consumer": "quality.permission_decision",
		"expected": "2",
		"got":      "missing",
	})
}
