package qualityworkbench

import (
	"strings"
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
	records[0].SourceEvent.DomainID = "sales"
	records[0].SourceEvent.SourceKind = "master"
	records[0].SourceEvent.SourceName = "react_loop"
	records[1].SourceEvent.DomainID = "support"
	records[1].SourceEvent.SourceKind = "subagent"
	records[1].SourceEvent.SourceName = "tool_runner"
	keyBeforeAttribution := ComputeClusterKey(DefaultGroupingRule(), records[0])

	clusters := AggregateClusters(DefaultGroupingRules(), records)

	require.Len(t, clusters, 1)
	assert.Equal(t, keyBeforeAttribution, clusters[0].Key)
	assert.Equal(t, 2, clusters[0].Size)
	assert.Equal(t, 2, clusters[0].OpenCount)
	assert.ElementsMatch(t, []string{"candidate-1", "candidate-2"}, clusters[0].CandidateIDs)
	assert.Equal(t, agentquality.FailureTool, clusters[0].FailureType)
	assert.Equal(t, "grep", clusters[0].Tool)
	assert.Equal(t, "system/base", clusters[0].PromptKey)
	assert.Equal(t, "sales", clusters[0].DomainID)
	assert.Equal(t, "master", clusters[0].SourceKind)
	assert.Equal(t, "react_loop", clusters[0].SourceName)
	assert.Equal(t, map[string]int{"sales": 1, "support": 1}, clusters[0].DomainCounts)
	assert.Equal(t, map[string]int{"master": 1, "subagent": 1}, clusters[0].SourceKindCounts)
	assert.Equal(t, map[string]int{"react_loop": 1, "tool_runner": 1}, clusters[0].SourceNameCounts)
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

func TestGroupingRule_SourceAttributionMatchAndExplicitKeyFields(t *testing.T) {
	now := time.Now()
	sales := clusterTestCandidate("candidate-sales", now, "permission denied /tmp/a")
	sales.SourceEvent.DomainID = "sales"
	sales.SourceEvent.SourceKind = "master"
	sales.SourceEvent.SourceName = "react_loop"
	support := clusterTestCandidate("candidate-support", now.Add(time.Minute), "permission denied /tmp/a")
	support.SourceEvent.DomainID = "support"
	support.SourceEvent.SourceKind = "master"
	support.SourceEvent.SourceName = "react_loop"
	rule := GroupingRule{
		ID:        "sales_source",
		Name:      "Sales Source",
		Priority:  1,
		Enabled:   true,
		Match:     GroupingMatch{DomainID: "sales", SourceKind: "master", SourceName: "react_loop"},
		KeyFields: []string{"failure_type", "domain_id", "source_kind", "source_name", "error_digest"},
	}

	assert.Equal(t, "sales_source", MatchGroupingRule([]GroupingRule{rule, DefaultGroupingRule()}, sales).ID)
	assert.Equal(t, "default_all", MatchGroupingRule([]GroupingRule{rule, DefaultGroupingRule()}, support).ID)
	assert.Contains(t, string(ComputeClusterKey(rule, sales)), "tool|sales|master|react_loop|")

	clusters := AggregateClusters([]GroupingRule{rule}, []agentquality.CandidateRecord{sales, support})

	require.Len(t, clusters, 2)
}

func TestClusterKeyV2IncludesVersionWithoutBreakingLegacyID(t *testing.T) {
	rec := clusterTestCandidate("candidate-v2", time.Now(), "permission denied /tmp/a")
	rec.SourceEvent.DomainID = "sales"
	rec.SourceEvent.SourceKind = "master"
	rec.SourceEvent.SourceName = "react_loop"
	legacyRule := GroupingRule{
		ID:        "source_legacy",
		Name:      "Source Legacy",
		Priority:  1,
		Enabled:   true,
		KeyFields: []string{"failure_type", "domain_id", "source_kind", "source_name", "error_digest"},
	}
	v2Rule := legacyRule
	v2Rule.ID = "source_v2"
	v2Rule.KeyVersion = "v2"

	legacyKey := ComputeClusterKey(legacyRule, rec)
	v2Key := ComputeClusterKey(v2Rule, rec)

	assert.NotContains(t, string(legacyKey), "v1|")
	assert.True(t, strings.HasPrefix(string(v2Key), "v2|"), "v2 key = %q", v2Key)
	assert.NotEqual(t, clusterIDFromKey(legacyKey), clusterIDFromKey(v2Key))
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
