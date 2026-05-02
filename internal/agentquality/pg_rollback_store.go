package agentquality

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type RollbackAlertStore interface {
	RollbackStore
	RecordAlert(ctx context.Context, alert RollbackAlert) (*RollbackAlert, error)
	ListAlerts(ctx context.Context) ([]RollbackAlert, error)
}

type PGRollbackStore struct {
	pool *pgxpool.Pool
}

func NewPGRollbackStore(pool *pgxpool.Pool) *PGRollbackStore {
	return &PGRollbackStore{pool: pool}
}

func (s *PGRollbackStore) RecordRollback(ctx context.Context, rec RollbackRecord) (*RollbackRecord, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("rollback store not configured")
	}
	if err := normalizeRollbackRecord(&rec); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(rec.Rollout)
	if err != nil {
		return nil, err
	}
	var out RollbackRecord
	row := s.pool.QueryRow(ctx, `
INSERT INTO optimization_rollbacks
	(id, suggestion_id, alert_id, trigger, triggered_by, rollout, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (id) DO UPDATE SET id=optimization_rollbacks.id
RETURNING id, suggestion_id, alert_id, trigger, triggered_by, rollout, created_at`,
		rec.ID,
		rec.SuggestionID,
		rec.AlertID,
		rec.Trigger,
		rec.TriggeredBy,
		payload,
		rec.CreatedAt,
	)
	if err := scanRollbackRecord(row, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *PGRollbackStore) ListRollbacks(ctx context.Context) ([]RollbackRecord, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("rollback store not configured")
	}
	rows, err := s.pool.Query(ctx, rollbackSelectSQL+` ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RollbackRecord, 0)
	for rows.Next() {
		var rec RollbackRecord
		if err := scanRollbackRecord(rows, &rec); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *PGRollbackStore) RecordAlert(ctx context.Context, alert RollbackAlert) (*RollbackAlert, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("rollback store not configured")
	}
	alert.ID = strings.TrimSpace(alert.ID)
	alert.EvalDiffID = strings.TrimSpace(alert.EvalDiffID)
	if alert.ID == "" {
		return nil, fmt.Errorf("alert id is required")
	}
	if alert.EvalDiffID == "" {
		return nil, fmt.Errorf("eval diff id is required")
	}
	if alert.Status == "" {
		alert.Status = RollbackAlertOpen
	}
	if alert.CreatedAt.IsZero() {
		alert.CreatedAt = time.Now()
	}
	reasons, err := json.Marshal(alert.Reasons)
	if err != nil {
		return nil, err
	}
	var out RollbackAlert
	row := s.pool.QueryRow(ctx, `
INSERT INTO optimization_rollback_alerts
	(id, status, eval_diff_id, treatment_run_id, reasons, success_rate_delta, average_latency_delta_ms, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (id) DO UPDATE SET status=$2,
	eval_diff_id=$3,
	treatment_run_id=$4,
	reasons=$5,
	success_rate_delta=$6,
	average_latency_delta_ms=$7
RETURNING id, status, eval_diff_id, treatment_run_id, reasons, success_rate_delta, average_latency_delta_ms, created_at`,
		alert.ID,
		alert.Status,
		alert.EvalDiffID,
		alert.TreatmentRunID,
		reasons,
		alert.SuccessRateDelta,
		alert.AverageLatencyDeltaMS,
		alert.CreatedAt,
	)
	if err := scanRollbackAlert(row, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *PGRollbackStore) ListAlerts(ctx context.Context) ([]RollbackAlert, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("rollback store not configured")
	}
	rows, err := s.pool.Query(ctx, rollbackAlertSelectSQL+` ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RollbackAlert, 0)
	for rows.Next() {
		var alert RollbackAlert
		if err := scanRollbackAlert(rows, &alert); err != nil {
			return nil, err
		}
		out = append(out, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

const rollbackSelectSQL = `SELECT id, suggestion_id, alert_id, trigger, triggered_by, rollout, created_at FROM optimization_rollbacks`

const rollbackAlertSelectSQL = `SELECT id, status, eval_diff_id, treatment_run_id, reasons, success_rate_delta, average_latency_delta_ms, created_at FROM optimization_rollback_alerts`

type rollbackScanner interface {
	Scan(dest ...any) error
}

func scanRollbackRecord(row rollbackScanner, rec *RollbackRecord) error {
	var rolloutJSON []byte
	if err := row.Scan(
		&rec.ID,
		&rec.SuggestionID,
		&rec.AlertID,
		&rec.Trigger,
		&rec.TriggeredBy,
		&rolloutJSON,
		&rec.CreatedAt,
	); err != nil {
		return err
	}
	if len(rolloutJSON) > 0 {
		return json.Unmarshal(rolloutJSON, &rec.Rollout)
	}
	return nil
}

func scanRollbackAlert(row rollbackScanner, alert *RollbackAlert) error {
	var reasonsJSON []byte
	if err := row.Scan(
		&alert.ID,
		&alert.Status,
		&alert.EvalDiffID,
		&alert.TreatmentRunID,
		&reasonsJSON,
		&alert.SuccessRateDelta,
		&alert.AverageLatencyDeltaMS,
		&alert.CreatedAt,
	); err != nil {
		return err
	}
	if len(reasonsJSON) > 0 {
		return json.Unmarshal(reasonsJSON, &alert.Reasons)
	}
	return nil
}
