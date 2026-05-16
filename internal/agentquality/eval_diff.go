package agentquality

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type EvalDiffStatus string

const (
	EvalDiffPending  EvalDiffStatus = "pending"
	EvalDiffRunning  EvalDiffStatus = "eval_diff_running"
	EvalDiffDone     EvalDiffStatus = "eval_diff_done"
	EvalDiffApproved EvalDiffStatus = "approved"
	EvalDiffRejected EvalDiffStatus = "rejected"
)

type EvalRun struct {
	ID         string           `json:"id"`
	RunnerInfo RunnerInfo       `json:"runner_info,omitempty"`
	Results    []EvalCaseResult `json:"results"`
	CreatedAt  time.Time        `json:"created_at,omitempty"`
}

type EvalCaseResult struct {
	CaseID         string      `json:"case_id"`
	Passed         bool        `json:"passed"`
	CostUSD        float64     `json:"cost_usd"`
	LatencyMS      float64     `json:"latency_ms"`
	FailureType    FailureType `json:"failure_type,omitempty"`
	Prompt         PromptRef   `json:"prompt,omitempty"`
	ExpectedTools  []string    `json:"expected_tools,omitempty"`
	ActualTool     string      `json:"actual_tool,omitempty"`
	ExpectedSkills []string    `json:"expected_skills,omitempty"`
	Reason         string      `json:"reason,omitempty"`
}

type EvalRunSummary struct {
	CaseCount        int     `json:"case_count"`
	SuccessCount     int     `json:"success_count"`
	SuccessRate      float64 `json:"success_rate"`
	AverageCostUSD   float64 `json:"average_cost_usd"`
	AverageLatencyMS float64 `json:"average_latency_ms"`
}

type EvalCaseDiff struct {
	CaseID          string      `json:"case_id"`
	BaselinePassed  bool        `json:"baseline_passed"`
	TreatmentPassed bool        `json:"treatment_passed"`
	CostDeltaUSD    float64     `json:"cost_delta_usd"`
	LatencyDeltaMS  float64     `json:"latency_delta_ms"`
	FailureType     FailureType `json:"failure_type,omitempty"`
	Prompt          PromptRef   `json:"prompt,omitempty"`
	ExpectedTools   []string    `json:"expected_tools,omitempty"`
	ActualTool      string      `json:"actual_tool,omitempty"`
	ExpectedSkills  []string    `json:"expected_skills,omitempty"`
	Reason          string      `json:"reason,omitempty"`
}

type EvalDiff struct {
	ID                    string         `json:"id"`
	Status                EvalDiffStatus `json:"status"`
	BaselineRunID         string         `json:"baseline_run_id"`
	TreatmentRunID        string         `json:"treatment_run_id"`
	BaselineRunnerInfo    RunnerInfo     `json:"baseline_runner_info,omitempty"`
	TreatmentRunnerInfo   RunnerInfo     `json:"treatment_runner_info,omitempty"`
	Baseline              EvalRunSummary `json:"baseline"`
	Treatment             EvalRunSummary `json:"treatment"`
	SuccessRateDelta      float64        `json:"success_rate_delta"`
	AverageCostDeltaUSD   float64        `json:"average_cost_delta_usd"`
	AverageLatencyDeltaMS float64        `json:"average_latency_delta_ms"`
	SuccessPValue         float64        `json:"success_p_value"`
	CaseDiffs             []EvalCaseDiff `json:"case_diffs"`
	CreatedAt             time.Time      `json:"created_at,omitempty"`
	UpdatedAt             time.Time      `json:"updated_at,omitempty"`
	ApprovedBy            string         `json:"approved_by,omitempty"`
	RejectedBy            string         `json:"rejected_by,omitempty"`
}

