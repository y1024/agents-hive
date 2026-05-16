package toolruntime

import (
	"fmt"
	"strings"
)

const RecoverableToolCallErrorMarker = "可恢复工具调用错误"

// RecoverableToolCallErrorContent 生成统一的可修复工具错误文本。
// 这类错误表示本次工具未执行，应该把结构化修复信息交回模型下一步重构调用。
func RecoverableToolCallErrorContent(kind, detail string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "tool_call_needs_repair"
	}
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "当前工具调用未执行。请根据本轮允许的工具和参数约束重新构造调用，不要重复相同工具和参数。"
	}
	return fmt.Sprintf("[%s: %s] %s", RecoverableToolCallErrorMarker, kind, detail)
}

func IsRecoverableToolCallError(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(content, RecoverableToolCallErrorMarker) || strings.Contains(lower, "recoverable_tool_call")
}

func RecoverableToolCallErrorKind(content string) string {
	prefix := "[" + RecoverableToolCallErrorMarker + ": "
	start := strings.Index(content, prefix)
	if start < 0 {
		return ""
	}
	start += len(prefix)
	end := strings.Index(content[start:], "]")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(content[start : start+end])
}
