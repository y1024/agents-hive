package skills

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/plugin"
	"github.com/chef-guo/agents-hive/internal/router"
	"github.com/chef-guo/agents-hive/internal/toolruntime"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// matchGlob 测试
// ---------------------------------------------------------------------------

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		value   string
		want    bool
	}{
		// 精确匹配
		{"精确匹配_命中", "bash", "bash", true},
		{"精确匹配_未命中", "bash", "edit", false},

		// 单个 * 匹配所有
		{"星号匹配所有", "*", "any_tool", true},
		{"星号匹配空串", "*", "", true},

		// 工具名通配符
		{"前缀通配符", "read_*", "read_file", true},
		{"前缀通配符_未命中", "read_*", "write_file", false},

		// ? 匹配单个字符
		{"问号匹配", "bas?", "bash", true},
		{"问号不匹配多字符", "bas?", "basho", false},

		// 路径 glob 模式
		{"路径匹配_精确", "src/main.go", "src/main.go", true},
		{"路径匹配_星号", "src/*.go", "src/main.go", true},
		{"路径匹配_星号不跨目录", "src/*.go", "src/sub/main.go", false},

		// ** 双星号匹配
		{"双星号匹配_任意深度", "src/**/*.go", "src/main.go", true},
		{"双星号匹配_子目录", "src/**/*.go", "src/sub/main.go", true},
		{"双星号匹配_深层子目录", "src/**/*.go", "src/a/b/c/main.go", true},
		{"双星号匹配_不匹配其他后缀", "src/**/*.go", "src/main.ts", false},
		{"双星号匹配_前缀不符", "src/**/*.go", "lib/main.go", false},

		// 命令模式
		{"命令匹配_rm", "rm *", "rm -rf /tmp", true},
		{"命令匹配_git", "git *", "git status", true},
		{"命令匹配_不匹配", "rm *", "ls -la", false},

		// 空模式
		{"空模式匹配空值", "", "", true},
		{"空模式不匹配非空", "", "something", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchGlob(tt.pattern, tt.value)
			if got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, 期望 %v", tt.pattern, tt.value, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractInputValue 测试
// ---------------------------------------------------------------------------

func TestExtractInputValue(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		input    string
		want     string
	}{
		{
			"bash_提取command",
			"bash",
			`{"command": "ls -la"}`,
			"ls -la",
		},
		{
			"edit_提取file_path",
			"edit",
			`{"file_path": "/tmp/test.go", "old_string": "a"}`,
			"/tmp/test.go",
		},
		{
			"read_file_提取file_path",
			"read_file",
			`{"file_path": "src/main.go"}`,
			"src/main.go",
		},
		{
			"未知工具_优先command",
			"custom_tool",
			`{"command": "do_something", "file_path": "/tmp"}`,
			"do_something",
		},
		{
			"未知工具_回退file_path",
			"custom_tool",
			`{"file_path": "/tmp/x.txt"}`,
			"/tmp/x.txt",
		},
		{
			"空输入",
			"bash",
			``,
			"",
		},
		{
			"无匹配字段",
			"bash",
			`{"unrelated": "value"}`,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var input json.RawMessage
			if tt.input != "" {
				input = json.RawMessage(tt.input)
			}
			got := extractInputValue(tt.toolName, input)
			if got != tt.want {
				t.Errorf("extractInputValue(%q, %s) = %q, 期望 %q", tt.toolName, tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// matchRules 测试
// ---------------------------------------------------------------------------

func TestMatchRules(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "read_file", Action: PermissionAllow},
		{ToolName: "bash", Pattern: "git *", Action: PermissionAllow},
		{ToolName: "bash", Pattern: "rm *", Action: PermissionDeny},
		{ToolName: "bash", Action: PermissionAsk},
		{ToolName: "edit", Pattern: "src/**/*.go", Action: PermissionAllow},
		{ToolName: "edit", Action: PermissionAsk},
		{ToolName: "*", Action: PermissionAsk},
	}

	pm := NewPermissionManager(rules, nil)

	tests := []struct {
		name       string
		tool       string
		inputValue string
		wantAction PermissionAction
		wantMatch  bool
	}{
		{"read_file无模式_直接允许", "read_file", "", PermissionAllow, true},
		{"read_file有参数_也允许", "read_file", "/any/path", PermissionAllow, true},
		{"bash_git命令_允许", "bash", "git status", PermissionAllow, true},
		{"bash_rm命令_拒绝", "bash", "rm -rf /", PermissionDeny, true},
		{"bash_其他命令_ask", "bash", "curl http://example.com", PermissionAsk, true},
		{"edit_src下go文件_允许", "edit", "src/main.go", PermissionAllow, true},
		{"edit_其他文件_ask", "edit", "config.yaml", PermissionAsk, true},
		{"未知工具_通配符匹配", "unknown_tool", "", PermissionAsk, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, matched := pm.matchRules(tt.tool, tt.inputValue)
			if matched != tt.wantMatch {
				t.Errorf("matchRules(%q, %q) matched = %v, 期望 %v", tt.tool, tt.inputValue, matched, tt.wantMatch)
			}
			if matched && action != tt.wantAction {
				t.Errorf("matchRules(%q, %q) action = %q, 期望 %q", tt.tool, tt.inputValue, action, tt.wantAction)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GrantSession 测试
// ---------------------------------------------------------------------------

func TestGrantSession(t *testing.T) {
	pm := NewPermissionManager(nil, nil)

	// 无模式 grant
	pm.GrantSession("bash", "", PermissionAllow)
	action, matched := pm.checkGrants("bash", "any command")
	if !matched || action != PermissionAllow {
		t.Fatalf("无模式 grant 应匹配任何参数, got matched=%v action=%v", matched, action)
	}

	// 带模式 grant
	pm.Reset()
	pm.GrantSession("bash", "git *", PermissionAllow)

	action, matched = pm.checkGrants("bash", "git push")
	if !matched || action != PermissionAllow {
		t.Fatalf("模式 grant 应匹配 git push, got matched=%v action=%v", matched, action)
	}

	_, matched = pm.checkGrants("bash", "rm -rf /")
	if matched {
		t.Fatal("模式 grant 不应匹配 rm 命令")
	}
}

func TestGrantSession_GetGrants(t *testing.T) {
	pm := NewPermissionManager(nil, nil)

	pm.GrantSession("bash", "", PermissionAllow)
	pm.GrantSession("edit", "src/**/*.go", PermissionAllow)

	grants := pm.GetGrants()
	if len(grants) != 2 {
		t.Fatalf("期望 2 个 grants, 得到 %d", len(grants))
	}
	if grants["bash"] != PermissionAllow {
		t.Errorf("bash grant 应为 allow, 得到 %v", grants["bash"])
	}
	if grants["edit:src/**/*.go"] != PermissionAllow {
		t.Errorf("edit:src/**/*.go grant 应为 allow, 得到 %v", grants["edit:src/**/*.go"])
	}
}

// ---------------------------------------------------------------------------
// CheckPermission 集成测试
// ---------------------------------------------------------------------------

func TestCheckPermission_SessionGrantOverridesRules(t *testing.T) {
	// 规则命中高风险 bash rm *，应转入审批；用户批准后执行。
	rules := []PermissionRule{
		{ToolName: "bash", Pattern: "rm *", Action: PermissionDeny},
	}

	promptCalled := 0
	pm := NewPermissionManager(rules, func(context.Context, PermissionRequest) (PermissionResponse, error) {
		promptCalled++
		return PermissionResponse{Granted: true}, nil
	})
	input := json.RawMessage(`{"command": "rm -rf /tmp/test"}`)

	err := pm.CheckPermission(context.Background(), "bash", input)
	if err != nil {
		t.Fatalf("规则 deny 应转入审批并在用户批准后成功: %v", err)
	}
	if promptCalled != 1 {
		t.Fatalf("规则 deny 应触发 1 次审批, got %d", promptCalled)
	}

	// 用户 "always allow" 覆盖规则
	pm.GrantSession("bash", "rm *", PermissionAllow)
	err = pm.CheckPermission(context.Background(), "bash", input)
	if err != nil {
		t.Fatalf("session grant 应覆盖规则: %v", err)
	}
}

func TestPermissionManager_CheckPermission_Allow(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "read_file", Action: PermissionAllow},
		{ToolName: "glob", Action: PermissionAllow},
	}

	mgr := NewPermissionManager(rules, nil)

	err := mgr.CheckPermission(context.Background(), "read_file", json.RawMessage(`{}`))
	if err != nil {
		t.Errorf("allowed 工具不应返回错误, got: %v", err)
	}
}

func TestPermissionManager_CheckPermission_Deny(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "dangerous_tool", Action: PermissionDeny},
	}

	promptCalled := false
	mgr := NewPermissionManager(rules, func(context.Context, PermissionRequest) (PermissionResponse, error) {
		promptCalled = true
		return PermissionResponse{Granted: true}, nil
	})

	err := mgr.CheckPermission(context.Background(), "dangerous_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Errorf("deny 工具经用户批准后不应返回错误, got: %v", err)
	}
	if !promptCalled {
		t.Error("deny 规则应转入人工审批")
	}
}

func TestPermissionManager_CheckPermission_Ask_Granted(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "write_file", Action: PermissionAsk},
	}

	promptCalled := false
	promptFn := func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
		promptCalled = true
		if req.ToolName != "write_file" {
			t.Errorf("期望 tool_name=write_file, 得到 %s", req.ToolName)
		}
		return PermissionResponse{Granted: true, Remember: false}, nil
	}

	mgr := NewPermissionManager(rules, promptFn)

	err := mgr.CheckPermission(context.Background(), "write_file", json.RawMessage(`{}`))
	if err != nil {
		t.Errorf("用户批准后不应返回错误, got: %v", err)
	}
	if !promptCalled {
		t.Error("应调用 prompt 函数")
	}
}

func TestPermissionManager_UnifiedPolicyPrimaryAllowSkipsLegacyRules(t *testing.T) {
	mgr := NewPermissionManager([]PermissionRule{
		{ToolName: "send_im_message", Action: PermissionDeny},
	}, func(context.Context, PermissionRequest) (PermissionResponse, error) {
		t.Fatal("unified allow should not trigger legacy prompt")
		return PermissionResponse{}, nil
	}, WithPermissionPolicyEvaluatorFunc(func(context.Context, string, json.RawMessage) router.ToolPolicyDecision {
		return router.ToolPolicyDecision{
			Action:      router.ToolPolicyAllow,
			CallableNow: true,
			Reason:      "routine_external_send",
			Source:      "tool_policy",
		}
	}), WithUnifiedPolicyPrimary(true))

	err := mgr.CheckPermission(context.Background(), "send_im_message", json.RawMessage(`{"content":"hi"}`))
	if err != nil {
		t.Fatalf("unified allow should skip legacy deny in primary mode: %v", err)
	}
}

func TestPermissionManager_UnifiedPolicyPrimaryAskUsesPrompt(t *testing.T) {
	promptCalled := false
	mgr := NewPermissionManager([]PermissionRule{
		{ToolName: "feishu_api", Action: PermissionAllow},
	}, func(context.Context, PermissionRequest) (PermissionResponse, error) {
		promptCalled = true
		return PermissionResponse{Granted: true}, nil
	}, WithPermissionPolicyEvaluatorFunc(func(context.Context, string, json.RawMessage) router.ToolPolicyDecision {
		return router.ToolPolicyDecision{
			Action:           router.ToolPolicyAsk,
			CallableNow:      true,
			RequiresApproval: true,
			Reason:           "privileged_external_action",
			Source:           "tool_policy",
		}
	}), WithUnifiedPolicyPrimary(true))

	err := mgr.CheckPermission(context.Background(), "feishu_api", json.RawMessage(`{"action":"upload_file"}`))
	if err != nil {
		t.Fatalf("unified ask should be approvable: %v", err)
	}
	if !promptCalled {
		t.Fatal("unified ask should trigger prompt even when legacy rule says allow")
	}
}

func TestPermissionManager_LegacyModeRulesStillApply(t *testing.T) {
	mgr := NewPermissionManager([]PermissionRule{
		{ToolName: "send_im_message", Action: PermissionDeny},
	}, nil, WithPermissionPolicyEvaluatorFunc(func(context.Context, string, json.RawMessage) router.ToolPolicyDecision {
		return router.ToolPolicyDecision{
			Action:      router.ToolPolicyAllow,
			CallableNow: true,
			Reason:      "routine_external_send",
			Source:      "tool_policy",
		}
	}), WithUnifiedPolicyPrimary(false))

	err := mgr.CheckPermission(context.Background(), "send_im_message", json.RawMessage(`{"content":"hi"}`))
	if err == nil {
		t.Fatal("legacy mode should still let permission_rules deny as strict rollback")
	}
}

func TestPermissionManager_CheckPermission_Ask_Denied(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "bash", Action: PermissionAsk},
	}

	promptFn := func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
		return PermissionResponse{Granted: false, Remember: false}, nil
	}

	mgr := NewPermissionManager(rules, promptFn)

	err := mgr.CheckPermission(context.Background(), "bash", json.RawMessage(`{}`))
	if err == nil {
		t.Error("用户拒绝后应返回错误")
	}

	if e, ok := err.(*errs.Error); ok {
		if e.Code != errs.CodePermissionDenied {
			t.Errorf("期望 CodePermissionDenied, 得到 %d", e.Code)
		}
	}
}

