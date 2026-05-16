package qualityworkbench

import (
	"testing"
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchEvalUsesConfiguredRealRunner(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	store := NewMemoryBatchEvalRunStore(func() time.Time { return now })

	// 创建一个 fake agent runner
	runner := &fakeAgentRunner{
		results: map[string]agentquality.Result{
			"aq01": {CaseID: "aq01", Passed: true},
		},
	}

	run, err := store.Start(BatchEvalStart{
		BatchID:    "batch-001",
		Kind:       BatchEvalKindFull,
		CasesDir:   "testdata/cases",
		EvalRunner: runner,
	})

	require.NoError(t, err)
	assert.Equal(t, "batch-001", run.BatchID)
	assert.Equal(t, BatchEvalKindFull, run.Kind)

	// 验证 runner info 被记录
	assert.Equal(t, "fake_agent_runner", run.RunnerInfo.Name)
	assert.Equal(t, agentquality.EvidenceRealRunner, run.RunnerInfo.EvidenceLevel)
}

func TestBatchEvalRecordsStaticRunnerInfo(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	store := NewMemoryBatchEvalRunStore(func() time.Time { return now })

	runner := agentquality.StaticEvalRunner{}

	run, err := store.Start(BatchEvalStart{
		BatchID:    "batch-002",
		Kind:       BatchEvalKindManual,
		CasesDir:   "testdata/cases",
		EvalRunner: runner,
	})

	require.NoError(t, err)

	// 验证 static runner info 被记录
	assert.Equal(t, "static", run.RunnerInfo.Name)
	assert.Equal(t, agentquality.EvidenceStaticSchema, run.RunnerInfo.EvidenceLevel)
}

func TestBatchEvalWithoutRunnerHasNoRunnerInfo(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	store := NewMemoryBatchEvalRunStore(func() time.Time { return now })

	run, err := store.Start(BatchEvalStart{
		BatchID: "batch-003",
		Kind:    BatchEvalKindManual,
		Candidates: []agentquality.CandidateRecord{
			{ID: "c1", VerifyResult: "passed"},
		},
	})

	require.NoError(t, err)

	// 没有 runner 时，RunnerInfo 应该是零值
	assert.Equal(t, "", run.RunnerInfo.Name)
	assert.Equal(t, agentquality.RunnerEvidenceLevel(""), run.RunnerInfo.EvidenceLevel)
}

// fakeAgentRunner 是一个测试用的 agent runner
type fakeAgentRunner struct {
	results map[string]agentquality.Result
}

func (f *fakeAgentRunner) Run(cases []agentquality.LoadedCase) (agentquality.GateInput, error) {
	input := agentquality.GateInput{
		Results:            make([]agentquality.Result, 0, len(cases)),
		Events:             []agentquality.Event{},
		EventsByCase:       make(map[string][]agentquality.Event),
		ToolActualByCaseID: make(map[string][]string),
		CandidateByCaseID:  make(map[string]bool),
		ReplayRefByCaseID:  make(map[string]string),
	}

	for _, lc := range cases {
		if result, ok := f.results[lc.Case.ID]; ok {
			input.Results = append(input.Results, result)
		} else {
			input.Results = append(input.Results, agentquality.Result{
				CaseID: lc.Case.ID,
				Passed: true,
				Reason: "fake runner default pass",
			})
		}
	}

	return input, nil
}

func (f *fakeAgentRunner) Info() agentquality.RunnerInfo {
	return agentquality.RunnerInfo{
		Name:          "fake_agent_runner",
		Version:       "1.0",
		EvidenceLevel: agentquality.EvidenceRealRunner,
	}
}
