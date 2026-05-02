package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/qualityworkbench"
)

func (s *Server) handleAdminQualityWorkbenchClusters(w http.ResponseWriter, r *http.Request) {
	page, size := parsePagination(r)
	items, clusters, _, ok := s.workbenchCandidatesAndClusters(w, r, page, size)
	if !ok {
		return
	}
	_ = items
	writeJSON(w, http.StatusOK, map[string]any{
		"clusters": clusters,
		"items":    clusters,
		"total":    len(clusters),
		"page":     page,
		"size":     size,
	})
}

func (s *Server) handleAdminQualityWorkbenchRecomputeClusters(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	_, clusters, _, ok := s.workbenchCandidatesAndClusters(w, r, 1, 100)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster_count": len(clusters),
		"took_ms":       time.Since(start).Milliseconds(),
	})
}

func (s *Server) handleAdminQualityWorkbenchPreviewGroupingRules(w http.ResponseWriter, r *http.Request) {
	items, _, err := s.listWorkbenchCandidates(r, 1, 1000)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	preview := qualityworkbench.PreviewGroupingRules(s.effectiveGroupingRules(), items)
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleAdminQualityWorkbenchListGroupingRules(w http.ResponseWriter, r *http.Request) {
	rules := s.workbenchGroupingRules().List()
	writeJSON(w, http.StatusOK, map[string]any{"items": rules, "total": len(rules)})
}

func (s *Server) handleAdminQualityWorkbenchUpsertGroupingRule(w http.ResponseWriter, r *http.Request) {
	var rule qualityworkbench.GroupingRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	if pathID := strings.TrimSpace(r.PathValue("id")); pathID != "" {
		rule.ID = pathID
	}
	saved, err := s.workbenchGroupingRules().Upsert(rule)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleAdminQualityWorkbenchDeleteGroupingRule(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := s.workbenchGroupingRules().Delete(id); err != nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: err.Error(), Code: errs.CodeNotFound})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminQualityWorkbenchReplayFanout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetIDs []string `json:"target_ids"`
		Limit     int      `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	writeJSON(w, http.StatusOK, qualityworkbench.PlanReplayClusterFanout(body.TargetIDs, body.Limit))
}

func (s *Server) handleAdminQualityWorkbenchVersionDiff(w http.ResponseWriter, r *http.Request) {
	var body qualityworkbench.VersionMatrixInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	writeJSON(w, http.StatusOK, qualityworkbench.CompareVersionMatrix(body))
}

func (s *Server) handleAdminQualityWorkbenchCreateReplays(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind       qualityworkbench.ReplayJobKind `json:"kind"`
		TargetIDs  []string                       `json:"target_ids"`
		MaxAttempt int                            `json:"max_attempt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	if body.Kind == "" {
		body.Kind = qualityworkbench.ReplayJobKindCandidate
	}
	batchID := "replay_batch_" + time.Now().UTC().Format("20060102T150405.000000000")
	job, err := s.workbenchReplay().Create(qualityworkbench.ReplayJobCreate{
		BatchID:    batchID,
		Kind:       body.Kind,
		TargetIDs:  body.TargetIDs,
		MaxAttempt: body.MaxAttempt,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"batch_id": batchID,
		"jobs":     []qualityworkbench.ReplayJob{job},
	})
}

func (s *Server) handleAdminQualityWorkbenchListReplays(w http.ResponseWriter, r *http.Request) {
	page, size := parsePagination(r)
	items := s.workbenchReplay().List(qualityworkbench.ReplayJobListFilter{
		BatchID: r.URL.Query().Get("batch_id"),
		Kind:    qualityworkbench.ReplayJobKind(r.URL.Query().Get("kind")),
		Status:  qualityworkbench.ReplayJobStatus(r.URL.Query().Get("status")),
		Limit:   size,
		Offset:  (page - 1) * size,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
		"page":  page,
		"size":  size,
	})
}

