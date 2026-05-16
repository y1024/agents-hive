package security

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

var sensitiveRedactionKeys = map[string]bool{
	"api_key":          true,
	"x_api_key":        true,
	"apikey":           true,
	"token":            true,
	"access_token":     true,
	"refresh_token":    true,
	"secret":           true,
	"password":         true,
	"credential":       true,
	"credentials":      true,
	"raw_credentials":  true,
	"context_token":    true,
	"authorization":    true,
	"cookie":           true,
	"set_cookie":       true,
	"private_key":      true,
	"client_secret":    true,
	"app_secret":       true,
	"aes_key":          true,
	"encrypt_key":      true,
	"encoding_aes_key": true,
	"websocket_token":  true,
	"jwt_secret":       true,
}

var sensitiveInlinePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(api[_-]?key|token|access[_-]?token|refresh[_-]?token|context[_-]?token|client[_-]?secret|app[_-]?secret|private[_-]?key|password|credential|secret)\s*[:=]\s*["']?[^"',\s}]+`),
	regexp.MustCompile(`(?i)\b(authorization)\s*[:=]\s*["']?bearer\s+[^"',\s}]+`),
	regexp.MustCompile(`(?i)\b(cookie|set[_-]?cookie)\s*[:=]\s*[^"\n\r]+`),
}

// RedactSecrets 递归清理结构化数据中的敏感字段。
func RedactSecrets(value any) (any, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return RedactJSON(v)
	case []byte:
		return redactMaybeJSON(string(v))
	case string:
		return redactMaybeJSON(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			if isSensitiveRedactionKey(key) {
				out[key] = RedactedValue
				continue
			}
			redacted, err := RedactSecrets(child)
			if err != nil {
				return nil, err
			}
			out[key] = redacted
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(v))
		for _, child := range v {
			redacted, err := RedactSecrets(child)
			if err != nil {
				return nil, err
			}
			out = append(out, redacted)
		}
		return out, nil
	default:
		return value, nil
	}
}

// RedactJSON 清理 JSON 文档中的敏感字段并返回规范化 JSON。
func RedactJSON(raw []byte) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`null`), nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	redacted, err := RedactSecrets(value)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(redacted)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

func redactMaybeJSON(text string) (any, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !looksLikeJSON(trimmed) {
		return redactInlineSecrets(text), nil
	}
	redacted, err := RedactJSON([]byte(trimmed))
	if err != nil {
		return redactInlineSecrets(text), nil
	}
	return string(redacted), nil
}

func redactInlineSecrets(text string) string {
	out := text
	for _, pattern := range sensitiveInlinePatterns {
		out = pattern.ReplaceAllStringFunc(out, func(match string) string {
			sep := strings.IndexAny(match, ":=")
			if sep < 0 {
				return RedactedValue
			}
			return match[:sep+1] + RedactedValue
		})
	}
	return out
}

func looksLikeJSON(text string) bool {
	if text == "" {
		return false
	}
	switch text[0] {
	case '{', '[':
		return true
	default:
		return false
	}
}

func isSensitiveRedactionKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	if sensitiveRedactionKeys[normalized] {
		return true
	}
	return strings.HasSuffix(normalized, "_api_key") ||
		strings.HasSuffix(normalized, "_token") ||
		strings.HasSuffix(normalized, "_secret") ||
		strings.HasSuffix(normalized, "_password") ||
		strings.HasSuffix(normalized, "_credential") ||
		strings.HasSuffix(normalized, "_credentials")
}

// HasRedactedMarker 判断一个字符串是否来自脱敏视图。
// 除了完整占位值，也识别内联脱敏后的字符串，例如 "token=[REDACTED]"。
func HasRedactedMarker(value string) bool {
	value = strings.TrimSpace(value)
	return value == RedactedValue || value == "****" ||
		strings.Contains(value, RedactedValue) ||
		strings.Contains(value, "****")
}

// PreserveRedactedValues 将 incoming 中的脱敏占位替换回 existing 的真实值。
// 用于 PATCH/配置保存场景：前端只能看到脱敏视图，不能把占位写回数据库。
func PreserveRedactedValues(incoming any, existing any) any {
	switch v := incoming.(type) {
	case string:
		if HasRedactedMarker(v) && existing != nil {
			return existing
		}
		return v
	case map[string]any:
		existingMap, _ := existing.(map[string]any)
		out := make(map[string]any, len(v))
		for k, child := range v {
			var existingChild any
			if existingMap != nil {
				existingChild = existingMap[k]
			}
			out[k] = PreserveRedactedValues(child, existingChild)
		}
		return out
	case []any:
		existingSlice, _ := existing.([]any)
		out := make([]any, len(v))
		for i, child := range v {
			var existingChild any
			if i < len(existingSlice) {
				existingChild = existingSlice[i]
			}
			out[i] = PreserveRedactedValues(child, existingChild)
		}
		return out
	default:
		return incoming
	}
}