func ComputeEvalDiff(baseline, treatment EvalRun) (EvalDiff, error) {
	if strings.TrimSpace(baseline.ID) == "" {
		return EvalDiff{}, fmt.Errorf("baseline run id is required")
	}
	if strings.TrimSpace(treatment.ID) == "" {
		return EvalDiff{}, fmt.Errorf("treatment run id is required")
	}
	baseByCase := indexEvalResults(baseline.Results)
	treatByCase := indexEvalResults(treatment.Results)
	if len(baseByCase) == 0 || len(treatByCase) == 0 {
		return EvalDiff{}, fmt.Errorf("baseline and treatment results are required")
	}

	caseIDs := intersectCaseIDs(baseByCase, treatByCase)
	if len(caseIDs) == 0 {
		return EvalDiff{}, fmt.Errorf("baseline and treatment share no case ids")
	}

	baseResults := make([]EvalCaseResult, 0, len(caseIDs))
	treatResults := make([]EvalCaseResult, 0, len(caseIDs))
	caseDiffs := make([]EvalCaseDiff, 0, len(caseIDs))
	for _, caseID := range caseIDs {
		base := baseByCase[caseID]
		treat := treatByCase[caseID]
		baseResults = append(baseResults, base)
		treatResults = append(treatResults, treat)
		caseDiffs = append(caseDiffs, EvalCaseDiff{
			CaseID:          caseID,
			BaselinePassed:  base.Passed,
			TreatmentPassed: treat.Passed,
			CostDeltaUSD:    treat.CostUSD - base.CostUSD,
			LatencyDeltaMS:  treat.LatencyMS - base.LatencyMS,
			FailureType:     firstFailureType(treat.FailureType, base.FailureType),
			Prompt:          firstPromptRef(treat.Prompt, base.Prompt),
			ExpectedTools:   firstStringSlice(treat.ExpectedTools, base.ExpectedTools),
			ActualTool:      firstNonEmpty(treat.ActualTool, base.ActualTool),
			ExpectedSkills:  firstStringSlice(treat.ExpectedSkills, base.ExpectedSkills),
			Reason:          firstNonEmpty(treat.Reason, base.Reason),
		})
	}

	baseSummary := summarizeEvalRun(baseResults)
	treatSummary := summarizeEvalRun(treatResults)
	now := time.Now()
	return EvalDiff{
		ID:                    evalDiffID(baseline.ID, treatment.ID),
		Status:                EvalDiffDone,
		BaselineRunID:         baseline.ID,
		TreatmentRunID:        treatment.ID,
		BaselineRunnerInfo:    baseline.RunnerInfo,
		TreatmentRunnerInfo:   treatment.RunnerInfo,
		Baseline:              baseSummary,
		Treatment:             treatSummary,
		SuccessRateDelta:      treatSummary.SuccessRate - baseSummary.SuccessRate,
		AverageCostDeltaUSD:   treatSummary.AverageCostUSD - baseSummary.AverageCostUSD,
		AverageLatencyDeltaMS: treatSummary.AverageLatencyMS - baseSummary.AverageLatencyMS,
		SuccessPValue:         twoSidedProportionPValue(baseSummary.SuccessCount, baseSummary.CaseCount, treatSummary.SuccessCount, treatSummary.CaseCount),
		CaseDiffs:             caseDiffs,
		CreatedAt:             now,
		UpdatedAt:             now,
	}, nil
}

func (d EvalDiff) Transition(to EvalDiffStatus) (EvalDiff, error) {
	from := d.Status
	if from == "" {
		from = EvalDiffPending
	}
	if !validEvalDiffTransition(from, to) {
		return d, fmt.Errorf("eval diff transition %s -> %s is not allowed", from, to)
	}
	d.Status = to
	d.UpdatedAt = time.Now()
	return d, nil
}

