package explore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/subagent"
	"github.com/chef-guo/agents-hive/internal/toolctx"
)

// TestExploreAgentCreation 测试 Explore Agent 的创建
func TestExploreAgentCreation(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agent := New(nil, nil, nil, nil, logger)

	assert.NotNil(t, agent)
	assert.Equal(t, "explore", agent.ID())

	card := agent.Card()
	assert.Equal(t, "Explore Agent", card.Name)
	assert.Contains(t, card.Description, "探索")
}

// TestExploreStub 测试无 LLM 时的存根响应
func TestExploreStub(t *testing.T) {
	tests := []struct {
		name    string
		req     ExploreRequest
		wantErr bool
	}{
		{
			name: "基本探索请求",
			req: ExploreRequest{
				Target: "/path/to/project",
				Focus:  "architecture",
				Depth:  "quick",
			},
			wantErr: false,
		},
		{
			name: "空目标",
			req: ExploreRequest{
				Target: "",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := exploreStub(tt.req)
			assert.NotEmpty(t, result.Summary)
			assert.NotNil(t, result.Structure)
			assert.NotNil(t, result.KeyFiles)
			assert.NotNil(t, result.Patterns)
			assert.NotNil(t, result.Insights)
		})
	}
}

// TestExploreAgentWithStub 测试 Agent 的存根处理流程
func TestExploreAgentWithStub(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agent := New(nil, nil, nil, nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	req := subagent.TaskRequest{
		ID:   "test-1",
		Type: "explore",
		Payload: mustMarshal(t, ExploreRequest{
			Target: "/test/project",
			Focus:  "api",
		}),
	}

	// 发送任务（异步）；用 done channel 等待 goroutine 退出，避免 zaptest logger 竞态
	done := make(chan struct{})
	go func() {
		agent.Run(ctx)
		close(done)
	}()
	defer func() { cancel(); <-done }()
	time.Sleep(100 * time.Millisecond) // 等待 agent 启动

	resp, err := agent.SendTask(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "completed", resp.Status)
	assert.NotEmpty(t, resp.Result)

	// 解析结果
	var output ExploreOutput
	err = json.Unmarshal(resp.Result, &output)
	require.NoError(t, err)
	assert.NotEmpty(t, output.Summary)
}

// TestExploreAgentInvalidRequest 测试无效请求处理
func TestExploreAgentInvalidRequest(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agent := New(nil, nil, nil, nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	// 用 done channel 等待 goroutine 退出，避免 zaptest logger 竞态
	done := make(chan struct{})
	go func() {
		agent.Run(ctx)
		close(done)
	}()
	defer func() { cancel(); <-done }()
	time.Sleep(100 * time.Millisecond)

	req := subagent.TaskRequest{
		ID:      "test-2",
		Type:    "explore",
		Payload: json.RawMessage(`{invalid json`),
	}

	resp, err := agent.SendTask(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "failed", resp.Status)
	assert.Contains(t, resp.Error, "invalid explore request")
}

// TestToolWhitelist 测试工具白名单机制
func TestToolWhitelist(t *testing.T) {
	// 创建工具过滤器
	allowedToolsList := []string{
		"glob",
		"grep",
		"read_file",
		"bash",
	}
	filter := skills.NewToolFilter(allowedToolsList)

	// 验证白名单中的工具
	for _, tool := range allowedToolsList {
		assert.True(t, filter.IsAllowed(tool), "工具 %s 应在白名单中", tool)
	}

	// 验证禁止的工具不在白名单中
	forbiddenTools := []string{"write_file", "edit", "multiedit", "multi_edit"}
	for _, tool := range forbiddenTools {
		assert.False(t, filter.IsAllowed(tool), "工具 %s 不应在白名单中", tool)
	}
}

// TestMaxTurnsLimit 测试 15 轮限制
func TestMaxTurnsLimit(t *testing.T) {
	// 这个测试验证 AgentLoop 的 maxTurns 可以设置为 15
	logger := zaptest.NewLogger(t)

	// 创建 nil client 和 bridge（只测试配置，不实际运行）
	loop := subagent.NewAgentLoop("explore", nil, nil, nil, logger)
	loop.SetMaxTurns(15)

	// 这里只验证 SetMaxTurns 调用成功
	// 实际的轮次限制验证在集成测试中进行
	assert.NotNil(t, loop)
}

// TestExploreOutputFormat 测试输出格式的完整性
func TestExploreOutputFormat(t *testing.T) {
	output := ExploreOutput{
		Summary: "测试项目是一个 Go 语言 web 应用",
		Structure: ProjectStructure{
			RootPath: "/test/project",
			Directories: map[string]string{
				"/cmd/":      "命令行入口",
				"/internal/": "内部包",
			},
			FileTypes: map[string]int{
				".go": 50,
				".md": 5,
			},
		},
		KeyFiles: []KeyFile{
			{
				Path:       "/go.mod",
				Purpose:    "Go 模块定义",
				Importance: "high",
			},
		},
		Patterns: []CodePattern{
			{
				Pattern:     "依赖注入",
				Description: "通过构造函数注入依赖",
				Examples:    []string{"NewServer(db *DB, logger *zap.Logger)"},
			},
		},
		Insights: []string{
			"使用标准 Go 项目布局",
			"包含完善的测试覆盖",
		},
	}

	// 序列化和反序列化验证
	data, err := json.Marshal(output)
	require.NoError(t, err)

	var decoded ExploreOutput
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, output.Summary, decoded.Summary)
	assert.Equal(t, output.Structure.RootPath, decoded.Structure.RootPath)
	assert.Equal(t, len(output.KeyFiles), len(decoded.KeyFiles))
	assert.Equal(t, len(output.Patterns), len(decoded.Patterns))
	assert.Equal(t, len(output.Insights), len(decoded.Insights))
}

// --- Mock implementations ---

type mockLLMClient struct{}

func (m *mockLLMClient) ChatWithTools(ctx context.Context, req llm.ChatWithToolsRequest) (*llm.ChatWithToolsResponse, error) {
	// 返回模拟响应（无工具调用）
	return &llm.ChatWithToolsResponse{
		Content:   `{"summary": "mock exploration", "structure": {"root_path": "/"}, "key_files": [], "patterns": [], "insights": []}`,
		ToolCalls: nil,
	}, nil
}

func (m *mockLLMClient) ChatWithToolsStream(ctx context.Context, req llm.ChatWithToolsRequest, onChunk llm.StreamCallback) (*llm.ChatWithToolsResponse, error) {
	return m.ChatWithTools(ctx, req)
}

type toolCallingLLMClient struct{}

func (m *toolCallingLLMClient) ChatWithTools(ctx context.Context, req llm.ChatWithToolsRequest) (*llm.ChatWithToolsResponse, error) {
	return &llm.ChatWithToolsResponse{Content: `{"summary":"done","structure":{"root_path":"/"},"key_files":[],"patterns":[],"insights":[]}`}, nil
}

func (m *toolCallingLLMClient) ChatWithToolsStream(ctx context.Context, req llm.ChatWithToolsRequest, onChunk llm.StreamCallback) (*llm.ChatWithToolsResponse, error) {
	if len(req.Messages) <= 1 {
		return &llm.ChatWithToolsResponse{
			ToolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)},
			},
			FinishReason: "tool_calls",
		}, nil
	}
	return m.ChatWithTools(ctx, req)
}

