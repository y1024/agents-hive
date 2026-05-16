package qualityworkbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

func TestBatchEvalRunStore_StartBuildsFailedSummaryForUnknownResults(t *testing.T) {
	now := time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)
	store := NewMemoryBatchEvalRunStore(func() time.Time { return now })

	run, err := store.Start(BatchEvalStart{
		BatchID: "batch-1",
		Kind:    BatchEvalKindReplay,
		Candidates: []agentquality.CandidateRecord{
			qualityWorkbenchCandidate("candidate-1", agentquality.CandidatePromoted, agentquality.FailureTool, "", now),
			qualityWorkbenchCandidate("candidate-2", agentquality.CandidatePromotedVerified, agentquality.FailureTool, "passed", now),
		},
		BaselineVerifyResults: map[string]string{"candidate-1": "passed"},
	})

	require.NoError(t, err)
	assert.Equal(t, BatchEvalFailed, run.Status)
	assert.Equal(t, 2, run.Summary.Total)
	assert.Equal(t, 1, run.Summary.Passed)
	assert.Equal(t, 1, run.Summary.Unknown)
	assert.Equal(t, 0, run.Summary.Failed)
	assert.Contains(t, run.Summary.Reasons, "candidate-1 has no verify_result")
	assert.Equal(t, []string{"candidate-1"}, run.Diff.ChangedCandidateIDs)
	require.Len(t, run.CaseResults, 2)
	assert.Equal(t, CaseRunResult{CaseID: "candidate-1", Passed: false, Reason: "unknown verify_result"}, run.CaseResults[0])
	assert.Equal(t, CaseRunResult{CaseID: "candidate-2", Passed: true}, run.CaseResults[1])
}

func TestBatchEvalRunStore_StartFailsEmptyInputRatherThanFakeSuccess(t *testing.T) {
	store := NewMemoryBatchEvalRunStore(func() time.Time { return time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC) })

	run, err := store.Start(BatchEvalStart{BatchID: "batch-empty", Kind: BatchEvalKindManual})

	require.NoError(t, err)
	assert.Equal(t, BatchEvalFailed, run.Status)
	assert.Equal(t, 0, run.Summary.Total)
	assert.Contains(t, run.Summary.Reasons, "no candidates")
}

func TestBatchEvalRunStore_StartCasesDirWithoutRunnerDoesNotFakeSuccess(t *testing.T) {
	now := time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)
	store := NewMemoryBatchEvalRunStore(func() time.Time { return now })
	casesDir := t.TempDir()
	writeGoldenCase(t, casesDir, agentquality.Case{
		ID:             "case-required-pass",
		Name:           "required pass",
		Route:          "web",
		Input:          "hello",
		ExpectedStatus: agentquality.StatusPass,
		Required:       true,
	})
	writeGoldenCase(t, casesDir, agentquality.Case{
		ID:             "case-optional-pass",
		Name:           "optional pass",
		Route:          "web",
		Input:          "world",
		ExpectedStatus: agentquality.StatusPass,
		Required:       false,
	})

	run, err := store.Start(BatchEvalStart{
		BatchID:  "batch-golden",
		Kind:     BatchEvalKindManual,
		CasesDir: casesDir,
	})

	require.NoError(t, err)
	assert.Equal(t, BatchEvalFailed, run.Status)
	assert.Equal(t, 2, run.Summary.Total)
	assert.Equal(t, 0, run.Summary.Passed)
	assert.Equal(t, 0, run.Summary.Failed)
	assert.Equal(t, 2, run.Summary.Unknown)
	assert.Contains(t, run.Summary.Reasons, "golden cases eval runner not configured")
	require.Len(t, run.CaseResults, 2)
	assert.Equal(t, "eval runner not configured", run.CaseResults[0].Reason)
}

func TestBatchEvalRunStore_StartEvaluatesGoldenCasesDirWithExplicitRunner(t *testing.T) {
	now := time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)
	store := NewMemoryBatchEvalRunStore(func() time.Time { return now })
	casesDir := t.TempDir()
	writeGoldenCase(t, casesDir, agentquality.Case{
		ID:             "case-required-pass",
		Name:           "required pass",
		Route:          "web",
		Input:          "hello",
		ExpectedStatus: agentquality.StatusPass,
		Required:       true,
	})
	writeGoldenCase(t, casesDir, agentquality.Case{
		ID:             "case-optional-pass",
		Name:           "optional pass",
		Route:          "web",
		Input:          "world",
		ExpectedStatus: agentquality.StatusPass,
		Required:       false,
	})

	run, err := store.Start(BatchEvalStart{
		BatchID:    "batch-golden",
		Kind:       BatchEvalKindManual,
		CasesDir:   casesDir,
		EvalRunner: agentquality.StaticEvalRunner{},
		Candidates: []agentquality.CandidateRecord{
			qualityWorkbenchCandidate("candidate-supplement", agentquality.CandidatePromoted, agentquality.FailureTool, "failed", now),
		},
	})

	require.NoError(t, err)
	assert.Equal(t, BatchEvalFailed, run.Status)
	assert.Equal(t, 3, run.Summary.Total)
	assert.Equal(t, 2, run.Summary.Passed)
	assert.Equal(t, 1, run.Summary.Failed)
	assert.Contains(t, run.Summary.Reasons, "golden cases gate passed")
	assert.Contains(t, run.Summary.Reasons, "candidate-supplement verify_result failed")
}

