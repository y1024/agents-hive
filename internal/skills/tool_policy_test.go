package skills

import (
	"strings"
	"testing"

	"github.com/chef-guo/agents-hive/internal/mcphost"
)

func testPolicyInput() ToolPolicyInput {
	return ToolPolicyInput{
		Groups: []ToolGroupInput{
			{Name: "fs", Tools: []string{"read_file", "write_file", "edit", "glob", "grep"}},
			{Name: "runtime", Tools: []string{"bash"}},
			{Name: "web", Tools: []string{"websearch", "webfetch"}},
		},
		Profiles: []ToolProfileInput{
			{Name: "full", Tools: []string{"*"}},
			{Name: "coding", Tools: []string{"group:fs", "group:runtime", "group:web", "skill"}},
			{Name: "readonly", Tools: []string{"read_file", "glob", "grep"}},
		},
		GlobalDeny:       []string{"remove_tool"},
		SubagentDeny:     []string{"spawn_agent", "create_tool"},
		SubagentLeafDeny: []string{"parallel_dispatch"},
		MasterProfile:    "coding",
	}
}

func TestExpandGroups(t *testing.T) {
	p := NewToolPolicy(testPolicyInput())

	tests := []struct {
		name   string
		input  []string
		expect []string
	}{
		{
			name:   "展开 group 引用",
			input:  []string{"group:fs"},
			expect: []string{"read_file", "write_file", "edit", "glob", "grep"},
		},
		{
			name:   "展开 profile 引用",
			input:  []string{"profile:readonly"},
			expect: []string{"read_file", "glob", "grep"},
		},
		{
			name:   "混合引用和直接工具名",
			input:  []string{"group:runtime", "skill"},
			expect: []string{"bash", "skill"},
		},
		{
			name:   "通配符直接返回",
			input:  []string{"*"},
			expect: []string{"*"},
		},
		{
			name:   "未知 group 被忽略",
			input:  []string{"group:unknown", "bash"},
			expect: []string{"bash"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.ExpandGroups(tt.input)
			if len(got) != len(tt.expect) {
				t.Fatalf("长度不匹配: got %v, want %v", got, tt.expect)
			}
			for i, v := range got {
				if v != tt.expect[i] {
					t.Errorf("index %d: got %q, want %q", i, v, tt.expect[i])
				}
			}
		})
	}
}

func TestBuildFilter_MasterProfile(t *testing.T) {
	p := NewToolPolicy(testPolicyInput())
	filter := p.MasterFilter()

	if filter == nil {
		t.Fatal("MasterFilter 不应为 nil")
	}

	// coding profile 应包含 fs + runtime + web + skill
	allowed := []string{"read_file", "write_file", "edit", "glob", "grep", "bash", "websearch", "webfetch", "skill"}
	for _, name := range allowed {
		if !filter.IsAllowed(name) {
			t.Errorf("工具 %q 应被允许", name)
		}
	}

	// 不在 coding profile 中的工具应被拒绝
	denied := []string{"spawn_agent", "create_tool", "memory"}
	for _, name := range denied {
		if filter.IsAllowed(name) {
			t.Errorf("工具 %q 不应被允许", name)
		}
	}

	// global deny 的工具即使在 profile 中也应被拒绝
	if filter.IsAllowed("remove_tool") {
		t.Error("remove_tool 在 global deny 中，不应被允许")
	}
}

func TestBuildFilter_FullProfile(t *testing.T) {
	input := testPolicyInput()
	input.MasterProfile = "full"
	p := NewToolPolicy(input)
	filter := p.MasterFilter()

	// full profile 只有 global deny 限制
	if filter == nil {
		t.Fatal("有 global deny 时 MasterFilter 不应为 nil")
	}

	// 任意工具应被允许（除了 global deny）
	if !filter.IsAllowed("spawn_agent") {
		t.Error("full profile 应允许 spawn_agent")
	}
	if filter.IsAllowed("remove_tool") {
		t.Error("remove_tool 在 global deny 中")
	}
}

