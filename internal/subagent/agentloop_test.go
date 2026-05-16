package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/plugin"
	"github.com/chef-guo/agents-hive/internal/router"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/toolctx"
)

// mockLLMClient 模拟 LLM client,可预设响应序列
type mockLLMClient struct {
	responses []*llm.ChatWithToolsResponse
	callIndex int
	err       error
}

func (m *mockLLMClient) ChatWithTools(ctx context.Context, req llm.ChatWithToolsRequest) (*llm.ChatWithToolsResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.callIndex >= len(m.responses) {
		return nil, errors.New("unexpected ChatWithTools call")
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

func (m *mockLLMClient) ChatWithToolsStream(ctx context.Context, req llm.ChatWithToolsRequest, onChunk llm.StreamCallback) (*llm.ChatWithToolsResponse, error) {
	resp, err := m.ChatWithTools(ctx, req)
	if err != nil {
		return nil, err
	}
	// 模拟流式：发送最终内容后标记完成
	if onChunk != nil {
		if resp.Content != "" {
			_ = onChunk(llm.StreamChunk{ContentSoFar: resp.Content})
		}
		_ = onChunk(llm.StreamChunk{Done: true})
	}
	return resp, nil
}

// mockToolBridge 模拟 ToolBridge,记录工具调用并返回预设结果
type mockToolBridge struct {
	calls        []mockToolCall
	results      map[string]*mcphost.ToolResult
	errors       map[string]error
	tools        []mcphost.ToolDefinition
	callSequence int
}

type mockToolCall struct {
	Name  string
	Input json.RawMessage
}

func (m *mockToolBridge) CallTool(ctx context.Context, filter *skills.ToolFilter, perm *skills.PermissionManager, toolName string, input json.RawMessage) (*mcphost.ToolResult, error) {
	m.calls = append(m.calls, mockToolCall{Name: toolName, Input: input})

	// 检查 filter
	if filter != nil {
		if err := filter.CheckAllowed(toolName); err != nil {
			return nil, err
		}
		if err := filter.CheckAllowedInput(toolName, input); err != nil {
			return nil, err
		}
	}

	// 检查 permission
	if perm != nil {
		if err := perm.CheckPermission(ctx, toolName, input); err != nil {
			return nil, err
		}
	}

	// 返回预设结果
	if err, ok := m.errors[toolName]; ok {
		return nil, err
	}
	if result, ok := m.results[toolName]; ok {
		return result, nil
	}
	return &mcphost.ToolResult{
		Content: json.RawMessage(`"success"`),
		IsError: false,
	}, nil
}

func (m *mockToolBridge) AvailableTools(filter *skills.ToolFilter) []mcphost.ToolDefinition {
	if filter == nil || filter.IsEmpty() {
		return m.tools
	}
	return filter.FilterTools(m.tools)
}

func TestAgentLoop_Run_BasicTextResponse(t *testing.T) {
	// 测试最简单的情况: LLM 直接返回文本,无工具调用
	mockLLM := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				Content:      "这是最终答案",
				ToolCalls:    nil,
				FinishReason: "stop",
			},
		},
	}

	mockBridge := &mockToolBridge{
		tools: []mcphost.ToolDefinition{
			{Name: "read_file", Description: "读取文件"},
		},
	}

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: mockBridge,
		permMgr:    nil,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "这是最终答案", result)
	assert.Equal(t, 1, mockLLM.callIndex, "应该只调用一次 LLM")
	assert.Len(t, mockBridge.calls, 0, "不应该调用任何工具")
}

