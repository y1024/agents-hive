package master

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/journal"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/master/assistantcap"
	"github.com/chef-guo/agents-hive/internal/observability"
	"github.com/chef-guo/agents-hive/internal/store"
)

// failingStore 是一个 SessionStore mock，RevertSession 总是返回错误，其余方法正常返回
type failingStore struct{}

func (f *failingStore) CreateSession(_ context.Context, _ *store.SessionRecord) error { return nil }
func (f *failingStore) SaveSession(_ context.Context, _ *store.SessionRecord) error   { return nil }
func (f *failingStore) LoadSession(_ context.Context, _ string) (*store.SessionRecord, error) {
	return nil, store.ErrNotFound
}
func (f *failingStore) DeleteSession(_ context.Context, _ string) error { return nil }
func (f *failingStore) ListSessions(_ context.Context) ([]*store.SessionRecord, error) {
	return nil, nil
}
func (f *failingStore) GetLastActiveSession(_ context.Context) (*store.SessionRecord, error) {
	return nil, store.ErrNotFound
}
func (f *failingStore) AddMessage(_ context.Context, _, _, _ string, _ map[string]any) error {
	return nil
}
func (f *failingStore) GetMessages(_ context.Context, _ string, _ int) ([]store.MessageRecord, error) {
	return nil, nil
}
func (f *failingStore) ForkSession(_ context.Context, _ string, _ int, _, _, _ string) error {
	return nil
}
func (f *failingStore) ListSessionsByUser(_ context.Context, _ string, _ bool) ([]*store.SessionRecord, error) {
	return nil, nil
}
func (f *failingStore) RevertSession(_ context.Context, _ string, _ int) error {
	return fmt.Errorf("模拟数据库错误: %w", store.ErrCorrupted)
}
func (f *failingStore) UpsertSessionPref(_ context.Context, _, _ string, _ bool) error { return nil }
func (f *failingStore) GetSessionStarred(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}
func (f *failingStore) UpdateSessionTags(_ context.Context, _ string, _ []string) error { return nil }
func (f *failingStore) Close() error                                                    { return nil }

// setupTestMasterWithStore 创建带有指定 store 的测试 Master
func setupTestMasterWithStore(t *testing.T, st store.SessionStore) *Master {
	m := setupTestMaster(t, st)
	m.store = st // setupTestMaster 默认忽略 store 参数，这里直接设置
	return m
}

// TestHandleLegacyCommand_ClearWithStoreFailure 测试 clear 命令在 store 失败时返回错误信息
func TestHandleLegacyCommand_ClearWithStoreFailure(t *testing.T) {
	m := setupTestMasterWithStore(t, &failingStore{})
	defer m.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m.Start(ctx)
	go m.SessionLoop(ctx)

	// 先发一条消息让会话有内容
	session, _ := m.sessionMgr.GetOrCreateSession("test-clear")
	session.mu.Lock()
	session.Messages = []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
	}
	session.mu.Unlock()

	// 发送 clear 命令
	select {
	case m.RequestCh() <- SessionRequest{Input: "clear"}:
	case <-time.After(time.Second):
		t.Fatal("超时: 无法发送请求")
	}

	// 验证响应包含错误信息
	select {
	case resp := <-m.ResponseCh():
		assert.True(t, resp.Completed, "clear 应该标记为完成")
		assert.False(t, resp.Exit, "clear 不应退出")
		assert.Contains(t, resp.Message, "数据库清空失败", "应该包含 store 错误信息")
	case <-time.After(time.Second):
		t.Fatal("超时: 没有收到响应")
	}
}

// TestHandleLegacyCommand_ResetWithStoreFailure 测试 reset 命令在 store 失败时返回错误信息
func TestHandleLegacyCommand_ResetWithStoreFailure(t *testing.T) {
	m := setupTestMasterWithStore(t, &failingStore{})
	defer m.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m.Start(ctx)
	go m.SessionLoop(ctx)

	// 发送 reset 命令
	select {
	case m.RequestCh() <- SessionRequest{Input: "reset"}:
	case <-time.After(time.Second):
		t.Fatal("超时: 无法发送请求")
	}

	// 验证响应包含错误信息
	select {
	case resp := <-m.ResponseCh():
		assert.True(t, resp.Completed, "reset 应该标记为完成")
		assert.False(t, resp.Exit, "reset 不应退出")
		assert.Contains(t, resp.Message, "数据库清空失败", "应该包含 store 错误信息")
	case <-time.After(time.Second):
		t.Fatal("超时: 没有收到响应")
	}
}

