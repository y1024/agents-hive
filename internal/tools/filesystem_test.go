package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/mcphost"
)

func callFilesystem(t *testing.T, host *mcphost.Host, input map[string]any) *mcphost.ToolResult {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := host.ExecuteTool(context.Background(), "filesystem", raw)
	if err != nil {
		t.Fatalf("ExecuteTool filesystem: %v", err)
	}
	return result
}

func callLegacyTool(t *testing.T, host *mcphost.Host, name string, input map[string]any) *mcphost.ToolResult {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := host.ExecuteTool(context.Background(), name, raw)
	if err != nil {
		t.Fatalf("ExecuteTool %s: %v", name, err)
	}
	return result
}

func assertSameToolResult(t *testing.T, name string, legacy, unified *mcphost.ToolResult) {
	t.Helper()
	if legacy.IsError != unified.IsError {
		t.Fatalf("%s error mismatch: legacy=%v unified=%v legacyContent=%q unifiedContent=%q",
			name, legacy.IsError, unified.IsError, legacy.DecodeContent(), unified.DecodeContent())
	}
	if legacy.DecodeContent() != unified.DecodeContent() {
		t.Fatalf("%s content mismatch:\nlegacy:\n%s\nunified:\n%s", name, legacy.DecodeContent(), unified.DecodeContent())
	}
}

func TestFilesystemRegisterRespectsFeatureFlag(t *testing.T) {
	logger := zap.NewNop()
	enabledHost := mcphost.NewHost(logger)
	RegisterBuiltinTools(enabledHost, logger, nil, nil, nil, "", nil, nil, nil, nil, nil)
	if _, err := enabledHost.GetTool("filesystem"); err != nil {
		t.Fatalf("filesystem should be registered by default: %v", err)
	}

	disabled := false
	disabledHost := mcphost.NewHost(logger)
	RegisterBuiltinTools(disabledHost, logger, &config.Config{
		Tools: config.ToolsConfig{FilesystemEnabled: &disabled},
	}, nil, nil, "", nil, nil, nil, nil, nil)
	if _, err := disabledHost.GetTool("filesystem"); err == nil {
		t.Fatal("filesystem should not be registered when disabled")
	}
	if _, err := disabledHost.GetTool("read_file"); err != nil {
		t.Fatalf("legacy read_file should remain registered: %v", err)
	}
}

func TestFilesystemDefinitionConcurrencySafeFalseAndLegacyReadsSafe(t *testing.T) {
	host := newTestHost(t)
	fsDef, err := host.GetTool("filesystem")
	if err != nil {
		t.Fatalf("filesystem tool missing: %v", err)
	}
	if fsDef.IsConcurrencySafe {
		t.Fatal("filesystem must be unsafe at tool level")
	}

	for _, name := range []string{"read_file", "grep", "glob", "ls"} {
		def, err := host.GetTool(name)
		if err != nil {
			t.Fatalf("%s tool missing: %v", name, err)
		}
		if !def.IsConcurrencySafe {
			t.Fatalf("%s should remain concurrency safe", name)
		}
	}
}

