package agentquality

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateRollbackAlertOnlyCreatesAlert(t *testing.T) {
	diff := EvalDiff{
		ID:                    "diff-1",
		TreatmentRunID:        "treatment-run",
		SuccessRateDelta:      -0.20,
		AverageLatencyDeltaMS: 750,
	}

	alert, ok := EvaluateRollbackAlert(diff, RollbackAlertThresholds{
		MinSuccessRateDelta: -0.05,
		MaxLatencyDeltaMS:   500,
	})

	require.True(t, ok)
	assert.Equal(t, "diff-1", alert.EvalDiffID)
	assert.Equal(t, RollbackAlertOpen, alert.Status)
	assert.Contains(t, alert.Reasons, "success_rate_regression")
	assert.Contains(t, alert.Reasons, "latency_regression")
}

func TestExecuteRollbackRequiresTriggerAndSupportsManualAndAlertAck(t *testing.T) {
	ctx := context.Background()
	rollouts := NewInMemoryOptimizationRolloutStore()
	rollbackStore := NewInMemoryRollbackStore()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	_, err := rollouts.RecordApplied(ctx, OptimizationRollout{
		SuggestionID:   "sug-1",
		Target:         TargetPrompt,
		TargetKey:      "system/base",
		PreviousValue:  "old",
		PreviousExists: true,
		AppliedValue:   "new",
		AppliedBy:      "lead-1",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	require.NoError(t, err)

	_, err = ExecuteRollback(ctx, rollouts, rollbackStore, RollbackRequest{
		SuggestionID: "sug-1",
		Trigger:      RollbackTriggerManual,
		TriggeredBy:  "",
		CreatedAt:    now,
	})
	assert.Error(t, err)

	manual, err := ExecuteRollback(ctx, rollouts, rollbackStore, RollbackRequest{
		SuggestionID: "sug-1",
		Trigger:      RollbackTriggerManual,
		TriggeredBy:  "operator-1",
		CreatedAt:    now.Add(time.Minute),
	})
	require.NoError(t, err)
	assert.Equal(t, RollbackTriggerManual, manual.Trigger)
	assert.Equal(t, "operator-1", manual.TriggeredBy)

	_, err = rollouts.RecordApplied(ctx, OptimizationRollout{
		SuggestionID:   "sug-2",
		Target:         TargetToolDescription,
		TargetKey:      "grep",
		PreviousValue:  "old grep",
		PreviousExists: true,
		AppliedValue:   "new grep",
		AppliedBy:      "lead-1",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	require.NoError(t, err)
	alertAck, err := ExecuteRollback(ctx, rollouts, rollbackStore, RollbackRequest{
		SuggestionID: "sug-2",
		AlertID:      "alert-1",
		Trigger:      RollbackTriggerAlertAck,
		TriggeredBy:  "lead-2",
		CreatedAt:    now.Add(2 * time.Minute),
	})
	require.NoError(t, err)
	assert.Equal(t, RollbackTriggerAlertAck, alertAck.Trigger)
	assert.Equal(t, "alert-1", alertAck.AlertID)

	records, err := rollbackStore.ListRollbacks(ctx)
	require.NoError(t, err)
	require.Len(t, records, 2)
}
