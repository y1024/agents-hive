package channel

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/imctx"
	"github.com/chef-guo/agents-hive/internal/master"
)

// mockEventBus 是 EventBusSubscriber 的线程安全 mock：
// 记录 Subscribe / Unsubscribe 次数，向订阅者提供一个可由测试直接 push 事件的 channel。
type mockEventBus struct {
	mu         sync.Mutex
	subCount   atomic.Int32
	unsubCount atomic.Int32
	broadcasts []master.BroadcastMessage
	lastSubID  uint64
	chans      map[uint64]chan master.BroadcastMessage
}

func newMockEventBus() *mockEventBus {
	return &mockEventBus{chans: make(map[uint64]chan master.BroadcastMessage)}
}

func (m *mockEventBus) SubscribeWSBroadcast() (uint64, chan master.BroadcastMessage) {
	m.subCount.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSubID++
	id := m.lastSubID
	ch := make(chan master.BroadcastMessage, 16)
	m.chans[id] = ch
	return id, ch
}

func (m *mockEventBus) UnsubscribeWSBroadcast(subID uint64) {
	m.unsubCount.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.chans[subID]; ok {
		close(ch)
		delete(m.chans, subID)
	}
}

func (m *mockEventBus) BroadcastSessionMessage(sessionID string, msg master.BroadcastMessage) {
	msg.SessionID = sessionID
	m.mu.Lock()
	m.broadcasts = append(m.broadcasts, msg)
	for _, ch := range m.chans {
		select {
		case ch <- msg:
		default:
		}
	}
	m.mu.Unlock()
}

func (m *mockEventBus) snapshotBroadcasts() []master.BroadcastMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]master.BroadcastMessage, len(m.broadcasts))
	copy(out, m.broadcasts)
	return out
}

func (m *mockEventBus) pushToAll(ev master.BroadcastMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.chans {
		select {
		case ch <- ev:
		default:
		}
	}
}

// mockRendererPlugin 同时实现 ChannelPlugin + EventRenderer。
// RenderEventStream 把收到的事件记在 gotEvents 里，直到 ctx cancel 或 eventCh close。
// 可通过 returnErr 字段注入兜底测试用的 *RendererError。
type mockRendererPlugin struct {
	mockPlugin
	mu         sync.Mutex
	gotEvents  []master.BroadcastMessage
	gotScope   SessionScope
	renderDone chan struct{}
	// 测试注入：renderer 返回该 error（nil 表示正常收敛）
	returnErr error
	// 测试观测：renderer 被调用时置为 true
	called atomic.Bool
}

func newMockRendererPlugin(platform Platform) *mockRendererPlugin {
	return &mockRendererPlugin{
		mockPlugin: mockPlugin{platform: platform},
		renderDone: make(chan struct{}),
	}
}

func (p *mockRendererPlugin) RenderEventStream(ctx context.Context, scope SessionScope, eventCh <-chan master.BroadcastMessage) error {
	p.called.Store(true)
	p.mu.Lock()
	p.gotScope = scope
	p.mu.Unlock()
	defer close(p.renderDone)
	for {
		select {
		case <-ctx.Done():
			return p.returnErr
		case ev, ok := <-eventCh:
			if !ok {
				// eventCh 被 Router 通过 Unsubscribe 关闭 → 正常收敛
				return p.returnErr
			}
			// Session filter 契约：subscriber 端必须按 scope.SessionID 过滤。
			// 这里透传 master 的 SessionID 语义：非空 SessionID 且不等于 scope 时跳过。
			if ev.SessionID != "" && ev.SessionID != scope.SessionID {
				continue
			}
			p.mu.Lock()
			p.gotEvents = append(p.gotEvents, ev)
			p.mu.Unlock()
		}
	}
}

func (p *mockRendererPlugin) snapshotEvents() []master.BroadcastMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]master.BroadcastMessage, len(p.gotEvents))
	copy(out, p.gotEvents)
	return out
}

