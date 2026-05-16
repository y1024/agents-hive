package qualityworkbench

import (
	"sort"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

type CaseRunResult struct {
	CaseID        string                           `json:"case_id"`
	Passed        bool                             `json:"passed"`
	Reason        string                           `json:"reason,omitempty"`
	RunnerInfo    agentquality.RunnerInfo          `json:"runner_info,omitempty"`
	EvidenceLevel agentquality.RunnerEvidenceLevel `json:"evidence_level,omitempty"`
	JudgeVerdict  agentquality.JudgeVerdict        `json:"judge_verdict,omitempty"`
	GateMetrics   agentquality.GateMetrics         `json:"gate_metrics,omitempty"`
	TraceRef      string                           `json:"trace_ref,omitempty"`
	ReplayRef     string                           `json:"replay_ref,omitempty"`
	DomainID      string                           `json:"domain_id,omitempty"`
	SourceKind    string                           `json:"source_kind,omitempty"`
	SourceName    string                           `json:"source_name,omitempty"`
}

type VersionMatrixInput struct {
	BaselineRunID  string          `json:"baseline_run_id,omitempty"`
	TreatmentRunID string          `json:"treatment_run_id,omitempty"`
	Baseline       []CaseRunResult `json:"baseline"`
	Treatment      []CaseRunResult `json:"treatment"`
}

type CaseVersionDiff struct {
	CaseID           string `json:"case_id"`
	BaselinePresent  bool   `json:"baseline_present"`
	TreatmentPresent bool   `json:"treatment_present"`
	BaselinePassed   bool   `json:"baseline_passed"`
	TreatmentPassed  bool   `json:"treatment_passed"`
	BaselineReason   string `json:"baseline_reason,omitempty"`
	TreatmentReason  string `json:"treatment_reason,omitempty"`
	Regressed        bool   `json:"regressed"`
	Recovered        bool   `json:"recovered"`
	NewFailure       bool   `json:"new_failure"`
}

type VersionMatrixDiff struct {
	BaselineRunID     string                     `json:"baseline_run_id,omitempty"`
	TreatmentRunID    string                     `json:"treatment_run_id,omitempty"`
	Cases             map[string]CaseVersionDiff `json:"cases"`
	RegressedCaseIDs  []string                   `json:"regressed_case_ids"`
	RecoveredCaseIDs  []string                   `json:"recovered_case_ids"`
	NewFailureCaseIDs []string                   `json:"new_failure_case_ids"`
}

func CompareVersionMatrix(input VersionMatrixInput) VersionMatrixDiff {
	base := indexCaseRunResults(input.Baseline)
	treat := indexCaseRunResults(input.Treatment)
	ids := map[string]struct{}{}
	for id := range base {
		ids[id] = struct{}{}
	}
	for id := range treat {
		ids[id] = struct{}{}
	}

	out := VersionMatrixDiff{
		BaselineRunID:  input.BaselineRunID,
		TreatmentRunID: input.TreatmentRunID,
		Cases:          map[string]CaseVersionDiff{},
	}
	for id := range ids {
		b, bOK := base[id]
		t, tOK := treat[id]
		diff := CaseVersionDiff{
			CaseID:           id,
			BaselinePresent:  bOK,
			TreatmentPresent: tOK,
			BaselinePassed:   b.Passed,
			TreatmentPassed:  t.Passed,
			BaselineReason:   b.Reason,
			TreatmentReason:  t.Reason,
		}
		diff.Regressed = bOK && tOK && b.Passed && !t.Passed
		diff.Recovered = bOK && tOK && !b.Passed && t.Passed
		diff.NewFailure = !bOK && tOK && !t.Passed
		out.Cases[id] = diff
		if diff.Regressed {
			out.RegressedCaseIDs = append(out.RegressedCaseIDs, id)
		}
		if diff.Recovered {
			out.RecoveredCaseIDs = append(out.RecoveredCaseIDs, id)
		}
		if diff.NewFailure {
			out.NewFailureCaseIDs = append(out.NewFailureCaseIDs, id)
		}
	}
	sort.Strings(out.RegressedCaseIDs)
	sort.Strings(out.RecoveredCaseIDs)
	sort.Strings(out.NewFailureCaseIDs)
	return out
}

func indexCaseRunResults(results []CaseRunResult) map[string]CaseRunResult {
	out := make(map[string]CaseRunResult, len(results))
	for _, result := range results {
		if result.CaseID == "" {
			continue
		}
		out[result.CaseID] = result
	}
	return out
}