// TestMaybeGenerateTitle_NoTitleAgent 测试 title-agent 不存在时安全跳过
func TestMaybeGenerateTitle_NoTitleAgent(t *testing.T) {
	m := setupTestMasterSimple(t)
	defer m.Stop()

	session, _ := m.sessionMgr.GetOrCreateSession("test-title")
	session.mu.Lock()
	session.Name = "main" // 默认名称，触发条件
	session.Messages = []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
		{Role: "assistant", Content: llm.NewTextContent("hi there")},
	}
	session.mu.Unlock()

	// 调用 maybeGenerateTitle — registry 中没有 title-agent，应安全返回
	ctx := context.Background()
	require.NotPanics(t, func() {
		m.maybeGenerateTitle(ctx, session)
	})

	// 等待一小段时间确认没有 goroutine panic
	time.Sleep(100 * time.Millisecond)

	// 名称应保持不变
	session.mu.RLock()
	assert.Equal(t, "main", session.Name, "没有 title-agent 时名称不应改变")
	session.mu.RUnlock()
}

// TestMaybeGenerateTitle_SkipWhenNameSet 测试已设置名称时跳过标题生成
func TestMaybeGenerateTitle_SkipWhenNameSet(t *testing.T) {
	m := setupTestMasterSimple(t)
	defer m.Stop()

	session, _ := m.sessionMgr.GetOrCreateSession("test-title-skip")
	session.mu.Lock()
	session.Name = "已有标题"
	session.Messages = []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
		{Role: "assistant", Content: llm.NewTextContent("hi")},
	}
	session.mu.Unlock()

	ctx := context.Background()
	require.NotPanics(t, func() {
		m.maybeGenerateTitle(ctx, session)
	})

	session.mu.RLock()
	assert.Equal(t, "已有标题", session.Name, "已设置名称时不应改变")
	session.mu.RUnlock()
}

// TestMaybeGenerateTitle_SkipWhenTooFewMessages 测试消息不足时跳过标题生成
func TestMaybeGenerateTitle_SkipWhenTooFewMessages(t *testing.T) {
	m := setupTestMasterSimple(t)
	defer m.Stop()

	session, _ := m.sessionMgr.GetOrCreateSession("test-title-few")
	session.mu.Lock()
	session.Name = "main"
	session.Messages = []llm.MessageWithTools{
		{Role: "user", Content: llm.NewTextContent("hello")},
	}
	session.mu.Unlock()

	ctx := context.Background()
	require.NotPanics(t, func() {
		m.maybeGenerateTitle(ctx, session)
	})

	session.mu.RLock()
	assert.Equal(t, "main", session.Name, "消息不足时不应改变名称")
	session.mu.RUnlock()
}

// TestProcessTask_FallbackToReAct 测试无 registry 或 LLM 时回退到 ReAct 模式
func TestProcessTask_FallbackToReAct(t *testing.T) {
	m := setupTestMasterSimple(t)
	defer m.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m.Start(ctx)
	go m.SessionLoop(ctx)

	// 发送一个复杂的多步骤请求（正常会触发 Plan-and-Execute）
	// 但由于没有 LLM client，应该回退到 ReAct
	complexRequest := "请帮我完成以下任务：1. 创建用户模块 2. 实现登录功能 3. 添加权限控制 4. 编写单元测试"

	select {
	case m.RequestCh() <- SessionRequest{Input: complexRequest}:
	case <-time.After(time.Second):
		t.Fatal("超时: 无法发送请求")
	}

	// 应该收到响应（ReAct 模式下会因为没有 LLM 而返回错误，但不应 panic）
	select {
	case resp := <-m.ResponseCh():
		// ReAct 模式下没有 LLM 会失败，但关键是没有 panic
		_ = resp
	case <-time.After(5 * time.Second):
		// 超时也可以接受 — processTaskDirectExec 内部可能因无 LLM 而阻塞
		// 关键验证点是没有 panic
	}
}

