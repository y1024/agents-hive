package router

import (
	"encoding/json"
	"strings"
)

type ToolPolicyAction string

const (
	ToolPolicyAllow ToolPolicyAction = "allow"
	ToolPolicyAsk   ToolPolicyAction = "ask"
	ToolPolicyDeny  ToolPolicyAction = "deny"
)

type ToolRiskClass string

const (
	ToolRiskReadOnly             ToolRiskClass = "read_only"
	ToolRiskRoutineSideEffect    ToolRiskClass = "routine_side_effect"
	ToolRiskPrivilegedSideEffect ToolRiskClass = "privileged_side_effect"
	ToolRiskDestructive          ToolRiskClass = "destructive"
	ToolRiskRuntimeExec          ToolRiskClass = "runtime_exec"
	ToolRiskUnknown              ToolRiskClass = "unknown"
)

type ToolRouteStatus string

const (
	ToolRouteDiscoveryOnly                 ToolRouteStatus = "discovery_only"
	ToolRouteCallableReadOnly              ToolRouteStatus = "callable_read_only"
	ToolRouteCallableWithActionConstraints ToolRouteStatus = "callable_with_action_constraints"
	ToolRouteRequiresSideEffectIntent      ToolRouteStatus = "requires_side_effect_intent"
	ToolRouteRequiresMatchingIntent        ToolRouteStatus = "requires_matching_intent"
	ToolRouteBlockedDangerous              ToolRouteStatus = "blocked_dangerous"
	ToolRouteBlockedUnknown                ToolRouteStatus = "blocked_unknown"
)

type ToolPolicyContext struct {
	Intent    IntentFrame
	Input     json.RawMessage
	ForRoute  bool
	ForAction bool
}

type ToolPolicyDecision struct {
	Action                   ToolPolicyAction `json:"action"`
	RouteStatus              ToolRouteStatus  `json:"route_status"`
	CallableNow              bool             `json:"callable_now"`
	RequiresApproval         bool             `json:"requires_approval"`
	MayRequireApproval       bool             `json:"may_require_approval,omitempty"`
	RequiresSideEffectIntent bool             `json:"requires_side_effect_intent"`
	RiskClass                ToolRiskClass    `json:"risk_class,omitempty"`
	Reason                   string           `json:"reason,omitempty"`
	Source                   string           `json:"source,omitempty"`
	ReadOnly                 bool             `json:"read_only,omitempty"`
	SideEffect               bool             `json:"side_effect,omitempty"`
}

