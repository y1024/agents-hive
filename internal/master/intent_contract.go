package master

import (
	"encoding/json"
	"strings"

	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/router"
)

type IntentContractKind string

const (
	IntentContractExternalSend IntentContractKind = "external_send"
)

type ContractStatus string

const (
	ContractSatisfied  ContractStatus = "satisfied"
	ContractNeedsUser  ContractStatus = "needs_user"
	ContractBlocked    ContractStatus = "blocked"
	ContractIncomplete ContractStatus = "incomplete"
)

type MissingRequirement string

const (
	MissingContent     MissingRequirement = "content"
	MissingRecipient   MissingRequirement = "recipient"
	MissingSendAttempt MissingRequirement = "send_attempt"
	MissingSendPayload MissingRequirement = "send_payload"
	MissingQuestion    MissingRequirement = "question"
)

type IntentContract struct {
	Kind   IntentContractKind
	Intent router.IntentFrame
}

type ContractEvaluation struct {
	Status   ContractStatus
	Reason   string
	Missing  []MissingRequirement
	Evidence ContractEvidence
}

type ContractEvidence struct {
	ContentPrepared    bool
	LookupAttempted    bool
	RecipientResolved  bool
	RecipientAmbiguous bool
	RecipientMissing   bool
	QuestionAsked      bool
	SendAttempted      bool
	SendAttemptValid   bool
	MessagingFailed    bool
	MessagingTool      string
}

func NewIntentContract(intent router.IntentFrame) (IntentContract, bool) {
	if intent.Kind == router.IntentExternalWrite && intent.AllowsSideEffects && intent.RequiresExternal {
		return IntentContract{Kind: IntentContractExternalSend, Intent: intent}, true
	}
	return IntentContract{}, false
}

func (c IntentContract) Evaluate(messages []llm.MessageWithTools, response llm.ChatWithToolsResponse) ContractEvaluation {
	if c.Kind != IntentContractExternalSend {
		return ContractEvaluation{Status: ContractSatisfied, Reason: "no_contract"}
	}
	current := messagesFromLatestUser(messages)
	if len(response.ToolCalls) > 0 {
		current = append(current, llm.MessageWithTools{Role: "assistant", ToolCalls: response.ToolCalls})
	}
	return evaluateExternalSendContract(current)
}

func evaluateExternalSendContract(messages []llm.MessageWithTools) ContractEvaluation {
	evidence := collectExternalSendEvidence(messages)
	if evidence.MessagingFailed {
		return ContractEvaluation{Status: ContractBlocked, Reason: "external_send_tool_failed", Evidence: evidence}
	}
	if evidence.RecipientAmbiguous && evidence.QuestionAsked {
		return ContractEvaluation{Status: ContractNeedsUser, Reason: "external_send_recipient_ambiguous", Evidence: evidence}
	}
	missing := missingExternalSendRequirements(evidence)
	if len(missing) > 0 {
		return ContractEvaluation{Status: ContractIncomplete, Reason: externalSendIncompleteReason(evidence, missing), Missing: missing, Evidence: evidence}
	}
	return ContractEvaluation{Status: ContractSatisfied, Reason: "external_send_satisfied", Evidence: evidence}
}

func missingExternalSendRequirements(e ContractEvidence) []MissingRequirement {
	var missing []MissingRequirement
	if !e.ContentPrepared {
		missing = append(missing, MissingContent)
	}
	if !e.RecipientResolved {
		missing = append(missing, MissingRecipient)
	}
	if e.RecipientAmbiguous && !e.QuestionAsked {
		missing = append(missing, MissingQuestion)
	}
	if !e.SendAttempted {
		missing = append(missing, MissingSendAttempt)
	} else if !e.SendAttemptValid {
		missing = append(missing, MissingSendPayload)
	}
	return missing
}

func externalSendIncompleteReason(e ContractEvidence, missing []MissingRequirement) string {
	if e.RecipientAmbiguous && !e.QuestionAsked {
		return "external_send_ambiguous_recipient_without_question"
	}
	if len(missing) == 1 && missing[0] == MissingSendAttempt {
		return "external_send_missing_send_attempt"
	}
	for _, requirement := range missing {
		if requirement == MissingSendPayload {
			return "external_send_missing_send_payload"
		}
	}
	return "external_send_missing_recipient_or_send"
}

type externalSendCallKind string

const (
	externalSendCallUnknown  externalSendCallKind = ""
	externalSendCallLookup   externalSendCallKind = "lookup"
	externalSendCallSend     externalSendCallKind = "send"
	externalSendCallQuestion externalSendCallKind = "question"
)

type externalSendCallFact struct {
	Name string
	Kind externalSendCallKind
}

