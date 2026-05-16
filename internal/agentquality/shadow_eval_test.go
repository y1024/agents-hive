package agentquality

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureShadowEvalRunner struct {
	cases chan []LoadedCase
}

func (r captureShadowEvalRunner) Info() RunnerInfo {
	return RunnerInfo{
		Name:          "capture",
		Version:       "test",
		EvidenceLevel: EvidenceProductionShadow,
	}
}

func (r captureShadowEvalRunner) Run(cases []LoadedCase) (GateInput, error) {
	copied := append([]LoadedCase(nil), cases...)
	r.cases <- copied
	return GateInput{
		Results: []Result{
			{
				CaseID: cases[0].Case.ID,
				Passed: true,
			},
		},
	}, nil
}

type captureShadowEvaluator struct {
	inputs chan EvaluationInput
}

func (e captureShadowEvaluator) Evaluate(_ context.Context, input EvaluationInput) (EvaluationVerdict, error) {
	e.inputs <- input
	return EvaluationVerdict{
		Score:       8,
		Verdict:     "通过",
		FailureType: FailureNone,
	}, nil
}

func TestShadowEvalSamplesQualityEventsByDomain(t *testing.T) {
	config := ShadowEvalConfig{
		Enabled:       true,
		SamplingRate:  1.0, // 100% 采样用于测试
		DomainFilters: []string{"domain-a", "domain-b"},
		MaxConcurrent: 2,
	}

	sampler := NewConfigurableSampler(config)

	// 应该采样的事件
	eventA := Event{
		Name:        EventToolDecision,
		DomainID:    "domain-a",
		FailureType: FailureTool,
		TraceID:     "trace-1",
		Ts:          time.Now(),
	}
	assert.True(t, sampler.ShouldSample(eventA))

	eventB := Event{
		Name:        EventReflection,
		DomainID:    "domain-b",
		FailureType: FailureRuntime,
		TraceID:     "trace-2",
		Ts:          time.Now(),
	}
	assert.True(t, sampler.ShouldSample(eventB))

	// 不应该采样的事件（domain 不匹配）
	eventC := Event{
		Name:        EventToolDecision,
		DomainID:    "domain-c",
		FailureType: FailureTool,
		TraceID:     "trace-3",
		Ts:          time.Now(),
	}
	assert.False(t, sampler.ShouldSample(eventC))
}

func TestShadowEvalSamplesQualityEventsByFailureType(t *testing.T) {
	config := ShadowEvalConfig{
		Enabled:            true,
		SamplingRate:       1.0,
		FailureTypeFilters: []FailureType{FailureTool, FailurePermission},
		MaxConcurrent:      2,
	}

	sampler := NewConfigurableSampler(config)

	// 应该采样的事件
	eventTool := Event{
		Name:        EventToolDecision,
		DomainID:    "domain-a",
		FailureType: FailureTool,
		TraceID:     "trace-1",
		Ts:          time.Now(),
	}
	assert.True(t, sampler.ShouldSample(eventTool))

	eventPermission := Event{
		Name:        EventPermissionDecision,
		DomainID:    "domain-a",
		FailureType: FailurePermission,
		TraceID:     "trace-2",
		Ts:          time.Now(),
	}
	assert.True(t, sampler.ShouldSample(eventPermission))

	// 不应该采样的事件（failure type 不匹配）
	eventRuntime := Event{
		Name:        EventReflection,
		DomainID:    "domain-a",
		FailureType: FailureRuntime,
		TraceID:     "trace-3",
		Ts:          time.Now(),
	}
	assert.False(t, sampler.ShouldSample(eventRuntime))
}

func TestShadowEvalNeverBlocksUserResponse(t *testing.T) {
	config := ShadowEvalConfig{
		Enabled:       true,
		SamplingRate:  1.0,
		MaxConcurrent: 1, // 只允许 1 个并发
	}

	sampler := NewConfigurableSampler(config)
	store := NewInMemoryShadowEvalResultStore()

	// 使用静态 runner 进行测试
	runner := StaticEvalRunner{}
	evaluator := HeuristicReflectionEvaluator{}

	shadowRunner := NewShadowEvalRunner(sampler, runner, evaluator, store, 1)

	event1 := Event{
		Name:        EventToolDecision,
		DomainID:    "domain-a",
		FailureType: FailureTool,
		TraceID:     "trace-1",
		Ts:          time.Now(),
	}

	event2 := Event{
		Name:        EventToolDecision,
		DomainID:    "domain-a",
		FailureType: FailureTool,
		TraceID:     "trace-2",
		Ts:          time.Now(),
	}

	// 第一次调用应该立即返回（启动异步评测）
	start := time.Now()
	err := shadowRunner.RunShadowEval(context.Background(), event1)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond, "RunShadowEval should return immediately")

	// 第二次调用应该立即返回（信号量已满，跳过）
	start = time.Now()
	err = shadowRunner.RunShadowEval(context.Background(), event2)
	elapsed = time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 100*time.Millisecond, "RunShadowEval should return immediately even when semaphore is full")

	// 等待异步评测完成
	time.Sleep(200 * time.Millisecond)

	// 验证至少有一个结果被存储
	results, err := store.ListRecent(context.Background(), "domain-a", 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 1, "At least one shadow eval should complete")
}

