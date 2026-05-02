package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/chef-guo/agents-hive/internal/store"
)

func TestAdminOptimizationGenerateSuggestionsFromCandidate(t *testing.T) {
	store := newFakeQualityCandidateStore()
	rec := agentquality.CandidateFromFailure("session-1", "定位权限", "session-1:step-1", agentquality.Event{
		Route:       "web",
		FailureType: agentquality.FailureTool,
		FinalStatus: agentquality.StatusFail,
		Prompt:      agentquality.PromptRef{Key: "system/base"},
		ToolDecision: agentquality.ToolDecision{
			Expected: []string{"grep"},
			Actual:   "read_file",
		},
	})
	store.records[rec.ID] = rec
	srv := newQualityCandidateTestServer(store)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions", strings.NewReader(`{"candidate_id":"`+rec.ID+`"}`))
	out := httptest.NewRecorder()
	srv.handleAdminOptimizationGenerateSuggestions(out, req)

	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	var got struct {
		Suggestions []agentquality.OptimizationReviewSuggestion `json:"suggestions"`
	}
	require.NoError(t, json.NewDecoder(out.Body).Decode(&got))
	require.Len(t, got.Suggestions, 2)
	require.Equal(t, agentquality.SuggestionPending, got.Suggestions[0].Status)
	require.Equal(t, rec.ID, got.Suggestions[0].SourceCandidateID)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/optimization/suggestions?status=pending", nil)
	listOut := httptest.NewRecorder()
	srv.handleAdminOptimizationListSuggestions(listOut, listReq)
	require.Equal(t, http.StatusOK, listOut.Code, listOut.Body.String())
	var listed struct {
		Suggestions []agentquality.OptimizationReviewSuggestion `json:"suggestions"`
		Total       int                                         `json:"total"`
	}
	require.NoError(t, json.NewDecoder(listOut.Body).Decode(&listed))
	require.Equal(t, 2, listed.Total)
	require.Len(t, listed.Suggestions, 2)
}

