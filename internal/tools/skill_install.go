package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/toolruntime"
)

// ChoiceTypeSkillInstallConfirmation 必须与 internal/skillhitl.ChoiceTypeSkillInstallConfirmation
// 保持字面一致。tools 包不能 import skillhitl（其 import master，master 反向 import tools 会包循环）——
// 因此此处重复一份常量，并在单测里做等值断言防止 spec drift。
const ChoiceTypeSkillInstallConfirmation = "skill_install_confirmation"

// skillInstallInput 是 skill_install 的入参。
//   - Name  : 必填；marketplace 中的 skill 名
//   - Scope : "personal" (默认) | "public"
//   - Source: 可选；覆盖默认 marketplace 解析顺序
//   - Refresh: 是否强制 bypass index cache (TTL=5min)
type skillInstallInput struct {
	Name    string `json:"name"`
	Scope   string `json:"scope,omitempty"`
	Source  string `json:"source,omitempty"`
	Refresh bool   `json:"refresh,omitempty"`
}

// skillInstallRegistry 是 skill_install 对 Registry 的最小接口，*skills.Registry
// 和 *skills.OverlayRegistry 均满足。接口化是为了测试 mock 方便。
type skillInstallRegistry interface {
	RegisterFromPath(ctx context.Context, path string, scope skills.SkillScope, userID string) error
}

// skillInstallBroadcaster 发送 skill.install.progress 事件到 EventBus。
// Master 已实现。传 nil 表示不广播（CLI/单测），handler 广播前统一判空。
type skillInstallBroadcaster interface {
	BroadcastGenericMessage(msgType string, payload interface{})
}

// hitlEmitter 与 mcphost.HITLEmitter 同语义（Host 本身即实现者）。
// tools 内部声明一份接口，是为了让单测 mock 一个 fake emitter，无需构造真 Host。
type hitlEmitter interface {
	EmitInputRequest(ctx context.Context, req mcphost.HITLInputRequest) (*mcphost.HITLInputResponse, error)
}

