package toolruntime

import (
	"encoding/json"
	"strings"

	"github.com/chef-guo/agents-hive/internal/router"
)

const (
	ExecutionActionAllow  = "allow"
	ExecutionActionAsk    = "ask"
	ExecutionActionDeny   = "deny"
	ExecutionActionRepair = "repair"
)

// ExecutionDecision 是运行时执行前的统一 allow/ask/deny 投影。
type ExecutionDecision struct {
	Action               string                    `json:"action"`
	Reason               string                    `json:"reason,omitempty"`
	Source               string                    `json:"source,omitempty"`
	RequiresConfirmation bool                      `json:"requires_confirmation,omitempty"`
	Policy               router.ToolPolicyDecision `json:"policy"`
}

// DecideExecution 将 ToolPolicyDecision 投影成运行时动作。
func DecideExecution(descriptor Descriptor, invocation Invocation) ExecutionDecision {
	if reason := RouteInputDenyReason(invocation); reason != "" {
		policy := router.ToolPolicyDecision{
			Action:           router.ToolPolicyAsk,
			RouteStatus:      router.ToolRouteRequiresMatchingIntent,
			CallableNow:      false,
			RequiresApproval: false,
			Reason:           reason,
			Source:           "route_decision",
			RiskClass:        router.ToolRiskUnknown,
		}
		return ExecutionDecision{Action: ExecutionActionRepair, Reason: reason, Source: "route_decision", Policy: policy}
	}
	policy := router.EvaluateToolPolicy(descriptor.Profile, router.ToolPolicyContext{
		Intent:    invocationPolicyIntent(invocation),
		Input:     invocation.Arguments,
		ForAction: true,
	})
	switch policy.Action {
	case router.ToolPolicyAllow:
		return ExecutionDecision{Action: ExecutionActionAllow, Reason: policy.Reason, Source: "tool_policy", Policy: policy}
	case router.ToolPolicyAsk:
		return ExecutionDecision{Action: ExecutionActionAsk, Reason: policy.Reason, Source: "tool_policy", RequiresConfirmation: true, Policy: policy}
	default:
		if policy.RouteStatus == router.ToolRouteDiscoveryOnly || policy.RouteStatus == router.ToolRouteRequiresMatchingIntent {
			return ExecutionDecision{Action: ExecutionActionRepair, Reason: policy.Reason, Source: "tool_policy", Policy: policy}
		}
		return ExecutionDecision{Action: ExecutionActionAsk, Reason: policy.Reason, Source: "tool_policy", RequiresConfirmation: true, Policy: policy}
	}
}

func RouteInputDenyReason(invocation Invocation) string {
	if invocation.Name == "" || len(invocation.Arguments) == 0 || len(invocation.Route.AllowedToolInputs) == 0 {
		return ""
	}
	allowed := invocation.Route.AllowedToolInputs[invocation.Name]
	if len(allowed) == 0 {
		return ""
	}
	for key, allowedValues := range allowed {
		actuals, ok := routeInputValues(invocation.Arguments, key)
		if !ok || !matchesAllowedRouteValues(actuals, allowedValues) {
			return "route_input_denied"
		}
	}
	return ""
}

func routeInputValues(args json.RawMessage, key string) ([]string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false
	}
	if strings.HasSuffix(key, "[].action") {
		arrayKey := strings.TrimSuffix(key, "[].action")
		return routeInputArrayStringField(args, arrayKey, "action")
	}
	if value, ok := routeInputString(args, key); ok {
		return []string{value}, true
	}
	return nil, false
}

func routeInputString(args json.RawMessage, key string) (string, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(args, &payload); err != nil {
		return "", false
	}
	raw, ok := payload[key]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func routeInputArrayStringField(args json.RawMessage, arrayKey, field string) ([]string, bool) {
	arrayKey = strings.TrimSpace(arrayKey)
	field = strings.TrimSpace(field)
	if arrayKey == "" || field == "" {
		return nil, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(args, &payload); err != nil {
		return nil, false
	}
	raw, ok := payload[arrayKey]
	if !ok {
		return nil, false
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return nil, false
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item[field].(string)
		if !ok {
			return nil, false
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func matchesAllowedRouteValues(actuals []string, allowedValues string) bool {
	if len(actuals) == 0 {
		return false
	}
	for _, actual := range actuals {
		if !matchesSingleAllowedRouteValue(actual, allowedValues) {
			return false
		}
	}
	return true
}

func matchesSingleAllowedRouteValue(actual, allowedValues string) bool {
	for _, allowed := range strings.Split(allowedValues, "|") {
		if strings.TrimSpace(actual) == strings.TrimSpace(allowed) {
			return true
		}
	}
	return false
}

// DecideExecutionWithArgs 保留旧调用点兼容。新执行路径应传 Invocation，
// 让统一策略层能消费本轮 intent / route 上下文。
func DecideExecutionWithArgs(descriptor Descriptor, args json.RawMessage) ExecutionDecision {
	return DecideExecution(descriptor, Invocation{Arguments: args})
}

func invocationPolicyIntent(invocation Invocation) router.IntentFrame {
	if invocation.Intent.Kind != "" {
		return invocation.Intent
	}
	if invocation.Route.Intent.Kind != "" {
		return invocation.Route.Intent
	}
	return router.IntentFrame{}
}
