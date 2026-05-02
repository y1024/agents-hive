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
