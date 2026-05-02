package qualityworkbench

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

func TestAggregateClusters_DefaultRuleGroupsSimilarFailures(t *testing.T) {
	now := time.Now()
	records := []agentquality.CandidateRecord{
		clusterTestCandidate("candidate-1", now.Add(-time.Hour), "failed on /tmp/a/session-123"),
		clusterTestCandidate("candidate-2", now, "failed on /tmp/b/session-456"),
	}

	clusters := AggregateClusters(DefaultGroupingRules(), records)

	require.Len(t, clusters, 1)
	assert.Equal(t, 2, clusters[0].Size)
	assert.Equal(t, 2, clusters[0].OpenCount)
	assert.ElementsMatch(t, []string{"candidate-1", "candidate-2"}, clusters[0].CandidateIDs)
	assert.Equal(t, agentquality.FailureTool, clusters[0].FailureType)
	assert.Equal(t, "grep", clusters[0].Tool)
	assert.Equal(t, "system/base", clusters[0].PromptKey)
	assert.NotEmpty(t, clusters[0].ErrorDigest)
}

func TestMatchGroupingRule_PriorityOrder(t *testing.T) {
	rec := clusterTestCandidate("candidate-1", time.Now(), "permission denied /tmp/a")
	rec.FailureType = agentquality.FailurePermission
	rec.SourceEvent.FailureType = agentquality.FailurePermission
	rules := []GroupingRule{
		{
			ID:        "permission_coarse",
			Name:      "permission_coarse",
			Priority:  10,
			Enabled:   true,
			Match:     GroupingMatch{FailureType: string(agentquality.FailurePermission)},
			KeyFields: []string{"failure_type", "tool_or_skill"},
		},
		DefaultGroupingRule(),
	}

	rule := MatchGroupingRule(rules, rec)

	assert.Equal(t, "permission_coarse", rule.ID)
	assert.Equal(t, ClusterKey("permission|grep"), ComputeClusterKey(rule, rec))
}

func clusterTestCandidate(id string, ts time.Time, errText string) agentquality.CandidateRecord {
	return agentquality.CandidateRecord{
		ID:          id,
		Status:      agentquality.CandidateNew,
		Route:       "web",
		Input:       "find symbol",
		FailureType: agentquality.FailureTool,
		CreatedAt:   ts,
		UpdatedAt:   ts,
		SourceEvent: agentquality.Event{
			FailureType: agentquality.FailureTool,
			FinalStatus: agentquality.StatusFail,
			Prompt:      agentquality.PromptRef{Key: "system/base"},
			ToolDecision: agentquality.ToolDecision{
				Actual: "grep",
			},
			Attributes: map[string]any{"error": errText},
		},
	}
}
