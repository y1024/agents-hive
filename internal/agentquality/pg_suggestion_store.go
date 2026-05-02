package agentquality

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type PGOptimizationSuggestionStore struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

func NewPGOptimizationSuggestionStore(pool *pgxpool.Pool, logger *zap.Logger) *PGOptimizationSuggestionStore {
	return &PGOptimizationSuggestionStore{pool: pool, logger: logger}
}

func (s *PGOptimizationSuggestionStore) UpsertSuggestion(ctx context.Context, rec OptimizationReviewSuggestion) (*OptimizationReviewSuggestion, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("suggestion store not configured")
	}
	if err := normalizeSuggestion(&rec); err != nil {
		return nil, err
	}

	var out OptimizationReviewSuggestion
	row := s.pool.QueryRow(ctx, `
INSERT INTO agentquality_optimization_suggestions
	(id, status, target, kind, title, rationale, current_value, proposed_value, diff_format, source_candidate_id, source_event,
	 source_eval_diff_id, review_required, created_by, approved_by, approval_note, apply_status, applied_by, apply_error, created_at, updated_at, approved_at, applied_at, expires_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
ON CONFLICT (id)
DO UPDATE SET updated_at = agentquality_optimization_suggestions.updated_at
RETURNING id, status, target, kind, title, rationale, current_value, proposed_value, diff_format, source_candidate_id, source_event,
	source_eval_diff_id, review_required, created_by, approved_by, approval_note, apply_status, applied_by, apply_error, created_at, updated_at, approved_at, applied_at, expires_at`,
		rec.ID, rec.Status, rec.Target, rec.Kind, rec.Title, rec.Rationale, rec.CurrentValue, rec.ProposedValue, rec.DiffFormat,
		rec.SourceCandidateID, mustJSON(rec.SourceEvent), rec.SourceEvalDiffID, rec.ReviewRequired, rec.CreatedBy, rec.ApprovedBy, rec.ApprovalNote,
		rec.ApplyStatus, rec.AppliedBy, rec.ApplyError, rec.CreatedAt, rec.UpdatedAt, rec.ApprovedAt, rec.AppliedAt, rec.ExpiresAt,
	)
	if err := scanOptimizationSuggestion(row, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *PGOptimizationSuggestionStore) ListSuggestions(ctx context.Context, filter SuggestionFilter) ([]OptimizationReviewSuggestion, int, error) {
	if s == nil || s.pool == nil {
		return nil, 0, fmt.Errorf("suggestion store not configured")
	}
	limit, offset := normalizeSuggestionPaging(filter.Limit, filter.Offset)
	where, args := suggestionWhere(filter)

	var total int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM agentquality_optimization_suggestions"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := s.pool.Query(ctx, optimizationSuggestionSelectSQL+where+fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out, err := scanOptimizationSuggestionRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (s *PGOptimizationSuggestionStore) GetSuggestion(ctx context.Context, id string) (*OptimizationReviewSuggestion, bool, error) {
	if s == nil || s.pool == nil {
		return nil, false, fmt.Errorf("suggestion store not configured")
	}
	var out OptimizationReviewSuggestion
	row := s.pool.QueryRow(ctx, optimizationSuggestionSelectSQL+" WHERE id=$1", id)
	if err := scanOptimizationSuggestion(row, &out); err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &out, true, nil
}

func (s *PGOptimizationSuggestionStore) UpdateSuggestionStatus(ctx context.Context, id string, status SuggestionStatus, reviewer, note string, now time.Time) (*OptimizationReviewSuggestion, error) {
	switch status {
	case SuggestionApproved:
		return s.ApproveSuggestion(ctx, id, reviewer, note, now)
	case SuggestionRejected:
		return s.RejectSuggestion(ctx, id, reviewer, note, now)
	default:
		return nil, fmt.Errorf("unsupported suggestion status update %q", status)
	}
}

func (s *PGOptimizationSuggestionStore) ApproveSuggestion(ctx context.Context, id string, reviewer, note string, now time.Time) (*OptimizationReviewSuggestion, error) {
	return s.transitionSuggestion(ctx, id, func(row OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error) {
		return row.Approve(reviewer, note, now)
	})
}

func (s *PGOptimizationSuggestionStore) RejectSuggestion(ctx context.Context, id string, reviewer, note string, now time.Time) (*OptimizationReviewSuggestion, error) {
	return s.transitionSuggestion(ctx, id, func(row OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error) {
		return row.Reject(reviewer, note, now)
	})
}

func (s *PGOptimizationSuggestionStore) MarkSuggestionApplied(ctx context.Context, id string, appliedBy string, now time.Time) (*OptimizationReviewSuggestion, error) {
	return s.transitionSuggestion(ctx, id, func(row OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error) {
		return row.MarkApplied(appliedBy, now), nil
	})
}

