package bootstrap

import (
	"testing"

	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/master"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/store"
	"github.com/chef-guo/agents-hive/internal/subagent"
	"go.uber.org/zap"
)

func TestRegisterFixedAgentsSkipsNilCompactionAgentWhenNoLLMAvailable(t *testing.T) {
	logger := zap.NewNop()
	m := master.NewMaster(
		master.Config{},
		config.HITLConfig{Enabled: false},
		subagent.NewRegistry(logger),
		skills.NewRegistry(logger),
		store.NewMemoryStore(),
		logger,
	)

	agent := RegisterFixedAgents(m, AgentRegistryConfig{
		SkillReg:           skills.NewRegistry(logger),
		ContextCompression: config.Default().Agent.ContextCompression,
		Logger:             logger,
	})
	if agent != nil {
		t.Fatal("compaction agent should be nil when no LLM client or resolver is available")
	}
}
