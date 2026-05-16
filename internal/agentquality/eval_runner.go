package agentquality

import "context"

type RunnerEvidenceLevel string

const (
	EvidenceStaticSchema     RunnerEvidenceLevel = "static_schema"
	EvidenceReplayTrace      RunnerEvidenceLevel = "replay_trace"
	EvidenceSimulatedRunner  RunnerEvidenceLevel = "simulated_runner"
	EvidenceRealRunner       RunnerEvidenceLevel = "real_runner"
	EvidenceProductionShadow RunnerEvidenceLevel = "production_shadow"
	EvidenceHumanVerified    RunnerEvidenceLevel = "human_verified"
)

type RunnerInfo struct {
	Name          string              `json:"name"`
	Version       string              `json:"version"`
	EvidenceLevel RunnerEvidenceLevel `json:"evidence_level"`
}

type EvalRunner interface {
	Run(cases []LoadedCase) (GateInput, error)
}

type ContextEvalRunner interface {
	RunWithContext(ctx context.Context, cases []LoadedCase) (GateInput, error)
}

type DescribedEvalRunner interface {
	EvalRunner
	Info() RunnerInfo
}

type StaticEvalRunner struct{}

func (StaticEvalRunner) Info() RunnerInfo {
	return RunnerInfo{
		Name:          "static",
		Version:       "1.0",
		EvidenceLevel: EvidenceStaticSchema,
	}
}

func (StaticEvalRunner) Run(cases []LoadedCase) (GateInput, error) {
	return StaticEvalSummary(cases), nil
}

// CanApproveOptimization returns true if the evidence level is strong enough
// to approve optimization decisions. Static schema checks are insufficient.
func CanApproveOptimization(level RunnerEvidenceLevel) bool {
	switch level {
	case EvidenceRealRunner, EvidenceProductionShadow, EvidenceHumanVerified:
		return true
	default:
		return false
	}
}

func CanAuthorizeRolloutEvidence(level RunnerEvidenceLevel) bool {
	switch level {
	case EvidenceRealRunner, EvidenceProductionShadow, EvidenceHumanVerified:
		return true
	default:
		return false
	}
}

func StaticEvalSummary(cases []LoadedCase) GateInput {
	input := GateInput{
		Results:             make([]Result, 0, len(cases)),
		Events:              make([]Event, 0, len(cases)*2),
		EventsByCase:        make(map[string][]Event, len(cases)),
		ToolActualByCaseID:  make(map[string][]string, len(cases)),
		CandidateByCaseID:   make(map[string]bool),
		ReplayRefByCaseID:   make(map[string]string),
		FinalOutputByCaseID: make(map[string]string),
	}
	for _, lc := range cases {
		c := lc.Case
		input.Results = append(input.Results, Result{
			CaseID: c.ID,
			Passed: true,
			Reason: "static schema check only",
		})

		tool := staticToolForCase(c)
		if tool != "" {
			input.ToolActualByCaseID[c.ID] = []string{tool}
			input.addStaticEvent(c, Event{
				Name:        EventToolDecision,
				CaseID:      c.ID,
				Route:       c.Route,
				FailureType: FailureNone,
				FinalStatus: c.ExpectedStatus,
				ToolDecision: ToolDecision{
					Expected: staticExpectedTools(c),
					Actual:   tool,
					Decision: DecisionExpected,
				},
			})
		}

		if c.Scenario == "delegation" {
			input.addStaticEvent(c, Event{
				Name:        EventDelegation,
				CaseID:      c.ID,
				Route:       c.Route,
				FailureType: FailureNone,
				FinalStatus: c.ExpectedStatus,
				Delegation: Delegation{
					ParentTraceID: "static-parent-" + c.ID,
					ChildTraceID:  "static-child-" + c.ID,
					AgentID:       "static-subagent",
					AgentType:     "subagent",
				},
			})
		}
		if c.Scenario == "acp_permission_cancel" {
			input.addStaticEvent(c, Event{
				Name:        EventDelegation,
				CaseID:      c.ID,
				Route:       c.Route,
				FailureType: FailureNone,
				FinalStatus: c.ExpectedStatus,
				Delegation: Delegation{
					AgentType:  "acp",
					StopReason: "cancelled",
				},
			})
		}
	}
	return input
}

func (input *GateInput) addStaticEvent(c Case, ev Event) {
	input.Events = append(input.Events, ev)
	input.EventsByCase[c.ID] = append(input.EventsByCase[c.ID], ev)
}

func staticToolForCase(c Case) string {
	if len(c.ExpectedTools) > 0 {
		return c.ExpectedTools[0]
	}
	if len(c.AllowedTools) > 0 {
		return c.AllowedTools[0]
	}
	return ""
}

func staticExpectedTools(c Case) []string {
	if len(c.ExpectedTools) > 0 {
		return c.ExpectedTools
	}
	return c.AllowedTools
}
