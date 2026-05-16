package agentquality

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAgentRunAdapter struct {
	outputs      map[string]AgentRunCaseOutput
	err          error
	inputs       []AgentRunCaseInput
	checkContext func(context.Context, AgentRunCaseInput) error
}

func (f *fakeAgentRunAdapter) RunCase(ctx context.Context, input AgentRunCaseInput) (AgentRunCaseOutput, error) {
	f.inputs = append(f.inputs, input)
	if err := ctx.Err(); err != nil {
		return AgentRunCaseOutput{}, err
	}
	if f.err != nil {
		return AgentRunCaseOutput{}, f.err
	}
	if f.checkContext != nil {
		if err := f.checkContext(ctx, input); err != nil {
			return AgentRunCaseOutput{}, err
		}
	}
	if output, ok := f.outputs[input.Case.ID]; ok {
		return output, nil
	}
	return AgentRunCaseOutput{
		FinalOutput: "default output",
		FinalStatus: input.Case.ExpectedStatus,
		TraceID:     "trace-" + input.Case.ID,
		ReplayRef:   "replay-" + input.Case.ID,
	}, nil
}

func TestAgentRunEvalRunnerCapturesFinalOutputAndToolCalls(t *testing.T) {
	adapter := &fakeAgentRunAdapter{
		outputs: map[string]AgentRunCaseOutput{
			"case1": {
				FinalOutput: "executed successfully",
				FinalStatus: StatusPass,
				TraceID:     "trace-001",
				ReplayRef:   "replay-001",
				ToolCalls: []ObservedToolCall{
					{ToolName: "bash", Status: "success"},
					{ToolName: "read", Status: "success"},
				},
				Events: []Event{
					{
						Name:        EventToolDecision,
						CaseID:      "case1",
						Route:       "web",
						FailureType: FailureNone,
						FinalStatus: StatusPass,
						ToolDecision: ToolDecision{
							Expected: []string{"bash"},
							Actual:   "bash",
							Decision: DecisionExpected,
						},
						Ts: time.Now(),
					},
				},
			},
		},
	}

	runner := AgentRunEvalRunner{
		Adapter:  adapter,
		DomainID: "test-domain",
		OwnerID:  "test-owner",
	}

	cases := []LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "run bash command",
				ExpectedTools:  []string{"bash"},
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
	}

	gateInput, err := runner.Run(cases)
	require.NoError(t, err)

	// 验证结果
	assert.Len(t, gateInput.Results, 1)
	assert.True(t, gateInput.Results[0].Passed)
	assert.Equal(t, "case1", gateInput.Results[0].CaseID)

	// 验证工具调用被捕获
	assert.Contains(t, gateInput.ToolActualByCaseID, "case1")
	assert.Contains(t, gateInput.ToolActualByCaseID["case1"], "bash")
	assert.Contains(t, gateInput.ToolActualByCaseID["case1"], "read")

	// 验证 replay ref 被捕获
	assert.Equal(t, "replay-001", gateInput.ReplayRefByCaseID["case1"])

	// 验证事件被捕获
	assert.Len(t, gateInput.Events, 1)
	assert.Equal(t, EventToolDecision, gateInput.Events[0].Name)
	assert.Equal(t, "case1", gateInput.Events[0].CaseID)

	// 验证 runner info
	info := runner.Info()
	assert.Equal(t, "agent_run", info.Name)
	assert.Equal(t, EvidenceRealRunner, info.EvidenceLevel)
}

func TestAgentRunEvalRunnerBlocksExternalWriteByDefault(t *testing.T) {
	adapter := &fakeAgentRunAdapter{
		outputs: map[string]AgentRunCaseOutput{
			"case1": {
				FinalStatus: StatusPass,
				ToolCalls: []ObservedToolCall{
					{ToolName: "feishu_api", Status: "success", SideEffect: true},
				},
			},
		},
	}
	runner := AgentRunEvalRunner{
		Adapter:            adapter,
		AllowedSideEffects: []string{"feishu_api"},
	}

	gateInput, err := runner.Run([]LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "send message",
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
	})
	require.NoError(t, err)

	assert.Empty(t, adapter.inputs)
	require.Len(t, gateInput.Results, 1)
	assert.False(t, gateInput.Results[0].Passed)
	assert.Contains(t, gateInput.Results[0].Reason, "side effect tool feishu_api blocked before agent run")
}

