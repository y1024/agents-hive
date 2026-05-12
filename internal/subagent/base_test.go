package subagent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/toolctx"
)

func testLogger() *zap.Logger {
	l, _ := zap.NewDevelopment()
	return l
}

func testSkillReg() *skills.Registry {
	return skills.NewRegistry(testLogger())
}

func echoHandler(ctx context.Context, req TaskRequest) TaskResponse {
	return TaskResponse{
		Status: "completed",
		Result: req.Payload,
	}
}

func TestBaseAgent_RunAndSendTask(t *testing.T) {
	card := AgentCard{ID: "test-agent", Name: "Test Agent"}
	agent := NewBaseAgent(card, echoHandler, testSkillReg(), testLogger())

	ctx, cancel := context.WithCancel(context.Background())

	// 用 done channel 等待 goroutine 退出，避免 logger 竞态
	done := make(chan struct{})
	go func() {
		agent.Run(ctx)
		close(done)
	}()
	defer func() { cancel(); <-done }()
	time.Sleep(10 * time.Millisecond) // let the agent start

	payload := json.RawMessage(`{"hello":"world"}`)
	resp, err := agent.SendTask(ctx, TaskRequest{ID: "req-1", Type: "echo", Payload: payload})
	if err != nil {
		t.Fatalf("SendTask error: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("expected status completed, got %s", resp.Status)
	}
	if string(resp.Result) != string(payload) {
		t.Errorf("expected result %s, got %s", payload, resp.Result)
	}
	if resp.AgentID != "test-agent" {
		t.Errorf("expected agent_id test-agent, got %s", resp.AgentID)
	}
}

func TestBaseAgent_HandlerReceivesRequestScopedContext(t *testing.T) {
	card := AgentCard{ID: "ctx-agent", Name: "Context Agent"}
	seen := make(chan struct {
		sessionID string
		userID    string
		toolCtx   toolctx.ToolContext
	}, 1)
	agent := NewBaseAgent(card, func(ctx context.Context, req TaskRequest) TaskResponse {
		seen <- struct {
			sessionID string
			userID    string
			toolCtx   toolctx.ToolContext
		}{
			sessionID: toolctx.GetSessionID(ctx),
			userID:    auth.UserIDFrom(ctx),
			toolCtx:   *toolctx.GetToolContext(ctx),
		}
		return TaskResponse{Status: "completed"}
	}, testSkillReg(), testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agent.Run(ctx)
		close(done)
	}()
	defer func() { cancel(); <-done }()
	time.Sleep(10 * time.Millisecond)

	_, err := agent.SendTask(ctx, TaskRequest{
		ID:           "req-context",
		Type:         "test",
		SessionID:    "sess-req",
		UserID:       "user-req",
		TraceID:      "trace-child",
		ParentSpanID: "span-parent",
		TurnID:       "turn-req",
		ToolCallID:   "call-parent",
	})
	if err != nil {
		t.Fatalf("SendTask error: %v", err)
	}

	got := <-seen
	if got.sessionID != "sess-req" {
		t.Fatalf("sessionID = %q, want sess-req", got.sessionID)
	}
	if got.userID != "user-req" {
		t.Fatalf("userID = %q, want user-req", got.userID)
	}
	if got.toolCtx.TraceID != "trace-child" {
		t.Fatalf("traceID = %q, want trace-child", got.toolCtx.TraceID)
	}
	if got.toolCtx.ParentSpanID != "span-parent" {
		t.Fatalf("parentSpanID = %q, want span-parent", got.toolCtx.ParentSpanID)
	}
	if got.toolCtx.TurnID != "turn-req" {
		t.Fatalf("turnID = %q, want turn-req", got.toolCtx.TurnID)
	}
	if got.toolCtx.ToolCallID != "call-parent" {
		t.Fatalf("toolCallID = %q, want call-parent", got.toolCtx.ToolCallID)
	}
}

func TestBaseAgent_Ping(t *testing.T) {
	card := AgentCard{ID: "ping-agent", Name: "Ping Agent"}
	agent := NewBaseAgent(card, echoHandler, testSkillReg(), testLogger())

	ctx, cancel := context.WithCancel(context.Background())

	// 用 done channel 等待 goroutine 退出，避免 logger 竞态
	done := make(chan struct{})
	go func() {
		agent.Run(ctx)
		close(done)
	}()
	defer func() { cancel(); <-done }()
	time.Sleep(10 * time.Millisecond)

	status, err := agent.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping error: %v", err)
	}
	if status.AgentID != "ping-agent" {
		t.Errorf("expected agent_id ping-agent, got %s", status.AgentID)
	}
	if status.Status != StatusRunning {
		t.Errorf("expected status running, got %s", status.Status.String())
	}
}

