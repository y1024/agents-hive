package store

import (
	"strings"
	"testing"
)

func TestPGInitSQLIncludesAgentQualityCandidates(t *testing.T) {
	sql := strings.Join(strings.Fields(pgInitSQL), " ")
	required := []string{
		"CREATE TABLE IF NOT EXISTS agentquality_candidates",
		"id TEXT PRIMARY KEY",
		"case_json JSONB NOT NULL DEFAULT '{}'",
		"source_event JSONB NOT NULL DEFAULT '{}'",
		"suggestions_json JSONB NOT NULL DEFAULT '[]'",
		"cluster_id TEXT NOT NULL DEFAULT ''",
		"verify_result JSONB NOT NULL DEFAULT '{}'",
		"last_verified_at TIMESTAMPTZ",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_agentquality_candidates_fingerprint",
		"WHERE status IN ('new', 'reviewing', 'approved')",
		"CREATE TABLE IF NOT EXISTS agentquality_optimization_suggestions",
		"source_candidate_id TEXT NOT NULL DEFAULT ''",
		"source_eval_diff_id TEXT NOT NULL DEFAULT ''",
		"runner_info JSONB NOT NULL DEFAULT '{}'",
		"proposed_value TEXT NOT NULL DEFAULT ''",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_opt_suggestions_status_created",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_opt_suggestions_source_candidate",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_opt_suggestions_target",
		"CREATE TABLE IF NOT EXISTS agentquality_shadow_eval_results",
		"ALTER TABLE agentquality_shadow_eval_results ADD COLUMN IF NOT EXISTS case_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agentquality_shadow_eval_results ADD COLUMN IF NOT EXISTS domain_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agentquality_shadow_eval_results ADD COLUMN IF NOT EXISTS source_kind TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agentquality_shadow_eval_results ADD COLUMN IF NOT EXISTS passed BOOLEAN NOT NULL DEFAULT FALSE",
		"judge_verdict JSONB NOT NULL DEFAULT '{}'",
		"runner_info JSONB NOT NULL DEFAULT '{}'",
		"evaluated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_shadow_eval_results_domain",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_shadow_eval_results_trace",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_shadow_eval_results_runner",
		"CREATE TABLE IF NOT EXISTS optimization_eval_diffs",
		"CREATE TABLE IF NOT EXISTS optimization_approvals",
		"CREATE TABLE IF NOT EXISTS optimization_rollback_alerts",
		"CREATE TABLE IF NOT EXISTS optimization_rollbacks",
		"CREATE TABLE IF NOT EXISTS embedding_backlog",
		"vector_space TEXT NOT NULL DEFAULT 'memory:default'",
		"CREATE TABLE IF NOT EXISTS agentquality_grouping_rules",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_grouping_rules_priority",
		"CREATE TABLE IF NOT EXISTS optimization_tool_descriptions",
		"CREATE TABLE IF NOT EXISTS memory_governance_policies",
		"CREATE TABLE IF NOT EXISTS optimization_rollouts",
		"previous_exists BOOLEAN NOT NULL DEFAULT FALSE",
		"CREATE INDEX IF NOT EXISTS idx_optimization_rollouts_status",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_candidates_status_created",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_candidates_session",
	}

	for _, needle := range required {
		if !strings.Contains(sql, needle) {
			t.Fatalf("pgInitSQL missing %q", needle)
		}
	}
}

func TestPGInitSQLDoesNotCreateIndexesBeforeCompatColumns(t *testing.T) {
	sql := strings.Join(strings.Fields(pgInitSQL), " ")
	forbidden := []string{
		"CREATE INDEX IF NOT EXISTS idx_usage_records_quality_case",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_candidates_cluster",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_opt_suggestions_source_eval_diff",
	}

	for _, needle := range forbidden {
		if strings.Contains(sql, needle) {
			t.Fatalf("pgInitSQL must not create %q before pgAddUserColumns has added compatibility columns", needle)
		}
	}
}