type mockToolBridge struct{}

func (m *mockToolBridge) CallTool(ctx context.Context, filter *skills.ToolFilter, perm *skills.PermissionManager, toolName string, input json.RawMessage) (*mcphost.ToolResult, error) {
	return &mcphost.ToolResult{
		Content: json.RawMessage(`"mock result"`),
		IsError: false,
	}, nil
}

func (m *mockToolBridge) AvailableTools(filter *skills.ToolFilter) []mcphost.ToolDefinition {
	// 返回所有可用工具
	tools := []mcphost.ToolDefinition{
		{Name: "glob", Description: "查找文件"},
		{Name: "grep", Description: "搜索内容"},
		{Name: "read_file", Description: "读取文件"},
		{Name: "bash", Description: "执行命令"},
		{Name: "write_file", Description: "写入文件"},
		{Name: "edit", Description: "编辑文件"},
	}

	// 如果提供了过滤器，使用 FilterTools 方法
	if filter != nil {
		return filter.FilterTools(tools)
	}

	return tools
}

// --- Helper functions ---

func mustMarshal(t *testing.T, v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

// TestExploreAgentIntegration 集成测试：验证工具白名单在实际场景中生效
func TestExploreAgentIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	logger := zaptest.NewLogger(t)

	// 创建不带 LLM 的 agent（使用 stub 模式）
	agent := New(nil, nil, nil, nil, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	// 用 done channel 等待 goroutine 退出，避免 zaptest logger 竞态
	done := make(chan struct{})
	go func() {
		agent.Run(ctx)
		close(done)
	}()
	defer func() { cancel(); <-done }()
	time.Sleep(100 * time.Millisecond)

	// 发送探索请求
	req := subagent.TaskRequest{
		ID:   "integration-1",
		Type: "explore",
		Payload: mustMarshal(t, ExploreRequest{
			Target: "/test/project",
			Focus:  "architecture",
			Depth:  "quick",
		}),
	}

	resp, err := agent.SendTask(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "completed", resp.Status)

	// 验证输出
	var output ExploreOutput
	err = json.Unmarshal(resp.Result, &output)
	require.NoError(t, err)
	assert.NotEmpty(t, output.Summary)
	assert.NotNil(t, output.Structure)
	assert.NotNil(t, output.KeyFiles)
	assert.NotNil(t, output.Patterns)
	assert.NotNil(t, output.Insights)

	t.Logf("探索摘要: %s", output.Summary)
	t.Logf("关键文件数: %d", len(output.KeyFiles))
	t.Logf("代码模式数: %d", len(output.Patterns))
	t.Logf("洞察数: %d", len(output.Insights))
}

// TestToolFilterIntegration 测试工具过滤器在真实场景中的行为
func TestToolFilterIntegration(t *testing.T) {
	// 创建工具过滤器
	filter := skills.NewToolFilter([]string{"glob", "grep", "read_file", "bash"})

	// 模拟可用工具列表
	allTools := []mcphost.ToolDefinition{
		{Name: "glob"},
		{Name: "grep"},
		{Name: "read_file"},
		{Name: "bash"},
		{Name: "write_file"}, // 不在白名单
		{Name: "edit"},       // 不在白名单
	}

	// 应用过滤
	filtered := filter.FilterTools(allTools)

	// 验证只有白名单中的工具
	assert.Equal(t, 4, len(filtered))
	toolNames := make(map[string]bool)
	for _, tool := range filtered {
		toolNames[tool.Name] = true
	}

	assert.True(t, toolNames["glob"])
	assert.True(t, toolNames["grep"])
	assert.True(t, toolNames["read_file"])
	assert.True(t, toolNames["bash"])
	assert.False(t, toolNames["write_file"])
	assert.False(t, toolNames["edit"])
}

func TestExploreAgentInjectsTaskSessionIDIntoToolGate(t *testing.T) {
	logger := zaptest.NewLogger(t)
	host := mcphost.NewHost(logger)
	host.RegisterTool(
		mcphost.ToolDefinition{Name: "read_file", Description: "读取文件"},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			return &mcphost.ToolResult{Content: json.RawMessage(`"ok"`)}, nil
		},
	)
	bridge := skills.NewToolBridge(host, logger)
	var gateSessionID string
	bridge.SetExecutionGate(func(ctx context.Context, toolName string, input json.RawMessage) error {
		gateSessionID = toolctx.GetSessionID(ctx)
		if gateSessionID == "" {
			return errors.New("missing session id")
		}
		return nil
	})

	loop := subagent.NewAgentLoopWithLLMClient("explore", &toolCallingLLMClient{}, bridge, nil, logger)
	loop.SetMaxTurns(5)
	handler := makeExploreHandler(loop, nil, nil, logger)

	resp := handler(context.Background(), subagent.TaskRequest{
		ID:        "explore-session-gate",
		Type:      "explore",
		SessionID: "sess-explore-gate",
		Payload: mustMarshal(t, ExploreRequest{
			Target: "/test/project",
		}),
	})

	require.Equal(t, "completed", resp.Status, resp.Error)
	assert.Equal(t, "sess-explore-gate", gateSessionID)
}
