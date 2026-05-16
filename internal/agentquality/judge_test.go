package agentquality

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJudgeInputRedactsSecrets(t *testing.T) {
	input := JudgeInput{
		CaseID:    "test-case-1",
		UserInput: "请帮我调用 API，token: sk-abc123456789012345678901234567890",
		FinalOutput: `调用成功，使用了 Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123`,
		ExpectedAnswer: "api_key: my-secret-key-that-is-very-long-and-should-be-redacted",
		TraceSummary: TraceSummary{
			ToolsCalled: []string{"http_request"},
			EventCount:  3,
			FinalStatus: StatusPass,
		},
	}

	redacted := RedactJudgeInput(input)

	// CaseID 不应被脱敏
	assert.Equal(t, "test-case-1", redacted.CaseID)

	// token 应被脱敏
	assert.NotContains(t, redacted.UserInput, "sk-abc123456789012345678901234567890")
	assert.Contains(t, redacted.UserInput, "[REDACTED]")

	// Bearer token 应被脱敏
	assert.NotContains(t, redacted.FinalOutput, "eyJhbGciOiJIUzI1NiJ9")
	assert.Contains(t, redacted.FinalOutput, "[REDACTED]")

	// api_key 应被脱敏
	assert.NotContains(t, redacted.ExpectedAnswer, "my-secret-key-that-is-very-long-and-should-be-redacted")
	assert.Contains(t, redacted.ExpectedAnswer, "[REDACTED]")

	// TraceSummary 不应被修改
	assert.Equal(t, input.TraceSummary, redacted.TraceSummary)
}

func TestJudgeInputRedactsAuthorizationHeader(t *testing.T) {
	input := JudgeInput{
		CaseID:    "auth-header-test",
		UserInput: "Authorization: Basic dXNlcjpwYXNz",
	}

	redacted := RedactJudgeInput(input)
	assert.NotContains(t, redacted.UserInput, "dXNlcjpwYXNz")
	assert.Contains(t, redacted.UserInput, "[REDACTED]")
}

func TestJudgeInputRedactsPassword(t *testing.T) {
	input := JudgeInput{
		CaseID:    "password-test",
		UserInput: `password: my_super_secret_password_123`,
	}

	redacted := RedactJudgeInput(input)
	assert.NotContains(t, redacted.UserInput, "my_super_secret_password_123")
	assert.Contains(t, redacted.UserInput, "[REDACTED]")
}

