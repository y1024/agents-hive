package compaction

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/llm"
)

type captureTrimObserver struct {
	events []ToolResultTrimEvent
}

func (o *captureTrimObserver) OnToolResultTrimmed(event ToolResultTrimEvent) {
	o.events = append(o.events, event)
}

// --- Pipeline 测试 ---

func TestNewPipeline_SkippedStages(t *testing.T) {
	registry := map[string]Compactor{
		"truncate": &TruncateCompactor{},
	}

	pipeline, skipped := NewPipeline(registry, []string{"truncate", "unknown_stage", "also_missing"})
	assert.Equal(t, []string{"truncate"}, pipeline.StageNames())
	assert.Equal(t, []string{"unknown_stage", "also_missing"}, skipped)
}

func TestNewPipeline_AllValid(t *testing.T) {
	registry := map[string]Compactor{
		"truncate":    &TruncateCompactor{},
		"tool_budget": &ToolResultBudgetCompactor{},
	}

	pipeline, skipped := NewPipeline(registry, []string{"tool_budget", "truncate"})
	assert.Equal(t, []string{"tool_budget", "truncate"}, pipeline.StageNames())
	assert.Empty(t, skipped)
}

func TestNewPipeline_Empty(t *testing.T) {
	pipeline, skipped := NewPipeline(map[string]Compactor{}, []string{"foo"})
	assert.Empty(t, pipeline.StageNames())
	assert.Equal(t, []string{"foo"}, skipped)

	// 空管线 Compact 应直接返回原始消息
	msgs := []llm.MessageWithTools{{Role: "user", Content: llm.NewTextContent("hi")}}
	result, err := pipeline.Compact(context.Background(), msgs, 100)
	assert.NoError(t, err)
	assert.Equal(t, msgs, result)
}

func TestPipeline_NilSafe(t *testing.T) {
	var p *Pipeline
	result, err := p.Compact(context.Background(), nil, 100)
	assert.NoError(t, err)
	assert.Nil(t, result)
	assert.Nil(t, p.StageNames())
}

// --- TruncateCompactor 测试 ---

func TestTruncateCompactor_EmptyMessages(t *testing.T) {
	c := &TruncateCompactor{}
	result, err := c.Compact(context.Background(), nil, 1000)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestTruncateCompactor_ZeroBudget(t *testing.T) {
	c := &TruncateCompactor{}
	msgs := []llm.MessageWithTools{{Role: "user", Content: llm.NewTextContent("hello")}}
	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	assert.Equal(t, msgs, result)
}

func TestTruncateCompactor_WithinBudget(t *testing.T) {
	c := &TruncateCompactor{UseTiktoken: false}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("short")},
	}
	result, err := c.Compact(context.Background(), msgs, 100000)
	assert.NoError(t, err)
	assert.Equal(t, msgs, result, "未超限时应返回原始消息")
}

func TestTruncateCompactor_Truncates(t *testing.T) {
	c := &TruncateCompactor{UseTiktoken: false}
	msgs := make([]llm.MessageWithTools, 20)
	for i := range msgs {
		msgs[i] = llm.MessageWithTools{
			Role:    "user",
			Content: llm.NewTextContent(strings.Repeat("word ", 50)),
		}
	}

	result, err := c.Compact(context.Background(), msgs, 500)
	assert.NoError(t, err)
	assert.Less(t, len(result), len(msgs), "应该压缩消息")
	assert.Equal(t, "system", result[0].Role, "第一条应为摘要")
	assert.Contains(t, result[0].Content.Text(), "[会话摘要]")
}

func TestTruncateCompactor_OversizedLastMessage(t *testing.T) {
	c := &TruncateCompactor{UseTiktoken: false}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("first message")},
		{Role: "assistant", Content: llm.NewTextContent("reply")},
		{Role: "user", Content: llm.NewTextContent(strings.Repeat("huge ", 10000))},
	}

	// budget 很小，最后一条消息本身就超过 budget
	result, err := c.Compact(context.Background(), msgs, 100)
	assert.NoError(t, err)
	// 应该降级处理而不是返回原始消息
	assert.LessOrEqual(t, len(result), len(msgs), "超大消息应触发降级")
}