func TestPermissionManager_CheckPermission_PromptErrorRecoverable(t *testing.T) {
	mgr := NewPermissionManager([]PermissionRule{
		{ToolName: "bash", Action: PermissionAsk},
	}, func(context.Context, PermissionRequest) (PermissionResponse, error) {
		return PermissionResponse{}, errors.New("approval unavailable")
	})

	err := mgr.CheckPermission(context.Background(), "bash", json.RawMessage(`{"command":"curl https://example.com"}`))
	if err == nil {
		t.Fatal("审批请求失败应返回错误")
	}
	if !errs.IsCode(err, errs.CodeExecApprovalTimeout) {
		t.Fatalf("err = %v, want CodeExecApprovalTimeout", err)
	}
	if !strings.Contains(err.Error(), toolruntime.RecoverableToolCallErrorMarker) {
		t.Fatalf("审批请求失败应返回可恢复错误, got: %v", err)
	}
}

func TestPermissionManager_CheckPermission_Remember(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "edit", Action: PermissionAsk},
	}

	callCount := 0
	promptFn := func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
		callCount++
		return PermissionResponse{Granted: true, Remember: true}, nil
	}

	mgr := NewPermissionManager(rules, promptFn)

	// 第一次调用 - 应该提示
	err := mgr.CheckPermission(context.Background(), "edit", json.RawMessage(`{}`))
	if err != nil {
		t.Errorf("第一次调用应成功: %v", err)
	}
	if callCount != 1 {
		t.Errorf("期望 1 次 prompt 调用, 得到 %d", callCount)
	}

	// 第二次调用 - 应该使用 remembered 决策，不提示
	err = mgr.CheckPermission(context.Background(), "edit", json.RawMessage(`{}`))
	if err != nil {
		t.Errorf("第二次调用应成功: %v", err)
	}
	if callCount != 1 {
		t.Errorf("期望仅 1 次 prompt 调用 (remembered), 得到 %d", callCount)
	}
}

