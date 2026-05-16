package master

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/journal"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/router"
	"github.com/chef-guo/agents-hive/internal/store"
)

// AgentQualityRunAdapter binds agentquality's eval runner to the production master path.
type AgentQualityRunAdapter struct {
	Master *Master
}

func NewAgentQualityRunAdapter(m *Master) AgentQualityRunAdapter {
	return AgentQualityRunAdapter{Master: m}
}

func (a AgentQualityRunAdapter) RunCase(ctx context.Context, input agentquality.AgentRunCaseInput) (agentquality.AgentRunCaseOutput, error) {
	if a.Master == nil {
		return agentquality.AgentRunCaseOutput{}, fmt.Errorf("master not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(input.Case.Input) == "" {
		return agentquality.AgentRunCaseOutput{}, fmt.Errorf("case input is required")
	}
	if violation := a.preflightSandboxViolation(input); violation != "" {
		traceID := strings.TrimSpace(input.RunID)
		if traceID == "" {
			traceID = strings.TrimSpace(input.SessionID)
		}
		blockedTool := sandboxBlockedToolName(input.Case)
		return agentquality.AgentRunCaseOutput{
			FinalOutput: violation,
			FinalStatus: agentquality.StatusBlocked,
			TraceID:     traceID,
			ReplayRef:   "sandbox:blocked",
			ToolCalls: []agentquality.ObservedToolCall{
				{
					ToolName:   blockedTool,
					Status:     "blocked",
					Error:      violation,
					SideEffect: true,
				},
			},
			Events: []agentquality.Event{
				{
					Name:        agentquality.EventToolDecision,
					CaseID:      input.Case.ID,
					RunID:       input.RunID,
					TraceID:     traceID,
					TurnID:      traceID,
					DomainID:    firstNonEmptyString(input.Case.DomainID, input.DomainID),
					SourceKind:  firstNonEmptyString(input.Case.SourceKind, "master"),
					SourceName:  firstNonEmptyString(input.Case.SourceName, "agent_quality_runner"),
					Route:       firstNonEmptyString(input.Case.Route, routeFromSessionID(input.SessionID)),
					FailureType: agentquality.FailurePermission,
					FinalStatus: agentquality.StatusBlocked,
					ToolDecision: agentquality.ToolDecision{
						Actual:   blockedTool,
						Decision: agentquality.DecisionRejected,
					},
					ReplayRef: "sandbox:blocked",
					Attributes: map[string]any{
						"reason":           violation,
						"sandbox_external": true,
					},
					Ts: time.Now(),
				},
			},
		}, nil
	}

	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		sessionID = "eval-" + strings.TrimSpace(input.RunID) + "-" + strings.TrimSpace(input.Case.ID)
	}
	if err := a.Master.EnsureEvalSession(ctx, sessionID, evalSessionName(input)); err != nil {
		return agentquality.AgentRunCaseOutput{}, err
	}

	traceID := strings.TrimSpace(input.RunID)
	if traceID == "" {
		traceID = sessionID
	}
	caseCtx := agentquality.WithContextValue(ctx, agentquality.ContextValue{
		CaseID:      input.Case.ID,
		FailureType: input.Case.FailureType,
		FinalStatus: input.Case.ExpectedStatus,
	})
	if userID := strings.TrimSpace(input.OwnerID); userID != "" {
		caseCtx = auth.WithUser(caseCtx, &auth.User{ID: userID, Role: "admin", Status: "active"})
	}

	start := time.Now()
	resp, err := a.Master.ProcessMessageWithOptions(caseCtx, sessionID, input.Case.Input, WithTurnID(traceID))
	if err == nil && strings.TrimSpace(resp.Error) != "" {
		err = fmt.Errorf("%s", resp.Error)
	}
	duration := time.Since(start)
	output := agentquality.AgentRunCaseOutput{
		FinalOutput: resp.Content,
		FinalStatus: taskResponseFinalStatus(resp, err),
		TraceID:     traceID,
		ReplayRef:   "session:" + sessionID,
	}
	output.ToolCalls = append(output.ToolCalls, a.Master.ObservedToolCalls(ctx, sessionID)...)
	output.Events = append(output.Events, agentQualityEventsFromToolCalls(input, output.ToolCalls)...)
	output.Events = append(output.Events, agentquality.Event{
		Name:          agentquality.EventAgentTurn,
		CaseID:        input.Case.ID,
		SessionIDHash: qualitySessionHash(sessionID),
		RunID:         input.RunID,
		TraceID:       traceID,
		TurnID:        traceID,
		DomainID:      firstNonEmptyString(input.Case.DomainID, input.DomainID),
		SourceKind:    firstNonEmptyString(input.Case.SourceKind, "master"),
		SourceName:    firstNonEmptyString(input.Case.SourceName, "agent_quality_runner"),
		OwnerScope:    agentquality.OwnerScopeUser,
		OwnerID:       strings.TrimSpace(input.OwnerID),
		UserID:        strings.TrimSpace(input.OwnerID),
		Route:         firstNonEmptyString(input.Case.Route, routeFromSessionID(sessionID)),
		FailureType:   failureTypeFromStatus(output.FinalStatus, input.Case.FailureType),
		FinalStatus:   output.FinalStatus,
		ReplayRef:     output.ReplayRef,
		Attributes: map[string]any{
			"duration_ms": duration.Milliseconds(),
			"runner":      "agent_run",
		},
		Ts: time.Now(),
	})
	if err != nil {
		return output, err
	}
	return output, nil
}