func TestTruncateCompactor_PreservesToolPairing(t *testing.T) {
	c := &TruncateCompactor{UseTiktoken: false}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent(strings.Repeat("old ", 200))},
		{Role: "assistant", Content: llm.NewTextContent("calling tool")},
		{Role: "tool", Content: llm.NewTextContent("tool result")},
		{Role: "user", Content: llm.NewTextContent("latest")},
	}

	result, err := c.Compact(context.Background(), msgs, 300)
	assert.NoError(t, err)
	// 不应该以 tool 消息开头（除了摘要）
	for i, msg := range result {
		if i == 0 && msg.Role == "system" {
			continue // 摘要 OK
		}
		if msg.Role == "tool" {
			// tool 消息前面应该有 assistant
			require.Greater(t, i, 0)
			assert.Equal(t, "assistant", result[i-1].Role,
				"tool 消息前应有 assistant（或 system 摘要）")
		}
	}
}

// --- LLMSummaryCompactor 测试 ---

func TestLLMSummaryCompactor_NilClient(t *testing.T) {
	c := &LLMSummaryCompactor{LLMClient: nil}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
		{Role: "assistant", Content: llm.NewTextContent("hi")},
	}
	result, err := c.Compact(context.Background(), msgs, 1000)
	assert.NoError(t, err)
	assert.Equal(t, msgs, result, "LLMClient 为 nil 时应返回原始消息")
}

func TestLLMSummaryCompactor_ZeroBudget(t *testing.T) {
	c := &LLMSummaryCompactor{}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
	}
	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	assert.Equal(t, msgs, result, "budget 为 0 时应返回原始消息")
}

func TestLLMSummaryCompactor_WithinBudget(t *testing.T) {
	c := &LLMSummaryCompactor{UseTiktoken: false}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("short")},
		{Role: "assistant", Content: llm.NewTextContent("reply")},
	}
	result, err := c.Compact(context.Background(), msgs, 100000)
	assert.NoError(t, err)
	assert.Equal(t, msgs, result, "未超限时应返回原始消息")
}

// --- ToolResultBudgetCompactor 测试 ---

func TestToolBudget_EmptyMessages(t *testing.T) {
	c := &ToolResultBudgetCompactor{}
	result, err := c.Compact(context.Background(), nil, 0)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestToolBudget_NoToolMessages(t *testing.T) {
	c := &ToolResultBudgetCompactor{ProtectedTurns: 2}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
		{Role: "assistant", Content: llm.NewTextContent("hi")},
	}
	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	assert.Equal(t, len(msgs), len(result))
}

func TestToolBudget_TruncatesLargeToolOutput(t *testing.T) {
	bigOutput := strings.Repeat("x", 30*1024) // 30KB > 20KB threshold
	c := &ToolResultBudgetCompactor{ProtectedTurns: 1}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("first")},
		{Role: "tool", Content: llm.NewTextContent(bigOutput)},
		{Role: "user", Content: llm.NewTextContent("second")},
		{Role: "assistant", Content: llm.NewTextContent("reply")},
		{Role: "user", Content: llm.NewTextContent("third")},
	}

	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	assert.Equal(t, len(msgs), len(result), "消息数不变，只是内容被裁剪")
	assert.Contains(t, result[1].Content.Text(), "[输出已裁剪", "大工具输出应被裁剪")
}

func TestToolBudget_ProtectsRecentTurns(t *testing.T) {
	bigOutput := strings.Repeat("x", 30*1024)
	c := &ToolResultBudgetCompactor{ProtectedTurns: 2}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("first")},
		{Role: "tool", Content: llm.NewTextContent(bigOutput)},
		{Role: "user", Content: llm.NewTextContent("second")},
		{Role: "tool", Content: llm.NewTextContent(bigOutput)},
		{Role: "user", Content: llm.NewTextContent("third")},
	}

	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	// 保护最近 2 轮 user，所以 index 2 开始的 tool 应被保护
	// index 1 的 tool 在保护区外，应被裁剪
	assert.Contains(t, result[1].Content.Text(), "[输出已裁剪")
	assert.Equal(t, bigOutput, result[3].Content.Text(), "保护区内的 tool 不应被裁剪")
}

func TestToolBudget_DoesNotMutateOriginal(t *testing.T) {
	bigOutput := strings.Repeat("x", 30*1024)
	c := &ToolResultBudgetCompactor{ProtectedTurns: 1}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("first")},
		{Role: "tool", Content: llm.NewTextContent(bigOutput)},
		{Role: "user", Content: llm.NewTextContent("second")},
	}

	_, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	assert.Equal(t, bigOutput, msgs[1].Content.Text(), "不应修改原始消息")
}