func TestAdminOptimizationEvalDiffApprovalAndRollbackAlertEndpoints(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
	srv.optimizationApprovalStore = agentquality.NewInMemoryApprovalStore()
	srv.optimizationRollbackStore = agentquality.NewInMemoryRollbackStore()
	srv.optimizationEvalDiffStore = newInMemoryEvalDiffStore()

	diffReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/eval-diffs", strings.NewReader(`{
		"baseline":{"id":"base","results":[{"case_id":"case-1","passed":true,"latency_ms":100,"failure_type":"prompt","prompt":{"key":"system/base"}}]},
		"treatment":{"id":"treat","results":[{"case_id":"case-1","passed":false,"latency_ms":150,"failure_type":"prompt","prompt":{"key":"system/base"},"reason":"regressed"}]}
	}`))
	diffOut := httptest.NewRecorder()
	srv.handleAdminOptimizationComputeEvalDiff(diffOut, diffReq)
	require.Equal(t, http.StatusOK, diffOut.Code, diffOut.Body.String())
	var diff agentquality.EvalDiff
	require.NoError(t, json.NewDecoder(diffOut.Body).Decode(&diff))
	require.Equal(t, "evaldiff_base_treat", diff.ID)
	require.Equal(t, -1.0, diff.SuccessRateDelta)
	diffJSON, err := json.Marshal(diff)
	require.NoError(t, err)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/optimization/eval-diffs", nil)
	listOut := httptest.NewRecorder()
	srv.handleAdminOptimizationListEvalDiffs(listOut, listReq)
	require.Equal(t, http.StatusOK, listOut.Code, listOut.Body.String())
	var listedDiffs struct {
		Items []agentquality.EvalDiff `json:"items"`
		Total int                     `json:"total"`
	}
	require.NoError(t, json.NewDecoder(listOut.Body).Decode(&listedDiffs))
	require.Equal(t, 1, listedDiffs.Total)

	reportReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/eval-diffs/"+diff.ID+"/report", nil)
	reportReq.SetPathValue("id", diff.ID)
	reportOut := httptest.NewRecorder()
	srv.handleAdminOptimizationABReport(reportOut, reportReq)
	require.Equal(t, http.StatusOK, reportOut.Code, reportOut.Body.String())
	require.Contains(t, reportOut.Body.String(), "Offline Eval Diff Report")

	suggestionReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/eval-diffs/suggestions", strings.NewReader(`{"eval_diff":`+string(diffJSON)+`}`))
	suggestionOut := httptest.NewRecorder()
	srv.handleAdminOptimizationGenerateEvalDiffSuggestions(suggestionOut, suggestionReq)
	require.Equal(t, http.StatusOK, suggestionOut.Code, suggestionOut.Body.String())
	var suggestions struct {
		Suggestions []agentquality.OptimizationReviewSuggestion `json:"suggestions"`
	}
	require.NoError(t, json.NewDecoder(suggestionOut.Body).Decode(&suggestions))
	require.NotEmpty(t, suggestions.Suggestions)
	require.Equal(t, diff.ID, suggestions.Suggestions[0].SourceEvalDiffID)

	approvalReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/approvals", strings.NewReader(`{"subject_id":"`+diff.ID+`","subject_type":"eval_diff","action":"approve","reviewer_role":"lead","note":"ok"}`))
	approvalOut := httptest.NewRecorder()
	srv.handleAdminOptimizationCreateApproval(approvalOut, approvalReq)
	require.Equal(t, http.StatusCreated, approvalOut.Code, approvalOut.Body.String())

	approvalsReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/optimization/approvals?subject_id="+diff.ID, nil)
	approvalsOut := httptest.NewRecorder()
	srv.handleAdminOptimizationListApprovals(approvalsOut, approvalsReq)
	require.Equal(t, http.StatusOK, approvalsOut.Code, approvalsOut.Body.String())
	var approvals struct {
		Items []agentquality.ApprovalRecord `json:"items"`
		Total int                           `json:"total"`
	}
	require.NoError(t, json.NewDecoder(approvalsOut.Body).Decode(&approvals))
	require.Equal(t, 1, approvals.Total)

	alertReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/rollback-alerts/evaluate", strings.NewReader(`{"eval_diff":`+string(diffJSON)+`,"thresholds":{"min_success_rate_delta":-0.2,"max_latency_delta_ms":20}}`))
	alertOut := httptest.NewRecorder()
	srv.handleAdminOptimizationEvaluateRollbackAlert(alertOut, alertReq)
	require.Equal(t, http.StatusOK, alertOut.Code, alertOut.Body.String())
	require.Contains(t, alertOut.Body.String(), `"triggered":true`)

	alertsReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/optimization/rollback-alerts", nil)
	alertsOut := httptest.NewRecorder()
	srv.handleAdminOptimizationListRollbackAlerts(alertsOut, alertsReq)
	require.Equal(t, http.StatusOK, alertsOut.Code, alertsOut.Body.String())
	var alerts struct {
		Items []agentquality.RollbackAlert `json:"items"`
		Total int                          `json:"total"`
	}
	require.NoError(t, json.NewDecoder(alertsOut.Body).Decode(&alerts))
	require.Equal(t, 1, alerts.Total)
}

func TestAdminOptimizationApproveSuggestion(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	suggestion := agentquality.OptimizationReviewSuggestion{
		ID:        "sug-1",
		Status:    agentquality.SuggestionPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
	_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), suggestion)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-1/approve", strings.NewReader(`{"note":"人工确认"}`))
	req.SetPathValue("id", "sug-1")
	out := httptest.NewRecorder()
	srv.handleAdminOptimizationApproveSuggestion(out, req)

	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	var got agentquality.OptimizationReviewSuggestion
	require.NoError(t, json.NewDecoder(out.Body).Decode(&got))
	require.Equal(t, agentquality.SuggestionApproved, got.Status)
	require.Equal(t, "人工确认", got.ApprovalNote)
	require.Equal(t, agentquality.SuggestionApplyUnapplied, got.ApplyStatus)
}

func TestAdminOptimizationRejectSuggestion(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
	_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
		ID:        "sug-2",
		Status:    agentquality.SuggestionPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-2/reject", strings.NewReader(`{"note":"不适用"}`))
	req.SetPathValue("id", "sug-2")
	out := httptest.NewRecorder()
	srv.handleAdminOptimizationRejectSuggestion(out, req)

	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	var got agentquality.OptimizationReviewSuggestion
	require.NoError(t, json.NewDecoder(out.Body).Decode(&got))
	require.Equal(t, agentquality.SuggestionRejected, got.Status)
	require.Equal(t, "不适用", got.ApprovalNote)
}

func TestAdminOptimizationApplySuggestion_RejectsNonApprovedSuggestions(t *testing.T) {
	statuses := []agentquality.SuggestionStatus{
		agentquality.SuggestionPending,
		agentquality.SuggestionRejected,
		agentquality.SuggestionExpired,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
			srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
			now := time.Now()
			_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
				ID:            "sug-" + string(status),
				Status:        status,
				Target:        agentquality.TargetPrompt,
				ProposedValue: "new prompt",
				SourceEvent:   agentquality.Event{Prompt: agentquality.PromptRef{Key: "system/base"}},
				CreatedAt:     now,
				UpdatedAt:     now,
				ExpiresAt:     now.Add(time.Hour),
			})
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug/apply", nil)
			req.SetPathValue("id", "sug-"+string(status))
			out := httptest.NewRecorder()
			srv.handleAdminOptimizationApplySuggestion(out, req)

			require.Equal(t, http.StatusBadRequest, out.Code, out.Body.String())
			got, ok, err := srv.optimizationStore.GetSuggestion(context.Background(), "sug-"+string(status))
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, agentquality.SuggestionApplyError, got.ApplyStatus)
			require.Contains(t, got.ApplyError, "approved")
		})
	}
}

func TestAdminOptimizationApplySuggestion_RejectsApprovedExpiredSuggestion(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
	now := time.Now()
	_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
		ID:            "sug-approved-expired",
		Status:        agentquality.SuggestionApproved,
		Target:        agentquality.TargetPrompt,
		ProposedValue: "new prompt",
		SourceEvent:   agentquality.Event{Prompt: agentquality.PromptRef{Key: "system/base"}},
		CreatedAt:     now.Add(-2 * time.Hour),
		UpdatedAt:     now.Add(-2 * time.Hour),
		ExpiresAt:     now.Add(-time.Hour),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-approved-expired/apply", nil)
	req.SetPathValue("id", "sug-approved-expired")
	out := httptest.NewRecorder()
	srv.handleAdminOptimizationApplySuggestion(out, req)

	require.Equal(t, http.StatusBadRequest, out.Code, out.Body.String())
	got, ok, err := srv.optimizationStore.GetSuggestion(context.Background(), "sug-approved-expired")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, agentquality.SuggestionApplyError, got.ApplyStatus)
	require.Contains(t, got.ApplyError, "expired")
}

func TestAdminOptimizationApplySuggestion_ApprovedPromptUpsertsAndInvalidatesCache(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
	prompts := &fakePromptStore{}
	loader := &fakePromptLoader{}
	srv.promptStore = prompts
	srv.promptLoader = loader
	now := time.Now()
	_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
		ID:            "sug-prompt",
		Status:        agentquality.SuggestionApproved,
		Target:        agentquality.TargetPrompt,
		ProposedValue: "new prompt content",
		SourceEvent:   agentquality.Event{Prompt: agentquality.PromptRef{Key: "system/base", Language: "zh-CN"}},
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-prompt/apply", nil)
	req.SetPathValue("id", "sug-prompt")
	out := httptest.NewRecorder()
	srv.handleAdminOptimizationApplySuggestion(out, req)

	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Len(t, prompts.upserts, 1)
	require.Equal(t, promptUpsert{key: "system/base", language: "zh-CN", content: "new prompt content", updatedBy: "optimization"}, prompts.upserts[0])
	require.Equal(t, []string{"system/base"}, loader.invalidated)
	var got agentquality.OptimizationReviewSuggestion
	require.NoError(t, json.NewDecoder(out.Body).Decode(&got))
	require.Equal(t, agentquality.SuggestionApplyApplied, got.ApplyStatus)
	require.NotNil(t, got.AppliedAt)
}

func TestAdminOptimizationRollbackSuggestion_RestoresPromptPreviousValue(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
	srv.optimizationRolloutStore = agentquality.NewInMemoryOptimizationRolloutStore()
	prompts := &fakePromptStore{records: map[string]string{"system/base|zh-CN": "old prompt content"}}
	srv.promptStore = prompts
	now := time.Now()
	_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
		ID:            "sug-prompt-rollback",
		Status:        agentquality.SuggestionApproved,
		Target:        agentquality.TargetPrompt,
		ProposedValue: "new prompt content",
		SourceEvent:   agentquality.Event{Prompt: agentquality.PromptRef{Key: "system/base", Language: "zh-CN"}},
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	})
	require.NoError(t, err)

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-prompt-rollback/apply", nil)
	applyReq.SetPathValue("id", "sug-prompt-rollback")
	applyOut := httptest.NewRecorder()
	srv.handleAdminOptimizationApplySuggestion(applyOut, applyReq)
	require.Equal(t, http.StatusOK, applyOut.Code, applyOut.Body.String())

	rollbackReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-prompt-rollback/rollback", nil)
	rollbackReq.SetPathValue("id", "sug-prompt-rollback")
	rollbackOut := httptest.NewRecorder()
	srv.handleAdminOptimizationRollbackSuggestion(rollbackOut, rollbackReq)

	require.Equal(t, http.StatusOK, rollbackOut.Code, rollbackOut.Body.String())
	require.Len(t, prompts.upserts, 2)
	require.Equal(t, "new prompt content", prompts.upserts[0].content)
	require.Equal(t, "old prompt content", prompts.upserts[1].content)
	var rollout agentquality.OptimizationRollout
	require.NoError(t, json.NewDecoder(rollbackOut.Body).Decode(&rollout))
	require.Equal(t, agentquality.RolloutRolledBack, rollout.Status)
}

func TestAdminOptimizationRollbackSuggestion_RestoresWritableTargets(t *testing.T) {
	t.Run("skill restores previous DB override", func(t *testing.T) {
		srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
		srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
		srv.optimizationRolloutStore = agentquality.NewInMemoryOptimizationRolloutStore()
		skills := &fakeSkillStore{records: map[string]*store.SkillRecord{
			"candidate-skill|": {Name: "candidate-skill", Content: "old skill body", Level: "user"},
		}}
		srv.skillStore = skills
		now := time.Now()
		_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
			ID:            "sug-skill-rollback",
			Status:        agentquality.SuggestionApproved,
			Target:        agentquality.TargetSkillContent,
			CurrentValue:  "candidate-skill",
			ProposedValue: "new skill body",
			CreatedAt:     now,
			UpdatedAt:     now,
			ExpiresAt:     now.Add(time.Hour),
		})
		require.NoError(t, err)

		applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-skill-rollback/apply", nil)
		applyReq.SetPathValue("id", "sug-skill-rollback")
		applyOut := httptest.NewRecorder()
		srv.handleAdminOptimizationApplySuggestion(applyOut, applyReq)
		require.Equal(t, http.StatusOK, applyOut.Code, applyOut.Body.String())

		rollbackReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-skill-rollback/rollback", nil)
		rollbackReq.SetPathValue("id", "sug-skill-rollback")
		rollbackOut := httptest.NewRecorder()
		srv.handleAdminOptimizationRollbackSuggestion(rollbackOut, rollbackReq)
		require.Equal(t, http.StatusOK, rollbackOut.Code, rollbackOut.Body.String())
		require.Len(t, skills.upserts, 2)
		require.Equal(t, "new skill body", skills.upserts[0].content)
		require.Equal(t, "old skill body", skills.upserts[1].content)
	})

	t.Run("tool description deletes newly created override", func(t *testing.T) {
		srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
		srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
		srv.optimizationRolloutStore = agentquality.NewInMemoryOptimizationRolloutStore()
		writable := newFakeOptimizationWritableStore()
		srv.toolDescriptionStore = writable
		now := time.Now()
		_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
			ID:            "sug-tool-rollback",
			Status:        agentquality.SuggestionApproved,
			Target:        agentquality.TargetToolDescription,
			CurrentValue:  "grep",
			ProposedValue: "new grep description",
			CreatedAt:     now,
			UpdatedAt:     now,
			ExpiresAt:     now.Add(time.Hour),
		})
		require.NoError(t, err)

		applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-tool-rollback/apply", nil)
		applyReq.SetPathValue("id", "sug-tool-rollback")
		applyOut := httptest.NewRecorder()
		srv.handleAdminOptimizationApplySuggestion(applyOut, applyReq)
		require.Equal(t, http.StatusOK, applyOut.Code, applyOut.Body.String())
		require.Equal(t, "new grep description", writable.toolDescriptions["grep"])

		rollbackReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-tool-rollback/rollback", nil)
		rollbackReq.SetPathValue("id", "sug-tool-rollback")
		rollbackOut := httptest.NewRecorder()
		srv.handleAdminOptimizationRollbackSuggestion(rollbackOut, rollbackReq)
		require.Equal(t, http.StatusOK, rollbackOut.Code, rollbackOut.Body.String())
		require.NotContains(t, writable.toolDescriptions, "grep")
	})

	t.Run("memory policy restores previous policy", func(t *testing.T) {
		srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
		srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
		srv.optimizationRolloutStore = agentquality.NewInMemoryOptimizationRolloutStore()
		writable := newFakeOptimizationWritableStore()
		writable.memoryPolicies["default"] = `{"min_confidence":0.3,"max_memories":100}`
		srv.memoryGovernancePolicyStore = writable
		now := time.Now()
		_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
			ID:            "sug-memory-rollback",
			Status:        agentquality.SuggestionApproved,
			Target:        agentquality.TargetMemoryGovernance,
			CurrentValue:  "default",
			ProposedValue: `{"min_confidence":0.7,"max_memories":50}`,
			CreatedAt:     now,
			UpdatedAt:     now,
			ExpiresAt:     now.Add(time.Hour),
		})
		require.NoError(t, err)

		applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-memory-rollback/apply", nil)
		applyReq.SetPathValue("id", "sug-memory-rollback")
		applyOut := httptest.NewRecorder()
		srv.handleAdminOptimizationApplySuggestion(applyOut, applyReq)
		require.Equal(t, http.StatusOK, applyOut.Code, applyOut.Body.String())

		rollbackReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-memory-rollback/rollback", nil)
		rollbackReq.SetPathValue("id", "sug-memory-rollback")
		rollbackOut := httptest.NewRecorder()
		srv.handleAdminOptimizationRollbackSuggestion(rollbackOut, rollbackReq)
		require.Equal(t, http.StatusOK, rollbackOut.Code, rollbackOut.Body.String())
		require.Equal(t, `{"min_confidence":0.3,"max_memories":100}`, writable.memoryPolicies["default"])
	})
}

func TestAdminOptimizationApplySuggestion_PromptWithoutKeyReturnsClearError(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
	srv.promptStore = &fakePromptStore{}
	now := time.Now()
	_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
		ID:            "sug-prompt-missing-key",
		Status:        agentquality.SuggestionApproved,
		Target:        agentquality.TargetPrompt,
		ProposedValue: "new prompt content",
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-prompt-missing-key/apply", nil)
	req.SetPathValue("id", "sug-prompt-missing-key")
	out := httptest.NewRecorder()
	srv.handleAdminOptimizationApplySuggestion(out, req)

	require.Equal(t, http.StatusBadRequest, out.Code, out.Body.String())
	got, ok, err := srv.optimizationStore.GetSuggestion(context.Background(), "sug-prompt-missing-key")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, agentquality.SuggestionApplyError, got.ApplyStatus)
	require.Contains(t, got.ApplyError, "prompt key")
}

func TestAdminOptimizationApplySuggestion_ApprovedSkillUpserts(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
	skills := &fakeSkillStore{}
	srv.skillStore = skills
	now := time.Now()
	_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
		ID:            "sug-skill",
		Status:        agentquality.SuggestionApproved,
		Target:        agentquality.TargetSkillContent,
		CurrentValue:  "candidate-skill",
		ProposedValue: "---\nname: candidate-skill\ndescription: test\n---\n\nbody",
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-skill/apply", nil)
	req.SetPathValue("id", "sug-skill")
	out := httptest.NewRecorder()
	srv.handleAdminOptimizationApplySuggestion(out, req)

	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Len(t, skills.upserts, 1)
	require.Equal(t, skillUpsert{name: "candidate-skill", userID: "", content: "---\nname: candidate-skill\ndescription: test\n---\n\nbody", level: "user", path: "", updatedBy: "optimization", expectRevision: 0}, skills.upserts[0])
	var got agentquality.OptimizationReviewSuggestion
	require.NoError(t, json.NewDecoder(out.Body).Decode(&got))
	require.Equal(t, agentquality.SuggestionApplyApplied, got.ApplyStatus)
}

func TestAdminOptimizationApplySuggestion_ToolDescriptionPersistsOverride(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
	toolStore := newFakeOptimizationWritableStore()
	srv.toolDescriptionStore = toolStore
	now := time.Now()
	_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
		ID:            "sug-tool",
		Status:        agentquality.SuggestionApproved,
		Target:        agentquality.TargetToolDescription,
		CurrentValue:  "grep",
		ProposedValue: "new tool description",
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-tool/apply", nil)
	req.SetPathValue("id", "sug-tool")
	out := httptest.NewRecorder()
	srv.handleAdminOptimizationApplySuggestion(out, req)

	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Equal(t, map[string]string{"grep": "new tool description"}, toolStore.toolDescriptions)
	got, ok, err := srv.optimizationStore.GetSuggestion(context.Background(), "sug-tool")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, agentquality.SuggestionApplyApplied, got.ApplyStatus)
}

func TestAdminOptimizationApplySuggestion_MemoryGovernancePersistsPolicy(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	srv.optimizationStore = agentquality.NewInMemoryOptimizationSuggestionStore()
	policyStore := newFakeOptimizationWritableStore()
	srv.memoryGovernancePolicyStore = policyStore
	now := time.Now()
	_, err := srv.optimizationStore.UpsertSuggestion(context.Background(), agentquality.OptimizationReviewSuggestion{
		ID:            "sug-memory-policy",
		Status:        agentquality.SuggestionApproved,
		Target:        agentquality.TargetMemoryGovernance,
		CurrentValue:  "default",
		ProposedValue: `{"min_confidence":0.6,"max_memories":250}`,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/optimization/suggestions/sug-memory-policy/apply", nil)
	req.SetPathValue("id", "sug-memory-policy")
	out := httptest.NewRecorder()
	srv.handleAdminOptimizationApplySuggestion(out, req)

	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	require.Equal(t, `{"min_confidence":0.6,"max_memories":250}`, policyStore.memoryPolicies["default"])
	got, ok, err := srv.optimizationStore.GetSuggestion(context.Background(), "sug-memory-policy")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, agentquality.SuggestionApplyApplied, got.ApplyStatus)
}

type promptUpsert struct {
	key       string
	language  string
	content   string
	updatedBy string
}

type fakePromptStore struct {
	records map[string]string
	upserts []promptUpsert
}

func (s *fakePromptStore) Get(ctx context.Context, key, language string) (string, bool, error) {
	value, ok := s.records[key+"|"+language]
	return value, ok, nil
}

func (s *fakePromptStore) Upsert(ctx context.Context, key, language, content, updatedBy string) error {
	if s.records == nil {
		s.records = map[string]string{}
	}
	s.records[key+"|"+language] = content
	s.upserts = append(s.upserts, promptUpsert{key: key, language: language, content: content, updatedBy: updatedBy})
	return nil
}

func (s *fakePromptStore) Delete(ctx context.Context, key, language string) error {
	return nil
}

func (s *fakePromptStore) List(ctx context.Context, page, size int) ([]store.PromptRecord, int, error) {
	return nil, 0, nil
}

type fakePromptLoader struct {
	invalidated []string
}

func (l *fakePromptLoader) InvalidateDBCache(key string) {
	l.invalidated = append(l.invalidated, key)
}

type skillUpsert struct {
	name           string
	userID         string
	content        string
	level          string
	path           string
	updatedBy      string
	expectRevision int
}

type fakeSkillStore struct {
	records map[string]*store.SkillRecord
	upserts []skillUpsert
	deletes []string
}

func (s *fakeSkillStore) Get(ctx context.Context, name, userID string) (*store.SkillRecord, bool, error) {
	if s.records == nil {
		return nil, false, nil
	}
	row, ok := s.records[name+"|"+userID]
	if !ok {
		return nil, false, nil
	}
	out := *row
	return &out, true, nil
}

func (s *fakeSkillStore) Upsert(ctx context.Context, name, userID, content, level, path, updatedBy string, expectRevision int) error {
	if s.records == nil {
		s.records = map[string]*store.SkillRecord{}
	}
	s.records[name+"|"+userID] = &store.SkillRecord{Name: name, UserID: userID, Content: content, Level: level, Path: path, UpdatedBy: updatedBy, Revision: expectRevision}
	s.upserts = append(s.upserts, skillUpsert{name: name, userID: userID, content: content, level: level, path: path, updatedBy: updatedBy, expectRevision: expectRevision})
	return nil
}

func (s *fakeSkillStore) Delete(ctx context.Context, name, userID string) error {
	delete(s.records, name+"|"+userID)
	s.deletes = append(s.deletes, name+"|"+userID)
	return nil
}

func (s *fakeSkillStore) List(ctx context.Context, page, size int) ([]store.SkillRecord, int, error) {
	return nil, 0, nil
}

type fakeOptimizationWritableStore struct {
	toolDescriptions map[string]string
	memoryPolicies   map[string]string
}

func newFakeOptimizationWritableStore() *fakeOptimizationWritableStore {
	return &fakeOptimizationWritableStore{
		toolDescriptions: map[string]string{},
		memoryPolicies:   map[string]string{},
	}
}

func (s *fakeOptimizationWritableStore) UpsertToolDescription(_ context.Context, toolName, description, _ string) error {
	s.toolDescriptions[toolName] = description
	return nil
}

func (s *fakeOptimizationWritableStore) GetToolDescription(_ context.Context, toolName string) (string, bool, error) {
	value, ok := s.toolDescriptions[toolName]
	return value, ok, nil
}

func (s *fakeOptimizationWritableStore) DeleteToolDescription(_ context.Context, toolName string) error {
	delete(s.toolDescriptions, toolName)
	return nil
}

func (s *fakeOptimizationWritableStore) UpsertMemoryGovernancePolicy(_ context.Context, policyName, policyJSON, _ string) error {
	s.memoryPolicies[policyName] = policyJSON
	return nil
}

func (s *fakeOptimizationWritableStore) GetMemoryGovernancePolicy(_ context.Context, policyName string) (string, bool, error) {
	value, ok := s.memoryPolicies[policyName]
	return value, ok, nil
}

func (s *fakeOptimizationWritableStore) DeleteMemoryGovernancePolicy(_ context.Context, policyName string) error {
	delete(s.memoryPolicies, policyName)
	return nil
}
