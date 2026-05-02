package agentquality

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPGRollbackStore_RecordAlertAndRollback(t *testing.T) {
	ctx := context.Background()
	stores, cleanup := setupPGOptimizationLifecycleStores(t)
	defer cleanup()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)

	alert, err := stores.rollbacks.RecordAlert(ctx, RollbackAlert{
		ID:                    "alert-pg-1",
		Status:                RollbackAlertOpen,
		EvalDiffID:            "evaldiff-pg-1",
		TreatmentRunID:        "treat-pg",
		Reasons:               []string{"success_rate_regression"},
		SuccessRateDelta:      -0.25,
		AverageLatencyDeltaMS: 120,
		CreatedAt:             now,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"success_rate_regression"}, alert.Reasons)

	alerts, err := stores.rollbacks.ListAlerts(ctx)
	require.NoError(t, err)
	require.Len(t, alerts, 1)

	_, err = stores.rollouts.RecordApplied(ctx, OptimizationRollout{
		SuggestionID:   "sug-pg-1",
		Target:         TargetPrompt,
		TargetKey:      "system/base|zh-CN",
		PreviousValue:  "old prompt",
		PreviousExists: true,
		AppliedValue:   "new prompt",
		AppliedBy:      "lead-1",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	require.NoError(t, err)

	record, err := ExecuteRollback(ctx, stores.rollouts, stores.rollbacks, RollbackRequest{
		SuggestionID: "sug-pg-1",
		AlertID:      "alert-pg-1",
		Trigger:      RollbackTriggerAlertAck,
		TriggeredBy:  "operator-1",
		CreatedAt:    now.Add(time.Minute),
	})
	require.NoError(t, err)
	require.Equal(t, RolloutRolledBack, record.Rollout.Status)
	require.Equal(t, "operator-1", record.TriggeredBy)

	rollbacks, err := stores.rollbacks.ListRollbacks(ctx)
	require.NoError(t, err)
	require.Len(t, rollbacks, 1)
	require.Equal(t, "sug-pg-1", rollbacks[0].SuggestionID)
}
