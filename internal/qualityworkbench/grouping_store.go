package qualityworkbench

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type GroupingRuleStore interface {
	List() []GroupingRule
	Get(id string) (GroupingRule, bool)
	Upsert(rule GroupingRule) (GroupingRule, error)
	Delete(id string) error
}

type MemoryGroupingRuleStore struct {
	mu    sync.RWMutex
	now   func() time.Time
	rules map[string]GroupingRule
}

func NewMemoryGroupingRuleStore(now func() time.Time) *MemoryGroupingRuleStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryGroupingRuleStore{now: now, rules: map[string]GroupingRule{}}
}

func (s *MemoryGroupingRuleStore) List() []GroupingRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]GroupingRule, 0, len(s.rules))
	for _, rule := range s.rules {
		out = append(out, cloneGroupingRule(rule))
	}
	sortGroupingRules(out)
	return out
}

func (s *MemoryGroupingRuleStore) Get(id string) (GroupingRule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rule, ok := s.rules[strings.TrimSpace(id)]
	if !ok {
		return GroupingRule{}, false
	}
	return cloneGroupingRule(rule), true
}

func (s *MemoryGroupingRuleStore) Upsert(rule GroupingRule) (GroupingRule, error) {
	if err := ValidateGroupingRule(rule); err != nil {
		return GroupingRule{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if existing, ok := s.rules[rule.ID]; ok && !existing.CreatedAt.IsZero() {
		rule.CreatedAt = existing.CreatedAt
	} else if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now
	}
	rule.UpdatedAt = now
	s.rules[rule.ID] = cloneGroupingRule(rule)
	return cloneGroupingRule(rule), nil
}

func (s *MemoryGroupingRuleStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	if _, ok := s.rules[id]; !ok {
		return fmt.Errorf("grouping rule %s not found", id)
	}
	delete(s.rules, id)
	return nil
}

func ValidateGroupingRule(rule GroupingRule) error {
	if strings.TrimSpace(rule.ID) == "" {
		return errors.New("grouping rule id is required")
	}
	if strings.TrimSpace(rule.Name) == "" {
		return errors.New("grouping rule name is required")
	}
	if len(rule.KeyFields) == 0 {
		return errors.New("grouping rule key_fields is required")
	}
	for _, field := range rule.KeyFields {
		switch field {
		case "failure_type", "tool", "skill", "tool_or_skill", "prompt_key", "case_id", "error_digest", "delegation_depth":
		default:
			return fmt.Errorf("unsupported grouping key field %s", field)
		}
	}
	return nil
}

func EffectiveGroupingRules(store GroupingRuleStore) []GroupingRule {
	if store == nil {
		return DefaultGroupingRules()
	}
	rules := store.List()
	if len(rules) == 0 {
		return DefaultGroupingRules()
	}
	return rules
}

func sortGroupingRules(rules []GroupingRule) {
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority == rules[j].Priority {
			return rules[i].ID < rules[j].ID
		}
		return rules[i].Priority < rules[j].Priority
	})
}

func cloneGroupingRule(rule GroupingRule) GroupingRule {
	rule.KeyFields = append([]string(nil), rule.KeyFields...)
	rule.DigestNormalize = append([]string(nil), rule.DigestNormalize...)
	return rule
}