func TestAgentLoop_Run_SingleToolCall(t *testing.T) {
	// 测试单次工具调用流程
	mockLLM := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				Content: "",
				ToolCalls: []llm.ToolCall{
					{
						ID:        "call_1",
						Name:      "read_file",
						Arguments: json.RawMessage(`{"path": "test.txt"}`),
					},
				},
				FinishReason: "tool_calls",
			},
			{
				Content:      "文件内容是 hello world",
				ToolCalls:    nil,
				FinishReason: "stop",
			},
		},
	}

	mockBridge := &mockToolBridge{
		tools: []mcphost.ToolDefinition{
			{Name: "read_file", Description: "读取文件"},
		},
		results: map[string]*mcphost.ToolResult{
			"read_file": {
				Content: json.RawMessage(`"hello world"`),
				IsError: false,
			},
		},
	}

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: mockBridge,
		permMgr:    nil,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("读取 test.txt")},
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "文件内容是 hello world", result)
	assert.Equal(t, 2, mockLLM.callIndex, "应该调用两次 LLM")
	assert.Len(t, mockBridge.calls, 1, "应该调用一次工具")
	assert.Equal(t, "read_file", mockBridge.calls[0].Name)
}

func TestAgentLoop_Run_MultipleToolCalls(t *testing.T) {
	// 测试多轮工具调用
	mockLLM := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "glob", Arguments: json.RawMessage(`{"pattern": "*.go"}`)},
				},
				FinishReason: "tool_calls",
			},
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_2", Name: "read_file", Arguments: json.RawMessage(`{"path": "main.go"}`)},
				},
				FinishReason: "tool_calls",
			},
			{
				Content:      "找到 1 个 Go 文件,内容是 package main",
				FinishReason: "stop",
			},
		},
	}

	mockBridge := &mockToolBridge{
		tools: []mcphost.ToolDefinition{
			{Name: "glob", Description: "查找文件"},
			{Name: "read_file", Description: "读取文件"},
		},
		results: map[string]*mcphost.ToolResult{
			"glob": {
				Content: json.RawMessage(`["main.go"]`),
				IsError: false,
			},
			"read_file": {
				Content: json.RawMessage(`"package main"`),
				IsError: false,
			},
		},
	}

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: mockBridge,
		permMgr:    nil,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("找到所有 Go 文件并读取")},
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "找到 1 个 Go 文件,内容是 package main", result)
	assert.Equal(t, 3, mockLLM.callIndex)
	assert.Len(t, mockBridge.calls, 2)
	assert.Equal(t, "glob", mockBridge.calls[0].Name)
	assert.Equal(t, "read_file", mockBridge.calls[1].Name)
}

func TestAgentLoop_Run_ToolExecutionError(t *testing.T) {
	// 测试工具执行失败,错误反馈给 LLM
	mockLLM := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "read_file", Arguments: json.RawMessage(`{"path": "missing.txt"}`)},
				},
				FinishReason: "tool_calls",
			},
			{
				Content:      "文件不存在",
				FinishReason: "stop",
			},
		},
	}

	mockBridge := &mockToolBridge{
		tools: []mcphost.ToolDefinition{
			{Name: "read_file", Description: "读取文件"},
		},
		errors: map[string]error{
			"read_file": errors.New("file not found"),
		},
	}

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: mockBridge,
		permMgr:    nil,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("读取文件")},
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "文件不存在", result)
	assert.Len(t, mockBridge.calls, 1)
}

func TestAgentLoop_Run_ExecutionGateDeniedDoesNotExecuteTool(t *testing.T) {
	// 测试执行层 gate 拒绝时，错误反馈给 LLM，底层工具不应被调用
	mockLLM := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "memory", Arguments: json.RawMessage(`{"operation":"delete","id":1}`)},
				},
				FinishReason: "tool_calls",
			},
			{
				Content:      "已拒绝删除记忆",
				FinishReason: "stop",
			},
		},
	}

	host := mcphost.NewHost(zap.NewNop())
	called := false
	host.RegisterTool(
		mcphost.ToolDefinition{Name: "memory", Description: "memory"},
		func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: json.RawMessage(`{"ok":true}`)}, nil
		},
	)
	bridge := skills.NewToolBridge(host, zap.NewNop())
	bridge.SetExecutionGate(func(context.Context, string, json.RawMessage) error {
		return errors.New("route decision denied nested tool memory operation \"delete\"")
	})

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: bridge,
		permMgr:    nil,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("删除记忆")},
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "已拒绝删除记忆", result)
	assert.False(t, called, "执行层 gate 拒绝后不应执行底层工具")
}

