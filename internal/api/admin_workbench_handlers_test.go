package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/chef-guo/agents-hive/internal/qualityworkbench"
)

func TestAdminQualityWorkbenchReplayLifecycle(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays", strings.NewReader(`{"kind":"cluster","target_ids":["cl_1"],"domain_id":"customer_service","source_kind":"workflow","source_name":"case_triage","max_attempt":2}`))
	createOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateReplays(createOut, createReq)

	require.Equal(t, http.StatusCreated, createOut.Code, createOut.Body.String())
	var created struct {
		BatchID string                       `json:"batch_id"`
		Jobs    []qualityworkbench.ReplayJob `json:"jobs"`
	}
	require.NoError(t, json.NewDecoder(createOut.Body).Decode(&created))
	require.Len(t, created.Jobs, 1)
	require.Equal(t, qualityworkbench.ReplayJobQueued, created.Jobs[0].Status)
	require.Equal(t, "customer_service", created.Jobs[0].DomainID)
	require.Equal(t, "workflow", created.Jobs[0].SourceKind)
	require.Equal(t, "case_triage", created.Jobs[0].SourceName)

	cancelReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays/"+created.Jobs[0].ID+"/cancel", nil)
	cancelReq.SetPathValue("id", created.Jobs[0].ID)
	cancelOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCancelReplay(cancelOut, cancelReq)

	require.Equal(t, http.StatusOK, cancelOut.Code, cancelOut.Body.String())
	var cancelled qualityworkbench.ReplayJob
	require.NoError(t, json.NewDecoder(cancelOut.Body).Decode(&cancelled))
	require.Equal(t, qualityworkbench.ReplayJobCancelled, cancelled.Status)
}

func TestAdminQualityWorkbenchReplayAndBatchEvalFilterByAttribution(t *testing.T) {
	store := newFakeQualityCandidateStore()
	first := testWorkbenchCandidate("candidate-1", "failed")
	first.SourceEvent.DomainID = "customer_service"
	first.SourceEvent.SourceKind = "workflow"
	first.SourceEvent.SourceName = "case_triage"
	store.records[first.ID] = first
	second := testWorkbenchCandidate("candidate-2", "failed")
	second.SourceEvent.DomainID = "generic"
	second.SourceEvent.SourceKind = "master"
	second.SourceEvent.SourceName = "react"
	store.records[second.ID] = second
	srv := newQualityCandidateTestServer(store)

	replayReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays", strings.NewReader(`{"kind":"candidate","target_ids":["candidate-1"],"domain_id":"customer_service","source_kind":"workflow","source_name":"case_triage"}`))
	replayOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateReplays(replayOut, replayReq)
	require.Equal(t, http.StatusCreated, replayOut.Code, replayOut.Body.String())

	replaysReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/quality-workbench/replays?domain_id=customer_service&source_kind=workflow&source_name=case_triage", nil)
	replaysOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchListReplays(replaysOut, replaysReq)
	require.Equal(t, http.StatusOK, replaysOut.Code, replaysOut.Body.String())
	var replays struct {
		Items []qualityworkbench.ReplayJob `json:"items"`
	}
	require.NoError(t, json.NewDecoder(replaysOut.Body).Decode(&replays))
	require.Len(t, replays.Items, 1)
	require.Equal(t, "customer_service", replays.Items[0].DomainID)

	evalReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/batch-evals?domain_id=customer_service&source_kind=workflow&source_name=case_triage", strings.NewReader(`{"mode":"manual"}`))
	evalOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateBatchEval(evalOut, evalReq)
	require.Equal(t, http.StatusCreated, evalOut.Code, evalOut.Body.String())
	var run qualityworkbench.BatchEvalRun
	require.NoError(t, json.NewDecoder(evalOut.Body).Decode(&run))
	require.Equal(t, "customer_service", run.DomainID)
	require.Equal(t, 1, run.Summary.Total)

	evalsReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/quality-workbench/batch-evals?domain_id=customer_service&source_kind=workflow&source_name=case_triage", nil)
	evalsOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchListBatchEvals(evalsOut, evalsReq)
	require.Equal(t, http.StatusOK, evalsOut.Code, evalsOut.Body.String())
	var evals struct {
		Items []qualityworkbench.BatchEvalRun `json:"items"`
	}
	require.NoError(t, json.NewDecoder(evalsOut.Body).Decode(&evals))
	require.Len(t, evals.Items, 1)
	require.Equal(t, "workflow", evals.Items[0].SourceKind)
}

