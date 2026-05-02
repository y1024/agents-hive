package qualityworkbench

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareVersionMatrixMarksRegressedCases(t *testing.T) {
	diff := CompareVersionMatrix(VersionMatrixInput{
		BaselineRunID:  "baseline",
		TreatmentRunID: "treatment",
		Baseline: []CaseRunResult{
			{CaseID: "case-stable", Passed: true},
			{CaseID: "case-regressed", Passed: true},
			{CaseID: "case-still-failing", Passed: false},
		},
		Treatment: []CaseRunResult{
			{CaseID: "case-stable", Passed: true},
			{CaseID: "case-regressed", Passed: false, Reason: "wrong tool"},
			{CaseID: "case-still-failing", Passed: false},
			{CaseID: "case-new", Passed: false},
		},
	})

	require.Len(t, diff.Cases, 4)
	assert.Equal(t, []string{"case-regressed"}, diff.RegressedCaseIDs)
	assert.Equal(t, []string{"case-new"}, diff.NewFailureCaseIDs)
	assert.Empty(t, diff.RecoveredCaseIDs)
	regressed := diff.Cases["case-regressed"]
	assert.True(t, regressed.Regressed)
	assert.Equal(t, "baseline", diff.BaselineRunID)
	assert.Equal(t, "treatment", diff.TreatmentRunID)
	assert.Equal(t, "wrong tool", regressed.TreatmentReason)
}

func TestCompareVersionMatrixMarksRecoveredCases(t *testing.T) {
	diff := CompareVersionMatrix(VersionMatrixInput{
		Baseline:  []CaseRunResult{{CaseID: "case-recovered", Passed: false}},
		Treatment: []CaseRunResult{{CaseID: "case-recovered", Passed: true}},
	})

	assert.Equal(t, []string{"case-recovered"}, diff.RecoveredCaseIDs)
	assert.True(t, diff.Cases["case-recovered"].Recovered)
}
