package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/errs"
)

func (s *Server) optimizationSuggestionStore() agentquality.OptimizationSuggestionStore {
	if s.optimizationStore == nil {
		s.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
	}
	return s.optimizationStore
}

func (s *Server) optimizationRolloutStoreOrDefault() agentquality.OptimizationRolloutStore {
	if s.optimizationRolloutStore == nil {
		s.optimizationRolloutStore = agentquality.NewInMemoryOptimizationRolloutStore()
	}
	return s.optimizationRolloutStore
}

func (s *Server) optimizationApprovalStoreOrDefault() agentquality.ApprovalStore {
	if s.optimizationApprovalStore == nil {
		s.optimizationApprovalStore = agentquality.NewInMemoryApprovalStore()
	}
	return s.optimizationApprovalStore
}

func (s *Server) optimizationRollbackStoreOrDefault() agentquality.RollbackAlertStore {
	if s.optimizationRollbackStore == nil {
		s.optimizationRollbackStore = agentquality.NewInMemoryRollbackStore()
	}
	return s.optimizationRollbackStore
}

func (s *Server) optimizationEvalDiffStoreOrDefault() agentquality.EvalDiffStore {
	if s.optimizationEvalDiffStore == nil {
		s.optimizationEvalDiffStore = newInMemoryEvalDiffStore()
	}
	return s.optimizationEvalDiffStore
}

func (s *Server) handleAdminOptimizationGenerateSuggestions(w http.ResponseWriter, r *http.Request) {
	if s.qualityCandidateStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "质量候选用例存储未启用", Code: errs.CodeInternal})
		return
	}
	var body struct {
		CandidateID string `json:"candidate_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	if strings.TrimSpace(body.CandidateID) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "candidate_id 不能为空", Code: errs.CodeInvalidInput})
		return
	}
	rec, ok, err := s.qualityCandidateStore.GetCandidate(r.Context(), body.CandidateID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "候选用例不存在", Code: errs.CodeNotFound})
		return
	}
	suggestions := agentquality.NewSuggestionGenerator().GenerateFromCandidate(*rec, auth.UserIDFrom(r.Context()))
	stored := make([]agentquality.OptimizationReviewSuggestion, 0, len(suggestions))
	store := s.optimizationSuggestionStore()
	for _, sug := range suggestions {
		row, err := store.UpsertSuggestion(r.Context(), sug)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
			return
		}
		stored = append(stored, *row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": stored})
}

func (s *Server) handleAdminOptimizationGenerateEvalDiffSuggestions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EvalDiff agentquality.EvalDiff `json:"eval_diff"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	if strings.TrimSpace(body.EvalDiff.ID) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "eval_diff.id 不能为空", Code: errs.CodeInvalidInput})
		return
	}
	if _, err := s.optimizationEvalDiffStoreOrDefault().UpsertEvalDiff(r.Context(), body.EvalDiff); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	suggestions := agentquality.NewSuggestionGenerator().GenerateFromEvalDiff(body.EvalDiff, auth.UserIDFrom(r.Context()))
	stored := make([]agentquality.OptimizationReviewSuggestion, 0, len(suggestions))
	store := s.optimizationSuggestionStore()
	for _, sug := range suggestions {
		row, err := store.UpsertSuggestion(r.Context(), sug)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
			return
		}
		stored = append(stored, *row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": stored})
}

