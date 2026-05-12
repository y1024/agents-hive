package master

import (
	"encoding/json"
	"testing"

	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/router"
)

func externalSendIntentForTest() router.IntentFrame {
	return router.IntentFrame{
		Kind:              router.IntentExternalWrite,
		AllowsSideEffects: true,
		RequiresExternal:  true,
		Confidence:        0.95,
		Signals:           []string{"test_structured_intent"},
	}
}

func mustExternalSendContract(t *testing.T) IntentContract {
	t.Helper()
	contract, ok := NewIntentContract(externalSendIntentForTest())
	if !ok {
		t.Fatal("external-send intent should create a contract")
	}
	return contract
}

func TestIntentContract_ExternalSendWeatherOnlyIncomplete(t *testing.T) {
	contract := mustExternalSendContract(t)
	eval := contract.Evaluate([]llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("给郭松发一下今天的天气信息")},
		{Role: "tool", ToolName: "websearch", Content: llm.NewTextContent("weather result")},
		{Role: "tool", ToolName: "webfetch", Content: llm.NewTextContent("weather detail")},
	}, llm.ChatWithToolsResponse{Content: "今天北京天气晴。", FinishReason: "stop"})

	if eval.Status != ContractIncomplete {
		t.Fatalf("Status = %q, want incomplete; eval=%+v", eval.Status, eval)
	}
	assertMissingRequirement(t, eval, MissingRecipient)
	assertMissingRequirement(t, eval, MissingSendAttempt)
	if !eval.Evidence.ContentPrepared {
		t.Fatalf("weather/search tool result should count as content preparation evidence: %+v", eval.Evidence)
	}
}

func TestIntentContract_ExternalSendUniqueLookupStillNeedsSend(t *testing.T) {
	contract := mustExternalSendContract(t)
	eval := contract.Evaluate([]llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("给郭松发一下今天的天气信息")},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "lookup-1", Name: "feishu_api", Arguments: json.RawMessage(`{"action":"search_contacts","query":"郭松"}`)}}},
		{Role: "tool", ToolCallID: "lookup-1", ToolName: "feishu_api", Content: llm.NewTextContent(`{"contacts":[{"name":"郭松","open_id":"ou_1"}]}`)},
		{Role: "tool", ToolName: "websearch", Content: llm.NewTextContent("weather result")},
	}, llm.ChatWithToolsResponse{Content: "今天北京天气晴。", FinishReason: "stop"})

	if eval.Status != ContractIncomplete {
		t.Fatalf("Status = %q, want incomplete; eval=%+v", eval.Status, eval)
	}
	if !eval.Evidence.RecipientResolved {
		t.Fatalf("unique contact lookup should resolve recipient: %+v", eval.Evidence)
	}
	assertMissingRequirement(t, eval, MissingSendAttempt)
}

func TestIntentContract_ExternalSendAmbiguousLookupWithQuestionNeedsUser(t *testing.T) {
	contract := mustExternalSendContract(t)
	eval := contract.Evaluate([]llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("给郭松发一下今天的天气信息")},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "lookup-1", Name: "feishu_api", Arguments: json.RawMessage(`{"action":"search_contacts","query":"郭松"}`)}}},
		{Role: "tool", ToolCallID: "lookup-1", ToolName: "feishu_api", Content: llm.NewTextContent(`{"contacts":[{"name":"郭松","open_id":"ou_1"},{"name":"郭松","open_id":"ou_2"}]}`)},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "question-1", Name: "question", Arguments: json.RawMessage(`{"question":"请选择联系人","options":["ou_1","ou_2"]}`)}}},
	}, llm.ChatWithToolsResponse{FinishReason: "tool_calls"})

	if eval.Status != ContractNeedsUser {
		t.Fatalf("Status = %q, want needs_user; eval=%+v", eval.Status, eval)
	}
	if !eval.Evidence.RecipientAmbiguous || !eval.Evidence.QuestionAsked {
		t.Fatalf("ambiguous lookup + question tool should be structured needs_user evidence: %+v", eval.Evidence)
	}
}

func TestIntentContract_ExternalSendQuestionWithoutLookupIncomplete(t *testing.T) {
	contract := mustExternalSendContract(t)
	eval := contract.Evaluate([]llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("给郭松发一下今天的天气信息")},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "question-1", Name: "question", Arguments: json.RawMessage(`{"question":"请选择联系人"}`)}}},
	}, llm.ChatWithToolsResponse{FinishReason: "tool_calls"})

	if eval.Status != ContractIncomplete {
		t.Fatalf("question without lookup must not satisfy ambiguity handling; eval=%+v", eval)
	}
	assertMissingRequirement(t, eval, MissingRecipient)
}

