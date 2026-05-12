package master

import "github.com/chef-guo/agents-hive/internal/llm"

type IntentFulfillmentAction string

const (
	IntentFulfillmentPass             IntentFulfillmentAction = "pass"
	IntentFulfillmentSuppressAndRetry IntentFulfillmentAction = "suppress_and_retry"
	IntentFulfillmentPause            IntentFulfillmentAction = "pause"
	IntentFulfillmentFail             IntentFulfillmentAction = "fail"
)

type intentFulfillmentGateInput struct {
	Evaluation ContractEvaluation
	Response   llm.ChatWithToolsResponse
	RetryCount int
}

type intentFulfillmentGateDecision struct {
	Action         IntentFulfillmentAction
	Reason         string
	AllowAssistant bool
	TaskStatus     TaskStatus
	Reflection     reflectionNoteInput
}

type IntentFulfillmentGate struct {
	MaxRetries int
}

func (g IntentFulfillmentGate) Decide(in intentFulfillmentGateInput) intentFulfillmentGateDecision {
	if in.Evaluation.Status == "" || in.Evaluation.Status == ContractSatisfied {
		return intentFulfillmentGateDecision{Action: IntentFulfillmentPass, Reason: in.Evaluation.Reason, AllowAssistant: true, TaskStatus: TaskStatusCompleted}
	}
	if in.Evaluation.Status == ContractNeedsUser {
		return intentFulfillmentGateDecision{Action: IntentFulfillmentPause, Reason: in.Evaluation.Reason, AllowAssistant: true, TaskStatus: TaskStatusPaused}
	}
	if in.Evaluation.Status == ContractBlocked {
		return intentFulfillmentGateDecision{Action: IntentFulfillmentFail, Reason: in.Evaluation.Reason, AllowAssistant: true, TaskStatus: TaskStatusFailed}
	}
	if len(in.Response.ToolCalls) > 0 || !shouldExitTask(in.Response.FinishReason) {
		if in.Evaluation.Status == ContractIncomplete && incompleteIntermediateToolCallNeedsCorrection(in.Response.ToolCalls) {
			return g.retryOrFail(in)
		}
		return intentFulfillmentGateDecision{Action: IntentFulfillmentPass, Reason: "intermediate_response", AllowAssistant: true, TaskStatus: TaskStatusCompleted}
	}
	return g.retryOrFail(in)
}

func (g IntentFulfillmentGate) retryOrFail(in intentFulfillmentGateInput) intentFulfillmentGateDecision {
	decision := intentFulfillmentGateDecision{
		Action:         IntentFulfillmentSuppressAndRetry,
		Reason:         in.Evaluation.Reason,
		AllowAssistant: false,
		TaskStatus:     TaskStatusPaused,
		Reflection: reflectionNoteInput{
			Trigger:  "intent_fulfillment",
			Severity: "warn",
			Detail:   lowCardinalityContractDetail(in.Evaluation),
		},
	}
	if g.shouldHardStop(in.RetryCount) {
		decision.Action = IntentFulfillmentFail
		decision.TaskStatus = TaskStatusFailed
		decision.Reflection.Severity = "hard_stop"
	}
	return decision
}

func incompleteIntermediateToolCallNeedsCorrection(calls []llm.ToolCall) bool {
	for _, call := range calls {
		fact := classifyExternalSendToolCall(call)
		switch fact.Kind {
		case externalSendCallQuestion:
			return true
		case externalSendCallSend:
			return !sendCallHasContentAndRecipient(call)
		}
	}
	return false
}

func (g IntentFulfillmentGate) shouldHardStop(retries int) bool {
	limit := g.MaxRetries
	if limit <= 0 {
		limit = 1
	}
	return retries >= limit
}

func lowCardinalityContractDetail(eval ContractEvaluation) string {
	if eval.Reason != "" {
		return eval.Reason
	}
	if len(eval.Missing) == 0 {
		return string(eval.Status)
	}
	return "external_send_incomplete"
}