// EvaluateToolPolicy 是工具路由、展示和运行时守卫共享的唯一策略入口。
func EvaluateToolPolicy(profile ToolProfile, ctx ToolPolicyContext) ToolPolicyDecision {
	profile = ToolActionProfile(profile, ctx.Input)
	if profile.Risk == RiskUnknown && profile.Destructive {
		if policyAskableBeforeDeny(profile, ctx) {
			return policyDecision(ToolPolicyAsk, ToolRouteBlockedDangerous, true, ToolRiskDestructive, "dangerous_or_open_world_tool", profile)
		}
		return policyDecision(ToolPolicyDeny, ToolRouteBlockedDangerous, false, ToolRiskDestructive, "dangerous_or_open_world_tool", profile)
	}
	if isDiscoveryOnlyProfile(profile) {
		if ctx.ForAction && isDiscoveryEntrypoint(profile) {
			return policyDecision(ToolPolicyAllow, ToolRouteDiscoveryOnly, true, ToolRiskReadOnly, "discovery_entrypoint", profile)
		}
		return policyDecision(ToolPolicyDeny, ToolRouteDiscoveryOnly, false, riskClassForProfile(profile), "discovery_only", profile)
	}
	if profile.Risk == RiskUnknown {
		if policyAskableBeforeDeny(profile, ctx) {
			return policyDecision(ToolPolicyAsk, ToolRouteBlockedUnknown, true, ToolRiskUnknown, "unknown_tool", profile)
		}
		return policyDecision(ToolPolicyDeny, ToolRouteBlockedUnknown, false, ToolRiskUnknown, "unknown_tool", profile)
	}
	if profile.OpenWorld || profile.Destructive || profile.Risk == RiskDestructive {
		if policyAskableBeforeDeny(profile, ctx) {
			return policyDecision(ToolPolicyAsk, ToolRouteBlockedDangerous, true, ToolRiskDestructive, "dangerous_or_open_world_tool", profile)
		}
		return policyDecision(ToolPolicyDeny, ToolRouteBlockedDangerous, false, ToolRiskDestructive, "dangerous_or_open_world_tool", profile)
	}
	if profile.Risk == RiskRuntimeExec {
		if ctx.Intent.Kind == IntentManageTool && ctx.Intent.AllowsSideEffects {
			return policyDecision(ToolPolicyAsk, ToolRouteRequiresMatchingIntent, true, ToolRiskRuntimeExec, "runtime_exec_under_manage_intent", profile)
		}
		if policyAskableBeforeDeny(profile, ctx) {
			decision := policyDecision(ToolPolicyAsk, ToolRouteBlockedDangerous, true, ToolRiskRuntimeExec, "runtime_exec_requires_confirmation", profile)
			decision.RequiresSideEffectIntent = true
			return decision
		}
		return policyDecision(ToolPolicyDeny, ToolRouteBlockedDangerous, false, ToolRiskRuntimeExec, "runtime_exec_not_allowed", profile)
	}
	if ctx.ForAction && isQuestionEntrypoint(profile) {
		return policyDecision(ToolPolicyAllow, ToolRouteCallableReadOnly, true, ToolRiskReadOnly, "question_entrypoint", profile)
	}
	if ctx.ForAction && isListOnlySkillInvocation(profile, ctx.Input) {
		return policyDecision(ToolPolicyAllow, ToolRouteCallableReadOnly, true, ToolRiskReadOnly, "skill_list", profile)
	}
	if ctx.ForAction && isNamedSkillInvocation(profile, ctx.Input) {
		if skillEntrypointCallableForIntent(ctx.Intent) {
			return policyDecision(ToolPolicyAllow, ToolRouteCallableWithActionConstraints, true, ToolRiskRoutineSideEffect, "skill_invocation_route_allowed", profile)
		}
		decision := policyDecision(ToolPolicyAsk, ToolRouteRequiresMatchingIntent, true, ToolRiskPrivilegedSideEffect, "skill_invocation_requires_route", profile)
		decision.RequiresSideEffectIntent = true
		return decision
	}
	if gate := capabilityGateForPolicy(profile, ctx); !gate.Allowed {
		if policyAskableBeforeDeny(profile, ctx) {
			return policyDecision(ToolPolicyAsk, ToolRouteRequiresMatchingIntent, true, riskClassForProfile(profile), gate.Reason, profile)
		}
		return policyDecision(ToolPolicyDeny, ToolRouteRequiresMatchingIntent, false, riskClassForProfile(profile), gate.Reason, profile)
	}
	if IsMixedReadWriteTool(profile.Name) {
		return evaluateMixedToolPolicy(profile, ctx)
	}
	if profile.Risk == RiskExternalWrite && IsRoutinePlainTextExternalSend(profile.Name, ctx.Input) {
		callable := externalSideEffectCallable(ctx)
		if callable {
			return policyDecision(ToolPolicyAllow, ToolRouteCallableWithActionConstraints, true, ToolRiskRoutineSideEffect, "routine_external_send", profile)
		}
		decision := policyDecision(ToolPolicyAsk, ToolRouteRequiresSideEffectIntent, true, ToolRiskRoutineSideEffect, "external_send_requires_intent", profile)
		decision.RequiresSideEffectIntent = true
		return decision
	}
	if profile.Risk == RiskExternalWrite && !IsMixedReadWriteTool(profile.Name) && len(ctx.Input) > 0 {
		callable := externalSideEffectCallable(ctx)
		decision := policyDecision(ToolPolicyAsk, ToolRouteRequiresSideEffectIntent, callable, ToolRiskPrivilegedSideEffect, "privileged_external_send", profile)
		decision.RequiresSideEffectIntent = !callable
		return decision
	}
	if profile.Invocation == InvocationSkillTool {
		callable := skillWorkflowCallableForIntent(profile, ctx.Intent)
	if !callable && ctx.ForAction && len(ctx.Input) > 0 {
		callable = true
	}
	return policyDecision(ToolPolicyAsk, ToolRouteRequiresMatchingIntent, callable, ToolRiskPrivilegedSideEffect, "requires_matching_intent", profile)
	}
	if profile.ReadOnly && !ProfileHasSideEffect(profile) && profile.Risk == RiskReadOnly {
		return policyDecision(ToolPolicyAllow, ToolRouteCallableReadOnly, true, ToolRiskReadOnly, "read_only", profile)
	}
	if ctx.ForAction && isPlanControlProfile(profile) {
		return policyDecision(ToolPolicyAllow, ToolRouteCallableWithActionConstraints, true, ToolRiskRoutineSideEffect, "plan_control_action", profile)
	}
	if ProfileHasSideEffect(profile) {
		callable := sideEffectCallable(ctx, profile)
		decision := policyDecision(ToolPolicyAsk, ToolRouteRequiresSideEffectIntent, callable, riskClassForProfile(profile), "side_effect_requires_intent", profile)
		decision.RequiresSideEffectIntent = !callable
		return decision
	}
	if policyAskableBeforeDeny(profile, ctx) {
		return policyDecision(ToolPolicyAsk, ToolRouteBlockedUnknown, true, ToolRiskUnknown, "unclassified_tool_policy", profile)
	}
	return policyDecision(ToolPolicyDeny, ToolRouteBlockedUnknown, false, ToolRiskUnknown, "unclassified_tool_policy", profile)
}