func TestIntentContract_ExternalSendSatisfiedBySendAttempt(t *testing.T) {
	contract := mustExternalSendContract(t)
	for _, tc := range []struct {
		name string
		call llm.ToolCall
	}{
		{
			name: "feishu send message",
			call: llm.ToolCall{ID: "send-1", Name: "feishu_api", Arguments: json.RawMessage(`{"action":"send_message","chat_id":"oc_1","content":"天气"}`)},
		},
		{
			name: "generic im send",
			call: llm.ToolCall{ID: "send-1", Name: "send_im_message", Arguments: json.RawMessage(`{"platform":"feishu","chat_id":"oc_1","content":"天气"}`)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eval := contract.Evaluate([]llm.MessageWithTools{
				{Role: "user", Content: llm.NewTextContent("给郭松发一下今天的天气信息")},
				{Role: "assistant", ToolCalls: []llm.ToolCall{tc.call}},
			}, llm.ChatWithToolsResponse{Content: "已发送。", FinishReason: "stop"})
			if eval.Status != ContractSatisfied {
				t.Fatalf("Status = %q, want satisfied; eval=%+v", eval.Status, eval)
			}
		})
	}
}

func TestIntentContract_ExternalSendSendAttemptWithMissingPayloadIncomplete(t *testing.T) {
	contract := mustExternalSendContract(t)
	eval := contract.Evaluate([]llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("给郭松发一下今天的天气信息")},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "send-1", Name: "feishu_api", Arguments: json.RawMessage(`{"action":"send_message","content":"天气"}`)}}},
	}, llm.ChatWithToolsResponse{Content: "已发送。", FinishReason: "stop"})

	if eval.Status != ContractIncomplete {
		t.Fatalf("send attempt without recipient must be incomplete; eval=%+v", eval)
	}
	assertMissingRequirement(t, eval, MissingRecipient)
	assertMissingRequirement(t, eval, MissingSendPayload)
	assertNotMissingRequirement(t, eval, MissingContent)
}

func TestIntentContract_ExternalSendIgnoresPreviousTurnSend(t *testing.T) {
	contract := mustExternalSendContract(t)
	eval := contract.Evaluate([]llm.MessageWithTools{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "old-send", Name: "feishu_api", Arguments: json.RawMessage(`{"action":"send_message","chat_id":"oc_old","content":"old"}`)}}},
		{Role: "tool", ToolCallID: "old-send", ToolName: "feishu_api", Content: llm.NewTextContent(`{"ok":true}`)},
		{Role: "user", Content: llm.NewTextContent("给郭松发一下今天的天气信息")},
		{Role: "tool", ToolName: "websearch", Content: llm.NewTextContent("weather result")},
	}, llm.ChatWithToolsResponse{Content: "今天北京天气晴。", FinishReason: "stop"})

	if eval.Status != ContractIncomplete {
		t.Fatalf("previous-turn send must not satisfy current turn; eval=%+v", eval)
	}
	assertMissingRequirement(t, eval, MissingSendAttempt)
}

func TestIntentContract_ExternalSendBlockedAfterMessagingFailure(t *testing.T) {
	contract := mustExternalSendContract(t)
	eval := contract.Evaluate([]llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("给郭松发一下今天的天气信息")},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "send-1", Name: "feishu_api", Arguments: json.RawMessage(`{"action":"send_message","chat_id":"oc_1","content":"天气"}`)}}},
		{Role: "tool", ToolCallID: "send-1", ToolName: "feishu_api", IsError: true, Content: llm.NewTextContent("permission denied")},
	}, llm.ChatWithToolsResponse{Content: "发送失败。", FinishReason: "stop"})

	if eval.Status != ContractBlocked {
		t.Fatalf("Status = %q, want blocked; eval=%+v", eval.Status, eval)
	}
	if eval.Reason != "external_send_tool_failed" {
		t.Fatalf("Reason = %q", eval.Reason)
	}
}

func assertMissingRequirement(t *testing.T, eval ContractEvaluation, want MissingRequirement) {
	t.Helper()
	for _, got := range eval.Missing {
		if got == want {
			return
		}
	}
	t.Fatalf("missing requirement %q not present in %+v", want, eval.Missing)
}

func assertNotMissingRequirement(t *testing.T, eval ContractEvaluation, want MissingRequirement) {
	t.Helper()
	for _, got := range eval.Missing {
		if got == want {
			t.Fatalf("missing requirement %q should not be present in %+v", want, eval.Missing)
		}
	}
}