func TestAgentRunEvalRunnerAllowsExplicitSideEffect(t *testing.T) {
	adapter := &fakeAgentRunAdapter{
		outputs: map[string]AgentRunCaseOutput{
			"case1": {
				FinalStatus: StatusPass,
				ToolCalls: []ObservedToolCall{
					{ToolName: "feishu_api", Status: "success", SideEffect: true},
				},
			},
		},
	}
	runner := AgentRunEvalRunner{
		Adapter:            adapter,
		AllowedSideEffects: []string{"feishu_api"},
	}

	gateInput, err := runner.Run([]LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "send message",
				AllowedTools:   []string{"feishu_api"},
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, adapter.inputs, 1)
	assert.True(t, adapter.inputs[0].SandboxExternal)
	assert.Equal(t, []string{"feishu_api"}, adapter.inputs[0].AllowedSideEffects)
	require.Len(t, gateInput.Results, 1)
	assert.True(t, gateInput.Results[0].Passed)
	assert.Equal(t, []string{"feishu_api"}, gateInput.ToolActualByCaseID["case1"])
}

func TestAgentRunEvalRunnerPassesWhenSandboxBlocksUndeclaredSideEffect(t *testing.T) {
	adapter := &fakeAgentRunAdapter{
		outputs: map[string]AgentRunCaseOutput{
			"case1": {
				FinalStatus: StatusBlocked,
				ToolCalls: []ObservedToolCall{
					{ToolName: "feishu_api", Status: "blocked", SideEffect: true},
				},
			},
		},
	}
	runner := AgentRunEvalRunner{
		Adapter:            adapter,
		AllowedSideEffects: []string{"feishu_api"},
	}

	gateInput, err := runner.Run([]LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "send message without permission",
				ExpectedStatus: StatusBlocked,
				Required:       true,
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, gateInput.Results, 1)
	assert.True(t, gateInput.Results[0].Passed)
}

type agentRunContextKey struct{}

func TestAgentRunEvalRunnerUsesCallerContext(t *testing.T) {
	adapter := &fakeAgentRunAdapter{
		checkContext: func(ctx context.Context, input AgentRunCaseInput) error {
			if got := ctx.Value(agentRunContextKey{}); got != "caller-context" {
				return errors.New("caller context missing")
			}
			return nil
		},
	}
	runner := AgentRunEvalRunner{Adapter: adapter}
	ctx := context.WithValue(context.Background(), agentRunContextKey{}, "caller-context")

	gateInput, err := runner.RunWithContext(ctx, []LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "test input",
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, gateInput.Results, 1)
	assert.True(t, gateInput.Results[0].Passed)
}

func TestAgentRunEvalRunnerPropagatesCanceledContext(t *testing.T) {
	adapter := &fakeAgentRunAdapter{}
	runner := AgentRunEvalRunner{Adapter: adapter}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runner.RunWithContext(ctx, []LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "test input",
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
	})

	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, adapter.inputs)
}

func TestAgentRunEvalRunnerGeneratesUniqueSessionIDs(t *testing.T) {
	adapter := &fakeAgentRunAdapter{}
	runner := AgentRunEvalRunner{Adapter: adapter}

	_, err := runner.Run([]LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "test input",
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
		{
			Path: "test/case2.json",
			Case: Case{
				ID:             "case2",
				Name:           "Test Case 2",
				Route:          "web",
				Input:          "test input",
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, adapter.inputs, 2)
	assert.NotEmpty(t, adapter.inputs[0].RunID)
	assert.Equal(t, adapter.inputs[0].RunID, adapter.inputs[1].RunID)
	assert.NotEmpty(t, adapter.inputs[0].SessionID)
	assert.NotEmpty(t, adapter.inputs[1].SessionID)
	assert.NotEqual(t, adapter.inputs[0].SessionID, adapter.inputs[1].SessionID)
}

func TestAgentRunEvalRunnerMapsQualityEventsToGateInput(t *testing.T) {
	adapter := &fakeAgentRunAdapter{
		outputs: map[string]AgentRunCaseOutput{
			"case1": {
				FinalOutput: "output",
				FinalStatus: StatusPass,
				Events: []Event{
					{
						Name:        EventToolDecision,
						Route:       "web",
						FailureType: FailureNone,
						FinalStatus: StatusPass,
						ToolDecision: ToolDecision{
							Actual:   "bash",
							Decision: DecisionExpected,
						},
						Ts: time.Now(),
					},
					{
						Name:        EventDelegation,
						Route:       "web",
						FailureType: FailureNone,
						FinalStatus: StatusPass,
						Delegation: Delegation{
							AgentType: "subagent",
							AgentID:   "sub-001",
						},
						Ts: time.Now(),
					},
				},
			},
		},
	}

	runner := AgentRunEvalRunner{Adapter: adapter}

	cases := []LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "test input",
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
	}

	gateInput, err := runner.Run(cases)
	require.NoError(t, err)

	// 验证事件被正确映射
	assert.Len(t, gateInput.Events, 2)
	assert.Equal(t, EventToolDecision, gateInput.Events[0].Name)
	assert.Equal(t, EventDelegation, gateInput.Events[1].Name)

	// 验证 EventsByCase 映射
	assert.Contains(t, gateInput.EventsByCase, "case1")
	assert.Len(t, gateInput.EventsByCase["case1"], 2)

	// 验证 CaseID 被填充
	for _, ev := range gateInput.Events {
		assert.Equal(t, "case1", ev.CaseID)
	}
}

func TestAgentRunEvalRunnerHandlesAdapterError(t *testing.T) {
	adapter := &fakeAgentRunAdapter{
		err: errors.New("adapter execution failed"),
	}

	runner := AgentRunEvalRunner{Adapter: adapter}

	cases := []LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "test input",
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
	}

	gateInput, err := runner.Run(cases)
	require.NoError(t, err) // runner 本身不应该失败

	// 验证结果标记为失败
	assert.Len(t, gateInput.Results, 1)
	assert.False(t, gateInput.Results[0].Passed)
	assert.Contains(t, gateInput.Results[0].Reason, "agent run failed")
}

