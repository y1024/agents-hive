package master

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/chef-guo/agents-hive/internal/journal"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/observability"
	"github.com/chef-guo/agents-hive/internal/router"
)

type recordingQualityShadowRunner struct {
	events []agentquality.Event
}

func (r *recordingQualityShadowRunner) RunShadowEval(_ context.Context, ev agentquality.Event) error {
	r.events = append(r.events, ev)
	return nil
}

func TestMaster_EnqueueLog_NilSafe(t *testing.T) {
	m := &Master{}
	assert.NotPanics(t, func() {
		m.enqueueLog(observability.LogEntry{Level: "info", Message: "x"})
	})
}

func TestMaster_SetObservabilityWriters_DoNotResetQueue(t *testing.T) {
	m := &Master{}
	m.SetLogWriter(&observability.NoopLogWriter{})
	ch := m.obsCh
	m.SetTracer(&observability.NoopTracer{})
	m.SetMetricsWriter(&observability.NoopMetricsWriter{})
	assert.True(t, ch == m.obsCh)
}

func TestEmitQualityEvent_EnqueuesMetricAndLog(t *testing.T) {
	m := &Master{obsCh: make(chan observabilityEntry, 4)}
	m.emitQualityEvent("trace", "span", "session-1", agentquality.Event{
		Name:        agentquality.EventToolDecision,
		Route:       "web",
		FailureType: agentquality.FailureNone,
		FinalStatus: agentquality.StatusPass,
		ToolDecision: agentquality.ToolDecision{
			Actual:   "grep",
			Decision: agentquality.DecisionExpected,
		},
	})

	first := <-m.obsCh
	second := <-m.obsCh
	require.NotNil(t, first.metric)
	require.NotNil(t, second.log)
	assert.Equal(t, "quality.tool_decision", first.metric.Name)
	assert.NotContains(t, first.metric.Labels, "session_id")
	assert.Equal(t, "session-1", second.log.SessionID)
}

func TestEmitQualityEventRunsShadowEvalWithEnrichedEvent(t *testing.T) {
	m := &Master{obsCh: make(chan observabilityEntry, 4)}
	runner := &recordingQualityShadowRunner{}
	m.SetQualityShadowEvalRunner(runner)
	defer m.SetQualityShadowEvalRunner(nil)

	m.emitQualityEvent("trace", "span", "session-1", agentquality.Event{
		Name:        agentquality.EventToolDecision,
		Route:       "web",
		FailureType: agentquality.FailureTool,
		FinalStatus: agentquality.StatusFail,
	})

	require.Len(t, runner.events, 1)
	got := runner.events[0]
	assert.Equal(t, "trace", got.TraceID)
	assert.Equal(t, "span", got.SpanID)
	assert.Equal(t, "generic", got.DomainID)
	assert.Equal(t, "master", got.SourceKind)
	assert.Equal(t, "master", got.SourceName)
	assert.NotEmpty(t, got.SessionIDHash)
}

func TestQualityEventCarriesExecutionRefWithoutMetricCardinalityExplosion(t *testing.T) {
	sm := NewSessionManager(make(chan struct{}), nil)
	sm.SetSession(&SessionState{
		ID:     "session-1",
		UserID: "user-1",
	})
	m := &Master{
		obsCh:      make(chan observabilityEntry, 4),
		journal:    journal.NoopJournal{},
		journalCh:  make(chan journalEntry, 1),
		sessionMgr: sm,
	}

	m.emitQualityEvent("trace-1", "span-1", "session-1", agentquality.Event{
		Name:        agentquality.EventAgentTurn,
		Route:       "web",
		FailureType: agentquality.FailureNone,
		FinalStatus: agentquality.StatusPass,
	})

	metricEntry := <-m.obsCh
	logEntry := <-m.obsCh
	require.NotNil(t, metricEntry.metric)
	require.NotNil(t, logEntry.log)
	assert.NotContains(t, metricEntry.metric.Labels, "run_id")
	assert.NotContains(t, metricEntry.metric.Labels, "trace_id")
	assert.NotContains(t, metricEntry.metric.Labels, "span_id")
	assert.NotContains(t, metricEntry.metric.Labels, "turn_id")
	assert.NotContains(t, metricEntry.metric.Labels, "user_id")
	assert.NotContains(t, metricEntry.metric.Labels, "owner_id")

	raw, ok := logEntry.log.Attributes["quality_event"].(json.RawMessage)
	require.True(t, ok)
	var ev agentquality.Event
	require.NoError(t, json.Unmarshal(raw, &ev))
	assert.Equal(t, "trace-1", ev.TraceID)
	assert.Equal(t, "span-1", ev.SpanID)
	assert.Equal(t, "trace-1", ev.TurnID)
	assert.Equal(t, "generic", ev.DomainID)
	assert.Equal(t, "master", ev.SourceKind)
	assert.Equal(t, "master", ev.SourceName)
	assert.Equal(t, agentquality.OwnerScopeUser, ev.OwnerScope)
	assert.Equal(t, "user-1", ev.OwnerID)
	assert.Equal(t, "user-1", ev.UserID)

	journalEntry := <-m.journalCh
	require.NotNil(t, journalEntry.decision)
	assert.Contains(t, journalEntry.decision.Reason, `"trace_id":"trace-1"`)
	assert.Contains(t, journalEntry.decision.Reason, `"owner_id":"user-1"`)
}