func (a AgentQualityRunAdapter) preflightSandboxViolation(input agentquality.AgentRunCaseInput) string {
	if !input.SandboxExternal {
		return ""
	}
	allowed := stringSet(input.AllowedSideEffects)
	for _, tool := range sideEffectToolsForCase(input.Case) {
		if allowed[tool] {
			continue
		}
		return fmt.Sprintf("side effect tool %s blocked before production agent run: case must explicitly declare allowed side effect", tool)
	}
	if intent, ok := sandboxSideEffectIntent(input.Case.Input); ok {
		return fmt.Sprintf("%s intent blocked before production agent run: case must explicitly declare a side effect tool and allowed side effect", intent)
	}
	return ""
}

func sideEffectToolsForCase(c agentquality.Case) []string {
	seen := make(map[string]bool, len(c.ExpectedTools)+len(c.AllowedTools))
	out := make([]string, 0, len(c.ExpectedTools)+len(c.AllowedTools))
	for _, tool := range append(append([]string(nil), c.ExpectedTools...), c.AllowedTools...) {
		tool = strings.TrimSpace(tool)
		if tool == "" || seen[tool] || !toolHasSideEffect(tool) {
			continue
		}
		seen[tool] = true
		out = append(out, tool)
	}
	return out
}

func sandboxSideEffectIntent(query string) (string, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", false
	}
	if isExplicitExternalSendRequest(query) {
		return "external_write", true
	}
	classified := router.IntentFrame{}
	if recovered, ok := recoverExplicitSideEffectIntent(query, classified); ok && recovered.AllowsSideEffects {
		return string(recovered.Kind), true
	}
	return "", false
}

func sandboxBlockedToolName(c agentquality.Case) string {
	if tools := sideEffectToolsForCase(c); len(tools) > 0 {
		return strings.Join(tools, ",")
	}
	if intent, ok := sandboxSideEffectIntent(c.Input); ok {
		return intent
	}
	return "side_effect"
}