func (s *Server) handleAdminOptimizationComputeEvalDiff(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Baseline  agentquality.EvalRun `json:"baseline"`
		Treatment agentquality.EvalRun `json:"treatment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	diff, err := agentquality.ComputeEvalDiff(body.Baseline, body.Treatment)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	if _, err := s.optimizationEvalDiffStoreOrDefault().UpsertEvalDiff(r.Context(), diff); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (s *Server) handleAdminOptimizationListEvalDiffs(w http.ResponseWriter, r *http.Request) {
	page, size := parsePagination(r)
	items, total, err := s.optimizationEvalDiffStoreOrDefault().ListEvalDiffs(r.Context(), size, (page-1)*size)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "size": size})
}

func (s *Server) handleAdminOptimizationGetEvalDiff(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	diff, ok, err := s.optimizationEvalDiffStoreOrDefault().GetEvalDiff(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "eval diff 不存在", Code: errs.CodeNotFound})
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (s *Server) handleAdminOptimizationABReport(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	diff, ok, err := s.optimizationEvalDiffStoreOrDefault().GetEvalDiff(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "eval diff 不存在", Code: errs.CodeNotFound})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"eval_diff_id": id,
		"markdown":     agentquality.RenderABReportMarkdown(*diff),
	})
}

func (s *Server) handleAdminOptimizationListApprovals(w http.ResponseWriter, r *http.Request) {
	subjectID := strings.TrimSpace(r.URL.Query().Get("subject_id"))
	items, err := s.optimizationApprovalStoreOrDefault().ListApprovals(r.Context(), subjectID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleAdminOptimizationCreateApproval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SubjectID    string                           `json:"subject_id"`
		SubjectType  agentquality.ApprovalSubjectType `json:"subject_type"`
		Action       agentquality.ApprovalAction      `json:"action"`
		ReviewerRole agentquality.ApprovalRole        `json:"reviewer_role"`
		Note         string                           `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	reviewer := auth.UserIDFrom(r.Context())
	if reviewer == "" {
		reviewer = "admin"
	}
	rec, err := s.optimizationApprovalStoreOrDefault().RecordApproval(r.Context(), agentquality.ApprovalRecord{
		ID:           fmt.Sprintf("approval_%d", time.Now().UnixNano()),
		SubjectID:    body.SubjectID,
		SubjectType:  body.SubjectType,
		Action:       body.Action,
		Reviewer:     reviewer,
		ReviewerRole: body.ReviewerRole,
		Note:         body.Note,
		CreatedAt:    time.Now(),
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleAdminOptimizationEvaluateRollbackAlert(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EvalDiff   agentquality.EvalDiff                `json:"eval_diff"`
		Thresholds agentquality.RollbackAlertThresholds `json:"thresholds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	alert, ok := agentquality.EvaluateRollbackAlert(body.EvalDiff, body.Thresholds)
	if ok {
		if _, err := s.optimizationRollbackStoreOrDefault().RecordAlert(r.Context(), alert); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alert": alert, "triggered": ok})
}

func (s *Server) handleAdminOptimizationListRollbackAlerts(w http.ResponseWriter, r *http.Request) {
	items, err := s.optimizationRollbackStoreOrDefault().ListAlerts(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleAdminOptimizationListRollbacks(w http.ResponseWriter, r *http.Request) {
	items, err := s.optimizationRollbackStoreOrDefault().ListRollbacks(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleAdminOptimizationListSuggestions(w http.ResponseWriter, r *http.Request) {
	page, size := parsePagination(r)
	filter := agentquality.SuggestionFilter{
		Status:            agentquality.SuggestionStatus(r.URL.Query().Get("status")),
		Target:            agentquality.SuggestionTarget(r.URL.Query().Get("target")),
		SourceCandidateID: r.URL.Query().Get("source_candidate_id"),
		SourceEvalDiffID:  r.URL.Query().Get("source_eval_diff_id"),
		Limit:             size,
		Offset:            (page - 1) * size,
	}
	if filter.Status != "" && !validSuggestionStatus(filter.Status) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid suggestion status", Code: errs.CodeInvalidInput})
		return
	}
	items, total, err := s.optimizationSuggestionStore().ListSuggestions(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"suggestions": items,
		"items":       items,
		"total":       total,
		"page":        page,
		"size":        size,
	})
}

func (s *Server) handleAdminOptimizationApproveSuggestion(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "suggestion id 不能为空", Code: errs.CodeInvalidInput})
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	approved, err := s.optimizationSuggestionStore().ApproveSuggestion(r.Context(), id, auth.UserIDFrom(r.Context()), body.Note, time.Now())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "优化建议不存在", Code: errs.CodeNotFound})
			return
		}
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	writeJSON(w, http.StatusOK, approved)
}

func (s *Server) handleAdminOptimizationRejectSuggestion(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "suggestion id 不能为空", Code: errs.CodeInvalidInput})
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	rejected, err := s.optimizationSuggestionStore().RejectSuggestion(r.Context(), id, auth.UserIDFrom(r.Context()), body.Note, time.Now())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "优化建议不存在", Code: errs.CodeNotFound})
			return
		}
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	writeJSON(w, http.StatusOK, rejected)
}

func (s *Server) handleAdminOptimizationApplySuggestion(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "suggestion id 不能为空", Code: errs.CodeInvalidInput})
		return
	}
	store := s.optimizationSuggestionStore()
	suggestion, ok, err := store.GetSuggestion(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "优化建议不存在", Code: errs.CodeNotFound})
		return
	}

	appliedBy := auth.UserIDFrom(r.Context())
	if appliedBy == "" {
		appliedBy = "optimization"
	}
	now := time.Now()
	if suggestion.Status != agentquality.SuggestionApproved {
		message := "only approved suggestions can be applied"
		updated, updateErr := store.MarkSuggestionApplyError(r.Context(), id, appliedBy, message, now)
		if updateErr != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: updateErr.Error(), Code: errs.CodeInternal})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": message, "code": errs.CodeInvalidInput, "suggestion": updated})
		return
	}
	if suggestion.IsExpired(now) {
		message := "approved suggestion expired"
		updated, updateErr := store.MarkSuggestionApplyError(r.Context(), id, appliedBy, message, now)
		if updateErr != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: updateErr.Error(), Code: errs.CodeInternal})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": message, "code": errs.CodeInvalidInput, "suggestion": updated})
		return
	}

	rollout, err := s.applyOptimizationSuggestion(r, *suggestion, appliedBy)
	if err != nil {
		status := http.StatusBadRequest
		mark := store.MarkSuggestionApplyError
		if errors.Is(err, errSuggestionApplyNotApplicable) {
			mark = store.MarkSuggestionNotApplicable
		}
		updated, updateErr := mark(r.Context(), id, appliedBy, err.Error(), now)
		if updateErr != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: updateErr.Error(), Code: errs.CodeInternal})
			return
		}
		writeJSON(w, status, map[string]any{"error": err.Error(), "code": errs.CodeInvalidInput, "suggestion": updated})
		return
	}

	if _, err := s.optimizationRolloutStoreOrDefault().RecordApplied(r.Context(), rollout); err != nil {
		updated, updateErr := store.MarkSuggestionApplyError(r.Context(), id, appliedBy, err.Error(), now)
		if updateErr != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: updateErr.Error(), Code: errs.CodeInternal})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "code": errs.CodeInternal, "suggestion": updated})
		return
	}

	applied, err := store.MarkSuggestionApplied(r.Context(), id, appliedBy, now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, applied)
}

var errSuggestionApplyNotApplicable = errors.New("suggestion target not applicable")

func (s *Server) applyOptimizationSuggestion(r *http.Request, suggestion agentquality.OptimizationReviewSuggestion, appliedBy string) (agentquality.OptimizationRollout, error) {
	switch suggestion.Target {
	case agentquality.TargetPrompt:
		return s.applyPromptSuggestion(r, suggestion, appliedBy)
	case agentquality.TargetSkillContent:
		return s.applySkillSuggestion(r, suggestion, appliedBy)
	case agentquality.TargetToolDescription:
		return s.applyToolDescriptionSuggestion(r, suggestion, appliedBy)
	case agentquality.TargetMemoryGovernance:
		return s.applyMemoryGovernanceSuggestion(r, suggestion, appliedBy)
	default:
		return agentquality.OptimizationRollout{}, fmt.Errorf("%w: unsupported target %q", errSuggestionApplyNotApplicable, suggestion.Target)
	}
}

func (s *Server) applyPromptSuggestion(r *http.Request, suggestion agentquality.OptimizationReviewSuggestion, appliedBy string) (agentquality.OptimizationRollout, error) {
	if s.promptStore == nil {
		return agentquality.OptimizationRollout{}, errors.New("prompt store not available")
	}
	key := strings.TrimSpace(suggestion.SourceEvent.Prompt.Key)
	if key == "" {
		return agentquality.OptimizationRollout{}, errors.New("prompt key is required to apply prompt suggestion")
	}
	if strings.Contains(key, "@") {
		return agentquality.OptimizationRollout{}, errors.New("prompt key must be an exact key without version suffix")
	}
	content := strings.TrimSpace(suggestion.ProposedValue)
	if content == "" {
		return agentquality.OptimizationRollout{}, errors.New("proposed prompt content is required")
	}
	language := strings.TrimSpace(suggestion.SourceEvent.Prompt.Language)
	previous, previousExists, err := s.promptStore.Get(r.Context(), key, language)
	if err != nil {
		return agentquality.OptimizationRollout{}, err
	}
	if err := s.promptStore.Upsert(r.Context(), key, language, content, appliedBy); err != nil {
		return agentquality.OptimizationRollout{}, err
	}
	if s.promptLoader != nil {
		s.promptLoader.InvalidateDBCache(key)
	}
	return optimizationRolloutFromSuggestion(suggestion, key+"|"+language, previous, previousExists, content, appliedBy), nil
}

func (s *Server) applySkillSuggestion(r *http.Request, suggestion agentquality.OptimizationReviewSuggestion, appliedBy string) (agentquality.OptimizationRollout, error) {
	if s.skillStore == nil {
		return agentquality.OptimizationRollout{}, errors.New("skill store not available")
	}
	name := strings.TrimSpace(suggestion.CurrentValue)
	if name == "" {
		return agentquality.OptimizationRollout{}, errors.New("skill name is required to apply skill suggestion")
	}
	content := strings.TrimSpace(suggestion.ProposedValue)
	if content == "" {
		return agentquality.OptimizationRollout{}, errors.New("proposed skill content is required")
	}
	current, previousExists, err := s.skillStore.Get(r.Context(), name, "")
	if err != nil {
		return agentquality.OptimizationRollout{}, err
	}
	previous := ""
	if current != nil {
		previous = current.Content
	}
	if err := s.skillStore.Upsert(r.Context(), name, "", content, "user", "", appliedBy, 0); err != nil {
		return agentquality.OptimizationRollout{}, err
	}
	return optimizationRolloutFromSuggestion(suggestion, name, previous, previousExists, content, appliedBy), nil
}

func (s *Server) applyToolDescriptionSuggestion(r *http.Request, suggestion agentquality.OptimizationReviewSuggestion, appliedBy string) (agentquality.OptimizationRollout, error) {
	if s.toolDescriptionStore == nil {
		return agentquality.OptimizationRollout{}, errors.New("tool description store not available")
	}
	name := strings.TrimSpace(suggestion.CurrentValue)
	if name == "" {
		name = strings.TrimSpace(suggestion.SourceEvent.ToolDecision.Actual)
	}
	if name == "" {
		return agentquality.OptimizationRollout{}, errors.New("tool name is required to apply tool description suggestion")
	}
	description := strings.TrimSpace(suggestion.ProposedValue)
	if description == "" {
		return agentquality.OptimizationRollout{}, errors.New("proposed tool description is required")
	}
	previous, previousExists, err := s.toolDescriptionStore.GetToolDescription(r.Context(), name)
	if err != nil {
		return agentquality.OptimizationRollout{}, err
	}
	if err := s.toolDescriptionStore.UpsertToolDescription(r.Context(), name, description, appliedBy); err != nil {
		return agentquality.OptimizationRollout{}, err
	}
	return optimizationRolloutFromSuggestion(suggestion, name, previous, previousExists, description, appliedBy), nil
}

func (s *Server) applyMemoryGovernanceSuggestion(r *http.Request, suggestion agentquality.OptimizationReviewSuggestion, appliedBy string) (agentquality.OptimizationRollout, error) {
	if s.memoryGovernancePolicyStore == nil {
		return agentquality.OptimizationRollout{}, errors.New("memory governance policy store not available")
	}
	name := strings.TrimSpace(suggestion.CurrentValue)
	if name == "" {
		name = "default"
	}
	policy := strings.TrimSpace(suggestion.ProposedValue)
	if policy == "" {
		return agentquality.OptimizationRollout{}, errors.New("proposed memory governance policy is required")
	}
	previous, previousExists, err := s.memoryGovernancePolicyStore.GetMemoryGovernancePolicy(r.Context(), name)
	if err != nil {
		return agentquality.OptimizationRollout{}, err
	}
	if err := s.memoryGovernancePolicyStore.UpsertMemoryGovernancePolicy(r.Context(), name, policy, appliedBy); err != nil {
		return agentquality.OptimizationRollout{}, err
	}
	return optimizationRolloutFromSuggestion(suggestion, name, previous, previousExists, policy, appliedBy), nil
}

func optimizationRolloutFromSuggestion(suggestion agentquality.OptimizationReviewSuggestion, targetKey, previous string, previousExists bool, appliedValue, appliedBy string) agentquality.OptimizationRollout {
	now := time.Now()
	return agentquality.OptimizationRollout{
		ID:             "rollout_" + strings.TrimSpace(suggestion.ID),
		SuggestionID:   strings.TrimSpace(suggestion.ID),
		Target:         suggestion.Target,
		TargetKey:      strings.TrimSpace(targetKey),
		PreviousValue:  previous,
		PreviousExists: previousExists,
		AppliedValue:   appliedValue,
		Status:         agentquality.RolloutApplied,
		AppliedBy:      strings.TrimSpace(appliedBy),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func (s *Server) handleAdminOptimizationRollbackSuggestion(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "suggestion id 不能为空", Code: errs.CodeInvalidInput})
		return
	}
	rollout, ok, err := s.optimizationRolloutStoreOrDefault().GetBySuggestion(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "应用记录不存在，无法回滚", Code: errs.CodeNotFound})
		return
	}
	if rollout.Status == agentquality.RolloutRolledBack {
		writeJSON(w, http.StatusOK, rollout)
		return
	}

	rolledBackBy := auth.UserIDFrom(r.Context())
	if rolledBackBy == "" {
		rolledBackBy = "optimization"
	}
	if err := s.rollbackOptimizationRollout(r, *rollout, rolledBackBy); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	rolledBack, err := s.optimizationRolloutStoreOrDefault().MarkRolledBack(r.Context(), id, rolledBackBy, time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, rolledBack)
}

func (s *Server) rollbackOptimizationRollout(r *http.Request, rollout agentquality.OptimizationRollout, rolledBackBy string) error {
	switch rollout.Target {
	case agentquality.TargetPrompt:
		return s.rollbackPromptRollout(r, rollout, rolledBackBy)
	case agentquality.TargetSkillContent:
		return s.rollbackSkillRollout(r, rollout, rolledBackBy)
	case agentquality.TargetToolDescription:
		return s.rollbackToolDescriptionRollout(r, rollout, rolledBackBy)
	case agentquality.TargetMemoryGovernance:
		return s.rollbackMemoryGovernanceRollout(r, rollout, rolledBackBy)
	default:
		return fmt.Errorf("unsupported rollout target %q", rollout.Target)
	}
}

func (s *Server) rollbackPromptRollout(r *http.Request, rollout agentquality.OptimizationRollout, rolledBackBy string) error {
	if s.promptStore == nil {
		return errors.New("prompt store not available")
	}
	key, language := splitRolloutTargetKey(rollout.TargetKey)
	if key == "" {
		return errors.New("prompt key is required to rollback prompt suggestion")
	}
	if rollout.PreviousExists {
		if err := s.promptStore.Upsert(r.Context(), key, language, rollout.PreviousValue, rolledBackBy); err != nil {
			return err
		}
	} else if err := s.promptStore.Delete(r.Context(), key, language); err != nil {
		return err
	}
	if s.promptLoader != nil {
		s.promptLoader.InvalidateDBCache(key)
	}
	return nil
}

func (s *Server) rollbackSkillRollout(r *http.Request, rollout agentquality.OptimizationRollout, rolledBackBy string) error {
	if s.skillStore == nil {
		return errors.New("skill store not available")
	}
	name := strings.TrimSpace(rollout.TargetKey)
	if name == "" {
		return errors.New("skill name is required to rollback skill suggestion")
	}
	if rollout.PreviousExists {
		return s.skillStore.Upsert(r.Context(), name, "", rollout.PreviousValue, "user", "", rolledBackBy, 0)
	}
	return s.skillStore.Delete(r.Context(), name, "")
}

func (s *Server) rollbackToolDescriptionRollout(r *http.Request, rollout agentquality.OptimizationRollout, rolledBackBy string) error {
	if s.toolDescriptionStore == nil {
		return errors.New("tool description store not available")
	}
	name := strings.TrimSpace(rollout.TargetKey)
	if name == "" {
		return errors.New("tool name is required to rollback tool description suggestion")
	}
	if rollout.PreviousExists {
		return s.toolDescriptionStore.UpsertToolDescription(r.Context(), name, rollout.PreviousValue, rolledBackBy)
	}
	return s.toolDescriptionStore.DeleteToolDescription(r.Context(), name)
}

func (s *Server) rollbackMemoryGovernanceRollout(r *http.Request, rollout agentquality.OptimizationRollout, rolledBackBy string) error {
	if s.memoryGovernancePolicyStore == nil {
		return errors.New("memory governance policy store not available")
	}
	name := strings.TrimSpace(rollout.TargetKey)
	if name == "" {
		name = "default"
	}
	if rollout.PreviousExists {
		return s.memoryGovernancePolicyStore.UpsertMemoryGovernancePolicy(r.Context(), name, rollout.PreviousValue, rolledBackBy)
	}
	return s.memoryGovernancePolicyStore.DeleteMemoryGovernancePolicy(r.Context(), name)
}

func splitRolloutTargetKey(targetKey string) (string, string) {
	key, language, ok := strings.Cut(targetKey, "|")
	if !ok {
		return strings.TrimSpace(targetKey), ""
	}
	return strings.TrimSpace(key), strings.TrimSpace(language)
}

func validSuggestionStatus(status agentquality.SuggestionStatus) bool {
	switch status {
	case agentquality.SuggestionPending, agentquality.SuggestionApproved, agentquality.SuggestionRejected, agentquality.SuggestionExpired:
		return true
	default:
		return false
	}
}

type inMemoryEvalDiffStore struct {
	mu   sync.RWMutex
	rows map[string]agentquality.EvalDiff
}

func newInMemoryEvalDiffStore() *inMemoryEvalDiffStore {
	return &inMemoryEvalDiffStore{rows: map[string]agentquality.EvalDiff{}}
}

func (s *inMemoryEvalDiffStore) UpsertEvalDiff(ctx context.Context, diff agentquality.EvalDiff) (*agentquality.EvalDiff, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || strings.TrimSpace(diff.ID) == "" {
		return nil, fmt.Errorf("eval diff id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = map[string]agentquality.EvalDiff{}
	}
	s.rows[diff.ID] = diff
	out := diff
	return &out, nil
}

func (s *inMemoryEvalDiffStore) GetEvalDiff(ctx context.Context, id string) (*agentquality.EvalDiff, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if s == nil {
		return nil, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	diff, ok := s.rows[id]
	if !ok {
		return nil, false, nil
	}
	out := diff
	return &out, true, nil
}

func (s *inMemoryEvalDiffStore) ListEvalDiffs(ctx context.Context, limit, offset int) ([]agentquality.EvalDiff, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if s == nil {
		return nil, 0, nil
	}
	limit, offset = normalizeEvalDiffPaging(limit, offset)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]agentquality.EvalDiff, 0, len(s.rows))
	for _, row := range s.rows {
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	total := len(out)
	if offset >= total {
		return []agentquality.EvalDiff{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return out[offset:end], total, nil
}

func normalizeEvalDiffPaging(limit, offset int) (int, int) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
