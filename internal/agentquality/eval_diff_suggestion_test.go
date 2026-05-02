package agentquality

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuggestionGeneratorGenerateFromEvalDiffCreatesPromptToolAndSkillSuggestions(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	diff := EvalDiff{
		ID:             "diff-1",
		BaselineRunID:  "baseline-run",
		TreatmentRunID: "treatment-run",
		Status:         EvalDiffDone,
		CaseDiffs: []EvalCaseDiff{
			{CaseID: "prompt-case", BaselinePassed: true, TreatmentPassed: false, FailureType: FailurePrompt, Prompt: PromptRef{Key: "system/base", Version: "old"}},
			{CaseID: "tool-case", BaselinePassed: true, TreatmentPassed: false, FailureType: FailureTool, ExpectedTools: []string{"grep"}, ActualTool: "read_file"},
			{CaseID: "skill-case", BaselinePassed: true, TreatmentPassed: false, FailureType: FailureSkill, ExpectedSkills: []string{"code-review"}},
		},
	}

	suggestions := NewSuggestionGenerator(WithSuggestionNow(func() time.Time { return now })).GenerateFromEvalDiff(diff, "optimizer")

	require.Len(t, suggestions, 3)
	assert.Equal(t, SuggestionPromptDiff, suggestions[0].Kind)
	assert.Equal(t, SuggestionToolDescription, suggestions[1].Kind)
	assert.Equal(t, SuggestionSkillDraft, suggestions[2].Kind)
	for _, suggestion := range suggestions {
		assert.Equal(t, "diff-1", suggestion.SourceEvalDiffID)
		assert.Equal(t, "optimizer", suggestion.CreatedBy)
		assert.Equal(t, SuggestionPending, suggestion.Status)
		assert.True(t, suggestion.ReviewRequired)
	}
}
