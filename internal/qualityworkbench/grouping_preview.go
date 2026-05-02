package qualityworkbench

import (
	"sort"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

type GroupingRulePreview struct {
	Clusters []Cluster      `json:"clusters"`
	RuleHits map[string]int `json:"rule_hits"`
}

func PreviewGroupingRules(rules []GroupingRule, records []agentquality.CandidateRecord) GroupingRulePreview {
	previewRules := append([]GroupingRule(nil), rules...)
	if len(previewRules) == 0 {
		previewRules = DefaultGroupingRules()
	}
	sort.SliceStable(previewRules, func(i, j int) bool {
		return previewRules[i].Priority < previewRules[j].Priority
	})

	hits := make(map[string]int, len(previewRules))
	for _, rule := range previewRules {
		hits[rule.ID] = 0
	}
	for _, rec := range records {
		rule := MatchGroupingRule(previewRules, rec)
		hits[rule.ID]++
	}
	return GroupingRulePreview{
		Clusters: AggregateClusters(previewRules, records),
		RuleHits: hits,
	}
}