func (p *mockRendererPlugin) snapshotScope() SessionScope {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gotScope
}

// --- Tests ---

// TestRouter_RendererPath_SubscribesAndUnsubscribes: plugin 实现 EventRenderer 且开关打开时，
// Router 必须调 Subscribe 恰 1 次，并在 ProcessMessage 返回后调 Unsubscribe 恰 1 次。
func TestRouter_RendererPath_SubscribesAndUnsubscribes(t *testing.T) {
	logger := zap.NewNop()
	proc := &mockProcessor{response: master.TaskResponse{Content: "done"}}
	router := NewRouter(proc, logger)
	t.Cleanup(router.Stop)

	eb := newMockEventBus()
	router.SetEventBusSubscriber(eb)
	router.SetRendererEnabled(func(Platform) bool { return true })

	plugin := newMockRendererPlugin(PlatformFeishu)
	router.RegisterPlugin(plugin)

	msg := InboundMessage{
		MessageID: "msg-1",
		Platform:  PlatformFeishu,
		ChatID:    "c1",
		SenderID:  "u1",
		ChatType:  ChatDirect,
		Content:   "hello",
	}
	router.processMessageImpl(msg)

	if got := eb.subCount.Load(); got != 1 {
		t.Fatalf("Subscribe 调用次数 = %d，want 1", got)
	}
	if got := eb.unsubCount.Load(); got != 1 {
		t.Fatalf("Unsubscribe 调用次数 = %d，want 1", got)
	}
	if !plugin.called.Load() {
		t.Fatal("renderer.RenderEventStream 应被调用")
	}
}

func TestRouter_RendererPath_PassesOwnerTenantAndReplyToken(t *testing.T) {
	logger := zap.NewNop()
	proc := &mockProcessor{response: master.TaskResponse{Content: "done"}}
	router := NewRouter(proc, logger)
	t.Cleanup(router.Stop)

	eb := newMockEventBus()
	router.SetEventBusSubscriber(eb)
	router.SetRendererEnabled(func(Platform) bool { return true })

	plugin := newMockRendererPlugin(PlatformWeChatBot)
	router.RegisterPlugin(plugin)
	router.SetOwnerUserResolver(func(_ context.Context, userID string) (*auth.User, error) {
		return &auth.User{ID: userID, Status: "active"}, nil
	})

	msg := InboundMessage{
		MessageID:   "msg-1",
		Platform:    PlatformWeChatBot,
		TenantKey:   "owner-1",
		OwnerUserID: "owner-1",
		ChatID:      "wx-peer",
		SenderID:    "wx-peer",
		ChatType:    ChatDirect,
		Content:     "hello",
		ReplyToken:  "ctx-1",
	}
	router.processMessageImpl(msg)

	scope := plugin.snapshotScope()
	if scope.OwnerUserID != "owner-1" || scope.TenantKey != "owner-1" || scope.ReplyToken != "ctx-1" {
		t.Fatalf("renderer scope missing owner/tenant/reply token: %+v", scope)
	}
}

// TestRouter_NonRendererPath_NoSubscribe: plugin 不实现 EventRenderer → 绝不 Subscribe，
// 且 plugin.Send 恰调 1 次，与 pre-change 行为等价。
func TestRouter_NonRendererPath_NoSubscribe(t *testing.T) {
	logger := zap.NewNop()
	proc := &mockProcessor{response: master.TaskResponse{Content: "reply"}}
	router := NewRouter(proc, logger)
	t.Cleanup(router.Stop)

	eb := newMockEventBus()
	router.SetEventBusSubscriber(eb)
	router.SetRendererEnabled(func(Platform) bool { return true })

	plugin := &mockPlugin{platform: PlatformDingTalk} // 不实现 EventRenderer
	router.RegisterPlugin(plugin)

	msg := InboundMessage{
		MessageID: "msg-dt-1",
		Platform:  PlatformDingTalk,
		ChatID:    "c-dt",
		SenderID:  "u-dt",
		ChatType:  ChatDirect,
		Content:   "hi",
	}
	router.processMessageImpl(msg)

	if got := eb.subCount.Load(); got != 0 {
		t.Fatalf("非 renderer 平台 Subscribe 次数 = %d，want 0", got)
	}
	if got := plugin.getLastMsg().Content; got != "reply" {
		t.Fatalf("plugin.Send 收到 content = %q，want %q", got, "reply")
	}
}

