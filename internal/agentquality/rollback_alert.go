package agentquality

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type RollbackAlertStatus string

const (
	RollbackAlertOpen RollbackAlertStatus = "open"
	RollbackAlertAck  RollbackAlertStatus = "acknowledged"
)

type RollbackAlert struct {
	ID                    string              `json:"id"`
	Status                RollbackAlertStatus `json:"status"`
	EvalDiffID            string              `json:"eval_diff_id"`
	TreatmentRunID        string              `json:"treatment_run_id"`
	Reasons               []string            `json:"reasons"`
	SuccessRateDelta      float64             `json:"success_rate_delta"`
	AverageLatencyDeltaMS float64             `json:"average_latency_delta_ms"`
	CreatedAt             time.Time           `json:"created_at"`
}

type RollbackAlertThresholds struct {
	MinSuccessRateDelta float64 `json:"min_success_rate_delta"`
	MaxLatencyDeltaMS   float64 `json:"max_latency_delta_ms"`
}

type RollbackTrigger string

const (
	RollbackTriggerManual   RollbackTrigger = "manual"
	RollbackTriggerAlertAck RollbackTrigger = "alert_ack"
)

type RollbackRequest struct {
	SuggestionID string          `json:"suggestion_id"`
	AlertID      string          `json:"alert_id,omitempty"`
	Trigger      RollbackTrigger `json:"trigger"`
	TriggeredBy  string          `json:"triggered_by"`
	CreatedAt    time.Time       `json:"created_at"`
}

type RollbackRecord struct {
	ID           string              `json:"id"`
	SuggestionID string              `json:"suggestion_id"`
	AlertID      string              `json:"alert_id,omitempty"`
	Trigger      RollbackTrigger     `json:"trigger"`
	TriggeredBy  string              `json:"triggered_by"`
	CreatedAt    time.Time           `json:"created_at"`
	Rollout      OptimizationRollout `json:"rollout"`
}

type RollbackStore interface {
	RecordRollback(ctx context.Context, rec RollbackRecord) (*RollbackRecord, error)
	ListRollbacks(ctx context.Context) ([]RollbackRecord, error)
}

type InMemoryRollbackStore struct {
	mu        sync.RWMutex
	rollbacks map[string]RollbackRecord
	alerts    map[string]RollbackAlert
}

func NewInMemoryRollbackStore() *InMemoryRollbackStore {
	return &InMemoryRollbackStore{
		rollbacks: map[string]RollbackRecord{},
		alerts:    map[string]RollbackAlert{},
	}
}

func EvaluateRollbackAlert(diff EvalDiff, thresholds RollbackAlertThresholds) (RollbackAlert, bool) {
	var reasons []string
	if diff.SuccessRateDelta < thresholds.MinSuccessRateDelta {
		reasons = append(reasons, "success_rate_regression")
	}
	if thresholds.MaxLatencyDeltaMS > 0 && diff.AverageLatencyDeltaMS > thresholds.MaxLatencyDeltaMS {
		reasons = append(reasons, "latency_regression")
	}
	if len(reasons) == 0 {
		return RollbackAlert{}, false
	}
	now := time.Now()
	return RollbackAlert{
		ID:                    "rollback_alert_" + strings.TrimPrefix(evalDiffID(diff.ID, diff.TreatmentRunID), "evaldiff_"),
		Status:                RollbackAlertOpen,
		EvalDiffID:            diff.ID,
		TreatmentRunID:        diff.TreatmentRunID,
		Reasons:               reasons,
		SuccessRateDelta:      diff.SuccessRateDelta,
		AverageLatencyDeltaMS: diff.AverageLatencyDeltaMS,
		CreatedAt:             now,
	}, true
}

