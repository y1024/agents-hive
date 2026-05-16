package agentquality

import (
	"context"
	"fmt"
	"time"
)

// FakeAgentAdapter 是用于测试的假 agent 适配器。
// 它不执行真实的 agent 逻辑，而是根据 case 的预期结果返回模拟输出。
type FakeAgentAdapter struct {
	// SimulateSuccess 控制是否模拟成功执行（默认 true）
	SimulateSuccess bool
	// SimulatedEvents 是模拟返回的事件列表
	SimulatedEvents []Event
	// SimulatedToolCalls 是模拟返回的工具调用列表
	SimulatedToolCalls []ObservedToolCall
}

// RunCase 执行单个 case（模拟实现）
func (f *FakeAgentAdapter) RunCase(ctx context.Context, input AgentRunCaseInput) (AgentRunCaseOutput, error) {
	if err := ctx.Err(); err != nil {
		return AgentRunCaseOutput{}, err
	}

	c := input.Case

	// 模拟执行延迟
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return AgentRunCaseOutput{}, ctx.Err()
	case <-timer.C:
	}

	// 构造模拟输出
	output := AgentRunCaseOutput{
		FinalOutput: fmt.Sprintf("Fake agent executed case %s: %s", c.ID, c.Input),
		FinalStatus: c.ExpectedStatus,
		TraceID:     fmt.Sprintf("fake-trace-%s", c.ID),
		ReplayRef:   fmt.Sprintf("fake-replay-%s", c.ID),
	}

	// 如果配置了模拟失败
	if !f.SimulateSuccess {
		output.FinalStatus = StatusFail
		output.FinalOutput = "Fake agent simulated failure"
	}

	// 添加模拟事件
	if len(f.SimulatedEvents) > 0 {
		output.Events = append([]Event{}, f.SimulatedEvents...)
	} else {
		// 默认生成一个基本事件
		output.Events = []Event{
			{
				Name:        EventAgentTurn,
				CaseID:      c.ID,
				Route:       c.Route,
				DomainID:    c.DomainID,
				SourceKind:  c.SourceKind,
				SourceName:  c.SourceName,
				FinalStatus: output.FinalStatus,
				TraceID:     output.TraceID,
				ReplayRef:   output.ReplayRef,
				Ts:          time.Now(),
			},
		}
	}

	// 添加模拟工具调用
	if len(f.SimulatedToolCalls) > 0 {
		output.ToolCalls = append([]ObservedToolCall{}, f.SimulatedToolCalls...)
	} else if len(c.ExpectedTools) > 0 {
		// 根据 case 的 ExpectedTools 生成模拟工具调用
		allowedSideEffects := stringSet(input.AllowedSideEffects)
		for _, tool := range c.ExpectedTools {
			output.ToolCalls = append(output.ToolCalls, ObservedToolCall{
				ToolName:   tool,
				Status:     "success",
				SideEffect: allowedSideEffects[tool],
			})
		}
	}

	if input.SandboxExternal {
		allowedSideEffects := stringSet(input.AllowedSideEffects)
		for _, call := range output.ToolCalls {
			if !call.SideEffect || allowedSideEffects[call.ToolName] {
				continue
			}
			output.FinalStatus = StatusBlocked
			output.FinalOutput = fmt.Sprintf("Fake agent blocked side-effect tool %s", call.ToolName)
			output.Events = append(output.Events, Event{
				Name:        EventPermissionDecision,
				CaseID:      c.ID,
				Route:       c.Route,
				DomainID:    c.DomainID,
				SourceKind:  c.SourceKind,
				SourceName:  c.SourceName,
				FinalStatus: StatusBlocked,
				FailureType: FailurePermission,
				ToolDecision: ToolDecision{
					Actual:   call.ToolName,
					Decision: DecisionRejected,
				},
				Ts: time.Now(),
			})
			break
		}
	}

	return output, nil
}

// NewFakeAgentAdapter 创建一个新的假 agent 适配器
func NewFakeAgentAdapter() *FakeAgentAdapter {
	return &FakeAgentAdapter{
		SimulateSuccess: true,
	}
}