func evaluateMixedToolPolicy(profile ToolProfile, ctx ToolPolicyContext) ToolPolicyDecision {
	if StructuredDangerousOperation(profile.Name, ctx.Input) {
		callable := sideEffectCallable(ctx, profile)
		decision := policyDecision(ToolPolicyAsk, ToolRouteRequiresSideEffectIntent, callable, ToolRiskPrivilegedSideEffect, "structured_dangerous_operation", profile)
		decision.RequiresSideEffectIntent = !callable
		return decision
	}

	action := structuredAction(ctx.Input)
	if action != "" {
		switch {
		case containsActionString(MixedReadOnlyActions(profile.Name), action):
			return policyDecision(ToolPolicyAllow, ToolRouteCallableWithActionConstraints, true, ToolRiskReadOnly, "mixed_read_action", profile)
		case containsActionString(RoutineSideEffectActions(profile.Name), action):
			callable := externalSideEffectCallable(ctx)
			routine := IsRoutinePlainTextExternalSend(profile.Name, ctx.Input)
			if callable && routine {
				return policyDecision(ToolPolicyAllow, ToolRouteCallableWithActionConstraints, true, ToolRiskRoutineSideEffect, "routine_external_send_action", profile)
			}
			if !routine {
				decision := policyDecision(ToolPolicyAsk, ToolRouteRequiresSideEffectIntent, callable, ToolRiskPrivilegedSideEffect, "privileged_external_action", profile)
				decision.RequiresSideEffectIntent = !callable
				return decision
			}
			decision := policyDecision(ToolPolicyAsk, ToolRouteRequiresSideEffectIntent, true, ToolRiskRoutineSideEffect, "routine_side_effect_requires_intent", profile)
			decision.RequiresSideEffectIntent = true
			return decision
		case containsActionString(PrivilegedActions(profile.Name), action):
			callable := externalSideEffectCallable(ctx)
			decision := policyDecision(ToolPolicyAsk, ToolRouteRequiresSideEffectIntent, callable, ToolRiskPrivilegedSideEffect, "privileged_external_action", profile)
			decision.RequiresSideEffectIntent = !callable
			return decision
		case containsActionString(ExternalSendActions(profile.Name), action):
			callable := externalSideEffectCallable(ctx)
			decision := policyDecision(ToolPolicyAsk, ToolRouteRequiresSideEffectIntent, callable, ToolRiskPrivilegedSideEffect, "external_send_action", profile)
			decision.RequiresSideEffectIntent = !callable
			return decision
		case containsActionString(MixedLocalWriteActions(profile.Name), action):
			callable := sideEffectCallable(ctx, ToolProfile{Risk: RiskLocalWrite})
			if callable {
				return policyDecision(ToolPolicyAllow, ToolRouteCallableWithActionConstraints, true, ToolRiskRoutineSideEffect, "routine_local_write_action", profile)
			}
			decision := policyDecision(ToolPolicyAsk, ToolRouteRequiresSideEffectIntent, true, ToolRiskRoutineSideEffect, "local_write_requires_intent", profile)
			decision.RequiresSideEffectIntent = true
			return decision
		default:
			if policyAskableBeforeDeny(profile, ctx) {
				return policyDecision(ToolPolicyAsk, ToolRouteBlockedUnknown, true, ToolRiskUnknown, "mixed_action_unknown:"+action, profile)
			}
			return policyDecision(ToolPolicyDeny, ToolRouteBlockedUnknown, false, ToolRiskUnknown, "mixed_action_unknown:"+action, profile)
		}
	}

	if allowed := MixedAllowedToolInputsForIntent(ctx.Intent, profile.Name); len(allowed) > 0 {
		return policyDecision(ToolPolicyAllow, ToolRouteCallableWithActionConstraints, true, ToolRiskReadOnly, "mixed_action_constraints", profile)
	}
	if len(MixedReadOnlyActions(profile.Name)) > 0 {
		return policyDecision(ToolPolicyAllow, ToolRouteCallableWithActionConstraints, true, ToolRiskReadOnly, "mixed_read_actions_available", profile)
	}
	decision := policyDecision(ToolPolicyAsk, ToolRouteRequiresSideEffectIntent, ctx.Intent.AllowsSideEffects, riskClassForProfile(profile), "mixed_side_effect_requires_intent", profile)
	decision.RequiresSideEffectIntent = !decision.CallableNow
	return decision
}

