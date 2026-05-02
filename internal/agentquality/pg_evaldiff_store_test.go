package agentquality

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPGEvalDiffStore_UpsertGetAndList(t *testing.T) {
	ctx := context.Background()
	stores, cleanup := setupPGOptimizationLifecycleStores(t)
	defer cleanup()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)

	diff, err := ComputeEvalDiff(
		EvalRun{ID: "base-pg", Results: []EvalCaseResult{{CaseID: "case-1", Passed: true, CostUSD: 0.01, LatencyMS: 100}}},
		EvalRun{ID: "treat-pg", Results: []EvalCaseResult{{CaseID: "case-1", Passed: false, CostUSD: 0.02, LatencyMS: 180, FailureType: FailureTool}}},
	)
	require.NoError(t, err)
	diff.CreatedAt = now
	diff.UpdatedAt = now

	saved, err := stores.evalDiffs.UpsertEvalDiff(ctx, diff)
	require.NoError(t, err)
	require.Equal(t, diff.ID, saved.ID)
	require.Equal(t, -1.0, saved.SuccessRateDelta)
	require.Len(t, saved.CaseDiffs, 1)

	got, ok, err := stores.evalDiffs.GetEvalDiff(ctx, diff.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, FailureTool, got.CaseDiffs[0].FailureType)

	listed, total, err := stores.evalDiffs.ListEvalDiffs(ctx, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, listed, 1)
	require.Equal(t, diff.ID, listed[0].ID)
}
