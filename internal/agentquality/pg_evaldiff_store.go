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

type EvalDiffStore interface {
	UpsertEvalDiff(ctx context.Context, diff EvalDiff) (*EvalDiff, error)
	GetEvalDiff(ctx context.Context, id string) (*EvalDiff, bool, error)
	ListEvalDiffs(ctx context.Context, limit, offset int) ([]EvalDiff, int, error)
}

type PGEvalDiffStore struct {
	pool *pgxpool.Pool
}

func NewPGEvalDiffStore(pool *pgxpool.Pool) *PGEvalDiffStore {
	return &PGEvalDiffStore{pool: pool}
}

func (s *PGEvalDiffStore) UpsertEvalDiff(ctx context.Context, diff EvalDiff) (*EvalDiff, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("eval diff store not configured")
	}
	if strings.TrimSpace(diff.ID) == "" {
		return nil, fmt.Errorf("eval diff id is required")
	}
	if diff.CreatedAt.IsZero() {
		diff.CreatedAt = time.Now()
	}
	if diff.UpdatedAt.IsZero() {
		diff.UpdatedAt = diff.CreatedAt
	}
	payload, err := json.Marshal(diff)
	if err != nil {
		return nil, err
	}
	var out EvalDiff
	row := s.pool.QueryRow(ctx, `
INSERT INTO optimization_eval_diffs
	(id, status, baseline_run_id, treatment_run_id, success_rate_delta, average_cost_delta_usd, average_latency_delta_ms, success_p_value, payload, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (id)
DO UPDATE SET status=$2,
	baseline_run_id=$3,
	treatment_run_id=$4,
	success_rate_delta=$5,
	average_cost_delta_usd=$6,
	average_latency_delta_ms=$7,
	success_p_value=$8,
	payload=$9,
	updated_at=$11
RETURNING payload`,
		diff.ID,
		diff.Status,
		diff.BaselineRunID,
		diff.TreatmentRunID,
		diff.SuccessRateDelta,
		diff.AverageCostDeltaUSD,
		diff.AverageLatencyDeltaMS,
		diff.SuccessPValue,
		payload,
		diff.CreatedAt,
		diff.UpdatedAt,
	)
	if err := scanEvalDiffPayload(row, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *PGEvalDiffStore) GetEvalDiff(ctx context.Context, id string) (*EvalDiff, bool, error) {
	if s == nil || s.pool == nil {
		return nil, false, fmt.Errorf("eval diff store not configured")
	}
	var out EvalDiff
	row := s.pool.QueryRow(ctx, `SELECT payload FROM optimization_eval_diffs WHERE id=$1`, strings.TrimSpace(id))
	if err := scanEvalDiffPayload(row, &out); err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &out, true, nil
}

func (s *PGEvalDiffStore) ListEvalDiffs(ctx context.Context, limit, offset int) ([]EvalDiff, int, error) {
	if s == nil || s.pool == nil {
		return nil, 0, fmt.Errorf("eval diff store not configured")
	}
	limit, offset = normalizeSuggestionPaging(limit, offset)
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM optimization_eval_diffs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT payload FROM optimization_eval_diffs ORDER BY updated_at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]EvalDiff, 0)
	for rows.Next() {
		var row EvalDiff
		if err := scanEvalDiffPayload(rows, &row); err != nil {
			return nil, 0, err
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

type evalDiffPayloadScanner interface {
	Scan(dest ...any) error
}

func scanEvalDiffPayload(row evalDiffPayloadScanner, diff *EvalDiff) error {
	var payload []byte
	if err := row.Scan(&payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, diff)
}
