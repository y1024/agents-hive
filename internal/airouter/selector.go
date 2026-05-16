package airouter

import (
	"strings"

	"github.com/chef-guo/agents-hive/internal/llm"
)

// selectBestModel 根据任务类型从可用模型中选最优模型
func (r *Router) selectBestModel(task LLMTaskType) *ModelScore {
	return r.selectBestModelWithUserModel(task, "")
}

func (r *Router) selectBestModelWithUserModel(task LLMTaskType, userModelName string) *ModelScore {
	r.mu.RLock()
	defer r.mu.RUnlock()

	switch task {
	case TaskChat:
		return r.userSelectedModelLocked(userModelName)
	case TaskTitle, TaskSummary:
		// 最便宜的、支持 tool use 的模型
		if m := r.cheapestWithCapability("tools"); m != nil {
			return m
		}
		return r.userSelectedModelLocked(userModelName)
	case TaskCodeReview:
		// 最强的推理模型
		if m := r.bestWithCapability("tools"); m != nil {
			return m
		}
		return r.userSelectedModelLocked(userModelName)
	case TaskVision:
		// 有 vision 能力的最优模型
		if m := r.bestWithCapability("vision"); m != nil {
			return m
		}
		return r.userSelectedModelLocked(userModelName)
	case TaskAgent:
		// 子代理需要 tool use
		if m := r.bestWithCapability("tools"); m != nil {
			return m
		}
		return r.userSelectedModelLocked(userModelName)
	case TaskPlanning:
		// spec-driven planner：需要 JSON 结构化输出，最便宜即可（haiku-tier）。
		// 硬约束：绝不回落到 TaskChat——否则 main model 会被 planner 流量偷走预算。
		// Fallback 链：cheapest(json) → cheapest(tools) → TaskSummary fallback（绝不 TaskChat）
		if m := r.cheapestWithCapability("json"); m != nil {
			return m
		}
		if m := r.cheapestWithCapability("tools"); m != nil {
			return m
		}
		return r.userSelectedModelLocked(userModelName)
	default:
		return r.userSelectedModelLocked(userModelName)
	}
}

// userSelectedModelLocked 返回指定会话选定的主对话模型；调用方必须持有 r.mu。
func (r *Router) userSelectedModelLocked(modelName string) *ModelScore {
	if modelName = strings.TrimSpace(modelName); modelName != "" {
		if m := r.findModelLocked(modelName); m != nil {
			return m
		}
	}
	if m := r.findModelLocked(r.userModel); m != nil {
		return m
	}
	// 没有匹配的，返回第一个可用的
	if len(r.models) > 0 {
		return &r.models[0]
	}
	return nil
}

func (r *Router) findModelLocked(modelName string) *ModelScore {
	for i := range r.models {
		if r.models[i].Name == modelName {
			return &r.models[i]
		}
	}
	return nil
}

// cheapestWithCapability 找最便宜的、满足所有能力要求的模型
func (r *Router) cheapestWithCapability(caps ...string) *ModelScore {
	var best *ModelScore
	for i := range r.models {
		m := &r.models[i]
		if !m.HasAllCapabilities(caps...) {
			continue
		}
		if best == nil || m.CostTier < best.CostTier {
			best = m
		}
	}
	return best
}

// bestWithCapability 找最强的（最贵的）、满足所有能力要求的模型
func (r *Router) bestWithCapability(caps ...string) *ModelScore {
	var best *ModelScore
	for i := range r.models {
		m := &r.models[i]
		if !m.HasAllCapabilities(caps...) {
			continue
		}
		if best == nil || m.CostTier > best.CostTier {
			best = m
		}
	}
	return best
}

// inferCostTier 根据模型元数据和名称推断成本层级
func inferCostTier(modelID string) CostTier {
	lower := strings.ToLower(modelID)

	// 检查静态注册表
	if meta := llm.GetModelMeta(modelID); meta != nil {
		// 按输出 token 价格判断
		if meta.CostPerOutputToken > 0 {
			switch {
			case meta.CostPerOutputToken <= 0.002: // $2/M tokens 以下
				return TierCheap
			case meta.CostPerOutputToken <= 0.015: // $15/M tokens 以下
				return TierMedium
			default:
				return TierExpensive
			}
		}
	}

	// 名称推断
	switch {
	case strings.Contains(lower, "mini") || strings.Contains(lower, "small") ||
		strings.Contains(lower, "haiku") || strings.Contains(lower, "flash"):
		return TierCheap
	case strings.Contains(lower, "o1") || strings.Contains(lower, "o3") ||
		strings.Contains(lower, "opus") || strings.Contains(lower, "pro"):
		return TierExpensive
	default:
		return TierMedium
	}
}

// inferCapabilities 根据模型元数据推断能力列表
func inferCapabilities(modelID string, providerCaps map[string]bool) []string {
	var caps []string

	meta := llm.GetModelMeta(modelID)
	if meta != nil {
		if meta.Capabilities.Vision {
			caps = append(caps, "vision")
		}
		if meta.Capabilities.ToolUse {
			caps = append(caps, "tools")
		}
		if meta.Capabilities.Reasoning {
			caps = append(caps, "reasoning")
		}
		if meta.Capabilities.JSON {
			caps = append(caps, "json")
		}
		if meta.Capabilities.Audio {
			caps = append(caps, "audio")
		}
		if meta.Capabilities.PDF {
			caps = append(caps, "pdf")
		}
		if meta.Capabilities.Streaming {
			caps = append(caps, "streaming")
		}
		if meta.Capabilities.PromptCaching {
			caps = append(caps, "prompt_caching")
		}
	}

	// 从 provider 能力补充
	if providerCaps != nil {
		for cap, supported := range providerCaps {
			if supported && !containsString(caps, cap) {
				caps = append(caps, cap)
			}
		}
	}

	// 如果没有任何元数据，给予基本能力假设
	if len(caps) == 0 {
		caps = append(caps, "tools", "json")
	}

	return caps
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
