package qualityworkbench

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

type ClusterKey string

type GroupingMatch struct {
	FailureType    string `json:"failure_type,omitempty"`
	Tool           string `json:"tool,omitempty"`
	Skill          string `json:"skill,omitempty"`
	PromptKey      string `json:"prompt_key,omitempty"`
	ErrorSubstring string `json:"error_substring,omitempty"`
}

type GroupingRule struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Priority        int           `json:"priority"`
	Enabled         bool          `json:"enabled"`
	Match           GroupingMatch `json:"match"`
	KeyFields       []string      `json:"key_fields"`
	DigestNormalize []string      `json:"digest_normalize"`
	Notes           string        `json:"notes,omitempty"`
	CreatedBy       string        `json:"created_by,omitempty"`
	CreatedAt       time.Time     `json:"created_at,omitempty"`
	UpdatedAt       time.Time     `json:"updated_at,omitempty"`
}

type Cluster struct {
	ID            string                   `json:"id"`
	Key           ClusterKey               `json:"key"`
	RuleID        string                   `json:"rule_id"`
	FailureType   agentquality.FailureType `json:"failure_type"`
	Tool          string                   `json:"tool,omitempty"`
	Skill         string                   `json:"skill,omitempty"`
	PromptKey     string                   `json:"prompt_key,omitempty"`
	ErrorDigest   string                   `json:"error_digest"`
	SampleMessage string                   `json:"sample_message"`
	FirstSeen     time.Time                `json:"first_seen"`
	LastSeen      time.Time                `json:"last_seen"`
	Size          int                      `json:"size"`
	OpenCount     int                      `json:"open_count"`
	CandidateIDs  []string                 `json:"candidate_ids"`
}

var (
	reNum    = regexp.MustCompile(`\d+`)
	reUUID   = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	rePath   = regexp.MustCompile(`(/[\w.\-]+)+`)
	reQuoted = regexp.MustCompile(`"[^"]*"|'[^']*'`)
)

func DefaultGroupingRule() GroupingRule {
	return GroupingRule{
		ID:              "default_all",
		Name:            "default_all",
		Priority:        9999,
		Enabled:         true,
		KeyFields:       []string{"failure_type", "tool_or_skill", "prompt_key", "error_digest"},
		DigestNormalize: []string{"uuid", "num", "path"},
		Notes:           "默认失败聚类规则",
	}
}

func DefaultGroupingRules() []GroupingRule {
	return []GroupingRule{DefaultGroupingRule()}
}

func MatchGroupingRule(rules []GroupingRule, rec agentquality.CandidateRecord) GroupingRule {
	if len(rules) == 0 {
		return DefaultGroupingRule()
	}
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if !matchesRule(r.Match, rec) {
			continue
		}
		return r
	}
	return rules[len(rules)-1]
}

func ComputeClusterKey(rule GroupingRule, rec agentquality.CandidateRecord) ClusterKey {
	ev := rec.SourceEvent
	parts := make([]string, 0, len(rule.KeyFields))
	for _, f := range rule.KeyFields {
		switch f {
		case "failure_type":
			parts = append(parts, string(firstFailureType(ev.FailureType, rec.FailureType)))
		case "tool":
			parts = append(parts, ev.ToolDecision.Actual)
		case "skill":
			parts = append(parts, stringAttr(ev, "skill"))
		case "tool_or_skill":
			parts = append(parts, firstNonEmpty(ev.ToolDecision.Actual, stringAttr(ev, "skill")))
		case "prompt_key":
			parts = append(parts, ev.Prompt.Key)
		case "case_id":
			parts = append(parts, ev.CaseID)
		case "error_digest":
			parts = append(parts, errorDigest(errorMessage(ev), rule.DigestNormalize))
		case "delegation_depth":
			parts = append(parts, fmt.Sprintf("%d", ev.Delegation.SpawnDepth))
		}
	}
	return ClusterKey(strings.Join(parts, "|"))
}