func TestContextBuildQualityEventCarriesMemoryDomainSourceAndOwner(t *testing.T) {
	sm := NewSessionManager(make(chan struct{}), nil)
	sm.SetSession(&SessionState{
		ID:     "session-1",
		UserID: "user-1",
	})
	m := &Master{
		obsCh:      make(chan observabilityEntry, 4),
		journal:    journal.NoopJournal{},
		journalCh:  make(chan journalEntry, 1),
		sessionMgr: sm,
	}

	m.emitQualityEvent("trace-1", "span-1", "session-1", agentquality.Event{
		Name:        agentquality.EventContextBuild,
		Route:       "web",
		FailureType: agentquality.FailureNone,
		FinalStatus: agentquality.StatusPass,
		ContextBuild: agentquality.ContextBuild{
			MessageCount:     3,
			MemoryInjected:   true,
			MemoryDomainID:   "customer_service",
			MemorySourceKind: "workflow",
			MemorySourceName: "case_triage",
			MemoryOwnerScope: "user",
			MemoryOwnerID:    "user-1",
		},
	})

	metricEntry := <-m.obsCh
	logEntry := <-m.obsCh
	require.NotNil(t, metricEntry.metric)
	assert.NotContains(t, metricEntry.metric.Labels, "owner_id")

	raw, ok := logEntry.log.Attributes["quality_event"].(json.RawMessage)
	require.True(t, ok)
	var ev agentquality.Event
	require.NoError(t, json.Unmarshal(raw, &ev))
	assert.Equal(t, agentquality.EventContextBuild, ev.Name)
	assert.Equal(t, "customer_service", ev.ContextBuild.MemoryDomainID)
	assert.Equal(t, "workflow", ev.ContextBuild.MemorySourceKind)
	assert.Equal(t, "case_triage", ev.ContextBuild.MemorySourceName)
	assert.Equal(t, "user", ev.ContextBuild.MemoryOwnerScope)
	assert.Equal(t, "user-1", ev.ContextBuild.MemoryOwnerID)
}

func TestRecordToolRecall_EmitsQualityEvent(t *testing.T) {
	m := &Master{obsCh: make(chan observabilityEntry, 4)}
	session := &SessionState{ID: "session-1"}

	m.recordToolRecall("trace", "span", session, agentquality.ToolRecall{
		Mode:                     "inject",
		TurnID:                   "turn-1",
		TraceID:                  "trace",
		QueryPreview:             "发送给飞书用户郭松",
		CandidateCount:           1,
		CandidateNames:           []string{"feishu_api"},
		CandidateScores:          map[string]float64{"feishu_api": 0.91},
		VisibleBeforeCount:       6,
		VisibleAfterCount:        7,
		SelectedTool:             "feishu_api",
		ModelUsedRecalledTool:    true,
		BlockedByPlanGate:        false,
		SideEffectCandidateCount: 1,
	})

	first := <-m.obsCh
	second := <-m.obsCh
	require.NotNil(t, first.metric)
	require.NotNil(t, second.log)
	assert.Equal(t, "quality.tool_recall", first.metric.Name)
	raw, ok := second.log.Attributes["quality_event"].(json.RawMessage)
	require.True(t, ok)
	assert.Contains(t, string(raw), `"name":"quality.tool_recall"`)
	assert.Contains(t, string(raw), `"selected_tool":"feishu_api"`)
	assert.Contains(t, string(raw), `"model_used_recalled_tool":true`)
	assert.Contains(t, string(raw), `"side_effect_candidate_count":1`)
}