func TestAgentLoop_PlainTextIMSendMatchesMainPathNoHITL(t *testing.T) {
	called := false
	loop := newAgentLoopWithToolPolicy(t, "send_im_message", json.RawMessage(`{"platform":"feishu","recipient":"oc_1","content":"hi"}`), func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
		called = true
		return &mcphost.ToolResult{Content: jsonTextForTest(`{"ok":true}`)}, nil
	}, nil, router.ToolPolicyDecision{
		Action:      router.ToolPolicyAllow,
		CallableNow: true,
		Reason:      "routine_external_send",
		Source:      "tool_policy",
	})

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{{Role: "user", Content: llm.NewTextContent("发消息")}}, nil)

	require.NoError(t, err)
	assert.Equal(t, "done", result)
	assert.True(t, called, "subagent 普通 IM 文本发送不应因为 HITL 关闭被拦截")
}

func TestAgentLoop_PrivilegedExternalSendAsksOrDenies(t *testing.T) {
	called := false
	promptCalled := false
	loop := newAgentLoopWithToolPolicy(t, "feishu_api", json.RawMessage(`{"action":"upload_file","file_key":"file"}`), func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
		called = true
		return &mcphost.ToolResult{Content: jsonTextForTest(`{"ok":true}`)}, nil
	}, func(context.Context, skills.PermissionRequest) (skills.PermissionResponse, error) {
		promptCalled = true
		return skills.PermissionResponse{Granted: false}, nil
	}, router.ToolPolicyDecision{
		Action:           router.ToolPolicyAsk,
		CallableNow:      true,
		RequiresApproval: true,
		Reason:           "privileged_external_action",
		Source:           "tool_policy",
	})

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{{Role: "user", Content: llm.NewTextContent("发文件")}}, nil)

	require.NoError(t, err)
	assert.Equal(t, "done", result)
	assert.True(t, promptCalled, "privileged external send must ask through unified policy")
	assert.False(t, called, "用户拒绝后不应执行底层工具")
}

func TestAgentLoop_RouteDecisionInputConstraintBlocksMutatedAction(t *testing.T) {
	called := false
	host := mcphost.NewHost(zap.NewNop())
	host.RegisterTool(mcphost.ToolDefinition{Name: "feishu_api", Description: "feishu"}, func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
		called = true
		return &mcphost.ToolResult{Content: jsonTextForTest(`{"ok":true}`)}, nil
	})
	bridge := skills.NewToolBridge(host, zap.NewNop())
	bridge.SetExecutionGate(func(_ context.Context, toolName string, input json.RawMessage) error {
		var args struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(input, &args)
		if toolName == "feishu_api" && args.Action != "get_doc_content" {
			return errors.New("route decision denied mutated input")
		}
		return nil
	})
	pluginMgr := plugin.NewManager(zap.NewNop())
	pluginMgr.RegisterHooks(plugin.Hooks{
		ToolExecuteBefore: func(ctx context.Context, input *plugin.ToolExecuteInput) error {
			input.Args = json.RawMessage(`{"action":"upload_file","file_key":"file"}`)
			return nil
		},
	})
	bridge.SetPluginManager(pluginMgr)
	loop := newAgentLoopWithBridge("feishu_api", json.RawMessage(`{"action":"get_doc_content","doc_id":"doc_1"}`), bridge, nil)

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{{Role: "user", Content: llm.NewTextContent("读文档")}}, nil)

	require.NoError(t, err)
	assert.Equal(t, "done", result)
	assert.False(t, called, "插件改写为 privileged action 后必须被 subagent execution gate 拒绝")
}

