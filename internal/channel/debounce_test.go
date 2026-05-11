package channel

import (
	"sync"
	"testing"
	"time"

	"github.com/chef-guo/agents-hive/internal/imctx"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestMessageBatcher_SingleMessage(t *testing.T) {
	var mu sync.Mutex
	var flushed []InboundMessage
	b := newMessageBatcher(func(msg InboundMessage) {
		mu.Lock()
		flushed = append(flushed, msg)
		mu.Unlock()
	}, zap.NewNop())

	msg := InboundMessage{
		MessageID:  "msg-1",
		Platform:   PlatformDingTalk,
		ChatID:     "chat-001",
		SenderID:   "user-001",
		SenderName: "Alice",
		Content:    "hello",
	}

	// 有 SenderID 的消息被缓冲
	result := b.Add(msg)
	assert.True(t, result)

	// 窗口到期后 flush
	time.Sleep(debounceWindow + 200*time.Millisecond)
	mu.Lock()
	assert.Len(t, flushed, 1)
	assert.Equal(t, "hello", flushed[0].Content)
	mu.Unlock()
}

func TestMessageBatcher_MultipleMessages_SameSender(t *testing.T) {
	var mu sync.Mutex
	var flushed []InboundMessage
	b := newMessageBatcher(func(msg InboundMessage) {
		mu.Lock()
		flushed = append(flushed, msg)
		mu.Unlock()
	}, zap.NewNop())

	msg1 := InboundMessage{MessageID: "msg-1", Platform: PlatformDingTalk, ChatID: "chat-001", SenderID: "user-001", Content: "hello"}
	msg2 := InboundMessage{MessageID: "msg-2", Platform: PlatformDingTalk, ChatID: "chat-001", SenderID: "user-001", Content: "world"}

	assert.True(t, b.Add(msg1))
	assert.True(t, b.Add(msg2))

	// 窗口期内未 flush
	mu.Lock()
	assert.Len(t, flushed, 0)
	mu.Unlock()

	// 等待窗口到期
	time.Sleep(debounceWindow + 200*time.Millisecond)
	mu.Lock()
	assert.Len(t, flushed, 1)
	assert.Equal(t, "hello\nworld", flushed[0].Content)
	assert.Equal(t, "msg-2", flushed[0].MessageID) // 保留最后一条的 MessageID
	mu.Unlock()
}

func TestMessageBatcher_DifferentSenders(t *testing.T) {
	var mu sync.Mutex
	var flushed []InboundMessage
	b := newMessageBatcher(func(msg InboundMessage) {
		mu.Lock()
		flushed = append(flushed, msg)
		mu.Unlock()
	}, zap.NewNop())

	msg1 := InboundMessage{MessageID: "msg-1", Platform: PlatformDingTalk, ChatID: "chat-001", SenderID: "user-001", Content: "from alice"}
	msg2 := InboundMessage{MessageID: "msg-2", Platform: PlatformDingTalk, ChatID: "chat-001", SenderID: "user-002", Content: "from bob"}

	assert.True(t, b.Add(msg1))
	assert.True(t, b.Add(msg2))

	time.Sleep(debounceWindow + 200*time.Millisecond)
	mu.Lock()
	assert.Len(t, flushed, 2)
	mu.Unlock()
}

func TestMessageBatcher_DifferentChats(t *testing.T) {
	var mu sync.Mutex
	var flushed []InboundMessage
	b := newMessageBatcher(func(msg InboundMessage) {
		mu.Lock()
		flushed = append(flushed, msg)
		mu.Unlock()
	}, zap.NewNop())

	msg1 := InboundMessage{MessageID: "msg-1", Platform: PlatformDingTalk, ChatID: "chat-001", SenderID: "user-001", Content: "in chat1"}
	msg2 := InboundMessage{MessageID: "msg-2", Platform: PlatformDingTalk, ChatID: "chat-002", SenderID: "user-001", Content: "in chat2"}

	assert.True(t, b.Add(msg1))
	assert.True(t, b.Add(msg2))

	time.Sleep(debounceWindow + 200*time.Millisecond)
	mu.Lock()
	assert.Len(t, flushed, 2)
	mu.Unlock()
}

func TestMessageBatcher_DifferentOwners(t *testing.T) {
	var mu sync.Mutex
	var flushed []InboundMessage
	b := newMessageBatcher(func(msg InboundMessage) {
		mu.Lock()
		flushed = append(flushed, msg)
		mu.Unlock()
	}, zap.NewNop())

	base := InboundMessage{
		MessageID:   "msg-1",
		Platform:    PlatformWeChatBot,
		TenantKey:   "owner-1",
		OwnerUserID: "owner-1",
		ChatID:      "wx-peer",
		SenderID:    "wx-peer",
		Content:     "from owner 1",
	}
	other := base
	other.MessageID = "msg-2"
	other.TenantKey = "owner-2"
	other.OwnerUserID = "owner-2"
	other.Content = "from owner 2"

	assert.True(t, b.Add(base))
	assert.True(t, b.Add(other))

	time.Sleep(debounceWindow + 200*time.Millisecond)
	mu.Lock()
	assert.Len(t, flushed, 2)
	mu.Unlock()
}

func TestMessageBatcher_EmptySenderID(t *testing.T) {
	var flushed []InboundMessage
	b := newMessageBatcher(func(msg InboundMessage) {
		flushed = append(flushed, msg)
	}, zap.NewNop())

	msg := InboundMessage{MessageID: "msg-1", Platform: PlatformDingTalk, ChatID: "chat-001", SenderID: "", Content: "no sender"}

	// SenderID 为空，不做 debounce，返回 false
	result := b.Add(msg)
	assert.False(t, result)
	assert.Len(t, flushed, 0) // 调用方自行处理
}

func TestMessageBatcher_Stop(t *testing.T) {
	var mu sync.Mutex
	var flushed []InboundMessage
	b := newMessageBatcher(func(msg InboundMessage) {
		mu.Lock()
		flushed = append(flushed, msg)
		mu.Unlock()
	}, zap.NewNop())

	msg := InboundMessage{MessageID: "msg-1", Platform: PlatformDingTalk, ChatID: "chat-001", SenderID: "user-001", Content: "hello"}
	b.Add(msg)

	// Stop 停止所有计时器，消息不会 flush
	b.Stop()
	time.Sleep(debounceWindow + 200*time.Millisecond)
	mu.Lock()
	assert.Len(t, flushed, 0)
	mu.Unlock()
}

func TestMessageBatcher_ConcurrentAdd(t *testing.T) {
	var mu sync.Mutex
	var flushed []InboundMessage
	b := newMessageBatcher(func(msg InboundMessage) {
		mu.Lock()
		flushed = append(flushed, msg)
		mu.Unlock()
	}, zap.NewNop())

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			msg := InboundMessage{
				MessageID: "msg-" + string(rune('a'+id)),
				Platform:  PlatformDingTalk,
				ChatID:    "chat-001",
				SenderID:  "user-001",
				Content:   "msg",
			}
			b.Add(msg)
		}(i)
	}
	wg.Wait()

	// 窗口到期后应全部合并为一条
	time.Sleep(debounceWindow + 200*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, flushed, 1)
}