func TestRecordReflectionAddsStructuralReflectionBlock(t *testing.T) {
	m := &Master{obsCh: make(chan observabilityEntry, 4)}
	session := &SessionState{ID: "session-1"}

	m.recordReflection("trace", "span", session, reflectionNoteInput{
		Trigger:     "call_failure",
		Severity:    "warn",
		ToolName:    "feishu_api",
		Detail:      "permission denied",
		FailureKind: "permission_denied",
	})

	blocks := session.ListReflectionBlocks()
	require.Len(t, blocks, 1)
	assert.Equal(t, "feishu_api", blocks[0].ToolName)
	assert.Equal(t, "exec", blocks[0].Mode)
	assert.Equal(t, "permission_denied", blocks[0].FailureKind)
}

func TestRecordReflectionIgnoresTransientReflectionBlock(t *testing.T) {
	m := &Master{obsCh: make(chan observabilityEntry, 4)}
	session := &SessionState{ID: "session-1"}

	m.recordReflection("trace", "span", session, reflectionNoteInput{
		Trigger:     "call_failure",
		Severity:    "warn",
		ToolName:    "web_fetch",
		Detail:      "timeout",
		FailureKind: "timeout",
	})

	assert.Empty(t, session.ListReflectionBlocks())
}

func TestReflectionFailureKindFromToolError(t *testing.T) {
	tests := map[string]string{
		"permission denied":             "permission_denied",
		"403 forbidden":                 "4xx",
		"invalid credentials from host": "auth",
		"schema validation failed":      "schema_invalid",
		"timeout":                       "",
	}
	for input, want := range tests {
		if got := reflectionFailureKindFromToolError(input); got != want {
			t.Fatalf("reflectionFailureKindFromToolError(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRecordRouteDecisionSpan_EnqueuesReplayLog(t *testing.T) {
	m := &Master{obsCh: make(chan observabilityEntry, 2)}
	session := &SessionState{ID: "session-1"}

	m.recordRouteDecisionSpan("trace", "span", session, router.DecisionSpan{
		TraceID:       "trace",
		SessionIDHash: "sha256:test",
		Intent:        router.DecisionSpanIntent{Kind: router.IntentCreateSkill, Source: "rule"},
		Allowed:       []string{"skill"},
		Mode:          router.DecisionModeAllow,
	})

	got := <-m.obsCh
	require.NotNil(t, got.log)
	assert.Equal(t, "quality.route_decision.span", got.log.Message)
	assert.Equal(t, "trace", got.log.TraceID)
	_, ok := got.log.Attributes["route_decision_span"].(router.DecisionSpan)
	assert.True(t, ok)
}

func TestHashToolArgs_StableForJSONKeyOrder(t *testing.T) {
	a := json.RawMessage(`{"b":2,"a":1}`)
	b := json.RawMessage(`{"a":1,"b":2}`)
	assert.Equal(t, hashToolArgs(a), hashToolArgs(b))
	assert.NotEmpty(t, hashToolArgs(a))
}

func TestAgentTurnAttributes_ContainLongRunFields(t *testing.T) {
	attrs := agentTurnAttributes("turn-1", 3, 3, 2, 12, 8, true)

	assert.Equal(t, "turn-1", attrs["turn_id"])
	assert.Equal(t, 3, attrs["turn_index"])
	assert.Equal(t, 3, attrs["llm_call_count"])
	assert.Equal(t, 2, attrs["tool_call_count"])
	assert.Equal(t, 12, attrs["prepared_message_count"])
	assert.Equal(t, 8, attrs["visible_tool_count"])
	assert.Equal(t, true, attrs["compaction_triggered"])
}

func TestEmitQualityEvent_EnqueuesJournalDecision(t *testing.T) {
	m := &Master{
		journal:   journal.NoopJournal{},
		journalCh: make(chan journalEntry, 1),
	}
	m.emitQualityEvent("trace", "span", "session-1", agentquality.Event{
		Name:        agentquality.EventAgentTurn,
		Route:       "web",
		FailureType: agentquality.FailureTool,
		FinalStatus: agentquality.StatusFail,
	})

	got := <-m.journalCh
	require.NotNil(t, got.decision)
	assert.Equal(t, "quality.agent_turn", got.decision.Decision)
	assert.Contains(t, got.decision.Reason, `"failure_type":"tool"`)
}

func TestEmitQualityEventRedactsLogAndJournal(t *testing.T) {
	m := &Master{
		obsCh:     make(chan observabilityEntry, 4),
		journal:   journal.NoopJournal{},
		journalCh: make(chan journalEntry, 1),
	}
	m.emitQualityEvent("trace", "span", "session-1", agentquality.Event{
		Name:        agentquality.EventToolDecision,
		Route:       "web",
		FailureType: agentquality.FailureTool,
		FinalStatus: agentquality.StatusFail,
		Attributes: map[string]any{
			"api_key": "key-123",
			"nested": map[string]any{
				"authorization": "Bearer token-123",
				"message":       "failed with access_token=inline-token",
			},
		},
	})

	<-m.obsCh
	logEntry := <-m.obsCh
	raw, ok := logEntry.log.Attributes["quality_event"].(json.RawMessage)
	require.True(t, ok)
	assert.NotContains(t, string(raw), "key-123")
	assert.NotContains(t, string(raw), "token-123")
	assert.NotContains(t, string(raw), "inline-token")
	assert.Contains(t, string(raw), "[REDACTED]")

	got := <-m.journalCh
	require.NotNil(t, got.decision)
	assert.NotContains(t, got.decision.Reason, "key-123")
	assert.NotContains(t, got.decision.Reason, "token-123")
	assert.NotContains(t, got.decision.Reason, "inline-token")
	assert.Contains(t, got.decision.Reason, "[REDACTED]")
}

func TestQualityJournalDecisionDoesNotPersistRawToken(t *testing.T) {
	m := &Master{
		journal:   journal.NoopJournal{},
		journalCh: make(chan journalEntry, 1),
	}
	raw := redactQualityEventJSON(agentquality.Event{
		Name:        agentquality.EventPermissionDecision,
		Route:       "web",
		FailureType: agentquality.FailurePermission,
		FinalStatus: agentquality.StatusFail,
	}, []byte(`{"name":"quality.permission_decision","attributes":{"context_token":"ctx-123"}}`))

	m.enqueueQualityJournalDecision("session-1", agentquality.Event{Name: agentquality.EventPermissionDecision}, raw)

	got := <-m.journalCh
	require.NotNil(t, got.decision)
	assert.NotContains(t, got.decision.Reason, "ctx-123")
	assert.Contains(t, got.decision.Reason, "[REDACTED]")
}

func TestEmitQualityEventMarshalErrorFailsClosed(t *testing.T) {
	m := &Master{
		obsCh:     make(chan observabilityEntry, 4),
		journal:   journal.NoopJournal{},
		journalCh: make(chan journalEntry, 1),
	}
	m.emitQualityEvent("trace", "span", "session-1", agentquality.Event{
		Name:        agentquality.EventToolDecision,
		Route:       "web",
		FailureType: agentquality.FailureTool,
		FinalStatus: agentquality.StatusFail,
		Attributes: map[string]any{
			"api_key": "key-123",
			"bad":     func() {},
		},
	})

	<-m.obsCh
	logEntry := <-m.obsCh
	raw, ok := logEntry.log.Attributes["quality_event"].(json.RawMessage)
	require.True(t, ok)
	assert.Contains(t, string(raw), `"redaction_error":true`)
	assert.Contains(t, string(raw), `"name":"quality.tool_decision"`)
	assert.NotContains(t, string(raw), "key-123")

	got := <-m.journalCh
	require.NotNil(t, got.decision)
	assert.Contains(t, got.decision.Reason, `"redaction_error":true`)
	assert.NotContains(t, got.decision.Reason, "key-123")
}

func TestRecordReflection_AppendsNoteAndEmitsEvent(t *testing.T) {
	m := &Master{obsCh: make(chan observabilityEntry, 4)}
	session := &SessionState{ID: "session-1"}

	m.recordReflection("trace", "span", session, reflectionNoteInput{
		Trigger:     "batch_loop",
		Severity:    "warn",
		Consecutive: 3,
	})

	require.Len(t, session.Messages, 1)
	msg := session.Messages[0]
	assert.Equal(t, "system", msg.Role)
	assert.Contains(t, msg.Content.Text(), "连续出现 3 次")
	assert.Equal(t, "batch_loop", msg.Metadata["reflection_trigger"])

	first := <-m.obsCh
	second := <-m.obsCh
	require.NotNil(t, first.metric)
	require.NotNil(t, second.log)
	assert.Equal(t, "quality.reflection", first.metric.Name)
	assert.Equal(t, "batch_loop", first.metric.Labels["reflection_trigger"])
	assert.Equal(t, "warn", first.metric.Labels["severity"])

	raw, ok := second.log.Attributes["quality_event"].(json.RawMessage)
	require.True(t, ok)
	assert.Contains(t, string(raw), `"trigger":"batch_loop"`)
	assert.Contains(t, string(raw), `"injected":true`)
}

func TestRecordReflection_DoesNotLeakDetail(t *testing.T) {
	m := &Master{obsCh: make(chan observabilityEntry, 4)}
	session := &SessionState{
		ID: "session-1",
		Messages: []llm.MessageWithTools{{
			Role:    "user",
			Content: llm.NewTextContent("hello"),
		}},
	}
	secret := strings.Repeat("secret stack trace ", 10)

	m.recordReflection("trace", "span", session, reflectionNoteInput{
		Trigger:  "guard_failure",
		Severity: "hard_stop",
		Detail:   secret,
	})

	require.Len(t, session.Messages, 2)
	note := session.Messages[1].Content.Text()
	assert.NotContains(t, note, secret)
	assert.NotContains(t, note, "secret stack trace")

	<-m.obsCh
	logEntry := <-m.obsCh
	raw, ok := logEntry.log.Attributes["quality_event"].(json.RawMessage)
	require.True(t, ok)
	assert.NotContains(t, string(raw), secret)
	assert.NotContains(t, string(raw), "secret stack trace")
}

func TestRecordReflection_TriggersMapToLowCardinalityEvents(t *testing.T) {
	tests := []struct {
		name        string
		input       reflectionNoteInput
		status      agentquality.FinalStatus
		failureType agentquality.FailureType
	}{
		{
			name: "loop hard stop",
			input: reflectionNoteInput{
				Trigger:     "batch_loop",
				Severity:    "hard_stop",
				Consecutive: 5,
			},
			status:      agentquality.StatusFail,
			failureType: agentquality.FailureTool,
		},
		{
			name: "call failure",
			input: reflectionNoteInput{
				Trigger:     "call_failure",
				Severity:    "warn",
				ToolName:    "read_file",
				Consecutive: 2,
			},
			status:      agentquality.StatusPass,
			failureType: agentquality.FailureTool,
		},
		{
			name:        "guard failure",
			input:       reflectionNoteInput{Trigger: "guard_failure", Severity: "warn"},
			status:      agentquality.StatusPass,
			failureType: agentquality.FailurePrompt,
		},
		{
			name:        "validation failure",
			input:       reflectionNoteInput{Trigger: "validation_failure", Severity: "hard_stop"},
			status:      agentquality.StatusFail,
			failureType: agentquality.FailureModel,
		},
		{
			name:        "intent fulfillment",
			input:       reflectionNoteInput{Trigger: "intent_fulfillment", Severity: "warn", Detail: "missing=send_attempt"},
			status:      agentquality.StatusPass,
			failureType: agentquality.FailureModel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Master{obsCh: make(chan observabilityEntry, 4)}
			session := &SessionState{ID: "session-1"}

			m.recordReflection("trace", "span", session, tt.input)

			<-m.obsCh
			logEntry := <-m.obsCh
			raw, ok := logEntry.log.Attributes["quality_event"].(json.RawMessage)
			require.True(t, ok)
			var ev agentquality.Event
			require.NoError(t, json.Unmarshal(raw, &ev))
			assert.Equal(t, agentquality.EventReflection, ev.Name)
			assert.Equal(t, tt.input.Trigger, ev.Reflection.Trigger)
			assert.Equal(t, tt.input.Severity, ev.Reflection.Severity)
			assert.Equal(t, tt.input.ToolName, ev.Reflection.ToolName)
			assert.Equal(t, tt.input.Consecutive, ev.Reflection.Consecutive)
			assert.Equal(t, tt.status, ev.FinalStatus)
			assert.Equal(t, tt.failureType, ev.FailureType)
		})
	}
}
