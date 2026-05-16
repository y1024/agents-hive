package agentquality

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// JudgeEvaluator 语义评判接口，判断 agent 是否真正解决了用户问题
type JudgeEvaluator interface {
	Judge(ctx context.Context, input JudgeInput) (JudgeVerdict, error)
}

// JudgeInput 语义评判输入
type JudgeInput struct {
	CaseID         string            `json:"case_id"`
	DomainID       string            `json:"domain_id,omitempty"`
	UserInput      string            `json:"user_input"`
	FinalOutput    string            `json:"final_output"`
	ExpectedAnswer string            `json:"expected_answer,omitempty"`
	Rubric         []RubricCriterion `json:"rubric,omitempty"`
	TraceSummary   TraceSummary      `json:"trace_summary"`
	ToolAssertions []ToolAssertion   `json:"tool_assertions,omitempty"`
}

// RubricCriterion 评分标准项
type RubricCriterion struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Weight      int    `json:"weight"` // 1-10
}

// TraceSummary trace 执行摘要
type TraceSummary struct {
	ToolsCalled []string    `json:"tools_called"`
	EventCount  int         `json:"event_count"`
	FinalStatus FinalStatus `json:"final_status"`
	DomainID    string      `json:"domain_id,omitempty"`
}

// ToolAssertion 工具调用断言
type ToolAssertion struct {
	ToolName string `json:"tool_name"`
	Expected bool   `json:"expected"`
	Actual   bool   `json:"actual"`
}

// JudgeVerdict 语义评判结果
type JudgeVerdict struct {
	Score          int         `json:"score"`           // 0-10
	Passed         bool        `json:"passed"`          // 是否通过
	FailureType    FailureType `json:"failure_type"`    // 失败类型
	Rationale      string      `json:"rationale"`       // 评判理由
	Evidence       []string    `json:"evidence"`        // 证据列表
	ShouldOptimize bool        `json:"should_optimize"` // 是否需要优化
}

// ValidateJudgeVerdict 验证 judge verdict 的合法性
func ValidateJudgeVerdict(verdict JudgeVerdict) error {
	if verdict.Score < 0 || verdict.Score > 10 {
		return fmt.Errorf("score must be between 0 and 10, got %d", verdict.Score)
	}
	if strings.TrimSpace(verdict.Rationale) == "" {
		return fmt.Errorf("rationale must not be empty")
	}
	if verdict.FailureType != "" && !isValidFailureType(verdict.FailureType) {
		return fmt.Errorf("failure_type must be a known failure type, got %s", verdict.FailureType)
	}
	return nil
}

// RedactJudgeInput 脱敏 judge input，移除敏感信息
func RedactJudgeInput(input JudgeInput) JudgeInput {
	redacted := input
	redacted.UserInput = redactSecrets(input.UserInput)
	redacted.FinalOutput = redactSecrets(input.FinalOutput)
	redacted.ExpectedAnswer = redactSecrets(input.ExpectedAnswer)
	return redacted
}

