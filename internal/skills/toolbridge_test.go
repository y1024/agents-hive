package skills

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/plugin"
	"github.com/chef-guo/agents-hive/internal/toolruntime"
)

func TestToolBridge_CallTool(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	// 注册测试工具
	host.RegisterTool(
		mcphost.ToolDefinition{
			Name:        "test_tool",
			Description: "测试工具",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
			return &mcphost.ToolResult{
				Content: json.RawMessage(`{"result":"success"}`),
				IsError: false,
			}, nil
		},
	)

	bridge := NewToolBridge(host, logger)

	tests := []struct {
		name        string
		filter      *ToolFilter
		toolName    string
		input       json.RawMessage
		wantErr     bool
		wantIsError bool // ToolResult.IsError（友好错误，非 Go error）
		errCode     int
		description string
	}{
		{
			name:        "无过滤器-成功",
			filter:      nil,
			toolName:    "test_tool",
			input:       json.RawMessage(`{}`),
			wantErr:     false,
			description: "nil filter 应该允许所有工具",
		},
		{
			name:        "空过滤器-成功",
			filter:      NewToolFilter([]string{}),
			toolName:    "test_tool",
			input:       json.RawMessage(`{}`),
			wantErr:     false,
			description: "空 filter 应该允许所有工具",
		},
		{
			name:        "允许的工具-成功",
			filter:      NewToolFilter([]string{"test_tool"}),
			toolName:    "test_tool",
			input:       json.RawMessage(`{}`),
			wantErr:     false,
			description: "filter 包含工具时应该成功",
		},
		{
			name:        "被阻止的工具-失败",
			filter:      NewToolFilter([]string{"other_tool"}),
			toolName:    "test_tool",
			input:       json.RawMessage(`{}`),
			wantErr:     true,
			errCode:     errs.CodeSkillToolBlocked,
			description: "filter 不包含工具时应该被阻止",
		},
		{
			name:        "工具不存在-友好错误",
			filter:      nil,
			toolName:    "nonexistent",
			input:       json.RawMessage(`{}`),
			wantErr:     false,
			wantIsError: true,
			description: "不存在的工具应该返回友好的 ToolResult 错误而非 Go error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := bridge.CallTool(context.Background(), tt.filter, nil, tt.toolName, tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("CallTool() 期望错误但没有得到, case: %s", tt.description)
					return
				}
				if e, ok := err.(*errs.Error); ok {
					if e.Code != tt.errCode {
						t.Errorf("CallTool() error code = %v, want %v, case: %s", e.Code, tt.errCode, tt.description)
					}
				}
			} else {
				if err != nil {
					t.Errorf("CallTool() unexpected error = %v, case: %s", err, tt.description)
					return
				}
				if result == nil {
					t.Errorf("CallTool() result = nil, want non-nil, case: %s", tt.description)
					return
				}
				if tt.wantIsError && !result.IsError {
					t.Errorf("CallTool() result.IsError = false, want true, case: %s", tt.description)
				}
			}
		})
	}
}

func TestToolBridge_AvailableTools(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	// 注册多个工具
	tools := []string{"read_file", "write_file", "grep", "glob"}
	for _, name := range tools {
		host.RegisterTool(
			mcphost.ToolDefinition{
				Name:        name,
				Description: "test tool " + name,
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
			func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
				return &mcphost.ToolResult{Content: json.RawMessage(`{}`)}, nil
			},
		)
	}

	bridge := NewToolBridge(host, logger)

	tests := []struct {
		name        string
		filter      *ToolFilter
		wantCount   int
		description string
	}{
		{
			name:        "无过滤器-返回全部",
			filter:      nil,
			wantCount:   4,
			description: "nil filter 应该返回所有 4 个工具",
		},
		{
			name:        "空过滤器-返回全部",
			filter:      NewToolFilter([]string{}),
			wantCount:   4,
			description: "空 filter 应该返回所有 4 个工具",
		},
		{
			name:        "部分过滤-返回子集",
			filter:      NewToolFilter([]string{"read_file", "grep"}),
			wantCount:   2,
			description: "filter 包含 2 个工具时应该只返回这 2 个",
		},
		{
			name:        "严格过滤-返回单个",
			filter:      NewToolFilter([]string{"write_file"}),
			wantCount:   1,
			description: "filter 包含 1 个工具时应该只返回 1 个",
		},
		{
			name:        "不匹配过滤-返回空",
			filter:      NewToolFilter([]string{"nonexistent"}),
			wantCount:   0,
			description: "filter 不包含任何已注册工具时应该返回空列表",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := bridge.AvailableTools(tt.filter)
			if len(result) != tt.wantCount {
				t.Errorf("AvailableTools() count = %v, want %v, case: %s", len(result), tt.wantCount, tt.description)
			}
		})
	}
}

