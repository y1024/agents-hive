package security

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestMatchPolicy(t *testing.T) {
	rules := []ExecRule{
		{Pattern: `^rm\s+-rf\s+`, Policy: PolicyAsk, Description: "递归删除需审批"},
		{Pattern: "^git\\s+push\\s+--force", Policy: PolicyAsk, Description: "强制推送需审批"},
		{Pattern: "^ls\\b", Policy: PolicyAllow, Description: "允许列目录"},
	}
	// 显式用 PolicyDeny 作为 default，覆盖"未匹配即拒绝"的 strict 语义。
	// 注：NewSafeExecutor 默认是 PolicyAllow（permission-minimalism），本测试用 strict 变体。
	executor := NewSafeExecutorWithDefault(rules, PolicyDeny, zap.NewNop())

	tests := []struct {
		name     string
		command  string
		expected ExecPolicy
	}{
		{"审批 rm -rf 根目录", "rm -rf /", PolicyAsk},
		{"审批 rm -rf 普通路径", "rm -rf ./build", PolicyAsk},
		{"审批 force push", "git push --force origin main", PolicyAsk},
		{"允许 ls", "ls -la", PolicyAllow},
		{"默认拒绝", "echo hello", PolicyDeny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := executor.MatchPolicy(tt.command)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExecuteAskRootDelete(t *testing.T) {
	rules := []ExecRule{
		{Pattern: `^rm\s+-rf\s+`, Policy: PolicyAsk},
	}
	executor := NewSafeExecutor(rules, zap.NewNop())

	_, err := executor.Execute(context.Background(), ExecRequest{Command: "rm -rf /"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "需要审批")
}

func TestExecuteAsk(t *testing.T) {
	rules := []ExecRule{
		{Pattern: `^rm\s+-rf\s+`, Policy: PolicyAsk},
	}
	executor := NewSafeExecutor(rules, zap.NewNop())

	_, err := executor.Execute(context.Background(), ExecRequest{Command: "rm -rf ./build"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "需要审批")
}

func TestExecuteAllow(t *testing.T) {
	// 显式 allow 规则（即便默认策略改动，本测试意图保持稳定）。
	rules := []ExecRule{
		{Pattern: "^echo\\b", Policy: PolicyAllow},
	}
	executor := NewSafeExecutor(rules, zap.NewNop())

	result, err := executor.Execute(context.Background(), ExecRequest{Command: "echo hello"})
	assert.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "hello")
}

func TestDefaultPolicyDeny(t *testing.T) {
	// 显式 PolicyDeny 作为 default：没有任何规则时，未匹配命令应进入审批。
	// NewSafeExecutor 默认 Allow（permission-minimalism），此处测 strict 变体。
	executor := NewSafeExecutorWithDefault(nil, PolicyDeny, zap.NewNop())

	_, err := executor.Execute(context.Background(), ExecRequest{Command: "echo hello"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "需要审批")
}