func TestAgentRunEvalRunnerStatusMismatch(t *testing.T) {
	adapter := &fakeAgentRunAdapter{
		outputs: map[string]AgentRunCaseOutput{
			"case1": {
				FinalOutput: "output",
				FinalStatus: StatusFail, // 期望 Pass，实际 Fail
			},
		},
	}

	runner := AgentRunEvalRunner{Adapter: adapter}

	cases := []LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "test input",
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
	}

	gateInput, err := runner.Run(cases)
	require.NoError(t, err)

	// 验证结果标记为失败
	assert.Len(t, gateInput.Results, 1)
	assert.False(t, gateInput.Results[0].Passed)
	assert.Contains(t, gateInput.Results[0].Reason, "expected status pass, got fail")
}

func TestAgentRunEvalRunnerToolMismatch(t *testing.T) {
	adapter := &fakeAgentRunAdapter{
		outputs: map[string]AgentRunCaseOutput{
			"case1": {
				FinalOutput: "output",
				FinalStatus: StatusPass,
				ToolCalls: []ObservedToolCall{
					{ToolName: "read"}, // 期望 bash，实际 read
				},
			},
		},
	}

	runner := AgentRunEvalRunner{Adapter: adapter}

	cases := []LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "test input",
				ExpectedTools:  []string{"bash"},
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
	}

	gateInput, err := runner.Run(cases)
	require.NoError(t, err)

	// 验证结果标记为失败
	assert.Len(t, gateInput.Results, 1)
	assert.False(t, gateInput.Results[0].Passed)
	assert.Contains(t, gateInput.Results[0].Reason, "tool mismatch")
}

func TestAgentRunEvalRunnerRequiresAdapter(t *testing.T) {
	runner := AgentRunEvalRunner{
		Adapter: nil,
	}

	cases := []LoadedCase{
		{
			Path: "test/case1.json",
			Case: Case{
				ID:             "case1",
				Name:           "Test Case 1",
				Route:          "web",
				Input:          "test input",
				ExpectedStatus: StatusPass,
				Required:       true,
			},
		},
	}

	_, err := runner.Run(cases)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent run adapter not configured")
}

func TestExtractToolNames(t *testing.T) {
	calls := []ObservedToolCall{
		{ToolName: "bash"},
		{ToolName: "read"},
		{ToolName: "bash"}, // 重复
		{ToolName: "write"},
	}

	names := extractToolNames(calls)
	assert.Len(t, names, 3)
	assert.Contains(t, names, "bash")
	assert.Contains(t, names, "read")
	assert.Contains(t, names, "write")
}

func TestToolsMatchExpectation(t *testing.T) {
	tests := []struct {
		name     string
		actual   []string
		expected []string
		allowed  []string
		want     bool
	}{
		{
			name:     "expected tool called",
			actual:   []string{"bash"},
			expected: []string{"bash"},
			want:     true,
		},
		{
			name:     "expected tool not called",
			actual:   []string{"read"},
			expected: []string{"bash"},
			want:     false,
		},
		{
			name:     "one of expected tools called",
			actual:   []string{"read", "bash"},
			expected: []string{"bash", "write"},
			want:     true,
		},
		{
			name:    "all tools in allowed list",
			actual:  []string{"bash", "read"},
			allowed: []string{"bash", "read", "write"},
			want:    true,
		},
		{
			name:    "tool not in allowed list",
			actual:  []string{"bash", "delete"},
			allowed: []string{"bash", "read"},
			want:    false,
		},
		{
			name:    "no tools called with allowed list",
			actual:  []string{},
			allowed: []string{"bash"},
			want:    false,
		},
		{
			name:   "no constraints",
			actual: []string{"bash"},
			want:   true,
		},
		{
			name:   "no tools called, no constraints",
			actual: []string{},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolsMatchExpectation(tt.actual, tt.expected, tt.allowed)
			assert.Equal(t, tt.want, got)
		})
	}
}