func TestPermissionManager_RememberSpecificDangerousActionDoesNotBlanketAllowTool(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "memory", Pattern: "delete", Action: PermissionAsk},
		{ToolName: "memory", Action: PermissionAllow},
	}

	callCount := 0
	mgr := NewPermissionManager(rules, func(context.Context, PermissionRequest) (PermissionResponse, error) {
		callCount++
		return PermissionResponse{Granted: true, Remember: true}, nil
	})

	err := mgr.CheckPermission(context.Background(), "memory", json.RawMessage(`{"operation":"delete","id":1}`))
	if err != nil {
		t.Fatalf("第一次 memory.delete approve 应成功: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("第一次 memory.delete 应触发 1 次 prompt, got %d", callCount)
	}

	err = mgr.CheckPermission(context.Background(), "memory", json.RawMessage(`{"operation":"delete","id":2}`))
	if err != nil {
		t.Fatalf("新的 memory.delete 输入经第二次 approve 应成功: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("危险操作 remember 应按具体输入收敛，新的 delete payload 应再次 prompt, got %d", callCount)
	}

	dangerCalled := false
	mgr.SetPromptFn(func(context.Context, PermissionRequest) (PermissionResponse, error) {
		dangerCalled = true
		return PermissionResponse{Granted: false}, nil
	})
	err = mgr.CheckPermission(context.Background(), "taskboard", json.RawMessage(`{"operation":"delete","id":"task-1"}`))
	if err == nil {
		t.Fatal("memory.delete 的 remember 不应放行 taskboard.delete")
	}
	if !dangerCalled {
		t.Fatal("taskboard.delete 应走自己的审批流程")
	}
}

func TestPermissionManager_PluginCannotAutoAllowStructuredDangerousOperation(t *testing.T) {
	mgr := NewPermissionManager([]PermissionRule{
		{ToolName: "memory", Pattern: "delete", Action: PermissionAsk},
		{ToolName: "memory", Action: PermissionAllow},
	}, func(context.Context, PermissionRequest) (PermissionResponse, error) {
		return PermissionResponse{Granted: false}, nil
	})
	pluginMgr := plugin.NewManager(zap.NewNop())
	pluginMgr.RegisterHooks(plugin.Hooks{
		PermissionAsk: func(context.Context, *plugin.PermissionAskInput) (*plugin.PermissionAskOutput, error) {
			return &plugin.PermissionAskOutput{Decision: "allow", Reason: "unsafe bypass attempt"}, nil
		},
	})
	mgr.SetPluginManager(pluginMgr)

	err := mgr.CheckPermission(context.Background(), "memory", json.RawMessage(`{"operation":"delete","id":1}`))
	if err == nil {
		t.Fatal("插件不应自动批准结构化危险操作")
	}
	if !errs.IsCode(err, errs.CodePermissionDenied) {
		t.Fatalf("err = %v, want permission denied", err)
	}
}

func TestPermissionManager_PluginCannotAutoAllowShellAsk(t *testing.T) {
	mgr := NewPermissionManager([]PermissionRule{
		{ToolName: "bash", Action: PermissionAsk},
	}, func(context.Context, PermissionRequest) (PermissionResponse, error) {
		return PermissionResponse{Granted: false}, nil
	})
	pluginMgr := plugin.NewManager(zap.NewNop())
	pluginMgr.RegisterHooks(plugin.Hooks{
		PermissionAsk: func(context.Context, *plugin.PermissionAskInput) (*plugin.PermissionAskOutput, error) {
			return &plugin.PermissionAskOutput{Decision: "allow", Reason: "unsafe bypass attempt"}, nil
		},
	})
	mgr.SetPluginManager(pluginMgr)

	err := mgr.CheckPermission(context.Background(), "bash", json.RawMessage(`{"command":"rm -rf /tmp/x"}`))
	if err == nil {
		t.Fatal("插件不应自动批准 shell ask")
	}
	if !errs.IsCode(err, errs.CodePermissionDenied) {
		t.Fatalf("err = %v, want permission denied", err)
	}
}

func TestPermissionManager_Grant(t *testing.T) {
	promptCalled := false
	mgr := NewPermissionManager(nil, func(context.Context, PermissionRequest) (PermissionResponse, error) {
		promptCalled = true
		return PermissionResponse{Granted: false}, nil
	})

	// 授予权限
	mgr.Grant("test_tool", PermissionAllow)

	// 验证授予的权限生效
	err := mgr.CheckPermission(context.Background(), "test_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Errorf("granted 工具应被允许: %v", err)
	}

	// 拒绝权限
	mgr.Grant("test_tool", PermissionDeny)
	err = mgr.CheckPermission(context.Background(), "test_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Error("用户拒绝审批后应返回错误")
	}
	if !promptCalled {
		t.Error("deny grant 应转入人工审批")
	}
}

func TestPermissionManager_Reset(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "tool1", Action: PermissionAsk},
	}

	callCount := 0
	promptFn := func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
		callCount++
		return PermissionResponse{Granted: true, Remember: true}, nil
	}

	mgr := NewPermissionManager(rules, promptFn)

	// 第一次调用并记住
	mgr.CheckPermission(context.Background(), "tool1", json.RawMessage(`{}`))

	// Reset
	mgr.Reset()

	// 第二次调用应该重新提示
	mgr.CheckPermission(context.Background(), "tool1", json.RawMessage(`{}`))

	if callCount != 2 {
		t.Errorf("reset 后期望 2 次 prompt 调用, 得到 %d", callCount)
	}
}

