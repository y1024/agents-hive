package wechatbot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/corespeed-io/wechatbot/golang"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/chef-guo/agents-hive/internal/channel"
	"github.com/chef-guo/agents-hive/internal/master"
	"github.com/chef-guo/agents-hive/internal/observability"
	"github.com/chef-guo/agents-hive/internal/store"
)

type fakeBackend struct {
	loginErr error
	sendErr  error
	runErr   error
	panicRun bool
	sentTo   string
	sentText string
	replyTo  string
	replyTok string
	replyTxt string
	replies  []string
	replyErr error
	typingTo string
	stopTo   string
	handler  func(*SDKMessage)
	onCount  int
	runCh    chan struct{}
	runCount int32
}

func (b *fakeBackend) Login(context.Context, bool) (*Credentials, error) {
	if b.loginErr != nil {
		return nil, b.loginErr
	}
	return &Credentials{AccountID: "wx-owner", UserID: "wx-sdk-user"}, nil
}

func (b *fakeBackend) OnMessage(handler func(*SDKMessage)) {
	b.handler = handler
	b.onCount++
}

func (b *fakeBackend) Run(ctx context.Context) error {
	atomic.AddInt32(&b.runCount, 1)
	if b.runCh != nil {
		close(b.runCh)
	}
	if b.panicRun {
		b.panicRun = false
		panic("sdk panic")
	}
	if b.runErr != nil {
		err := b.runErr
		b.runErr = nil
		return err
	}
	<-ctx.Done()
	return nil
}

func (b *fakeBackend) Stop() {}

func (b *fakeBackend) Reply(_ context.Context, msg *SDKMessage, text string) error {
	b.replyTo = msg.UserID
	b.replyTok = msg.ContextToken
	b.replyTxt = text
	b.replies = append(b.replies, text)
	return b.replyErr
}

func (b *fakeBackend) Send(_ context.Context, userID, text string) error {
	b.sentTo = userID
	b.sentText = text
	return b.sendErr
}

func (b *fakeBackend) SendWithContextToken(_ context.Context, userID, contextToken, text string) error {
	b.replyTo = userID
	b.replyTok = contextToken
	b.replyTxt = text
	b.replies = append(b.replies, text)
	return b.replyErr
}

func (b *fakeBackend) SendTyping(_ context.Context, userID string) error {
	b.typingTo = userID
	return nil
}

func (b *fakeBackend) StopTyping(_ context.Context, userID string) error {
	b.stopTo = userID
	return nil
}

func TestPluginSendRequiresOwnerScope(t *testing.T) {
	reg := NewRegistry(Config{Enabled: true, CredRoot: t.TempDir()}, nil, store.NewMemoryStore(), zap.NewNop())
	plugin := NewPlugin(reg, zap.NewNop())

	if err := plugin.Send(context.Background(), channel.OutboundMessage{
		TenantKey: "owner-1",
		ChatID:    "wx-peer",
		Content:   "hello",
	}); err == nil {
		t.Fatal("expected owner_user_id error")
	}

	if err := plugin.Send(context.Background(), channel.OutboundMessage{
		OwnerUserID: "owner-1",
		TenantKey:   "owner-2",
		ChatID:      "wx-peer",
		Content:     "hello",
	}); err == nil {
		t.Fatal("expected tenant mismatch error")
	}
}

