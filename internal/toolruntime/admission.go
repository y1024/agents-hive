package toolruntime

import (
	"context"

	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/router"
)

// AdmissionsForDefinitions 是 catalog、tool_search、MCP 列表和可见性共享的策略入口。
func AdmissionsForDefinitions(defs []mcphost.ToolDefinition, ctx router.ToolPolicyContext) []Admission {
	if len(defs) == 0 {
		return nil
	}
	out := make([]Admission, 0, len(defs))
	for _, def := range defs {
		out = append(out, Admit(DescriptorFromDefinition(def), ctx))
	}
	return out
}

// ProfilesForDefinitions 返回 RouteDecision 使用的统一 profile 列表。
func ProfilesForDefinitions(defs []mcphost.ToolDefinition) []router.ToolProfile {
	if len(defs) == 0 {
		return nil
	}
	profiles := make([]router.ToolProfile, 0, len(defs))
	for _, def := range defs {
		profiles = append(profiles, DescriptorFromDefinition(def).Profile)
	}
	return profiles
}

// ListAdmissions 从 Provider 读取目录并计算统一准入结果。
func ListAdmissions(ctx context.Context, provider Provider, policyCtx router.ToolPolicyContext) ([]Admission, error) {
	if provider == nil {
		return nil, nil
	}
	descriptors, err := provider.ListToolDescriptors(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Admission, 0, len(descriptors))
	for _, descriptor := range descriptors {
		out = append(out, Admit(descriptor, policyCtx))
	}
	return out, nil
}
