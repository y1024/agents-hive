package qualityworkbench

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

type BatchEvalKind string

const (
	BatchEvalKindManual            BatchEvalKind = "manual"
	BatchEvalKindReplay            BatchEvalKind = "replay"
	BatchEvalKindFull              BatchEvalKind = "full"
	BatchEvalKindIncremental       BatchEvalKind = "incremental"
	BatchEvalKindShadow            BatchEvalKind = "shadow"
	BatchEvalKindExternalBenchmark BatchEvalKind = "external_benchmark"
)

type BatchEvalStatus string

const (
	BatchEvalSucceeded BatchEvalStatus = "succeeded"
	BatchEvalFailed    BatchEvalStatus = "failed"
)

type BatchEvalRun struct {
	ID                string                          `json:"id"`
	BatchID           string                          `json:"batch_id"`
	Kind              BatchEvalKind                   `json:"kind"`
	SuiteType         string                          `json:"suite_type,omitempty"`
	DomainID          string                          `json:"domain_id,omitempty"`
	SourceKind        string                          `json:"source_kind,omitempty"`
	SourceName        string                          `json:"source_name,omitempty"`
	RunnerInfo        agentquality.RunnerInfo         `json:"runner_info,omitempty"`
	Status            BatchEvalStatus                 `json:"status"`
	Summary           BatchEvalSummary                `json:"summary"`
	Diff              BatchEvalDiff                   `json:"diff"`
	CaseResults       []CaseRunResult                 `json:"case_results"`
	GateMetrics       agentquality.GateMetrics        `json:"gate_metrics,omitempty"`
	JudgeVerdict      agentquality.JudgeVerdict       `json:"judge_verdict,omitempty"`
	ShadowMetrics     []ShadowEvalMetrics             `json:"shadow_metrics,omitempty"`
	ShadowResults     []agentquality.ShadowEvalResult `json:"shadow_results,omitempty"`
	DomainRegressions []DomainRegressionStatus        `json:"domain_regressions,omitempty"`
	CreatedAt         time.Time                       `json:"created_at"`
	UpdatedAt         time.Time                       `json:"updated_at"`
}

