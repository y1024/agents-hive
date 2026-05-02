package agentquality

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeEvalDiffComparesGoldenCaseRuns(t *testing.T) {
	baseline := EvalRun{
		ID: "baseline-run",
		Results: []EvalCaseResult{
			{CaseID: "case-1", Passed: true, CostUSD: 0.10, LatencyMS: 1000},
			{CaseID: "case-2", Passed: true, CostUSD: 0.20, LatencyMS: 2000},
			{CaseID: "case-3", Passed: false, CostUSD: 0.30, LatencyMS: 3000},
			{CaseID: "case-4", Passed: false, CostUSD: 0.40, LatencyMS: 4000},
		},
	}
	treatment := EvalRun{
		ID: "treatment-run",
		Results: []EvalCaseResult{
			{CaseID: "case-1", Passed: true, CostUSD: 0.15, LatencyMS: 900},
			{CaseID: "case-2", Passed: true, CostUSD: 0.15, LatencyMS: 1900},
			{CaseID: "case-3", Passed: true, CostUSD: 0.25, LatencyMS: 2900},
			{CaseID: "case-4", Passed: true, CostUSD: 0.35, LatencyMS: 3900},
		},
	}

	diff, err := ComputeEvalDiff(baseline, treatment)

	require.NoError(t, err)
	assert.Equal(t, "baseline-run", diff.BaselineRunID)
	assert.Equal(t, "treatment-run", diff.TreatmentRunID)
	assert.Equal(t, 0.50, diff.Baseline.SuccessRate)
	assert.Equal(t, 1.00, diff.Treatment.SuccessRate)
	assert.InDelta(t, 0.50, diff.SuccessRateDelta, 0.0001)
	assert.InDelta(t, -0.025, diff.AverageCostDeltaUSD, 0.0001)
	assert.InDelta(t, -100.0, diff.AverageLatencyDeltaMS, 0.0001)
	assert.GreaterOrEqual(t, diff.SuccessPValue, 0.0)
	assert.LessOrEqual(t, diff.SuccessPValue, 1.0)
	assert.Len(t, diff.CaseDiffs, 4)
}

func TestEvalDiffStatusTransitions(t *testing.T) {
	diff := EvalDiff{ID: "diff-1", Status: EvalDiffPending}

	running, err := diff.Transition(EvalDiffRunning)
	require.NoError(t, err)
	done, err := running.Transition(EvalDiffDone)
	require.NoError(t, err)
	approved, err := done.Transition(EvalDiffApproved)
	require.NoError(t, err)
	assert.Equal(t, EvalDiffApproved, approved.Status)

	_, err = diff.Transition(EvalDiffApproved)
	assert.Error(t, err)
	_, err = approved.Transition(EvalDiffRejected)
	assert.Error(t, err)
}

func TestRenderABReportMarkdownUsesOfflineEvalDiffFields(t *testing.T) {
	diff := EvalDiff{
		ID:                    "diff-1",
		BaselineRunID:         "baseline-run",
		TreatmentRunID:        "treatment-run",
		Status:                EvalDiffDone,
		SuccessRateDelta:      0.25,
		AverageCostDeltaUSD:   -0.02,
		AverageLatencyDeltaMS: 150,
		SuccessPValue:         0.0412,
		Baseline:              EvalRunSummary{SuccessRate: 0.50, AverageCostUSD: 0.10, AverageLatencyMS: 1000},
		Treatment:             EvalRunSummary{SuccessRate: 0.75, AverageCostUSD: 0.08, AverageLatencyMS: 1150},
		CreatedAt:             time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC),
	}

	report := RenderABReportMarkdown(diff)

	assert.Contains(t, report, "BaselineRunID: baseline-run")
	assert.Contains(t, report, "TreatmentRunID: treatment-run")
	assert.Contains(t, report, "| Success rate |")
	assert.NotContains(t, strings.ToLower(report), "canary")
	assert.NotContains(t, report, "灰度")
	assert.NotContains(t, strings.ToLower(report), "rollout")
}
