package runtimepolicy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefault_ReturnsValidPolicy(t *testing.T) {
	policy := Default()

	require.NoError(t, policy.Validate())
	assert.Equal(t, 5*time.Minute, policy.LLMCallTimeout)
	assert.Equal(t, 2*time.Minute, policy.ToolTimeout)
	assert.Equal(t, 30*time.Minute, policy.TaskTimeout)
	assert.Equal(t, 30*time.Minute, policy.SpawnAgentTimeout)
	assert.Equal(t, 30*time.Minute, policy.ACPPromptTimeout)
	assert.Equal(t, 10*time.Second, policy.ACPReconnectTimeout)
	assert.Equal(t, 30, policy.SubagentMaxTurns)
	assert.Equal(t, 1, policy.SubagentMaxDepth)
	assert.Equal(t, 1, policy.PerSessionParallel)
	assert.Equal(t, 50, policy.GlobalWorkers)
	assert.Equal(t, 0.0, policy.MaxSessionCostUSD)
}

func TestWithDefaults_FillsZeroValues(t *testing.T) {
	policy := Policy{
		ToolTimeout:        45 * time.Second,
		SubagentMaxTurns:   8,
		MaxSessionCostUSD:  1.25,
		PerSessionParallel: 2,
	}

	got := policy.WithDefaults()

	assert.Equal(t, 5*time.Minute, got.LLMCallTimeout)
	assert.Equal(t, 45*time.Second, got.ToolTimeout)
	assert.Equal(t, 8, got.SubagentMaxTurns)
	assert.Equal(t, 2, got.PerSessionParallel)
	assert.Equal(t, 1.25, got.MaxSessionCostUSD)
	require.NoError(t, got.Validate())
}

func TestValidate_RejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Policy)
	}{
		{name: "llm timeout", mutate: func(p *Policy) { p.LLMCallTimeout = -time.Second }},
		{name: "tool timeout", mutate: func(p *Policy) { p.ToolTimeout = -time.Second }},
		{name: "task timeout", mutate: func(p *Policy) { p.TaskTimeout = -time.Second }},
		{name: "spawn timeout", mutate: func(p *Policy) { p.SpawnAgentTimeout = -time.Second }},
		{name: "acp prompt timeout", mutate: func(p *Policy) { p.ACPPromptTimeout = -time.Second }},
		{name: "acp reconnect timeout", mutate: func(p *Policy) { p.ACPReconnectTimeout = -time.Second }},
		{name: "subagent turns", mutate: func(p *Policy) { p.SubagentMaxTurns = -1 }},
		{name: "subagent depth", mutate: func(p *Policy) { p.SubagentMaxDepth = -1 }},
		{name: "per session parallel", mutate: func(p *Policy) { p.PerSessionParallel = -1 }},
		{name: "global workers", mutate: func(p *Policy) { p.GlobalWorkers = -1 }},
		{name: "session cost", mutate: func(p *Policy) { p.MaxSessionCostUSD = -0.01 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := Default()
			tt.mutate(&policy)

			assert.Error(t, policy.Validate())
		})
	}
}

func TestRuntimePolicyDoesNotAuthorizeBusinessDomainTools(t *testing.T) {
	policy := Default()

	if policy.AuthorizesBusinessDomainTools() {
		t.Fatal("runtime policy must not authorize business-domain tools")
	}
}