func ExecuteRollback(ctx context.Context, rolloutStore OptimizationRolloutStore, rollbackStore RollbackStore, req RollbackRequest) (*RollbackRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if rolloutStore == nil {
		return nil, fmt.Errorf("rollout store is required")
	}
	if rollbackStore == nil {
		return nil, fmt.Errorf("rollback store is required")
	}
	if strings.TrimSpace(req.SuggestionID) == "" {
		return nil, fmt.Errorf("suggestion id is required")
	}
	req.TriggeredBy = strings.TrimSpace(req.TriggeredBy)
	if req.TriggeredBy == "" {
		return nil, fmt.Errorf("triggered_by is required")
	}
	switch req.Trigger {
	case RollbackTriggerManual, RollbackTriggerAlertAck:
	default:
		return nil, fmt.Errorf("invalid rollback trigger %q", req.Trigger)
	}
	if req.Trigger == RollbackTriggerAlertAck && strings.TrimSpace(req.AlertID) == "" {
		return nil, fmt.Errorf("alert id is required for alert_ack rollback")
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now()
	}

	rollout, err := rolloutStore.MarkRolledBack(ctx, req.SuggestionID, req.TriggeredBy, req.CreatedAt)
	if err != nil {
		return nil, err
	}
	return rollbackStore.RecordRollback(ctx, RollbackRecord{
		ID:           rollbackRecordID(req),
		SuggestionID: req.SuggestionID,
		AlertID:      strings.TrimSpace(req.AlertID),
		Trigger:      req.Trigger,
		TriggeredBy:  req.TriggeredBy,
		CreatedAt:    req.CreatedAt,
		Rollout:      *rollout,
	})
}

func (s *InMemoryRollbackStore) RecordRollback(ctx context.Context, rec RollbackRecord) (*RollbackRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("rollback store not configured")
	}
	if err := normalizeRollbackRecord(&rec); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rollbacks == nil {
		s.rollbacks = map[string]RollbackRecord{}
	}
	s.rollbacks[rec.ID] = rec
	out := rec
	return &out, nil
}

func (s *InMemoryRollbackStore) ListRollbacks(ctx context.Context) ([]RollbackRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("rollback store not configured")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RollbackRecord, 0, len(s.rollbacks))
	for _, row := range s.rollbacks {
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *InMemoryRollbackStore) RecordAlert(ctx context.Context, alert RollbackAlert) (*RollbackAlert, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("rollback store not configured")
	}
	if strings.TrimSpace(alert.ID) == "" {
		return nil, fmt.Errorf("alert id is required")
	}
	if strings.TrimSpace(alert.EvalDiffID) == "" {
		return nil, fmt.Errorf("eval diff id is required")
	}
	if alert.Status == "" {
		alert.Status = RollbackAlertOpen
	}
	if alert.CreatedAt.IsZero() {
		alert.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alerts == nil {
		s.alerts = map[string]RollbackAlert{}
	}
	s.alerts[alert.ID] = alert
	out := alert
	return &out, nil
}

func (s *InMemoryRollbackStore) ListAlerts(ctx context.Context) ([]RollbackAlert, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("rollback store not configured")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RollbackAlert, 0, len(s.alerts))
	for _, row := range s.alerts {
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func normalizeRollbackRecord(rec *RollbackRecord) error {
	rec.ID = strings.TrimSpace(rec.ID)
	rec.SuggestionID = strings.TrimSpace(rec.SuggestionID)
	rec.AlertID = strings.TrimSpace(rec.AlertID)
	rec.TriggeredBy = strings.TrimSpace(rec.TriggeredBy)
	if rec.ID == "" {
		return fmt.Errorf("rollback id is required")
	}
	if rec.SuggestionID == "" {
		return fmt.Errorf("suggestion id is required")
	}
	if rec.TriggeredBy == "" {
		return fmt.Errorf("triggered_by is required")
	}
	switch rec.Trigger {
	case RollbackTriggerManual, RollbackTriggerAlertAck:
	default:
		return fmt.Errorf("invalid rollback trigger %q", rec.Trigger)
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}
	return nil
}

func rollbackRecordID(req RollbackRequest) string {
	return "rollback_" + strings.NewReplacer(" ", "_", "/", "_", ":", "_").Replace(req.SuggestionID+"_"+string(req.Trigger)+"_"+req.TriggeredBy)
}
