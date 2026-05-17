package master

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/observability"
	"github.com/chef-guo/agents-hive/internal/router"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/toolctx"
)

func actionGuardFingerprint(toolName string, args json.RawMessage) string {
	return strings.TrimSpace(strings.ToLower(toolName)) + ":" + hashToolArgs(args)
}

func (m *Master) guardToolExecution(ctx context.Context, session *SessionState, sessionID, userID, toolCallID, toolName string, args json.RawMessage, sessionTraceID, sessionSpanID string, approved map[string]bool) (toolResult, bool) {
	if m.config.SecurityPermissionMode == "strict" {
		if m.hitlBroker == nil || !m.hitlBroker.Enabled() {
			content := recoverableToolCallErrorContentWithHint(recoverableToolCallErrorHint(session, toolName, "strict_hitl_disabled",
				"strict 权限模式需要 HITL，但当前审批通道未启用。请先启用 HITL 后重试。",
				"enable_approval_or_retry"))
			m.recordStrictPermissionBlocked(session, sessionID, userID, toolCallID, toolName, args, sessionTraceID, sessionSpanID, "strict_hitl_disabled")
			return m.recoverableToolCallErrorResult(ctx, sessionID, toolCallID, toolName, args, sessionTraceID, content, "strict_hitl_disabled"), false
		}
		fingerprint := actionGuardFingerprint(toolName, args)
		if approved != nil && approved[fingerprint] {
			return toolResult{}, true
		}
		tr, ok := m.enforceToolExecutionGate(ctx, session, sessionID, toolCallID, toolName, args, sessionTraceID, sessionSpanID)
		if ok && approved != nil {
			approved[fingerprint] = true
		}
		return tr, ok
	}
	if !m.config.ActionGuardEnabled {
		return m.enforceToolExecutionGate(ctx, session, sessionID, toolCallID, toolName, args, sessionTraceID, sessionSpanID)
	}

	// ActionGuard 接管 Master 主路径权限判断；这里仍复用原 runtime allow-list，
	// 但跳过 legacy PermissionManager，避免 shell / 外发动作双审批。
	routeCtx := toolctx.WithSkipPermission(ctx)
	if tr, ok := m.enforceToolExecutionGate(routeCtx, session, sessionID, toolCallID, toolName, args, sessionTraceID, sessionSpanID); !ok {
		return tr, false
	}

	fingerprint := actionGuardFingerprint(toolName, args)
	if approved != nil && approved[fingerprint] {
		return toolResult{}, true
	}

	start := time.Now()
	var route router.RouteDecision
	if session != nil {
		route, _ = session.RouteDecisionSnapshot()
	}
	decision := newDeterministicActionGuard().Decide(ctx, ActionGuardInput{
		SessionID:    sessionID,
		UserID:       userID,
		ToolCallID:   toolCallID,
		ToolName:     toolName,
		Arguments:    args,
		SafeExecutor: m.safeExecutor.Load(),
		ToolDef:      m.actionGuardToolDefinition(toolName),
		Intent:       route.Intent,
		Route:        route,
	})
	latency := time.Since(start)

	switch decision.Action {
	case ActionGuardAllow:
		m.recordActionGuardDecision(session, sessionID, userID, toolCallID, toolName, args, sessionTraceID, sessionSpanID, decision, agentquality.StatusPass, latency, "")
		return toolResult{}, true
	case ActionGuardAsk:
		m.recordActionGuardDecision(session, sessionID, userID, toolCallID, toolName, args, sessionTraceID, sessionSpanID, decision, agentquality.StatusNeedsUser, latency, "")
		if m.hitlBroker == nil || !m.hitlBroker.Enabled() {
			content := recoverableToolCallErrorContentWithHint(recoverableToolCallErrorHint(session, toolName, "action_guard_hitl_disabled",
				fmt.Sprintf("当前工具调用需要人工确认（reason=%s），但审批通道未启用。请先启用 HITL，或在恢复后重新发起审批。", decision.Reason),
				"enable_approval_or_retry"))
			m.emitActionGuardOutcomeEvent(session, sessionID, userID, toolCallID, toolName, args, sessionTraceID, sessionSpanID, decision, agentquality.StatusBlocked, latency, "action_guard_hitl_disabled")
			return m.recoverableToolCallErrorResult(ctx, sessionID, toolCallID, toolName, args, sessionTraceID, content, "action_guard_hitl_disabled"), false
		}
		resp, err := m.requestHITLPermission(toolctx.WithSessionID(ctx, sessionID), skills.PermissionRequest{
			ToolName:    toolName,
			Description: actionGuardPermissionDescription(toolName, args, decision),
			Input:       args,
		}, sessionID)
		if err != nil {
			content := recoverableToolCallErrorContentWithHint(recoverableToolCallErrorHint(session, toolName, "action_guard_approval_request_failed",
				fmt.Sprintf("当前工具调用需要人工确认，但权限确认请求失败，工具未执行。error=%v。请恢复审批通道后重新发起审批。", err),
				"retry_approval_request"))
			m.emitActionGuardOutcomeEvent(session, sessionID, userID, toolCallID, toolName, args, sessionTraceID, sessionSpanID, decision, agentquality.StatusBlocked, latency, err.Error())
			return m.recoverableToolCallErrorResult(ctx, sessionID, toolCallID, toolName, args, sessionTraceID, content, err.Error()), false
		}
		if !resp.Granted {
			content := recoverableToolCallErrorContentWithHint(recoverableToolCallErrorHint(session, toolName, "user_denied_tool_approval",
				fmt.Sprintf("用户没有批准工具 %q，本次调用未执行。请不要重复同一调用；应向用户说明未执行，或根据用户新指令选择替代工具/参数。", toolName),
				"ask_user_or_choose_alternative"))
			m.emitActionGuardOutcomeEvent(session, sessionID, userID, toolCallID, toolName, args, sessionTraceID, sessionSpanID, decision, agentquality.StatusBlocked, latency, "user_denied")
			return m.recoverableToolCallErrorResult(ctx, sessionID, toolCallID, toolName, args, sessionTraceID, content, "user_denied"), false
		}
		if approved != nil {
			approved[fingerprint] = true
		}
		m.emitActionGuardOutcomeEvent(session, sessionID, userID, toolCallID, toolName, args, sessionTraceID, sessionSpanID, decision, agentquality.StatusPass, latency, "")
		return toolResult{}, true
	case ActionGuardDeny:
		content := recoverableToolCallErrorContentWithHint(recoverableToolCallErrorHint(session, toolName, decision.Reason,
			fmt.Sprintf("ActionGuard 未放行当前工具调用（reason=%s），本次调用未执行。请按本轮路由、allowed_inputs 或用户确认路径重构调用。", decision.Reason),
			"rebuild_or_request_approval"))
		m.recordActionGuardDecision(session, sessionID, userID, toolCallID, toolName, args, sessionTraceID, sessionSpanID, decision, agentquality.StatusBlocked, latency, "")
		return m.recoverableToolCallErrorResult(ctx, sessionID, toolCallID, toolName, args, sessionTraceID, content, decision.Reason), false
	case ActionGuardRepair:
		content := recoverableToolCallErrorContentWithHint(recoverableToolCallErrorHint(session, toolName, decision.Reason,
			"当前工具调用与本轮工具/参数约束不一致，未执行。请基于可见工具、allowed_inputs 和用户目标重新构造工具调用，不要重复相同参数。",
			"rebuild_tool_call"))
		m.recordActionGuardDecision(session, sessionID, userID, toolCallID, toolName, args, sessionTraceID, sessionSpanID, decision, agentquality.StatusFail, latency, "")
		return m.recoverableToolCallErrorResult(ctx, sessionID, toolCallID, toolName, args, sessionTraceID, content, decision.Reason), false
	default:
		deny := ActionGuardDecision{Action: ActionGuardDeny, Reason: "unknown_action_guard_decision", Source: "action_guard"}
		content := "[工具调用无法执行: unknown_action_guard_decision]"
		m.recordActionGuardDecision(session, sessionID, userID, toolCallID, toolName, args, sessionTraceID, sessionSpanID, deny, agentquality.StatusBlocked, latency, "")
		return m.actionGuardErrorResult(ctx, sessionID, toolCallID, toolName, args, sessionTraceID, content, deny.Reason), false
	}
}