func TestPermissionManager_GetGrants(t *testing.T) {
	mgr := NewPermissionManager(nil, nil)

	mgr.Grant("tool1", PermissionAllow)
	mgr.Grant("tool2", PermissionDeny)

	grants := mgr.GetGrants()

	if len(grants) != 2 {
		t.Errorf("期望 2 个 grants, 得到 %d", len(grants))
	}
	if grants["tool1"] != PermissionAllow {
		t.Errorf("期望 tool1=allow, 得到 %v", grants["tool1"])
	}
	if grants["tool2"] != PermissionDeny {
		t.Errorf("期望 tool2=deny, 得到 %v", grants["tool2"])
	}
}

func TestPermissionManager_DefaultAction(t *testing.T) {
	// 无规则，无 promptFn - 应该默认 ask 但失败（因为无 promptFn）
	mgr := NewPermissionManager(nil, nil)

	err := mgr.CheckPermission(context.Background(), "unknown_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Error("无规则且无 prompt 函数时应返回错误")
	}
}

func TestPermissionManager_RememberDeny(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "risky", Action: PermissionAsk},
	}

	callCount := 0
	promptFn := func(ctx context.Context, req PermissionRequest) (PermissionResponse, error) {
		callCount++
		return PermissionResponse{Granted: false, Remember: true}, nil
	}

	mgr := NewPermissionManager(rules, promptFn)

	// 第一次调用 - 用户拒绝并记住
	err := mgr.CheckPermission(context.Background(), "risky", json.RawMessage(`{}`))
	if err == nil {
		t.Error("第一次调用应被拒绝")
	}

	// 第二次调用 - remembered deny 仍应转入审批，由用户当前确认决定
	err = mgr.CheckPermission(context.Background(), "risky", json.RawMessage(`{}`))
	if err == nil {
		t.Error("第二次调用应被拒绝")
	}

	if callCount != 2 {
		t.Errorf("期望 2 次 prompt 调用 (remembered deny 仍需用户确认), 得到 %d", callCount)
	}
}

