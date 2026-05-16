package toolruntime

import (
	"context"
	"encoding/json"

	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/router"
)

// Descriptor 是所有工具来源进入路由、展示和执行层前的统一事实对象。
type Descriptor struct {
	Definition mcphost.ToolDefinition `json:"definition"`
	Profile    router.ToolProfile     `json:"profile"`
	Entry      router.CapabilityEntry `json:"entry"`
}

// Admission 是基于 Descriptor 和当前上下文得到的统一策略投影。
type Admission struct {
	Descriptor Descriptor                `json:"descriptor"`
	Policy     router.ToolPolicyDecision `json:"policy"`
}

// Provider 统一 MCP、内置工具、Skill、IM 和自定义工具的目录读取接口。
type Provider interface {
	ListToolDescriptors(context.Context) ([]Descriptor, error)
	LookupToolDescriptor(context.Context, string) (Descriptor, bool)
}

// Invoker 统一工具执行接口。执行前的准入、审批和审计由上层 Runtime 组合。
type Invoker interface {
	InvokeTool(context.Context, Invocation) (*mcphost.ToolResult, error)
}

// Invocation 是工具执行前的最小结构化输入。
type Invocation struct {
	Name      string               `json:"name"`
	Arguments json.RawMessage      `json:"arguments,omitempty"`
	Intent    router.IntentFrame   `json:"intent,omitempty"`
	Route     router.RouteDecision `json:"route,omitempty"`
}

// Admit 使用统一策略层计算工具在当前上下文下的准入结果。
func Admit(descriptor Descriptor, ctx router.ToolPolicyContext) Admission {
	return Admission{
		Descriptor: descriptor,
		Policy:     router.EvaluateToolPolicy(descriptor.Profile, ctx),
	}
}

// DescriptorFromDefinition 从现有 mcphost.ToolDefinition 生成统一 descriptor。
func DescriptorFromDefinition(def mcphost.ToolDefinition) Descriptor {
	profile := router.InferToolProfile(def, router.ProfileHint{})
	return Descriptor{
		Definition: def,
		Profile:    profile,
		Entry:      profile.Entry(),
	}
}
