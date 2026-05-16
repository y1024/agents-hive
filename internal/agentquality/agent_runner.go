package agentquality

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

// AgentRunAdapter 是真实 agent 执行的最小适配接口。
// 实现者负责构造隔离 session、调用 master/session 入口、捕获质量事件和工具调用。
type AgentRunAdapter interface {
	RunCase(ctx context.Context, input AgentRunCaseInput) (AgentRunCaseOutput, error)
}

// AgentRunCaseInput 是单个 case 的执行输入。
type AgentRunCaseInput struct {
	Case               Case
	DomainID           string
	OwnerID            string
	SessionID          string
	RunID              string
	SandboxExternal    bool
	AllowedSideEffects []string
}

// AgentRunCaseOutput 是单个 case 的执行输出。
type AgentRunCaseOutput struct {
	FinalOutput string
	FinalStatus FinalStatus
	Events      []Event
	TraceID     string
	ReplayRef   string
	ToolCalls   []ObservedToolCall
}

// ObservedToolCall 是捕获的工具调用记录。
type ObservedToolCall struct {
	ToolName   string `json:"tool_name"`
	ArgsHash   string `json:"args_hash,omitempty"`
	Status     string `json:"status,omitempty"`
	Error      string `json:"error,omitempty"`
	SideEffect bool   `json:"side_effect,omitempty"`
}

// AgentRunEvalRunner 使用真实 agent 执行路径运行 cases。
type AgentRunEvalRunner struct {
	Adapter            AgentRunAdapter
	DomainID           string
	OwnerID            string
	SandboxExternal    bool     // 兼容旧配置；runner 零值也会默认 sandbox 外部写入。
	AllowedSideEffects []string // 允许的副作用工具白名单
}

func (r AgentRunEvalRunner) Run(cases []LoadedCase) (GateInput, error) {
	return r.RunWithContext(context.Background(), cases)
}

func (r AgentRunEvalRunner) RunWithContext(ctx context.Context, cases []LoadedCase) (GateInput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.Adapter == nil {
		return GateInput{}, fmt.Errorf("agent run adapter not configured")
	}

	runID := newAgentRunID()
	input := GateInput{
		Results:             make([]Result, 0, len(cases)),
		Events:              make([]Event, 0, len(cases)*2),
		EventsByCase:        make(map[string][]Event, len(cases)),
		ToolActualByCaseID:  make(map[string][]string, len(cases)),
		CandidateByCaseID:   make(map[string]bool),
		ReplayRefByCaseID:   make(map[string]string),
		FinalOutputByCaseID: make(map[string]string),
	}

	for i, lc := range cases {
		if err := ctx.Err(); err != nil {
			return input, err
		}

		c := lc.Case
		allowedSideEffects := allowedSideEffectsForCase(c, r.AllowedSideEffects)
		if violation := preflightSideEffectViolation(c, r.AllowedSideEffects, allowedSideEffects); violation != "" {
			input.Results = append(input.Results, Result{
				CaseID: c.ID,
				Passed: c.ExpectedStatus == StatusBlocked,
				Reason: violation,
			})
			continue
		}

		// 构造执行输入
		runInput := AgentRunCaseInput{
			Case:               c,
			DomainID:           firstNonEmpty(c.DomainID, r.DomainID),
			OwnerID:            firstNonEmpty(c.SourceName, r.OwnerID),
			SessionID:          agentRunSessionID(runID, c.ID, i),
			RunID:              runID,
			SandboxExternal:    true,
			AllowedSideEffects: allowedSideEffects,
		}

		// 执行 case
		output, err := r.Adapter.RunCase(ctx, runInput)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return input, ctxErr
		}

		// 记录结果
		passed := false
		reason := ""

		if err != nil {
			reason = fmt.Sprintf("agent run failed: %v", err)
		} else {
			// 检查 final status 是否匹配
			if output.FinalStatus != c.ExpectedStatus {
				reason = fmt.Sprintf("expected status %s, got %s", c.ExpectedStatus, output.FinalStatus)
			} else {
				if violation := sideEffectViolation(output.FinalStatus, output.ToolCalls, r.AllowedSideEffects, allowedSideEffects); violation != "" {
					reason = violation
				} else {
					// 检查工具调用是否匹配
					actualTools := extractToolNames(output.ToolCalls)
					if !toolsMatchExpectation(actualTools, c.ExpectedTools, c.AllowedTools) {
						reason = fmt.Sprintf("tool mismatch: expected %v, got %v", c.ExpectedTools, actualTools)
					} else {
						passed = true
					}
				}
			}
		}

		input.Results = append(input.Results, Result{
			CaseID: c.ID,
			Passed: passed,
			Reason: reason,
		})

		// 记录事件
		for _, ev := range output.Events {
			if ev.CaseID == "" {
				ev.CaseID = c.ID
			}
			input.Events = append(input.Events, ev)
			input.EventsByCase[c.ID] = append(input.EventsByCase[c.ID], ev)
		}

		// 记录工具调用
		if len(output.ToolCalls) > 0 {
			tools := extractToolNames(output.ToolCalls)
			input.ToolActualByCaseID[c.ID] = tools
		}

		// 记录 replay ref
		if output.ReplayRef != "" {
			input.ReplayRefByCaseID[c.ID] = output.ReplayRef
		}
		if output.FinalOutput != "" {
			input.FinalOutputByCaseID[c.ID] = output.FinalOutput
		}
	}

	return input, nil
}

