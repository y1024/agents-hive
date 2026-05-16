package qualityworkbench

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

func TestReplayRunnerWithoutEvalRunnerDoesNotFakeSuccess(t *testing.T) {
	store := newReplayCandidateStore(map[string]agentquality.CandidateRecord{
		"candidate-1": {
			ID:     "candidate-1",
			Status: agentquality.CandidatePromoted,
			Route:  "web",
			Input:  "run safe check",
			Case: agentquality.Candidate{
				ID:             "candidate-1",
				Name:           "candidate 1",
				Route:          "web",
				Input:          "run safe check",
				ExpectedStatus: agentquality.StatusPass,
			},
			FailureType:  agentquality.FailureTool,
			VerifyResult: "passed",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		},
	})
	result, err := ReplayRunner{Store: store}.Run(context.Background(), ReplayJob{
		ID:        "replay-1",
		Kind:      ReplayJobKindCandidate,
		TargetIDs: []string{"candidate-1"},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "eval runner not configured")
	require.Equal(t, 1, result.Total)
	require.Equal(t, 1, result.Unknown)
	require.Contains(t, result.Reasons, "candidate-1 has no result")
	require.Len(t, result.Reasons, 2)
	require.Contains(t, result.Reasons[1], "gate failed:")
}

func TestReplayRunnerRequiresNonStaticEvidenceForPassedGate(t *testing.T) {
	store := newReplayCandidateStore(map[string]agentquality.CandidateRecord{
		"candidate-1": {
			ID:     "candidate-1",
			Status: agentquality.CandidatePromoted,
			Route:  "web",
			Input:  "run safe check",
			Case: agentquality.Candidate{
				ID:             "candidate-1",
				Name:           "candidate 1",
				Route:          "web",
				Input:          "run safe check",
				ExpectedStatus: agentquality.StatusPass,
			},
			FailureType:  agentquality.FailureTool,
			VerifyResult: "passed",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		},
	})

	// Using StaticEvalRunner: gate passes but evidence level is static_schema
	result, err := ReplayRunner{
		Store:      store,
		EvalRunner: agentquality.StaticEvalRunner{},
	}.Run(context.Background(), ReplayJob{
		ID:        "replay-2",
		Kind:      ReplayJobKindCandidate,
		TargetIDs: []string{"candidate-1"},
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.Total)
	require.Equal(t, 1, result.Passed)
	require.Equal(t, agentquality.EvidenceStaticSchema, result.RunnerInfo.EvidenceLevel)

	// Static schema evidence must NOT be sufficient for optimization approval
	require.False(t, agentquality.CanApproveOptimization(result.RunnerInfo.EvidenceLevel),
		"static_schema evidence must not approve optimization")
}

func TestReplayJobReturnsReplayRefForRealRunner(t *testing.T) {
	store := newReplayCandidateStore(map[string]agentquality.CandidateRecord{
		"candidate-1": {
			ID:     "candidate-1",
			Status: agentquality.CandidatePromoted,
			Route:  "web",
			Input:  "run safe check",
			Case: agentquality.Candidate{
				ID:             "candidate-1",
				Name:           "candidate 1",
				Route:          "web",
				Input:          "run safe check",
				ExpectedStatus: agentquality.StatusPass,
			},
			FailureType:  agentquality.FailureTool,
			VerifyResult: "passed",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		},
	})

	// 使用 real runner 返回 replay ref
	runner := &replayRefAgentRunner{
		replayRefs: map[string]string{
			"candidate-1": "replay-ref-real-001",
		},
	}

	result, err := ReplayRunner{
		Store:      store,
		EvalRunner: runner,
	}.Run(context.Background(), ReplayJob{
		ID:        "replay-3",
		Kind:      ReplayJobKindCandidate,
		TargetIDs: []string{"candidate-1"},
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.Total)
	require.Equal(t, 1, result.Passed)

	// 验证 runner info 是 real_runner
	require.Equal(t, agentquality.EvidenceRealRunner, result.RunnerInfo.EvidenceLevel)
	require.Equal(t, "agent_run", result.RunnerInfo.Name)

	// real_runner 证据可以用于优化审批
	require.True(t, agentquality.CanApproveOptimization(result.RunnerInfo.EvidenceLevel))
}

type replayRefAgentRunner struct {
	replayRefs map[string]string
}

func (r *replayRefAgentRunner) Run(cases []agentquality.LoadedCase) (agentquality.GateInput, error) {
	input := agentquality.GateInput{
		Results:            make([]agentquality.Result, 0, len(cases)),
		Events:             []agentquality.Event{},
		EventsByCase:       make(map[string][]agentquality.Event),
		ToolActualByCaseID: make(map[string][]string),
		CandidateByCaseID:  make(map[string]bool),
		ReplayRefByCaseID:  make(map[string]string),
	}
	for _, lc := range cases {
		input.Results = append(input.Results, agentquality.Result{
			CaseID: lc.Case.ID,
			Passed: true,
		})
		if ref, ok := r.replayRefs[lc.Case.ID]; ok {
			input.ReplayRefByCaseID[lc.Case.ID] = ref
		}
	}
	return input, nil
}

func (r *replayRefAgentRunner) Info() agentquality.RunnerInfo {
	return agentquality.RunnerInfo{
		Name:          "agent_run",
		Version:       "1.0",
		EvidenceLevel: agentquality.EvidenceRealRunner,
	}
}

type replayCandidateStore struct {
	rows map[string]agentquality.CandidateRecord
}

func newReplayCandidateStore(rows map[string]agentquality.CandidateRecord) replayCandidateStore {
	return replayCandidateStore{rows: rows}
}

func (s replayCandidateStore) GetCandidate(_ context.Context, id string) (*agentquality.CandidateRecord, bool, error) {
	rec, ok := s.rows[id]
	if !ok {
		return nil, false, nil
	}
	return &rec, true, nil
}

func (s replayCandidateStore) ListCandidates(_ context.Context, _ agentquality.CandidateFilter) ([]agentquality.CandidateRecord, int, error) {
	out := make([]agentquality.CandidateRecord, 0, len(s.rows))
	for _, rec := range s.rows {
		out = append(out, rec)
	}
	return out, len(out), nil
}