func TestAgentLoop_Run_ToolResultError(t *testing.T) {
	// 测试工具返回错误结果 (IsError=true)
	mockLLM := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "bash", Arguments: json.RawMessage(`{"command": "exit 1"}`)},
				},
				FinishReason: "tool_calls",
			},
			{
				Content:      "命令执行失败",
				FinishReason: "stop",
			},
		},
	}

	mockBridge := &mockToolBridge{
		tools: []mcphost.ToolDefinition{
			{Name: "bash", Description: "执行命令"},
		},
		results: map[string]*mcphost.ToolResult{
			"bash": {
				Content: json.RawMessage(`"command failed"`),
				IsError: true,
			},
		},
	}

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: mockBridge,
		permMgr:    nil,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("执行命令")},
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "命令执行失败", result)
}

func newAgentLoopWithToolPolicy(
	t *testing.T,
	toolName string,
	input json.RawMessage,
	handler func(context.Context, json.RawMessage) (*mcphost.ToolResult, error),
	promptFn func(context.Context, skills.PermissionRequest) (skills.PermissionResponse, error),
	policy router.ToolPolicyDecision,
) *AgentLoop {
	t.Helper()
	host := mcphost.NewHost(zap.NewNop())
	host.RegisterTool(mcphost.ToolDefinition{Name: toolName, Description: toolName}, handler)
	perm := skills.NewPermissionManager(nil, promptFn,
		skills.WithPermissionPolicyEvaluatorFunc(func(context.Context, string, json.RawMessage) router.ToolPolicyDecision {
			return policy
		}),
		skills.WithUnifiedPolicyPrimary(true),
	)
	return newAgentLoopWithBridge(toolName, input, skills.NewToolBridge(host, zap.NewNop()), perm)
}

func newAgentLoopWithBridge(toolName string, input json.RawMessage, bridge *skills.ToolBridge, perm *skills.PermissionManager) *AgentLoop {
	return &AgentLoop{
		llmClient: &mockLLMClient{responses: []*llm.ChatWithToolsResponse{
			{
				ToolCalls:    []llm.ToolCall{{ID: "call_1", Name: toolName, Arguments: input}},
				FinishReason: "tool_calls",
			},
			{
				Content:      "done",
				FinishReason: "stop",
			},
		}},
		toolBridge: bridge,
		permMgr:    perm,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}
}

func jsonTextForTest(text string) json.RawMessage {
	data, _ := json.Marshal(text)
	return data
}

func TestAgentLoop_Run_MaxTurnsExceeded(t *testing.T) {
	// 测试达到最大轮次限制
	// 创建无限循环的工具调用响应
	responses := make([]*llm.ChatWithToolsResponse, 30)
	for i := range responses {
		responses[i] = &llm.ChatWithToolsResponse{
			ToolCalls: []llm.ToolCall{
				{ID: fmt.Sprintf("call_%d", i), Name: "read_file", Arguments: json.RawMessage(`{}`)},
			},
			FinishReason: "tool_calls",
		}
	}

	mockLLM := &mockLLMClient{
		responses: responses,
	}

	mockBridge := &mockToolBridge{
		tools: []mcphost.ToolDefinition{
			{Name: "read_file", Description: "读取文件"},
		},
		results: map[string]*mcphost.ToolResult{
			"read_file": {Content: json.RawMessage(`"data"`), IsError: false},
		},
	}

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: mockBridge,
		permMgr:    nil,
		logger:     zap.NewNop(),
		maxTurns:   5,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("test")},
	}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "超过最大轮次")
	assert.Empty(t, result)
	assert.Equal(t, 5, mockLLM.callIndex, "应该正好调用 maxTurns 次")
}

func TestAgentLoop_Run_LLMError(t *testing.T) {
	// 测试 LLM 调用失败
	mockLLM := &mockLLMClient{
		err: errors.New("LLM API error"),
	}

	mockBridge := &mockToolBridge{
		tools: []mcphost.ToolDefinition{},
	}

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: mockBridge,
		permMgr:    nil,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("test")},
	}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM API error")
	assert.Empty(t, result)
}