func skillWorkflowCallableForIntent(profile ToolProfile, intent IntentFrame) bool {
	if profile.Kind != CapabilityKindSkillWorkflow || profile.Invocation != InvocationSkillTool {
		return false
	}
	if len(profile.AllowedIntentKinds) > 0 && !containsIntentKind(profile.AllowedIntentKinds, intent.Kind) {
		return false
	}
	rule, ok := skillDomainRule(profile.Domain)
	if !ok || strings.TrimSpace(rule.CallableTool) == "" {
		return false
	}
	return containsIntentKind(rule.AllowedIntentKinds, intent.Kind)
}

func sideEffectIntentAllowsProfile(intent IntentFrame, profile ToolProfile) bool {
	switch profile.Risk {
	case RiskLocalWrite:
		return intent.Kind == IntentWriteLocal || intent.Kind == IntentExternalWrite
	case RiskExternalWrite:
		return intent.Kind == IntentExternalWrite
	default:
		return intent.AllowsSideEffects
	}
}

func externalSideEffectCallable(ctx ToolPolicyContext) bool {
	if ctx.ForAction && ctx.Intent.Kind == "" {
		return true
	}
	return ctx.Intent.Kind == IntentExternalWrite && ctx.Intent.AllowsSideEffects
}

func sideEffectCallable(ctx ToolPolicyContext, profile ToolProfile) bool {
	if ctx.ForAction && ctx.Intent.Kind == "" {
		return true
	}
	return ctx.Intent.AllowsSideEffects && sideEffectIntentAllowsProfile(ctx.Intent, profile)
}

