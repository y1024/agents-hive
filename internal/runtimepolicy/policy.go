package runtimepolicy

import (
	"fmt"
	"time"
)

// Policy 定义运行期 timeout、容量和成本上限。
type Policy struct {
	LLMCallTimeout      time.Duration `json:"llm_call_timeout"`
	ToolTimeout         time.Duration `json:"tool_timeout"`
	TaskTimeout         time.Duration `json:"task_timeout"`
	SpawnAgentTimeout   time.Duration `json:"spawn_agent_timeout"`
	ACPPromptTimeout    time.Duration `json:"acp_prompt_timeout"`
	ACPReconnectTimeout time.Duration `json:"acp_reconnect_timeout"`
	SubagentMaxTurns    int           `json:"subagent_max_turns"`
	SubagentMaxDepth    int           `json:"subagent_max_depth"`
	PerSessionParallel  int           `json:"per_session_parallel"`
	GlobalWorkers       int           `json:"global_workers"`
	MaxSessionCostUSD   float64       `json:"max_session_cost_usd"`
}

// AuthorizesBusinessDomainTools 固定返回 false，避免把运行期资源策略误当成业务域授权来源。
func (p Policy) AuthorizesBusinessDomainTools() bool {
	_ = p
	return false
}

// Default 返回默认运行策略。
func Default() Policy {
	return Policy{
		LLMCallTimeout:      5 * time.Minute,
		ToolTimeout:         2 * time.Minute,
		TaskTimeout:         30 * time.Minute,
		SpawnAgentTimeout:   30 * time.Minute,
		ACPPromptTimeout:    30 * time.Minute,
		ACPReconnectTimeout: 10 * time.Second,
		SubagentMaxTurns:    30,
		SubagentMaxDepth:    1,
		PerSessionParallel:  1,
		GlobalWorkers:       50,
		MaxSessionCostUSD:   0,
	}
}

// WithDefaults 用默认值填充未配置字段。
func (p Policy) WithDefaults() Policy {
	defaults := Default()
	if p.LLMCallTimeout == 0 {
		p.LLMCallTimeout = defaults.LLMCallTimeout
	}
	if p.ToolTimeout == 0 {
		p.ToolTimeout = defaults.ToolTimeout
	}
	if p.TaskTimeout == 0 {
		p.TaskTimeout = defaults.TaskTimeout
	}
	if p.SpawnAgentTimeout == 0 {
		p.SpawnAgentTimeout = defaults.SpawnAgentTimeout
	}
	if p.ACPPromptTimeout == 0 {
		p.ACPPromptTimeout = defaults.ACPPromptTimeout
	}
	if p.ACPReconnectTimeout == 0 {
		p.ACPReconnectTimeout = defaults.ACPReconnectTimeout
	}
	if p.SubagentMaxTurns == 0 {
		p.SubagentMaxTurns = defaults.SubagentMaxTurns
	}
	if p.SubagentMaxDepth == 0 {
		p.SubagentMaxDepth = defaults.SubagentMaxDepth
	}
	if p.PerSessionParallel == 0 {
		p.PerSessionParallel = defaults.PerSessionParallel
	}
	if p.GlobalWorkers == 0 {
		p.GlobalWorkers = defaults.GlobalWorkers
	}
	if p.MaxSessionCostUSD <= 0 {
		p.MaxSessionCostUSD = defaults.MaxSessionCostUSD
	}
	return p
}

// Validate 校验运行策略是否可用。
func (p Policy) Validate() error {
	if p.LLMCallTimeout < 0 {
		return fmt.Errorf("runtime policy timeout cannot be negative")
	}
	if p.ToolTimeout < 0 {
		return fmt.Errorf("runtime policy timeout cannot be negative")
	}
	if p.TaskTimeout < 0 {
		return fmt.Errorf("runtime policy timeout cannot be negative")
	}
	if p.SpawnAgentTimeout < 0 {
		return fmt.Errorf("runtime policy timeout cannot be negative")
	}
	if p.ACPPromptTimeout < 0 {
		return fmt.Errorf("runtime policy timeout cannot be negative")
	}
	if p.ACPReconnectTimeout < 0 {
		return fmt.Errorf("runtime policy timeout cannot be negative")
	}
	if p.SubagentMaxTurns < 0 {
		return fmt.Errorf("runtime policy limits cannot be negative")
	}
	if p.SubagentMaxDepth < 0 {
		return fmt.Errorf("runtime policy limits cannot be negative")
	}
	if p.PerSessionParallel < 0 {
		return fmt.Errorf("runtime policy limits cannot be negative")
	}
	if p.GlobalWorkers < 0 {
		return fmt.Errorf("runtime policy limits cannot be negative")
	}
	if p.MaxSessionCostUSD < 0 {
		return fmt.Errorf("runtime policy cost cannot be negative")
	}
	return nil
}