func TestMergeBatch(t *testing.T) {
	msgs := []InboundMessage{
		{MessageID: "msg-1", Content: "hello"},
		{MessageID: "msg-2", Content: "world"},
		{MessageID: "msg-3", Content: "!"},
	}
	merged := mergeBatch(msgs)
	assert.Equal(t, "msg-3", merged.MessageID)
	assert.Equal(t, "hello\nworld\n!", merged.Content)
}

func TestMergeBatchPreservesLastMessageScopeAndReplyToken(t *testing.T) {
	first := InboundMessage{
		MessageID:   "msg-1",
		Platform:    PlatformWeChatBot,
		TenantKey:   "owner-1",
		OwnerUserID: "owner-1",
		ChatType:    ChatDirect,
		ChatID:      "wx-peer",
		SenderID:    "wx-peer",
		SenderName:  "客户",
		Content:     "第一句",
		MessageType: "text",
		Attachments: []Attachment{{
			Type:     "file",
			Key:      "file-1",
			FileName: "first.txt",
		}},
		ReplyToken:   "ctx-1",
		NoDebounce:   false,
		References:   []imctx.DocRef{{Token: "doc-1", Type: imctx.RefDocx}},
		ParentID:     "parent-1",
		RootID:       "root-1",
		Mentions:     []imctx.Mention{{Name: "机器人", OpenID: "mention-1", IsBot: true}},
		BotMentioned: true,
		RawData:      []byte(`{"seq":1}`),
		Timestamp:    time.Unix(1, 0),
	}
	second := first
	second.MessageID = "msg-2"
	second.Content = "第二句"
	second.ReplyToken = "ctx-2"
	second.Attachments = []Attachment{{
		Type:     "file",
		Key:      "file-2",
		FileName: "second.txt",
	}}
	second.RawData = []byte(`{"seq":2}`)
	second.Timestamp = time.Unix(2, 0)

	merged := mergeBatch([]InboundMessage{first, second})

	assert.Equal(t, "msg-2", merged.MessageID)
	assert.Equal(t, "owner-1", merged.TenantKey)
	assert.Equal(t, "owner-1", merged.OwnerUserID)
	assert.Equal(t, "ctx-2", merged.ReplyToken)
	assert.Equal(t, "第一句\n第二句", merged.Content)
	assert.Equal(t, "text", merged.MessageType)
	assert.Equal(t, second.Attachments, merged.Attachments)
	assert.Equal(t, second.References, merged.References)
	assert.Equal(t, "parent-1", merged.ParentID)
	assert.Equal(t, "root-1", merged.RootID)
	assert.Equal(t, second.Mentions, merged.Mentions)
	assert.True(t, merged.BotMentioned)
	assert.JSONEq(t, `{"seq":2}`, string(merged.RawData))
	assert.Equal(t, second.Timestamp, merged.Timestamp)
}

func TestMergeBatch_SingleMessage(t *testing.T) {
	msg := InboundMessage{MessageID: "msg-1", Content: "hello"}
	merged := mergeBatch([]InboundMessage{msg})
	assert.Equal(t, "msg-1", merged.MessageID)
	assert.Equal(t, "hello", merged.Content)
}