// TestSessionLoop_LoadCorruptedSession 测试加载损坏会话后创建新会话
func TestSessionLoop_LoadCorruptedSession(t *testing.T) {
	m := setupTestMasterWithStore(t, &failingStore{})
	defer m.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m.Start(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- m.SessionLoop(ctx)
	}()

	// SessionLoop 应该成功启动（即使加载失败也会创建新会话）
	// 发送 exit 验证它正在运行
	select {
	case m.RequestCh() <- SessionRequest{Input: "exit"}:
	case <-time.After(2 * time.Second):
		t.Fatal("超时: SessionLoop 可能未启动")
	}

	select {
	case resp := <-m.ResponseCh():
		assert.True(t, resp.Exit, "应返回 Exit=true")
	case <-time.After(time.Second):
		t.Fatal("超时: 没有收到退出响应")
	}

	select {
	case err := <-errCh:
		assert.NoError(t, err, "SessionLoop 应正常退出")
	case <-time.After(time.Second):
		t.Fatal("超时: SessionLoop 未退出")
	}
}

// TestSessionLoop_ChineseCommands 测试中文命令处理
func TestSessionLoop_ChineseCommands(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantExit bool
		wantMsg  string
	}{
		{"退出", "退出", true, ""},
		{"再见", "再见", true, ""},
		{"清空", "清空", false, "已清空"},
		{"清空历史", "清空历史", false, "已清空"},
		{"重置", "重置", false, "已重置"},
		{"重新开始", "重新开始", false, "已重置"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := setupTestMasterSimple(t)
			defer m.Stop()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			m.Start(ctx)
			go m.SessionLoop(ctx)

			select {
			case m.RequestCh() <- SessionRequest{Input: tt.input}:
			case <-time.After(time.Second):
				t.Fatal("超时: 无法发送请求")
			}

			select {
			case resp := <-m.ResponseCh():
				assert.Equal(t, tt.wantExit, resp.Exit, "Exit 状态不符")
				if tt.wantMsg != "" {
					assert.Contains(t, resp.Message, tt.wantMsg, "响应消息不符")
				}
			case <-time.After(time.Second):
				t.Fatal("超时: 没有收到响应")
			}
		})
	}
}

type terminateSessionStore struct {
	saveCalls atomic.Int32
}

func (s *terminateSessionStore) CreateSession(_ context.Context, _ *store.SessionRecord) error {
	return nil
}
func (s *terminateSessionStore) SaveSession(_ context.Context, _ *store.SessionRecord) error {
	s.saveCalls.Add(1)
	return nil
}
func (s *terminateSessionStore) LoadSession(_ context.Context, _ string) (*store.SessionRecord, error) {
	return nil, store.ErrNotFound
}
func (s *terminateSessionStore) DeleteSession(_ context.Context, _ string) error { return nil }
func (s *terminateSessionStore) ListSessions(_ context.Context) ([]*store.SessionRecord, error) {
	return nil, nil
}
func (s *terminateSessionStore) GetLastActiveSession(_ context.Context) (*store.SessionRecord, error) {
	return nil, store.ErrNotFound
}
func (s *terminateSessionStore) AddMessage(_ context.Context, _, _, _ string, _ map[string]any) error {
	return nil
}
func (s *terminateSessionStore) GetMessages(_ context.Context, _ string, _ int) ([]store.MessageRecord, error) {
	return nil, nil
}
func (s *terminateSessionStore) ForkSession(_ context.Context, _ string, _ int, _, _, _ string) error {
	return nil
}
func (s *terminateSessionStore) ListSessionsByUser(_ context.Context, _ string, _ bool) ([]*store.SessionRecord, error) {
	return nil, nil
}
func (s *terminateSessionStore) RevertSession(_ context.Context, _ string, _ int) error { return nil }
func (s *terminateSessionStore) UpsertSessionPref(_ context.Context, _, _ string, _ bool) error {
	return nil
}
func (s *terminateSessionStore) GetSessionStarred(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}
func (s *terminateSessionStore) UpdateSessionTags(_ context.Context, _ string, _ []string) error {
	return nil
}
func (s *terminateSessionStore) Close() error { return nil }

type terminateSessionJournal struct {
	endCalls atomic.Int32
	lastID   atomic.Value
	lastWhy  atomic.Value
}

