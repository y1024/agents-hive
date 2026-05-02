package qualityworkbench

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGroupingRuleStoreUpsertListDelete(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := NewMemoryGroupingRuleStore(func() time.Time { return now })
	rule := GroupingRule{
		ID:        "tool_rule",
		Name:      "Tool Rule",
		Priority:  10,
		Enabled:   true,
		Match:     GroupingMatch{Tool: "grep"},
		KeyFields: []string{"failure_type", "tool"},
		CreatedBy: "admin",
	}

	saved, err := store.Upsert(rule)
	require.NoError(t, err)
	require.Equal(t, "tool_rule", saved.ID)
	require.Equal(t, now, saved.CreatedAt)
	require.Equal(t, now, saved.UpdatedAt)

	list := store.List()
	require.Len(t, list, 1)
	require.Equal(t, "tool_rule", list[0].ID)

	got, ok := store.Get("tool_rule")
	require.True(t, ok)
	require.Equal(t, []string{"failure_type", "tool"}, got.KeyFields)

	require.NoError(t, store.Delete("tool_rule"))
	_, ok = store.Get("tool_rule")
	require.False(t, ok)
}

func TestGroupingRuleStoreRejectsInvalidRule(t *testing.T) {
	store := NewMemoryGroupingRuleStore(nil)

	_, err := store.Upsert(GroupingRule{ID: "bad", Name: "bad", Enabled: true})

	require.Error(t, err)
}