var (
	// 匹配常见的 token/key 模式
	tokenPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(bearer\s+)[a-zA-Z0-9\-._~+/]+=*`),
		regexp.MustCompile(`(?i)(token["\s:=]+)[a-zA-Z0-9\-._~+/]{20,}`),
		regexp.MustCompile(`(?i)(api[_-]?key["\s:=]+)[a-zA-Z0-9\-._~+/]{20,}`),
		regexp.MustCompile(`(?i)(secret["\s:=]+)[a-zA-Z0-9\-._~+/]{20,}`),
		regexp.MustCompile(`(?i)(password["\s:=]+)[^\s"']{8,}`),
		regexp.MustCompile(`(?i)(authorization:\s*)\S+(\s+\S+)?`),
	}
)

func redactSecrets(text string) string {
	result := text
	for _, pattern := range tokenPatterns {
		result = pattern.ReplaceAllString(result, "${1}[REDACTED]")
	}
	return result
}

// FakeJudgeEvaluator 测试用假 judge，返回可配置的结果
type FakeJudgeEvaluator struct {
	Verdict JudgeVerdict
	Err     error
}

func (f FakeJudgeEvaluator) Judge(_ context.Context, _ JudgeInput) (JudgeVerdict, error) {
	return f.Verdict, f.Err
}

// HeuristicJudgeEvaluator 基于规则的 judge，使用断言和工具匹配
type HeuristicJudgeEvaluator struct{}

func (HeuristicJudgeEvaluator) Judge(_ context.Context, input JudgeInput) (JudgeVerdict, error) {
	score := 5 // 基准分
	evidence := []string{}
	failureType := FailureNone

	// 检查工具断言
	toolAssertionsPassed := true
	for _, assertion := range input.ToolAssertions {
		if assertion.Expected != assertion.Actual {
			toolAssertionsPassed = false
			evidence = append(evidence, fmt.Sprintf("tool_assertion_failed: %s (expected=%v, actual=%v)",
				assertion.ToolName, assertion.Expected, assertion.Actual))
			failureType = FailureTool
			score -= 2
		}
	}

	// 检查 final status
	if input.TraceSummary.FinalStatus == StatusFail {
		evidence = append(evidence, "trace_final_status: fail")
		failureType = FailureRuntime
		score -= 3
	} else if input.TraceSummary.FinalStatus == StatusBlocked {
		evidence = append(evidence, "trace_final_status: blocked")
		failureType = FailurePermission
		score -= 2
	}

	// 检查是否有输出
	if strings.TrimSpace(input.FinalOutput) == "" {
		evidence = append(evidence, "empty_final_output")
		if failureType == FailureNone {
			failureType = FailureRuntime
		}
		score -= 2
	}

	// 检查是否调用了工具
	if len(input.TraceSummary.ToolsCalled) == 0 && len(input.ToolAssertions) > 0 {
		evidence = append(evidence, "no_tools_called")
		if failureType == FailureNone {
			failureType = FailureTool
		}
		score -= 1
	}

	// 确保分数在 0-10 范围内
	if score < 0 {
		score = 0
	}
	if score > 10 {
		score = 10
	}

	passed := score >= 7 && toolAssertionsPassed
	rationale := fmt.Sprintf("启发式评判: 工具断言通过=%v, 最终状态=%s, 分数=%d",
		toolAssertionsPassed, input.TraceSummary.FinalStatus, score)

	return JudgeVerdict{
		Score:          score,
		Passed:         passed,
		FailureType:    failureType,
		Rationale:      rationale,
		Evidence:       evidence,
		ShouldOptimize: !passed,
	}, nil
}

// LLMJudgeEvaluator 基于 LLM 的 judge，调用 LLM 进行语义评判
type LLMJudgeEvaluator struct {
	// LLMClient 用于调用 LLM 的客户端接口
	// 实际实现需要注入具体的 LLM 调用逻辑
	CallLLM func(ctx context.Context, prompt string) (string, error)
}

func (e LLMJudgeEvaluator) Judge(ctx context.Context, input JudgeInput) (JudgeVerdict, error) {
	if e.CallLLM == nil {
		return JudgeVerdict{}, fmt.Errorf("LLM client not configured")
	}

	// 脱敏输入
	redactedInput := RedactJudgeInput(input)

	// 构建 prompt
	prompt := buildJudgePrompt(redactedInput)

	// 调用 LLM
	response, err := e.CallLLM(ctx, prompt)
	if err != nil {
		return JudgeVerdict{}, fmt.Errorf("LLM call failed: %w", err)
	}

	// 解析响应
	verdict, err := parseJudgeResponse(response)
	if err != nil {
		return JudgeVerdict{}, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	// 验证结果
	if err := ValidateJudgeVerdict(verdict); err != nil {
		return JudgeVerdict{}, fmt.Errorf("invalid LLM verdict: %w", err)
	}

	return verdict, nil
}

func buildJudgePrompt(input JudgeInput) string {
	// 使用 judge_prompt.go 中的模板
	return RenderJudgePromptV1(input)
}

func parseJudgeResponse(response string) (JudgeVerdict, error) {
	return ParseJudgeResponseV1(response)
}