func TestAdminQualityWorkbenchPreviewFanoutAndVersionDiff(t *testing.T) {
	store := newFakeQualityCandidateStore()
	store.records["candidate-1"] = testWorkbenchCandidate("candidate-1", "failed")
	store.records["candidate-2"] = testWorkbenchCandidate("candidate-2", "failed")
	srv := newQualityCandidateTestServer(store)

	previewReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/grouping-rules/preview", nil)
	previewOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchPreviewGroupingRules(previewOut, previewReq)
	require.Equal(t, http.StatusOK, previewOut.Code, previewOut.Body.String())
	var preview qualityworkbench.GroupingRulePreview
	require.NoError(t, json.NewDecoder(previewOut.Body).Decode(&preview))
	require.NotEmpty(t, preview.Clusters)
	require.NotEmpty(t, preview.RuleHits)

	fanoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays/fanout", strings.NewReader(`{"target_ids":["a","b","c"],"limit":2}`))
	fanoutOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchReplayFanout(fanoutOut, fanoutReq)
	require.Equal(t, http.StatusOK, fanoutOut.Code, fanoutOut.Body.String())
	var fanout qualityworkbench.ReplayClusterFanoutPlan
	require.NoError(t, json.NewDecoder(fanoutOut.Body).Decode(&fanout))
	require.True(t, fanout.Truncated)
	require.Equal(t, []string{"a", "b"}, fanout.SelectedIDs)

	diffReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/version-diff", strings.NewReader(`{
		"baseline_run_id":"base",
		"treatment_run_id":"treat",
		"baseline":[{"case_id":"case-1","passed":true}],
		"treatment":[{"case_id":"case-1","passed":false,"reason":"tool miss"}]
	}`))
	diffOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchVersionDiff(diffOut, diffReq)
	require.Equal(t, http.StatusOK, diffOut.Code, diffOut.Body.String())
	var diff qualityworkbench.VersionMatrixDiff
	require.NoError(t, json.NewDecoder(diffOut.Body).Decode(&diff))
	require.Equal(t, []string{"case-1"}, diff.RegressedCaseIDs)
}

func TestAdminQualityWorkbenchGroupingRulesCRUDDrivesPreview(t *testing.T) {
	store := newFakeQualityCandidateStore()
	store.records["candidate-1"] = testWorkbenchCandidate("candidate-1", "failed")
	store.records["candidate-2"] = testWorkbenchCandidate("candidate-2", "failed")
	second := store.records["candidate-2"]
	second.SourceEvent.ToolDecision.Actual = "curl"
	store.records["candidate-2"] = second
	srv := newQualityCandidateTestServer(store)

	body := `{
		"name": "Tool Split",
		"priority": 1,
		"enabled": true,
		"match": {"failure_type":"tool"},
		"key_fields": ["failure_type","tool"],
		"digest_normalize": ["path","num"]
	}`
	upsertReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/quality-workbench/grouping-rules/tool_split", strings.NewReader(body))
	upsertReq.SetPathValue("id", "tool_split")
	upsertOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchUpsertGroupingRule(upsertOut, upsertReq)
	require.Equal(t, http.StatusOK, upsertOut.Code, upsertOut.Body.String())

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/quality-workbench/grouping-rules", nil)
	listOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchListGroupingRules(listOut, listReq)
	require.Equal(t, http.StatusOK, listOut.Code, listOut.Body.String())
	var listed struct {
		Items []qualityworkbench.GroupingRule `json:"items"`
		Total int                             `json:"total"`
	}
	require.NoError(t, json.NewDecoder(listOut.Body).Decode(&listed))
	require.Equal(t, 1, listed.Total)
	require.Equal(t, "tool_split", listed.Items[0].ID)

	previewReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/grouping-rules/preview", nil)
	previewOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchPreviewGroupingRules(previewOut, previewReq)
	require.Equal(t, http.StatusOK, previewOut.Code, previewOut.Body.String())
	var preview qualityworkbench.GroupingRulePreview
	require.NoError(t, json.NewDecoder(previewOut.Body).Decode(&preview))
	require.Equal(t, 2, preview.RuleHits["tool_split"])
	require.Len(t, preview.Clusters, 2)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/quality-workbench/grouping-rules/tool_split", nil)
	deleteReq.SetPathValue("id", "tool_split")
	deleteOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchDeleteGroupingRule(deleteOut, deleteReq)
	require.Equal(t, http.StatusNoContent, deleteOut.Code)
}

func TestAdminQualityWorkbenchRunReplayExecutesCandidate(t *testing.T) {
	store := newFakeQualityCandidateStore()
	store.records["candidate-1"] = testWorkbenchCandidate("candidate-1", "passed")
	rec := store.records["candidate-1"]
	rec.Case = agentquality.Candidate{
		ID:             "candidate-1",
		Name:           "candidate replay",
		Route:          "web",
		Input:          "hello",
		ExpectedStatus: agentquality.StatusPass,
		Required:       true,
	}
	store.records["candidate-1"] = rec
	srv := newQualityCandidateTestServer(store)
	srv.SetQualityEvalRunner(agentquality.StaticEvalRunner{})

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays", strings.NewReader(`{"kind":"candidate","target_ids":["candidate-1"],"max_attempt":1}`))
	createOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateReplays(createOut, createReq)
	require.Equal(t, http.StatusCreated, createOut.Code, createOut.Body.String())
	var created struct {
		Jobs []qualityworkbench.ReplayJob `json:"jobs"`
	}
	require.NoError(t, json.NewDecoder(createOut.Body).Decode(&created))
	require.Len(t, created.Jobs, 1)

	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays/"+created.Jobs[0].ID+"/run", nil)
	runReq.SetPathValue("id", created.Jobs[0].ID)
	runOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchRunReplay(runOut, runReq)

	require.Equal(t, http.StatusOK, runOut.Code, runOut.Body.String())
	var ran qualityworkbench.ReplayJob
	require.NoError(t, json.NewDecoder(runOut.Body).Decode(&ran))
	require.Equal(t, qualityworkbench.ReplayJobSucceeded, ran.Status)
	require.Equal(t, 1, ran.Attempt)
	require.Equal(t, 1, ran.Result.Total)
	require.Equal(t, 1, ran.Result.Passed)
	require.Equal(t, []string{"candidate-1"}, ran.Result.CaseIDs)
}

func TestAdminQualityWorkbenchRunReplayWithoutEvalRunnerFailsClosed(t *testing.T) {
	store := newFakeQualityCandidateStore()
	store.records["candidate-1"] = testWorkbenchCandidate("candidate-1", "passed")
	rec := store.records["candidate-1"]
	rec.Case = agentquality.Candidate{
		ID:             "candidate-1",
		Name:           "candidate replay",
		Route:          "web",
		Input:          "hello",
		ExpectedStatus: agentquality.StatusPass,
		Required:       true,
	}
	store.records["candidate-1"] = rec
	srv := newQualityCandidateTestServer(store)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays", strings.NewReader(`{"kind":"candidate","target_ids":["candidate-1"],"max_attempt":1}`))
	createOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateReplays(createOut, createReq)
	require.Equal(t, http.StatusCreated, createOut.Code, createOut.Body.String())
	var created struct {
		Jobs []qualityworkbench.ReplayJob `json:"jobs"`
	}
	require.NoError(t, json.NewDecoder(createOut.Body).Decode(&created))
	require.Len(t, created.Jobs, 1)

	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays/"+created.Jobs[0].ID+"/run", nil)
	runReq.SetPathValue("id", created.Jobs[0].ID)
	runOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchRunReplay(runOut, runReq)

	require.Equal(t, http.StatusOK, runOut.Code, runOut.Body.String())
	var ran qualityworkbench.ReplayJob
	require.NoError(t, json.NewDecoder(runOut.Body).Decode(&ran))
	require.Equal(t, qualityworkbench.ReplayJobFailed, ran.Status)
	require.Contains(t, ran.Error, "eval runner not configured")
	require.Equal(t, 1, ran.Result.Total)
	require.Equal(t, 1, ran.Result.Unknown)
	require.Equal(t, []string{"candidate-1"}, ran.Result.CaseIDs)
	require.Contains(t, ran.Result.Reasons, "candidate-1 has no result")
}

func TestAdminQualityWorkbenchBatchEvalAndReport(t *testing.T) {
	store := newFakeQualityCandidateStore()
	store.records["candidate-1"] = testWorkbenchCandidate("candidate-1", "failed")
	srv := newQualityCandidateTestServer(store)

	evalReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/batch-evals", strings.NewReader(`{"mode":"manual"}`))
	evalOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateBatchEval(evalOut, evalReq)

	require.Equal(t, http.StatusCreated, evalOut.Code, evalOut.Body.String())
	var run qualityworkbench.BatchEvalRun
	require.NoError(t, json.NewDecoder(evalOut.Body).Decode(&run))
	require.Equal(t, qualityworkbench.BatchEvalFailed, run.Status)

	reportReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/reports/generate", strings.NewReader(`{"week_start":"2026-04-27"}`))
	reportOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchGenerateReport(reportOut, reportReq)

	require.Equal(t, http.StatusCreated, reportOut.Code, reportOut.Body.String())
	var report struct {
		ID       string `json:"id"`
		Markdown string `json:"markdown"`
	}
	require.NoError(t, json.NewDecoder(reportOut.Body).Decode(&report))
	require.Equal(t, "report_20260427", report.ID)
	require.Contains(t, report.Markdown, "Quality Workbench Weekly Report")

	downloadReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/quality-workbench/reports/"+report.ID+"/download", nil)
	downloadReq.SetPathValue("id", report.ID)
	downloadOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchDownloadReport(downloadOut, downloadReq)

	require.Equal(t, http.StatusOK, downloadOut.Code, downloadOut.Body.String())
	require.Equal(t, "text/markdown; charset=utf-8", downloadOut.Header().Get("Content-Type"))
	require.Contains(t, downloadOut.Header().Get("Content-Disposition"), `filename="report_20260427.md"`)
	require.Contains(t, downloadOut.Body.String(), "Quality Workbench Weekly Report")
}

func TestAdminQualityWorkbenchBatchEvalModes(t *testing.T) {
	store := newFakeQualityCandidateStore()
	store.records["candidate-1"] = testWorkbenchCandidate("candidate-1", "passed")
	srv := newQualityCandidateTestServer(store)
	shadowStore := agentquality.NewInMemoryShadowEvalResultStore()
	require.NoError(t, shadowStore.Store(
		context.Background(),
		agentquality.ShadowEvalResult{
			CaseID:   "shadow-case-1",
			DomainID: "customer_service",
			Passed:   true,
			JudgeVerdict: agentquality.EvaluationVerdict{
				Score:       8,
				Verdict:     "passed",
				FailureType: agentquality.FailureNone,
			},
			RunnerInfo: agentquality.RunnerInfo{EvidenceLevel: agentquality.EvidenceProductionShadow},
			Timestamp:  time.Now(),
		},
	))
	srv.SetQualityShadowEvalStore(shadowStore)

	for _, tc := range []struct {
		mode string
		kind qualityworkbench.BatchEvalKind
	}{
		{mode: "full", kind: qualityworkbench.BatchEvalKindFull},
		{mode: "incremental", kind: qualityworkbench.BatchEvalKindIncremental},
		{mode: "shadow", kind: qualityworkbench.BatchEvalKindShadow},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/batch-evals", strings.NewReader(`{"mode":"`+tc.mode+`"}`))
			out := httptest.NewRecorder()
			srv.handleAdminQualityWorkbenchCreateBatchEval(out, req)

			require.Equal(t, http.StatusCreated, out.Code, out.Body.String())
			var run qualityworkbench.BatchEvalRun
			require.NoError(t, json.NewDecoder(out.Body).Decode(&run))
			require.Equal(t, tc.kind, run.Kind)
			if tc.kind == qualityworkbench.BatchEvalKindShadow {
				require.Len(t, run.ShadowResults, 1)
				require.Len(t, run.ShadowMetrics, 1)
				require.Len(t, run.DomainRegressions, 1)
			}
		})
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/batch-evals", strings.NewReader(`{"mode":"unsafe"}`))
	out := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateBatchEval(out, req)
	require.Equal(t, http.StatusBadRequest, out.Code)
}

func TestAdminQualityWorkbenchShadowBatchEvalRequiresShadowStore(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	srv.SetQualityShadowEvalStore(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/batch-evals", strings.NewReader(`{"mode":"shadow"}`))
	out := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateBatchEval(out, req)

	require.Equal(t, http.StatusInternalServerError, out.Code)
	require.Contains(t, out.Body.String(), "shadow eval result store not configured")
}

func TestAdminQualityWorkbenchUsesInjectedStores(t *testing.T) {
	store := newFakeQualityCandidateStore()
	store.records["candidate-1"] = testWorkbenchCandidate("candidate-1", "passed")
	srv := newQualityCandidateTestServer(store)
	replayStore := qualityworkbench.NewMemoryReplayJobStore(func() time.Time {
		return time.Date(2026, 4, 30, 1, 2, 3, 0, time.UTC)
	})
	evalStore := qualityworkbench.NewMemoryBatchEvalRunStore(func() time.Time {
		return time.Date(2026, 4, 30, 1, 2, 3, 0, time.UTC)
	})
	reportStore := qualityworkbench.NewMemoryWeeklyReportStore(func() time.Time {
		return time.Date(2026, 4, 30, 1, 2, 3, 0, time.UTC)
	})
	srv.SetQualityWorkbenchStores(replayStore, evalStore, reportStore)

	replayReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays", strings.NewReader(`{"target_ids":["candidate-1"]}`))
	replayOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateReplays(replayOut, replayReq)
	require.Equal(t, http.StatusCreated, replayOut.Code, replayOut.Body.String())

	replays := replayStore.List(qualityworkbench.ReplayJobListFilter{Limit: 10})
	require.Len(t, replays, 1)

	evalReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/batch-evals", strings.NewReader(`{"mode":"manual"}`))
	evalOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateBatchEval(evalOut, evalReq)
	require.Equal(t, http.StatusCreated, evalOut.Code, evalOut.Body.String())
	require.Len(t, evalStore.List(qualityworkbench.BatchEvalRunListFilter{Limit: 10}), 1)

	reportReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/reports/generate", strings.NewReader(`{"week_start":"2026-04-27"}`))
	reportOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchGenerateReport(reportOut, reportReq)
	require.Equal(t, http.StatusCreated, reportOut.Code, reportOut.Body.String())
	require.Len(t, reportStore.List(qualityworkbench.WeeklyReportListFilter{Limit: 10}), 1)
}

func TestAdminQualityWorkbenchBatchEvalLoadsCasesDir(t *testing.T) {
	store := newFakeQualityCandidateStore()
	srv := newQualityCandidateTestServer(store)
	casesDir := t.TempDir()
	c := `{"id":"case-required","name":"required","route":"web","input":"hello","expected_status":"pass","required":true}`
	require.NoError(t, os.WriteFile(filepath.Join(casesDir, "case-required.json"), []byte(c), 0o600))

	evalReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/batch-evals", strings.NewReader(`{"mode":"manual","cases_dir":"`+casesDir+`"}`))
	evalOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateBatchEval(evalOut, evalReq)

	require.Equal(t, http.StatusCreated, evalOut.Code, evalOut.Body.String())
	var run qualityworkbench.BatchEvalRun
	require.NoError(t, json.NewDecoder(evalOut.Body).Decode(&run))
	require.Equal(t, qualityworkbench.BatchEvalFailed, run.Status)
	require.Equal(t, 1, run.Summary.Total)
	require.Equal(t, 1, run.Summary.Unknown)
	require.Contains(t, run.Summary.Reasons, "golden cases eval runner not configured")
}

func TestAdminQualityWorkbenchDashboardHonorsTimeWindow(t *testing.T) {
	store := newFakeQualityCandidateStore()
	inside := testWorkbenchCandidate("candidate-inside", "failed")
	inside.CreatedAt = time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	inside.SourceEvent.DomainID = "customer_service"
	inside.SourceEvent.SourceKind = "workflow"
	inside.SourceEvent.SourceName = "case_triage"
	store.records[inside.ID] = inside
	outside := testWorkbenchCandidate("candidate-outside", "failed")
	outside.CreatedAt = time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	outside.SourceEvent.DomainID = "customer_service"
	outside.SourceEvent.SourceKind = "workflow"
	outside.SourceEvent.SourceName = "case_triage"
	store.records[outside.ID] = outside
	srv := newQualityCandidateTestServer(store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/quality-workbench/dashboard/snapshot?since=2026-05-01T00:00:00Z&until=2026-05-03T00:00:00Z&domain_id=customer_service", nil)
	out := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchDashboardSnapshot(out, req)

	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	var snapshot qualityworkbench.DashboardSnapshot
	require.NoError(t, json.NewDecoder(out.Body).Decode(&snapshot))
	require.Equal(t, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), snapshot.Since)
	require.Equal(t, time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC), snapshot.Until)
	require.Equal(t, 1, snapshot.CandidateStatusCounts[agentquality.CandidatePromoted])
	require.Equal(t, 1, snapshot.DomainCounts["customer_service"])
}

func TestAdminQualityWorkbenchClustersRedactsSecretsAndHonorsUserBoundary(t *testing.T) {
	store := newFakeQualityCandidateStore()
	own := testWorkbenchCandidate("candidate-own", "failed")
	own.SourceEvent.UserID = "admin-1"
	own.SourceEvent.Attributes = map[string]any{
		"error": "request failed with token=raw-cluster-secret",
	}
	store.records[own.ID] = own
	other := testWorkbenchCandidate("candidate-other", "failed")
	other.SourceEvent.UserID = "admin-2"
	other.SourceEvent.Attributes = map[string]any{
		"error": "request failed with token=other-user-secret",
	}
	store.records[other.ID] = other
	srv := newQualityCandidateTestServer(store)

	req := adminQualityRequest(http.MethodGet, "/api/v1/admin/quality-workbench/clusters", "", "admin-1")
	out := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchClusters(out, req)

	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	body := out.Body.String()
	require.Contains(t, body, "candidate-own")
	require.NotContains(t, body, "candidate-other")
	require.NotContains(t, body, "raw-cluster-secret")
	require.NotContains(t, body, "other-user-secret")
	require.Contains(t, body, "[REDACTED]")
}

func TestAdminQualityWorkbenchReplayResponsesRedactSecrets(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays", strings.NewReader(`{
		"kind":"candidate",
		"target_ids":["candidate-1"],
		"source_name":"token=raw-create-token",
		"max_attempt":1
	}`))
	createOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateReplays(createOut, createReq)
	require.Equal(t, http.StatusCreated, createOut.Code, createOut.Body.String())
	createBody := createOut.Body.String()
	require.NotContains(t, createBody, "raw-create-token")
	require.Contains(t, createBody, "[REDACTED]")

	var created struct {
		Jobs []qualityworkbench.ReplayJob `json:"jobs"`
	}
	require.NoError(t, json.NewDecoder(strings.NewReader(createBody)).Decode(&created))
	require.Len(t, created.Jobs, 1)
	id := created.Jobs[0].ID

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/quality-workbench/replays", nil)
	listOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchListReplays(listOut, listReq)
	require.Equal(t, http.StatusOK, listOut.Code, listOut.Body.String())
	require.NotContains(t, listOut.Body.String(), "raw-create-token")
	require.Contains(t, listOut.Body.String(), "[REDACTED]")

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/quality-workbench/replays/"+id, nil)
	getReq.SetPathValue("id", id)
	getOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchGetReplay(getOut, getReq)
	require.Equal(t, http.StatusOK, getOut.Code, getOut.Body.String())
	require.NotContains(t, getOut.Body.String(), "raw-create-token")
	require.Contains(t, getOut.Body.String(), "[REDACTED]")

	cancelReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays/"+id+"/cancel", nil)
	cancelReq.SetPathValue("id", id)
	cancelOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCancelReplay(cancelOut, cancelReq)
	require.Equal(t, http.StatusOK, cancelOut.Code, cancelOut.Body.String())
	require.NotContains(t, cancelOut.Body.String(), "raw-create-token")
	require.Contains(t, cancelOut.Body.String(), "[REDACTED]")
}

func TestAdminQualityWorkbenchBatchEvalListRedactsSecrets(t *testing.T) {
	store := newFakeQualityCandidateStore()
	candidate := testWorkbenchCandidate("candidate-1", "failed")
	candidate.SourceEvent.SourceName = "case_triage"
	store.records[candidate.ID] = candidate
	srv := newQualityCandidateTestServer(store)

	evalReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/batch-evals", strings.NewReader(`{
		"mode":"manual",
		"source_name":"client_secret=raw-eval-secret"
	}`))
	evalOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateBatchEval(evalOut, evalReq)
	require.Equal(t, http.StatusCreated, evalOut.Code, evalOut.Body.String())

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/quality-workbench/batch-evals", nil)
	listOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchListBatchEvals(listOut, listReq)
	require.Equal(t, http.StatusOK, listOut.Code, listOut.Body.String())
	require.NotContains(t, listOut.Body.String(), "raw-eval-secret")
	require.Contains(t, listOut.Body.String(), "[REDACTED]")
}

func TestAdminQualityWorkbenchDownloadReportRedactsSecrets(t *testing.T) {
	srv := newQualityCandidateTestServer(newFakeQualityCandidateStore())
	reportStore := qualityworkbench.NewMemoryWeeklyReportStore(func() time.Time {
		return time.Date(2026, 5, 15, 1, 2, 3, 0, time.UTC)
	})
	srv.SetQualityWorkbenchStores(nil, nil, reportStore)
	_, err := reportStore.Save(qualityworkbench.WeeklyReportSave{
		ID:        "report_secret",
		WeekStart: time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC),
		Report: qualityworkbench.WeeklyReport{
			Markdown: "# Report\n\npassword=raw-report-password\nclient_secret=raw-report-secret\n",
		},
	})
	require.NoError(t, err)

	downloadReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/quality-workbench/reports/report_secret/download", nil)
	downloadReq.SetPathValue("id", "report_secret")
	downloadOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchDownloadReport(downloadOut, downloadReq)

	require.Equal(t, http.StatusOK, downloadOut.Code, downloadOut.Body.String())
	body := downloadOut.Body.String()
	require.Contains(t, body, "# Report")
	require.NotContains(t, body, "raw-report-password")
	require.NotContains(t, body, "raw-report-secret")
	require.Contains(t, body, "[REDACTED]")
}

func testWorkbenchCandidate(id string, verifyResult string) agentquality.CandidateRecord {
	return agentquality.CandidateRecord{
		ID:           id,
		Status:       agentquality.CandidatePromoted,
		Route:        "web",
		Input:        "失败输入",
		FailureType:  agentquality.FailureTool,
		VerifyResult: verifyResult,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		SourceEvent: agentquality.Event{
			Route:       "web",
			FailureType: agentquality.FailureTool,
			FinalStatus: agentquality.StatusFail,
		},
	}
}