func TestBuildFilter_SubagentDeny(t *testing.T) {
	p := NewToolPolicy(testPolicyInput())

	// 非叶子 subagent
	filter := p.BuildFilter("", []string{"read_file", "spawn_agent", "parallel_dispatch"}, true, false)
	if filter == nil {
		t.Fatal("filter 不应为 nil")
	}
	if filter.IsAllowed("spawn_agent") {
		t.Error("spawn_agent 在 subagent deny 中")
	}
	if !filter.IsAllowed("parallel_dispatch") {
		t.Error("非叶子 subagent 应允许 parallel_dispatch")
	}
	if !filter.IsAllowed("read_file") {
		t.Error("read_file 应被允许")
	}

	// 叶子 subagent
	leafFilter := p.BuildFilter("", []string{"read_file", "parallel_dispatch"}, true, true)
	if leafFilter.IsAllowed("parallel_dispatch") {
		t.Error("叶子 subagent 不应允许 parallel_dispatch")
	}
}

func TestBuildFilter_LeafConstrainsFilesystemToReadActions(t *testing.T) {
	p := NewToolPolicy(testPolicyInput())

	filter := p.BuildFilter("", nil, true, true)
	if filter == nil {
		t.Fatal("leaf subagent filter 不应为 nil")
	}
	inputs := filter.AllowedToolInputsSnapshot()
	actions := inputs["filesystem"]["action"]
	for _, action := range []string{"list", "glob", "grep", "read"} {
		if !containsPipeAction(actions, action) {
			t.Fatalf("leaf filesystem input constraints missing %q: %#v", action, inputs)
		}
	}
	for _, action := range []string{"write", "edit", "multiedit"} {
		if containsPipeAction(actions, action) {
			t.Fatalf("leaf filesystem input constraints must not allow %q: %q", action, actions)
		}
	}
}

func TestBuildFilter_NilPolicy(t *testing.T) {
	var p *ToolPolicy
	filter := p.BuildFilter("coding", nil, false, false)
	if filter != nil {
		t.Error("nil policy 应返回 nil filter")
	}
	filter = p.MasterFilter()
	if filter != nil {
		t.Error("nil policy 的 MasterFilter 应返回 nil")
	}
}

func TestBuildFilter_EmptyInput(t *testing.T) {
	p := NewToolPolicy(ToolPolicyInput{})
	filter := p.MasterFilter()
	if filter != nil {
		t.Error("空配置应返回 nil filter（不限制）")
	}
}

func TestBuildFilter_AgentToolsIntersection(t *testing.T) {
	p := NewToolPolicy(testPolicyInput())

	// coding profile + agent 限制取交集
	filter := p.BuildFilter("coding", []string{"read_file", "bash", "memory"}, false, false)
	if filter == nil {
		t.Fatal("filter 不应为 nil")
	}

	if !filter.IsAllowed("read_file") {
		t.Error("read_file 应被允许（交集）")
	}
	if !filter.IsAllowed("bash") {
		t.Error("bash 应被允许（交集）")
	}
	if filter.IsAllowed("memory") {
		t.Error("memory 不在 coding profile 中，不应被允许")
	}
	if filter.IsAllowed("websearch") {
		t.Error("websearch 不在 agentTools 中，不应被允许")
	}
}

func TestToolFilterWithDeny(t *testing.T) {
	// deny 优先于 allow
	filter := NewToolFilterWithDeny(
		[]string{"read_file", "bash", "remove_tool"},
		[]string{"remove_tool"},
	)
	if !filter.IsAllowed("read_file") {
		t.Error("read_file 应被允许")
	}
	if filter.IsAllowed("remove_tool") {
		t.Error("remove_tool 在 deny 中，即使在 allow 中也应被拒绝")
	}

	// 只有 deny，没有 allow → 允许所有不在 deny 中的
	denyOnly := NewToolFilterWithDeny(nil, []string{"dangerous"})
	if !denyOnly.IsAllowed("anything") {
		t.Error("无 allow 列表时应允许不在 deny 中的工具")
	}
	if denyOnly.IsAllowed("dangerous") {
		t.Error("dangerous 在 deny 中")
	}
}