// ---------------------------------------------------------------------------
// 通配符工具名规则测试
// ---------------------------------------------------------------------------

func TestCheckPermission_WildcardToolRule(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "read_*", Action: PermissionAllow},
	}
	pm := NewPermissionManager(rules, nil)

	if err := pm.CheckPermission(context.Background(), "read_file", nil); err != nil {
		t.Fatalf("read_file 应被 read_* 规则允许: %v", err)
	}
	if err := pm.CheckPermission(context.Background(), "read_dir", nil); err != nil {
		t.Fatalf("read_dir 应被 read_* 规则允许: %v", err)
	}
}

func TestCheckPermission_StarMatchesAll(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "*", Action: PermissionAllow},
	}
	pm := NewPermissionManager(rules, nil)

	for _, tool := range []string{"bash", "edit", "read_file", "custom"} {
		if err := pm.CheckPermission(context.Background(), tool, nil); err != nil {
			t.Fatalf("* 规则应允许所有工具, %q 报错: %v", tool, err)
		}
	}
}

// ---------------------------------------------------------------------------
// 模式匹配 + 工具输入 集成测试
// ---------------------------------------------------------------------------

func TestCheckPermission_PatternMatchWithInput(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "edit", Pattern: "src/**/*.go", Action: PermissionAllow},
		{ToolName: "edit", Action: PermissionDeny}, // 其他路径拒绝
	}
	promptCalled := false
	pm := NewPermissionManager(rules, func(context.Context, PermissionRequest) (PermissionResponse, error) {
		promptCalled = true
		return PermissionResponse{Granted: true}, nil
	})

	// src 下的 .go 文件应被允许
	input := json.RawMessage(`{"file_path": "src/main.go"}`)
	if err := pm.CheckPermission(context.Background(), "edit", input); err != nil {
		t.Fatalf("src/main.go 应被允许: %v", err)
	}

	// 子目录也应被允许
	input = json.RawMessage(`{"file_path": "src/pkg/handler.go"}`)
	if err := pm.CheckPermission(context.Background(), "edit", input); err != nil {
		t.Fatalf("src/pkg/handler.go 应被允许: %v", err)
	}

	// 非 src 目录命中 deny 规则时应转入审批，用户批准后允许执行
	input = json.RawMessage(`{"file_path": "config.yaml"}`)
	if err := pm.CheckPermission(context.Background(), "edit", input); err != nil {
		t.Fatalf("config.yaml 经用户批准后应允许执行: %v", err)
	}
	if !promptCalled {
		t.Fatal("config.yaml 应触发审批")
	}
}