func TestShadowEvalUsesRealIOFromAttributes(t *testing.T) {
	sampler := NewConfigurableSampler(ShadowEvalConfig{
		Enabled:      true,
		SamplingRate: 1.0,
	})
	runner := captureShadowEvalRunner{cases: make(chan []LoadedCase, 1)}
	evaluator := captureShadowEvaluator{inputs: make(chan EvaluationInput, 1)}
	shadowRunner := NewShadowEvalRunner(sampler, runner, evaluator, nil, 1)

	event := Event{
		Name:          EventReflection,
		SessionIDHash: "sha256:session",
		DomainID:      "domain-a",
		FailureType:   FailureRuntime,
		TraceID:       "trace-real-io",
		Route:         "web",
		FinalStatus:   StatusFail,
		Attributes: map[string]any{
			"user_input":       "帮我总结订单 123",
			"assistant_output": "订单 123 已完成",
		},
		Ts: time.Unix(1700000000, 0),
	}

	require.NoError(t, shadowRunner.RunShadowEval(context.Background(), event))

	var loaded []LoadedCase
	select {
	case loaded = <-runner.cases:
	case <-time.After(time.Second):
		t.Fatal("shadow eval runner did not run")
	}
	require.Len(t, loaded, 1)
	assert.Equal(t, "帮我总结订单 123", loaded[0].Case.Input)
	assert.Equal(t, "shadow_eval_real_io", loaded[0].Case.CreatedFrom)

	var input EvaluationInput
	select {
	case input = <-evaluator.inputs:
	case <-time.After(time.Second):
		t.Fatal("shadow evaluator did not run")
	}
	assert.Equal(t, "sha256:session", input.SessionID)
	assert.Equal(t, "trace-real-io", input.TraceID)
	assert.Equal(t, "shadow_eval", input.Trigger)
	assert.Equal(t, "帮我总结订单 123", input.UserInput)
	assert.Equal(t, "订单 123 已完成", input.AssistantOutput)
}

func TestShadowEvalRedactsAttributeIOBeforeEvaluation(t *testing.T) {
	sampler := NewConfigurableSampler(ShadowEvalConfig{
		Enabled:      true,
		SamplingRate: 1.0,
	})
	runner := captureShadowEvalRunner{cases: make(chan []LoadedCase, 1)}
	evaluator := captureShadowEvaluator{inputs: make(chan EvaluationInput, 1)}
	shadowRunner := NewShadowEvalRunner(sampler, runner, evaluator, nil, 1)

	event := Event{
		Name:        EventToolDecision,
		DomainID:    "domain-a",
		FailureType: FailureTool,
		TraceID:     "trace-redacted",
		Attributes: map[string]any{
			"input": map[string]any{
				"query":     "调用订单接口",
				"api_token": "secret-token",
			},
			"final_output": "request failed: access_token=secret-token",
		},
		Ts: time.Unix(1700000000, 0),
	}

	require.NoError(t, shadowRunner.RunShadowEval(context.Background(), event))

	var loaded []LoadedCase
	select {
	case loaded = <-runner.cases:
	case <-time.After(time.Second):
		t.Fatal("shadow eval runner did not run")
	}
	require.Len(t, loaded, 1)
	assert.Contains(t, loaded[0].Case.Input, "调用订单接口")
	assert.NotContains(t, loaded[0].Case.Input, "secret-token")
	assert.Contains(t, loaded[0].Case.Input, "[REDACTED]")

	var input EvaluationInput
	select {
	case input = <-evaluator.inputs:
	case <-time.After(time.Second):
		t.Fatal("shadow evaluator did not run")
	}
	assert.Contains(t, input.UserInput, "调用订单接口")
	assert.NotContains(t, input.UserInput, "secret-token")
	assert.Equal(t, "request failed: access_token=[REDACTED]", input.AssistantOutput)
}

func TestShadowEvalFallsBackToTraceWhenRealIOIsMissing(t *testing.T) {
	event := Event{
		Name:    EventReflection,
		TraceID: "trace-fallback",
		Attributes: map[string]any{
			"user_input": "   ",
		},
		Ts: time.Unix(1700000000, 0),
	}

	testCase := eventToCase(event)

	assert.Equal(t, "Shadow eval trace fallback: trace-fallback", testCase.Input)
	assert.Equal(t, "shadow_eval_trace_fallback", testCase.CreatedFrom)
}

