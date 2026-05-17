package master

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestSessionPersistToolCallsRoundTrip 验证会话持久化 ToolCalls/ToolCallID 的完整 round-trip
func TestSessionPersistToolCallsRoundTrip(t *testing.T) {
	logger := zaptest.NewLogger(t)
	ctx := context.Background()

	st := store.NewMemoryStore()

	// 构造带 ToolCalls 和 ToolCallID 的消息
	toolCalls := []llm.ToolCall{
		{ID: "call_abc123", Name: "read_file", Arguments: json.RawMessage(`{"path":"/tmp/test.go"}`)},
		{ID: "call_def456", Name: "grep", Arguments: json.RawMessage(`{"pattern":"TODO"}`)},
	}

	sessionID := "test-session-001"
	// 创建 SessionManager 并初始化会话
	sm := NewSessionManager(make(chan struct{}), logger)
	session := &SessionState{
		ID:           sessionID,
		Name:         "测试会话",
		Messages:     []llm.MessageWithTools{},
		Metadata:     make(map[string]any),
		Created:      time.Now(),
		LastAccessed: time.Now(),
	}
	sm.SetSession(session)
	sm.SetActiveSessionID(sessionID)

	// 先在 store 中创建会话记录
	err := st.CreateSession(ctx, &store.SessionRecord{
		ID:             sessionID,
		Name:           "测试会话",
		CreatedAt:      time.Now().Format(time.RFC3339),
		UpdatedAt:      time.Now().Format(time.RFC3339),
		LastAccessedAt: time.Now().Format(time.RFC3339),
	})
	require.NoError(t, err)

	// 添加消息：user → assistant(with ToolCalls) → tool(with ToolCallID) → tool(with ToolCallID)
	msgs := []llm.MessageWithTools{
		{
			Role:    "user",
			Content: llm.NewTextContent("请读取文件并搜索 TODO"),
		},
		{
			Role:      "assistant",
			Content:   llm.NewTextContent("好的，我来执行这两个操作"),
			ToolCalls: toolCalls,
		},
		{
			Role:       "tool",
			ToolCallID: "call_abc123",
			Content:    llm.NewTextContent("文件内容：package main..."),
		},
		{
			Role:       "tool",
			ToolCallID: "call_def456",
			Content:    llm.NewTextContent("找到 3 个 TODO"),
		},
	}
	session.Messages = msgs

	// 通过 buildMessageMeta + AddMessage 增量写入（模拟 appendSessionMessage 的行为）
	for _, msg := range msgs {
		meta := buildMessageMeta(msg)
		err = st.AddMessage(ctx, sessionID, msg.Role, msg.Content.Text(), meta)
		require.NoError(t, err)
	}

	// 保存会话元数据
	err = sm.SaveSession(ctx, st, session)
	require.NoError(t, err)

	// 创建新的 SessionManager 模拟重启
	sm2 := NewSessionManager(make(chan struct{}), logger)
	err = sm2.LoadLastActiveSession(ctx, st)
	require.NoError(t, err)

	// 获取恢复后的会话
	restored := sm2.GetSession(sessionID)
	require.NotNil(t, restored)
	require.Len(t, restored.Messages, 4)

	// 验证 user 消息
	assert.Equal(t, "user", restored.Messages[0].Role)
	assert.Equal(t, "请读取文件并搜索 TODO", restored.Messages[0].Content.Text())

	// 验证 assistant 消息保留了 ToolCalls
	assistantMsg := restored.Messages[1]
	assert.Equal(t, "assistant", assistantMsg.Role)
	require.Len(t, assistantMsg.ToolCalls, 2, "ToolCalls 应该被完整恢复")
	assert.Equal(t, "call_abc123", assistantMsg.ToolCalls[0].ID)
	assert.Equal(t, "read_file", assistantMsg.ToolCalls[0].Name)
	assert.JSONEq(t, `{"path":"/tmp/test.go"}`, string(assistantMsg.ToolCalls[0].Arguments))
	assert.Equal(t, "call_def456", assistantMsg.ToolCalls[1].ID)
	assert.Equal(t, "grep", assistantMsg.ToolCalls[1].Name)

	// 验证 tool 消息保留了 ToolCallID
	tool1 := restored.Messages[2]
	assert.Equal(t, "tool", tool1.Role)
	assert.Equal(t, "call_abc123", tool1.ToolCallID, "ToolCallID 应该被完整恢复")

	tool2 := restored.Messages[3]
	assert.Equal(t, "tool", tool2.Role)
	assert.Equal(t, "call_def456", tool2.ToolCallID, "ToolCallID 应该被完整恢复")
}

func TestAttachmentMetadataDoesNotPersistBase64ContentParts(t *testing.T) {
	data := "base64-image-data"
	msg := llm.MessageWithTools{
		Role: "user",
		Content: llm.NewMultiContent(
			llm.TextPart("看这张图"),
			llm.ImageBase64Part("image/png", data),
		),
		Metadata: AttachmentMetadataForTest([]FileAttachment{{
			Filename:    "chart.png",
			MimeType:    "image/png",
			Data:        data,
			Size:        12,
			AssetURI:    "asset://chat/user/u1/session/s1/hash.png",
			ContentHash: "hash",
		}}),
	}

	meta := buildMessageMeta(msg)
	require.NotNil(t, meta)
	assert.NotContains(t, meta, "content_parts")
	raw, ok := meta["attachments"].(string)
	require.True(t, ok)
	assert.Contains(t, raw, "asset://chat/user/u1/session/s1/hash.png")
	assert.NotContains(t, raw, data)
}