func (m *Master) actionGuardToolDefinition(toolName string) *mcphost.ToolDefinition {
	def, ok := m.lookupToolDefinition(toolName)
	if !ok {
		return nil
	}
	return &def
}

func actionGuardPermissionDescription(toolName string, args json.RawMessage, decision ActionGuardDecision) string {
	preview := strings.TrimSpace(string(args))
	if len(preview) > 600 {
		preview = preview[:600] + "..."
	}
	return fmt.Sprintf("ActionGuard 请求确认: tool=%s reason=%s pattern=%s args=%s", toolName, decision.Reason, decision.Pattern, preview)
}

func (m *Master) actionGuardErrorResult(ctx context.Context, sessionID, toolCallID, toolName string, args json.RawMessage, turnID, content, errText string) toolResult {
	m.logger.Info("ActionGuard 终止工具执行",
		zap.String("tool", toolName),
		zap.String("reason", errText),
	)
	m.recordToolCallCounter(toolName, args, "error")
	m.recordToolErrorCounter(toolName, args, errText)
	m.emitToolCallEvent(sessionID, ToolCallEvent{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		TurnID:     turnID,
		Status:     "error",
		Error:      errText,
		Terminal:   true,
		SessionID:  sessionID,
	})
	m.logToolCall(ctx, sessionID, llm.ToolCall{ID: toolCallID, Name: toolName, Arguments: args}, string(args), content, true, 0)
	return toolResult{Content: content, IsError: true, Terminal: true}
}