func TestRollbackAlertIncludesJudgeEvidence(t *testing.T) {
	now := time.Now()

	// 构造影子评测结果
	results := []ShadowEvalResult{
		{
			CaseID:   "case-1",
			DomainID: "domain-a",
			Passed:   false,
			JudgeVerdict: EvaluationVerdict{
				Score:       4,
				Verdict:     "工具调用失败",
				FailureType: FailureTool,
			},
			RunnerInfo: RunnerInfo{
				EvidenceLevel: EvidenceProductionShadow,
			},
			TraceRef:  "trace-1",
			ReplayRef: "replay-1",
			Timestamp: now,
		},
		{
			CaseID:   "case-2",
			DomainID: "domain-a",
			Passed:   false,
			JudgeVerdict: EvaluationVerdict{
				Score:       3,
				Verdict:     "权限检查失败",
				FailureType: FailurePermission,
			},
			RunnerInfo: RunnerInfo{
				EvidenceLevel: EvidenceProductionShadow,
			},
			TraceRef:  "trace-2",
			ReplayRef: "replay-2",
			Timestamp: now,
		},
	}

	// 检测语义回归
	alert := DetectSemanticRegression(results, 7.0)
	require.NotNil(t, alert)

	// 验证告警包含证据
	assert.Equal(t, "domain-a", alert.DomainID)
	assert.Contains(t, alert.Reasons, "semantic_regression")
	assert.Len(t, alert.Evidence, 2, "Alert should include evidence for both low-scoring cases")

	// 验证证据包含必要字段
	for _, evidence := range alert.Evidence {
		assert.NotEmpty(t, evidence.CaseID, "Evidence must include case ID")
		assert.NotEmpty(t, evidence.TraceRef, "Evidence must include trace ref")
		assert.NotEmpty(t, evidence.JudgeVerdictRef, "Evidence must include judge verdict ref")
		assert.NotEmpty(t, evidence.RunnerEvidence, "Evidence must include runner evidence level")
		assert.Equal(t, string(EvidenceProductionShadow), evidence.RunnerEvidence)
	}
}

func TestSemanticRegressionTriggersRollbackAlert(t *testing.T) {
	now := time.Now()

	// 构造低分结果（平均分 5.0，低于基线 7.0）
	results := []ShadowEvalResult{
		{
			CaseID:   "case-1",
			DomainID: "domain-a",
			Passed:   false,
			JudgeVerdict: EvaluationVerdict{
				Score:       5,
				Verdict:     "部分失败",
				FailureType: FailureTool,
			},
			RunnerInfo: RunnerInfo{
				EvidenceLevel: EvidenceProductionShadow,
			},
			TraceRef:  "trace-1",
			Timestamp: now,
		},
		{
			CaseID:   "case-2",
			DomainID: "domain-a",
			Passed:   false,
			JudgeVerdict: EvaluationVerdict{
				Score:       5,
				Verdict:     "部分失败",
				FailureType: FailureTool,
			},
			RunnerInfo: RunnerInfo{
				EvidenceLevel: EvidenceProductionShadow,
			},
			TraceRef:  "trace-2",
			Timestamp: now,
		},
	}

	// 检测语义回归
	alert := DetectSemanticRegression(results, 7.0)
	require.NotNil(t, alert, "Should trigger alert when avg score < baseline")

	assert.Equal(t, "domain-a", alert.DomainID)
	assert.Contains(t, alert.Reasons, "semantic_regression")
	assert.Equal(t, "high", alert.Severity) // delta = -2.0, triggers "high"
	assert.Less(t, alert.SuccessRateDelta, 0.0, "Delta should be negative for regression")

	// 测试高分结果（不应触发告警）
	goodResults := []ShadowEvalResult{
		{
			CaseID:   "case-3",
			DomainID: "domain-a",
			Passed:   true,
			JudgeVerdict: EvaluationVerdict{
				Score:       8,
				Verdict:     "成功",
				FailureType: FailureNone,
			},
			RunnerInfo: RunnerInfo{
				EvidenceLevel: EvidenceProductionShadow,
			},
			TraceRef:  "trace-3",
			Timestamp: now,
		},
	}

	noAlert := DetectSemanticRegression(goodResults, 7.0)
	assert.Nil(t, noAlert, "Should not trigger alert when avg score >= baseline")
}

