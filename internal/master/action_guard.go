package master

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/router"
	"github.com/chef-guo/agents-hive/internal/security"
	"github.com/chef-guo/agents-hive/internal/toolruntime"
)

const (
	ActionGuardAllow  = "allow"
	ActionGuardAsk    = "ask"
	ActionGuardDeny   = "deny"
	ActionGuardRepair = "repair"
)

// ActionGuardInput 是确定性动作守卫的最小输入。
type ActionGuardInput struct {
	SessionID  string
	UserID     string
	ToolCallID string
	ToolName   string
	Arguments  json.RawMessage

	SafeExecutor *security.SafeExecutor
	ToolDef      *mcphost.ToolDefinition
	Intent       router.IntentFrame
	Route        router.RouteDecision
}

// ActionGuardDecision 描述一次工具调用的确定性安全决策。
type ActionGuardDecision struct {
	Action               string
	Reason               string
	Source               string
	Pattern              string
	RequiresConfirmation bool
	Policy               router.ToolPolicyDecision
}

// ActionGuard 不依赖 LLM，按工具名和结构化参数作确定性 allow/ask/deny。
type ActionGuard interface {
	Decide(context.Context, ActionGuardInput) ActionGuardDecision
}

type deterministicActionGuard struct{}

var unsafeSQLPattern = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|truncate|alter|create|replace|merge|grant|revoke|vacuum|copy|call|exec|execute)\b`)

func newDeterministicActionGuard() ActionGuard {
	return deterministicActionGuard{}
}

func (deterministicActionGuard) Decide(_ context.Context, input ActionGuardInput) ActionGuardDecision {
	toolName := strings.TrimSpace(strings.ToLower(input.ToolName))
	if toolName == "" {
		return actionGuardDecision(ActionGuardRepair, "empty_tool_name", "policy", "")
	}

	if router.IsShellCommandTool(toolName) {
		return decideShellAction(input)
	}

	descriptor, ok := actionGuardDescriptor(toolName, input.ToolDef)
	if !ok {
		return actionGuardDecision(ActionGuardRepair, "unknown_tool", "policy", "")
	}

	return actionGuardDecisionFromRuntime(toolName, input.Arguments, toolruntime.DecideExecution(descriptor, toolruntime.Invocation{
		Name:      toolName,
		Arguments: input.Arguments,
		Intent:    input.Intent,
		Route:     input.Route,
	}))
}

func actionGuardDescriptor(toolName string, def *mcphost.ToolDefinition) (toolruntime.Descriptor, bool) {
	if def != nil {
		return toolruntime.DescriptorFromDefinition(*def), true
	}
	profile, ok := router.BuiltinToolProfile(toolName)
	if !ok {
		return toolruntime.Descriptor{}, false
	}
	return toolruntime.Descriptor{
		Definition: mcphost.ToolDefinition{Name: toolName, Core: true},
		Profile:    profile,
		Entry:      profile.Entry(),
	}, true
}

func actionGuardDecisionFromRuntime(toolName string, args json.RawMessage, decision toolruntime.ExecutionDecision) ActionGuardDecision {
	if reason, invalid := invalidIMSendAction(toolName, args); invalid {
		out := actionGuardDecision(ActionGuardRepair, reason, "tool_policy", "")
		out.Policy = decision.Policy
		return out
	}
	if decision.Action == toolruntime.ExecutionActionDeny {
		out := actionGuardDecision(ActionGuardAsk, decision.Reason, decision.Source, "")
		out.Policy = decision.Policy
		return out
	}
	if decision.Action == toolruntime.ExecutionActionRepair {
		out := actionGuardDecision(ActionGuardRepair, decision.Reason, decision.Source, "")
		out.Policy = decision.Policy
		return out
	}
	if toolArgumentsRequireApproval(toolName, args) {
		out := actionGuardDecision(ActionGuardAsk, "argument_side_effect", "tool_policy", "")
		out.Policy = decision.Policy
		return out
	}
	if router.StructuredDangerousOperation(toolName, args) {
		out := actionGuardDecision(ActionGuardAsk, "structured_dangerous_operation", "tool_policy", "")
		out.Policy = decision.Policy
		return out
	}
	switch decision.Action {
	case toolruntime.ExecutionActionAllow:
		out := actionGuardDecision(ActionGuardAllow, decision.Reason, decision.Source, "")
		out.Policy = decision.Policy
		return out
	case toolruntime.ExecutionActionAsk:
		out := actionGuardDecision(ActionGuardAsk, decision.Reason, decision.Source, "")
		out.Policy = decision.Policy
		return out
	default:
		out := actionGuardDecision(ActionGuardRepair, decision.Reason, decision.Source, "")
		out.Policy = decision.Policy
		return out
	}
}

func decideShellAction(input ActionGuardInput) ActionGuardDecision {
	cmd, ok := extractShellCommand(input.Arguments)
	if !ok {
		return actionGuardDecision(ActionGuardRepair, "malformed_shell_input", "safe_executor", "")
	}
	if input.SafeExecutor == nil {
		return actionGuardDecision(ActionGuardAsk, "safe_executor_missing", "safe_executor", "")
	}

	policy, pattern := input.SafeExecutor.MatchPolicyWithRule(cmd)
	switch policy {
	case security.PolicyDeny:
		return actionGuardDecision(ActionGuardAsk, "shell_policy_ask", "safe_executor", pattern)
	case security.PolicyAsk:
		return actionGuardDecision(ActionGuardAsk, "shell_policy_ask", "safe_executor", pattern)
	case security.PolicyAllow:
		return actionGuardDecision(ActionGuardAllow, "shell_policy_allow", "safe_executor", pattern)
	default:
		return actionGuardDecision(ActionGuardRepair, "shell_policy_unknown", "safe_executor", pattern)
	}
}

func toolArgumentsRequireApproval(toolName string, input json.RawMessage) bool {
	if len(input) == 0 {
		return false
	}
	var payload any
	if err := json.Unmarshal(input, &payload); err != nil {
		return false
	}
	return toolValueRequiresApproval(toolName, "", payload)
}

func toolValueRequiresApproval(toolName, key string, value any) bool {
	keyLower := strings.ToLower(strings.TrimSpace(key))
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			if toolValueRequiresApproval(toolName, k, child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if toolValueRequiresApproval(toolName, keyLower, child) {
				return true
			}
		}
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return false
		}
		if keyLower == "sql" || strings.Contains(keyLower, "query") || strings.Contains(keyLower, "statement") {
			return unsafeSQLPattern.MatchString(text)
		}
		if toolActionKeyRequiresApproval(keyLower) {
			action := strings.ToLower(text)
			if router.StructuredDangerousAction(toolName, action) {
				return true
			}
			if router.StructuredRoutineSideEffectAction(toolName, action) {
				return false
			}
			return toolActionLooksDangerous(action)
		}
	}
	return false
}

func toolActionKeyRequiresApproval(key string) bool {
	switch key {
	case "action", "operation", "op", "command", "cmd", "method", "mutation":
		return true
	default:
		return false
	}
}

func toolActionLooksDangerous(action string) bool {
	tokens := strings.FieldsFunc(strings.ToLower(action), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	for _, keyword := range []string{
		"delete", "drop", "truncate", "update", "insert", "write", "create", "send", "publish", "deploy", "restart", "exec", "execute", "shell",
	} {
		for _, token := range tokens {
			if token == keyword {
				return true
			}
		}
	}
	return false
}

func invalidIMSendAction(toolName string, input json.RawMessage) (string, bool) {
	if !isIMSendMessageAction(toolName, input) {
		return "", false
	}
	payload, ok := actionGuardObject(input)
	if !ok {
		return "im_send_missing_content", true
	}
	if !actionGuardHasString(payload, "content", "text", "message") {
		return "im_send_missing_content", true
	}
	if !actionGuardHasRecipientSignal(payload) {
		return "im_send_missing_recipient", true
	}
	return "", false
}

func isIMSendMessageAction(toolName string, input json.RawMessage) bool {
	switch toolName {
	case "send_im_message":
		return true
	case "feishu_api", "im_api":
		return actionGuardStructuredAction(input) == "send_message"
	default:
		return false
	}
}

func actionGuardObject(input json.RawMessage) (map[string]any, bool) {
	if len(input) == 0 {
		return nil, false
	}
	var payload map[string]any
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, false
	}
	return payload, true
}

func actionGuardHasString(payload map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func actionGuardHasRecipientSignal(payload map[string]any) bool {
	for _, key := range []string{"receive_id", "chat_id", "open_id", "user_id", "email", "recipient", "to", "recipient_id", "conversation_id"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return true
		}
		if value, ok := payload[key]; ok && actionGuardNonEmptyValue(value) {
			return true
		}
	}
	for _, key := range []string{"receive_ids", "chat_ids", "open_ids", "user_ids", "emails", "recipients", "recipient_ids", "tos", "to_list", "conversation_ids", "targets", "target_ids"} {
		if value, ok := payload[key]; ok && actionGuardNonEmptyValue(value) {
			return true
		}
	}
	return false
}

func actionGuardNonEmptyValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case bool:
		return v
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	case float64:
		return v != 0
	default:
		return true
	}
}

func actionGuardStructuredAction(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var payload struct {
		Action    string `json:"action"`
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return ""
	}
	if payload.Action != "" {
		return strings.TrimSpace(strings.ToLower(payload.Action))
	}
	return strings.TrimSpace(strings.ToLower(payload.Operation))
}

func actionGuardDecision(action, reason, source, pattern string) ActionGuardDecision {
	return ActionGuardDecision{
		Action:               action,
		Reason:               reason,
		Source:               source,
		Pattern:              pattern,
		RequiresConfirmation: action == ActionGuardAsk,
	}
}