func TestCheckPermission_BashCommandPattern(t *testing.T) {
	rules := []PermissionRule{
		{ToolName: "bash", Pattern: "git *", Action: PermissionAllow},
		{ToolName: "bash", Pattern: "rm *", Action: PermissionDeny},
		{ToolName: "bash", Action: PermissionAsk},
	}

	promptCalled := false
	promptFn := func(_ context.Context, _ PermissionRequest) (PermissionResponse, error) {
		promptCalled = true
		return PermissionResponse{Granted: true}, nil
	}

	pm := NewPermissionManager(rules, promptFn)

	// git 命令允许
	input := json.RawMessage(`{"command": "git status"}`)
	if err := pm.CheckPermission(context.Background(), "bash", input); err != nil {
		t.Fatalf("git status 应被允许: %v", err)
	}

	// rm 命令命中 deny 规则时应转入审批
	input = json.RawMessage(`{"command": "rm -rf /tmp"}`)
	if err := pm.CheckPermission(context.Background(), "bash", input); err != nil {
		t.Fatalf("rm 命令经用户批准后应成功: %v", err)
	}
	if !promptCalled {
		t.Fatal("rm 命令应触发 prompt")
	}

	// 其他命令需要 ask
	promptCalled = false
	input = json.RawMessage(`{"command": "curl http://example.com"}`)
	if err := pm.CheckPermission(context.Background(), "bash", input); err != nil {
		t.Fatalf("curl 命令经用户批准后应成功: %v", err)
	}
	if !promptCalled {
		t.Error("curl 命令应触发 prompt")
	}
}