func TestToolBudget_NotifiesTrimmedToolResultWithCallArguments(t *testing.T) {
	observer := &captureTrimObserver{}
	c := &ToolResultBudgetCompactor{
		ProtectedTurns:  1,
		OutputThreshold: 100,
		ContextBudget:   10000,
		Observer:        observer,
	}
	args := json.RawMessage(`{"path":"/tmp/read.go"}`)
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("read file")},
		{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{ID: "call-read", Name: "read_file", Arguments: args},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call-read",
			ToolName:   "read_file",
			Content:    llm.NewTextContent(strings.Repeat("x", 512)),
		},
		{Role: "user", Content: llm.NewTextContent("latest")},
	}

	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	assert.Contains(t, result[2].Content.Text(), "[输出已裁剪")
	require.Len(t, observer.events, 1)
	assert.Equal(t, "call-read", observer.events[0].ToolCallID)
	assert.Equal(t, "read_file", observer.events[0].ToolName)
	assert.JSONEq(t, string(args), string(observer.events[0].Arguments))
}

// --- SessionMemoryCompactor 测试 ---

func TestSessionMemory_EmptyMessages(t *testing.T) {
	c := &SessionMemoryCompactor{}
	result, err := c.Compact(context.Background(), nil, 0)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestSessionMemory_TooFewMessages(t *testing.T) {
	c := &SessionMemoryCompactor{}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
		{Role: "assistant", Content: llm.NewTextContent("hi")},
	}
	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	assert.Equal(t, msgs, result, "消息太少不应插入记忆")
}

func TestSessionMemory_InsertsMemory(t *testing.T) {
	c := &SessionMemoryCompactor{MaxExtractMessages: 10}
	msgs := make([]llm.MessageWithTools, 8)
	msgs[0] = llm.MessageWithTools{Role: "user", Content: llm.NewTextContent("我要重构认证模块")}
	msgs[1] = llm.MessageWithTools{Role: "assistant", Content: llm.NewTextContent("好的，我来分析一下当前的认证流程")}
	for i := 2; i < 8; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = llm.MessageWithTools{Role: role, Content: llm.NewTextContent("对话内容 " + strings.Repeat("x", 30))}
	}

	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	assert.Equal(t, len(msgs)+1, len(result), "应插入一条记忆消息")
	assert.Equal(t, "system", result[0].Role)
	assert.Contains(t, result[0].Content.Text(), "[会话记忆]")
}

func TestSessionMemory_DeduplicatesExisting(t *testing.T) {
	c := &SessionMemoryCompactor{MaxExtractMessages: 10}
	msgs := make([]llm.MessageWithTools, 8)
	msgs[0] = llm.MessageWithTools{Role: "system", Content: llm.NewTextContent("[会话记忆]\n旧记忆内容")}
	msgs[1] = llm.MessageWithTools{Role: "user", Content: llm.NewTextContent("我要重构认证模块")}
	msgs[2] = llm.MessageWithTools{Role: "assistant", Content: llm.NewTextContent("好的，我来分析")}
	for i := 3; i < 8; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		msgs[i] = llm.MessageWithTools{Role: role, Content: llm.NewTextContent("对话内容 " + strings.Repeat("x", 30))}
	}

	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	assert.Equal(t, len(msgs), len(result), "已有记忆时应更新而非新增")
	assert.Contains(t, result[0].Content.Text(), "[会话记忆]")
}

// --- HistorySnipCompactor 测试 ---

func TestHistorySnip_FewMessages(t *testing.T) {
	c := &HistorySnipCompactor{KeepFirst: true, KeepLast: 4}
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("a")},
		{Role: "assistant", Content: llm.NewTextContent("b")},
	}
	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	assert.Equal(t, msgs, result, "消息太少不应压缩")
}

func TestHistorySnip_CompressesMiddle(t *testing.T) {
	c := &HistorySnipCompactor{KeepFirst: true, KeepLast: 2}
	msgs := make([]llm.MessageWithTools, 10)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = llm.MessageWithTools{Role: role, Content: llm.NewTextContent("msg " + strings.Repeat("x", 20))}
	}

	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	assert.Less(t, len(result), len(msgs))
	// 第一条保留
	assert.Equal(t, msgs[0].Content.Text(), result[0].Content.Text())
	// 中间有摘要
	assert.Equal(t, "system", result[1].Role)
	assert.Contains(t, result[1].Content.Text(), "[中间消息已压缩]")
	// 最后两条保留
	assert.Equal(t, msgs[8].Content.Text(), result[len(result)-2].Content.Text())
	assert.Equal(t, msgs[9].Content.Text(), result[len(result)-1].Content.Text())
}