func RenderABReportMarkdown(diff EvalDiff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Offline Eval Diff Report\n\n")
	fmt.Fprintf(&b, "BaselineRunID: %s\n", diff.BaselineRunID)
	fmt.Fprintf(&b, "TreatmentRunID: %s\n", diff.TreatmentRunID)
	fmt.Fprintf(&b, "Status: %s\n", diff.Status)
	if !diff.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "CreatedAt: %s\n", diff.CreatedAt.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "\n| Metric | Baseline | Treatment | Delta |\n")
	fmt.Fprintf(&b, "| --- | ---: | ---: | ---: |\n")
	fmt.Fprintf(&b, "| Success rate | %.4f | %.4f | %.4f |\n", diff.Baseline.SuccessRate, diff.Treatment.SuccessRate, diff.SuccessRateDelta)
	fmt.Fprintf(&b, "| Average cost USD | %.6f | %.6f | %.6f |\n", diff.Baseline.AverageCostUSD, diff.Treatment.AverageCostUSD, diff.AverageCostDeltaUSD)
	fmt.Fprintf(&b, "| Average latency ms | %.2f | %.2f | %.2f |\n", diff.Baseline.AverageLatencyMS, diff.Treatment.AverageLatencyMS, diff.AverageLatencyDeltaMS)
	fmt.Fprintf(&b, "\nSuccess p-value: %.6f\n", diff.SuccessPValue)
	return b.String()
}

func indexEvalResults(results []EvalCaseResult) map[string]EvalCaseResult {
	out := make(map[string]EvalCaseResult, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.CaseID) == "" {
			continue
		}
		out[result.CaseID] = result
	}
	return out
}

func intersectCaseIDs(a, b map[string]EvalCaseResult) []string {
	out := make([]string, 0, len(a))
	for caseID := range a {
		if _, ok := b[caseID]; ok {
			out = append(out, caseID)
		}
	}
	sort.Strings(out)
	return out
}

func summarizeEvalRun(results []EvalCaseResult) EvalRunSummary {
	var s EvalRunSummary
	s.CaseCount = len(results)
	if s.CaseCount == 0 {
		return s
	}
	for _, result := range results {
		if result.Passed {
			s.SuccessCount++
		}
		s.AverageCostUSD += result.CostUSD
		s.AverageLatencyMS += result.LatencyMS
	}
	s.SuccessRate = float64(s.SuccessCount) / float64(s.CaseCount)
	s.AverageCostUSD /= float64(s.CaseCount)
	s.AverageLatencyMS /= float64(s.CaseCount)
	return s
}

func twoSidedProportionPValue(successA, nA, successB, nB int) float64 {
	if nA == 0 || nB == 0 {
		return 1
	}
	pA := float64(successA) / float64(nA)
	pB := float64(successB) / float64(nB)
	pooled := float64(successA+successB) / float64(nA+nB)
	se := math.Sqrt(pooled * (1 - pooled) * (1/float64(nA) + 1/float64(nB)))
	if se == 0 {
		if pA == pB {
			return 1
		}
		return 0
	}
	z := math.Abs(pB-pA) / se
	p := math.Erfc(z / math.Sqrt2)
	if math.IsNaN(p) || p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

func validEvalDiffTransition(from, to EvalDiffStatus) bool {
	switch from {
	case EvalDiffPending:
		return to == EvalDiffRunning
	case EvalDiffRunning:
		return to == EvalDiffDone
	case EvalDiffDone:
		return to == EvalDiffApproved || to == EvalDiffRejected
	case EvalDiffApproved, EvalDiffRejected:
		return false
	default:
		return false
	}
}

func evalDiffID(baselineRunID, treatmentRunID string) string {
	return "evaldiff_" + strings.NewReplacer(" ", "_", "/", "_", ":", "_").Replace(baselineRunID+"_"+treatmentRunID)
}

func firstPromptRef(vals ...PromptRef) PromptRef {
	for _, v := range vals {
		if v.Key != "" || v.Version != "" || v.Source != "" || v.Language != "" {
			return v
		}
	}
	return PromptRef{}
}

func firstStringSlice(vals ...[]string) []string {
	for _, v := range vals {
		if len(v) > 0 {
			return append([]string(nil), v...)
		}
	}
	return nil
}