// skillInstallProgress 是 skill.install.progress 事件 payload。
// stage ∈ {resolving, awaiting_approval, downloading, registering, done, error}。
type skillInstallProgress struct {
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Source    string `json:"source,omitempty"`
	Stage     string `json:"stage"`
	Reason    string `json:"reason,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Path      string `json:"path,omitempty"`
}

// SkillInstallApprovalTimeout 是 input_request 默认超时。
// 0 表示沿用 master HITL 的全局 InputTimeout；tasks.md §6.4 预留此钩子供
// permission-minimalism 方后续拉齐。
var SkillInstallApprovalTimeout time.Duration

// ErrSkillInstallUserDeclined — 用户点"拒绝"时 handler 返回；单测用它做断言。
var ErrSkillInstallUserDeclined = errors.New("user declined skill installation")

// skillInstallDeps 汇聚 handler 所需所有依赖，测试路径直接注入 mock。
type skillInstallDeps struct {
	Logger       *zap.Logger
	Registry     skillInstallRegistry
	Discovery    *skills.Discovery
	Broadcaster  skillInstallBroadcaster
	AdminChecker skills.AdminChecker
	Emitter      hitlEmitter
}

// registerSkillInstall 注册 skill_install 工具。所有依赖通过 deps 注入。
// bootstrap 侧 gated by cfg.Agent.Skills.OnDemandEnabled；on=true 才调用此函数。
func registerSkillInstall(host *mcphost.Host, deps skillInstallDeps) {
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "marketplace 中的 skill 名称",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"personal", "public"},
				"description": "personal (默认，仅当前用户可见) 或 public (全局，admin only)",
			},
			"source": map[string]any{
				"type":        "string",
				"description": "可选，覆盖 marketplace URL",
			},
			"refresh": map[string]any{
				"type":        "boolean",
				"description": "强制刷新 marketplace index 缓存 (TTL=5min)",
			},
		},
		"required": []string{"name"},
	})

	host.RegisterTool(
		mcphost.ToolDefinition{
			Name:        "skill_install",
			Description: "下载并注册 marketplace 中的 skill；触发 HITL approval 后写盘。personal scope 要求已登录；public scope 要求 admin。",
			InputSchema: schema,
		},
		func(ctx context.Context, raw json.RawMessage) (*mcphost.ToolResult, error) {
			return handleSkillInstall(ctx, deps, raw)
		},
	)
	if deps.Logger != nil {
		deps.Logger.Info("已注册 skill_install 工具")
	}
}

// handleSkillInstall 实现 6 阶段 pipeline。
//
// Goroutine 防漏（MAJOR 4）：主 goroutine 全程同步；唯一可阻塞点
// EmitInputRequest 内部 select 已 ctx-aware；PullOne/RegisterFromPath 同步接受 ctx。
// 无主动 spawn 的 background worker → 单测 goleak.VerifyNone 必然通过。
func handleSkillInstall(ctx context.Context, deps skillInstallDeps, raw json.RawMessage) (*mcphost.ToolResult, error) {
	var in skillInstallInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return errorResult("skill_install 输入无效: " + err.Error()), nil
	}
	if strings.TrimSpace(in.Name) == "" {
		return errorResult("skill_install: name 必填"), nil
	}
	if deps.Discovery == nil {
		return errorResult("skill_install: Discovery 未配置（on_demand_enabled 是否为 false？）"), nil
	}

	scope, err := skills.ParseScope(in.Scope)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	// ParseScope("") → ScopePublic；但本工具默认语义是 personal（对齐 tasks.md §6.3），
	// 故只有用户显式写 scope 才用解析结果；空值重置为 personal。
	if strings.TrimSpace(in.Scope) == "" {
		scope = skills.ScopePersonal
	}

	userID := auth.UserIDFrom(ctx)

	if scope == skills.ScopePersonal && userID == "" {
		return errorResult("skill_install: personal scope requires authenticated session"), nil
	}
	if scope == skills.ScopePublic {
		if deps.AdminChecker == nil || !deps.AdminChecker.IsAdmin(ctx, userID) {
			return errorResult("skill_install: public scope requires admin privilege"), nil
		}
	}

	broadcast := func(p skillInstallProgress) {
		if deps.Broadcaster == nil {
			return
		}
		deps.Broadcaster.BroadcastGenericMessage("skill.install.progress", p)
	}

	// Stage 1: resolving
	broadcast(skillInstallProgress{Name: in.Name, Scope: string(scope), Source: in.Source, Stage: "resolving"})

	resolved, rerr := resolveForInstall(ctx, deps.Discovery, in)
	if rerr != nil {
		broadcast(skillInstallProgress{Name: in.Name, Scope: string(scope), Stage: "error", Reason: rerr.Error()})
		return errorResult("skill_install 解析失败: " + rerr.Error()), nil
	}

	// Stage 2: awaiting_approval —— 发 input_request
	broadcast(skillInstallProgress{Name: in.Name, Scope: string(scope), Source: resolved.Source, Stage: "awaiting_approval"})

	approved, aerr := askInstallApproval(ctx, deps.Emitter, in.Name, string(scope), resolved.Source, userID)
	if aerr != nil {
		broadcast(skillInstallProgress{Name: in.Name, Scope: string(scope), Source: resolved.Source, Stage: "error", Reason: aerr.Error()})
		return errorResult(aerr.Error()), nil
	}
	if !approved {
		broadcast(skillInstallProgress{Name: in.Name, Scope: string(scope), Source: resolved.Source, Stage: "error", Reason: "user_declined"})
		return errorResult(ErrSkillInstallUserDeclined.Error()), nil
	}

	// Stage 3: downloading —— PullOne 同步写盘
	broadcast(skillInstallProgress{Name: in.Name, Scope: string(scope), Source: resolved.Source, Stage: "downloading"})
	path, perr := deps.Discovery.PullOne(ctx, resolved.Source, in.Name)
	if perr != nil {
		broadcast(skillInstallProgress{Name: in.Name, Scope: string(scope), Source: resolved.Source, Stage: "error", Reason: perr.Error()})
		return errorResult("skill_install 下载失败: " + perr.Error()), nil
	}

	// Stage 4: registering
	broadcast(skillInstallProgress{Name: in.Name, Scope: string(scope), Source: resolved.Source, Stage: "registering", Path: path})
	if rErr := deps.Registry.RegisterFromPath(ctx, path, scope, userID); rErr != nil {
		broadcast(skillInstallProgress{Name: in.Name, Scope: string(scope), Source: resolved.Source, Stage: "error", Reason: rErr.Error()})
		return errorResult("skill_install 注册失败: " + rErr.Error()), nil
	}

	// Stage 5: done
	broadcast(skillInstallProgress{Name: in.Name, Scope: string(scope), Source: resolved.Source, Stage: "done", Path: path})

	out, _ := json.Marshal(map[string]any{
		"ok":     true,
		"name":   in.Name,
		"scope":  string(scope),
		"source": resolved.Source,
		"path":   path,
	})
	return textResult(string(out)), nil
}

// resolveForInstall 封装"用户显式 source vs 默认 marketplaces"两条解析路径。
func resolveForInstall(ctx context.Context, d *skills.Discovery, in skillInstallInput) (*skills.ResolvedSkill, error) {
	if in.Source != "" {
		// 显式 source：临时把该 URL 覆盖到 marketplaceURLs 做一次解析（不改动 Discovery 状态）
		// 技术上 ResolveByName 遍历 d.marketplaceURLs；显式 source 语义是"只查这一个"。
		// 简化实现：直接尝试 PullOne 的 pre-check——从 Discovery 的 index 拉该 URL 的 index.json。
		// 但 Discovery 没暴露 "resolve from arbitrary URL" 的公开方法；这里走 ResolveByName 的
		// 降级路径：若 d.marketplaceURLs 包含 Source 则 ResolveByName 会命中；否则需要补一条
		// 临时 URL。出于最小侵入，假定管理员已把 source 加入 marketplace_urls 列表，
		// 否则返回更清晰错误。
		entry, err := d.ResolveByName(ctx, in.Name, in.Refresh)
		if err != nil {
			return nil, fmt.Errorf("source %q 未配置于 marketplace_urls 或未找到 skill: %w", in.Source, err)
		}
		if entry.Source != in.Source {
			// ResolveByName 命中了别的 marketplace；用户显式 source 要求精确匹配
			return nil, fmt.Errorf("skill %q 解析到 %q，与请求的 source %q 不一致",
				in.Name, entry.Source, in.Source)
		}
		return entry, nil
	}
	return d.ResolveByName(ctx, in.Name, in.Refresh)
}

// askInstallApproval 发 input_request，解析 action → approved bool。
// action 语义：approve / proceed → true；其他 → false。
//
// emitter 为 nil 或审批请求失败时返回可恢复工具错误；明确拒绝才走 user_declined。
func askInstallApproval(
	ctx context.Context,
	emitter hitlEmitter,
	name, scope, source, userID string,
) (bool, error) {
	if emitter == nil {
		return false, errors.New(toolruntime.RecoverableToolCallErrorContent("approval_channel_missing",
			fmt.Sprintf("skill_install 需要人工确认，但审批通道未初始化。当前 skill 未安装；name=%s；scope=%s；source=%s", name, scope, source)))
	}
	data, _ := json.Marshal(map[string]any{
		"name":           name,
		"scope":          scope,
		"source":         source,
		"admin_required": scope == "public",
		"requested_by":   userID,
	})
	prompt := fmt.Sprintf("安装 skill %q (scope=%s, source=%s)？", name, scope, source)
	req := mcphost.HITLInputRequest{
		Type:       "confirmation",
		Prompt:     prompt,
		Options:    []string{"approve", "decline"},
		Default:    "decline",
		ChoiceType: ChoiceTypeSkillInstallConfirmation,
		Data:       data,
		Timeout:    SkillInstallApprovalTimeout,
	}
	resp, err := emitter.EmitInputRequest(ctx, req)
	if err != nil {
		return false, errors.New(toolruntime.RecoverableToolCallErrorContent("approval_request_failed",
			fmt.Sprintf("skill_install 的审批请求失败。当前 skill 未安装；name=%s；scope=%s；source=%s；error=%s", name, scope, source, err.Error())))
	}
	if resp == nil {
		return false, errors.New(toolruntime.RecoverableToolCallErrorContent("approval_request_failed",
			fmt.Sprintf("skill_install 的审批请求未返回结果。当前 skill 未安装；name=%s；scope=%s；source=%s", name, scope, source)))
	}
	switch strings.ToLower(resp.Action) {
	case "approve", "proceed", "confirm":
		return true, nil
	default:
		return false, nil
	}
}
