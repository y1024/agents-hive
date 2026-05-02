package qualityworkbench

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

func TestPreviewGroupingRulesHonorsPriorityAndEnabledWithoutMutatingInput(t *testing.T) {
	now := time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC)
	records := []agentquality.CandidateRecord{
		clusterTestCandidate("candidate-grep", now, "permission denied /tmp/a/1"),
		clusterTestCandidate("candidate-curl", now.Add(time.Minute), "permission denied /tmp/b/2"),
	}
	records[0].SourceEvent.ToolDecision.Actual = "grep"
	records[1].SourceEvent.ToolDecision.Actual = "curl"
	rules := []GroupingRule{
		{
			ID:        "disabled_specific",
			Name:      "disabled specific",
			Priority:  1,
			Enabled:   false,
			Match:     GroupingMatch{Tool: "grep"},
			KeyFields: []string{"tool"},
		},
		{
			ID:        "tool_rule",
			Name:      "tool rule",
			Priority:  5,
			Enabled:   true,
			Match:     GroupingMatch{ErrorSubstring: "permission denied"},
			KeyFields: []string{"failure_type", "tool"},
		},
		DefaultGroupingRule(),
	}
	originalFirstID := rules[0].ID
	originalFirstPriority := rules[0].Priority

	preview := PreviewGroupingRules(rules, records)

	require.Len(t, preview.Clusters, 2)
	assert.Equal(t, "tool_rule", preview.Clusters[0].RuleID)
	assert.Equal(t, "tool_rule", preview.Clusters[1].RuleID)
	assert.Equal(t, 2, preview.RuleHits["tool_rule"])
	assert.Zero(t, preview.RuleHits["disabled_specific"])
	assert.Equal(t, originalFirstID, rules[0].ID)
	assert.Equal(t, originalFirstPriority, rules[0].Priority)
}
