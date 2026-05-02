package agentquality

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryOptimizationSuggestionStore_ListFiltersAndPages(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryOptimizationSuggestionStore()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	rows := []OptimizationReviewSuggestion{
		testReviewSuggestion("sug-1", SuggestionPending, TargetPrompt, "candidate-1", "web", now.Add(time.Minute)),
		testReviewSuggestion("sug-2", SuggestionApproved, TargetPrompt, "candidate-1", "web", now.Add(2*time.Minute)),
		testReviewSuggestion("sug-3", SuggestionPending, TargetSkillContent, "candidate-2", "im", now.Add(3*time.Minute)),
		testReviewSuggestion("sug-4", SuggestionPending, TargetPrompt, "candidate-1", "web", now.Add(4*time.Minute)),
	}
	for _, row := range rows {
		_, err := store.UpsertSuggestion(ctx, row)
		require.NoError(t, err)
	}

	got, total, err := store.ListSuggestions(ctx, SuggestionFilter{
		Status:            SuggestionPending,
		Target:            TargetPrompt,
		SourceCandidateID: "candidate-1",
		Limit:             1,
		Offset:            1,
	})

	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, got, 1)
	assert.Equal(t, "sug-1", got[0].ID)
}

func TestInMemoryOptimizationSuggestionStore_ApproveAndRejectPersistState(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryOptimizationSuggestionStore()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	approveMe := testReviewSuggestion("sug-approve", SuggestionPending, TargetPrompt, "candidate-1", "web", now)
	rejectMe := testReviewSuggestion("sug-reject", SuggestionPending, TargetPrompt, "candidate-1", "web", now)
	_, err := store.UpsertSuggestion(ctx, approveMe)
	require.NoError(t, err)
	_, err = store.UpsertSuggestion(ctx, rejectMe)
	require.NoError(t, err)

	approved, err := store.ApproveSuggestion(ctx, approveMe.ID, "reviewer-1", "采用", now.Add(time.Minute))
	require.NoError(t, err)
	rejected, err := store.RejectSuggestion(ctx, rejectMe.ID, "reviewer-2", "误报", now.Add(2*time.Minute))
	require.NoError(t, err)

	assert.Equal(t, SuggestionApproved, approved.Status)
	assert.Equal(t, "reviewer-1", approved.ApprovedBy)
	assert.Equal(t, "采用", approved.ApprovalNote)
	assert.Equal(t, SuggestionRejected, rejected.Status)
	assert.Equal(t, "reviewer-2", rejected.ApprovedBy)
	assert.Equal(t, "误报", rejected.ApprovalNote)

	got, ok, err := store.GetSuggestion(ctx, approveMe.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, SuggestionApproved, got.Status)
	require.NotNil(t, got.ApprovedAt)
}

func TestInMemoryOptimizationSuggestionStore_UpsertIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryOptimizationSuggestionStore()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	rec := testReviewSuggestion("sug-1", SuggestionPending, TargetPrompt, "candidate-1", "web", now)

	first, err := store.UpsertSuggestion(ctx, rec)
	require.NoError(t, err)
	second, err := store.UpsertSuggestion(ctx, rec)
	require.NoError(t, err)

	assert.Equal(t, first, second)
	got, total, err := store.ListSuggestions(ctx, SuggestionFilter{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, got, 1)
	assert.Equal(t, rec.ID, got[0].ID)
}

func TestInMemoryOptimizationSuggestionStore_ExpiredSuggestionCannotBeApproved(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryOptimizationSuggestionStore()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	rec := testReviewSuggestion("sug-expired", SuggestionPending, TargetPrompt, "candidate-1", "web", now)
	rec.ExpiresAt = now.Add(-time.Minute)
	_, err := store.UpsertSuggestion(ctx, rec)
	require.NoError(t, err)

	_, err = store.ApproveSuggestion(ctx, rec.ID, "reviewer-1", "too late", now)

	require.Error(t, err)
	got, ok, err := store.GetSuggestion(ctx, rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, SuggestionPending, got.Status)
}

func TestInMemoryOptimizationSuggestionStore_RecordApplyAudit(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryOptimizationSuggestionStore()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	rec := testReviewSuggestion("sug-apply", SuggestionApproved, TargetPrompt, "candidate-1", "web", now)
	_, err := store.UpsertSuggestion(ctx, rec)
	require.NoError(t, err)

	appliedAt := now.Add(time.Minute)
	applied, err := store.MarkSuggestionApplied(ctx, rec.ID, "reviewer-1", appliedAt)

	require.NoError(t, err)
	assert.Equal(t, SuggestionApplyApplied, applied.ApplyStatus)
	assert.Equal(t, "reviewer-1", applied.AppliedBy)
	require.NotNil(t, applied.AppliedAt)
	assert.Equal(t, appliedAt, *applied.AppliedAt)
	assert.Empty(t, applied.ApplyError)

	failedAt := now.Add(2 * time.Minute)
	failed, err := store.MarkSuggestionApplyError(ctx, rec.ID, "reviewer-2", "store unavailable", failedAt)
	require.NoError(t, err)
	assert.Equal(t, SuggestionApplyError, failed.ApplyStatus)
	assert.Equal(t, "reviewer-2", failed.AppliedBy)
	assert.Equal(t, "store unavailable", failed.ApplyError)
	require.NotNil(t, failed.AppliedAt)
	assert.Equal(t, failedAt, *failed.AppliedAt)
}

func testReviewSuggestion(id string, status SuggestionStatus, target SuggestionTarget, candidateID, route string, createdAt time.Time) OptimizationReviewSuggestion {
	return OptimizationReviewSuggestion{
		ID:                id,
		Status:            status,
		Target:            target,
		Kind:              SuggestionPromptDiff,
		Title:             "优化建议",
		Rationale:         "失败归因",
		CurrentValue:      "old",
		ProposedValue:     "new",
		DiffFormat:        "text",
		SourceCandidateID: candidateID,
		SourceEvent: Event{
			Route:       route,
			FailureType: FailurePrompt,
			FinalStatus: StatusFail,
		},
		ReviewRequired: true,
		CreatedBy:      "worker",
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
		ExpiresAt:      createdAt.Add(time.Hour),
	}
}