func AggregateClusters(rules []GroupingRule, records []agentquality.CandidateRecord) []Cluster {
	if len(rules) == 0 {
		rules = DefaultGroupingRules()
	}
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
	bucket := map[ClusterKey]*Cluster{}
	for _, rec := range records {
		rule := MatchGroupingRule(rules, rec)
		key := ComputeClusterKey(rule, rec)
		c := bucket[key]
		if c == nil {
			ev := rec.SourceEvent
			errText := errorMessage(ev)
			c = &Cluster{
				ID:            clusterIDFromKey(key),
				Key:           key,
				RuleID:        rule.ID,
				FailureType:   firstFailureType(ev.FailureType, rec.FailureType),
				Tool:          ev.ToolDecision.Actual,
				Skill:         stringAttr(ev, "skill"),
				PromptKey:     ev.Prompt.Key,
				ErrorDigest:   errorDigest(errText, rule.DigestNormalize),
				SampleMessage: truncate(errText, 200),
				FirstSeen:     rec.CreatedAt,
				LastSeen:      rec.CreatedAt,
			}
			bucket[key] = c
		}
		c.Size++
		c.CandidateIDs = append(c.CandidateIDs, rec.ID)
		if !rec.CreatedAt.IsZero() && (c.FirstSeen.IsZero() || rec.CreatedAt.Before(c.FirstSeen)) {
			c.FirstSeen = rec.CreatedAt
		}
		if !rec.CreatedAt.IsZero() && (c.LastSeen.IsZero() || rec.CreatedAt.After(c.LastSeen)) {
			c.LastSeen = rec.CreatedAt
		}
		if rec.Status == agentquality.CandidateNew || rec.Status == agentquality.CandidateReviewing {
			c.OpenCount++
		}
	}
	out := make([]Cluster, 0, len(bucket))
	for _, c := range bucket {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	return out
}

func matchesRule(match GroupingMatch, rec agentquality.CandidateRecord) bool {
	ev := rec.SourceEvent
	if match.FailureType != "" && string(firstFailureType(ev.FailureType, rec.FailureType)) != match.FailureType {
		return false
	}
	if match.Tool != "" && ev.ToolDecision.Actual != match.Tool {
		return false
	}
	if match.Skill != "" && stringAttr(ev, "skill") != match.Skill {
		return false
	}
	if match.PromptKey != "" && ev.Prompt.Key != match.PromptKey {
		return false
	}
	if match.ErrorSubstring != "" && !strings.Contains(strings.ToLower(errorMessage(ev)), strings.ToLower(match.ErrorSubstring)) {
		return false
	}
	return true
}

func errorDigest(msg string, normalizers []string) string {
	msg = strings.TrimSpace(strings.ToLower(msg))
	if msg == "" {
		return "empty"
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	for _, n := range normalizers {
		switch n {
		case "uuid":
			msg = reUUID.ReplaceAllString(msg, "<uuid>")
		case "num":
			msg = reNum.ReplaceAllString(msg, "<n>")
		case "path":
			msg = rePath.ReplaceAllString(msg, "<path>")
		case "quoted_string":
			msg = reQuoted.ReplaceAllString(msg, "<q>")
		}
	}
	sum := sha1.Sum([]byte(msg))
	return hex.EncodeToString(sum[:6])
}

func clusterIDFromKey(key ClusterKey) string {
	sum := sha1.Sum([]byte(key))
	return "cl_" + hex.EncodeToString(sum[:8])
}

func errorMessage(ev agentquality.Event) string {
	if ev.Attributes != nil {
		if v, ok := ev.Attributes["error"].(string); ok {
			return v
		}
		if v, ok := ev.Attributes["error_message"].(string); ok {
			return v
		}
	}
	return ev.RetryReason
}

func stringAttr(ev agentquality.Event, key string) string {
	if ev.Attributes == nil {
		return ""
	}
	v, _ := ev.Attributes[key].(string)
	return v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstFailureType(vals ...agentquality.FailureType) agentquality.FailureType {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return agentquality.FailureNone
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