func (m *Master) EnsureEvalSession(ctx context.Context, sessionID, name string) error {
	if m == nil {
		return fmt.Errorf("master not configured")
	}
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	session := m.sessionMgr.GetSession(sessionID)
	if session != nil {
		return nil
	}
	if m.store != nil {
		now := time.Now()
		record := &store.SessionRecord{
			ID:             sessionID,
			Name:           firstNonEmptyString(name, "agent-quality-eval"),
			CreatedAt:      now.Format(time.RFC3339),
			UpdatedAt:      now.Format(time.RFC3339),
			LastAccessedAt: now.Format(time.RFC3339),
			Tags:           []string{"agent_quality_eval"},
			UserID:         auth.UserIDFrom(ctx),
		}
		if err := m.store.CreateSession(ctx, record); err != nil {
			if _, loadErr := m.store.LoadSession(ctx, sessionID); loadErr != nil {
				return err
			}
		}
	}
	session = &SessionState{
		ID:           sessionID,
		Name:         firstNonEmptyString(name, "agent-quality-eval"),
		Messages:     []llm.MessageWithTools{},
		Metadata:     map[string]any{"agent_quality_eval": true},
		Tags:         []string{"agent_quality_eval"},
		UserID:       auth.UserIDFrom(ctx),
		Created:      time.Now(),
		LastAccessed: time.Now(),
	}
	m.sessionMgr.SetSession(session)
	return nil
}

func (m *Master) ObservedToolCalls(ctx context.Context, sessionID string) []agentquality.ObservedToolCall {
	if m == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	observed := make([]agentquality.ObservedToolCall, 0)
	if m.journal != nil {
		if sj, err := m.journal.GetJournal(ctx, sessionID, 0); err == nil && sj != nil {
			for _, call := range sj.ToolCalls {
				observed = append(observed, observedToolCallFromJournal(call))
			}
		}
	}
	if session := m.sessionMgr.GetSession(sessionID); session != nil {
		session.mu.RLock()
		messages := append([]llm.MessageWithTools(nil), session.Messages...)
		session.mu.RUnlock()
		observed = append(observed, observedToolCallsFromMessages(messages)...)
	}
	return dedupeObservedToolCalls(observed)
}

func observedToolCallFromJournal(call journal.ToolCallEntry) agentquality.ObservedToolCall {
	status := "success"
	if call.IsError {
		status = "error"
	}
	return agentquality.ObservedToolCall{
		ToolName:   strings.TrimSpace(call.ToolName),
		ArgsHash:   hashToolArgsText(call.Arguments),
		Status:     status,
		Error:      errorText(call.IsError, call.Result),
		SideEffect: toolHasSideEffect(call.ToolName),
	}
}

func observedToolCallsFromMessages(messages []llm.MessageWithTools) []agentquality.ObservedToolCall {
	out := make([]agentquality.ObservedToolCall, 0)
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			out = append(out, agentquality.ObservedToolCall{
				ToolName:   strings.TrimSpace(call.Name),
				ArgsHash:   hashToolArgs(call.Arguments),
				Status:     "called",
				SideEffect: toolHasSideEffect(call.Name),
			})
		}
		if msg.Role == "tool" && strings.TrimSpace(msg.ToolName) != "" {
			status := "success"
			if msg.IsError {
				status = "error"
			}
			out = append(out, agentquality.ObservedToolCall{
				ToolName:   strings.TrimSpace(msg.ToolName),
				Status:     status,
				Error:      errorText(msg.IsError, msg.Content.Text()),
				SideEffect: toolHasSideEffect(msg.ToolName),
			})
		}
	}
	return out
}

func dedupeObservedToolCalls(calls []agentquality.ObservedToolCall) []agentquality.ObservedToolCall {
	out := make([]agentquality.ObservedToolCall, 0, len(calls))
	seen := make(map[string]bool, len(calls))
	for _, call := range calls {
		if strings.TrimSpace(call.ToolName) == "" {
			continue
		}
		key := strings.Join([]string{call.ToolName, call.ArgsHash, call.Status, call.Error}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, call)
	}
	return out
}