func TestRegistryLoginAndPluginSend(t *testing.T) {
	st := store.NewMemoryStore()
	backend := &fakeBackend{runCh: make(chan struct{})}
	writer := &wechatMetricCaptureWriter{ch: make(chan observability.Metric, 8)}
	reg := NewRegistry(Config{Enabled: true, CredRoot: t.TempDir()}, nil, st, zap.NewNop())
	reg.SetBackendFactory(func(string, string, BackendOptions) Backend { return backend })
	reg.SetMetricsWriter(writer)
	plugin := NewPlugin(reg, zap.NewNop())

	if _, err := reg.Ensure(context.Background(), "owner-1", false); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	select {
	case <-backend.runCh:
	case <-time.After(time.Second):
		t.Fatal("backend Run was not started")
	}

	err := st.UpsertWechatConversation(context.Background(), &store.WechatConversationRecord{
		OwnerUserID:    "owner-1",
		OwnerAccountID: "wx-owner",
		PeerWxid:       "wx-peer",
		SessionID:      "im-wechatbot-owner-1-wx-peer",
		CanSend:        true,
		SendState:      "ready",
	})
	if err != nil {
		t.Fatalf("upsert conversation: %v", err)
	}

	if err := plugin.Send(context.Background(), channel.OutboundMessage{
		OwnerUserID: "owner-1",
		TenantKey:   "owner-1",
		ChatID:      "wx-peer",
		Content:     "hello",
	}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if backend.sentTo != "wx-peer" || backend.sentText != "hello" {
		t.Fatalf("unexpected send target/content: %q %q", backend.sentTo, backend.sentText)
	}
	if metric := writer.find(MetricActiveBots, "", nil); metric == nil || metric.Value != 1 {
		t.Fatalf("missing active bots metric: %+v", writer.items)
	}
	if metric := writer.find(MetricLoginTotal, "status", "success"); metric == nil {
		t.Fatalf("missing login success metric: %+v", writer.items)
	}
}

func TestPluginSendUsesSDKReplyWhenReplyTokenPresent(t *testing.T) {
	st := store.NewMemoryStore()
	backend := &fakeBackend{runCh: make(chan struct{})}
	reg := NewRegistry(Config{Enabled: true, CredRoot: t.TempDir()}, nil, st, zap.NewNop())
	reg.SetBackendFactory(func(string, string, BackendOptions) Backend { return backend })
	plugin := NewPlugin(reg, zap.NewNop())

	if _, err := reg.Ensure(context.Background(), "owner-1", false); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	defer reg.Stop()
	select {
	case <-backend.runCh:
	case <-time.After(time.Second):
		t.Fatal("backend Run was not started")
	}

	if err := plugin.Send(context.Background(), channel.OutboundMessage{
		OwnerUserID: "owner-1",
		TenantKey:   "owner-1",
		ChatID:      "wx-peer",
		Content:     "hello",
		ReplyToken:  "ctx-1",
	}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if backend.replyTo != "wx-peer" || backend.replyTok != "ctx-1" || backend.replyTxt != "hello" {
		t.Fatalf("unexpected reply target/token/content: %q %q %q", backend.replyTo, backend.replyTok, backend.replyTxt)
	}
	if backend.sentTo != "" {
		t.Fatalf("legacy Send was used unexpectedly: %q", backend.sentTo)
	}
	if backend.stopTo != "wx-peer" {
		t.Fatalf("StopTyping target = %q, want wx-peer", backend.stopTo)
	}
}

type fakeInputCoordinator struct {
	pending   []*master.InputRequest
	submitted []master.InputResponse
	err       error
}

func (f *fakeInputCoordinator) PendingInputs(taskID string) []*master.InputRequest {
	var out []*master.InputRequest
	for _, req := range f.pending {
		if req == nil {
			continue
		}
		if taskID == "" || req.TaskID == taskID {
			out = append(out, req)
		}
	}
	return out
}

func (f *fakeInputCoordinator) SubmitInput(resp master.InputResponse) error {
	f.submitted = append(f.submitted, resp)
	return f.err
}

func TestPluginControlInboundSubmitsClarificationOnlyWhenPending(t *testing.T) {
	coord := &fakeInputCoordinator{pending: []*master.InputRequest{{
		ID:        "input-1",
		TaskID:    "im-wechatbot-owner-peer",
		Type:      master.InputClarification,
		Prompt:    "城市？",
		CreatedAt: time.Now(),
	}}}
	plugin := NewPlugin(nil, zap.NewNop()).WithInputCoordinator(coord)

	res, err := plugin.ControlInbound(context.Background(), channel.InboundMessage{Content: "北京"}, "im-wechatbot-owner-peer")
	if err != nil {
		t.Fatalf("ControlInbound failed: %v", err)
	}
	if !res.Handled {
		t.Fatal("pending clarification should be handled")
	}
	if len(coord.submitted) != 1 {
		t.Fatalf("submitted count = %d, want 1", len(coord.submitted))
	}
	got := coord.submitted[0]
	if got.RequestID != "input-1" || got.TaskID != "im-wechatbot-owner-peer" || got.Value != "北京" || got.Action != "" {
		t.Fatalf("unexpected response: %+v", got)
	}

	res, err = plugin.ControlInbound(context.Background(), channel.InboundMessage{Content: "新问题"}, "other-session")
	if err != nil {
		t.Fatalf("ControlInbound no pending failed: %v", err)
	}
	if res.Handled {
		t.Fatal("message without pending input must not be intercepted")
	}
}

func TestPluginControlInboundApprovalRequiresExplicitDecision(t *testing.T) {
	coord := &fakeInputCoordinator{pending: []*master.InputRequest{{
		ID:        "perm-1",
		TaskID:    "im-wechatbot-owner-peer",
		Type:      master.InputPermission,
		Prompt:    "允许删除文件？",
		CreatedAt: time.Now(),
	}}}
	plugin := NewPlugin(nil, zap.NewNop()).WithInputCoordinator(coord)

	res, err := plugin.ControlInbound(context.Background(), channel.InboundMessage{Content: "随便看看"}, "im-wechatbot-owner-peer")
	if err != nil {
		t.Fatalf("ControlInbound ambiguous failed: %v", err)
	}
	if !res.Handled || res.Response == "" {
		t.Fatalf("ambiguous approval should be handled with help text: %+v", res)
	}
	if len(coord.submitted) != 0 {
		t.Fatalf("ambiguous text submitted unexpectedly: %+v", coord.submitted)
	}

	res, err = plugin.ControlInbound(context.Background(), channel.InboundMessage{Content: "批准"}, "im-wechatbot-owner-peer")
	if err != nil {
		t.Fatalf("ControlInbound approve failed: %v", err)
	}
	if !res.Handled {
		t.Fatal("approval should be handled")
	}
	if len(coord.submitted) != 1 || coord.submitted[0].Action != "approve" {
		t.Fatalf("approval submit mismatch: %+v", coord.submitted)
	}
}

func TestWechatRendererSendsInputRequestAndFinalMessage(t *testing.T) {
	st := store.NewMemoryStore()
	backend := &fakeBackend{runCh: make(chan struct{})}
	reg := NewRegistry(Config{Enabled: true, CredRoot: t.TempDir()}, nil, st, zap.NewNop())
	reg.SetBackendFactory(func(string, string, BackendOptions) Backend { return backend })
	plugin := NewPlugin(reg, zap.NewNop())

	if _, err := reg.Ensure(context.Background(), "owner-1", false); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	defer reg.Stop()
	select {
	case <-backend.runCh:
	case <-time.After(time.Second):
		t.Fatal("backend Run was not started")
	}

	events := make(chan master.BroadcastMessage, 4)
	events <- master.BroadcastMessage{
		Type:      master.EventTypeInputRequest,
		SessionID: "im-wechatbot-owner-1-wx-peer",
		Payload: &master.InputRequest{
			ID:      "input-1",
			TaskID:  "im-wechatbot-owner-1-wx-peer",
			Type:    master.InputChoice,
			Prompt:  "选时间",
			Options: []string{"现在", "明天"},
		},
	}
	events <- master.BroadcastMessage{
		Type:      master.EventTypeMessage,
		SessionID: "im-wechatbot-owner-1-wx-peer",
		Payload:   map[string]any{"content": "最终回复", "partial": false, "role": "assistant"},
	}
	close(events)

	err := plugin.RenderEventStream(context.Background(), channel.SessionScope{
		SessionID:   "im-wechatbot-owner-1-wx-peer",
		TenantKey:   "owner-1",
		OwnerUserID: "owner-1",
		ChatID:      "wx-peer",
		ReplyToken:  "ctx-1",
	}, events)
	if err != nil {
		t.Fatalf("RenderEventStream failed: %v", err)
	}
	if backend.replyTo != "wx-peer" || backend.replyTok != "ctx-1" {
		t.Fatalf("reply target/token mismatch: %q %q", backend.replyTo, backend.replyTok)
	}
	if backend.replyTxt != "最终回复" {
		t.Fatalf("last reply = %q, want final", backend.replyTxt)
	}
	if len(backend.replies) != 2 {
		t.Fatalf("reply count = %d, want 2: %+v", len(backend.replies), backend.replies)
	}
	if !strings.Contains(backend.replies[0], "选时间") || !strings.Contains(backend.replies[0], "1. 现在") {
		t.Fatalf("input request was not rendered: %q", backend.replies[0])
	}
}

func TestInstanceRegistersMessageHandlerOnce(t *testing.T) {
	backend := &fakeBackend{runCh: make(chan struct{})}
	inst := NewInstance(InstanceOptions{
		OwnerUserID:        "owner-1",
		CredentialPath:     filepath.Join(t.TempDir(), "credentials.json"),
		Backend:            backend,
		Store:              store.NewMemoryStore(),
		Logger:             zap.NewNop(),
		RecoverDelay:       time.Millisecond,
		MaxRecoverAttempts: 1,
	})
	defer inst.Stop()

	if err := inst.Login(context.Background(), false); err != nil {
		t.Fatalf("login: %v", err)
	}
	if backend.onCount != 1 {
		t.Fatalf("OnMessage count after first login = %d, want 1", backend.onCount)
	}
	inst.Stop()
	backend.runCh = make(chan struct{})
	if err := inst.Login(context.Background(), false); err != nil {
		t.Fatalf("second login: %v", err)
	}
	if backend.onCount != 1 {
		t.Fatalf("OnMessage count after second login = %d, want 1", backend.onCount)
	}
}

func TestBotRegistryConcurrentLoginIdempotent(t *testing.T) {
	backend := &blockingLoginBackend{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	reg := NewRegistry(Config{Enabled: true, CredRoot: t.TempDir()}, nil, store.NewMemoryStore(), zap.NewNop())
	var factoryCalls int32
	reg.SetBackendFactory(func(string, string, BackendOptions) Backend {
		atomic.AddInt32(&factoryCalls, 1)
		return backend
	})

	const workers = 20
	results := make(chan *BotInstance, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for idx := 0; idx < workers; idx++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inst, err := reg.Ensure(context.Background(), "owner-1", false)
			if err != nil {
				errs <- err
				return
			}
			results <- inst
		}()
	}

	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("login was not started")
	}
	if got := atomic.LoadInt32(&factoryCalls); got != 1 {
		t.Fatalf("factory calls while first login is blocked = %d, want 1", got)
	}
	close(backend.release)
	wg.Wait()
	close(results)
	close(errs)
	defer reg.Stop()

	for err := range errs {
		t.Fatalf("Ensure returned error: %v", err)
	}
	var first *BotInstance
	for inst := range results {
		if first == nil {
			first = inst
			continue
		}
		if inst != first {
			t.Fatal("concurrent Ensure returned different instances for the same owner")
		}
	}
	if first == nil {
		t.Fatal("no instance returned")
	}
	if got := atomic.LoadInt32(&backend.loginCount); got != 1 {
		t.Fatalf("Login count = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&factoryCalls); got != 1 {
		t.Fatalf("factory calls = %d, want 1", got)
	}
	if first.Status() != StatusOnline {
		t.Fatalf("status = %s, want online", first.Status())
	}
}

func TestBotRegistry_ConcurrentAccess_NoDeadlock(t *testing.T) {
	reg := NewRegistry(Config{Enabled: true, CredRoot: t.TempDir()}, nil, store.NewMemoryStore(), zap.NewNop())
	reg.SetBackendFactory(func(string, string, BackendOptions) Backend {
		release := make(chan struct{})
		close(release)
		return &blockingLoginBackend{
			started: make(chan struct{}),
			release: release,
		}
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for idx := 0; idx < 100; idx++ {
			idx := idx
			wg.Add(1)
			go func() {
				defer wg.Done()
				owner := "owner-" + string(rune('a'+idx%5))
				_, _ = reg.Ensure(context.Background(), owner, false)
				_, _ = reg.Get(owner)
				_, _ = reg.Status(context.Background(), owner)
				if idx%17 == 0 {
					_ = reg.Logout(context.Background(), owner)
				}
			}()
		}
		wg.Wait()
		_ = reg.Stop()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent registry access deadlocked")
	}
}

func TestInstanceSendMarksNoContext(t *testing.T) {
	st := store.NewMemoryStore()
	backend := &fakeBackend{sendErr: errors.New("no context_token for user wx-peer")}
	writer := &wechatMetricCaptureWriter{ch: make(chan observability.Metric, 8)}
	inst := NewInstance(InstanceOptions{
		OwnerUserID:   "owner-1",
		Backend:       backend,
		Store:         st,
		Logger:        zap.NewNop(),
		MetricsWriter: writer,
	})
	inst.setStatus(StatusOnline, "")
	err := st.UpsertWechatConversation(context.Background(), &store.WechatConversationRecord{
		OwnerUserID:    "owner-1",
		OwnerAccountID: "wx-owner",
		PeerWxid:       "wx-peer",
		SessionID:      "im-wechatbot-owner-1-wx-peer",
		CanSend:        true,
		SendState:      "ready",
	})
	if err != nil {
		t.Fatalf("upsert conversation: %v", err)
	}

	if err := inst.Send(context.Background(), "wx-peer", "hello"); !errors.Is(err, ErrNoContextToken) {
		t.Fatalf("expected ErrNoContextToken, got %v", err)
	}
	conv, err := st.GetWechatConversationByOwnerPeer(context.Background(), "owner-1", "wx-peer")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if conv.CanSend || conv.SendState != "no_context" {
		t.Fatalf("send state not updated: can_send=%v send_state=%q", conv.CanSend, conv.SendState)
	}
	if metric := writer.find(MetricUnavailableTotal, "reason", "no_context"); metric == nil {
		t.Fatalf("missing unavailable no_context metric: %+v", writer.items)
	}
	if metric := writer.find(MetricOutboundTotal, "status", "no_context"); metric == nil {
		t.Fatalf("missing outbound no_context metric: %+v", writer.items)
	}
}

func TestInstanceSendFallsBackToPersistedContextToken(t *testing.T) {
	st := store.NewMemoryStore()
	backend := &fakeBackend{sendErr: errors.New("no context_token for user wx-peer")}
	inst := NewInstance(InstanceOptions{
		OwnerUserID: "owner-1",
		Backend:     backend,
		Store:       st,
		Logger:      zap.NewNop(),
	})
	inst.setStatus(StatusOnline, "")
	if err := st.UpsertWechatConversation(context.Background(), &store.WechatConversationRecord{
		OwnerUserID:    "owner-1",
		OwnerAccountID: "wx-owner",
		PeerWxid:       "wx-peer",
		SessionID:      "im-wechatbot-owner-1-wx-peer",
		CanSend:        true,
		SendState:      "ready",
	}); err != nil {
		t.Fatalf("upsert conversation: %v", err)
	}
	if err := st.UpdateWechatConversationContextToken(context.Background(), "owner-1", "wx-peer", "ctx-stored"); err != nil {
		t.Fatalf("save context token: %v", err)
	}

	if err := inst.Send(context.Background(), "wx-peer", "hello"); err != nil {
		t.Fatalf("send fallback: %v", err)
	}
	if backend.replyTo != "wx-peer" || backend.replyTok != "ctx-stored" || backend.replyTxt != "hello" {
		t.Fatalf("unexpected persisted-token send: %q %q %q", backend.replyTo, backend.replyTok, backend.replyTxt)
	}
}

func TestInstanceSendEmitsOutboundSuccessMetric(t *testing.T) {
	st := store.NewMemoryStore()
	backend := &fakeBackend{}
	writer := &wechatMetricCaptureWriter{ch: make(chan observability.Metric, 8)}
	inst := NewInstance(InstanceOptions{
		OwnerUserID:   "owner-1",
		Backend:       backend,
		Store:         st,
		Logger:        zap.NewNop(),
		MetricsWriter: writer,
	})
	inst.setStatus(StatusOnline, "")
	if err := st.UpsertWechatConversation(context.Background(), &store.WechatConversationRecord{
		OwnerUserID:    "owner-1",
		OwnerAccountID: "wx-owner",
		PeerWxid:       "wx-peer",
		SessionID:      "im-wechatbot-owner-1-wx-peer",
		CanSend:        false,
		SendState:      "unknown",
	}); err != nil {
		t.Fatalf("upsert conversation: %v", err)
	}

	if err := inst.Send(context.Background(), "wx-peer", "hello"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if metric := writer.find(MetricOutboundTotal, "status", "success"); metric == nil {
		t.Fatalf("missing outbound success metric: %+v", writer.items)
	}
	conv, err := st.GetWechatConversationByOwnerPeer(context.Background(), "owner-1", "wx-peer")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if !conv.CanSend || conv.SendState != "ready" {
		t.Fatalf("send state = can_send=%v state=%q, want true/ready", conv.CanSend, conv.SendState)
	}
}

func TestBuildSessionRecordMasksPeerID(t *testing.T) {
	rec := buildSessionRecord("im-wechatbot-owner-1-wxid_real_peer_123456", "owner-1", "wxid_real_peer_123456", time.Now())
	if strings.Contains(rec.Name, "wxid_real_peer_123456") {
		t.Fatalf("session name leaked raw peer wxid: %q", rec.Name)
	}
	if rec.Name == "微信会话 " {
		t.Fatalf("session name missing safe peer suffix: %q", rec.Name)
	}
}

func TestBotInstanceAutoRecoverAfterRunError(t *testing.T) {
	backend := &fakeBackend{runErr: errors.New("stream closed")}
	writer := &wechatMetricCaptureWriter{ch: make(chan observability.Metric, 8)}
	inst := NewInstance(InstanceOptions{
		OwnerUserID:        "owner-1",
		Backend:            backend,
		Store:              store.NewMemoryStore(),
		Logger:             zap.NewNop(),
		RecoverDelay:       time.Millisecond,
		MaxRecoverAttempts: 1,
		MetricsWriter:      writer,
	})
	defer inst.Stop()

	if err := inst.Login(context.Background(), false); err != nil {
		t.Fatalf("login: %v", err)
	}
	deadline := time.After(time.Second)
	for inst.Status() != StatusOnline || atomic.LoadInt32(&backend.runCount) < 2 {
		select {
		case <-deadline:
			t.Fatalf("auto recover did not return online, status=%s run_count=%d err=%s", inst.Status(), atomic.LoadInt32(&backend.runCount), inst.Error())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if metric := writer.find(MetricAutoRecoverTotal, "result", "success"); metric == nil {
		t.Fatalf("missing auto recover success metric: %+v", writer.items)
	}
}

func TestBotInstanceAutoRecoverRequiresManualAfterFailures(t *testing.T) {
	backend := &fakeBackend{loginErr: errors.New("login failed"), runErr: errors.New("stream closed")}
	writer := &wechatMetricCaptureWriter{ch: make(chan observability.Metric, 8)}
	inst := NewInstance(InstanceOptions{
		OwnerUserID:        "owner-1",
		Backend:            backend,
		Logger:             zap.NewNop(),
		RecoverDelay:       time.Millisecond,
		MaxRecoverAttempts: 2,
		MetricsWriter:      writer,
	})
	inst.setStatus(StatusOnline, "")
	go inst.runLoop(context.Background())
	defer inst.Stop()

	deadline := time.After(time.Second)
	for inst.Status() != StatusReloginRequired {
		select {
		case <-deadline:
			t.Fatalf("status=%s, want relogin_required; err=%s", inst.Status(), inst.Error())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if metric := writer.find(MetricAutoRecoverTotal, "result", "manual_required"); metric == nil {
		t.Fatalf("missing manual_required metric: %+v", writer.items)
	}
}

func TestBotInstanceAutoRecoverAfterPanic(t *testing.T) {
	backend := &fakeBackend{panicRun: true}
	inst := NewInstance(InstanceOptions{
		OwnerUserID:        "owner-1",
		Backend:            backend,
		Logger:             zap.NewNop(),
		RecoverDelay:       time.Millisecond,
		MaxRecoverAttempts: 1,
	})
	defer inst.Stop()

	if err := inst.Login(context.Background(), false); err != nil {
		t.Fatalf("login: %v", err)
	}
	deadline := time.After(time.Second)
	for inst.Status() != StatusOnline || atomic.LoadInt32(&backend.runCount) < 2 {
		select {
		case <-deadline:
			t.Fatalf("panic auto recover did not return online, status=%s run_count=%d", inst.Status(), atomic.LoadInt32(&backend.runCount))
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestBotInstanceInboundEmitsMetric(t *testing.T) {
	writer := &wechatMetricCaptureWriter{ch: make(chan observability.Metric, 8)}
	st := store.NewMemoryStore()
	inst := NewInstance(InstanceOptions{
		OwnerUserID:   "owner-1",
		Backend:       &fakeBackend{},
		Store:         st,
		Logger:        zap.NewNop(),
		MetricsWriter: writer,
	})

	inst.handleIncoming(context.Background(), &SDKMessage{
		UserID:       "wx-peer",
		Text:         "hello",
		Type:         "text",
		ContextToken: "ctx-token",
		Timestamp:    time.Now(),
	})

	if metric := writer.find(MetricInboundTotal, "msg_type", "text"); metric == nil {
		t.Fatalf("missing inbound text metric: %+v", writer.items)
	}
	token, err := st.GetWechatConversationContextToken(context.Background(), "owner-1", "wx-peer")
	if err != nil {
		t.Fatalf("get persisted context token: %v", err)
	}
	if token != "ctx-token" {
		t.Fatalf("persisted context token = %q, want ctx-token", token)
	}
}

func TestBotInstanceInboundPreservesSafeMediaMetadata(t *testing.T) {
	st := store.NewMemoryStore()
	inst := NewInstance(InstanceOptions{
		OwnerUserID: "owner-1",
		Backend:     &fakeBackend{},
		Store:       st,
		Logger:      zap.NewNop(),
	})

	inst.handleIncoming(context.Background(), &SDKMessage{
		UserID:       "wx-peer",
		Type:         "file",
		Files:        []sdk.FileContent{{FileName: "a.pdf", Size: 123}},
		ContextToken: "ctx-token",
		Timestamp:    time.Now(),
	})

	conv, err := st.GetWechatConversationByOwnerPeer(context.Background(), "owner-1", "wx-peer")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(conv.Metadata, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata["message_type"] != "file" {
		t.Fatalf("message_type = %v, want file", metadata["message_type"])
	}
	if metadata["file_count"] != float64(1) {
		t.Fatalf("file_count = %v, want 1", metadata["file_count"])
	}
	if metadata["first_file_name"] != "a.pdf" {
		t.Fatalf("first_file_name = %v, want a.pdf", metadata["first_file_name"])
	}
	if metadata["first_file_size"] != float64(123) {
		t.Fatalf("first_file_size = %v, want 123", metadata["first_file_size"])
	}
	encoded := string(conv.Metadata)
	for _, forbidden := range []string{"context_token", "ctx-token", "encrypt_query_param", "aes_key"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("metadata leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestBotInstanceInboundLogsMalformedMessages(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	inst := NewInstance(InstanceOptions{
		OwnerUserID: "owner-1",
		Backend:     &fakeBackend{},
		Store:       store.NewMemoryStore(),
		Logger:      zap.New(core),
	})

	inst.handleIncoming(context.Background(), nil)
	inst.handleIncoming(context.Background(), &SDKMessage{
		Text:         "hello",
		Type:         "text",
		ContextToken: "ctx-token",
		Timestamp:    time.Now(),
	})

	if logs.FilterMessage("wechatbot 入站消息为空，已丢弃").Len() != 1 {
		t.Fatalf("missing nil inbound diagnostic log: %+v", logs.All())
	}
	if logs.FilterMessage("wechatbot 入站消息缺少 peer wxid，已丢弃").Len() != 1 {
		t.Fatalf("missing empty user_id diagnostic log: %+v", logs.All())
	}
}

func TestWeChatEventHubUserIsolation(t *testing.T) {
	reg := NewRegistry(Config{Enabled: true, CredRoot: t.TempDir()}, nil, store.NewMemoryStore(), zap.NewNop())
	ownerAEvents, unsubscribeA := reg.Subscribe("owner-a")
	defer unsubscribeA()
	ownerBEvents, unsubscribeB := reg.Subscribe("owner-b")
	defer unsubscribeB()

	reg.events.Publish("owner-a", Event{Type: "qr", Status: StatusWaitingQRScan, QRURL: "https://example.test/qr"})

	select {
	case ev := <-ownerAEvents:
		if ev.Type != "qr" || ev.QRURL == "" {
			t.Fatalf("unexpected owner-a event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("owner-a did not receive event")
	}

	select {
	case ev := <-ownerBEvents:
		t.Fatalf("owner-b received leaked event: %+v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestWeChatBotCredentialsDirPermission(t *testing.T) {
	credRoot := t.TempDir()
	backend := &fakeBackend{}
	reg := NewRegistry(Config{Enabled: true, CredRoot: credRoot}, nil, store.NewMemoryStore(), zap.NewNop())
	reg.SetBackendFactory(func(_ string, credPath string, _ BackendOptions) Backend {
		if err := os.MkdirAll(filepath.Dir(credPath), 0777); err != nil {
			t.Fatalf("mkdir cred dir: %v", err)
		}
		if err := os.WriteFile(credPath, []byte("{}"), 0666); err != nil {
			t.Fatalf("write cred file: %v", err)
		}
		return backend
	})

	inst, err := reg.Ensure(context.Background(), "owner-1", false)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	defer inst.Stop()

	dirInfo, err := os.Stat(filepath.Join(credRoot, "users", "owner-1"))
	if err != nil {
		t.Fatalf("stat cred dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0700 {
		t.Fatalf("dir mode = %o, want 0700", got)
	}
	fileInfo, err := os.Stat(filepath.Join(credRoot, "users", "owner-1", "credentials.json"))
	if err != nil {
		t.Fatalf("stat cred file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0600 {
		t.Fatalf("file mode = %o, want 0600", got)
	}
}

func TestPluginWebhookIsNotSupported(t *testing.T) {
	plugin := NewPlugin(nil, zap.NewNop())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/channel/wechatbot/webhook", nil)
	plugin.WebhookHandler()(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

type wechatMetricCaptureWriter struct {
	mu    sync.Mutex
	items []observability.Metric
	ch    chan observability.Metric
}

func (w *wechatMetricCaptureWriter) Record(_ context.Context, metric observability.Metric) error {
	w.mu.Lock()
	w.items = append(w.items, metric)
	w.mu.Unlock()
	if w.ch != nil {
		select {
		case w.ch <- metric:
		default:
		}
	}
	return nil
}

func (w *wechatMetricCaptureWriter) find(name, key string, value any) *observability.Metric {
	deadline := time.After(time.Second)
	for {
		w.mu.Lock()
		for _, metric := range w.items {
			if metric.Name == name {
				if key == "" {
					cp := metric
					w.mu.Unlock()
					return &cp
				}
				if metric.Labels != nil && metric.Labels[key] == value {
					cp := metric
					w.mu.Unlock()
					return &cp
				}
			}
		}
		w.mu.Unlock()
		select {
		case metric := <-w.ch:
			if metric.Name != name {
				continue
			}
			if key == "" {
				return &metric
			}
			if metric.Labels != nil && metric.Labels[key] == value {
				return &metric
			}
		case <-deadline:
			return nil
		}
	}
}

var _ observability.MetricsWriter = (*wechatMetricCaptureWriter)(nil)

type blockingLoginBackend struct {
	started    chan struct{}
	release    chan struct{}
	loginCount int32
	runCount   int32
	startOnce  sync.Once
}

func (b *blockingLoginBackend) Login(ctx context.Context, _ bool) (*Credentials, error) {
	atomic.AddInt32(&b.loginCount, 1)
	b.startOnce.Do(func() { close(b.started) })
	select {
	case <-b.release:
		return &Credentials{AccountID: "wx-owner", UserID: "wx-sdk-user"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *blockingLoginBackend) OnMessage(func(*SDKMessage)) {}

func (b *blockingLoginBackend) Run(ctx context.Context) error {
	atomic.AddInt32(&b.runCount, 1)
	<-ctx.Done()
	return nil
}

func (b *blockingLoginBackend) Stop() {}

func (b *blockingLoginBackend) Reply(context.Context, *SDKMessage, string) error {
	return nil
}

func (b *blockingLoginBackend) Send(context.Context, string, string) error {
	return nil
}

func (b *blockingLoginBackend) SendWithContextToken(context.Context, string, string, string) error {
	return nil
}

func (b *blockingLoginBackend) SendTyping(context.Context, string) error {
	return nil
}

func (b *blockingLoginBackend) StopTyping(context.Context, string) error {
	return nil
}

var _ Backend = (*blockingLoginBackend)(nil)
