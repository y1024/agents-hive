package agentquality

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

type pgOptimizationLifecycleStores struct {
	pool      *pgxpool.Pool
	approvals *PGApprovalStore
	evalDiffs *PGEvalDiffStore
	rollbacks *PGRollbackStore
	rollouts  *PGOptimizationRolloutStore
}

func setupPGOptimizationLifecycleStores(t *testing.T) (pgOptimizationLifecycleStores, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL 未设置，跳过 agentquality optimization PG 集成测试")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
DROP TABLE IF EXISTS optimization_rollbacks;
DROP TABLE IF EXISTS optimization_rollback_alerts;
DROP TABLE IF EXISTS optimization_rollouts;
DROP TABLE IF EXISTS optimization_approvals;
DROP TABLE IF EXISTS optimization_eval_diffs;

CREATE TABLE optimization_eval_diffs (
	id TEXT PRIMARY KEY,
	status TEXT NOT NULL DEFAULT 'pending',
	baseline_run_id TEXT NOT NULL DEFAULT '',
	treatment_run_id TEXT NOT NULL DEFAULT '',
	success_rate_delta DOUBLE PRECISION NOT NULL DEFAULT 0,
	average_cost_delta_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
	average_latency_delta_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
	success_p_value DOUBLE PRECISION NOT NULL DEFAULT 1,
	payload JSONB NOT NULL DEFAULT '{}',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE optimization_approvals (
	id TEXT PRIMARY KEY,
	subject_id TEXT NOT NULL,
	subject_type TEXT NOT NULL,
	action TEXT NOT NULL,
	reviewer TEXT NOT NULL DEFAULT '',
	reviewer_role TEXT NOT NULL DEFAULT '',
	note TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE optimization_rollback_alerts (
	id TEXT PRIMARY KEY,
	status TEXT NOT NULL DEFAULT 'open',
	eval_diff_id TEXT NOT NULL,
	treatment_run_id TEXT NOT NULL DEFAULT '',
	reasons JSONB NOT NULL DEFAULT '[]',
	success_rate_delta DOUBLE PRECISION NOT NULL DEFAULT 0,
	average_latency_delta_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE optimization_rollouts (
	id TEXT PRIMARY KEY,
	suggestion_id TEXT NOT NULL UNIQUE,
	target TEXT NOT NULL DEFAULT '',
	target_key TEXT NOT NULL DEFAULT '',
	previous_value TEXT NOT NULL DEFAULT '',
	previous_exists BOOLEAN NOT NULL DEFAULT FALSE,
	applied_value TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'applied',
	applied_by TEXT NOT NULL DEFAULT '',
	rolled_back_by TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	rolled_back_at TIMESTAMPTZ
);

CREATE TABLE optimization_rollbacks (
	id TEXT PRIMARY KEY,
	suggestion_id TEXT NOT NULL,
	alert_id TEXT NOT NULL DEFAULT '',
	trigger TEXT NOT NULL,
	triggered_by TEXT NOT NULL DEFAULT '',
	rollout JSONB NOT NULL DEFAULT '{}',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`)
	require.NoError(t, err)

	stores := pgOptimizationLifecycleStores{
		pool:      pool,
		approvals: NewPGApprovalStore(pool),
		evalDiffs: NewPGEvalDiffStore(pool),
		rollbacks: NewPGRollbackStore(pool),
		rollouts:  NewPGOptimizationRolloutStore(pool),
	}
	return stores, func() {
		_, _ = pool.Exec(ctx, `
DROP TABLE IF EXISTS optimization_rollbacks;
DROP TABLE IF EXISTS optimization_rollback_alerts;
DROP TABLE IF EXISTS optimization_rollouts;
DROP TABLE IF EXISTS optimization_approvals;
DROP TABLE IF EXISTS optimization_eval_diffs;
`)
		pool.Close()
	}
}