func (s *PGOptimizationSuggestionStore) MarkSuggestionApplyError(ctx context.Context, id string, appliedBy, message string, now time.Time) (*OptimizationReviewSuggestion, error) {
	return s.transitionSuggestion(ctx, id, func(row OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error) {
		return row.MarkApplyError(appliedBy, message, now), nil
	})
}

func (s *PGOptimizationSuggestionStore) MarkSuggestionNotApplicable(ctx context.Context, id string, appliedBy, message string, now time.Time) (*OptimizationReviewSuggestion, error) {
	return s.transitionSuggestion(ctx, id, func(row OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error) {
		return row.MarkNotApplicable(appliedBy, message, now), nil
	})
}

func (s *PGOptimizationSuggestionStore) transitionSuggestion(ctx context.Context, id string, apply func(OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error)) (*OptimizationReviewSuggestion, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("suggestion store not configured")
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("suggestion id required")
	}
	current, ok, err := s.GetSuggestion(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, pgx.ErrNoRows
	}
	next, err := apply(*current)
	if err != nil {
		return nil, err
	}

	var out OptimizationReviewSuggestion
	row := s.pool.QueryRow(ctx, `
UPDATE agentquality_optimization_suggestions
SET status=$2,
	approved_by=$3,
	approval_note=$4,
	approved_at=$5,
	apply_status=$6,
	applied_by=$7,
	apply_error=$8,
	applied_at=$9,
	updated_at=$10
WHERE id=$1 AND status=$11
RETURNING id, status, target, kind, title, rationale, current_value, proposed_value, diff_format, source_candidate_id, source_event,
	source_eval_diff_id, review_required, created_by, approved_by, approval_note, apply_status, applied_by, apply_error, created_at, updated_at, approved_at, applied_at, expires_at`,
		id, next.Status, next.ApprovedBy, next.ApprovalNote, next.ApprovedAt, next.ApplyStatus, next.AppliedBy, next.ApplyError, next.AppliedAt, next.UpdatedAt, current.Status,
	)
	if err := scanOptimizationSuggestion(row, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func suggestionWhere(filter SuggestionFilter) (string, []any) {
	var clauses []string
	var args []any
	if filter.Status != "" {
		clauses = append(clauses, fmt.Sprintf("status=$%d", len(args)+1))
		args = append(args, filter.Status)
	}
	if filter.Target != "" {
		clauses = append(clauses, fmt.Sprintf("target=$%d", len(args)+1))
		args = append(args, filter.Target)
	}
	if filter.SourceCandidateID != "" {
		clauses = append(clauses, fmt.Sprintf("source_candidate_id=$%d", len(args)+1))
		args = append(args, filter.SourceCandidateID)
	}
	if filter.SourceEvalDiffID != "" {
		clauses = append(clauses, fmt.Sprintf("source_eval_diff_id=$%d", len(args)+1))
		args = append(args, filter.SourceEvalDiffID)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

const optimizationSuggestionSelectSQL = `SELECT id, status, target, kind, title, rationale, current_value, proposed_value, diff_format, source_candidate_id, source_event,
	source_eval_diff_id, review_required, created_by, approved_by, approval_note, apply_status, applied_by, apply_error, created_at, updated_at, approved_at, applied_at, expires_at
FROM agentquality_optimization_suggestions`

type optimizationSuggestionScanner interface {
	Scan(dest ...any) error
}

func scanOptimizationSuggestion(row optimizationSuggestionScanner, rec *OptimizationReviewSuggestion) error {
	var eventJSON []byte
	if err := row.Scan(
		&rec.ID,
		&rec.Status,
		&rec.Target,
		&rec.Kind,
		&rec.Title,
		&rec.Rationale,
		&rec.CurrentValue,
		&rec.ProposedValue,
		&rec.DiffFormat,
		&rec.SourceCandidateID,
		&eventJSON,
		&rec.SourceEvalDiffID,
		&rec.ReviewRequired,
		&rec.CreatedBy,
		&rec.ApprovedBy,
		&rec.ApprovalNote,
		&rec.ApplyStatus,
		&rec.AppliedBy,
		&rec.ApplyError,
		&rec.CreatedAt,
		&rec.UpdatedAt,
		&rec.ApprovedAt,
		&rec.AppliedAt,
		&rec.ExpiresAt,
	); err != nil {
		return err
	}
	if len(eventJSON) > 0 {
		if err := json.Unmarshal(eventJSON, &rec.SourceEvent); err != nil {
			return err
		}
	}
	return nil
}

func scanOptimizationSuggestionRows(rows pgx.Rows) ([]OptimizationReviewSuggestion, error) {
	var out []OptimizationReviewSuggestion
	for rows.Next() {
		var rec OptimizationReviewSuggestion
		if err := scanOptimizationSuggestion(rows, &rec); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