func (m *Master) recordActionGuardDecision(session *SessionState, sessionID, userID, toolCallID, toolName string, args json.RawMessage, traceID, spanID string, decision ActionGuardDecision, status agentquality.FinalStatus, latency time.Duration, errText string) {
	m.emitActionGuardMetrics(toolName, args, decision)
	m.emitActionGuardOutcomeEvent(session, sessionID, userID, toolCallID, toolName, args, traceID, spanID, decision, status, latency, errText)
}

func (m *Master) emitActionGuardOutcomeEvent(session *SessionState, sessionID, userID, toolCallID, toolName string, args json.RawMessage, traceID, spanID string, decision ActionGuardDecision, status agentquality.FinalStatus, latency time.Duration, errText string) {
	toolDecision := agentquality.DecisionAllowed
	if decision.Action == ActionGuardDeny || status == agentquality.StatusBlocked {
		toolDecision = agentquality.DecisionRejected
	}
	if decision.Action == ActionGuardRepair {
		toolDecision = agentquality.DecisionUnexpected
	}
	attrs := map[string]any{
		"tool_name":             toolName,
		"tool_call_id":          toolCallID,
		"action":                decision.Action,
		"reason":                decision.Reason,
		"source":                decision.Source,
		"latency_ms":            latency.Milliseconds(),
		"policy_schema_version": 2,
	}
	if decision.Pattern != "" {
		attrs["pattern"] = decision.Pattern
	}
	if decision.Policy.Source != "" {
		attrs["policy_source"] = decision.Policy.Source
		attrs["policy_action"] = string(decision.Policy.Action)
		attrs["route_status"] = string(decision.Policy.RouteStatus)
		attrs["callable_now"] = decision.Policy.CallableNow
		attrs["requires_approval"] = decision.Policy.RequiresApproval
		attrs["may_require_approval"] = decision.Policy.MayRequireApproval
		attrs["requires_side_effect_intent"] = decision.Policy.RequiresSideEffectIntent
		attrs["risk_class"] = string(decision.Policy.RiskClass)
		attrs["policy_reason"] = decision.Policy.Reason
	}
	if userID != "" {
		attrs["user_id"] = userID
	}
	if errText != "" {
		attrs["error"] = errText
	}
	if decision.Action == ActionGuardRepair {
		attrs["recoverable"] = true
		attrs["repair_action"] = "rebuild_tool_call"
	}
	m.recordPolicySchemaDriftIfMismatch("quality.permission_decision", 2, attrs["policy_schema_version"])
	m.emitQualityEvent(traceID, spanID, sessionID, agentquality.Event{
		Name:        agentquality.EventPermissionDecision,
		Route:       routeFromSession(session),
		FailureType: agentquality.FailurePermission,
		FinalStatus: status,
		ToolDecision: agentquality.ToolDecision{
			Actual:   toolName,
			Decision: toolDecision,
			ArgsHash: hashToolArgs(args),
		},
		Attributes: attrs,
	})
}

