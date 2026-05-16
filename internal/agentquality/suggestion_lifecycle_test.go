package agentquality

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOptimizationSuggestionFromDraftCreatesReviewableEntity(t *testing.T) {
	now := time.Now()
	rec := CandidateFromFailure("session-1", "定位权限审批", "session-1:step-4", Event{
		Route:       "web",
		FailureType: FailureTool,
		FinalStatus: StatusFail,
		Prompt:      PromptRef{Key: "system/base", Version: "sha256:old"},
		ToolDecision: ToolDecision{
			Expected: []string{"grep"},
			Actual:   "read_file",
		},
	})

	out := NewSuggestionGenerator(WithSuggestionNow(func() time.Time { return now })).GenerateFromCandidate(rec, "reviewer-1")

	require.Len(t, out, 2)
	assert.NotEmpty(t, out[0].ID)
	assert.Equal(t, SuggestionPending, out[0].Status)
	assert.Equal(t, rec.ID, out[0].SourceCandidateID)
	assert.Equal(t, "reviewer-1", out[0].CreatedBy)
	assert.True(t, out[0].ReviewRequired)
	assert.True(t, out[0].ExpiresAt.Equal(now.Add(30*24*time.Hour)))
}

func TestSuggestionApprovalStateMachine(t *testing.T) {
	now := time.Now()
	s := OptimizationReviewSuggestion{
		ID:         "suggestion-1",
		Status:     SuggestionPending,
		RunnerInfo: RunnerInfo{EvidenceLevel: EvidenceRealRunner},
		ExpiresAt:  now.Add(time.Hour),
	}

	approved, err := s.Approve("reviewer-1", "人工确认", now)

	require.NoError(t, err)
	assert.Equal(t, SuggestionApproved, approved.Status)
	assert.Equal(t, "reviewer-1", approved.ApprovedBy)
	assert.Equal(t, "人工确认", approved.ApprovalNote)
	require.NotNil(t, approved.ApprovedAt)
	assert.True(t, approved.ApprovedAt.Equal(now))
	_, err = approved.Approve("reviewer-2", "repeat", now)
	assert.Error(t, err)
}

func TestSuggestionApprovalRejectsExpired(t *testing.T) {
	now := time.Now()
	s := OptimizationReviewSuggestion{
		ID:        "suggestion-1",
		Status:    SuggestionPending,
		ExpiresAt: now.Add(-time.Second),
	}

	_, err := s.Approve("reviewer-1", "too late", now)

	assert.Error(t, err)
}
