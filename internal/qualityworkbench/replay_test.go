package qualityworkbench

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplayJobStore_CreateListGetAndCancel(t *testing.T) {
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	store := NewMemoryReplayJobStore(func() time.Time { return now })

	job, err := store.Create(ReplayJobCreate{
		BatchID:    "batch-1",
		Kind:       ReplayJobKindCluster,
		TargetIDs:  []string{"cl_1", "cl_2"},
		DomainID:   "customer_service",
		SourceKind: "workflow",
		SourceName: "case_triage",
		MaxAttempt: 3,
	})
	require.NoError(t, err)
	assert.Equal(t, "batch-1", job.BatchID)
	assert.Equal(t, ReplayJobQueued, job.Status)
	assert.Equal(t, 0, job.Attempt)
	assert.Equal(t, 3, job.MaxAttempt)
	assert.Equal(t, "customer_service", job.DomainID)
	assert.Equal(t, "workflow", job.SourceKind)
	assert.Equal(t, "case_triage", job.SourceName)
	assert.Equal(t, now, job.CreatedAt)
	assert.Equal(t, now, job.UpdatedAt)

	got, ok := store.Get(job.ID)
	require.True(t, ok)
	assert.Equal(t, job.ID, got.ID)

	now = now.Add(time.Minute)
	cancelled, err := store.Cancel(job.ID)
	require.NoError(t, err)
	assert.Equal(t, ReplayJobCancelled, cancelled.Status)
	assert.Equal(t, now, cancelled.UpdatedAt)

	list := store.List(ReplayJobListFilter{
		BatchID: "batch-1",
		Status:  ReplayJobCancelled,
		Limit:   10,
	})
	require.Len(t, list, 1)
	assert.Equal(t, job.ID, list[0].ID)
}

func TestReplayJobStore_ListFiltersAndPaginatesNewestFirst(t *testing.T) {
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	store := NewMemoryReplayJobStore(func() time.Time { return now })

	_, err := store.Create(ReplayJobCreate{BatchID: "batch-1", Kind: ReplayJobKindCandidate, TargetIDs: []string{"c1"}})
	require.NoError(t, err)
	now = now.Add(time.Minute)
	second, err := store.Create(ReplayJobCreate{BatchID: "batch-2", Kind: ReplayJobKindCluster, TargetIDs: []string{"cl1"}})
	require.NoError(t, err)
	now = now.Add(time.Minute)
	third, err := store.Create(ReplayJobCreate{BatchID: "batch-2", Kind: ReplayJobKindCluster, TargetIDs: []string{"cl2"}})
	require.NoError(t, err)

	list := store.List(ReplayJobListFilter{
		BatchID: "batch-2",
		Kind:    ReplayJobKindCluster,
		Limit:   1,
		Offset:  1,
	})

	require.Len(t, list, 1)
	assert.Equal(t, second.ID, list[0].ID)
	assert.NotEqual(t, third.ID, list[0].ID)
}

func TestReplayJobStore_ListFiltersByAttribution(t *testing.T) {
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	store := NewMemoryReplayJobStore(func() time.Time { return now })

	_, err := store.Create(ReplayJobCreate{
		BatchID:    "batch-1",
		Kind:       ReplayJobKindCandidate,
		TargetIDs:  []string{"c1"},
		DomainID:   "customer_service",
		SourceKind: "workflow",
		SourceName: "case_triage",
	})
	require.NoError(t, err)
	_, err = store.Create(ReplayJobCreate{
		BatchID:    "batch-1",
		Kind:       ReplayJobKindCandidate,
		TargetIDs:  []string{"c2"},
		DomainID:   "generic",
		SourceKind: "master",
		SourceName: "react",
	})
	require.NoError(t, err)

	list := store.List(ReplayJobListFilter{
		DomainID:   "customer_service",
		SourceKind: "workflow",
		SourceName: "case_triage",
		Limit:      10,
	})

	require.Len(t, list, 1)
	assert.Equal(t, []string{"c1"}, list[0].TargetIDs)
}

func TestReplayJobTransition_RejectsInvalidTerminalTransition(t *testing.T) {
	job := ReplayJob{ID: "job-1", Status: ReplayJobCancelled}

	err := job.Transition(ReplayJobRunning, time.Now())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "terminal")
}

func TestReplayJobStore_RunLifecyclePersistsResultAndError(t *testing.T) {
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	store := NewMemoryReplayJobStore(func() time.Time { return now })
	job, err := store.Create(ReplayJobCreate{BatchID: "batch-run", Kind: ReplayJobKindCandidate, TargetIDs: []string{"candidate-1"}})
	require.NoError(t, err)

	now = now.Add(time.Minute)
	running, err := store.MarkRunning(job.ID)
	require.NoError(t, err)
	assert.Equal(t, ReplayJobRunning, running.Status)
	assert.Equal(t, 1, running.Attempt)

	now = now.Add(time.Minute)
	finished, err := store.Finish(job.ID, ReplayJobSucceeded, ReplayJobResult{
		Total:  1,
		Passed: 1,
		CaseIDs: []string{
			"candidate-1",
		},
	}, "")
	require.NoError(t, err)
	assert.Equal(t, ReplayJobSucceeded, finished.Status)
	assert.Equal(t, 1, finished.Result.Total)
	assert.Equal(t, 1, finished.Result.Passed)
	assert.Equal(t, []string{"candidate-1"}, finished.Result.CaseIDs)
	assert.Empty(t, finished.Error)

	_, err = store.Finish(job.ID, ReplayJobFailed, ReplayJobResult{}, "late failure")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "terminal")
}
