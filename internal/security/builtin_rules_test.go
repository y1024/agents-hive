package security

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestBuiltinRulesAlwaysPresent(t *testing.T) {
	// 即使不传任何用户规则，内置规则也必须生效
	executor := NewSafeExecutor(nil, zap.NewNop())

	tests := []struct {
		command  string
		expected ExecPolicy
	}{
		// PolicyAsk: 需要审批
		{"rm -rf /", PolicyAsk},
		{"rm -rf /*", PolicyAsk},
		{"mkfs /dev/sda1", PolicyAsk},
		{"dd if=/dev/zero of=/dev/sda", PolicyAsk},
		{"rm -rf ./build", PolicyAsk},
		{"rm -r ./node_modules", PolicyAsk},
		{"git push --force origin main", PolicyAsk},
		{"git push -f origin main", PolicyAsk},
		{"git reset --hard HEAD~1", PolicyAsk},
		{"kubectl delete pod my-pod", PolicyAsk},
		{"docker rm -f my-container", PolicyAsk},
		{"docker rmi -f my-image", PolicyAsk},
		{"chmod 777 /tmp/test", PolicyAsk},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			policy := executor.MatchPolicy(tt.command)
			assert.Equal(t, tt.expected, policy, "command: %s", tt.command)
		})
	}
}

func TestBuiltinRulesCannotBeOverridden(t *testing.T) {
	// 用户规则不能覆盖内置规则（内置规则优先匹配）
	userRules := []ExecRule{
		{Pattern: "rm -rf *", Policy: PolicyAllow, Description: "用户试图放行 rm -rf"},
	}
	executor := NewSafeExecutor(userRules, zap.NewNop())

	// rm -rf / 仍然命中内置审批规则（内置 PolicyAsk 在用户 PolicyAllow 之前）
	assert.Equal(t, PolicyAsk, executor.MatchPolicy("rm -rf /"))
	// rm -rf ./build 仍然需要审批（内置 PolicyAsk 在用户 PolicyAllow 之前）
	assert.Equal(t, PolicyAsk, executor.MatchPolicy("rm -rf ./build"))
}

func TestBuiltinRulesWithUserRulesAppended(t *testing.T) {
	userRules := []ExecRule{
		{Pattern: "echo *", Policy: PolicyAllow, Description: "允许 echo"},
		{Pattern: "curl *", Policy: PolicyAsk, Description: "curl 需审批"},
	}
	executor := NewSafeExecutor(userRules, zap.NewNop())

	// 用户规则正常工作
	assert.Equal(t, PolicyAllow, executor.MatchPolicy("echo hello"))
	assert.Equal(t, PolicyAsk, executor.MatchPolicy("curl https://example.com"))

	// 内置规则仍然生效
	assert.Equal(t, PolicyAsk, executor.MatchPolicy("rm -rf /"))
	assert.Equal(t, PolicyAsk, executor.MatchPolicy("git push --force origin main"))
}

func TestSQLDangerousCommands(t *testing.T) {
	executor := NewSafeExecutor(nil, zap.NewNop())

	tests := []struct {
		command  string
		expected ExecPolicy
	}{
		{"DROP TABLE users", PolicyAsk},
		{"DROP DATABASE production", PolicyAsk},
		{"TRUNCATE orders", PolicyAsk},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			policy := executor.MatchPolicy(tt.command)
			assert.Equal(t, tt.expected, policy)
		})
	}
}

func TestSafeCommandsStillDeniedByDefault(t *testing.T) {
	// strict 模式（默认策略显式 Deny）：未匹配内置规则的命令必须被拒绝。
	// 注：NewSafeExecutor 默认 Allow（permission-minimalism），此测试固定 strict 变体语义。
	executor := NewSafeExecutorWithDefault(nil, PolicyDeny, zap.NewNop())
	assert.Equal(t, PolicyDeny, executor.MatchPolicy("some-unknown-command"))
}
