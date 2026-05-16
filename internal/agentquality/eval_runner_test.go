package agentquality

import (
	"path/filepath"
	"testing"
)

func TestStaticEvalRunnerBuildsGateableSummaryFromFixtures(t *testing.T) {
	cases, err := LoadCases(filepath.Join("testdata"))
	if err != nil {
		t.Fatalf("load cases: %v", err)
	}

	input := StaticEvalSummary(cases)
	metrics := ComputeGateMetrics(GateInput{
		Cases:              cases,
		Results:            input.Results,
		Events:             input.Events,
		EventsByCase:       input.EventsByCase,
		CandidateByCaseID:  input.CandidateByCaseID,
		ToolActualByCaseID: input.ToolActualByCaseID,
		ReplayRefByCaseID:  input.ReplayRefByCaseID,
	})

	if len(input.Results) != len(cases) {
		t.Fatalf("results count = %d, want %d", len(input.Results), len(cases))
	}
	if err := EvaluateGate(metrics, DefaultGateThresholds()); err != nil {
		t.Fatalf("static eval summary must pass gate: %v", err)
	}
}

func TestStaticEvalRunnerReportsStaticSchemaEvidence(t *testing.T) {
	var runner StaticEvalRunner

	// StaticEvalRunner must implement DescribedEvalRunner
	var _ DescribedEvalRunner = runner

	info := runner.Info()
	if info.EvidenceLevel != EvidenceStaticSchema {
		t.Fatalf("expected evidence level %q, got %q", EvidenceStaticSchema, info.EvidenceLevel)
	}
	if info.Name != "static" {
		t.Fatalf("expected runner name %q, got %q", "static", info.Name)
	}

	// Verify the reason text in results
	cases := []LoadedCase{{
		Path: "test",
		Case: Case{
			ID:             "test-case",
			Name:           "test case",
			Route:          "web",
			Input:          "hello",
			ExpectedStatus: StatusPass,
		},
	}}
	gateInput, err := runner.Run(cases)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gateInput.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(gateInput.Results))
	}
	if gateInput.Results[0].Reason != "static schema check only" {
		t.Fatalf("expected reason %q, got %q", "static schema check only", gateInput.Results[0].Reason)
	}
}

func TestStaticSchemaResultCannotApproveOptimization(t *testing.T) {
	tests := []struct {
		level RunnerEvidenceLevel
		want  bool
	}{
		{EvidenceStaticSchema, false},
		{EvidenceReplayTrace, false},
		{EvidenceSimulatedRunner, false},
		{EvidenceRealRunner, true},
		{EvidenceProductionShadow, true},
		{EvidenceHumanVerified, true},
		{"unknown_level", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			got := CanApproveOptimization(tt.level)
			if got != tt.want {
				t.Fatalf("CanApproveOptimization(%q) = %v, want %v", tt.level, got, tt.want)
			}
		})
	}
}