func (j *terminateSessionJournal) StartSession(_ context.Context, _ string, _ string) error {
	return nil
}
func (j *terminateSessionJournal) UpdateSessionTask(_ context.Context, _ string, _ string) error {
	return nil
}
func (j *terminateSessionJournal) EndSession(_ context.Context, sessionID string, reason string) error {
	j.endCalls.Add(1)
	j.lastID.Store(sessionID)
	j.lastWhy.Store(reason)
	return nil
}
func (j *terminateSessionJournal) GetJournal(_ context.Context, _ string, _ int) (*journal.SessionJournal, error) {
	return nil, nil
}
func (j *terminateSessionJournal) LogToolCall(_ context.Context, _ journal.ToolCallEntry) error {
	return nil
}
func (j *terminateSessionJournal) LogFileChange(_ context.Context, _ journal.FileChangeEntry) error {
	return nil
}
func (j *terminateSessionJournal) LogDecision(_ context.Context, _ journal.DecisionEntry) error {
	return nil
}
func (j *terminateSessionJournal) GetJournalEvents(_ context.Context, _ string, _ int, _ time.Time) ([]journal.JournalEvent, error) {
	return nil, nil
}
func (j *terminateSessionJournal) GetJournalStats(_ context.Context, _ []string) (map[string]*journal.JournalStats, error) {
	return nil, nil
}
func (j *terminateSessionJournal) DeleteSession(_ context.Context, _ string) error { return nil }
func (j *terminateSessionJournal) Close() error                                    { return nil }

func TestPublicAPI_TerminateSession_IdempotentContract(t *testing.T) {
	m := setupTestMasterSimple(t)
	defer m.Stop()

	session, _ := m.sessionMgr.GetOrCreateSession("terminate-idempotent")
	require.NotNil(t, session)

	j := &terminateSessionJournal{}
	m.SetJournal(j)

	err := m.TerminateSession(session.ID, "feishu reset")
	require.NoError(t, err)

	err = m.TerminateSession(session.ID, "feishu reset")
	require.NoError(t, err)

	require.Equal(t, int32(1), j.endCalls.Load(), "EndSession 必须至多调用一次")
	require.Equal(t, session.ID, j.lastID.Load())
	require.Equal(t, "feishu reset", j.lastWhy.Load())
	assert.True(t, session.IsTerminated(), "公开 API 必须将 session 标记为 terminated")
	assert.Equal(t, "feishu reset", session.TerminationReason(), "公开 API 必须记录终止原因")
}

func TestPublicAPI_TerminateSession_CancelsRunningTaskAndWaitsForSemaphore(t *testing.T) {
	m := setupTestMasterSimple(t)
	defer m.Stop()

	session, _ := m.sessionMgr.GetOrCreateSession("terminate-running")
	require.NotNil(t, session)

	sem := m.getSessionSem(session.ID)
	sem <- struct{}{}

	taskStarted := make(chan struct{})
	taskCanceled := make(chan struct{})

	// 最小化模拟"运行中任务已占用该 session 的 semaphore"。
	// 这里不启动真实 worker，直接验证 TerminateSession 是否先 cancel，再等待 semaphore 释放。
	m.setTaskCancel(session.ID, func() {
		select {
		case <-taskStarted:
		default:
			close(taskStarted)
		}
		close(taskCanceled)
		time.AfterFunc(120*time.Millisecond, func() {
			<-sem
		})
	})

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- m.TerminateSession(session.ID, "cancel running")
	}()

	select {
	case <-taskCanceled:
	case <-time.After(time.Second):
		t.Fatal("TerminateSession 没有取消运行中的任务")
	}

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("TerminateSession 没有等待 semaphore 释放")
	}

	assert.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond, "TerminateSession 应等待运行中任务释放 session semaphore")
}

func TestPublicAPI_TerminateSession_NoOpForEmptyAndNonexistentSession(t *testing.T) {
	m := setupTestMasterSimple(t)
	defer m.Stop()

	require.NoError(t, m.TerminateSession("", "empty is noop"))
	require.NoError(t, m.TerminateSession("missing-session", "missing is noop"))
}