func TestDetectSafetyFailureSpike(t *testing.T) {
	now := time.Now()

	// 构造包含安全失败的结果
	results := []ShadowEvalResult{
		{
			CaseID:   "case-1",
			DomainID: "domain-a",
			JudgeVerdict: EvaluationVerdict{
				Score:       4,
				Verdict:     "权限检查失败",
				FailureType: FailurePermission,
			},
			RunnerInfo: RunnerInfo{
				EvidenceLevel: EvidenceProductionShadow,
			},
			TraceRef:  "trace-1",
			Timestamp: now,
		},
		{
			CaseID:   "case-2",
			DomainID: "domain-a",
			JudgeVerdict: EvaluationVerdict{
				Score:       5,
				Verdict:     "运行时错误",
				FailureType: FailureRuntime,
			},
			RunnerInfo: RunnerInfo{
				EvidenceLevel: EvidenceProductionShadow,
			},
			TraceRef:  "trace-2",
			Timestamp: now,
		},
		{
			CaseID:   "case-3",
			DomainID: "domain-a",
			JudgeVerdict: EvaluationVerdict{
				Score:       8,
				Verdict:     "成功",
				FailureType: FailureNone,
			},
			RunnerInfo: RunnerInfo{
				EvidenceLevel: EvidenceProductionShadow,
			},
			TraceRef:  "trace-3",
			Timestamp: now,
		},
	}

	// 检测安全失败激增（阈值 0.5，实际 2/3 = 0.67）
	alert := DetectSafetyFailureSpike(results, 0.5)
	require.NotNil(t, alert, "Should trigger alert when safety failure rate > threshold")

	assert.Equal(t, "domain-a", alert.DomainID)
	assert.Contains(t, alert.Reasons, "safety_failure_spike")
	assert.Equal(t, "critical", alert.Severity)
	assert.Len(t, alert.Evidence, 2, "Should include evidence for both safety failures")
}

func TestDetectToolMisuseIncrease(t *testing.T) {
	now := time.Now()

	// 构造包含工具误用的结果
	results := []ShadowEvalResult{
		{
			CaseID:   "case-1",
			DomainID: "domain-a",
			JudgeVerdict: EvaluationVerdict{
				Score:       5,
				Verdict:     "工具调用错误",
				FailureType: FailureTool,
			},
			RunnerInfo: RunnerInfo{
				EvidenceLevel: EvidenceProductionShadow,
			},
			TraceRef:  "trace-1",
			Timestamp: now,
		},
		{
			CaseID:   "case-2",
			DomainID: "domain-a",
			JudgeVerdict: EvaluationVerdict{
				Score:       6,
				Verdict:     "工具参数错误",
				FailureType: FailureTool,
			},
			RunnerInfo: RunnerInfo{
				EvidenceLevel: EvidenceProductionShadow,
			},
			TraceRef:  "trace-2",
			Timestamp: now,
		},
		{
			CaseID:   "case-3",
			DomainID: "domain-a",
			JudgeVerdict: EvaluationVerdict{
				Score:       8,
				Verdict:     "成功",
				FailureType: FailureNone,
			},
			RunnerInfo: RunnerInfo{
				EvidenceLevel: EvidenceProductionShadow,
			},
			TraceRef:  "trace-3",
			Timestamp: now,
		},
	}

	// 检测工具误用增加（阈值 0.5，实际 2/3 = 0.67）
	alert := DetectToolMisuseIncrease(results, 0.5)
	require.NotNil(t, alert, "Should trigger alert when tool misuse rate > threshold")

	assert.Equal(t, "domain-a", alert.DomainID)
	assert.Contains(t, alert.Reasons, "tool_misuse_increase")
	assert.Equal(t, "high", alert.Severity)
	assert.Len(t, alert.Evidence, 2, "Should include evidence for both tool misuses")
}

func TestShadowEvalResultStore(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryShadowEvalResultStore()

	now := time.Now()

	// 存储多个结果
	results := []ShadowEvalResult{
		{
			CaseID:    "case-1",
			DomainID:  "domain-a",
			Passed:    true,
			Timestamp: now,
		},
		{
			CaseID:    "case-2",
			DomainID:  "domain-a",
			Passed:    false,
			Timestamp: now.Add(time.Second),
		},
		{
			CaseID:    "case-3",
			DomainID:  "domain-b",
			Passed:    true,
			Timestamp: now.Add(2 * time.Second),
		},
	}

	for _, result := range results {
		err := store.Store(ctx, result)
		require.NoError(t, err)
	}

	// 查询 domain-a 的结果
	domainAResults, err := store.ListRecent(ctx, "domain-a", 10)
	require.NoError(t, err)
	assert.Len(t, domainAResults, 2, "Should return 2 results for domain-a")

	// 查询所有结果
	allResults, err := store.ListRecent(ctx, "", 10)
	require.NoError(t, err)
	assert.Len(t, allResults, 3, "Should return all 3 results")

	// 验证结果按时间倒序排列（最新的在前）
	assert.Equal(t, "case-3", allResults[0].CaseID)
	assert.Equal(t, "case-2", allResults[1].CaseID)
	assert.Equal(t, "case-1", allResults[2].CaseID)
}
