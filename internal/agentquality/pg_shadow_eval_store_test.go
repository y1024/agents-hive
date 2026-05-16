package agentquality

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestPGShadowEvalResultStoreStoreAndListRecent(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupPGShadowEvalResultStore(t)
	defer cleanup()

	oldResult := ShadowEvalResult{
		CaseID:     "shadow-old",
		DomainID:   "customer_service",
		SourceKind: "workflow",
		Passed:     false,
		JudgeVerdict: EvaluationVerdict{
			Score:       4,
			Verdict:     "工具调用失败",
			FailureType: FailureTool,
			Feedback:    []string{"review_trace"},
		},
		RunnerInfo: RunnerInfo{
			Name:          "agent-run",
			Version:       "test",
			EvidenceLevel: EvidenceProductionShadow,
		},
		TraceRef:       "trace-old",
		ReplayRef:      "replay-old",
		Timestamp:      time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC),
		EvalDurationMS: 42,
	}
	newResult := oldResult
	newResult.CaseID = "shadow-new"
	newResult.Passed = true
	newResult.JudgeVerdict.Score = 9
	newResult.TraceRef = "trace-new"
	newResult.Timestamp = oldResult.Timestamp.Add(time.Hour)
	otherDomain := oldResult
	otherDomain.CaseID = "shadow-other"
	otherDomain.DomainID = "sales"
	otherDomain.TraceRef = "trace-other"
	otherDomain.Timestamp = oldResult.Timestamp.Add(2 * time.Hour)

	require.NoError(t, store.Store(ctx, oldResult))
	require.NoError(t, store.Store(ctx, newResult))
	require.NoError(t, store.Store(ctx, otherDomain))

	customerResults, err := store.ListRecent(ctx, "customer_service", 10)
	require.NoError(t, err)
	require.Len(t, customerResults, 2)
	require.Equal(t, "shadow-new", customerResults[0].CaseID)
	require.Equal(t, "shadow-old", customerResults[1].CaseID)
	require.Equal(t, EvidenceProductionShadow, customerResults[0].RunnerInfo.EvidenceLevel)
	require.Equal(t, FailureTool, customerResults[1].JudgeVerdict.FailureType)
	require.Equal(t, int64(42), customerResults[1].EvalDurationMS)

	latest, err := store.ListRecent(ctx, "", 2)
	require.NoError(t, err)
	require.Len(t, latest, 2)
	require.Equal(t, "shadow-other", latest[0].CaseID)
	require.Equal(t, "shadow-new", latest[1].CaseID)
}

func setupPGShadowEvalResultStore(t *testing.T) (*PGShadowEvalResultStore, func()) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL 未设置，跳过 shadow eval PG 集成测试")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
DROP TABLE IF EXISTS agentquality_shadow_eval_results;
CREATE TABLE IF NOT EXISTS agentquality_shadow_eval_results (
	id               BIGSERIAL PRIMARY KEY,
	case_id          TEXT NOT NULL DEFAULT '',
	domain_id        TEXT NOT NULL DEFAULT '',
	source_kind      TEXT NOT NULL DEFAULT '',
	passed           BOOLEAN NOT NULL DEFAULT FALSE,
	judge_verdict    JSONB NOT NULL DEFAULT '{}',
	runner_info      JSONB NOT NULL DEFAULT '{}',
	trace_ref        TEXT NOT NULL DEFAULT '',
	replay_ref       TEXT NOT NULL DEFAULT '',
	eval_duration_ms BIGINT NOT NULL DEFAULT 0,
	evaluated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_agentquality_shadow_eval_results_domain
	ON agentquality_shadow_eval_results(domain_id, evaluated_at DESC, id DESC);
`)
	require.NoError(t, err)

	return NewPGShadowEvalResultStore(pool), func() {
		_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS agentquality_shadow_eval_results`)
		pool.Close()
	}
}