func TestToolBridge_SkillEmptyName_NoHITL(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	// 注册 skill 工具
	host.RegisterTool(
		mcphost.ToolDefinition{
			Name:        "skill",
			Description: "技能工具",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
			return &mcphost.ToolResult{
				Content: json.RawMessage(`[{"name":"debug","description":"调试"}]`),
				IsError: false,
			}, nil
		},
	)

	bridge := NewToolBridge(host, logger)

	// 创建带 HITL 的 PermissionManager
	promptCalled := false
	promptFn := func(_ context.Context, _ PermissionRequest) (PermissionResponse, error) {
		promptCalled = true
		return PermissionResponse{Granted: true}, nil
	}
	perm := NewPermissionManager(nil, promptFn)

	// skill 空 name 应直接返回结果，不触发 HITL
	input := json.RawMessage(`{"name": "", "arguments": ""}`)
	result, err := bridge.CallTool(context.Background(), nil, perm, "skill", input)
	if err != nil {
		t.Fatalf("skill 空 name 应成功: %v", err)
	}
	if result == nil {
		t.Fatal("skill 空 name 应返回结果")
	}
	if promptCalled {
		t.Fatal("skill 空 name 不应触发 HITL 审批")
	}
}

func TestToolBridge_CallToolRechecksPermissionAfterPluginMutatesArgs(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	called := false
	host.RegisterTool(
		mcphost.ToolDefinition{
			Name:        "memory",
			Description: "memory",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: json.RawMessage(`{"ok":true}`)}, nil
		},
	)

	bridge := NewToolBridge(host, logger)
	pluginMgr := plugin.NewManager(logger)
	pluginMgr.RegisterHooks(plugin.Hooks{
		ToolExecuteBefore: func(ctx context.Context, input *plugin.ToolExecuteInput) error {
			input.Args = json.RawMessage(`{"operation":"delete","id":1}`)
			return nil
		},
	})
	bridge.SetPluginManager(pluginMgr)

	perm := NewPermissionManager([]PermissionRule{
		{ToolName: "memory", Pattern: "search", Action: PermissionAllow},
		{ToolName: "memory", Pattern: "delete", Action: PermissionDeny},
	}, func(context.Context, PermissionRequest) (PermissionResponse, error) {
		return PermissionResponse{Granted: true}, nil
	})

	result, err := bridge.CallTool(context.Background(), nil, perm, "memory", json.RawMessage(`{"operation":"search","query":"x"}`))

	if err != nil {
		t.Fatalf("插件改写为高风险 operation 后，用户批准应继续执行: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v, want success after approval", result)
	}
	if !called {
		t.Fatal("用户批准后应执行底层工具")
	}
}

func TestToolBridge_CallToolRecoverableWhenPluginRewritesToolName(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	called := false
	host.RegisterTool(
		mcphost.ToolDefinition{Name: "memory", Description: "memory", InputSchema: json.RawMessage(`{"type":"object"}`)},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: json.RawMessage(`{"ok":true}`)}, nil
		},
	)

	bridge := NewToolBridge(host, logger)
	pluginMgr := plugin.NewManager(logger)
	pluginMgr.RegisterHooks(plugin.Hooks{
		ToolExecuteBefore: func(_ context.Context, input *plugin.ToolExecuteInput) error {
			input.ToolName = "bash"
			return nil
		},
	})
	bridge.SetPluginManager(pluginMgr)

	result, err := bridge.CallTool(context.Background(), nil, nil, "memory", json.RawMessage(`{"operation":"search"}`))
	if err == nil {
		t.Fatal("插件改写工具名应返回错误")
	}
	if !errs.IsCode(err, errs.CodePermissionDenied) {
		t.Fatalf("err = %v, want CodePermissionDenied", err)
	}
	if !strings.Contains(err.Error(), toolruntime.RecoverableToolCallErrorMarker) {
		t.Fatalf("插件改写工具名应返回可恢复错误, got: %v", err)
	}
	if result != nil {
		t.Fatalf("result = %+v, want nil", result)
	}
	if called {
		t.Fatal("插件改写工具名后不应执行底层工具")
	}
}

func TestToolBridge_CallToolExecutionGateBlocksBeforeHost(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	called := false
	host.RegisterTool(
		mcphost.ToolDefinition{
			Name:        "memory",
			Description: "memory",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: json.RawMessage(`{"ok":true}`)}, nil
		},
	)

	bridge := NewToolBridge(host, logger)
	gateErr := errors.New(toolruntime.RecoverableToolCallErrorContent("nested_route_tool_not_allowed", "route decision denied nested tool"))
	bridge.SetExecutionGate(func(context.Context, string, json.RawMessage) error {
		return gateErr
	})

	result, err := bridge.CallTool(context.Background(), nil, nil, "memory", json.RawMessage(`{"operation":"delete"}`))

	if !errors.Is(err, gateErr) {
		t.Fatalf("err = %v, want gateErr", err)
	}
	if result != nil {
		t.Fatalf("result = %+v, want nil", result)
	}
	if called {
		t.Fatal("execution gate 拒绝后不应执行底层工具")
	}
}

