package agentquality

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPGCandidateStore_UpsertDedupAndReview(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupPGCandidateStore(t)
	defer cleanup()

	rec := CandidateFromFailure("session-1", "执行 rm -rf ./tmp-cache", "session-1:step-3", Event{
		Name:         EventPermissionDecision,
		Route:        "im",
		FailureType:  FailurePermission,
		FinalStatus:  StatusNeedsUser,
		ToolDecision: ToolDecision{Actual: "bash"},
	})
	rec.CreatedBy = "quality-worker"

	first, err := store.UpsertCandidate(ctx, rec)
	require.NoError(t, err)
	require.Equal(t, CandidateNew, first.Status)
	require.False(t, first.Case.Required)

	second, err := store.UpsertCandidate(ctx, rec)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, first.Fingerprint, second.Fingerprint)

	require.NoError(t, store.UpdateCandidateStatus(ctx, first.ID, CandidateApproved, "reviewer-1", "可复现", ""))
	got, ok, err := store.GetCandidate(ctx, first.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, CandidateApproved, got.Status)
	require.Equal(t, "reviewer-1", got.ReviewedBy)
	require.Equal(t, "可复现", got.ReviewNote)
	require.NotNil(t, got.ReviewedAt)
}

func TestPGCandidateStore_PromotedRequiresCaseID(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupPGCandidateStore(t)
	defer cleanup()

	rec := CandidateFromFailure("session-1", "需要工具但失败", "session-1:step-4", Event{
		Name:        EventToolDecision,
		Route:       "web",
		FailureType: FailureTool,
		FinalStatus: StatusFail,
	})
	created, err := store.UpsertCandidate(ctx, rec)
	require.NoError(t, err)

	err = store.UpdateCandidateStatus(ctx, created.ID, CandidatePromoted, "reviewer-1", "晋升", "")
	require.Error(t, err)
}

func TestPGCandidateStore_BlocksDirectPromotionBeforeApproval(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupPGCandidateStore(t)
	defer cleanup()

	rec := CandidateFromFailure("session-1", "需要工具但失败", "session-1:step-4", Event{
		Name:        EventToolDecision,
		Route:       "web",
		FailureType: FailureTool,
		FinalStatus: StatusFail,
	})
	created, err := store.UpsertCandidate(ctx, rec)
	require.NoError(t, err)

	err = store.UpdateCandidateStatus(ctx, created.ID, CandidatePromoted, "reviewer-1", "晋升", "aq08_tool_failure")
	require.Error(t, err)

	got, ok, err := store.GetCandidate(ctx, created.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, CandidateNew, got.Status)
	require.Empty(t, got.PromotedCaseID)
}

func TestPGCandidateStore_RejectedFingerprintCanReopen(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupPGCandidateStore(t)
	defer cleanup()

	rec := CandidateFromFailure("session-1", "执行 rm -rf ./tmp-cache", "session-1:step-3", Event{
		Name:         EventPermissionDecision,
		Route:        "im",
		FailureType:  FailurePermission,
		FinalStatus:  StatusNeedsUser,
		ToolDecision: ToolDecision{Actual: "bash"},
	})
	first, err := store.UpsertCandidate(ctx, rec)
	require.NoError(t, err)
	require.NoError(t, store.UpdateCandidateStatus(ctx, first.ID, CandidateRejected, "reviewer-1", "误报", ""))

	second, err := store.UpsertCandidate(ctx, rec)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, first.Fingerprint, second.Fingerprint)
	require.Equal(t, CandidateNew, second.Status)
}

func setupPGCandidateStore(t *testing.T) (*PGCandidateStore, func()) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL 未设置，跳过 agentquality candidate PG 集成测试")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
DROP TABLE IF EXISTS agentquality_candidates;
CREATE TABLE IF NOT EXISTS agentquality_candidates (
	id TEXT PRIMARY KEY,
	status TEXT NOT NULL DEFAULT 'new',
	route TEXT NOT NULL DEFAULT '',
	session_id TEXT NOT NULL DEFAULT '',
	replay_ref TEXT NOT NULL DEFAULT '',
	input TEXT NOT NULL DEFAULT '',
	case_json JSONB NOT NULL DEFAULT '{}',
	failure_type TEXT NOT NULL DEFAULT '',
	risk TEXT NOT NULL DEFAULT 'safe',
	fingerprint TEXT NOT NULL DEFAULT '',
	source_event JSONB NOT NULL DEFAULT '{}',
	suggestions_json JSONB NOT NULL DEFAULT '[]',
	review_note TEXT NOT NULL DEFAULT '',
	created_by TEXT NOT NULL DEFAULT '',
	reviewed_by TEXT NOT NULL DEFAULT '',
	promoted_case_id TEXT NOT NULL DEFAULT '',
	cluster_id TEXT NOT NULL DEFAULT '',
	verify_result JSONB NOT NULL DEFAULT '{}',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	reviewed_at TIMESTAMPTZ,
	last_verified_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agentquality_candidates_fingerprint
	ON agentquality_candidates(fingerprint)
	WHERE status IN ('new', 'reviewing', 'approved');
CREATE INDEX IF NOT EXISTS idx_agentquality_candidates_status_created
	ON agentquality_candidates(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agentquality_candidates_session
	ON agentquality_candidates(session_id, created_at DESC)
	WHERE session_id != '';
`)
	require.NoError(t, err)

	return NewPGCandidateStore(pool, zap.NewNop()), func() {
		_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS agentquality_candidates`)
		pool.Close()
	}
}
