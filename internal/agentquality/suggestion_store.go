package agentquality

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type SuggestionFilter struct {
	Status            SuggestionStatus
	Target            SuggestionTarget
	SourceCandidateID string
	SourceEvalDiffID  string
	Limit             int
	Offset            int
}

type OptimizationSuggestionStore interface {
	UpsertSuggestion(ctx context.Context, rec OptimizationReviewSuggestion) (*OptimizationReviewSuggestion, error)
	ListSuggestions(ctx context.Context, filter SuggestionFilter) ([]OptimizationReviewSuggestion, int, error)
	GetSuggestion(ctx context.Context, id string) (*OptimizationReviewSuggestion, bool, error)
	UpdateSuggestionStatus(ctx context.Context, id string, status SuggestionStatus, reviewer, note string, now time.Time) (*OptimizationReviewSuggestion, error)
	ApproveSuggestion(ctx context.Context, id string, reviewer, note string, now time.Time) (*OptimizationReviewSuggestion, error)
	RejectSuggestion(ctx context.Context, id string, reviewer, note string, now time.Time) (*OptimizationReviewSuggestion, error)
	MarkSuggestionApplied(ctx context.Context, id string, appliedBy string, now time.Time) (*OptimizationReviewSuggestion, error)
	MarkSuggestionApplyError(ctx context.Context, id string, appliedBy, message string, now time.Time) (*OptimizationReviewSuggestion, error)
	MarkSuggestionNotApplicable(ctx context.Context, id string, appliedBy, message string, now time.Time) (*OptimizationReviewSuggestion, error)
}

type InMemoryOptimizationSuggestionStore struct {
	mu   sync.RWMutex
	rows map[string]OptimizationReviewSuggestion
}

func NewInMemoryOptimizationSuggestionStore() *InMemoryOptimizationSuggestionStore {
	return &InMemoryOptimizationSuggestionStore{rows: make(map[string]OptimizationReviewSuggestion)}
}

func (s *InMemoryOptimizationSuggestionStore) UpsertSuggestion(ctx context.Context, rec OptimizationReviewSuggestion) (*OptimizationReviewSuggestion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("suggestion store not configured")
	}
	if err := normalizeSuggestion(&rec); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = make(map[string]OptimizationReviewSuggestion)
	}
	if existing, ok := s.rows[rec.ID]; ok {
		return cloneSuggestion(existing), nil
	}
	s.rows[rec.ID] = rec
	return cloneSuggestion(rec), nil
}