func TestToolBridge_CallToolRechecksExecutionGateAfterPluginMutatesArgs(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)
	called := false
	host.RegisterTool(
		mcphost.ToolDefinition{
			Name:        "memory",
			Description: "memory",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
			called = true
			return &mcphost.ToolResult{Content: json.RawMessage(`{"ok":true}`)}, nil
		},
	)

	bridge := NewToolBridge(host, logger)
	pluginMgr := plugin.NewManager(logger)
	pluginMgr.RegisterHooks(plugin.Hooks{
		ToolExecuteBefore: func(ctx context.Context, input *plugin.ToolExecuteInput) error {
			input.Args = json.RawMessage(`{"operation":"delete","id":1}`)
			return nil
		},
	})
	bridge.SetPluginManager(pluginMgr)

	var seenInputs []string
	gateErr := errors.New(toolruntime.RecoverableToolCallErrorContent("nested_route_input_outside_allowed_values", "route decision denied mutated input"))
	bridge.SetExecutionGate(func(_ context.Context, toolName string, input json.RawMessage) error {
		seenInputs = append(seenInputs, string(input))
		var args struct {
			Operation string `json:"operation"`
		}
		_ = json.Unmarshal(input, &args)
		if args.Operation == "delete" {
			return gateErr
		}
		return nil
	})

	result, err := bridge.CallTool(context.Background(), nil, nil, "memory", json.RawMessage(`{"operation":"search","query":"x"}`))

	if !errors.Is(err, gateErr) {
		t.Fatalf("err = %v, want gateErr", err)
	}
	if result != nil {
		t.Fatalf("result = %+v, want nil", result)
	}
	if called {
		t.Fatal("插件改写后被 gate 拒绝，不应执行底层工具")
	}
	if len(seenInputs) != 2 {
		t.Fatalf("execution gate 调用次数 = %d, want 2; inputs=%v", len(seenInputs), seenInputs)
	}
}

func TestToolBridge_Integration(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	// 模拟真实场景: 注册 6 个内置工具
	builtinTools := []string{"read_file", "write_file", "glob", "grep", "bash", "edit"}
	for _, name := range builtinTools {
		toolName := name
		host.RegisterTool(
			mcphost.ToolDefinition{
				Name:        toolName,
				Description: "builtin tool " + toolName,
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
			func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
				return &mcphost.ToolResult{
					Content: json.RawMessage(`{"tool":"` + toolName + `"}`),
					IsError: false,
				}, nil
			},
		)
	}

	bridge := NewToolBridge(host, logger)

	t.Run("集成测试-允许读取工具", func(t *testing.T) {
		filter := NewToolFilter([]string{"read_file", "glob", "grep"})
		available := bridge.AvailableTools(filter)

		if len(available) != 3 {
			t.Errorf("expected 3 read-only tools, got %d", len(available))
		}

		// 调用允许的工具
		result, err := bridge.CallTool(context.Background(), filter, nil, "read_file", json.RawMessage(`{}`))
		if err != nil {
			t.Errorf("unexpected error calling allowed tool: %v", err)
		}
		if result == nil {
			t.Error("expected non-nil result")
		}
	})

	t.Run("集成测试-阻止写入工具", func(t *testing.T) {
		filter := NewToolFilter([]string{"read_file", "glob", "grep"})

		// 尝试调用被阻止的工具
		_, err := bridge.CallTool(context.Background(), filter, nil, "write_file", json.RawMessage(`{}`))
		if err == nil {
			t.Error("expected error when calling blocked tool")
		}

		if e, ok := err.(*errs.Error); ok {
			if e.Code != errs.CodeSkillToolBlocked {
				t.Errorf("expected CodeSkillToolBlocked, got %d", e.Code)
			}
		} else {
			t.Error("expected *errs.Error type")
		}
	})

	t.Run("集成测试-无限制模式", func(t *testing.T) {
		// nil filter = 允许所有工具
		available := bridge.AvailableTools(nil)
		if len(available) != 6 {
			t.Errorf("expected all 6 tools, got %d", len(available))
		}

		// 所有工具都应该可调用
		for _, tool := range builtinTools {
			_, err := bridge.CallTool(context.Background(), nil, nil, tool, json.RawMessage(`{}`))
			if err != nil {
				t.Errorf("unexpected error calling %s in unrestricted mode: %v", tool, err)
			}
		}
	})
}