func TestFilesystemReadOnlyActionsMatchLegacyTools(t *testing.T) {
	host := newTestHost(t)
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	alpha := filepath.Join(dir, "alpha.txt")
	beta := filepath.Join(nested, "beta.txt")
	if err := os.WriteFile(alpha, []byte("alpha\nneedle one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beta, []byte("beta\nneedle two\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	assertSameToolResult(t, "list",
		callLegacyTool(t, host, "ls", map[string]any{"path": dir}),
		callFilesystem(t, host, map[string]any{"action": "list", "path": dir}),
	)
	assertSameToolResult(t, "glob",
		callLegacyTool(t, host, "glob", map[string]any{"pattern": "*.txt", "path": dir}),
		callFilesystem(t, host, map[string]any{"action": "glob", "pattern": "*.txt", "path": dir}),
	)
	assertSameToolResult(t, "grep",
		callLegacyTool(t, host, "grep", map[string]any{"pattern": "needle", "path": dir, "glob": "*.txt"}),
		callFilesystem(t, host, map[string]any{"action": "grep", "pattern": "needle", "path": dir, "glob": "*.txt"}),
	)
	assertSameToolResult(t, "read",
		callLegacyTool(t, host, "read_file", map[string]any{"path": alpha, "show_line_numbers": true}),
		callFilesystem(t, host, map[string]any{"action": "read", "path": alpha, "show_line_numbers": true}),
	)
}

func TestFilesystemGrepOutputOrderStable(t *testing.T) {
	host := newTestHost(t)
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	alpha := filepath.Join(dir, "alpha.txt")
	beta := filepath.Join(nested, "beta.txt")
	if err := os.WriteFile(alpha, []byte("needle alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beta, []byte("needle beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		result *mcphost.ToolResult
	}{
		{
			name:   "legacy",
			result: callLegacyTool(t, host, "grep", map[string]any{"pattern": "needle", "path": dir, "glob": "*.txt"}),
		},
		{
			name:   "filesystem",
			result: callFilesystem(t, host, map[string]any{"action": "grep", "pattern": "needle", "path": dir, "glob": "*.txt"}),
		},
	} {
		if tc.result.IsError {
			t.Fatalf("%s grep failed: %s", tc.name, tc.result.DecodeContent())
		}
		lines := strings.Split(strings.TrimSpace(tc.result.DecodeContent()), "\n")
		if len(lines) != 2 {
			t.Fatalf("%s grep lines = %d, want 2: %q", tc.name, len(lines), tc.result.DecodeContent())
		}
		if !strings.Contains(lines[0], "alpha.txt") || !strings.Contains(lines[1], "nested/beta.txt") {
			t.Fatalf("%s grep output order is not stable:\n%s", tc.name, tc.result.DecodeContent())
		}
	}
}

func TestFilesystemReadWriteEditAndMultiEditReuseTrackers(t *testing.T) {
	host := newTestHost(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")

	writeNew := callFilesystem(t, host, map[string]any{
		"action":  "write",
		"path":    path,
		"content": "old\n",
	})
	if writeNew.IsError {
		t.Fatalf("write new file failed: %s", writeNew.DecodeContent())
	}

	writeExisting := callFilesystem(t, host, map[string]any{
		"action":  "write",
		"path":    path,
		"content": "new\n",
	})
	if !writeExisting.IsError {
		t.Fatal("write existing file should require prior read")
	}
	if got := writeExisting.DecodeContent(); !strings.Contains(got, "filesystem.write") || strings.Contains(got, "new\n") {
		t.Fatalf("write error should include action and not content, got %q", got)
	}

	read := callFilesystem(t, host, map[string]any{
		"action": "read",
		"path":   path,
	})
	if read.IsError {
		t.Fatalf("read failed: %s", read.DecodeContent())
	}

	edit := callFilesystem(t, host, map[string]any{
		"action":     "edit",
		"path":       path,
		"old_string": "old",
		"new_string": "new",
	})
	if edit.IsError {
		t.Fatalf("edit failed after read: %s", edit.DecodeContent())
	}

	secondPath := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(secondPath, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	readB := callFilesystem(t, host, map[string]any{
		"action": "read",
		"path":   secondPath,
	})
	if readB.IsError {
		t.Fatalf("read b failed: %s", readB.DecodeContent())
	}

	multi := callFilesystem(t, host, map[string]any{
		"action": "multiedit",
		"edits": []map[string]any{
			{
				"path":       path,
				"old_string": "new",
				"new_string": "newer",
			},
			{
				"path":       secondPath,
				"old_string": "before",
				"new_string": "after",
			},
		},
	})
	if multi.IsError {
		t.Fatalf("multiedit failed: %s", multi.DecodeContent())
	}
	got, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "after") {
		t.Fatalf("multiedit did not update second file: %q", string(got))
	}
}

func TestFilesystemMissingAndExecutorErrorsAreActionScopedAndSanitized(t *testing.T) {
	host := newTestHost(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("secret-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	missing := callFilesystem(t, host, map[string]any{"action": "grep"})
	if !missing.IsError {
		t.Fatal("grep without pattern should fail")
	}
	if got := missing.DecodeContent(); !strings.Contains(got, "filesystem.grep") || !strings.Contains(got, "missing_field") {
		t.Fatalf("missing field error not action scoped: %q", got)
	}

	edit := callFilesystem(t, host, map[string]any{
		"action":     "edit",
		"path":       path,
		"old_string": "secret-old",
		"new_string": "secret-new",
	})
	if !edit.IsError {
		t.Fatal("edit without read should fail")
	}
	got := edit.DecodeContent()
	if !strings.Contains(got, "filesystem.edit") {
		t.Fatalf("edit error should include action: %q", got)
	}
	for _, leaked := range []string{path, "secret-old", "secret-new"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("filesystem error leaked sensitive input %q in %q", leaked, got)
		}
	}
}

func TestFilesystemWriteAndEditRequiredStringPresence(t *testing.T) {
	host := newTestHost(t)
	dir := t.TempDir()

	missingContent := callFilesystem(t, host, map[string]any{
		"action": "write",
		"path":   filepath.Join(dir, "missing-content.txt"),
	})
	if !missingContent.IsError {
		t.Fatal("write without content should fail")
	}
	if got := missingContent.DecodeContent(); !strings.Contains(got, "filesystem.write") || !strings.Contains(got, "missing_field") || !strings.Contains(got, "content") {
		t.Fatalf("write missing content error mismatch: %q", got)
	}

	emptyPath := filepath.Join(dir, "empty.txt")
	emptyContent := ""
	writeEmpty := callFilesystem(t, host, map[string]any{
		"action":  "write",
		"path":    emptyPath,
		"content": emptyContent,
	})
	if writeEmpty.IsError {
		t.Fatalf("explicit empty write content should be allowed: %s", writeEmpty.DecodeContent())
	}
	if data, err := os.ReadFile(emptyPath); err != nil || string(data) != "" {
		t.Fatalf("empty write result mismatch: data=%q err=%v", string(data), err)
	}

	editPath := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(editPath, []byte("prefix delete-me suffix"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := callFilesystem(t, host, map[string]any{
		"action": "read",
		"path":   editPath,
	})
	if read.IsError {
		t.Fatalf("read before edit failed: %s", read.DecodeContent())
	}

	missingNewString := callFilesystem(t, host, map[string]any{
		"action":     "edit",
		"path":       editPath,
		"old_string": "delete-me",
	})
	if !missingNewString.IsError {
		t.Fatal("edit without new_string should fail")
	}
	if got := missingNewString.DecodeContent(); !strings.Contains(got, "filesystem.edit") || !strings.Contains(got, "missing_field") || !strings.Contains(got, "new_string") {
		t.Fatalf("edit missing new_string error mismatch: %q", got)
	}

	deleteReplacement := ""
	editEmpty := callFilesystem(t, host, map[string]any{
		"action":     "edit",
		"path":       editPath,
		"old_string": "delete-me",
		"new_string": deleteReplacement,
	})
	if editEmpty.IsError {
		t.Fatalf("explicit empty edit replacement should be allowed: %s", editEmpty.DecodeContent())
	}
	got, err := os.ReadFile(editPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "delete-me") {
		t.Fatalf("empty replacement did not delete target text: %q", string(got))
	}
}

func TestFilesystemMultiEditRequiresNewStringPresenceButAllowsEmptyReplacement(t *testing.T) {
	host := newTestHost(t)
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.txt")
	pathB := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(pathA, []byte("remove-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte("remove-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{pathA, pathB} {
		read := callFilesystem(t, host, map[string]any{"action": "read", "path": path})
		if read.IsError {
			t.Fatalf("read %s failed: %s", path, read.DecodeContent())
		}
	}

	missing := callFilesystem(t, host, map[string]any{
		"action": "multiedit",
		"edits": []map[string]any{
			{
				"path":       pathA,
				"old_string": "remove-a",
			},
		},
	})
	if !missing.IsError {
		t.Fatal("multiedit without new_string should fail")
	}
	if got := missing.DecodeContent(); !strings.Contains(got, "filesystem.multiedit") || !strings.Contains(got, "missing_field") || !strings.Contains(got, "edits[0].new_string") {
		t.Fatalf("multiedit missing new_string error mismatch: %q", got)
	}

	emptyReplacement := ""
	ok := callFilesystem(t, host, map[string]any{
		"action": "multiedit",
		"edits": []map[string]any{
			{
				"path":       pathA,
				"old_string": "remove-a",
				"new_string": emptyReplacement,
			},
			{
				"path":       pathB,
				"old_string": "remove-b",
				"new_string": "kept-b",
			},
		},
	})
	if ok.IsError {
		t.Fatalf("explicit empty multiedit replacement should be allowed: %s", ok.DecodeContent())
	}
	gotA, err := os.ReadFile(pathA)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := os.ReadFile(pathB)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotA) != "" || string(gotB) != "kept-b" {
		t.Fatalf("multiedit result mismatch: a=%q b=%q", string(gotA), string(gotB))
	}
}

func TestFileExecutorWriteExistingRequiresRead(t *testing.T) {
	tracker := NewReadTracker(5 * time.Minute)
	dir := t.TempDir()
	origAllowAll := allowAllPaths
	allowAllPaths = false
	t.Setenv("TOOLS_WORK_DIR", dir)
	t.Cleanup(func() { allowAllPaths = origAllowAll })

	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := executeWriteFile(context.Background(), writeFileInput{Path: path, Content: "new"}, tracker)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected existing write to require read")
	}
	if got := res.DecodeContent(); !strings.Contains(got, "has not been read") && !strings.Contains(got, "尚未读取") {
		t.Fatalf("expected read-before-write error, got %q", got)
	}
}

func TestFileExecutorEditRejectsExternalModification(t *testing.T) {
	tracker := NewReadTracker(5 * time.Minute)
	dir := t.TempDir()
	origAllowAll := allowAllPaths
	origFileTracker := globalFileTracker
	allowAllPaths = false
	globalFileTracker = NewFileTracker(0)
	t.Setenv("TOOLS_WORK_DIR", dir)
	t.Cleanup(func() {
		allowAllPaths = origAllowAll
		globalFileTracker = origFileTracker
	})

	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	tracker.RecordRead(path)
	if err := globalFileTracker.Track(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed elsewhere"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := executeEdit(context.Background(), editInput{Path: path, OldString: "changed", NewString: "updated"}, tracker, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected external modification rejection")
	}
	if got := res.DecodeContent(); !strings.Contains(got, "外部修改") {
		t.Fatalf("expected external modification error, got %q", got)
	}
}