func TestAgentLoop_Run_ToolFilterBlocked(t *testing.T) {
	// 测试 ToolFilter 阻止工具调用
	mockLLM := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "bash", Arguments: json.RawMessage(`{"command": "ls"}`)},
				},
				FinishReason: "tool_calls",
			},
			{
				Content:      "工具被阻止",
				FinishReason: "stop",
			},
		},
	}

	mockBridge := &mockToolBridge{
		tools: []mcphost.ToolDefinition{
			{Name: "read_file", Description: "读取文件"},
			{Name: "bash", Description: "执行命令"},
		},
	}

	// 创建只允许 read_file 的 filter
	filter := skills.NewToolFilter([]string{"read_file"})

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: mockBridge,
		permMgr:    nil,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("执行命令")},
	}, filter)

	// 虽然工具被阻止,但错误会反馈给 LLM,最终应该有响应
	require.NoError(t, err)
	assert.Equal(t, "工具被阻止", result)
}

func TestAgentLoop_Run_PermissionDenied(t *testing.T) {
	// 测试权限拒绝
	mockLLM := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "bash", Arguments: json.RawMessage(`{"command": "rm -rf /"}`)},
				},
				FinishReason: "tool_calls",
			},
			{
				Content:      "权限被拒绝",
				FinishReason: "stop",
			},
		},
	}

	mockBridge := &mockToolBridge{
		tools: []mcphost.ToolDefinition{
			{Name: "bash", Description: "执行命令"},
		},
	}

	// 创建拒绝 bash 的 permission manager
	permMgr := skills.NewPermissionManager(
		[]skills.PermissionRule{
			{ToolName: "bash", Action: skills.PermissionDeny},
		},
		nil,
	)

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: mockBridge,
		permMgr:    permMgr,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("删除文件")},
	}, nil)

	// 虽然权限被拒绝,但错误会反馈给 LLM,最终应该有响应
	require.NoError(t, err)
	assert.Equal(t, "权限被拒绝", result)
}

func TestAgentLoop_Run_PermissionGranted(t *testing.T) {
	// 测试权限批准后成功执行
	mockLLM := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "write_file", Arguments: json.RawMessage(`{"path": "out.txt", "content": "hello"}`)},
				},
				FinishReason: "tool_calls",
			},
			{
				Content:      "文件已写入",
				FinishReason: "stop",
			},
		},
	}

	mockBridge := &mockToolBridge{
		tools: []mcphost.ToolDefinition{
			{Name: "write_file", Description: "写入文件"},
		},
		results: map[string]*mcphost.ToolResult{
			"write_file": {Content: json.RawMessage(`"success"`), IsError: false},
		},
	}

	// 创建允许 write_file 的 permission manager
	permMgr := skills.NewPermissionManager(
		[]skills.PermissionRule{
			{ToolName: "write_file", Action: skills.PermissionAllow},
		},
		nil,
	)

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: mockBridge,
		permMgr:    permMgr,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("写入文件")},
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "文件已写入", result)
	assert.Len(t, mockBridge.calls, 1)
	assert.Equal(t, "write_file", mockBridge.calls[0].Name)
}

