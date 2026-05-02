package agentquality

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPGOptimizationSuggestionStore_UpsertListAndReview(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupPGOptimizationSuggestionStore(t)
	defer cleanup()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	first := testReviewSuggestion("sug-pg-1", SuggestionPending, TargetPrompt, "candidate-1", "web", now)
	first.SourceEvalDiffID = "evaldiff-1"
	second := testReviewSuggestion("sug-pg-2", SuggestionPending, TargetSkillContent, "candidate-2", "im", now.Add(time.Minute))

	created, err := store.UpsertSuggestion(ctx, first)
	require.NoError(t, err)
	duplicate, err := store.UpsertSuggestion(ctx, first)
	require.NoError(t, err)
	require.Equal(t, created.ID, duplicate.ID)

	_, err = store.UpsertSuggestion(ctx, second)
	require.NoError(t, err)
	listed, total, err := store.ListSuggestions(ctx, SuggestionFilter{
		Status:            SuggestionPending,
		Target:            TargetPrompt,
		SourceCandidateID: "candidate-1",
		Limit:             10,
	})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, listed, 1)
	assert.Equal(t, first.ID, listed[0].ID)
	assert.Equal(t, "evaldiff-1", listed[0].SourceEvalDiffID)

	byEvalDiff, total, err := store.ListSuggestions(ctx, SuggestionFilter{SourceEvalDiffID: "evaldiff-1", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, byEvalDiff, 1)
	assert.Equal(t, first.ID, byEvalDiff[0].ID)

	approved, err := store.ApproveSuggestion(ctx, first.ID, "reviewer-1", "采用", now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, SuggestionApproved, approved.Status)
	assert.Equal(t, "reviewer-1", approved.ApprovedBy)
	assert.Equal(t, "采用", approved.ApprovalNote)

	_, err = store.RejectSuggestion(ctx, second.ID, "reviewer-2", "误报", now.Add(3*time.Minute))
	require.NoError(t, err)
}

func TestPGOptimizationSuggestionStore_ExpiredSuggestionCannotBeApproved(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupPGOptimizationSuggestionStore(t)
	defer cleanup()
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	rec := testReviewSuggestion("sug-pg-expired", SuggestionPending, TargetPrompt, "candidate-1", "web", now)
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

func setupPGOptimizationSuggestionStore(t *testing.T) (*PGOptimizationSuggestionStore, func()) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL 未设置，跳过 agentquality suggestion PG 集成测试")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
DROP TABLE IF EXISTS agentquality_optimization_suggestions;
CREATE TABLE IF NOT EXISTS agentquality_optimization_suggestions (
	id TEXT PRIMARY KEY,
	status TEXT NOT NULL DEFAULT 'pending',
	target TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	rationale TEXT NOT NULL DEFAULT '',
	current_value TEXT NOT NULL DEFAULT '',
	proposed_value TEXT NOT NULL DEFAULT '',
	diff_format TEXT NOT NULL DEFAULT 'text',
	source_candidate_id TEXT NOT NULL DEFAULT '',
	source_eval_diff_id TEXT NOT NULL DEFAULT '',
	source_event JSONB NOT NULL DEFAULT '{}',
	review_required BOOLEAN NOT NULL DEFAULT TRUE,
	created_by TEXT NOT NULL DEFAULT '',
	approved_by TEXT NOT NULL DEFAULT '',
	approval_note TEXT NOT NULL DEFAULT '',
	apply_status TEXT NOT NULL DEFAULT 'unapplied',
	applied_by TEXT NOT NULL DEFAULT '',
	apply_error TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	approved_at TIMESTAMPTZ,
	applied_at TIMESTAMPTZ,
	expires_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_agentquality_suggestions_status_created
	ON agentquality_optimization_suggestions(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agentquality_suggestions_target_created
	ON agentquality_optimization_suggestions(target, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agentquality_suggestions_candidate_created
	ON agentquality_optimization_suggestions(source_candidate_id, created_at DESC);
`)
	require.NoError(t, err)

	return NewPGOptimizationSuggestionStore(pool, zap.NewNop()), func() {
		_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS agentquality_optimization_suggestions`)
		pool.Close()
	}
}
