package master

import (
	"fmt"
	"strings"
)

type reflectionNoteInput struct {
	Trigger     string
	Severity    string
	ToolName    string
	Consecutive int
	Detail      string
	FailureKind string
}

func buildReflectionSystemNote(in reflectionNoteInput) string {
	trigger := strings.TrimSpace(in.Trigger)
	if trigger == "" {
		trigger = "unknown"
	}
	switch trigger {
	case "batch_loop":
		return fmt.Sprintf("[系统反思] 检测到相同工具组合连续出现 %d 次。请先说明重复原因，然后换一种策略；如果没有可行路径，直接向用户说明阻塞点，不要继续重复相同工具参数。", in.Consecutive)
	case "call_failure":
		return fmt.Sprintf("[系统反思] 工具 %s 使用相同参数连续失败 %d 次。下一步必须先验证前置条件、调整参数或换工具；不要再次调用同一 tool+args。", in.ToolName, in.Consecutive)
	case "guard_failure":
		return "[系统反思] 当前回答被质量护栏拦截。下一步必须补证据、调用必要工具或明确无法完成，不能直接复述被拦截内容。"
	case "validation_failure":
		return "[系统反思] 当前产物未通过后置验证。下一步必须根据验证错误修正证据链或输出结构。"
	case "intent_fulfillment":
		return "[系统反思] 用户目标尚未完成。下一步必须补齐缺失的能力链：如果用户要求发送给某人，先完成联系人查找；如果联系人有歧义，调用 question 工具向用户确认；确认后调用发送工具。不能把中间查询结果当作最终回复。"
	default:
		return "[系统反思] 当前执行路径没有取得进展。请总结阻塞原因并改变策略。"
	}
}