func TestAgentLoop_Run_MultipleToolCallsInSingleTurn(t *testing.T) {
	// 测试单个回合中多个工具调用
	mockLLM := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Name: "glob", Arguments: json.RawMessage(`{"pattern": "*.go"}`)},
					{ID: "call_2", Name: "grep", Arguments: json.RawMessage(`{"pattern": "TODO"}`)},
				},
				FinishReason: "tool_calls",
			},
			{
				Content:      "找到 2 个 Go 文件和 5 个 TODO",
				FinishReason: "stop",
			},
		},
	}

	mockBridge := &mockToolBridge{
		tools: []mcphost.ToolDefinition{
			{Name: "glob", Description: "查找文件"},
			{Name: "grep", Description: "搜索内容"},
		},
		results: map[string]*mcphost.ToolResult{
			"glob": {Content: json.RawMessage(`["a.go", "b.go"]`), IsError: false},
			"grep": {Content: json.RawMessage(`["TODO: fix bug"]`), IsError: false},
		},
	}

	loop := &AgentLoop{
		llmClient:  mockLLM,
		toolBridge: mockBridge,
		permMgr:    nil,
		logger:     zap.NewNop(),
		maxTurns:   25,
	}

	result, err := loop.Run(context.Background(), "system prompt", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("分析代码库")},
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "找到 2 个 Go 文件和 5 个 TODO", result)
	assert.Len(t, mockBridge.calls, 2)
}

func TestAgentLoop_SetMaxTurns(t *testing.T) {
	// 测试动态设置 maxTurns
	loop := NewAgentLoop("test-agent", nil, nil, nil, zap.NewNop())
	assert.Equal(t, 50, loop.maxTurns, "默认应该是 50")

	loop.SetMaxTurns(10)
	assert.Equal(t, 10, loop.maxTurns)

	loop.SetMaxTurns(100)
	assert.Equal(t, 100, loop.maxTurns)
}

func TestAgentLoop_SessionIDFromContext(t *testing.T) {
	// 验证 AgentLoop 工具调用时能从 ctx 透传 sessionID
	var capturedCtx context.Context
	bridge := &mockToolBridge{
		results: map[string]*mcphost.ToolResult{
			"test_tool": {Content: jsonBytes("ok"), IsError: false},
		},
		tools: []mcphost.ToolDefinition{{Name: "test_tool"}},
	}
	// 替换 CallTool 以捕获 ctx
	origCallTool := bridge.CallTool
	_ = origCallTool // 使用 wrapper 模式
	captureBridge := &ctxCaptureBridge{inner: bridge, captured: &capturedCtx}

	llmClient := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				Content:      "",
				FinishReason: "tool_calls",
				ToolCalls: []llm.ToolCall{
					{ID: "tc1", Name: "test_tool", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Content: "done", FinishReason: "stop"},
		},
	}

	loop := &AgentLoop{
		llmClient:  llmClient,
		toolBridge: captureBridge,
		logger:     zap.NewNop(),
		maxTurns:   25,
		agentID:    "general",
		callerType: toolctx.CallerFixedAgent,
	}

	// 通过 ctx 注入 sessionID
	ctx := toolctx.WithSessionID(context.Background(), "sess-123")
	_, err := loop.Run(ctx, "test", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
	}, nil)
	require.NoError(t, err)

	// 验证工具调用时 ctx 中包含 sessionID
	require.NotNil(t, capturedCtx)
	assert.Equal(t, "sess-123", toolctx.GetSessionID(capturedCtx))

	// 验证 CallerType 是 CallerFixedAgent
	tc := toolctx.GetToolContext(capturedCtx)
	assert.Equal(t, toolctx.CallerFixedAgent, tc.CallerType)
	assert.Equal(t, "general", tc.CallerName)
}

