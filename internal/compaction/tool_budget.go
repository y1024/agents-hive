package compaction

import (
	"context"
	"strconv"
	"strings"

	"github.com/chef-guo/agents-hive/internal/llm"
)

// ToolResultTrimEvent 描述一次工具结果裁剪，供上层同步清理依赖该结果的运行态状态。
type ToolResultTrimEvent struct {
	ToolCallID string
	ToolName   string
	Arguments  []byte
}

// ToolResultTrimObserver 接收工具结果裁剪事件。
type ToolResultTrimObserver interface {
	OnToolResultTrimmed(event ToolResultTrimEvent)
}

// ToolResultBudgetCompactor 对旧工具输出施加 token/字节 budget，
// 保护最近 N 轮对话不被裁剪，超阈值的旧工具输出截断为占位符。
type ToolResultBudgetCompactor struct {
	// ProtectedTurns 保护的最近用户轮数（默认 2）
	ProtectedTurns int
	// OutputThreshold 单条工具输出超过此字节数时裁剪（默认 20KB）
	OutputThreshold int
	// ContextBudget 累积保护上下文总量（字节），超出后强制裁剪（默认 40KB）
	ContextBudget int
	// Observer 可选；工具输出被裁剪时同步通知上层清理相关运行态状态。
	Observer ToolResultTrimObserver
}

func (c *ToolResultBudgetCompactor) Name() string { return "tool_budget" }

func (c *ToolResultBudgetCompactor) Compact(_ context.Context, messages []llm.MessageWithTools, _ int) ([]llm.MessageWithTools, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	protectedTurns := c.ProtectedTurns
	if protectedTurns <= 0 {
		protectedTurns = 2
	}
	threshold := c.OutputThreshold
	if threshold <= 0 {
		threshold = 20 * 1024
	}
	budgetBytes := c.ContextBudget
	if budgetBytes <= 0 {
		budgetBytes = 40 * 1024
	}

	// 复制消息列表避免修改原始数据
	result := make([]llm.MessageWithTools, len(messages))
	copy(result, messages)

	// 找到保护区域的起始位置
	protectedStart := findProtectedStart(messages, protectedTurns)

	// 从前往后扫描非保护区域的工具消息
	cumulativeSize := 0
	for i := 0; i < protectedStart; i++ {
		if result[i].Role != "tool" {
			continue
		}

		contentSize := len(result[i].Content.Text())
		cumulativeSize += contentSize

		if contentSize > threshold || cumulativeSize > budgetBytes {
			c.notifyTrimmed(messages, i)
			sizeKB := float64(contentSize) / 1024.0
			result[i].Content = llm.NewTextContent(
				"[输出已裁剪，原始大小: " + strconv.FormatFloat(sizeKB, 'f', 1, 64) + " KB]",
			)
		}
	}

	return result, nil
}

func (c *ToolResultBudgetCompactor) notifyTrimmed(messages []llm.MessageWithTools, index int) {
	if c == nil || c.Observer == nil || index < 0 || index >= len(messages) {
		return
	}
	msg := messages[index]
	if msg.Role != "tool" || strings.TrimSpace(msg.ToolCallID) == "" {
		return
	}
	event := ToolResultTrimEvent{
		ToolCallID: msg.ToolCallID,
		ToolName:   strings.TrimSpace(msg.ToolName),
	}
	foundName, foundArgs := findToolCallByID(messages[:index], msg.ToolCallID)
	if event.ToolName == "" {
		event.ToolName = foundName
	}
	event.Arguments = foundArgs
	c.Observer.OnToolResultTrimmed(event)
}

func findToolCallByID(messages []llm.MessageWithTools, id string) (string, []byte) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		for _, call := range messages[i].ToolCalls {
			if strings.TrimSpace(call.ID) != id {
				continue
			}
			args := append([]byte(nil), call.Arguments...)
			return strings.TrimSpace(call.Name), args
		}
	}
	return "", nil
}

// findProtectedStart 计算保护区域的起始索引。
// 从消息末尾往前数 turns 轮用户消息，返回最后一轮保护区域的起始位置。
func findProtectedStart(messages []llm.MessageWithTools, turns int) int {
	userCount := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			userCount++
			if userCount >= turns {
				return i
			}
		}
	}
	return 0
}
