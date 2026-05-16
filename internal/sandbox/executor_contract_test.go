package sandbox

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// runExecutorContractTests 对任意 Executor 实现运行契约测试，
// 确保 LocalExecutor 和 DockerExecutor 对相同输入产生相同输出。
func runExecutorContractTests(t *testing.T, executor Executor) {
	t.Helper()

	t.Run("echo hello", func(t *testing.T) {
		result, err := executor.Execute(context.Background(), ExecRequest{
			Command: "echo hello",
			Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := trimOutput(result.Stdout)
		if got != "hello" {
			t.Errorf("stdout = %q, want %q", got, "hello")
		}
		if result.ExitCode != 0 {
			t.Errorf("exit code = %d, want 0", result.ExitCode)
		}
	})

	t.Run("exit code non-zero", func(t *testing.T) {
		result, err := executor.Execute(context.Background(), ExecRequest{
			Command: "exit 42",
			Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ExitCode != 42 {
			t.Errorf("exit code = %d, want 42", result.ExitCode)
		}
	})

	t.Run("timeout enforcement", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		result, err := executor.Execute(ctx, ExecRequest{
			Command: "sleep 30",
			Timeout: 500 * time.Millisecond,
		})
		// 超时应返回 error 或非零 exit code
		if err == nil && result.ExitCode == 0 {
			t.Error("expected timeout (error or non-zero exit code), got success")
		}
	})

	t.Run("workdir respected", func(t *testing.T) {
		tmpDir := strings.TrimRight(os.TempDir(), "/")
		result, err := executor.Execute(context.Background(), ExecRequest{
			Command: "pwd",
			WorkDir: tmpDir,
			Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := trimOutput(result.Stdout)
		// macOS 下 /tmp 可能是 /private/tmp 的符号链接
		if got != tmpDir && got != "/private"+tmpDir {
			t.Errorf("workdir = %q, want %q", got, tmpDir)
		}
	})

	t.Run("env injection", func(t *testing.T) {
		result, err := executor.Execute(context.Background(), ExecRequest{
			Command: "echo $TEST_SANDBOX_VAR",
			Env:     map[string]string{"TEST_SANDBOX_VAR": "sandbox_value"},
			Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := trimOutput(result.Stdout)
		if got != "sandbox_value" {
			t.Errorf("env var = %q, want %q", got, "sandbox_value")
		}
	})

	t.Run("empty command rejected", func(t *testing.T) {
		_, err := executor.Execute(context.Background(), ExecRequest{
			Command: "",
			Timeout: 10 * time.Second,
		})
		if err == nil {
			t.Error("expected error for empty command, got nil")
		}
	})
}

func trimOutput(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// TestLocalExecutorContract 对 LocalExecutor 运行契约测试。
func TestLocalExecutorContract(t *testing.T) {
	shell := &testShell{}
	executor := NewLocalExecutor(shell, nil)
	defer executor.Close()

	runExecutorContractTests(t, executor)
}

// testShell 是一个简单的 PersistentShellIface 实现，用于测试。
// 为每个命令启动一个新的 bash 进程。
type testShell struct{}

func (s *testShell) Execute(ctx context.Context, command string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", command)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			err = nil
		}
	}
	return
}

func (s *testShell) Close() error { return nil }

// TestSafeExecutorWrapper 测试安全策略装饰器。
func TestSafeExecutorWrapper(t *testing.T) {
	shell := &testShell{}
	inner := NewLocalExecutor(shell, nil)
	defer inner.Close()

	t.Run("nil checker passes through", func(t *testing.T) {
		w := NewSafeExecutorWrapper(inner, nil)
		result, err := w.Execute(context.Background(), ExecRequest{
			Command: "echo pass",
			Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if trimOutput(result.Stdout) != "pass" {
			t.Errorf("stdout = %q, want %q", result.Stdout, "pass")
		}
	})

	t.Run("deny policy passes through for upper-layer approval", func(t *testing.T) {
		w := NewSafeExecutorWrapper(inner, &mockChecker{policy: "deny"})
		result, err := w.Execute(context.Background(), ExecRequest{
			Command: "echo reviewed",
			Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatalf("deny policy should pass through to executor, got error: %v", err)
		}
		if trimOutput(result.Stdout) != "reviewed" {
			t.Errorf("stdout = %q, want %q", result.Stdout, "reviewed")
		}
	})

	t.Run("ask policy passes through (HITL handled by upper layer)", func(t *testing.T) {
		w := NewSafeExecutorWrapper(inner, &mockChecker{policy: "ask"})
		result, err := w.Execute(context.Background(), ExecRequest{
			Command: "echo ask",
			Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatalf("ask policy should pass through to executor, got error: %v", err)
		}
		if trimOutput(result.Stdout) != "ask" {
			t.Errorf("stdout = %q, want %q", result.Stdout, "ask")
		}
	})

	t.Run("allow policy passes", func(t *testing.T) {
		w := NewSafeExecutorWrapper(inner, &mockChecker{policy: "allow"})
		result, err := w.Execute(context.Background(), ExecRequest{
			Command: "echo allowed",
			Timeout: 10 * time.Second,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if trimOutput(result.Stdout) != "allowed" {
			t.Errorf("stdout = %q, want %q", result.Stdout, "allowed")
		}
	})
}

type mockChecker struct {
	policy string
}

func (m *mockChecker) MatchPolicy(_ string) string {
	return m.policy
}
