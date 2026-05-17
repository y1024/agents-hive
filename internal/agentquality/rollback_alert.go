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
	DomainID              string              `json:"domain_id"`
	Severity              string              `json:"severity"` // "low", "medium", "high", "critical"
	EvalDiffID            string              `json:"eval_diff_id,omitempty"`
	TreatmentRunID        string              `json:"treatment_run_id,omitempty"`
	Reasons               []string            `json:"reasons"`
	Evidence              []RollbackEvidence  `json:"evidence"`
	SuccessRateDelta      float64             `json:"success_rate_delta"`
	AverageLatencyDeltaMS float64             `json:"average_latency_delta_ms"`
	CreatedAt             time.Time           `json:"created_at"`
}

// RollbackEvidence 是回滚告警的证据。
type RollbackEvidence struct {
	CaseID          string `json:"case_id"`
	TraceRef        string `json:"trace_ref,omitempty"`
	ReplayRef       string `json:"replay_ref,omitempty"`
	JudgeVerdictRef string `json:"judge_verdict_ref,omitempty"`
	RunnerEvidence  string `json:"runner_evidence"`
	SemanticScore   int    `json:"semantic_score,omitempty"`
	FailureType     string `json:"failure_type,omitempty"`
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

	severity := "medium"
	if diff.SuccessRateDelta < -0.20 {
		severity = "critical"
	} else if diff.SuccessRateDelta < -0.10 {
		severity = "high"
	}

	return RollbackAlert{
		ID:                    "rollback_alert_" + strings.TrimPrefix(evalDiffID(diff.ID, diff.TreatmentRunID), "evaldiff_"),
		Status:                RollbackAlertOpen,
		Severity:              severity,
		EvalDiffID:            diff.ID,
		TreatmentRunID:        diff.TreatmentRunID,
		Reasons:               reasons,
		Evidence:              []RollbackEvidence{}, // 由 EvalDiff 构建时填充
		SuccessRateDelta:      diff.SuccessRateDelta,
		AverageLatencyDeltaMS: diff.AverageLatencyDeltaMS,
		CreatedAt:             now,
	}, true
}

// DetectSemanticRegression 检测语义回归并生成回滚告警。
// baseline 是基线语义分数（0-10），通常为 7.0。
func DetectSemanticRegression(recentResults []ShadowEvalResult, baseline float64) *RollbackAlert {
	if len(recentResults) == 0 {
		return nil
	}

	// 计算平均语义分数
	var totalScore float64
	var domainID string
	evidence := []RollbackEvidence{}

	for _, result := range recentResults {
		totalScore += float64(result.JudgeVerdict.Score)
		if domainID == "" {
			domainID = result.DomainID
		}

		// 收集低分证据
		if result.JudgeVerdict.Score < int(baseline) {
			evidence = append(evidence, RollbackEvidence{
				CaseID:          result.CaseID,
				TraceRef:        result.TraceRef,
				ReplayRef:       result.ReplayRef,
				JudgeVerdictRef: fmt.Sprintf("verdict_score_%d", result.JudgeVerdict.Score),
				RunnerEvidence:  string(result.RunnerInfo.EvidenceLevel),
				SemanticScore:   result.JudgeVerdict.Score,
				FailureType:     string(result.JudgeVerdict.FailureType),
			})
		}
	}

	avgScore := totalScore / float64(len(recentResults))

	// 如果平均分低于基线，触发告警
	if avgScore < baseline {
		delta := avgScore - baseline
		severity := "medium"
		if delta < -2.0 {
			severity = "critical"
		} else if delta < -1.0 {
			severity = "high"
		}

		return &RollbackAlert{
			ID:               fmt.Sprintf("rollback_alert_semantic_%s_%d", domainID, time.Now().Unix()),
			Status:           RollbackAlertOpen,
			DomainID:         domainID,
			Severity:         severity,
			Reasons:          []string{"semantic_regression"},
			Evidence:         evidence,
			SuccessRateDelta: delta / 10.0, // 归一化到 -1.0 到 1.0
			CreatedAt:        time.Now(),
		}
	}

	return nil
}

