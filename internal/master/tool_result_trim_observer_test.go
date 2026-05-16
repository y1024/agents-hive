package master

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chef-guo/agents-hive/internal/compaction"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/tools"
	"go.uber.org/zap"
)

func TestReadTrackerTrimObserverRemovesCompactedReadFileResult(t *testing.T) {
	host := newReadTrackerTrimTestHost(t)
	dir := t.TempDir()
	t.Setenv("TOOLS_WORK_DIR", dir)
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	callTrimTestTool(t, host, "read_file", map[string]any{"path": path})

	messages := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("read file")},
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{ID: "call-read", Name: "read_file", Arguments: []byte(`{"path":"` + path + `"}`)},
			},
		},
		{Role: "tool", ToolCallID: "call-read", ToolName: "read_file", Content: llm.NewTextContent(strings.Repeat("x", 512))},
		{Role: "user", Content: llm.NewTextContent("latest")},
	}
	c := &compaction.ToolResultBudgetCompactor{
		ProtectedTurns:  1,
		OutputThreshold: 100,
		ContextBudget:   10000,
		Observer:        toolsReadTrackerTrimObserver{},
	}
	if _, err := c.Compact(context.Background(), messages, 0); err != nil {
		t.Fatal(err)
	}
	write := callTrimTestTool(t, host, "write_file", map[string]any{"path": path, "content": "new"})
	if !write.IsError {
		t.Fatal("compacted read_file result should force later writes to re-read")
	}
	if got := write.DecodeContent(); !strings.Contains(got, "has not been read") && !strings.Contains(got, "尚未读取") {
		t.Fatalf("expected read-before-write error after compaction, got %q", got)
	}
}

func TestReadTrackerTrimObserverRemovesCompactedFilesystemReadResult(t *testing.T) {
	host := newReadTrackerTrimTestHost(t)
	dir := t.TempDir()
	t.Setenv("TOOLS_WORK_DIR", dir)
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	callTrimTestTool(t, host, "filesystem", map[string]any{"action": "read", "path": path})

	event := compaction.ToolResultTrimEvent{
		ToolName:  "filesystem",
		Arguments: []byte(`{"action":"read","path":"` + path + `"}`),
	}
	toolsReadTrackerTrimObserver{}.OnToolResultTrimmed(event)

	write := callTrimTestTool(t, host, "filesystem", map[string]any{"action": "write", "path": path, "content": "new"})
	if !write.IsError {
		t.Fatal("compacted filesystem.read result should force later writes to re-read")
	}
	if got := write.DecodeContent(); !strings.Contains(got, "需要先读取目标文件") {
		t.Fatalf("expected sanitized read-before-write error after compaction, got %q", got)
	}
}

func newReadTrackerTrimTestHost(t *testing.T) *mcphost.Host {
	t.Helper()
	host := mcphost.NewHost(zap.NewNop())
	tools.RegisterBuiltinTools(host, zap.NewNop(), nil, nil, nil, "", nil, nil, nil, nil, nil)
	return host
}

func callTrimTestTool(t *testing.T, host *mcphost.Host, name string, input map[string]any) *mcphost.ToolResult {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := host.ExecuteTool(context.Background(), name, raw)
	if err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return result
}

func TestToolMetricsCountersCoverSuccessAndFilesystemToolError(t *testing.T) {
	m := newPhase6MasterWithMCPHost(t)
	m.config.ActionGuardEnabled = false
	m.obsCh = make(chan observabilityEntry, 16)
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "filesystem", Description: "filesystem", Core: true},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			return &mcphost.ToolResult{Content: jsonTestText("filesystem.read: missing_field: 缺少 path"), IsError: true}, nil
		},
	)

	result := m.executeTool(context.Background(), newTestSession("tool-metrics-error"), "user-1", llm.ToolCall{
		ID:        "tool-metrics-error-1",
		Name:      "filesystem",
		Arguments: json.RawMessage(`{"action":"read"}`),
	}, "trace-tool-metrics-error", "span-parent")

	if !result.IsError {
		t.Fatal("expected filesystem tool error")
	}
	assertObsMetric(t, m, "hive_tool_call_total", map[string]any{"tool_name": "filesystem", "action": "read", "status": "error"})
	assertObsMetric(t, m, "hive_tool_error_total", map[string]any{"tool_name": "filesystem", "action": "read", "reason": "missing_field"})

	m.obsCh = make(chan observabilityEntry, 16)
	m.mcpHost.RegisterTool(
		mcphost.ToolDefinition{Name: "read_file", Description: "read", Core: true},
		func(context.Context, json.RawMessage) (*mcphost.ToolResult, error) {
			return &mcphost.ToolResult{Content: jsonTestText("ok")}, nil
		},
	)
	success := m.executeTool(context.Background(), newTestSession("tool-metrics-success"), "user-1", llm.ToolCall{
		ID:        "tool-metrics-success-1",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"README.md"}`),
	}, "trace-tool-metrics-success", "span-parent")

	if success.IsError {
		t.Fatalf("expected success, got %s", success.Content)
	}
	assertObsMetric(t, m, "hive_tool_call_total", map[string]any{"tool_name": "read_file", "action": "unknown", "status": "success"})
}