func TestBaseAgent_Stop(t *testing.T) {
	card := AgentCard{ID: "stop-agent", Name: "Stop Agent"}
	agent := NewBaseAgent(card, echoHandler, testSkillReg(), testLogger())

	// 使用可取消的 ctx，确保 defer 可以触发 Run 退出
	ctx, cancel := context.WithCancel(context.Background())

	// 用 done channel 等待 goroutine 退出，避免 logger 竞态
	done := make(chan struct{})
	go func() {
		agent.Run(ctx)
		close(done)
	}()
	defer func() { cancel(); <-done }()
	time.Sleep(10 * time.Millisecond)

	agent.Stop()
	time.Sleep(10 * time.Millisecond)

	if agent.Status() != StatusStopped {
		t.Errorf("expected status stopped, got %s", agent.Status().String())
	}
}

func TestBaseAgent_ContextCancelDuringResponseSend(t *testing.T) {
	// 测试场景：context 先被取消，然后 handler 完成并尝试写入 unbuffered replyCh。
	// Go select 在多个 case 就绪时随机选择，所以可能收到正常响应或 error 响应。
	// 关键验证：agent 不会永远阻塞，replyCh 一定能收到某个响应。
	card := AgentCard{ID: "cancel-agent", Name: "Cancel Agent"}

	handlerStarted := make(chan struct{})
	handlerBlock := make(chan struct{})
	slowHandler := func(ctx context.Context, req TaskRequest) TaskResponse {
		close(handlerStarted)
		<-handlerBlock
		return TaskResponse{Status: "completed"}
	}

	agent := NewBaseAgent(card, slowHandler, testSkillReg(), testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agent.Run(ctx)
		close(done)
	}()

	// 等待 agent 进入 Running 状态（比 time.Sleep 更可靠）
	for i := 0; i < 200; i++ {
		if agent.Status() == StatusRunning {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if agent.Status() != StatusRunning {
		t.Fatal("agent did not start in time")
	}

	replyCh := make(chan TaskResponse) // unbuffered

	agent.Mailbox().Request <- TaskEnvelope{
		Req:     TaskRequest{ID: "cancel-req-1", Type: "test"},
		ReplyCh: replyCh,
	}

	<-handlerStarted
	cancel()
	close(handlerBlock)

	// 关键断言：replyCh 一定能收到响应（不会永远阻塞）
	select {
	case resp := <-replyCh:
		if resp.Status != "completed" && resp.Status != "failed" {
			t.Errorf("expected status completed or failed, got %s", resp.Status)
		}
		if resp.Status == "failed" && resp.Error != "context canceled" {
			t.Errorf("expected error 'context canceled', got %q", resp.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response on replyCh — agent is blocked")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not exit after context cancel")
	}
}

func TestBaseAgent_SendTaskWhenNotRunning(t *testing.T) {
	card := AgentCard{ID: "idle-agent", Name: "Idle Agent"}
	agent := NewBaseAgent(card, echoHandler, testSkillReg(), testLogger())

	_, err := agent.SendTask(context.Background(), TaskRequest{ID: "req-1"})
	if err == nil {
		t.Fatal("expected error when sending to stopped agent")
	}
	if !errs.IsCode(err, errs.CodeAgentUnavailable) {
		t.Errorf("expected CodeAgentUnavailable, got %v", err)
	}
}
