package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeMetricLabels_DropsHighCardinality(t *testing.T) {
	got := SanitizeMetricLabels("hive.llm.duration_ms", map[string]any{
		"model":      "gpt-5.2",
		"session_id": "s1",
		"user_id":    "u1",
		"status":     "ok",
	})

	assert.Equal(t, "gpt-5.2", got["model"])
	assert.Equal(t, "ok", got["status"])
	assert.NotContains(t, got, "session_id")
	assert.NotContains(t, got, "user_id")
}

func TestSanitizeMetricLabels_DropsUnknownLabels(t *testing.T) {
	got := SanitizeMetricLabels("hive.tool.duration_ms", map[string]any{
		"tool_name": "shell",
		"command":   "rm -rf /tmp/example",
		"tool_args": map[string]any{"command": "rm -rf /tmp/example"},
	})

	assert.Equal(t, map[string]any{"tool_name": "shell"}, got)
}

func TestSanitizeMetricLabels_AllowsStopReasonAfterEmitterNormalization(t *testing.T) {
	got := SanitizeMetricLabels("quality.delegation", map[string]any{
		"status":      "fail",
		"stop_reason": "cancelled",
		"session_id":  "session-1",
	})

	assert.Equal(t, map[string]any{"status": "fail", "stop_reason": "cancelled"}, got)
}

func TestSanitizeMetricLabels_AllowsMetricSpecificLabels(t *testing.T) {
	got := SanitizeMetricLabels("hive.eventbus.dropped", map[string]any{
		"msg_type": "task",
		"route":    "cli",
		"trace_id": "trace-1",
	})

	assert.Equal(t, "task", got["msg_type"])
	assert.Equal(t, "cli", got["route"])
	assert.NotContains(t, got, "trace_id")

	triggerLabels := SanitizeMetricLabels("tool_choice_required_total", map[string]any{
		"trigger":    "router_intent",
		"session_id": "s1",
	})
	assert.Equal(t, map[string]any{"trigger": "router_intent"}, triggerLabels)
}

func TestSanitizeMetricLabels_AllowsToolDomainLowCardinalityLabels(t *testing.T) {
	got := SanitizeMetricLabels("hive_filesystem_action_total", map[string]any{
		"tool_name":  "filesystem",
		"action":     "edit",
		"status":     "error",
		"reason":     "route_input_denied",
		"path":       "/Users/example/secret.go",
		"content":    "secret",
		"old_string": "old",
		"new_string": "new",
	})

	assert.Equal(t, map[string]any{
		"tool_name": "filesystem",
		"action":    "edit",
		"status":    "error",
		"reason":    "route_input_denied",
	}, got)

	callLabels := SanitizeMetricLabels("hive_tool_call_total", map[string]any{
		"tool_name":  "filesystem",
		"action":     "read",
		"status":     "success",
		"path":       "/tmp/secret.go",
		"old_string": "secret",
	})
	assert.Equal(t, map[string]any{
		"tool_name": "filesystem",
		"action":    "read",
		"status":    "success",
	}, callLabels)

	errorLabels := SanitizeMetricLabels("hive_tool_error_total", map[string]any{
		"tool_name": "filesystem",
		"action":    "edit",
		"reason":    "missing_field",
		"content":   "secret",
	})
	assert.Equal(t, map[string]any{
		"tool_name": "filesystem",
		"action":    "edit",
		"reason":    "missing_field",
	}, errorLabels)
}

func TestSanitizeMetricLabels_ReturnsNilWhenEmptyAfterFiltering(t *testing.T) {
	got := SanitizeMetricLabels("hive.llm.duration_ms", map[string]any{
		"session_id": "s1",
		"user_id":    "u1",
	})

	assert.Nil(t, got)
}