func (m *Master) recordPolicySchemaDriftIfMismatch(consumer string, expected int, got any) {
	gotLabel := policySchemaVersionLabel(got)
	expectedLabel := fmt.Sprintf("%d", expected)
	if gotLabel == expectedLabel {
		return
	}
	m.enqueueMetric(observability.Metric{
		Name:  agentquality.MetricPolicySchemaDriftTotal,
		Value: 1,
		Labels: map[string]any{
			"consumer": emptyAsUnknown(consumer),
			"expected": expectedLabel,
			"got":      gotLabel,
		},
		Ts: time.Now(),
	})
}

func policySchemaVersionLabel(value any) string {
	switch v := value.(type) {
	case nil:
		return "missing"
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case json.Number:
		return v.String()
	case string:
		return emptyAsUnknown(v)
	default:
		return fmt.Sprintf("%T", value)
	}
}

func (m *Master) emitActionGuardMetrics(toolName string, args json.RawMessage, decision ActionGuardDecision) {
	m.enqueueMetric(observability.Metric{
		Name:  agentquality.MetricPolicyDecisionTotal,
		Value: 1,
		Labels: map[string]any{
			"action":     emptyAsUnknown(decision.Action),
			"risk_class": emptyAsUnknown(string(decision.Policy.RiskClass)),
			"reason":     emptyAsUnknown(decision.Reason),
		},
		Ts: time.Now(),
	})
	if decision.Action == ActionGuardAsk {
		m.enqueueMetric(observability.Metric{
			Name:  agentquality.MetricActionGuardAskTotal,
			Value: 1,
			Labels: map[string]any{
				"tool":   emptyAsUnknown(toolName),
				"reason": emptyAsUnknown(decision.Reason),
			},
			Ts: time.Now(),
		})
	}
	if decision.Action == ActionGuardAllow && decision.Policy.RiskClass == router.ToolRiskRoutineSideEffect && router.IsRoutinePlainTextExternalSend(toolName, args) {
		m.enqueueMetric(observability.Metric{
			Name:  agentquality.MetricExternalSendRoutineTotal,
			Value: 1,
			Labels: map[string]any{
				"tool":     emptyAsUnknown(toolName),
				"platform": emptyAsUnknown(actionGuardExternalSendPlatform(args)),
			},
			Ts: time.Now(),
		})
	}
}

func actionGuardExternalSendPlatform(input json.RawMessage) string {
	payload, ok := actionGuardObject(input)
	if !ok {
		return ""
	}
	for _, key := range []string{"platform", "provider", "channel"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func emptyAsUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func (m *Master) recordStrictPermissionBlocked(session *SessionState, sessionID, userID, toolCallID, toolName string, args json.RawMessage, traceID, spanID, reason string) {
	attrs := map[string]any{
		"tool_name":             toolName,
		"tool_call_id":          toolCallID,
		"reason":                reason,
		"mode":                  "strict",
		"policy_schema_version": 2,
	}
	if userID != "" {
		attrs["user_id"] = userID
	}
	m.emitQualityEvent(traceID, spanID, sessionID, agentquality.Event{
		Name:        agentquality.EventPermissionDecision,
		Route:       routeFromSession(session),
		FailureType: agentquality.FailurePermission,
		FinalStatus: agentquality.StatusBlocked,
		ToolDecision: agentquality.ToolDecision{
			Actual:   toolName,
			Decision: agentquality.DecisionRejected,
			ArgsHash: hashToolArgs(args),
		},
		Attributes: attrs,
	})
}