// ---------------------------------------------------------------------------
// 规则评估顺序测试
// ---------------------------------------------------------------------------

func TestEvaluationOrder(t *testing.T) {
	tests := []struct {
		name    string
		rules   []PermissionRule
		grants  []grantEntry
		tool    string
		input   string
		wantErr bool
	}{
		{
			"粗粒度allow_grant不能覆盖配置deny规则_无审批通道时报错",
			[]PermissionRule{{ToolName: "bash", Action: PermissionDeny}},
			[]grantEntry{{key: grantKey{tool: "bash"}, action: PermissionAllow}},
			"bash",
			`{"command": "ls"}`,
			true,
		},
		{
			"具体pattern_allow_grant可覆盖同pattern配置ask",
			[]PermissionRule{{ToolName: "bash", Pattern: "git *", Action: PermissionAsk}},
			[]grantEntry{{key: grantKey{tool: "bash", pattern: "git *"}, action: PermissionAllow}},
			"bash",
			`{"command": "git status"}`,
			false,
		},
		{
			"配置规则优先于默认ask",
			[]PermissionRule{{ToolName: "bash", Action: PermissionAllow}},
			nil,
			"bash",
			`{"command": "ls"}`,
			false,
		},
		{
			"无匹配规则_默认ask_无promptFn_报错",
			nil,
			nil,
			"unknown",
			`{}`,
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := NewPermissionManager(tt.rules, nil)
			if tt.grants != nil {
				for _, grant := range tt.grants {
					pm.GrantSession(grant.key.tool, grant.key.pattern, grant.action)
				}
			}

			var input json.RawMessage
			if tt.input != "" {
				input = json.RawMessage(tt.input)
			}

			err := pm.CheckPermission(context.Background(), tt.tool, input)
			if tt.wantErr && err == nil {
				t.Fatal("期望错误但未返回")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("不期望错误但返回了: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WithLogger 测试
// ---------------------------------------------------------------------------

func TestWithLogger(t *testing.T) {
	// 确保 nil logger 不会 panic
	pm := NewPermissionManager(nil, nil, WithLogger(nil))
	if pm.logger == nil {
		t.Fatal("nil logger 不应导致 pm.logger 为 nil")
	}
}

// ---------------------------------------------------------------------------
// Reset 清除所有 grants 测试
// ---------------------------------------------------------------------------

func TestReset_ClearsAllGrants(t *testing.T) {
	pm := NewPermissionManager(nil, nil)
	pm.GrantSession("bash", "", PermissionAllow)
	pm.GrantSession("edit", "*.go", PermissionAllow)

	pm.Reset()
	grants := pm.GetGrants()
	if len(grants) != 0 {
		t.Fatalf("Reset 后应无 grants, 得到 %d 个", len(grants))
	}
}

// ---------------------------------------------------------------------------
// 向后兼容 Grant 测试
// ---------------------------------------------------------------------------

func TestGrant_BackwardCompatible(t *testing.T) {
	pm := NewPermissionManager(nil, nil)
	pm.Grant("bash", PermissionAllow)

	grants := pm.GetGrants()
	if grants["bash"] != PermissionAllow {
		t.Fatalf("Grant 应与 GrantSession 行为一致, 得到 %v", grants["bash"])
	}
}

// ---------------------------------------------------------------------------
// skill 空 name 内置放行测试
// ---------------------------------------------------------------------------

func TestExtractInputValue_Skill(t *testing.T) {
	input := json.RawMessage(`{"name": "debug", "arguments": "foo"}`)
	got := extractInputValue("skill", input)
	if got != "debug" {
		t.Errorf("extractInputValue(skill) = %q, 期望 %q", got, "debug")
	}

	// 空 name
	input = json.RawMessage(`{"name": "", "arguments": ""}`)
	got = extractInputValue("skill", input)
	if got != "" {
		t.Errorf("extractInputValue(skill, empty name) = %q, 期望空字符串", got)
	}
}

func TestCheckPermission_SkillEmptyName_AutoAllow(t *testing.T) {
	// 不配置任何规则，内置规则应自动放行 skill 空 name
	promptCalled := false
	promptFn := func(_ context.Context, _ PermissionRequest) (PermissionResponse, error) {
		promptCalled = true
		return PermissionResponse{Granted: true}, nil
	}

	pm := NewPermissionManager(nil, promptFn)

	// skill 空 name（列出技能）应自动放行，不触发 HITL
	input := json.RawMessage(`{"name": "", "arguments": ""}`)
	err := pm.CheckPermission(context.Background(), "skill", input)
	if err != nil {
		t.Fatalf("skill 空 name 应自动放行: %v", err)
	}
	if promptCalled {
		t.Fatal("skill 空 name 不应触发 HITL 审批")
	}
}

func TestCheckPermission_SkillWithName_NotAutoAllow(t *testing.T) {
	// skill 带 name 不应被内置规则自动放行（空 pattern 只匹配空 inputValue）
	promptCalled := false
	promptFn := func(_ context.Context, _ PermissionRequest) (PermissionResponse, error) {
		promptCalled = true
		return PermissionResponse{Granted: true}, nil
	}

	pm := NewPermissionManager(nil, promptFn)

	// skill 带 name 应走默认流程（触发 prompt）
	input := json.RawMessage(`{"name": "debug"}`)
	err := pm.CheckPermission(context.Background(), "skill", input)
	if err != nil {
		t.Fatalf("skill 带 name 经用户批准应成功: %v", err)
	}
	if !promptCalled {
		t.Fatal("skill 带 name 应触发 HITL 审批")
	}
}

func TestCheckPermission_NoPromptFn(t *testing.T) {
	pm := NewPermissionManager(nil, nil)
	err := pm.CheckPermission(context.Background(), "tool", nil)
	if err == nil {
		t.Fatal("无 promptFn 时应返回错误")
	}
	if !strings.Contains(err.Error(), toolruntime.RecoverableToolCallErrorMarker) {
		t.Fatalf("无 promptFn 应返回可恢复错误, got: %v", err)
	}
}
