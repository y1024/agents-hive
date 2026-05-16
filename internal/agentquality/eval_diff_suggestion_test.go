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
		TreatmentRunnerInfo: RunnerInfo{
			Name:          "agent_run",
			Version:       "1.0",
			EvidenceLevel: EvidenceRealRunner,
		},
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
		assert.Equal(t, EvidenceRealRunner, suggestion.RunnerInfo.EvidenceLevel)
		assert.True(t, suggestion.ReviewRequired)
	}
}

func TestEvalDiffSuggestionRequiresRealEvidenceForApproval(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	suggestion := OptimizationReviewSuggestion{
		ID:               "sug-static",
		Status:           SuggestionPending,
		SourceEvalDiffID: "diff-static",
		RunnerInfo: RunnerInfo{
			Name:          "static",
			Version:       "1.0",
			EvidenceLevel: EvidenceStaticSchema,
		},
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}

	_, err := suggestion.Approve("reviewer", "ok", now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires real_runner, production_shadow, or human_verified evidence")
}

func TestCandidateSuggestionRequiresAuthorizingEvidenceForApproval(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	base := OptimizationReviewSuggestion{
		ID:                "sug-candidate",
		Status:            SuggestionPending,
		SourceCandidateID: "candidate-1",
		CreatedAt:         now,
		UpdatedAt:         now,
		ExpiresAt:         now.Add(time.Hour),
	}

	cases := []struct {
		name    string
		level   RunnerEvidenceLevel
		wantErr bool
	}{
		{name: "empty evidence", level: "", wantErr: true},
		{name: "static evidence", level: EvidenceStaticSchema, wantErr: true},
		{name: "real runner", level: EvidenceRealRunner},
		{name: "production shadow", level: EvidenceProductionShadow},
		{name: "human verified", level: EvidenceHumanVerified},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			suggestion := base
			suggestion.RunnerInfo = RunnerInfo{EvidenceLevel: tt.level}

			got, err := suggestion.Approve("reviewer", "ok", now)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "requires real_runner, production_shadow, or human_verified evidence")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, SuggestionApproved, got.Status)
		})
	}
}