// TestRouter_RendererPath_SessionFilter: EventBus 推一条 SessionID="other" 的事件，
// renderer 按契约过滤，不应出现在 gotEvents。（contract 在 mock renderer 中强制执行。）
func TestRouter_RendererPath_SessionFilter(t *testing.T) {
	logger := zap.NewNop()
	proc := &mockProcessorSlow{response: master.TaskResponse{Content: "done"}, delay: 200 * time.Millisecond}
	router := NewRouter(proc, logger)
	t.Cleanup(router.Stop)

	eb := newMockEventBus()
	router.SetEventBusSubscriber(eb)
	router.SetRendererEnabled(func(Platform) bool { return true })

	plugin := newMockRendererPlugin(PlatformFeishu)
	router.RegisterPlugin(plugin)

	msg := InboundMessage{
		MessageID: "msg-filter",
		Platform:  PlatformFeishu,
		ChatID:    "c1",
		SenderID:  "u1",
		ChatType:  ChatDirect,
		Content:   "hello",
	}

	// 在 ProcessMessage 执行期间向 EventBus 推两条事件：一条匹配、一条不匹配。
	// 自动生成的 session_id 由 imctx.BuildSessionID 拼装：
	// im-<platform>-<tenantKey>-<chatID>，TenantKey 缺省时回退 "default"
	expectedSessionID := "im-feishu-default-c1"
	go func() {
		time.Sleep(50 * time.Millisecond)
		eb.pushToAll(master.BroadcastMessage{Type: "message", SessionID: expectedSessionID, Payload: "match"})
		eb.pushToAll(master.BroadcastMessage{Type: "message", SessionID: "other-session", Payload: "mismatch"})
	}()

	router.processMessageImpl(msg)

	evs := plugin.snapshotEvents()
	for _, ev := range evs {
		if ev.SessionID != "" && ev.SessionID != expectedSessionID {
			t.Fatalf("renderer 消费到非本 session 事件，违反 filter 契约：%+v", ev)
		}
	}
	// 至少应该收到 match 那一条
	var sawMatch bool
	for _, ev := range evs {
		if s, ok := ev.Payload.(string); ok && s == "match" {
			sawMatch = true
			break
		}
	}
	if !sawMatch {
		t.Fatalf("renderer 未收到匹配 SessionID 的事件，gotEvents=%+v", evs)
	}
}

// TestRouter_RendererPath_FallbackOnError: renderer 返回 *RendererError{LastContent: "hi"}
// → Router 必须调 plugin.Send 一次，参数 Content="hi"。
func TestRouter_RendererPath_FallbackOnError(t *testing.T) {
	logger := zap.NewNop()
	proc := &mockProcessor{response: master.TaskResponse{Content: "done"}}
	router := NewRouter(proc, logger)
	t.Cleanup(router.Stop)

	eb := newMockEventBus()
	router.SetEventBusSubscriber(eb)
	router.SetRendererEnabled(func(Platform) bool { return true })

	plugin := newMockRendererPlugin(PlatformFeishu)
	plugin.returnErr = &RendererError{Inner: errContextForTest("patch failed"), LastContent: "hi"}
	router.RegisterPlugin(plugin)

	msg := InboundMessage{
		MessageID: "msg-fb",
		Platform:  PlatformFeishu,
		ChatID:    "c1",
		SenderID:  "u1",
		ChatType:  ChatDirect,
		Content:   "hello",
	}
	router.processMessageImpl(msg)

	last := plugin.getLastMsg()
	if last.Content != "hi" {
		t.Fatalf("plugin.Send Content = %q，want %q（应走 RendererError.LastContent 兜底）", last.Content, "hi")
	}
	if last.ReplyTo != "msg-fb" {
		t.Errorf("plugin.Send ReplyTo = %q，want %q", last.ReplyTo, "msg-fb")
	}
}

