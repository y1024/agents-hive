package skills

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/errs"
)

// realSandboxExecutor 测试用 SandboxExecutor，直接在宿主机执行命令。
type realSandboxExecutor struct {
	timeout time.Duration
}

func (e *realSandboxExecutor) Execute(ctx context.Context, req SandboxExecRequest) (SandboxExecResult, error) {
	timeout := req.Timeout
	if timeout == 0 {
		timeout = e.timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", req.Command)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			err = nil
		}
	}
	if err != nil {
		return SandboxExecResult{}, err
	}
	return SandboxExecResult{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: exitCode,
	}, nil
}

func (e *realSandboxExecutor) Close() error { return nil }

func newTestScriptRunner() *ScriptRunner {
	r := NewScriptRunner(10*time.Second, zap.NewNop())
	r.Executor = &realSandboxExecutor{timeout: 10 * time.Second}
	return r
}

func writeScript(t *testing.T, dir, name, content string) {
	t.Helper()
	scriptDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(scriptDir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDetectInterpreter(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  string
		want     string
		wantErr  bool
	}{
		{name: "sh extension", filename: "run.sh", want: "/bin/sh"},
		{name: "py extension", filename: "run.py", want: "python3"},
		{name: "js extension", filename: "run.js", want: "node"},
		{name: "ts extension", filename: "run.ts", want: "npx ts-node"},
		{name: "rb extension", filename: "run.rb", want: "ruby"},
		{name: "shebang fallback", filename: "run", content: "#!/usr/bin/env bash\necho hi", want: "/usr/bin/env bash"},
		{name: "no interpreter", filename: "run.xyz", content: "hello", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.filename)
			content := tt.content
			if content == "" {
				content = "echo hello"
			}
			if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
				t.Fatal(err)
			}

			got, err := detectInterpreter(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScriptRunner_RunScript(t *testing.T) {
	runner := newTestScriptRunner()
	dir := t.TempDir()

	writeScript(t, dir, "hello.sh", "#!/bin/sh\necho hello world")

	ctx := context.Background()
	output, err := runner.RunScript(ctx, dir, "hello.sh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "hello world" {
		t.Errorf("got %q, want %q", output, "hello world")
	}
}

func TestScriptRunner_RunScript_WithArgs(t *testing.T) {
	runner := newTestScriptRunner()
	dir := t.TempDir()

	writeScript(t, dir, "greet.sh", "#!/bin/sh\necho hello $1")

	ctx := context.Background()
	output, err := runner.RunScript(ctx, dir, "greet.sh", "world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "hello world" {
		t.Errorf("got %q, want %q", output, "hello world")
	}
}

func TestScriptRunner_RunScript_QuotesArguments(t *testing.T) {
	runner := newTestScriptRunner()
	dir := t.TempDir()

	writeScript(t, dir, "echo.sh", "#!/bin/sh\nprintf '%s' \"$1\"")

	ctx := context.Background()
	output, err := runner.RunScript(ctx, dir, "echo.sh", "hello world 'quoted'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != "hello world 'quoted'" {
		t.Errorf("got %q, want quoted argument preserved", output)
	}
}

func TestScriptRunner_RunScript_ArgumentCommandSubstitutionIsLiteral(t *testing.T) {
	runner := newTestScriptRunner()
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned")

	writeScript(t, dir, "echo.sh", "#!/bin/sh\nprintf '%s' \"$1\"")

	payload := "$(touch " + marker + ")"
	output, err := runner.RunScript(context.Background(), dir, "echo.sh", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output != payload {
		t.Fatalf("payload should be passed literally, got %q", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command substitution executed unexpectedly, stat err=%v", err)
	}
}

func TestScriptRunner_RunScript_ScriptNameCannotEscapeScriptsDir(t *testing.T) {
	runner := newTestScriptRunner()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "escape.sh"), []byte("#!/bin/sh\necho escaped"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := runner.RunScript(context.Background(), dir, "../escape.sh")
	if err == nil {
		t.Fatal("expected directory traversal error")
	}
	if !errs.IsCode(err, errs.CodeSkillScriptFailed) {
		t.Fatalf("expected CodeSkillScriptFailed, got %v", err)
	}
}

func TestScriptRunner_RunScript_SymlinkCannotEscapeScriptsDir(t *testing.T) {
	runner := newTestScriptRunner()
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside.sh")
	if err := os.WriteFile(outside, []byte("#!/bin/sh\necho escaped"), 0o755); err != nil {
		t.Fatal(err)
	}
	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(scriptsDir, "link.sh")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := runner.RunScript(context.Background(), dir, "link.sh")
	if err == nil {
		t.Fatal("expected symlink escape error")
	}
	if !errs.IsCode(err, errs.CodeSkillScriptFailed) {
		t.Fatalf("expected CodeSkillScriptFailed, got %v", err)
	}
}

func TestScriptRunner_RunScript_NotFound(t *testing.T) {
	runner := newTestScriptRunner()
	dir := t.TempDir()

	ctx := context.Background()
	_, err := runner.RunScript(ctx, dir, "missing.sh")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errs.IsCode(err, errs.CodeSkillScriptFailed) {
		t.Errorf("expected CodeSkillScriptFailed, got %v", err)
	}
}

func TestScriptRunner_RunScript_Failure(t *testing.T) {
	runner := newTestScriptRunner()
	dir := t.TempDir()

	writeScript(t, dir, "fail.sh", "#!/bin/sh\nexit 1")

	ctx := context.Background()
	_, err := runner.RunScript(ctx, dir, "fail.sh")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errs.IsCode(err, errs.CodeSkillScriptFailed) {
		t.Errorf("expected CodeSkillScriptFailed, got %v", err)
	}
}

func TestScriptRunner_RunAllScripts(t *testing.T) {
	runner := newTestScriptRunner()
	dir := t.TempDir()

	writeScript(t, dir, "one.sh", "#!/bin/sh\necho one")
	writeScript(t, dir, "two.sh", "#!/bin/sh\necho two")

	ctx := context.Background()
	results, err := runner.RunAllScripts(ctx, dir, []string{"one.sh", "two.sh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results["one.sh"] != "one" {
		t.Errorf("one.sh: got %q, want %q", results["one.sh"], "one")
	}
	if results["two.sh"] != "two" {
		t.Errorf("two.sh: got %q, want %q", results["two.sh"], "two")
	}
}

func TestScriptRunner_RunAllScripts_StopsOnError(t *testing.T) {
	runner := newTestScriptRunner()
	dir := t.TempDir()

	writeScript(t, dir, "ok.sh", "#!/bin/sh\necho ok")
	writeScript(t, dir, "fail.sh", "#!/bin/sh\nexit 1")
	writeScript(t, dir, "never.sh", "#!/bin/sh\necho never")

	ctx := context.Background()
	results, err := runner.RunAllScripts(ctx, dir, []string{"ok.sh", "fail.sh", "never.sh"})
	if err == nil {
		t.Fatal("expected error")
	}
	if results["ok.sh"] != "ok" {
		t.Errorf("ok.sh should have succeeded, got %q", results["ok.sh"])
	}
	if _, exists := results["never.sh"]; exists {
		t.Error("never.sh should not have been executed")
	}
}

func TestScriptRunner_Timeout(t *testing.T) {
	runner := NewScriptRunner(100*time.Millisecond, zap.NewNop())
	dir := t.TempDir()

	writeScript(t, dir, "slow.sh", "#!/bin/sh\nsleep 10")

	ctx := context.Background()
	_, err := runner.RunScript(ctx, dir, "slow.sh")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
