package skills

import (
	"strings"

	"github.com/chef-guo/agents-hive/internal/router"
)

// ToolPolicyInput 是构建 ToolPolicy 所需的纯数据输入，
// 避免 skills 包直接依赖 config 包（config → skills 已有依赖，反向会循环引用）。
type ToolPolicyInput struct {
	Groups           []ToolGroupInput
	Profiles         []ToolProfileInput
	GlobalDeny       []string
	SubagentDeny     []string
	SubagentLeafDeny []string
	MasterProfile    string
}

// ToolGroupInput 工具分组定义
type ToolGroupInput struct {
	Name  string
	Tools []string
}

// ToolProfileInput 工具 Profile 定义
type ToolProfileInput struct {
	Name  string
	Tools []string // 支持 "group:xxx" 引用
}

// ToolPolicy 基于配置驱动的工具过滤策略引擎。
// 支持 Profile（命名工具集）、Group（工具分组）和多层 deny 列表。
type ToolPolicy struct {
	groups           map[string][]string // group name → tool names
	profiles         map[string][]string // profile name → 展开后的工具名列表
	globalDeny       map[string]bool
	subagentDeny     map[string]bool
	subagentLeafDeny map[string]bool
	masterProfile    string
	warnings         []string // 构建时发现的配置问题
}

// NewToolPolicy 从输入数据构建 ToolPolicy。
// 如果输入为零值，所有方法都返回不限制的结果（向后兼容）。
// 构建时验证所有引用的完整性，问题通过 Warnings() 获取。
func NewToolPolicy(input ToolPolicyInput) *ToolPolicy {
	p := &ToolPolicy{
		groups:        make(map[string][]string),
		profiles:      make(map[string][]string),
		masterProfile: input.MasterProfile,
	}

	// 构建 group 索引
	for _, g := range input.Groups {
		p.groups[g.Name] = g.Tools
	}

	// 构建 profile 索引（展开 group 引用）
	for _, pr := range input.Profiles {
		p.profiles[pr.Name] = p.expandGroupsValidated(pr.Tools, "profile "+pr.Name)
	}

	// 构建 deny 集合
	p.globalDeny = toSet(input.GlobalDeny)
	p.subagentDeny = toSet(input.SubagentDeny)
	p.subagentLeafDeny = toSet(input.SubagentLeafDeny)

	// 验证 masterProfile 引用
	if input.MasterProfile != "" {
		if _, ok := p.profiles[input.MasterProfile]; !ok {
			p.warnings = append(p.warnings, "master_profile references unknown profile: "+input.MasterProfile)
		}
	}

	return p
}

// Warnings 返回构建时发现的配置问题。
// 空切片表示配置无问题。
func (p *ToolPolicy) Warnings() []string {
	if p == nil {
		return nil
	}
	return p.warnings
}

// ExpandGroups 将工具列表中的 "group:xxx" 和 "profile:xxx" 引用展开为具体工具名。
// 不匹配的前缀原样保留。
func (p *ToolPolicy) ExpandGroups(tools []string) []string {
	if p == nil {
		return tools
	}
	return p.expandGroups(tools)
}

func (p *ToolPolicy) expandGroups(tools []string) []string {
	return p.expandGroupsValidated(tools, "")
}

func (p *ToolPolicy) expandGroupsValidated(tools []string, context string) []string {
	var result []string
	for _, t := range tools {
		switch {
		case t == "*":
			return []string{"*"}
		case strings.HasPrefix(t, "group:"):
			groupName := strings.TrimPrefix(t, "group:")
			if members, ok := p.groups[groupName]; ok {
				result = append(result, members...)
			} else if context != "" {
				p.warnings = append(p.warnings, context+" references unknown group: "+groupName)
			}
		case strings.HasPrefix(t, "profile:"):
			profileName := strings.TrimPrefix(t, "profile:")
			if members, ok := p.profiles[profileName]; ok {
				result = append(result, members...)
			} else if context != "" {
				p.warnings = append(p.warnings, context+" references unknown profile: "+profileName)
			}
		default:
			result = append(result, t)
		}
	}
	return result
}

// BuildFilter 组合多层策略生成最终 ToolFilter。
//
// Pipeline（漏斗模型）:
//  1. Profile 过滤（选基础集）
//  2. agentTools 交集（agent 定义的 tools 字段进一步收窄）
//  3. Global Deny
//  4. Subagent Deny（仅 isSubagent 时）
//  5. Leaf Deny（仅 isLeaf 时）
//
// profileName 为空则不通过 profile 限制。
// agentTools 为空则不通过 agent 限制。
// 如果策略引擎为 nil 或未配置，返回 nil（不限制）。
func (p *ToolPolicy) BuildFilter(profileName string, agentTools []string, isSubagent, isLeaf bool) *ToolFilter {
	if p == nil {
		return nil
	}

	// [1] Profile 过滤
	var allowed []string
	isWildcard := false
	if profileName != "" {
		if profileTools, ok := p.profiles[profileName]; ok {
			for _, t := range profileTools {
				if t == "*" {
					isWildcard = true
					break
				}
			}
			if !isWildcard {
				allowed = profileTools
			}
		}
	}

	// [2] Agent Tools 交集
	if len(agentTools) > 0 {
		for _, t := range agentTools {
			if t == "*" {
				// agent 不限制
				agentTools = nil
				break
			}
		}
		if agentTools != nil {
			if len(allowed) > 0 {
				// 取交集
				agentSet := toSet(agentTools)
				var intersection []string
				for _, t := range allowed {
					if agentSet[t] {
						intersection = append(intersection, t)
					}
				}
				allowed = intersection
			} else {
				// profile 不限制，直接用 agentTools
				allowed = agentTools
			}
		}
	}

	// [3-5] 构建 deny 列表
	var denied []string
	allowedInputs := map[string]map[string]string{}
	for t := range p.globalDeny {
		denied = append(denied, t)
	}
	if isSubagent {
		for t := range p.subagentDeny {
			denied = append(denied, t)
		}
	}
	if isLeaf {
		for t := range p.subagentLeafDeny {
			denied = append(denied, t)
		}
		if inputs := router.MixedAllowedToolInputsForIntent(router.IntentFrame{Kind: router.IntentRead}, "filesystem"); len(inputs) > 0 {
			allowedInputs["filesystem"] = inputs
		}
	}

	// 如果既没有 allow 限制也没有 deny 限制，返回 nil（不过滤）
	if len(allowed) == 0 && len(denied) == 0 && len(allowedInputs) == 0 {
		return nil
	}

	return NewToolFilterWithDenyAndInputs(allowed, denied, allowedInputs)
}

// MasterFilter 构建 Master agent 专用的 ToolFilter。
// 使用配置中的 MasterProfile。
func (p *ToolPolicy) MasterFilter() *ToolFilter {
	if p == nil {
		return nil
	}
	return p.BuildFilter(p.masterProfile, nil, false, false)
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}