func TestRouter_RendererPath_InputReceivedBeforeResolver(t *testing.T) {
	logger := zap.NewNop()
	proc := &imCaptureProcessor{response: master.TaskResponse{Content: "done"}}
	router := NewRouter(proc, logger)
	t.Cleanup(router.Stop)

	eb := newMockEventBus()
	router.SetEventBusSubscriber(eb)
	router.SetRendererEnabled(func(Platform) bool { return true })

	plugin := newMockRendererPlugin(PlatformFeishu)
	router.RegisterPlugin(plugin)

	resolver := &blockingResolver{
		started: make(chan struct{}),
		release: make(chan struct{}),
		out: &imctx.IMMessageContext{
			References: []imctx.DocRef{{Type: imctx.RefDocx, Token: "docx_123"}},
		},
	}
	router.SetInboundContextResolver(PlatformFeishu, resolver)

	msg := InboundMessage{
		MessageID: "msg-ack-before-resolver",
		Platform:  PlatformFeishu,
		ChatID:    "c1",
		SenderID:  "u1",
		ChatType:  ChatDirect,
		Content:   "分析这个文档",
	}

	done := make(chan struct{})
	go func() {
		router.processMessageImpl(msg)
		close(done)
	}()

	select {
	case <-resolver.started:
	case <-time.After(time.Second):
		t.Fatal("resolver 未启动")
	}

	expectedSessionID := "im-feishu-default-c1"
	var sawAck bool
	for _, ev := range eb.snapshotBroadcasts() {
		if ev.Type != master.EventTypeInputReceived {
			continue
		}
		if ev.SessionID != expectedSessionID {
			t.Fatalf("input_received SessionID = %q，want %q", ev.SessionID, expectedSessionID)
		}
		payload, ok := ev.Payload.(master.InputReceivedEvent)
		if !ok {
			t.Fatalf("payload type = %T，want master.InputReceivedEvent", ev.Payload)
		}
		if payload.ChannelMessageID != msg.MessageID {
			t.Fatalf("ChannelMessageID = %q，want %q", payload.ChannelMessageID, msg.MessageID)
		}
		sawAck = true
	}
	if !sawAck {
		t.Fatalf("resolver 阻塞期间必须已经广播 input_received，got=%+v", eb.snapshotBroadcasts())
	}

	close(resolver.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("router 未在 resolver 放行后完成")
	}

	_, _, _, _, gotCtx := proc.snapshot()
	if gotCtx == nil || len(gotCtx.References) != 1 {
		t.Fatalf("resolver 结果仍需透传给 processor，got=%+v", gotCtx)
	}
}

// mockProcessorSlow 模拟 ProcessMessage 耗时，给 session filter 测试一个推事件的窗口。
type mockProcessorSlow struct {
	response master.TaskResponse
	delay    time.Duration
}

func (m *mockProcessorSlow) ProcessMessage(ctx context.Context, _ string, _ string) (master.TaskResponse, error) {
	select {
	case <-time.After(m.delay):
	case <-ctx.Done():
	}
	return m.response, nil
}

// errContextForTest 是 RendererError.Inner 的最小占位 error。
type errForTest string

func (e errForTest) Error() string { return string(e) }

func errContextForTest(s string) error { return errForTest(s) }

type blockingResolver struct {
	started chan struct{}
	release chan struct{}
	out     *imctx.IMMessageContext
}

func (r *blockingResolver) Resolve(_ context.Context, _ *InboundMessage) (*imctx.IMMessageContext, error) {
	close(r.started)
	<-r.release
	return r.out, nil
}
