package qualityworkbench

import (
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
	BatchEvalKindManual      BatchEvalKind = "manual"
	BatchEvalKindReplay      BatchEvalKind = "replay"
	BatchEvalKindFull        BatchEvalKind = "full"
	BatchEvalKindIncremental BatchEvalKind = "incremental"
	BatchEvalKindShadow      BatchEvalKind = "shadow"
)

type BatchEvalStatus string

const (
	BatchEvalSucceeded BatchEvalStatus = "succeeded"
	BatchEvalFailed    BatchEvalStatus = "failed"
)

type BatchEvalRun struct {
	ID          string           `json:"id"`
	BatchID     string           `json:"batch_id"`
	Kind        BatchEvalKind    `json:"kind"`
	Status      BatchEvalStatus  `json:"status"`
	Summary     BatchEvalSummary `json:"summary"`
	Diff        BatchEvalDiff    `json:"diff"`
	CaseResults []CaseRunResult  `json:"case_results"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

type BatchEvalSummary struct {
	Total   int      `json:"total"`
	Passed  int      `json:"passed"`
	Failed  int      `json:"failed"`
	Unknown int      `json:"unknown"`
	Reasons []string `json:"reasons,omitempty"`
}

type BatchEvalDiff struct {
	ChangedCandidateIDs []string `json:"changed_candidate_ids"`
	NewFailures         []string `json:"new_failures"`
	Recovered           []string `json:"recovered"`
}

type BatchEvalStart struct {
	BatchID               string
	Kind                  BatchEvalKind
	CasesDir              string
	EvalRunner            agentquality.EvalRunner
	GateThresholds        agentquality.GateThresholds
	Candidates            []agentquality.CandidateRecord
	BaselineVerifyResults map[string]string
}

type BatchEvalRunListFilter struct {
	BatchID string
	Kind    BatchEvalKind
	Status  BatchEvalStatus
	Limit   int
	Offset  int
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
	summary, diff, caseResults := summarizeBatchEvalWithGolden(input)
	status := BatchEvalSucceeded
	if summary.Total == 0 || summary.Failed > 0 || summary.Unknown > 0 {
		status = BatchEvalFailed
	}
	run := BatchEvalRun{
		ID:          fmt.Sprintf("eval_%06d", s.seq),
		BatchID:     input.BatchID,
		Kind:        input.Kind,
		Status:      status,
		Summary:     summary,
		Diff:        diff,
		CaseResults: caseResults,
		CreatedAt:   now,
		UpdatedAt:   now,
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
	case BatchEvalKindManual, BatchEvalKindReplay, BatchEvalKindFull, BatchEvalKindIncremental, BatchEvalKindShadow:
		return nil
	default:
		return fmt.Errorf("invalid batch eval kind %s", kind)
	}
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
	summary, diff, _ := summarizeBatchEvalWithGolden(BatchEvalStart{Candidates: candidates, BaselineVerifyResults: baseline})
	return summary, diff
}

func summarizeBatchEvalWithGolden(input BatchEvalStart) (BatchEvalSummary, BatchEvalDiff, []CaseRunResult) {
	summary := BatchEvalSummary{}
	diff := BatchEvalDiff{}
	caseResults := []CaseRunResult{}
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
				} else if gateInput, err := runner.Run(validCases); err != nil {
					summary.Unknown += len(validCases)
					summary.Reasons = append(summary.Reasons, "golden cases eval failed: "+err.Error())
					for _, lc := range validCases {
						caseResults = append(caseResults, CaseRunResult{CaseID: lc.Case.ID, Passed: false, Reason: "golden cases eval failed: " + err.Error()})
					}
				} else {
					gateInput.Cases = validCases
					caseResults = append(caseResults, appendGoldenResults(&summary, validCases, gateInput.Results)...)
					thresholds := input.GateThresholds
					if thresholds == (agentquality.GateThresholds{}) {
						thresholds = agentquality.DefaultGateThresholds()
					}
					metrics := agentquality.ComputeGateMetrics(gateInput)
					if err := agentquality.EvaluateGate(metrics, thresholds); err != nil {
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
		return summary, diff, caseResults
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
	return summary, diff, caseResults
}

func appendGoldenResults(summary *BatchEvalSummary, cases []agentquality.LoadedCase, results []agentquality.Result) []CaseRunResult {
	byID := make(map[string]agentquality.Result, len(results))
	for _, result := range results {
		byID[result.CaseID] = result
	}
	caseResults := make([]CaseRunResult, 0, len(cases))
	for _, lc := range cases {
		result, ok := byID[lc.Case.ID]
		if !ok {
			summary.Unknown++
			summary.Reasons = append(summary.Reasons, lc.Case.ID+" golden case has no eval result")
			caseResults = append(caseResults, CaseRunResult{CaseID: lc.Case.ID, Passed: false, Reason: "golden case has no eval result"})
			continue
		}
		if result.Passed {
			summary.Passed++
			caseResults = append(caseResults, CaseRunResult{CaseID: lc.Case.ID, Passed: true})
			continue
		}
		summary.Failed++
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = "golden case failed"
		}
		summary.Reasons = append(summary.Reasons, lc.Case.ID+" "+reason)
		caseResults = append(caseResults, CaseRunResult{CaseID: lc.Case.ID, Passed: false, Reason: reason})
	}
	return caseResults
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
