package tools

import (
	"strings"
	"testing"
)

// TestTruncateOutput 测试输出截断功能
func TestTruncateOutput(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectTruncate bool
	}{
		{
			name:           "小输出不截断",
			input:          strings.Repeat("a", 1024), // 1KB
			expectTruncate: false,
		},
		{
			name:           "正好10MB不截断",
			input:          strings.Repeat("b", MaxToolOutputSize),
			expectTruncate: false,
		},
		{
			name:           "超过10MB应截断",
			input:          strings.Repeat("c", MaxToolOutputSize+1),
			expectTruncate: true,
		},
		{
			name:           "大输出应截断",
			input:          strings.Repeat("d", 20*1024*1024), // 20MB
			expectTruncate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateOutput(tt.input)

			if tt.expectTruncate {
				// 应该被截断
				if len(result) >= len(tt.input) {
					t.Errorf("输出应该被截断，但长度没有减少: 输入=%d, 输出=%d", len(tt.input), len(result))
				}

				// 应该包含截断消息
				if !strings.Contains(result, "输出过大，已截断") {
					t.Error("截断后的输出应包含截断消息")
				}

				// 截断后的大小应该合理（头部+尾部+消息）
				expectedSize := TruncateHeadSize + TruncateTailSize + 500 // 500字节用于截断消息
				if len(result) > expectedSize {
					t.Errorf("截断后的输出太大: 期望~%d, 实际=%d", expectedSize, len(result))
				}
			} else {
				// 不应该被截断
				if result != tt.input {
					t.Error("小输出不应该被修改")
				}
			}
		})
	}
}

// TestTruncateOutputContent 测试截断内容正确性
func TestTruncateOutputContent(t *testing.T) {
	// 创建一个大输出: 前面是 'A', 后面是 'Z'
	input := strings.Repeat("A", TruncateHeadSize) +
		strings.Repeat("M", 10*1024*1024) + // 中间10MB
		strings.Repeat("Z", TruncateTailSize)

	result := truncateOutput(input)

	// 检查头部保留
	if !strings.HasPrefix(result, strings.Repeat("A", 100)) {
		t.Error("截断后应保留头部内容")
	}

	// 检查尾部保留
	if !strings.HasSuffix(result, strings.Repeat("Z", 100)) {
		t.Error("截断后应保留尾部内容")
	}

	// 检查截断消息存在
	if !strings.Contains(result, "已截断") {
		t.Error("应包含截断消息")
	}
}

// TestSafeExecutorIntegration 测试安全执行器注入
func TestSafeExecutorIntegration(t *testing.T) {
	// 这个测试验证 SetSafeExecutor 函数存在并可以调用
	// 实际的安全检查在 security 包中测试

	// 模拟安全执行器
	mockExec := &mockSafeExecChecker{
		policy: "allow",
	}

	// 应该能够设置
	SetSafeExecutor(mockExec)

	// 验证全局变量已设置
	if globalSafeExec == nil {
		t.Error("SetSafeExecutor 应该设置 globalSafeExec")
	}

	// 验证策略返回
	if policy := globalSafeExec.MatchPolicy("test"); policy != "allow" {
		t.Errorf("期望 policy='allow', 实际='%s'", policy)
	}
}

// mockSafeExecChecker 模拟安全执行检查器
type mockSafeExecChecker struct {
	policy string
}

func (m *mockSafeExecChecker) MatchPolicy(command string) string {
	return m.policy
}