// DetectSafetyFailureSpike 检测安全失败激增。
// threshold 是触发告警的失败率阈值（0.0-1.0）。
func DetectSafetyFailureSpike(recentResults []ShadowEvalResult, threshold float64) *RollbackAlert {
	if len(recentResults) == 0 {
		return nil
	}

	safetyFailures := 0
	var domainID string
	evidence := []RollbackEvidence{}

	for _, result := range recentResults {
		if domainID == "" {
			domainID = result.DomainID
		}

		// 检查是否为安全失败
		if result.JudgeVerdict.FailureType == FailurePermission ||
			result.JudgeVerdict.FailureType == FailureRuntime ||
			strings.Contains(result.JudgeVerdict.Verdict, "安全") ||
			strings.Contains(result.JudgeVerdict.Verdict, "权限") {
			safetyFailures++

			evidence = append(evidence, RollbackEvidence{
				CaseID:          result.CaseID,
				TraceRef:        result.TraceRef,
				ReplayRef:       result.ReplayRef,
				JudgeVerdictRef: fmt.Sprintf("safety_failure_%s", result.JudgeVerdict.FailureType),
				RunnerEvidence:  string(result.RunnerInfo.EvidenceLevel),
				FailureType:     string(result.JudgeVerdict.FailureType),
			})
		}
	}

	failureRate := float64(safetyFailures) / float64(len(recentResults))

	if failureRate > threshold {
		severity := "high"
		if failureRate > 0.5 {
			severity = "critical"
		}

		return &RollbackAlert{
			ID:               fmt.Sprintf("rollback_alert_safety_%s_%d", domainID, time.Now().Unix()),
			Status:           RollbackAlertOpen,
			DomainID:         domainID,
			Severity:         severity,
			Reasons:          []string{"safety_failure_spike"},
			Evidence:         evidence,
			SuccessRateDelta: -failureRate,
			CreatedAt:        time.Now(),
		}
	}

	return nil
}

// DetectToolMisuseIncrease 检测工具误用增加。
// threshold 是触发告警的误用率阈值（0.0-1.0）。
func DetectToolMisuseIncrease(recentResults []ShadowEvalResult, threshold float64) *RollbackAlert {
	if len(recentResults) == 0 {
		return nil
	}

	toolMisuses := 0
	var domainID string
	evidence := []RollbackEvidence{}

	for _, result := range recentResults {
		if domainID == "" {
			domainID = result.DomainID
		}

		// 检查是否为工具误用
		if result.JudgeVerdict.FailureType == FailureTool ||
			strings.Contains(result.JudgeVerdict.Verdict, "工具") ||
			strings.Contains(result.JudgeVerdict.Verdict, "tool") {
			toolMisuses++

			evidence = append(evidence, RollbackEvidence{
				CaseID:          result.CaseID,
				TraceRef:        result.TraceRef,
				ReplayRef:       result.ReplayRef,
				JudgeVerdictRef: fmt.Sprintf("tool_misuse_%s", result.JudgeVerdict.FailureType),
				RunnerEvidence:  string(result.RunnerInfo.EvidenceLevel),
				FailureType:     string(result.JudgeVerdict.FailureType),
			})
		}
	}

	misuseRate := float64(toolMisuses) / float64(len(recentResults))

	if misuseRate > threshold {
		severity := "medium"
		if misuseRate > 0.3 {
			severity = "high"
		}

		return &RollbackAlert{
			ID:               fmt.Sprintf("rollback_alert_tool_misuse_%s_%d", domainID, time.Now().Unix()),
			Status:           RollbackAlertOpen,
			DomainID:         domainID,
			Severity:         severity,
			Reasons:          []string{"tool_misuse_increase"},
			Evidence:         evidence,
			SuccessRateDelta: -misuseRate,
			CreatedAt:        time.Now(),
		}
	}

	return nil
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