func (s *InMemoryOptimizationSuggestionStore) ListSuggestions(ctx context.Context, filter SuggestionFilter) ([]OptimizationReviewSuggestion, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if s == nil {
		return nil, 0, fmt.Errorf("suggestion store not configured")
	}
	limit, offset := normalizeSuggestionPaging(filter.Limit, filter.Offset)

	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]OptimizationReviewSuggestion, 0, len(s.rows))
	for _, row := range s.rows {
		if !matchSuggestionFilter(row, filter) {
			continue
		}
		matched = append(matched, row)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID > matched[j].ID
		}
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	total := len(matched)
	if offset >= total {
		return []OptimizationReviewSuggestion{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return cloneSuggestions(matched[offset:end]), total, nil
}

func (s *InMemoryOptimizationSuggestionStore) GetSuggestion(ctx context.Context, id string) (*OptimizationReviewSuggestion, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if s == nil {
		return nil, false, fmt.Errorf("suggestion store not configured")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.rows[id]
	if !ok {
		return nil, false, nil
	}
	return cloneSuggestion(row), true, nil
}

func (s *InMemoryOptimizationSuggestionStore) UpdateSuggestionStatus(ctx context.Context, id string, status SuggestionStatus, reviewer, note string, now time.Time) (*OptimizationReviewSuggestion, error) {
	switch status {
	case SuggestionApproved:
		return s.ApproveSuggestion(ctx, id, reviewer, note, now)
	case SuggestionRejected:
		return s.RejectSuggestion(ctx, id, reviewer, note, now)
	default:
		return nil, fmt.Errorf("unsupported suggestion status update %q", status)
	}
}

func (s *InMemoryOptimizationSuggestionStore) ApproveSuggestion(ctx context.Context, id string, reviewer, note string, now time.Time) (*OptimizationReviewSuggestion, error) {
	return s.transitionSuggestion(ctx, id, func(row OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error) {
		return row.Approve(reviewer, note, now)
	})
}

func (s *InMemoryOptimizationSuggestionStore) RejectSuggestion(ctx context.Context, id string, reviewer, note string, now time.Time) (*OptimizationReviewSuggestion, error) {
	return s.transitionSuggestion(ctx, id, func(row OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error) {
		return row.Reject(reviewer, note, now)
	})
}

func (s *InMemoryOptimizationSuggestionStore) MarkSuggestionApplied(ctx context.Context, id string, appliedBy string, now time.Time) (*OptimizationReviewSuggestion, error) {
	return s.transitionSuggestion(ctx, id, func(row OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error) {
		if err := row.ValidateApprovalEvidence(); err != nil {
			return row, err
		}
		return row.MarkApplied(appliedBy, now), nil
	})
}

func (s *InMemoryOptimizationSuggestionStore) MarkSuggestionApplyError(ctx context.Context, id string, appliedBy, message string, now time.Time) (*OptimizationReviewSuggestion, error) {
	return s.transitionSuggestion(ctx, id, func(row OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error) {
		return row.MarkApplyError(appliedBy, message, now), nil
	})
}

func (s *InMemoryOptimizationSuggestionStore) MarkSuggestionNotApplicable(ctx context.Context, id string, appliedBy, message string, now time.Time) (*OptimizationReviewSuggestion, error) {
	return s.transitionSuggestion(ctx, id, func(row OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error) {
		return row.MarkNotApplicable(appliedBy, message, now), nil
	})
}

func (s *InMemoryOptimizationSuggestionStore) transitionSuggestion(ctx context.Context, id string, apply func(OptimizationReviewSuggestion) (OptimizationReviewSuggestion, error)) (*OptimizationReviewSuggestion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("suggestion store not configured")
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("suggestion id required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return nil, fmt.Errorf("suggestion %s not found", id)
	}
	next, err := apply(row)
	if err != nil {
		return nil, err
	}
	s.rows[id] = next
	return cloneSuggestion(next), nil
}

func normalizeSuggestion(rec *OptimizationReviewSuggestion) error {
	if strings.TrimSpace(rec.ID) == "" {
		return fmt.Errorf("suggestion id required")
	}
	if rec.Status == "" {
		rec.Status = SuggestionPending
	}
	switch rec.Status {
	case SuggestionPending, SuggestionApproved, SuggestionRejected, SuggestionExpired:
	default:
		return fmt.Errorf("invalid suggestion status %q", rec.Status)
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = rec.CreatedAt
	}
	if rec.ApplyStatus == "" {
		rec.ApplyStatus = SuggestionApplyUnapplied
	}
	switch rec.ApplyStatus {
	case SuggestionApplyUnapplied, SuggestionApplyApplied, SuggestionApplyError, SuggestionApplyNotApplicable:
	default:
		return fmt.Errorf("invalid suggestion apply status %q", rec.ApplyStatus)
	}
	return nil
}

func normalizeSuggestionPaging(limit, offset int) (int, int) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func matchSuggestionFilter(row OptimizationReviewSuggestion, filter SuggestionFilter) bool {
	if filter.Status != "" && row.Status != filter.Status {
		return false
	}
	if filter.Target != "" && row.Target != filter.Target {
		return false
	}
	if filter.SourceCandidateID != "" && row.SourceCandidateID != filter.SourceCandidateID {
		return false
	}
	if filter.SourceEvalDiffID != "" && row.SourceEvalDiffID != filter.SourceEvalDiffID {
		return false
	}
	return true
}

func cloneSuggestion(row OptimizationReviewSuggestion) *OptimizationReviewSuggestion {
	out := row
	if row.ApprovedAt != nil {
		approvedAt := *row.ApprovedAt
		out.ApprovedAt = &approvedAt
	}
	if row.AppliedAt != nil {
		appliedAt := *row.AppliedAt
		out.AppliedAt = &appliedAt
	}
	return &out
}

func cloneSuggestions(rows []OptimizationReviewSuggestion) []OptimizationReviewSuggestion {
	out := make([]OptimizationReviewSuggestion, 0, len(rows))
	for _, row := range rows {
		out = append(out, *cloneSuggestion(row))
	}
	return out
}
