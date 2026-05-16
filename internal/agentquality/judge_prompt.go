package agentquality

import (
	"encoding/json"
	"fmt"
	"strings"
)

// JudgePromptVersion prompt 版本标识
const JudgePromptV1 = "judge_v1"

// RenderJudgePromptV1 渲染 v1 版本的 judge prompt
func RenderJudgePromptV1(input JudgeInput) string {
	var sb strings.Builder

	sb.WriteString("# Agent 质量语义评判任务\n\n")
	sb.WriteString("你是一个 Agent 质量评判专家，需要判断 Agent 是否真正解决了用户的问题。\n\n")

	sb.WriteString("## 用户输入\n")
	sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", input.UserInput))

	if input.ExpectedAnswer != "" {
		sb.WriteString("## 期望答案\n")
		sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", input.ExpectedAnswer))
	}

	sb.WriteString("## Agent 最终输出\n")
	sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", input.FinalOutput))

	sb.WriteString("## 执行轨迹摘要\n")
	sb.WriteString(fmt.Sprintf("- 调用工具: %v\n", input.TraceSummary.ToolsCalled))
	sb.WriteString(fmt.Sprintf("- 事件数量: %d\n", input.TraceSummary.EventCount))
	sb.WriteString(fmt.Sprintf("- 最终状态: %s\n", input.TraceSummary.FinalStatus))
	if input.TraceSummary.DomainID != "" {
		sb.WriteString(fmt.Sprintf("- 业务域: %s\n", input.TraceSummary.DomainID))
	}
	sb.WriteString("\n")

	if len(input.ToolAssertions) > 0 {
		sb.WriteString("## 工具调用断言\n")
		for _, assertion := range input.ToolAssertions {
			status := "✓"
			if assertion.Expected != assertion.Actual {
				status = "✗"
			}
			sb.WriteString(fmt.Sprintf("- %s %s: 期望=%v, 实际=%v\n",
				status, assertion.ToolName, assertion.Expected, assertion.Actual))
		}
		sb.WriteString("\n")
	}

	if len(input.Rubric) > 0 {
		sb.WriteString("## 评分标准 (Rubric)\n")
		for _, criterion := range input.Rubric {
			sb.WriteString(fmt.Sprintf("- **%s** (权重: %d/10): %s\n",
				criterion.Name, criterion.Weight, criterion.Description))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 评判要求\n\n")
	sb.WriteString("请根据以上信息，评判 Agent 是否成功解决了用户问题。\n\n")
	sb.WriteString("评判维度:\n")
	sb.WriteString("1. **正确性**: Agent 的输出是否正确回答了用户的问题\n")
	sb.WriteString("2. **完整性**: Agent 是否完整地完成了任务，没有遗漏关键步骤\n")
	sb.WriteString("3. **工具使用**: Agent 是否正确使用了必要的工具\n")
	sb.WriteString("4. **执行状态**: Agent 是否成功执行完成，没有错误或阻塞\n")
	if len(input.Rubric) > 0 {
		sb.WriteString("5. **Rubric 符合度**: Agent 的表现是否符合评分标准中的要求\n")
	}
	sb.WriteString("\n")

	sb.WriteString("## 输出格式\n\n")
	sb.WriteString("请以 JSON 格式输出评判结果:\n\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"score\": <0-10 的整数分数>,\n")
	sb.WriteString("  \"passed\": <true/false>,\n")
	sb.WriteString("  \"failure_type\": \"<none|prompt|tool|skill|context|model|permission|runtime|user_input>\",\n")
	sb.WriteString("  \"rationale\": \"<评判理由，说明为什么给出这个分数>\",\n")
	sb.WriteString("  \"evidence\": [\"<证据1>\", \"<证据2>\", ...],\n")
	sb.WriteString("  \"should_optimize\": <true/false>\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n\n")

	sb.WriteString("评分标准:\n")
	sb.WriteString("- 9-10分: 完美解决问题，输出准确完整\n")
	sb.WriteString("- 7-8分: 基本解决问题，有小瑕疵但不影响核心功能\n")
	sb.WriteString("- 5-6分: 部分解决问题，有明显不足\n")
	sb.WriteString("- 3-4分: 未能有效解决问题，但有尝试\n")
	sb.WriteString("- 0-2分: 完全失败或严重错误\n\n")

	sb.WriteString("请开始评判。\n")

	return sb.String()
}

// ParseJudgeResponseV1 解析 v1 版本的 judge 响应
func ParseJudgeResponseV1(response string) (JudgeVerdict, error) {
	// 提取 JSON 代码块
	jsonStr := extractJSONFromMarkdown(response)
	if jsonStr == "" {
		jsonStr = response
	}

	var verdict JudgeVerdict
	if err := json.Unmarshal([]byte(jsonStr), &verdict); err != nil {
		return JudgeVerdict{}, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return verdict, nil
}

// extractJSONFromMarkdown 从 markdown 代码块中提取 JSON
func extractJSONFromMarkdown(text string) string {
	// 查找 ```json ... ``` 或 ``` ... ``` 代码块
	start := strings.Index(text, "```json")
	if start == -1 {
		start = strings.Index(text, "```")
	}
	if start == -1 {
		return ""
	}

	// 跳过开始标记
	start = strings.Index(text[start:], "\n")
	if start == -1 {
		return ""
	}
	start += strings.Index(text, "```")

	// 查找结束标记
	end := strings.Index(text[start:], "```")
	if end == -1 {
		return ""
	}

	return strings.TrimSpace(text[start : start+end])
}
