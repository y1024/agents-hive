package toolruntime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chef-guo/agents-hive/internal/mcphost"
)

// MCPHostAdapter 把现有 mcphost.Host 包装成统一工具 provider/invoker。
type MCPHostAdapter struct {
	Host *mcphost.Host
}

func NewMCPHostAdapter(host *mcphost.Host) MCPHostAdapter {
	return MCPHostAdapter{Host: host}
}

func (a MCPHostAdapter) ListToolDescriptors(context.Context) ([]Descriptor, error) {
	if a.Host == nil {
		return nil, nil
	}
	defs := a.Host.ListTools()
	out := make([]Descriptor, 0, len(defs))
	for _, def := range defs {
		out = append(out, DescriptorFromDefinition(def))
	}
	return out, nil
}

func (a MCPHostAdapter) LookupToolDescriptor(_ context.Context, name string) (Descriptor, bool) {
	if a.Host == nil {
		return Descriptor{}, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Descriptor{}, false
	}
	def, err := a.Host.GetTool(name)
	if err != nil || def == nil {
		return Descriptor{}, false
	}
	return DescriptorFromDefinition(*def), true
}

func (a MCPHostAdapter) InvokeTool(ctx context.Context, invocation Invocation) (*mcphost.ToolResult, error) {
	if a.Host == nil {
		return nil, nil
	}
	return a.Host.ExecuteTool(ctx, invocation.Name, json.RawMessage(invocation.Arguments))
}

// InvokeHostTool 是现有执行路径迁移到统一 invoker 的轻量入口。
func InvokeHostTool(ctx context.Context, host *mcphost.Host, name string, args json.RawMessage) (*mcphost.ToolResult, error) {
	return NewMCPHostAdapter(host).InvokeTool(ctx, Invocation{Name: name, Arguments: args})
}
