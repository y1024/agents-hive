package router

import (
	"encoding/json"
	"strings"
)

var routineExternalSendRecipientKeys = []string{
	"receive_id", "chat_id", "open_id", "user_id", "email",
	"recipient", "to", "recipient_id", "conversation_id",
}

var routineExternalSendMultiRecipientKeys = map[string]bool{
	"receive_ids":      true,
	"chat_ids":         true,
	"open_ids":         true,
	"user_ids":         true,
	"emails":           true,
	"recipients":       true,
	"recipient_ids":    true,
	"tos":              true,
	"to_list":          true,
	"conversation_ids": true,
	"targets":          true,
	"target_ids":       true,
}

var nonRoutineExternalSendKeys = map[string]bool{
	"attachment":         true,
	"attachments":        true,
	"at_all":             true,
	"audio":              true,
	"batch":              true,
	"broadcast":          true,
	"bulk":               true,
	"card":               true,
	"card_id":            true,
	"file":               true,
	"file_key":           true,
	"files":              true,
	"image":              true,
	"image_key":          true,
	"images":             true,
	"interactive_card":   true,
	"media":              true,
	"media_key":          true,
	"mention_all":        true,
	"notify_all":         true,
	"post":               true,
	"resource_key":       true,
	"rich_text":          true,
	"template":           true,
	"template_id":        true,
	"template_variables": true,
	"template_vars":      true,
	"video":              true,
	"all_members":        true,
	"all_member":         true,
	"all_staff":          true,
	"all_users":          true,
}

// IsRoutinePlainTextExternalSend reports whether the concrete input is the
// low-risk external-send subset: one recipient, plain text content, no media,
// card, template, broadcast, or bulk-send fields.
func IsRoutinePlainTextExternalSend(toolName string, input json.RawMessage) bool {
	toolName = ToolNamePolicy{}.Normalize(toolName)
	if !isExternalSendMessageAction(toolName, input) {
		return false
	}
	payload, ok := externalSendPayloadObject(input)
	if !ok {
		return false
	}
	if !externalSendHasString(payload, "content", "text", "message") {
		return false
	}
	if externalSendRecipientCount(payload) != 1 {
		return false
	}
	return !externalSendHasNonRoutinePayload(payload)
}

func isExternalSendMessageAction(toolName string, input json.RawMessage) bool {
	switch toolName {
	case "send_im_message":
		return true
	case "feishu_api", "im_api":
		return structuredAction(input) == "send_message"
	default:
		return false
	}
}

func externalSendPayloadObject(input json.RawMessage) (map[string]any, bool) {
	if len(input) == 0 {
		return nil, false
	}
	var payload map[string]any
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, false
	}
	return payload, true
}

func externalSendHasString(payload map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func externalSendRecipientCount(payload map[string]any) int {
	count := 0
	for _, key := range routineExternalSendRecipientKeys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			if strings.TrimSpace(text) != "" {
				count++
			}
			continue
		}
		if externalSendNonEmptyValue(value) {
			count += 2
		}
	}
	for key, value := range payload {
		if routineExternalSendMultiRecipientKeys[normalizeExternalSendKey(key)] && externalSendNonEmptyValue(value) {
			count += 2
		}
	}
	return count
}

func externalSendHasNonRoutinePayload(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			normalized := normalizeExternalSendKey(key)
			if nonRoutineExternalSendKeys[normalized] && externalSendNonEmptyValue(child) {
				return true
			}
			if routineExternalSendMultiRecipientKeys[normalized] && externalSendNonEmptyValue(child) {
				return true
			}
			if externalSendMessageTypeKey(normalized) && !externalSendTextMessageType(child) {
				return true
			}
			if externalSendHasNonRoutinePayload(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if externalSendHasNonRoutinePayload(child) {
				return true
			}
		}
	}
	return false
}

func externalSendMessageTypeKey(key string) bool {
	switch key {
	case "msg_type", "message_type", "content_type":
		return true
	default:
		return false
	}
}

func externalSendTextMessageType(value any) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	switch strings.TrimSpace(strings.ToLower(text)) {
	case "", "text", "plain_text", "plain-text":
		return true
	default:
		return false
	}
}

func externalSendNonEmptyValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case bool:
		return v
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	case float64:
		return v != 0
	default:
		return true
	}
}

func normalizeExternalSendKey(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	return normalized
}
