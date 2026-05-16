package bootstrap

import (
	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/master"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/subagent"
	"github.com/chef-guo/agents-hive/internal/subagent/compaction"
	"github.com/chef-guo/agents-hive/internal/subagent/explore"
	"github.com/chef-guo/agents-hive/internal/subagent/summary"
	"github.com/chef-guo/agents-hive/internal/subagent/title"
)

// AgentRegistryConfig 是注册固定 Agent 所需的依赖集合
type AgentRegistryConfig struct {
	SkillReg           *skills.Registry
	LLMClient          *llm.Client                           // 静态 LLM client（CLI 路径使用）
	LLMResolvers       map[string]subagent.LLMClientResolver // 按 agent 类型的动态 LLM resolver（Server 路径使用，优先于 LLMClient）
	ToolBridge         *skills.ToolBridge
	PermMgr            *skills.PermissionManager
	Callbacks          subagent.AgentCallbacks
	ContextCompression config.CompactionConfig
	PromptLoader       any // *i18n.PromptLoader（可选，用于 prompt 外部化；用 any 避免循环依赖）
	Logger             *zap.Logger
}

// RegisterFixedAgents 向 Master 注册所有固定 Agent。
// CLI 和 Server 都调用此函数，消除双份维护风险。
// 返回 compaction agent，供调用方在记忆系统初始化后调用 SetMemoryExtractor。
//
// 当 LLMResolvers 非空时，各 agent 使用对应的 resolver 动态获取 LLM client（走 AIRouter task-type 选路）；
// 否则 fallback 到静态 LLMClient（向后兼容 CLI 路径）。
//
// 注册顺序：
//  1. explore（spawn_agent 模板，只读探索）
//  2. 系统服务 Agent（compaction / title / summary）
func RegisterFixedAgents(m *master.Master, cfg AgentRegistryConfig) *compaction.Agent {
	logger := cfg.Logger

	// explore — 使用 TaskAgent 类型的 resolver
	if r, ok := cfg.LLMResolvers["explore"]; ok {
		exploreAgent := explore.NewWithPromptLoader(cfg.SkillReg, r, nil, cfg.ToolBridge, cfg.PermMgr, cfg.PromptLoader, logger, cfg.Callbacks)
		m.RegisterAgent(exploreAgent)
	} else if cfg.LLMClient != nil {
		exploreAgent := explore.NewWithPromptLoader(cfg.SkillReg, nil, cfg.LLMClient, cfg.ToolBridge, cfg.PermMgr, cfg.PromptLoader, logger, cfg.Callbacks)
		m.RegisterAgent(exploreAgent)
	}

	// title — 使用 TaskTitle 类型的 resolver（最便宜的模型）
	if r, ok := cfg.LLMResolvers["title"]; ok {
		titleAgent := title.NewWithResolver(r, logger, cfg.Callbacks)
		if cfg.PromptLoader != nil {
			titleAgent.SetPromptLoader(cfg.PromptLoader)
		}
		m.RegisterAgent(titleAgent)
	} else if cfg.LLMClient != nil {
		titleAgent := title.New(cfg.LLMClient, logger, cfg.Callbacks)
		if cfg.PromptLoader != nil {
			titleAgent.SetPromptLoader(cfg.PromptLoader)
		}
		m.RegisterAgent(titleAgent)
	}

	// summary — 使用 TaskSummary 类型的 resolver（便宜的模型）
	if r, ok := cfg.LLMResolvers["summary"]; ok {
		summaryAgent := summary.NewWithResolver(r, logger, cfg.Callbacks)
		if cfg.PromptLoader != nil {
			summaryAgent.SetPromptLoader(cfg.PromptLoader)
		}
		m.RegisterAgent(summaryAgent)
	} else if cfg.LLMClient != nil {
		summaryAgent := summary.New(cfg.LLMClient, logger, cfg.Callbacks)
		if cfg.PromptLoader != nil {
			summaryAgent.SetPromptLoader(cfg.PromptLoader)
		}
		m.RegisterAgent(summaryAgent)
	}

	// compaction — 使用 TaskSummary 类型的 resolver（压缩摘要走便宜模型）
	var compactionAgent *compaction.Agent
	if r, ok := cfg.LLMResolvers["compaction"]; ok {
		compactionAgent = compaction.NewWithResolver(r, cfg.ContextCompression, logger, cfg.Callbacks)
	} else if cfg.LLMClient != nil {
		compactionAgent = compaction.New(cfg.LLMClient, cfg.ContextCompression, logger, cfg.Callbacks)
	}
	if compactionAgent != nil && cfg.PromptLoader != nil {
		compactionAgent.SetPromptLoader(cfg.PromptLoader)
	}
	if compactionAgent != nil {
		m.RegisterAgent(compactionAgent)
	}

	logger.Info("固定 Agent 注册完成",
		zap.Strings("agents", []string{"explore", "compaction", "title", "summary"}),
		zap.Bool("dynamic_routing", len(cfg.LLMResolvers) > 0),
	)
	return compactionAgent
}