func (s *Server) handleAdminQualityWorkbenchGetReplay(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	job, ok := s.workbenchReplay().Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "replay job 不存在", Code: errs.CodeNotFound})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleAdminQualityWorkbenchCancelReplay(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	job, err := s.workbenchReplay().Cancel(id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleAdminQualityWorkbenchRunReplay(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	job, ok := s.workbenchReplay().Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "replay job 不存在", Code: errs.CodeNotFound})
		return
	}
	running, err := s.workbenchReplay().MarkRunning(id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	job = running
	result, runErr := qualityworkbench.ReplayRunner{
		Store:      s.qualityCandidateStore,
		EvalRunner: s.qualityEvalRunner,
	}.Run(r.Context(), job)
	status := qualityworkbench.ReplayJobSucceeded
	errText := ""
	if runErr != nil {
		status = qualityworkbench.ReplayJobFailed
		errText = runErr.Error()
	}
	finished, err := s.workbenchReplay().Finish(id, status, result, errText)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, finished)
}

func (s *Server) handleAdminQualityWorkbenchCreateBatchEval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode          string `json:"mode"`
		BaselineRunID string `json:"baseline_run_id"`
		CasesDir      string `json:"cases_dir"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	kind, err := qualityworkbench.ParseBatchEvalKind(body.Mode)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	items, _, err := s.listWorkbenchCandidates(r, 1, 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	run, err := s.workbenchBatchEval().Start(qualityworkbench.BatchEvalStart{
		BatchID:    "eval_batch_" + time.Now().UTC().Format("20060102T150405.000000000"),
		Kind:       kind,
		CasesDir:   body.CasesDir,
		EvalRunner: s.qualityEvalRunner,
		Candidates: items,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeInvalidInput})
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) handleAdminQualityWorkbenchListBatchEvals(w http.ResponseWriter, r *http.Request) {
	page, size := parsePagination(r)
	items := s.workbenchBatchEval().List(qualityworkbench.BatchEvalRunListFilter{
		BatchID: r.URL.Query().Get("batch_id"),
		Kind:    qualityworkbench.BatchEvalKind(r.URL.Query().Get("kind")),
		Status:  qualityworkbench.BatchEvalStatus(r.URL.Query().Get("status")),
		Limit:   size,
		Offset:  (page - 1) * size,
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items), "page": page, "size": size})
}

func (s *Server) handleAdminQualityWorkbenchGetBatchEval(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	run, ok := s.workbenchBatchEval().Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "batch eval 不存在", Code: errs.CodeNotFound})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleAdminQualityWorkbenchDashboardSnapshot(w http.ResponseWriter, r *http.Request) {
	items, clusters, _, ok := s.workbenchCandidatesAndClusters(w, r, 1, 100)
	if !ok {
		return
	}
	snapshot := qualityworkbench.BuildDashboardSnapshot(qualityworkbench.DashboardInput{
		Now:        time.Now(),
		Window:     7 * 24 * time.Hour,
		Clusters:   clusters,
		Candidates: items,
	})
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleAdminQualityWorkbenchDashboardSeries(w http.ResponseWriter, r *http.Request) {
	items, clusters, _, ok := s.workbenchCandidatesAndClusters(w, r, 1, 100)
	if !ok {
		return
	}
	series := qualityworkbench.BuildDashboardSeries(qualityworkbench.DashboardInput{
		Now:        time.Now(),
		Window:     7 * 24 * time.Hour,
		Clusters:   clusters,
		Candidates: items,
	}, 24*time.Hour)
	writeJSON(w, http.StatusOK, map[string]any{"items": series})
}

func (s *Server) handleAdminQualityWorkbenchGenerateReport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WeekStart string `json:"week_start"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	since := time.Now().AddDate(0, 0, -7)
	if body.WeekStart != "" {
		if parsed, err := time.Parse("2006-01-02", body.WeekStart); err == nil {
			since = parsed
		}
	}
	until := since.AddDate(0, 0, 7)
	items, clusters, _, ok := s.workbenchCandidatesAndClusters(w, r, 1, 100)
	if !ok {
		return
	}
	report := qualityworkbench.GenerateWeeklyReport(qualityworkbench.WeeklyReportInput{
		Since:      since,
		Until:      until,
		Clusters:   clusters,
		Candidates: items,
		EvalRuns:   s.workbenchBatchEval().List(qualityworkbench.BatchEvalRunListFilter{Limit: 100}),
	})
	id := "report_" + since.Format("20060102")
	record, err := s.workbenchReports().Save(qualityworkbench.WeeklyReportSave{
		ID:        id,
		WeekStart: since,
		Title:     "Quality Workbench Weekly Report",
		Report:    report,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         record.ID,
		"week_start": record.WeekStart.Format("2006-01-02"),
		"title":      record.Title,
		"summary":    record.Summary,
		"markdown":   record.Markdown,
		"created_at": record.CreatedAt,
	})
}