func (r AgentRunEvalRunner) Info() RunnerInfo {
	return RunnerInfo{
		Name:          "agent_run",
		Version:       "1.0",
		EvidenceLevel: EvidenceRealRunner,
	}
}

func extractToolNames(calls []ObservedToolCall) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(calls))
	for _, call := range calls {
		if call.ToolName != "" && !seen[call.ToolName] {
			seen[call.ToolName] = true
			result = append(result, call.ToolName)
		}
	}
	return result
}

func toolsMatchExpectation(actual, expected, allowed []string) bool {
	if len(expected) > 0 {
		// 必须调用至少一个 expected tool
		for _, got := range actual {
			for _, want := range expected {
				if got == want {
					return true
				}
			}
		}
		return false
	}

	if len(allowed) > 0 {
		// 所有调用的工具必须在 allowed 列表中
		if len(actual) == 0 {
			return false
		}
		allowedSet := make(map[string]bool, len(allowed))
		for _, tool := range allowed {
			allowedSet[tool] = true
		}
		for _, got := range actual {
			if !allowedSet[got] {
				return false
			}
		}
		return true
	}

	// 没有工具约束，总是通过
	return true
}

var agentRunFallbackCounter uint64

func newAgentRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return "fallback-" + strconv.FormatUint(atomic.AddUint64(&agentRunFallbackCounter, 1), 10)
}

func agentRunSessionID(runID, caseID string, index int) string {
	return "eval-" + safeSessionSegment(caseID) + "-" + runID + "-" + strconv.Itoa(index+1)
}

func safeSessionSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "case"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func allowedSideEffectsForCase(c Case, runnerAllowed []string) []string {
	declared := make(map[string]bool, len(c.ExpectedTools)+len(c.AllowedTools))
	for _, tool := range c.ExpectedTools {
		if tool != "" {
			declared[tool] = true
		}
	}
	for _, tool := range c.AllowedTools {
		if tool != "" {
			declared[tool] = true
		}
	}

	allowed := make([]string, 0, len(runnerAllowed))
	seen := make(map[string]bool, len(runnerAllowed))
	for _, tool := range runnerAllowed {
		if tool == "" || seen[tool] || !declared[tool] {
			continue
		}
		seen[tool] = true
		allowed = append(allowed, tool)
	}
	return allowed
}

func preflightSideEffectViolation(c Case, runnerAllowed, caseAllowed []string) string {
	runnerAllowedSet := stringSet(runnerAllowed)
	caseAllowedSet := stringSet(caseAllowed)
	for _, tool := range append(append([]string(nil), c.ExpectedTools...), c.AllowedTools...) {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if !runnerAllowedSet[tool] {
			continue
		}
		if caseAllowedSet[tool] {
			continue
		}
		return fmt.Sprintf("side effect tool %s blocked before agent run: case must explicitly declare allowed side effect", tool)
	}
	if len(caseAllowedSet) == 0 && caseLooksLikeSideEffectIntent(c.Input) {
		if tool := firstStringMapKey(runnerAllowedSet); tool != "" {
			return fmt.Sprintf("side effect tool %s blocked before agent run: case must explicitly declare allowed side effect", tool)
		}
		return "side effect intent blocked before agent run: case must explicitly declare allowed side effect"
	}
	return ""
}

func caseLooksLikeSideEffectIntent(input string) bool {
	q := strings.ToLower(strings.TrimSpace(input))
	if q == "" || containsAnyString(q, "不要发送", "别发送", "不用发送", "先别发", "不要发", "别发", "don't send", "do not send") {
		return false
	}
	return containsAnyString(q,
		"发送给", "发给", "发到", "发送到", "转发给", "转给", "推送到", "通知到",
		"send message", "send this to", "send to", "forward this to", "forward to",
		"删除", "移除", "清空", "修改", "更新", "写入", "创建", "新增",
		"delete", "remove", "clear", "modify", "update", "write", "create",
	)
}

func containsAnyString(s string, patterns ...string) bool {
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(s, pattern) {
			return true
		}
	}
	return false
}

func firstStringMapKey(values map[string]bool) string {
	for value := range values {
		return value
	}
	return ""
}

func sideEffectViolation(finalStatus FinalStatus, calls []ObservedToolCall, runnerAllowed, caseAllowed []string) string {
	if len(calls) == 0 {
		return ""
	}
	caseAllowedSet := stringSet(caseAllowed)
	runnerAllowedSet := stringSet(runnerAllowed)
	for _, call := range calls {
		if call.ToolName == "" {
			continue
		}
		if !call.SideEffect && !runnerAllowedSet[call.ToolName] {
			continue
		}
		if caseAllowedSet[call.ToolName] {
			continue
		}
		if sideEffectContained(finalStatus, call.Status) {
			continue
		}
		return fmt.Sprintf("side effect tool %s blocked: case must explicitly declare allowed side effect", call.ToolName)
	}
	return ""
}

func sideEffectContained(finalStatus FinalStatus, callStatus string) bool {
	switch strings.ToLower(strings.TrimSpace(callStatus)) {
	case "blocked", "rejected", "denied", "needs_user":
		return true
	case "":
		return finalStatus == StatusBlocked || finalStatus == StatusNeedsUser
	default:
		return false
	}
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	return set
}