func TestBatchEvalRunStore_ListAndGet(t *testing.T) {
	now := time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)
	store := NewMemoryBatchEvalRunStore(func() time.Time { return now })

	first, err := store.Start(BatchEvalStart{BatchID: "batch-1", Kind: BatchEvalKindManual, Candidates: []agentquality.CandidateRecord{
		qualityWorkbenchCandidate("candidate-1", agentquality.CandidatePromotedVerified, agentquality.FailureTool, "passed", now),
	}})
	require.NoError(t, err)
	now = now.Add(time.Minute)
	second, err := store.Start(BatchEvalStart{BatchID: "batch-2", Kind: BatchEvalKindReplay, Candidates: []agentquality.CandidateRecord{
		qualityWorkbenchCandidate("candidate-2", agentquality.CandidatePromotedRegressed, agentquality.FailureRuntime, "failed", now),
	}})
	require.NoError(t, err)

	got, ok := store.Get(first.ID)
	require.True(t, ok)
	assert.Equal(t, first.ID, got.ID)

	list := store.List(BatchEvalRunListFilter{Status: BatchEvalFailed, Limit: 10})
	require.Len(t, list, 1)
	assert.Equal(t, second.ID, list[0].ID)
}

func TestBatchEvalRunStore_ListFiltersByAttribution(t *testing.T) {
	now := time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)
	store := NewMemoryBatchEvalRunStore(func() time.Time { return now })

	first, err := store.Start(BatchEvalStart{
		BatchID:    "batch-1",
		Kind:       BatchEvalKindManual,
		DomainID:   "customer_service",
		SourceKind: "workflow",
		SourceName: "case_triage",
		Candidates: []agentquality.CandidateRecord{
			qualityWorkbenchCandidate("candidate-1", agentquality.CandidatePromotedRegressed, agentquality.FailureTool, "failed", now),
		},
	})
	require.NoError(t, err)
	_, err = store.Start(BatchEvalStart{
		BatchID:    "batch-2",
		Kind:       BatchEvalKindManual,
		DomainID:   "generic",
		SourceKind: "master",
		SourceName: "react",
		Candidates: []agentquality.CandidateRecord{
			qualityWorkbenchCandidate("candidate-2", agentquality.CandidatePromotedRegressed, agentquality.FailureTool, "failed", now),
		},
	})
	require.NoError(t, err)

	list := store.List(BatchEvalRunListFilter{
		DomainID:   "customer_service",
		SourceKind: "workflow",
		SourceName: "case_triage",
		Limit:      10,
	})

	require.Len(t, list, 1)
	assert.Equal(t, first.ID, list[0].ID)
	assert.Equal(t, "customer_service", list[0].DomainID)
}

func writeGoldenCase(t *testing.T, dir string, c agentquality.Case) {
	t.Helper()
	b, err := json.Marshal(c)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, c.ID+".json"), b, 0o600))
}

func TestBatchEvalPersistsRunnerEvidenceLevel(t *testing.T) {
	now := time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)
	store := NewMemoryBatchEvalRunStore(func() time.Time { return now })
	casesDir := t.TempDir()
	writeGoldenCase(t, casesDir, agentquality.Case{
		ID:             "case-1",
		Name:           "test case",
		Route:          "web",
		Input:          "hello",
		ExpectedStatus: agentquality.StatusPass,
		Required:       true,
	})

	run, err := store.Start(BatchEvalStart{
		BatchID:    "batch-with-runner",
		Kind:       BatchEvalKindManual,
		CasesDir:   casesDir,
		EvalRunner: agentquality.StaticEvalRunner{},
	})

	require.NoError(t, err)
	assert.Equal(t, agentquality.EvidenceStaticSchema, run.RunnerInfo.EvidenceLevel)
	assert.Equal(t, "static", run.RunnerInfo.Name)
	assert.Equal(t, "1.0", run.RunnerInfo.Version)
}

func TestBatchEvalShadowModeAttachesShadowMetricsAndDomainRegression(t *testing.T) {
	now := time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)
	store := NewMemoryBatchEvalRunStore(func() time.Time { return now })

	run, err := store.Start(BatchEvalStart{
		BatchID: "batch-shadow",
		Kind:    BatchEvalKindShadow,
		ShadowResults: []agentquality.ShadowEvalResult{
			{
				CaseID:   "case-1",
				DomainID: "customer_service",
				Passed:   true,
				JudgeVerdict: agentquality.EvaluationVerdict{
					Score:       8,
					Verdict:     "passed",
					FailureType: agentquality.FailureNone,
				},
				RunnerInfo: agentquality.RunnerInfo{EvidenceLevel: agentquality.EvidenceProductionShadow},
				Timestamp:  now,
			},
			{
				CaseID:   "case-2",
				DomainID: "customer_service",
				Passed:   false,
				JudgeVerdict: agentquality.EvaluationVerdict{
					Score:       4,
					Verdict:     "tool mismatch",
					FailureType: agentquality.FailureTool,
				},
				RunnerInfo: agentquality.RunnerInfo{EvidenceLevel: agentquality.EvidenceProductionShadow},
				Timestamp:  now,
			},
		},
	})

	require.NoError(t, err)
	require.Len(t, run.ShadowResults, 2)
	require.Len(t, run.ShadowMetrics, 1)
	assert.Equal(t, "customer_service", run.ShadowMetrics[0].DomainID)
	assert.Equal(t, 2, run.ShadowMetrics[0].SampleCount)
	assert.Equal(t, 0.5, run.ShadowMetrics[0].PassRate)
	assert.Equal(t, 1, run.ShadowMetrics[0].ToolMisuses)
	require.Len(t, run.DomainRegressions, 1)
	assert.Equal(t, "fail", run.DomainRegressions[0].Status)
	assert.Equal(t, string(agentquality.EvidenceProductionShadow), run.DomainRegressions[0].EvidenceLevel)
}