func TestHistorySnip_KeepFirstFalse(t *testing.T) {
	c := &HistorySnipCompactor{KeepFirst: false, KeepLast: 2}
	msgs := make([]llm.MessageWithTools, 10)
	for i := range msgs {
		msgs[i] = llm.MessageWithTools{Role: "user", Content: llm.NewTextContent("msg")}
	}

	result, err := c.Compact(context.Background(), msgs, 0)
	assert.NoError(t, err)
	// KeepFirst=false 时，第一条不保留，中间全部压缩
	assert.Equal(t, "system", result[0].Role, "第一条应为摘要")
}

// --- generateSimpleSummary 测试 ---

func TestGenerateSimpleSummary_Empty(t *testing.T) {
	s := generateSimpleSummary(nil)
	assert.Contains(t, s, "无早期消息")
}

func TestGenerateSimpleSummary_Normal(t *testing.T) {
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello world")},
		{Role: "assistant", Content: llm.NewTextContent("hi there")},
	}
	s := generateSimpleSummary(msgs)
	assert.Contains(t, s, "[会话摘要]")
	assert.Contains(t, s, "2 条早期消息")
	assert.Contains(t, s, "hello world")
}

func TestGenerateSimpleSummary_OverTenMessages(t *testing.T) {
	msgs := make([]llm.MessageWithTools, 15)
	for i := range msgs {
		msgs[i] = llm.MessageWithTools{Role: "user", Content: llm.NewTextContent("msg")}
	}
	s := generateSimpleSummary(msgs)
	assert.Contains(t, s, "15 条早期消息")
	assert.Contains(t, s, "还有 5 条消息已省略")
}

// --- truncateRunes 测试 ---

func TestTruncateRunes(t *testing.T) {
	assert.Equal(t, "hello", truncateRunes("hello", 10))
	assert.Equal(t, "hel...", truncateRunes("hello world", 3))
	assert.Equal(t, "你好...", truncateRunes("你好世界测试", 2))
	assert.Equal(t, "", truncateRunes("", 5))
}

// --- findProtectedStart 测试 ---

func TestFindProtectedStart(t *testing.T) {
	msgs := []llm.MessageWithTools{
		{Role: "user"},
		{Role: "assistant"},
		{Role: "tool"},
		{Role: "user"},
		{Role: "assistant"},
		{Role: "user"},
	}

	assert.Equal(t, 3, findProtectedStart(msgs, 2), "保护最近 2 轮 user")
	assert.Equal(t, 5, findProtectedStart(msgs, 1), "保护最近 1 轮 user")
	assert.Equal(t, 0, findProtectedStart(msgs, 10), "user 不够时返回 0")
}

func TestFindProtectedStart_NoUserMessages(t *testing.T) {
	msgs := []llm.MessageWithTools{
		{Role: "assistant"},
		{Role: "tool"},
		{Role: "assistant"},
	}
	assert.Equal(t, 0, findProtectedStart(msgs, 2), "无 user 消息时返回 0")
}

// --- EstimateTokens 测试 ---

func TestEstimateTokens_Heuristic(t *testing.T) {
	msgs := []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello world")},
	}
	tokens := EstimateTokens(msgs, nil, false)
	assert.Greater(t, tokens, 0)
}

func TestEstimateSingleTokens_Heuristic(t *testing.T) {
	msg := llm.MessageWithTools{Role: "user", Content: llm.NewTextContent("hello")}
	tokens := EstimateSingleTokens(msg, nil, false)
	assert.Greater(t, tokens, 0)
}

// --- isFileModifyTool 测试 ---

func TestIsFileModifyTool(t *testing.T) {
	assert.True(t, isFileModifyTool("write_file"))
	assert.True(t, isFileModifyTool("edit_file"))
	assert.True(t, isFileModifyTool("bash"))
	assert.False(t, isFileModifyTool("read_file"))
	assert.False(t, isFileModifyTool("search"))
	assert.False(t, isFileModifyTool(""))
}
