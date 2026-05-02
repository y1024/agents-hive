package agentquality

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RolloutStatus string

const (
	RolloutApplied    RolloutStatus = "applied"
	RolloutRolledBack RolloutStatus = "rolled_back"
)

type OptimizationRollout struct {
	ID             string           `json:"id"`
	SuggestionID   string           `json:"suggestion_id"`
	Target         SuggestionTarget `json:"target"`
	TargetKey      string           `json:"target_key"`
	PreviousValue  string           `json:"previous_value"`
	PreviousExists bool             `json:"previous_exists"`
	AppliedValue   string           `json:"applied_value"`
	Status         RolloutStatus    `json:"status"`
	AppliedBy      string           `json:"applied_by"`
	RolledBackBy   string           `json:"rolled_back_by,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	RolledBackAt   *time.Time       `json:"rolled_back_at,omitempty"`
}

type OptimizationRolloutStore interface {
	RecordApplied(ctx context.Context, rollout OptimizationRollout) (*OptimizationRollout, error)
	GetBySuggestion(ctx context.Context, suggestionID string) (*OptimizationRollout, bool, error)
	MarkRolledBack(ctx context.Context, suggestionID, rolledBackBy string, now time.Time) (*OptimizationRollout, error)
}

type InMemoryOptimizationRolloutStore struct {
	mu   sync.RWMutex
	rows map[string]OptimizationRollout
}

func NewInMemoryOptimizationRolloutStore() *InMemoryOptimizationRolloutStore {
	return &InMemoryOptimizationRolloutStore{rows: map[string]OptimizationRollout{}}
}

func (s *InMemoryOptimizationRolloutStore) RecordApplied(ctx context.Context, rollout OptimizationRollout) (*OptimizationRollout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(rollout.SuggestionID) == "" {
		return nil, fmt.Errorf("suggestion id is required")
	}
	if rollout.ID == "" {
		rollout.ID = "rollout_" + rollout.SuggestionID
	}
	if rollout.Status == "" {
		rollout.Status = RolloutApplied
	}
	if rollout.CreatedAt.IsZero() {
		rollout.CreatedAt = time.Now()
	}
	if rollout.UpdatedAt.IsZero() {
		rollout.UpdatedAt = rollout.CreatedAt
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = map[string]OptimizationRollout{}
	}
	s.rows[rollout.SuggestionID] = rollout
	return cloneRollout(rollout), nil
}

func (s *InMemoryOptimizationRolloutStore) GetBySuggestion(ctx context.Context, suggestionID string) (*OptimizationRollout, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.rows[suggestionID]
	if !ok {
		return nil, false, nil
	}
	return cloneRollout(row), true, nil
}

func (s *InMemoryOptimizationRolloutStore) MarkRolledBack(ctx context.Context, suggestionID, rolledBackBy string, now time.Time) (*OptimizationRollout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[suggestionID]
	if !ok {
		return nil, fmt.Errorf("rollout for suggestion %s not found", suggestionID)
	}
	if row.Status == RolloutRolledBack {
		return cloneRollout(row), nil
	}
	row.Status = RolloutRolledBack
	row.RolledBackBy = strings.TrimSpace(rolledBackBy)
	row.RolledBackAt = &now
	row.UpdatedAt = now
	s.rows[suggestionID] = row
	return cloneRollout(row), nil
}

func cloneRollout(row OptimizationRollout) *OptimizationRollout {
	out := row
	if row.RolledBackAt != nil {
		at := *row.RolledBackAt
		out.RolledBackAt = &at
	}
	return &out
}

type PGOptimizationRolloutStore struct {
	pool *pgxpool.Pool
}

func NewPGOptimizationRolloutStore(pool *pgxpool.Pool) *PGOptimizationRolloutStore {
	return &PGOptimizationRolloutStore{pool: pool}
}

func (s *PGOptimizationRolloutStore) RecordApplied(ctx context.Context, rollout OptimizationRollout) (*OptimizationRollout, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("optimization rollout store not configured")
	}
	if strings.TrimSpace(rollout.SuggestionID) == "" {
		return nil, fmt.Errorf("suggestion id is required")
	}
	if rollout.ID == "" {
		rollout.ID = "rollout_" + rollout.SuggestionID
	}
	if rollout.Status == "" {
		rollout.Status = RolloutApplied
	}
	if rollout.CreatedAt.IsZero() {
		rollout.CreatedAt = time.Now()
	}
	if rollout.UpdatedAt.IsZero() {
		rollout.UpdatedAt = rollout.CreatedAt
	}
	var out OptimizationRollout
	row := s.pool.QueryRow(ctx, `
INSERT INTO optimization_rollouts
	(id, suggestion_id, target, target_key, previous_value, previous_exists, applied_value, status, applied_by, rolled_back_by, created_at, updated_at, rolled_back_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (suggestion_id) DO UPDATE
SET target=$3,
	target_key=$4,
	previous_value=$5,
	previous_exists=$6,
	applied_value=$7,
	status=$8,
	applied_by=$9,
	rolled_back_by='',
	updated_at=$12,
	rolled_back_at=NULL
RETURNING id, suggestion_id, target, target_key, previous_value, previous_exists, applied_value, status, applied_by, rolled_back_by, created_at, updated_at, rolled_back_at`,
		rollout.ID,
		rollout.SuggestionID,
		rollout.Target,
		rollout.TargetKey,
		rollout.PreviousValue,
		rollout.PreviousExists,
		rollout.AppliedValue,
		rollout.Status,
		strings.TrimSpace(rollout.AppliedBy),
		strings.TrimSpace(rollout.RolledBackBy),
		rollout.CreatedAt,
		rollout.UpdatedAt,
		rollout.RolledBackAt,
	)
	if err := scanOptimizationRollout(row, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *PGOptimizationRolloutStore) GetBySuggestion(ctx context.Context, suggestionID string) (*OptimizationRollout, bool, error) {
	if s == nil || s.pool == nil {
		return nil, false, fmt.Errorf("optimization rollout store not configured")
	}
	var out OptimizationRollout
	row := s.pool.QueryRow(ctx, rolloutSelectSQL+` WHERE suggestion_id=$1`, strings.TrimSpace(suggestionID))
	if err := scanOptimizationRollout(row, &out); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &out, true, nil
}

func (s *PGOptimizationRolloutStore) MarkRolledBack(ctx context.Context, suggestionID, rolledBackBy string, now time.Time) (*OptimizationRollout, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("optimization rollout store not configured")
	}
	var out OptimizationRollout
	row := s.pool.QueryRow(ctx, `
UPDATE optimization_rollouts
SET status=$2, rolled_back_by=$3, rolled_back_at=$4, updated_at=$4
WHERE suggestion_id=$1
RETURNING id, suggestion_id, target, target_key, previous_value, previous_exists, applied_value, status, applied_by, rolled_back_by, created_at, updated_at, rolled_back_at`,
		strings.TrimSpace(suggestionID), RolloutRolledBack, strings.TrimSpace(rolledBackBy), now,
	)
	if err := scanOptimizationRollout(row, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

const rolloutSelectSQL = `SELECT id, suggestion_id, target, target_key, previous_value, previous_exists, applied_value, status, applied_by, rolled_back_by, created_at, updated_at, rolled_back_at
FROM optimization_rollouts`

type rolloutScanner interface {
	Scan(dest ...any) error
}

func scanOptimizationRollout(row rolloutScanner, rec *OptimizationRollout) error {
	return row.Scan(
		&rec.ID,
		&rec.SuggestionID,
		&rec.Target,
		&rec.TargetKey,
		&rec.PreviousValue,
		&rec.PreviousExists,
		&rec.AppliedValue,
		&rec.Status,
		&rec.AppliedBy,
		&rec.RolledBackBy,
		&rec.CreatedAt,
		&rec.UpdatedAt,
		&rec.RolledBackAt,
	)
}