func TestFilterTools(t *testing.T) {
	tools := []mcphost.ToolDefinition{
		{Name: "read_file"},
		{Name: "bash"},
		{Name: "spawn_agent"},
		{Name: "remove_tool"},
	}

	filter := NewToolFilterWithDeny(
		[]string{"read_file", "bash", "remove_tool"},
		[]string{"remove_tool"},
	)
	filtered := filter.FilterTools(tools)

	if len(filtered) != 2 {
		t.Fatalf("期望 2 个工具, 得到 %d: %v", len(filtered), filtered)
	}
	names := make(map[string]bool)
	for _, tool := range filtered {
		names[tool.Name] = true
	}
	if !names["read_file"] || !names["bash"] {
		t.Errorf("过滤结果不正确: %v", names)
	}
}

func TestNewToolPolicy_Warnings(t *testing.T) {
	// 未知 masterProfile
	input := ToolPolicyInput{
		Groups: []ToolGroupInput{
			{Name: "fs", Tools: []string{"read_file"}},
		},
		Profiles: []ToolProfileInput{
			{Name: "coding", Tools: []string{"group:fs"}},
		},
		MasterProfile: "codign", // typo
	}
	p := NewToolPolicy(input)
	warnings := p.Warnings()
	if len(warnings) == 0 {
		t.Fatal("应检测到 masterProfile typo")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "codign") {
			found = true
		}
	}
	if !found {
		t.Errorf("warning 应包含 typo 的 profile 名: %v", warnings)
	}

	// 未知 group 引用
	input2 := ToolPolicyInput{
		Groups: []ToolGroupInput{
			{Name: "fs", Tools: []string{"read_file"}},
		},
		Profiles: []ToolProfileInput{
			{Name: "coding", Tools: []string{"group:nonexistent"}},
		},
		MasterProfile: "coding",
	}
	p2 := NewToolPolicy(input2)
	warnings2 := p2.Warnings()
	if len(warnings2) == 0 {
		t.Fatal("应检测到未知 group 引用")
	}
	found2 := false
	for _, w := range warnings2 {
		if strings.Contains(w, "nonexistent") {
			found2 = true
		}
	}
	if !found2 {
		t.Errorf("warning 应包含未知 group 名: %v", warnings2)
	}

	// 正常配置应无 warning
	p3 := NewToolPolicy(testPolicyInput())
	if len(p3.Warnings()) != 0 {
		t.Errorf("正常配置不应有 warning: %v", p3.Warnings())
	}
}

func TestBuildFilter_ThreeLayerCombo(t *testing.T) {
	p := NewToolPolicy(testPolicyInput())

	// coding profile + agent 只要 [read_file, spawn_agent, bash] + subagent deny [spawn_agent]
	// 预期: read_file（在 profile 交集中且不在 deny 中）, bash（同理）
	// spawn_agent 在 coding profile 中不存在，且在 subagent deny 中 → 被拒绝
	filter := p.BuildFilter("coding", []string{"read_file", "spawn_agent", "bash"}, true, false)
	if filter == nil {
		t.Fatal("三层组合 filter 不应为 nil")
	}

	if !filter.IsAllowed("read_file") {
		t.Error("read_file 应被允许（在 profile 和 agentTools 中）")
	}
	if !filter.IsAllowed("bash") {
		t.Error("bash 应被允许（在 profile 和 agentTools 中）")
	}
	if filter.IsAllowed("spawn_agent") {
		t.Error("spawn_agent 不应被允许（不在 coding profile 中，且在 subagent deny 中）")
	}
	if filter.IsAllowed("websearch") {
		t.Error("websearch 不应被允许（在 profile 中但不在 agentTools 中）")
	}
	if filter.IsAllowed("remove_tool") {
		t.Error("remove_tool 不应被允许（在 global deny 中）")
	}
}

func containsPipeAction(actions, want string) bool {
	for _, action := range strings.Split(actions, "|") {
		if strings.TrimSpace(action) == want {
			return true
		}
	}
	return false
}