func (s *Server) handleAdminQualityWorkbenchListReports(w http.ResponseWriter, r *http.Request) {
	page, size := parsePagination(r)
	reports := s.workbenchReports().List(qualityworkbench.WeeklyReportListFilter{Limit: size, Offset: (page - 1) * size})
	items := make([]map[string]any, 0, len(reports))
	for _, report := range reports {
		items = append(items, map[string]any{
			"id":       report.ID,
			"title":    report.Title,
			"summary":  report.Summary,
			"markdown": report.Markdown,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items), "page": page, "size": size})
}

func (s *Server) handleAdminQualityWorkbenchGetReport(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	report, ok := s.workbenchReports().Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "report 不存在", Code: errs.CodeNotFound})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       report.ID,
		"title":    report.Title,
		"summary":  report.Summary,
		"markdown": report.Markdown,
	})
}

func (s *Server) handleAdminQualityWorkbenchDownloadReport(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	report, ok := s.workbenchReports().Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "report 不存在", Code: errs.CodeNotFound})
		return
	}
	filename := strings.NewReplacer(`\`, "_", `/`, "_", `"`, "_", "\n", "_", "\r", "_").Replace(report.ID)
	if filename == "" {
		filename = "quality-workbench-report"
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.md"`, filename))
	_, _ = w.Write([]byte(report.Markdown))
}

func (s *Server) workbenchReplay() qualityworkbench.ReplayJobStore {
	if s.workbenchReplayStore == nil {
		s.workbenchReplayStore = qualityworkbench.NewMemoryReplayJobStore(time.Now)
	}
	return s.workbenchReplayStore
}

func (s *Server) workbenchBatchEval() qualityworkbench.BatchEvalRunStore {
	if s.workbenchBatchEvalStore == nil {
		s.workbenchBatchEvalStore = qualityworkbench.NewMemoryBatchEvalRunStore(time.Now)
	}
	return s.workbenchBatchEvalStore
}

func (s *Server) workbenchReports() qualityworkbench.WeeklyReportStore {
	if s.workbenchReportStore == nil {
		s.workbenchReportStore = qualityworkbench.NewMemoryWeeklyReportStore(time.Now)
	}
	return s.workbenchReportStore
}

func (s *Server) workbenchGroupingRules() qualityworkbench.GroupingRuleStore {
	if s.workbenchGroupingRuleStore == nil {
		s.workbenchGroupingRuleStore = qualityworkbench.NewMemoryGroupingRuleStore(time.Now)
	}
	return s.workbenchGroupingRuleStore
}

func (s *Server) effectiveGroupingRules() []qualityworkbench.GroupingRule {
	return qualityworkbench.EffectiveGroupingRules(s.workbenchGroupingRules())
}

func (s *Server) workbenchCandidatesAndClusters(w http.ResponseWriter, r *http.Request, page, size int) ([]agentquality.CandidateRecord, []qualityworkbench.Cluster, int, bool) {
	items, total, err := s.listWorkbenchCandidates(r, page, size)
	if err != nil {
		if strings.Contains(err.Error(), "未启用") {
			writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
			return nil, nil, 0, false
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeInternal})
		return nil, nil, 0, false
	}
	clusters := qualityworkbench.AggregateClusters(s.effectiveGroupingRules(), items)
	return items, clusters, total, true
}

func (s *Server) listWorkbenchCandidates(r *http.Request, page, size int) ([]agentquality.CandidateRecord, int, error) {
	if s.qualityCandidateStore == nil {
		return nil, 0, fmt.Errorf("质量候选用例存储未启用")
	}
	filter := agentquality.CandidateFilter{
		Status: agentquality.CandidateStatus(r.URL.Query().Get("status")),
		Route:  r.URL.Query().Get("route"),
		Limit:  size,
		Offset: (page - 1) * size,
	}
	if filter.Status != "" {
		if err := agentquality.ValidateCandidateStatus(filter.Status); err != nil {
			return nil, 0, err
		}
	}
	return s.qualityCandidateStore.ListCandidates(r.Context(), filter)
}