func TestJudgeVerdictValidationRejectsInvalidJSON(t *testing.T) {
	tests := []struct {
		name    string
		verdict JudgeVerdict
		wantErr bool
	}{
		{
			name: "valid verdict",
			verdict: JudgeVerdict{
				Score:       7,
				Passed:      true,
				FailureType: FailureNone,
				Rationale:   "Agent correctly solved the problem",
				Evidence:    []string{"output matches expected"},
			},
			wantErr: false,
		},
		{
			name: "score too high",
			verdict: JudgeVerdict{
				Score:     11,
				Rationale: "some rationale",
			},
			wantErr: true,
		},
		{
			name: "score negative",
			verdict: JudgeVerdict{
				Score:     -1,
				Rationale: "some rationale",
			},
			wantErr: true,
		},
		{
			name: "empty rationale",
			verdict: JudgeVerdict{
				Score:     5,
				Rationale: "",
			},
			wantErr: true,
		},
		{
			name: "whitespace-only rationale",
			verdict: JudgeVerdict{
				Score:     5,
				Rationale: "   ",
			},
			wantErr: true,
		},
		{
			name: "invalid failure type",
			verdict: JudgeVerdict{
				Score:       5,
				Rationale:   "some rationale",
				FailureType: "invalid_type",
			},
			wantErr: true,
		},
		{
			name: "valid failure type",
			verdict: JudgeVerdict{
				Score:       3,
				Rationale:   "tool failed",
				FailureType: FailureTool,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJudgeVerdict(tt.verdict)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGateFailsWhenRequiredJudgeMissing(t *testing.T) {
	metrics := GateMetrics{
		RequiredTotal:               7,
		RequiredPassed:              7,
		DangerousMisallowCount:      0,
		FailureAttributionRate:      0.95,
		ToolChoiceAccuracy:          0.90,
		ReplayLocatableRate:         0.92,
		RegressionCandidateRate:     0.85,
		DelegationTraceCoverageRate: 1.0,
		// Judge is missing for a required domain
		JudgeMissing:        true,
		JudgeRequiredDomain: "finance",
	}

	thresholds := DefaultGateThresholds()
	thresholds.JudgeRequiredForDomains = []string{"finance", "healthcare"}

	err := EvaluateGate(metrics, thresholds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "judge_missing")
}

func TestGatePassesWhenJudgeMissingButDomainNotRequired(t *testing.T) {
	metrics := GateMetrics{
		RequiredTotal:               7,
		RequiredPassed:              7,
		DangerousMisallowCount:      0,
		FailureAttributionRate:      0.95,
		ToolChoiceAccuracy:          0.90,
		ReplayLocatableRate:         0.92,
		RegressionCandidateRate:     0.85,
		DelegationTraceCoverageRate: 1.0,
		// Judge is missing but domain is not in required list
		JudgeMissing:        true,
		JudgeRequiredDomain: "general",
	}

	thresholds := DefaultGateThresholds()
	thresholds.JudgeRequiredForDomains = []string{"finance", "healthcare"}

	err := EvaluateGate(metrics, thresholds)
	require.NoError(t, err)
}

func TestSemanticScoreBelowThresholdFailsGate(t *testing.T) {
	metrics := GateMetrics{
		RequiredTotal:               7,
		RequiredPassed:              7,
		DangerousMisallowCount:      0,
		FailureAttributionRate:      0.95,
		ToolChoiceAccuracy:          0.90,
		ReplayLocatableRate:         0.92,
		RegressionCandidateRate:     0.85,
		DelegationTraceCoverageRate: 1.0,
		SemanticScore:               5.0, // Below threshold
	}

	thresholds := DefaultGateThresholds()
	thresholds.SemanticScoreMin = 7.0

	err := EvaluateGate(metrics, thresholds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "semantic_score")
}

func TestSemanticScoreAboveThresholdPassesGate(t *testing.T) {
	metrics := GateMetrics{
		RequiredTotal:               7,
		RequiredPassed:              7,
		DangerousMisallowCount:      0,
		FailureAttributionRate:      0.95,
		ToolChoiceAccuracy:          0.90,
		ReplayLocatableRate:         0.92,
		RegressionCandidateRate:     0.85,
		DelegationTraceCoverageRate: 1.0,
		SemanticScore:               8.5,
	}

	thresholds := DefaultGateThresholds()
	thresholds.SemanticScoreMin = 7.0

	err := EvaluateGate(metrics, thresholds)
	require.NoError(t, err)
}

func TestSemanticScoreZeroThresholdSkipsCheck(t *testing.T) {
	// 当 SemanticScoreMin 为 0 时，不检查语义分数（向后兼容）
	metrics := GateMetrics{
		RequiredTotal:               7,
		RequiredPassed:              7,
		DangerousMisallowCount:      0,
		FailureAttributionRate:      0.95,
		ToolChoiceAccuracy:          0.90,
		ReplayLocatableRate:         0.92,
		RegressionCandidateRate:     0.85,
		DelegationTraceCoverageRate: 1.0,
		SemanticScore:               0, // No judge ran
	}

	thresholds := DefaultGateThresholds()
	// SemanticScoreMin defaults to 0 - should not fail

	err := EvaluateGate(metrics, thresholds)
	require.NoError(t, err)
}

func TestHeuristicEvaluatorRemainsFallbackOnly(t *testing.T) {
	// HeuristicJudgeEvaluator 的 evidence level 应低于 LLM judge
	// 验证启发式 judge 只作为 fallback 使用
	ctx := context.Background()
	heuristic := HeuristicJudgeEvaluator{}

	input := JudgeInput{
		CaseID:    "test-heuristic",
		UserInput: "帮我查询订单状态",
		FinalOutput: "您的订单已发货",
		TraceSummary: TraceSummary{
			ToolsCalled: []string{"order_query"},
			EventCount:  3,
			FinalStatus: StatusPass,
		},
		ToolAssertions: []ToolAssertion{
			{ToolName: "order_query", Expected: true, Actual: true},
		},
	}

	verdict, err := heuristic.Judge(ctx, input)
	require.NoError(t, err)

	// 启发式 judge 不应给出满分（因为它无法真正理解语义）
	assert.LessOrEqual(t, verdict.Score, 8)
	assert.NotEmpty(t, verdict.Rationale)

	// 验证 verdict 合法
	err = ValidateJudgeVerdict(verdict)
	require.NoError(t, err)
}

func TestHeuristicJudgeFailsOnToolAssertionMismatch(t *testing.T) {
	ctx := context.Background()
	heuristic := HeuristicJudgeEvaluator{}

	input := JudgeInput{
		CaseID:    "test-tool-mismatch",
		UserInput: "帮我发送邮件",
		FinalOutput: "邮件已发送",
		TraceSummary: TraceSummary{
			ToolsCalled: []string{"search"},
			EventCount:  2,
			FinalStatus: StatusPass,
		},
		ToolAssertions: []ToolAssertion{
			{ToolName: "send_email", Expected: true, Actual: false},
		},
	}

	verdict, err := heuristic.Judge(ctx, input)
	require.NoError(t, err)

	assert.False(t, verdict.Passed)
	assert.True(t, verdict.ShouldOptimize)
	assert.Contains(t, verdict.Evidence, "tool_assertion_failed: send_email (expected=true, actual=false)")
}

func TestHeuristicJudgeFailsOnEmptyOutput(t *testing.T) {
	ctx := context.Background()
	heuristic := HeuristicJudgeEvaluator{}

	input := JudgeInput{
		CaseID:    "test-empty-output",
		UserInput: "帮我查询天气",
		FinalOutput: "",
		TraceSummary: TraceSummary{
			ToolsCalled: []string{},
			EventCount:  1,
			FinalStatus: StatusFail,
		},
		ToolAssertions: []ToolAssertion{
			{ToolName: "weather_api", Expected: true, Actual: false},
		},
	}

	verdict, err := heuristic.Judge(ctx, input)
	require.NoError(t, err)

	assert.False(t, verdict.Passed)
	// FinalStatus=fail 优先级高于 tool assertion，所以是 runtime
	assert.Equal(t, FailureRuntime, verdict.FailureType)
}

func TestFakeJudgeEvaluatorReturnsConfiguredVerdict(t *testing.T) {
	ctx := context.Background()
	expected := JudgeVerdict{
		Score:       9,
		Passed:      true,
		FailureType: FailureNone,
		Rationale:   "Perfect execution",
		Evidence:    []string{"all assertions passed"},
	}

	fake := FakeJudgeEvaluator{Verdict: expected}
	verdict, err := fake.Judge(ctx, JudgeInput{CaseID: "any"})
	require.NoError(t, err)
	assert.Equal(t, expected, verdict)
}

func TestFakeJudgeEvaluatorReturnsConfiguredError(t *testing.T) {
	ctx := context.Background()
	fake := FakeJudgeEvaluator{
		Err: assert.AnError,
	}

	_, err := fake.Judge(ctx, JudgeInput{CaseID: "any"})
	require.Error(t, err)
}

func TestRubricCriterionInPrompt(t *testing.T) {
	input := JudgeInput{
		CaseID:    "rubric-test",
		UserInput: "帮我写一个函数",
		FinalOutput: "def hello(): pass",
		Rubric: []RubricCriterion{
			{Name: "正确性", Description: "代码能正确运行", Weight: 8},
			{Name: "可读性", Description: "代码易于理解", Weight: 5},
		},
		TraceSummary: TraceSummary{
			ToolsCalled: []string{"code_gen"},
			EventCount:  2,
			FinalStatus: StatusPass,
		},
	}

	prompt := RenderJudgePromptV1(input)
	assert.Contains(t, prompt, "正确性")
	assert.Contains(t, prompt, "可读性")
	assert.Contains(t, prompt, "权重: 8/10")
	assert.Contains(t, prompt, "权重: 5/10")
	assert.Contains(t, prompt, "帮我写一个函数")
	assert.Contains(t, prompt, "def hello(): pass")
}

func TestGateBackwardCompatibility(t *testing.T) {
	// 确保不设置新字段时，gate 行为与之前一致
	metrics := GateMetrics{
		RequiredTotal:               7,
		RequiredPassed:              7,
		DangerousMisallowCount:      0,
		FailureAttributionRate:      0.95,
		ToolChoiceAccuracy:          0.90,
		ReplayLocatableRate:         0.92,
		RegressionCandidateRate:     0.85,
		DelegationTraceCoverageRate: 1.0,
		// 不设置 SemanticScore 和 JudgeMissing
	}

	// 使用默认阈值（不含新字段）
	thresholds := DefaultGateThresholds()

	err := EvaluateGate(metrics, thresholds)
	require.NoError(t, err)
}