func agentQualityEventsFromToolCalls(input agentquality.AgentRunCaseInput, calls []agentquality.ObservedToolCall) []agentquality.Event {
	events := make([]agentquality.Event, 0, len(calls))
	for _, call := range calls {
		if strings.TrimSpace(call.ToolName) == "" {
			continue
		}
		decision := agentquality.DecisionAllowed
		if call.Status == "error" || call.Status == "blocked" || call.Status == "rejected" || call.Status == "denied" {
			decision = agentquality.DecisionRejected
		}
		events = append(events, agentquality.Event{
			Name:        agentquality.EventToolDecision,
			CaseID:      input.Case.ID,
			RunID:       input.RunID,
			TraceID:     input.RunID,
			TurnID:      input.RunID,
			DomainID:    firstNonEmptyString(input.Case.DomainID, input.DomainID),
			SourceKind:  firstNonEmptyString(input.Case.SourceKind, "master"),
			SourceName:  firstNonEmptyString(input.Case.SourceName, "agent_quality_runner"),
			Route:       firstNonEmptyString(input.Case.Route, routeFromSessionID(input.SessionID)),
			FailureType: failureTypeFromToolCall(call),
			FinalStatus: statusFromToolCall(call),
			ToolDecision: agentquality.ToolDecision{
				Expected: append([]string(nil), input.Case.ExpectedTools...),
				Actual:   call.ToolName,
				Decision: decision,
				ArgsHash: call.ArgsHash,
			},
			ReplayRef: "session:" + input.SessionID,
			Ts:        time.Now(),
		})
	}
	return events
}

func taskResponseFinalStatus(resp TaskResponse, err error) agentquality.FinalStatus {
	if err != nil || resp.Error != "" || resp.Status == string(TaskStatusFailed) {
		return agentquality.StatusFail
	}
	if resp.Status == string(TaskStatusPaused) {
		return agentquality.StatusNeedsUser
	}
	return agentquality.StatusPass
}

func failureTypeFromStatus(status agentquality.FinalStatus, fallback agentquality.FailureType) agentquality.FailureType {
	if fallback != "" && fallback != agentquality.FailureNone {
		return fallback
	}
	switch status {
	case agentquality.StatusPass:
		return agentquality.FailureNone
	case agentquality.StatusBlocked, agentquality.StatusNeedsUser:
		return agentquality.FailurePermission
	default:
		return agentquality.FailureRuntime
	}
}

func failureTypeFromToolCall(call agentquality.ObservedToolCall) agentquality.FailureType {
	switch strings.ToLower(strings.TrimSpace(call.Status)) {
	case "blocked", "rejected", "denied":
		return agentquality.FailurePermission
	case "error", "failed":
		return agentquality.FailureTool
	default:
		return agentquality.FailureNone
	}
}

func statusFromToolCall(call agentquality.ObservedToolCall) agentquality.FinalStatus {
	switch strings.ToLower(strings.TrimSpace(call.Status)) {
	case "blocked", "rejected", "denied":
		return agentquality.StatusBlocked
	case "error", "failed":
		return agentquality.StatusFail
	default:
		return agentquality.StatusPass
	}
}

func toolHasSideEffect(toolName string) bool {
	profile, ok := router.BuiltinToolProfile(strings.TrimSpace(toolName))
	return ok && router.ProfileHasSideEffect(profile)
}

func hashToolArgsText(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(args))
	return "sha256:" + hex.EncodeToString(sum[:8])
}

func errorText(isError bool, value string) string {
	if !isError {
		return ""
	}
	return strings.TrimSpace(value)
}

func evalSessionName(input agentquality.AgentRunCaseInput) string {
	if strings.TrimSpace(input.Case.Name) != "" {
		return "agent-quality: " + strings.TrimSpace(input.Case.Name)
	}
	if strings.TrimSpace(input.Case.ID) != "" {
		return "agent-quality: " + strings.TrimSpace(input.Case.ID)
	}
	return "agent-quality"
}