func TestPGInitSQLAddsQualityWorkbenchAttributionColumnsBeforeIndexes(t *testing.T) {
	sql := strings.Join(strings.Fields(pgInitSQL), " ")
	requiredOrder := []struct {
		column string
		index  string
	}{
		{
			column: "ALTER TABLE qualityworkbench_replay_jobs ADD COLUMN IF NOT EXISTS domain_id TEXT NOT NULL DEFAULT ''",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_replay_jobs_attribution",
		},
		{
			column: "ALTER TABLE qualityworkbench_replay_jobs ADD COLUMN IF NOT EXISTS source_kind TEXT NOT NULL DEFAULT ''",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_replay_jobs_attribution",
		},
		{
			column: "ALTER TABLE qualityworkbench_replay_jobs ADD COLUMN IF NOT EXISTS source_name TEXT NOT NULL DEFAULT ''",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_replay_jobs_attribution",
		},
		{
			column: "ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS domain_id TEXT NOT NULL DEFAULT ''",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_batch_eval_runs_attribution",
		},
		{
			column: "ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS source_kind TEXT NOT NULL DEFAULT ''",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_batch_eval_runs_attribution",
		},
		{
			column: "ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS source_name TEXT NOT NULL DEFAULT ''",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_batch_eval_runs_attribution",
		},
		{
			column: "ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS runner_info JSONB NOT NULL DEFAULT '{}'",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_batch_eval_runs_attribution",
		},
		{
			column: "ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS gate_metrics JSONB NOT NULL DEFAULT '{}'",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_batch_eval_runs_attribution",
		},
		{
			column: "ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS judge_verdict JSONB NOT NULL DEFAULT '{}'",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_batch_eval_runs_attribution",
		},
		{
			column: "ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS shadow_metrics JSONB NOT NULL DEFAULT '[]'",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_batch_eval_runs_attribution",
		},
		{
			column: "ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS shadow_results JSONB NOT NULL DEFAULT '[]'",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_batch_eval_runs_attribution",
		},
		{
			column: "ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS domain_regressions JSONB NOT NULL DEFAULT '[]'",
			index:  "CREATE INDEX IF NOT EXISTS idx_qualityworkbench_batch_eval_runs_attribution",
		},
	}

	for _, tt := range requiredOrder {
		columnPos := strings.Index(sql, tt.column)
		if columnPos < 0 {
			t.Fatalf("pgInitSQL missing compatibility column %q", tt.column)
		}
		indexPos := strings.Index(sql, tt.index)
		if indexPos < 0 {
			t.Fatalf("pgInitSQL missing attribution index %q", tt.index)
		}
		if columnPos > indexPos {
			t.Fatalf("pgInitSQL creates %q before compatibility column %q", tt.index, tt.column)
		}
	}
}

func TestPGAddUserColumnsCreatesIndexesAfterCompatColumns(t *testing.T) {
	sql := strings.Join(strings.Fields(pgAddUserColumns), " ")
	required := []string{
		"ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS quality_case_id TEXT NOT NULL DEFAULT ''",
		"CREATE INDEX IF NOT EXISTS idx_usage_records_quality_case",
		"ALTER TABLE agentquality_candidates ADD COLUMN IF NOT EXISTS cluster_id TEXT NOT NULL DEFAULT ''",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_candidates_cluster",
		"ALTER TABLE agentquality_optimization_suggestions ADD COLUMN IF NOT EXISTS source_eval_diff_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agentquality_optimization_suggestions ADD COLUMN IF NOT EXISTS runner_info JSONB NOT NULL DEFAULT '{}'",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_opt_suggestions_source_eval_diff",
		"CREATE TABLE IF NOT EXISTS agentquality_shadow_eval_results",
		"ALTER TABLE agentquality_shadow_eval_results ADD COLUMN IF NOT EXISTS case_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agentquality_shadow_eval_results ADD COLUMN IF NOT EXISTS domain_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE agentquality_shadow_eval_results ADD COLUMN IF NOT EXISTS evaluated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()",
		"CREATE INDEX IF NOT EXISTS idx_agentquality_shadow_eval_results_domain",
		"ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS runner_info JSONB NOT NULL DEFAULT '{}'",
		"ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS gate_metrics JSONB NOT NULL DEFAULT '{}'",
		"ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS judge_verdict JSONB NOT NULL DEFAULT '{}'",
		"ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS shadow_metrics JSONB NOT NULL DEFAULT '[]'",
		"ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS shadow_results JSONB NOT NULL DEFAULT '[]'",
		"ALTER TABLE qualityworkbench_batch_eval_runs ADD COLUMN IF NOT EXISTS domain_regressions JSONB NOT NULL DEFAULT '[]'",
	}

	for _, needle := range required {
		if !strings.Contains(sql, needle) {
			t.Fatalf("pgAddUserColumns missing %q", needle)
		}
	}
}
