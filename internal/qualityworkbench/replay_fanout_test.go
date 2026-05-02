package qualityworkbench

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanReplayClusterFanoutTruncatesAndPlansRemainingBatches(t *testing.T) {
	targets := make([]string, 120)
	for i := range targets {
		targets[i] = fmt.Sprintf("candidate-%03d", i+1)
	}

	plan := PlanReplayClusterFanout(targets, 50)

	assert.True(t, plan.Truncated)
	assert.Equal(t, 120, plan.Total)
	assert.Equal(t, 50, plan.Limit)
	assert.Len(t, plan.SelectedIDs, 50)
	assert.Equal(t, 70, plan.Remaining)
	require.Len(t, plan.RemainingBatches, 2)
	assert.Len(t, plan.RemainingBatches[0], 50)
	assert.Len(t, plan.RemainingBatches[1], 20)
	assert.Equal(t, "candidate-051", plan.RemainingBatches[0][0])
	assert.Equal(t, "candidate-120", plan.RemainingBatches[1][19])
}

func TestPlanReplayClusterFanoutHandlesNoRemaining(t *testing.T) {
	plan := PlanReplayClusterFanout([]string{"c1", "c2"}, 50)

	assert.False(t, plan.Truncated)
	assert.Equal(t, 2, plan.Total)
	assert.Equal(t, 2, plan.Limit)
	assert.Equal(t, []string{"c1", "c2"}, plan.SelectedIDs)
	assert.Zero(t, plan.Remaining)
	assert.Empty(t, plan.RemainingBatches)
}