func collectExternalSendEvidence(messages []llm.MessageWithTools) ContractEvidence {
	var evidence ContractEvidence
	callsByID := map[string]externalSendCallFact{}
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			fact := classifyExternalSendToolCall(call)
			if fact.Kind == externalSendCallUnknown {
				continue
			}
			if call.ID != "" {
				callsByID[call.ID] = fact
			}
			switch fact.Kind {
			case externalSendCallLookup:
				evidence.LookupAttempted = true
			case externalSendCallQuestion:
				evidence.QuestionAsked = true
			case externalSendCallSend:
				evidence.SendAttempted = true
				evidence.MessagingTool = fact.Name
				hasContent, hasRecipient := sendCallPayloadFacts(call)
				if hasContent {
					evidence.ContentPrepared = true
				}
				if hasRecipient {
					evidence.RecipientResolved = true
				}
				if hasContent && hasRecipient {
					evidence.SendAttemptValid = true
				}
			}
		}
		if msg.Role != "tool" {
			continue
		}
		fact := callsByID[msg.ToolCallID]
		if toolResultCanPrepareExternalSendContent(msg, fact) {
			evidence.ContentPrepared = true
		}
		if fact.Kind == externalSendCallLookup {
			applyContactLookupEvidence(&evidence, msg.Content.Text())
		}
		if msg.IsError && (fact.Kind == externalSendCallSend || isExternalMessagingTool(msg.ToolName)) {
			evidence.MessagingFailed = true
			if evidence.MessagingTool == "" {
				evidence.MessagingTool = msg.ToolName
			}
		}
	}
	return evidence
}

func toolResultCanPrepareExternalSendContent(msg llm.MessageWithTools, fact externalSendCallFact) bool {
	if msg.IsError || strings.TrimSpace(msg.ToolName) == "" {
		return false
	}
	switch fact.Kind {
	case externalSendCallLookup, externalSendCallSend, externalSendCallQuestion:
		return false
	default:
		return !isExternalMessagingTool(msg.ToolName)
	}
}

func classifyExternalSendToolCall(call llm.ToolCall) externalSendCallFact {
	name := strings.TrimSpace(call.Name)
	switch name {
	case "question":
		return externalSendCallFact{Name: name, Kind: externalSendCallQuestion}
	case "send_im_message":
		return externalSendCallFact{Name: name, Kind: externalSendCallSend}
	case "feishu_api":
		switch toolActionFromArgs(call.Arguments) {
		case "search_contacts", "get_user_info", "get_chat_info", "list_chat_members":
			return externalSendCallFact{Name: name, Kind: externalSendCallLookup}
		case "send_message", "send_image", "send_file":
			return externalSendCallFact{Name: name, Kind: externalSendCallSend}
		}
	}
	return externalSendCallFact{Name: name}
}

func toolActionFromArgs(args json.RawMessage) string {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Action)
}

func sendCallPayloadFacts(call llm.ToolCall) (bool, bool) {
	var payload map[string]any
	if err := json.Unmarshal(call.Arguments, &payload); err != nil {
		return false, false
	}
	hasContent := firstNonEmptyStringFromMap(payload, "content", "text", "message") != ""
	hasRecipient := firstNonEmptyStringFromMap(payload, "chat_id", "open_id", "user_id", "receive_id", "email", "recipient", "to") != ""
	return hasContent, hasRecipient
}

func sendCallHasContentAndRecipient(call llm.ToolCall) bool {
	hasContent, hasRecipient := sendCallPayloadFacts(call)
	return hasContent && hasRecipient
}

func firstNonEmptyStringFromMap(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func applyContactLookupEvidence(e *ContractEvidence, content string) {
	count, ok := countContactCandidates(content)
	if !ok {
		return
	}
	switch {
	case count == 0:
		e.RecipientMissing = true
	case count == 1:
		e.RecipientResolved = true
	case count > 1:
		e.RecipientAmbiguous = true
	}
}

func countContactCandidates(content string) (int, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return 0, false
	}
	for _, key := range []string{"contacts", "users", "items", "results"} {
		if n, ok := jsonArrayLen(payload[key]); ok {
			return n, true
		}
	}
	if raw := payload["data"]; len(raw) > 0 {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err == nil {
			for _, key := range []string{"contacts", "users", "items", "results"} {
				if n, ok := jsonArrayLen(nested[key]); ok {
					return n, true
				}
			}
		}
	}
	return 0, false
}

func jsonArrayLen(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return 0, false
	}
	return len(arr), true
}

func isExternalMessagingTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "feishu_api", "send_im_message":
		return true
	default:
		return false
	}
}

func messagesFromLatestUser(messages []llm.MessageWithTools) []llm.MessageWithTools {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i:]
		}
	}
	return messages
}
