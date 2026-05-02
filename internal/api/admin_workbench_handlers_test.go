package api

import (
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

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays", strings.NewReader(`{"kind":"cluster","target_ids":["cl_1"],"max_attempt":2}`))
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

	cancelReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/replays/"+created.Jobs[0].ID+"/cancel", nil)
	cancelReq.SetPathValue("id", created.Jobs[0].ID)
	cancelOut := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCancelReplay(cancelOut, cancelReq)

	require.Equal(t, http.StatusOK, cancelOut.Code, cancelOut.Body.String())
	var cancelled qualityworkbench.ReplayJob
	require.NoError(t, json.NewDecoder(cancelOut.Body).Decode(&cancelled))
	require.Equal(t, qualityworkbench.ReplayJobCancelled, cancelled.Status)
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
		})
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/quality-workbench/batch-evals", strings.NewReader(`{"mode":"unsafe"}`))
	out := httptest.NewRecorder()
	srv.handleAdminQualityWorkbenchCreateBatchEval(out, req)
	require.Equal(t, http.StatusBadRequest, out.Code)
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