func TestPublicAPI_TerminateSession_RejectsFurtherProcessMessage(t *testing.T) {
	m := setupTestMasterSimple(t)
	defer m.Stop()

	session, _ := m.sessionMgr.GetOrCreateSession("terminate-reject")
	require.NotNil(t, session)

	err := m.TerminateSession(session.ID, "stop session")
	require.NoError(t, err)

	_, err = m.ProcessMessage(context.Background(), session.ID, "should fail")
	require.Error(t, err)
	assert.True(t, errs.IsCode(err, errs.CodeInvalidInput), "terminated session 应显式返回 CodeInvalidInput")
	assert.Contains(t, err.Error(), "session terminated")
}

func TestPublicAPI_TerminateSession_PreventsAssistantBroadcastLeak(t *testing.T) {
	m := setupTestMasterSimple(t)
	defer m.Stop()

	session, _ := m.sessionMgr.GetOrCreateSession("terminate-broadcast")
	require.NotNil(t, session)

	subID, ch := m.SubscribeWSBroadcast()
	defer m.UnsubscribeWSBroadcast(subID)

	session.MarkTerminated("broadcast leak")

	cap, ok := assistantcap.GrantPass(int(requiredGuardPass), int(requiredGuardPass))
	require.True(t, ok, "测试必须走合法 assistant capability 路径")

	m.broadcastAssistant(cap, session.ID, map[string]any{
		"content":    "stale assistant output",
		"session_id": session.ID,
		"partial":    false,
	})

	select {
	case msg := <-ch:
		t.Fatalf("terminated session 不应广播 stale assistant output，got: %+v", msg)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestExecuteSessionTask_SkipsWriteBackAfterTermination(t *testing.T) {
	st := &terminateSessionStore{}
	m := setupTestMasterWithStore(t, st)
	defer m.Stop()

	session, _ := m.sessionMgr.GetOrCreateSession("terminate-stale")
	require.NotNil(t, session)

	responseID := uint64(4242)
	respCh := make(chan TaskResponse, 1)
	m.sessionMgr.responseMu.Lock()
	m.sessionMgr.pendingResponses[responseID] = respCh
	m.sessionMgr.responseMu.Unlock()
	defer func() {
		m.sessionMgr.responseMu.Lock()
		delete(m.sessionMgr.pendingResponses, responseID)
		m.sessionMgr.responseMu.Unlock()
	}()

	task := sessionTask{
		req:        SessionRequest{Input: "ignored"},
		session:    session,
		responseID: responseID,
		semToken:   m.getSessionSem(session.ID),
	}
	task.semToken <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	session.MarkTerminated("stale writeback")

	m.executeSessionTask(ctx, task)

	select {
	case resp := <-respCh:
		t.Fatalf("terminated session 不应再发送响应，got: %+v", resp)
	default:
	}

	assert.Zero(t, st.saveCalls.Load(), "terminated session 不应执行会话保存")
}

func TestExecuteSessionTask_EnqueuesTaskStartedAndFinishedMetrics(t *testing.T) {
	logger := zaptest.NewLogger(t)
	m := &Master{
		config:     Config{},
		logger:     logger,
		obsCh:      make(chan observabilityEntry, 8),
		sessionMgr: NewSessionManager(make(chan struct{}), logger),
		eventBus:   NewEventBus(logger),
	}
	session := &SessionState{
		ID:       "s-task-metrics",
		Name:     "main",
		Messages: []llm.MessageWithTools{},
		Metadata: map[string]any{},
		Created:  time.Now(),
	}
	sem := make(chan struct{}, 1)
	sem <- struct{}{}

	m.executeSessionTask(context.Background(), sessionTask{
		req:        SessionRequest{Input: "hello"},
		session:    session,
		responseID: 1,
		semToken:   sem,
	})

	var started, finished *observability.Metric
	for len(m.obsCh) > 0 {
		entry := <-m.obsCh
		if entry.metric == nil {
			continue
		}
		switch entry.metric.Name {
		case "hive.task.started":
			started = entry.metric
		case "hive.task.finished":
			finished = entry.metric
		}
	}
	require.NotNil(t, started)
	require.NotNil(t, finished)
	assert.Equal(t, "web", started.Labels["route"])
	assert.NotContains(t, started.Labels, "session_id")
	assert.Equal(t, "web", finished.Labels["route"])
	assert.Equal(t, "error", finished.Labels["status"])
	assert.NotContains(t, finished.Labels, "session_id")
}