func capabilityGateForPolicy(profile ToolProfile, ctx ToolPolicyContext) CapabilityGateResult {
	if ctx.Intent.Kind == "" {
		return CapabilityGateResult{Allowed: true}
	}
	if capabilityGateDeferredByMissingSideEffectIntent(profile, ctx) {
		return CapabilityGateResult{Allowed: true}
	}
	return CheckCapabilityGate(CapabilityGateInput{
		IntentRequired: RequiredCapabilitiesForIntent(ctx.Intent),
		ToolGranted:    ToolCapabilitiesFromProfile(profile),
	})
}

func capabilityGateDeferredByMissingSideEffectIntent(profile ToolProfile, ctx ToolPolicyContext) bool {
	if profile.ReadOnly && !ProfileHasSideEffect(profile) && profile.Risk == RiskReadOnly {
		return true
	}
	if !ProfileHasSideEffect(profile) {
		return false
	}
	if profile.Risk == RiskRuntimeExec {
		return false
	}
	if profile.OpenWorld || profile.Destructive || profile.Risk == RiskDestructive || profile.Risk == RiskUnknown {
		return false
	}
	return !sideEffectCallable(ctx, profile)
}

func policyAskableBeforeDeny(profile ToolProfile, ctx ToolPolicyContext) bool {
	if sanitizeBlockedProfile(profile) {
		return false
	}
	if isDiscoveryOnlyProfile(profile) {
		return false
	}
	if profile.Name == "" {
		return false
	}
	if ctx.ForAction && len(ctx.Input) > 0 {
		return true
	}
	if ctx.Intent.AllowsSideEffects {
		return true
	}
	return false
}

func sanitizeBlockedProfile(profile ToolProfile) bool {
	if profile.Metadata == nil {
		return false
	}
	return strings.TrimSpace(profile.Metadata["sanitize_blocked"]) == "true"
}

func policyDecision(action ToolPolicyAction, route ToolRouteStatus, callableNow bool, riskClass ToolRiskClass, reason string, profile ToolProfile) ToolPolicyDecision {
	return ToolPolicyDecision{
		Action:                   action,
		RouteStatus:              route,
		CallableNow:              callableNow,
		RequiresApproval:         action == ToolPolicyAsk,
		MayRequireApproval:       riskClassMayRequireApproval(riskClass) || ProfileMayRequireApproval(profile),
		RequiresSideEffectIntent: false,
		RiskClass:                riskClass,
		Reason:                   reason,
		Source:                   "tool_policy",
		ReadOnly:                 profile.ReadOnly && !ProfileHasSideEffect(profile),
		SideEffect:               ProfileHasSideEffect(profile),
	}
}

func riskClassForProfile(profile ToolProfile) ToolRiskClass {
	if profile.OpenWorld || profile.Destructive || profile.Risk == RiskDestructive {
		return ToolRiskDestructive
	}
	switch profile.Risk {
	case RiskReadOnly:
		return ToolRiskReadOnly
	case RiskLocalWrite, RiskExternalWrite:
		return ToolRiskPrivilegedSideEffect
	case RiskRuntimeExec:
		return ToolRiskRuntimeExec
	case RiskUnknown:
		return ToolRiskUnknown
	default:
		if ProfileHasSideEffect(profile) {
			return ToolRiskPrivilegedSideEffect
		}
		return ToolRiskUnknown
	}
}

func riskClassMayRequireApproval(riskClass ToolRiskClass) bool {
	switch riskClass {
	case ToolRiskPrivilegedSideEffect, ToolRiskDestructive, ToolRiskRuntimeExec, ToolRiskUnknown:
		return true
	default:
		return false
	}
}

func containsIntentKind(values []IntentKind, want IntentKind) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
