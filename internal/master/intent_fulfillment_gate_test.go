package master

import (
	"encoding/json"
	"testing"

	"github.com/chef-guo/agents-hive/internal/llm"
)

func TestIntentFulfillmentGate_SuppressesIncompleteFinalAnswer(t *testing.T) {
	gate := IntentFulfillmentGate{}
	decision := gate.Decide(intentFulfillmentGateInput{
		Evaluation: ContractEvaluation{Status: ContractIncomplete, Reason: "external_send_missing_recipient_or_send", Missing: []MissingRequirement{MissingRecipient, MissingSendAttempt}},
		Response:   llm.ChatWithToolsResponse{Content: "今天北京天气晴。", FinishReason: "stop"},
	})
	if decision.Action != IntentFulfillmentSuppressAndRetry {
		t.Fatalf("Action = %q, want suppress_and_retry; decision=%+v", decision.Action, decision)
	}
	if decision.AllowAssistant {
		t.Fatal("incomplete final answer must not be persisted or broadcast")
	}
	if decision.Reflection.Trigger != "intent_fulfillment" {
		t.Fatalf("reflection trigger = %q", decision.Reflection.Trigger)
	}
}

func TestIntentFulfillmentGate_PassesSatisfiedFinalAnswer(t *testing.T) {
	gate := IntentFulfillmentGate{}
	decision := gate.Decide(intentFulfillmentGateInput{
		Evaluation: ContractEvaluation{Status: ContractSatisfied, Reason: "external_send_satisfied"},
		Response:   llm.ChatWithToolsResponse{Content: "已发送。", FinishReason: "stop"},
	})
	if decision.Action != IntentFulfillmentPass || !decision.AllowAssistant {
		t.Fatalf("satisfied final answer should pass; decision=%+v", decision)
	}
	if decision.TaskStatus != TaskStatusCompleted {
		t.Fatalf("TaskStatus = %q", decision.TaskStatus)
	}
}

func TestIntentFulfillmentGate_PausesNeedsUser(t *testing.T) {
	gate := IntentFulfillmentGate{}
	decision := gate.Decide(intentFulfillmentGateInput{
		Evaluation: ContractEvaluation{Status: ContractNeedsUser, Reason: "external_send_recipient_ambiguous"},
		Response: llm.ChatWithToolsResponse{
			ToolCalls:    []llm.ToolCall{{ID: "question-1", Name: "question", Arguments: json.RawMessage(`{"question":"请选择联系人"}`)}},
			FinishReason: "tool_calls",
		},
	})
	if decision.Action != IntentFulfillmentPause || !decision.AllowAssistant {
		t.Fatalf("needs_user should allow the question tool path and mark pause; decision=%+v", decision)
	}
	if decision.TaskStatus != TaskStatusPaused {
		t.Fatalf("TaskStatus = %q", decision.TaskStatus)
	}
}

func TestIntentFulfillmentGate_SuppressesQuestionWithoutLookup(t *testing.T) {
	gate := IntentFulfillmentGate{}
	decision := gate.Decide(intentFulfillmentGateInput{
		Evaluation: ContractEvaluation{
			Status:   ContractIncomplete,
			Reason:   "external_send_missing_recipient_or_send",
			Missing:  []MissingRequirement{MissingRecipient, MissingSendAttempt},
			Evidence: ContractEvidence{QuestionAsked: true},
		},
		Response: llm.ChatWithToolsResponse{
			ToolCalls:    []llm.ToolCall{{ID: "question-1", Name: "question", Arguments: json.RawMessage(`{"question":"请选择联系人"}`)}},
			FinishReason: "tool_calls",
		},
	})
	if decision.Action != IntentFulfillmentSuppressAndRetry || decision.AllowAssistant {
		t.Fatalf("question without structured ambiguity evidence must be suppressed; decision=%+v", decision)
	}
}

func TestIntentFulfillmentGate_SuppressesInvalidSendToolCall(t *testing.T) {
	gate := IntentFulfillmentGate{}
	decision := gate.Decide(intentFulfillmentGateInput{
		Evaluation: ContractEvaluation{
			Status:   ContractIncomplete,
			Reason:   "external_send_missing_send_payload",
			Missing:  []MissingRequirement{MissingRecipient, MissingSendPayload},
			Evidence: ContractEvidence{SendAttempted: true, ContentPrepared: true},
		},
		Response: llm.ChatWithToolsResponse{
			ToolCalls:    []llm.ToolCall{{ID: "send-1", Name: "feishu_api", Arguments: json.RawMessage(`{"action":"send_message","content":"天气"}`)}},
			FinishReason: "tool_calls",
		},
	})
	if decision.Action != IntentFulfillmentSuppressAndRetry || decision.AllowAssistant {
		t.Fatalf("send tool call without recipient/content must be suppressed before execution; decision=%+v", decision)
	}
}

func TestIntentFulfillmentGate_FailsBlockedExternalSend(t *testing.T) {
	gate := IntentFulfillmentGate{}
	decision := gate.Decide(intentFulfillmentGateInput{
		Evaluation: ContractEvaluation{Status: ContractBlocked, Reason: "external_send_tool_failed"},
		Response:   llm.ChatWithToolsResponse{Content: "发送失败。", FinishReason: "stop"},
	})
	if decision.Action != IntentFulfillmentFail || !decision.AllowAssistant {
		t.Fatalf("blocked final answer should be shown as failed; decision=%+v", decision)
	}
	if decision.TaskStatus != TaskStatusFailed {
		t.Fatalf("TaskStatus = %q", decision.TaskStatus)
	}
}

func TestIntentFulfillmentGate_HardStopsAfterOneRetryByDefault(t *testing.T) {
	gate := IntentFulfillmentGate{}
	first := gate.Decide(intentFulfillmentGateInput{
		Evaluation: ContractEvaluation{Status: ContractIncomplete, Reason: "external_send_missing_send_attempt"},
		Response:   llm.ChatWithToolsResponse{Content: "天气结果。", FinishReason: "stop"},
		RetryCount: 0,
	})
	if first.Action != IntentFulfillmentSuppressAndRetry {
		t.Fatalf("first miss should retry; decision=%+v", first)
	}
	second := gate.Decide(intentFulfillmentGateInput{
		Evaluation: ContractEvaluation{Status: ContractIncomplete, Reason: "external_send_missing_send_attempt"},
		Response:   llm.ChatWithToolsResponse{Content: "天气结果。", FinishReason: "stop"},
		RetryCount: 1,
	})
	if second.Action != IntentFulfillmentFail || second.AllowAssistant {
		t.Fatalf("second miss should hard stop without persisting bad answer; decision=%+v", second)
	}
}
