package toolruntime

import (
	"encoding/json"
	"fmt"
	"strings"
)

const RecoverableToolCallErrorMarker = "可恢复工具调用错误"

type RecoverableToolCallError struct {
	Type                       string                        `json:"type"`
	Kind                       string                        `json:"kind"`
	Detail                     string                        `json:"detail"`
	RepairAction               string                        `json:"repair_action,omitempty"`
	ToolName                   string                        `json:"tool_name,omitempty"`
	AllowedTools               []string                      `json:"allowed_tools,omitempty"`
	AllowedInputs              map[string]map[string]string  `json:"allowed_inputs,omitempty"`
	ActionCapabilities         []RecoverableActionCapability `json:"action_capabilities,omitempty"`
	RecommendedToolSearchQuery string                        `json:"recommended_tool_search_query,omitempty"`
	Message                    string                        `json:"message"`
}

type RecoverableActionCapability struct {
	ToolName           string         `json:"tool_name"`
	Action             string         `json:"action"`
	ActionField        string         `json:"action_field,omitempty"`
	CapabilityID       string         `json:"capability_id,omitempty"`
	RequiredFields     []string       `json:"required_fields,omitempty"`
	PreparatoryActions []string       `json:"preparatory_actions,omitempty"`
	ExampleToolCall    map[string]any `json:"example_tool_call,omitempty"`
	InvocationHint     string         `json:"invocation_hint,omitempty"`
	RepairHint         string         `json:"repair_hint,omitempty"`
}

// RecoverableToolCallErrorContent 生成统一的可修复工具错误文本。
// 这类错误表示本次工具未执行，应该把结构化修复信息交回模型下一步重构调用。
func RecoverableToolCallErrorContent(kind, detail string) string {
	return RecoverableToolCallErrorContentWithHint(RecoverableToolCallError{
		Kind:   kind,
		Detail: detail,
	})
}

func RecoverableToolCallErrorContentWithHint(hint RecoverableToolCallError) string {
	kind := strings.TrimSpace(hint.Kind)
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "tool_call_needs_repair"
	}
	detail := strings.TrimSpace(hint.Detail)
	if detail == "" {
		detail = "当前工具调用未执行。请根据本轮允许的工具和参数约束重新构造调用，不要重复相同工具和参数。"
	}
	hint.Type = "recoverable_tool_call"
	hint.Kind = kind
	hint.Detail = detail
	if strings.TrimSpace(hint.RepairAction) == "" {
		hint.RepairAction = "rebuild_tool_call"
	}
	if strings.TrimSpace(hint.Message) == "" {
		hint.Message = "本次工具未执行。这是可恢复错误；下一步必须自动修复工具名或参数，必要时先调用 tool_search/question，不要把该错误当作最终回复。"
	}
	payload, err := json.Marshal(hint)
	if err != nil {
		return fmt.Sprintf("[%s: %s] %s", RecoverableToolCallErrorMarker, kind, detail)
	}
	return fmt.Sprintf("[%s: %s] %s", RecoverableToolCallErrorMarker, kind, string(payload))
}

func IsRecoverableToolCallError(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(content, RecoverableToolCallErrorMarker) || strings.Contains(lower, "recoverable_tool_call")
}

func RecoverableToolCallErrorKind(content string) string {
	if hint, ok := ParseRecoverableToolCallError(content); ok {
		return hint.Kind
	}
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

func ParseRecoverableToolCallError(content string) (RecoverableToolCallError, bool) {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return RecoverableToolCallError{}, false
	}
	var hint RecoverableToolCallError
	if err := json.Unmarshal([]byte(content[start:end+1]), &hint); err != nil {
		return RecoverableToolCallError{}, false
	}
	if strings.TrimSpace(hint.Type) != "recoverable_tool_call" {
		return RecoverableToolCallError{}, false
	}
	hint.Kind = strings.TrimSpace(hint.Kind)
	if hint.Kind == "" {
		hint.Kind = "tool_call_needs_repair"
	}
	return hint, true
}
