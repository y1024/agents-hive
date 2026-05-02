package agentquality

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCandidateFromFailure_DoesNotMakeRequiredCase(t *testing.T) {
	rec := CandidateFromFailure("session-1", "执行 rm -rf ./tmp-cache", "session-1:step-3", Event{
		Route:        "im",
		FailureType:  FailurePermission,
		FinalStatus:  StatusNeedsUser,
		ToolDecision: ToolDecision{Actual: "bash"},
	})

	assert.Equal(t, CandidateNew, rec.Status)
	assert.False(t, rec.Case.Required)
	assert.Equal(t, "dangerous", rec.Risk)
	assert.Equal(t, StatusNeedsUser, rec.Case.ExpectedStatus)
	assert.Equal(t, []string{"bash"}, rec.Case.AllowedTools)
	assert.NotEmpty(t, rec.Fingerprint)
	assert.Equal(t, "session-1:step-3", rec.ReplayRef)
}

func TestCandidateFromFailure_PersistsOptimizationSuggestions(t *testing.T) {
	rec := CandidateFromFailure("session-1", "定位 createPermissionPromptFn", "session-1:step-4", Event{
		Route:       "web",
		FailureType: FailureTool,
		FinalStatus: StatusFail,
		Prompt:      PromptRef{Key: "system/base", Version: "sha256:old"},
		ToolDecision: ToolDecision{
			Expected: []string{"grep"},
			Actual:   "read_file",
		},
	})

	assertSuggestionKinds(t, rec.Suggestions, SuggestionPromptDiff, SuggestionToolDescription)
	assert.Equal(t, "system/base@sha256:old", rec.Suggestions[0].Target)
	assert.Equal(t, "grep", rec.Suggestions[1].Target)
}

func TestValidateCandidateStatus(t *testing.T) {
	assert.NoError(t, ValidateCandidateStatus(CandidateApproved))
	assert.Error(t, ValidateCandidateStatus("invalid"))
}

func TestValidateCandidateTransition_BlocksDirectPromotion(t *testing.T) {
	assert.NoError(t, ValidateCandidateTransition(CandidateNew, CandidateReviewing))
	assert.NoError(t, ValidateCandidateTransition(CandidateReviewing, CandidateApproved))
	assert.NoError(t, ValidateCandidateTransition(CandidateApproved, CandidatePromoted))
	assert.Error(t, ValidateCandidateTransition(CandidateNew, CandidatePromoted))
}

func TestValidateCandidateTransition_AllowsPromotedVerificationResults(t *testing.T) {
	assert.NoError(t, ValidateCandidateStatus(CandidatePromotedVerified))
	assert.NoError(t, ValidateCandidateStatus(CandidatePromotedRegressed))
	assert.NoError(t, ValidateCandidateTransition(CandidatePromoted, CandidatePromotedVerified))
	assert.NoError(t, ValidateCandidateTransition(CandidatePromoted, CandidatePromotedRegressed))
	assert.Error(t, ValidateCandidateTransition(CandidatePromotedVerified, CandidatePromoted))
	assert.Error(t, ValidateCandidateTransition(CandidatePromotedRegressed, CandidatePromoted))
}
