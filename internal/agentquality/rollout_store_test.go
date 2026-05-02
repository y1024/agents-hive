package agentquality

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInMemoryOptimizationRolloutStore_RecordAndRollback(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryOptimizationRolloutStore()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)

	applied, err := store.RecordApplied(ctx, OptimizationRollout{
		SuggestionID:   "sug-1",
		Target:         TargetPrompt,
		TargetKey:      "system/base|zh-CN",
		PreviousValue:  "old prompt",
		PreviousExists: true,
		AppliedValue:   "new prompt",
		AppliedBy:      "reviewer",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	require.NoError(t, err)
	require.Equal(t, RolloutApplied, applied.Status)
	require.True(t, applied.PreviousExists)

	got, ok, err := store.GetBySuggestion(ctx, "sug-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "old prompt", got.PreviousValue)

	rolledBackAt := now.Add(time.Minute)
	rolledBack, err := store.MarkRolledBack(ctx, "sug-1", "operator", rolledBackAt)
	require.NoError(t, err)
	require.Equal(t, RolloutRolledBack, rolledBack.Status)
	require.Equal(t, "operator", rolledBack.RolledBackBy)
	require.NotNil(t, rolledBack.RolledBackAt)
	require.Equal(t, rolledBackAt, *rolledBack.RolledBackAt)
}