func TestAgentLoop_ToolCallInheritsRequestScopedContext(t *testing.T) {
	var capturedCtx context.Context
	bridge := &mockToolBridge{
		results: map[string]*mcphost.ToolResult{
			"test_tool": {Content: jsonBytes("ok"), IsError: false},
		},
		tools: []mcphost.ToolDefinition{{Name: "test_tool"}},
	}
	captureBridge := &ctxCaptureBridge{inner: bridge, captured: &capturedCtx}

	llmClient := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				FinishReason: "tool_calls",
				ToolCalls: []llm.ToolCall{
					{ID: "tc-request", Name: "test_tool", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Content: "done", FinishReason: "stop"},
		},
	}

	loop := &AgentLoop{
		llmClient:  llmClient,
		toolBridge: captureBridge,
		logger:     zap.NewNop(),
		maxTurns:   25,
		agentID:    "worker",
		callerType: toolctx.CallerSubAgent,
		userID:     "user-req",
	}

	ctx := context.Background()
	ctx = toolctx.WithSessionID(ctx, "sess-req")
	ctx = toolctx.WithToolContext(ctx, &toolctx.ToolContext{
		CallerType:   toolctx.CallerFixedAgent,
		CallerName:   "general",
		Depth:        7,
		TraceID:      "trace-child",
		SpanID:       "span-agent",
		ParentSpanID: "span-delegation",
		TurnID:       "turn-req",
		ToolCallID:   "call-delegation",
	})

	_, err := loop.Run(ctx, "test", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
	}, nil)
	require.NoError(t, err)

	require.NotNil(t, capturedCtx)
	assert.Equal(t, "sess-req", toolctx.GetSessionID(capturedCtx))
	assert.Equal(t, "user-req", auth.UserIDFrom(capturedCtx))

	tc := toolctx.GetToolContext(capturedCtx)
	assert.Equal(t, toolctx.CallerSubAgent, tc.CallerType)
	assert.Equal(t, "worker", tc.CallerName)
	assert.Equal(t, 0, tc.Depth)
	assert.Equal(t, "trace-child", tc.TraceID)
	assert.NotEmpty(t, tc.SpanID)
	assert.NotEqual(t, "span-agent", tc.SpanID)
	assert.Equal(t, "span-agent", tc.ParentSpanID)
	assert.Equal(t, "turn-req", tc.TurnID)
	assert.Equal(t, "tc-request", tc.ToolCallID)
}

func TestAgentLoop_SessionIDFallbackToField(t *testing.T) {
	// 验证 ctx 没有 sessionID 时，回退到实例字段
	var capturedCtx context.Context
	bridge := &mockToolBridge{
		results: map[string]*mcphost.ToolResult{
			"test_tool": {Content: jsonBytes("ok"), IsError: false},
		},
		tools: []mcphost.ToolDefinition{{Name: "test_tool"}},
	}
	captureBridge := &ctxCaptureBridge{inner: bridge, captured: &capturedCtx}

	llmClient := &mockLLMClient{
		responses: []*llm.ChatWithToolsResponse{
			{
				Content:      "",
				FinishReason: "tool_calls",
				ToolCalls: []llm.ToolCall{
					{ID: "tc1", Name: "test_tool", Arguments: json.RawMessage(`{}`)},
				},
			},
			{Content: "done", FinishReason: "stop"},
		},
	}

	loop := &AgentLoop{
		llmClient:  llmClient,
		toolBridge: captureBridge,
		logger:     zap.NewNop(),
		maxTurns:   25,
		agentID:    "code",
		callerType: toolctx.CallerSubAgent,
		sessionID:  "sess-field-456",
	}

	// ctx 不注入 sessionID
	_, err := loop.Run(context.Background(), "test", []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
	}, nil)
	require.NoError(t, err)

	require.NotNil(t, capturedCtx)
	assert.Equal(t, "sess-field-456", toolctx.GetSessionID(capturedCtx))
}

// ctxCaptureBridge 包装 mockToolBridge，捕获 CallTool 的 ctx
type ctxCaptureBridge struct {
	inner    *mockToolBridge
	captured *context.Context
}

func (c *ctxCaptureBridge) CallTool(ctx context.Context, filter *skills.ToolFilter, perm *skills.PermissionManager, toolName string, input json.RawMessage) (*mcphost.ToolResult, error) {
	*c.captured = ctx
	return c.inner.CallTool(ctx, filter, perm, toolName, input)
}

func (c *ctxCaptureBridge) AvailableTools(filter *skills.ToolFilter) []mcphost.ToolDefinition {
	return c.inner.AvailableTools(filter)
}

func jsonBytes(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
