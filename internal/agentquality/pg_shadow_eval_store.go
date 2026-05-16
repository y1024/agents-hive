package agentquality

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ ShadowEvalResultStore = (*PGShadowEvalResultStore)(nil)

// PGShadowEvalResultStore 持久化生产 shadow eval 结果，供后续 shadow batch 读取历史证据。
type PGShadowEvalResultStore struct {
	pool *pgxpool.Pool
}

func NewPGShadowEvalResultStore(pool *pgxpool.Pool) *PGShadowEvalResultStore {
	return &PGShadowEvalResultStore{pool: pool}
}

func (s *PGShadowEvalResultStore) Store(ctx context.Context, result ShadowEvalResult) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("shadow eval result store not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if result.Timestamp.IsZero() {
		result.Timestamp = time.Now().UTC()
	}
	judgeVerdictJSON, err := json.Marshal(result.JudgeVerdict)
	if err != nil {
		return err
	}
	runnerInfoJSON, err := json.Marshal(result.RunnerInfo)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
INSERT INTO agentquality_shadow_eval_results
	(case_id, domain_id, source_kind, passed, judge_verdict, runner_info, trace_ref, replay_ref, eval_duration_ms, evaluated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		strings.TrimSpace(result.CaseID),
		strings.TrimSpace(result.DomainID),
		strings.TrimSpace(result.SourceKind),
		result.Passed,
		judgeVerdictJSON,
		runnerInfoJSON,
		strings.TrimSpace(result.TraceRef),
		strings.TrimSpace(result.ReplayRef),
		result.EvalDurationMS,
		result.Timestamp,
	)
	return err
}

func (s *PGShadowEvalResultStore) ListRecent(ctx context.Context, domainID string, limit int) ([]ShadowEvalResult, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("shadow eval result store not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	var (
		where string
		args  []any
	)
	if domainID = strings.TrimSpace(domainID); domainID != "" {
		where = " WHERE domain_id=$1"
		args = append(args, domainID)
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, pgShadowEvalResultSelectSQL+where+fmt.Sprintf(" ORDER BY evaluated_at DESC, id DESC LIMIT $%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPGShadowEvalResultRows(rows)
}

const pgShadowEvalResultSelectSQL = `SELECT case_id, domain_id, source_kind, passed, judge_verdict, runner_info, trace_ref, replay_ref, evaluated_at, eval_duration_ms FROM agentquality_shadow_eval_results`

func scanPGShadowEvalResult(row interface{ Scan(dest ...any) error }) (ShadowEvalResult, error) {
	var (
		result           ShadowEvalResult
		judgeVerdictJSON []byte
		runnerInfoJSON   []byte
	)
	if err := row.Scan(
		&result.CaseID,
		&result.DomainID,
		&result.SourceKind,
		&result.Passed,
		&judgeVerdictJSON,
		&runnerInfoJSON,
		&result.TraceRef,
		&result.ReplayRef,
		&result.Timestamp,
		&result.EvalDurationMS,
	); err != nil {
		return ShadowEvalResult{}, err
	}
	if len(judgeVerdictJSON) > 0 {
		if err := json.Unmarshal(judgeVerdictJSON, &result.JudgeVerdict); err != nil {
			return ShadowEvalResult{}, err
		}
	}
	if len(runnerInfoJSON) > 0 {
		if err := json.Unmarshal(runnerInfoJSON, &result.RunnerInfo); err != nil {
			return ShadowEvalResult{}, err
		}
	}
	return result, nil
}

func scanPGShadowEvalResultRows(rows pgx.Rows) ([]ShadowEvalResult, error) {
	var out []ShadowEvalResult
	for rows.Next() {
		result, err := scanPGShadowEvalResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, rows.Err()
}