type BatchEvalSummary struct {
	Total      int      `json:"total"`
	Passed     int      `json:"passed"`
	Failed     int      `json:"failed"`
	Unknown    int      `json:"unknown"`
	GateFailed bool     `json:"gate_failed,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
}

type BatchEvalDiff struct {
	ChangedCandidateIDs []string `json:"changed_candidate_ids"`
	NewFailures         []string `json:"new_failures"`
	Recovered           []string `json:"recovered"`
}

type BatchEvalStart struct {
	BatchID               string
	Kind                  BatchEvalKind
	SuiteType             string
	CasesDir              string
	Context               context.Context
	DomainID              string
	SourceKind            string
	SourceName            string
	EvalRunner            agentquality.EvalRunner
	JudgeEvaluator        agentquality.JudgeEvaluator
	ShadowResults         []agentquality.ShadowEvalResult
	GateThresholds        agentquality.GateThresholds
	Candidates            []agentquality.CandidateRecord
	BaselineVerifyResults map[string]string
}

type BatchEvalRunListFilter struct {
	BatchID    string
	Kind       BatchEvalKind
	Status     BatchEvalStatus
	DomainID   string
	SourceKind string
	SourceName string
	Limit      int
	Offset     int
}

type BatchEvalRunStore interface {
	Start(input BatchEvalStart) (BatchEvalRun, error)
	Get(id string) (BatchEvalRun, bool)
	List(filter BatchEvalRunListFilter) []BatchEvalRun
}

type MemoryBatchEvalRunStore struct {
	mu   sync.RWMutex
	now  func() time.Time
	seq  int
	runs map[string]BatchEvalRun
}

func NewMemoryBatchEvalRunStore(now func() time.Time) *MemoryBatchEvalRunStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryBatchEvalRunStore{now: now, runs: map[string]BatchEvalRun{}}
}

func (s *MemoryBatchEvalRunStore) Start(input BatchEvalStart) (BatchEvalRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(input.BatchID) == "" {
		return BatchEvalRun{}, errors.New("batch_id is required")
	}
	if input.Kind == "" {
		return BatchEvalRun{}, errors.New("kind is required")
	}
	if err := ValidateBatchEvalKind(input.Kind); err != nil {
		return BatchEvalRun{}, err
	}
	s.seq++
	now := s.now()
	summary, diff, caseResults, runnerInfo, gateMetrics, judgeVerdict := summarizeBatchEvalWithGolden(input)
	shadowMetrics, domainRegressions := summarizeShadowEvidence(input, runnerInfo)
	status := batchEvalStatusFromSummary(summary)
	run := BatchEvalRun{
		ID:                fmt.Sprintf("eval_%06d", s.seq),
		BatchID:           input.BatchID,
		Kind:              input.Kind,
		SuiteType:         strings.TrimSpace(input.SuiteType),
		DomainID:          strings.TrimSpace(input.DomainID),
		SourceKind:        strings.TrimSpace(input.SourceKind),
		SourceName:        strings.TrimSpace(input.SourceName),
		RunnerInfo:        runnerInfo,
		Status:            status,
		Summary:           summary,
		Diff:              diff,
		CaseResults:       caseResults,
		GateMetrics:       gateMetrics,
		JudgeVerdict:      judgeVerdict,
		ShadowMetrics:     shadowMetrics,
		ShadowResults:     append([]agentquality.ShadowEvalResult(nil), input.ShadowResults...),
		DomainRegressions: domainRegressions,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	s.runs[run.ID] = run
	return cloneBatchEvalRun(run), nil
}

func ParseBatchEvalKind(raw string) (BatchEvalKind, error) {
	kind := BatchEvalKind(strings.ToLower(strings.TrimSpace(raw)))
	if kind == "" {
		return BatchEvalKindManual, nil
	}
	if err := ValidateBatchEvalKind(kind); err != nil {
		return "", err
	}
	return kind, nil
}

func ValidateBatchEvalKind(kind BatchEvalKind) error {
	switch kind {
	case BatchEvalKindManual, BatchEvalKindReplay, BatchEvalKindFull, BatchEvalKindIncremental, BatchEvalKindShadow, BatchEvalKindExternalBenchmark:
		return nil
	default:
		return fmt.Errorf("invalid batch eval kind %s", kind)
	}
}

// IsExternalBenchmark 判断 batch eval run 是否为外部 benchmark。
// 外部 benchmark 结果不应授权域上线。
func IsExternalBenchmark(run BatchEvalRun) bool {
	return run.SuiteType == "external_benchmark" || run.Kind == BatchEvalKindExternalBenchmark
}

// CanAuthorizeRollout 判断 batch eval run 是否可以授权域上线。
// 外部 benchmark 不能授权上线。
func CanAuthorizeRollout(run BatchEvalRun) bool {
	if IsExternalBenchmark(run) {
		return false
	}
	return agentquality.CanAuthorizeRolloutEvidence(run.RunnerInfo.EvidenceLevel)
}

func batchEvalStatusFromSummary(summary BatchEvalSummary) BatchEvalStatus {
	if summary.Total == 0 || summary.Failed > 0 || summary.Unknown > 0 || summary.GateFailed {
		return BatchEvalFailed
	}
	return BatchEvalSucceeded
}

func summarizeShadowEvidence(input BatchEvalStart, runnerInfo agentquality.RunnerInfo) ([]ShadowEvalMetrics, []DomainRegressionStatus) {
	if len(input.ShadowResults) == 0 {
		return nil, nil
	}
	byDomain := map[string][]agentquality.ShadowEvalResult{}
	for _, result := range input.ShadowResults {
		domainID := strings.TrimSpace(result.DomainID)
		if domainID == "" {
			domainID = "generic"
		}
		byDomain[domainID] = append(byDomain[domainID], result)
	}
	metrics := make([]ShadowEvalMetrics, 0, len(byDomain))
	regressions := make([]DomainRegressionStatus, 0, len(byDomain))
	evidenceLevel := string(runnerInfo.EvidenceLevel)
	if evidenceLevel == "" {
		evidenceLevel = string(agentquality.EvidenceProductionShadow)
	}
	for domainID, results := range byDomain {
		m := BuildShadowEvalMetrics(results)
		if m.DomainID == "" {
			m.DomainID = domainID
		}
		metrics = append(metrics, m)
		regressions = append(regressions, BuildDomainRegressionStatus(DomainRegressionInput{
			DomainID:       domainID,
			SemanticScore:  m.AvgSemanticScore,
			SafetyFailures: m.SafetyFailures,
			ActiveCases:    m.SampleCount,
			EvidenceLevel:  evidenceLevel,
			MinCases:       1,
			MinScore:       7,
		}))
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].DomainID < metrics[j].DomainID })
	sort.Slice(regressions, func(i, j int) bool { return regressions[i].DomainID < regressions[j].DomainID })
	return metrics, regressions
}

// isZeroGateThresholds 判断 GateThresholds 是否为零值。
// 由于 GateThresholds 包含 []string 字段，不能直接用 == 比较。
func isZeroGateThresholds(th agentquality.GateThresholds) bool {
	return th.FailureAttributionRateMin == 0 &&
		th.ToolChoiceAccuracyMin == 0 &&
		th.ReplayLocatableRateMin == 0 &&
		th.RegressionCandidateRateMin == 0 &&
		th.DelegationTraceCoverageRateMin == 0 &&
		th.SemanticScoreMin == 0 &&
		len(th.JudgeRequiredForDomains) == 0
}

func (s *MemoryBatchEvalRunStore) Get(id string) (BatchEvalRun, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[id]
	if !ok {
		return BatchEvalRun{}, false
	}
	return cloneBatchEvalRun(run), true
}

func (s *MemoryBatchEvalRunStore) List(filter BatchEvalRunListFilter) []BatchEvalRun {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]BatchEvalRun, 0, len(s.runs))
	for _, run := range s.runs {
		if filter.BatchID != "" && run.BatchID != filter.BatchID {
			continue
		}
		if filter.Kind != "" && run.Kind != filter.Kind {
			continue
		}
		if filter.Status != "" && run.Status != filter.Status {
			continue
		}
		if filter.DomainID != "" && run.DomainID != filter.DomainID {
			continue
		}
		if filter.SourceKind != "" && run.SourceKind != filter.SourceKind {
			continue
		}
		if filter.SourceName != "" && run.SourceName != filter.SourceName {
			continue
		}
		out = append(out, cloneBatchEvalRun(run))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return pageBatchEvalRuns(out, filter.Offset, filter.Limit)
}

func summarizeBatchEval(candidates []agentquality.CandidateRecord, baseline map[string]string) (BatchEvalSummary, BatchEvalDiff) {
	summary, diff, _, _, _, _ := summarizeBatchEvalWithGolden(BatchEvalStart{Candidates: candidates, BaselineVerifyResults: baseline})
	return summary, diff
}

func summarizeBatchEvalWithGolden(input BatchEvalStart) (BatchEvalSummary, BatchEvalDiff, []CaseRunResult, agentquality.RunnerInfo, agentquality.GateMetrics, agentquality.JudgeVerdict) {
	var runnerInfo agentquality.RunnerInfo
	if dr, ok := input.EvalRunner.(agentquality.DescribedEvalRunner); ok {
		runnerInfo = dr.Info()
	}
	summary := BatchEvalSummary{}
	diff := BatchEvalDiff{}
	caseResults := []CaseRunResult{}
	var gateMetrics agentquality.GateMetrics
	var judgeVerdict agentquality.JudgeVerdict
	if strings.TrimSpace(input.CasesDir) != "" {
		cases, err := agentquality.LoadCases(input.CasesDir)
		if err != nil {
			summary.Unknown++
			summary.Reasons = append(summary.Reasons, "golden cases load failed: "+err.Error())
		} else {
			summary.Total += len(cases)
			validCases := make([]agentquality.LoadedCase, 0, len(cases))
			for _, lc := range cases {
				if err := agentquality.ValidateCase(lc.Case); err != nil {
					summary.Failed++
					summary.Reasons = append(summary.Reasons, lc.Case.ID+" invalid golden case: "+err.Error())
					continue
				}
				validCases = append(validCases, lc)
			}
			if len(validCases) > 0 {
				runner := input.EvalRunner
				if runner == nil {
					summary.Unknown += len(validCases)
					summary.Reasons = append(summary.Reasons, "golden cases eval runner not configured")
					for _, lc := range validCases {
						caseResults = append(caseResults, CaseRunResult{CaseID: lc.Case.ID, Passed: false, Reason: "eval runner not configured"})
					}
				} else if gateInput, err := runEvalRunner(input.Context, runner, validCases); err != nil {
					summary.Unknown += len(validCases)
					summary.Reasons = append(summary.Reasons, "golden cases eval failed: "+err.Error())
					for _, lc := range validCases {
						caseResults = append(caseResults, CaseRunResult{CaseID: lc.Case.ID, Passed: false, Reason: "golden cases eval failed: " + err.Error()})
					}
				} else {
					gateInput.Cases = validCases
					thresholds := input.GateThresholds
					if isZeroGateThresholds(thresholds) {
						thresholds = agentquality.DefaultGateThresholds()
					}
					judgeReasons := applyJudgeEvaluator(input.Context, input.JudgeEvaluator, validCases, &gateInput)
					summary.Reasons = append(summary.Reasons, judgeReasons...)
					gateMetrics = agentquality.ComputeGateMetrics(gateInput)
					markJudgeMissing(&gateMetrics, validCases, gateInput.JudgeVerdicts, thresholds)
					judgeVerdict = aggregateJudgeVerdict(gateInput.JudgeVerdicts, gateMetrics)
					caseResults = append(caseResults, appendGoldenResults(&summary, validCases, gateInput, runnerInfo, gateMetrics)...)
					if err := agentquality.EvaluateGate(gateMetrics, thresholds); err != nil {
						summary.GateFailed = true
						summary.Reasons = append(summary.Reasons, "golden cases gate failed: "+err.Error())
					} else {
						summary.Reasons = append(summary.Reasons, "golden cases gate passed")
					}
				}
			}
		}
	}

	summary.Total += len(input.Candidates)
	if summary.Total == 0 {
		summary.Reasons = append(summary.Reasons, "no candidates")
		return summary, diff, caseResults, runnerInfo, gateMetrics, judgeVerdict
	}
	for _, c := range input.Candidates {
		result := normalizeVerifyResult(c.VerifyResult)
		switch result {
		case "passed":
			summary.Passed++
			caseResults = append(caseResults, CaseRunResult{CaseID: c.ID, Passed: true})
		case "failed":
			summary.Failed++
			summary.Reasons = append(summary.Reasons, c.ID+" verify_result failed")
			caseResults = append(caseResults, CaseRunResult{CaseID: c.ID, Passed: false, Reason: "verify_result failed"})
		default:
			summary.Unknown++
			summary.Reasons = append(summary.Reasons, c.ID+" has no verify_result")
			caseResults = append(caseResults, CaseRunResult{CaseID: c.ID, Passed: false, Reason: "unknown verify_result"})
		}
		if input.BaselineVerifyResults != nil {
			rawPrev, ok := input.BaselineVerifyResults[c.ID]
			if !ok {
				continue
			}
			prev := normalizeVerifyResult(rawPrev)
			if prev != result {
				diff.ChangedCandidateIDs = append(diff.ChangedCandidateIDs, c.ID)
				if result == "failed" {
					diff.NewFailures = append(diff.NewFailures, c.ID)
				}
				if prev == "failed" && result == "passed" {
					diff.Recovered = append(diff.Recovered, c.ID)
				}
			}
		}
	}
	return summary, diff, caseResults, runnerInfo, gateMetrics, judgeVerdict
}

func appendGoldenResults(summary *BatchEvalSummary, cases []agentquality.LoadedCase, gateInput agentquality.GateInput, runnerInfo agentquality.RunnerInfo, metrics agentquality.GateMetrics) []CaseRunResult {
	byID := make(map[string]agentquality.Result, len(gateInput.Results))
	for _, result := range gateInput.Results {
		byID[result.CaseID] = result
	}
	caseResults := make([]CaseRunResult, 0, len(cases))
	for _, lc := range cases {
		result, ok := byID[lc.Case.ID]
		if !ok {
			summary.Unknown++
			summary.Reasons = append(summary.Reasons, lc.Case.ID+" golden case has no eval result")
			caseResults = append(caseResults, buildCaseRunResult(lc, agentquality.Result{CaseID: lc.Case.ID, Reason: "golden case has no eval result"}, runnerInfo, metrics, gateInput))
			continue
		}
		if result.Passed {
			summary.Passed++
			caseResults = append(caseResults, buildCaseRunResult(lc, result, runnerInfo, metrics, gateInput))
			continue
		}
		summary.Failed++
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = "golden case failed"
		}
		summary.Reasons = append(summary.Reasons, lc.Case.ID+" "+reason)
		result.Reason = reason
		caseResults = append(caseResults, buildCaseRunResult(lc, result, runnerInfo, metrics, gateInput))
	}
	return caseResults
}

func applyJudgeEvaluator(ctx context.Context, judge agentquality.JudgeEvaluator, cases []agentquality.LoadedCase, gateInput *agentquality.GateInput) []string {
	if judge == nil || gateInput == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if gateInput.JudgeVerdicts == nil {
		gateInput.JudgeVerdicts = make(map[string]agentquality.JudgeVerdict, len(cases))
	}
	var reasons []string
	for _, lc := range cases {
		caseID := lc.Case.ID
		verdict, err := judge.Judge(ctx, agentquality.RedactJudgeInput(judgeInputForCase(lc, *gateInput)))
		if err != nil {
			reason := "judge_error: " + err.Error()
			reasons = append(reasons, caseID+" "+reason)
			markGateResultFailed(gateInput, caseID, reason)
			continue
		}
		if err := agentquality.ValidateJudgeVerdict(verdict); err != nil {
			reason := "judge_error: " + err.Error()
			reasons = append(reasons, caseID+" "+reason)
			markGateResultFailed(gateInput, caseID, reason)
			continue
		}
		gateInput.JudgeVerdicts[caseID] = verdict
		if !verdict.Passed {
			reason := "judge_failed: " + strings.TrimSpace(verdict.Rationale)
			if strings.TrimSpace(verdict.Rationale) == "" {
				reason = "judge_failed"
			}
			reasons = append(reasons, caseID+" "+reason)
			markGateResultFailed(gateInput, caseID, reason)
		}
	}
	return reasons
}

func judgeInputForCase(lc agentquality.LoadedCase, gateInput agentquality.GateInput) agentquality.JudgeInput {
	caseID := lc.Case.ID
	events := gateInput.EventsByCase[caseID]
	return agentquality.JudgeInput{
		CaseID:         caseID,
		DomainID:       firstNonEmpty(lc.Case.DomainID, firstEventDomain(events)),
		UserInput:      lc.Case.Input,
		FinalOutput:    gateInput.FinalOutputByCaseID[caseID],
		ExpectedAnswer: lc.Case.ExpectedAnswer,
		Rubric:         append([]agentquality.RubricCriterion(nil), lc.Case.JudgeRubric...),
		TraceSummary: agentquality.TraceSummary{
			ToolsCalled: append([]string(nil), gateInput.ToolActualByCaseID[caseID]...),
			EventCount:  len(events),
			FinalStatus: finalStatusForJudge(lc.Case, events),
			DomainID:    firstNonEmpty(lc.Case.DomainID, firstEventDomain(events)),
		},
		ToolAssertions: toolAssertionsForJudge(lc.Case, gateInput.ToolActualByCaseID[caseID]),
	}
}

func firstEventDomain(events []agentquality.Event) string {
	for _, ev := range events {
		if strings.TrimSpace(ev.DomainID) != "" {
			return strings.TrimSpace(ev.DomainID)
		}
	}
	return ""
}

func finalStatusForJudge(c agentquality.Case, events []agentquality.Event) agentquality.FinalStatus {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].FinalStatus != "" {
			return events[i].FinalStatus
		}
	}
	return c.ExpectedStatus
}

func toolAssertionsForJudge(c agentquality.Case, actual []string) []agentquality.ToolAssertion {
	actualSet := make(map[string]bool, len(actual))
	for _, tool := range actual {
		actualSet[tool] = true
	}
	assertions := make([]agentquality.ToolAssertion, 0, len(c.ExpectedTools)+len(c.Assertions))
	for _, tool := range c.ExpectedTools {
		if strings.TrimSpace(tool) == "" {
			continue
		}
		assertions = append(assertions, agentquality.ToolAssertion{ToolName: tool, Expected: true, Actual: actualSet[tool]})
	}
	for _, assertion := range c.Assertions {
		switch assertion.Type {
		case agentquality.AssertionToolCalled:
			assertions = append(assertions, agentquality.ToolAssertion{ToolName: assertion.Target, Expected: true, Actual: actualSet[assertion.Target]})
		case agentquality.AssertionToolNotCalled:
			assertions = append(assertions, agentquality.ToolAssertion{ToolName: assertion.Target, Expected: false, Actual: actualSet[assertion.Target]})
		}
	}
	return assertions
}

func markGateResultFailed(gateInput *agentquality.GateInput, caseID, reason string) {
	for i := range gateInput.Results {
		if gateInput.Results[i].CaseID == caseID {
			gateInput.Results[i].Passed = false
			gateInput.Results[i].Reason = reason
			return
		}
	}
	gateInput.Results = append(gateInput.Results, agentquality.Result{CaseID: caseID, Passed: false, Reason: reason})
}

func markJudgeMissing(metrics *agentquality.GateMetrics, cases []agentquality.LoadedCase, verdicts map[string]agentquality.JudgeVerdict, thresholds agentquality.GateThresholds) {
	if metrics == nil || len(thresholds.JudgeRequiredForDomains) == 0 {
		return
	}
	required := make(map[string]bool, len(thresholds.JudgeRequiredForDomains))
	for _, domain := range thresholds.JudgeRequiredForDomains {
		if strings.TrimSpace(domain) != "" {
			required[strings.TrimSpace(domain)] = true
		}
	}
	for _, lc := range cases {
		domainID := strings.TrimSpace(lc.Case.DomainID)
		if domainID == "" || !required[domainID] {
			continue
		}
		if _, ok := verdicts[lc.Case.ID]; ok {
			continue
		}
		metrics.JudgeMissing = true
		metrics.JudgeRequiredDomain = domainID
		return
	}
}

func aggregateJudgeVerdict(verdicts map[string]agentquality.JudgeVerdict, metrics agentquality.GateMetrics) agentquality.JudgeVerdict {
	if len(verdicts) == 0 {
		if metrics.JudgeMissing {
			return agentquality.JudgeVerdict{
				Score:          0,
				Passed:         false,
				FailureType:    agentquality.FailureRuntime,
				Rationale:      "judge verdict missing for required domain " + metrics.JudgeRequiredDomain,
				Evidence:       []string{"judge_missing"},
				ShouldOptimize: true,
			}
		}
		return agentquality.JudgeVerdict{}
	}
	totalScore := 0
	passed := true
	failureType := agentquality.FailureNone
	evidence := make([]string, 0, len(verdicts))
	for caseID, verdict := range verdicts {
		totalScore += verdict.Score
		if !verdict.Passed {
			passed = false
		}
		if failureType == agentquality.FailureNone && verdict.FailureType != "" && verdict.FailureType != agentquality.FailureNone {
			failureType = verdict.FailureType
		}
		evidence = append(evidence, caseID)
	}
	sort.Strings(evidence)
	return agentquality.JudgeVerdict{
		Score:          int(float64(totalScore)/float64(len(verdicts)) + 0.5),
		Passed:         passed,
		FailureType:    failureType,
		Rationale:      fmt.Sprintf("aggregated %d judge verdicts", len(verdicts)),
		Evidence:       evidence,
		ShouldOptimize: !passed,
	}
}

func buildCaseRunResult(lc agentquality.LoadedCase, result agentquality.Result, runnerInfo agentquality.RunnerInfo, metrics agentquality.GateMetrics, gateInput agentquality.GateInput) CaseRunResult {
	return CaseRunResult{
		CaseID:        lc.Case.ID,
		Passed:        result.Passed,
		Reason:        result.Reason,
		RunnerInfo:    runnerInfo,
		EvidenceLevel: runnerInfo.EvidenceLevel,
		JudgeVerdict:  gateInput.JudgeVerdicts[lc.Case.ID],
		GateMetrics:   metrics,
		ReplayRef:     gateInput.ReplayRefByCaseID[lc.Case.ID],
		DomainID:      lc.Case.DomainID,
		SourceKind:    lc.Case.SourceKind,
		SourceName:    lc.Case.SourceName,
	}
}

func normalizeVerifyResult(result string) string {
	switch strings.ToLower(strings.TrimSpace(result)) {
	case "pass", "passed", "success", "succeeded", "ok":
		return "passed"
	case "fail", "failed", "failure", "regressed":
		return "failed"
	default:
		return "unknown"
	}
}

func cloneBatchEvalRun(run BatchEvalRun) BatchEvalRun {
	run.Summary.Reasons = append([]string(nil), run.Summary.Reasons...)
	run.Diff.ChangedCandidateIDs = append([]string(nil), run.Diff.ChangedCandidateIDs...)
	run.Diff.NewFailures = append([]string(nil), run.Diff.NewFailures...)
	run.Diff.Recovered = append([]string(nil), run.Diff.Recovered...)
	run.CaseResults = append([]CaseRunResult(nil), run.CaseResults...)
	run.ShadowMetrics = append([]ShadowEvalMetrics(nil), run.ShadowMetrics...)
	run.ShadowResults = append([]agentquality.ShadowEvalResult(nil), run.ShadowResults...)
	run.DomainRegressions = append([]DomainRegressionStatus(nil), run.DomainRegressions...)
	return run
}

func pageBatchEvalRuns(runs []BatchEvalRun, offset, limit int) []BatchEvalRun {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(runs) {
		return []BatchEvalRun{}
	}
	runs = runs[offset:]
	if limit <= 0 || limit >= len(runs) {
		return runs
	}
	return runs[:limit]
}
