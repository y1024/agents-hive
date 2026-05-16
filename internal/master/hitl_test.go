package master

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/subagent"
	"github.com/chef-guo/agents-hive/internal/toolctx"
)

func setupHITLMaster(t *testing.T, hitlCfg config.HITLConfig) (*Master, context.CancelFunc) {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	skillReg := skills.NewRegistry(logger)
	agentReg := subagent.NewRegistry(logger)

	m := NewMaster(Config{
		Model: "test",
	}, hitlCfg, agentReg, skillReg, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)
	time.Sleep(20 * time.Millisecond)

	return m, cancel
}

// TestHITL_SubmitInput_NotPending tests submitting input for non-existent request.
func TestHITL_SubmitInput_NotPending(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{Enabled: true})
	defer cancel()
	defer m.Stop()

	err := m.SubmitInput(InputResponse{
		RequestID: "nonexistent-input-id",
		TaskID:    "task-123",
		Action:    "approve",
	})
	if err == nil {
		t.Fatal("expected error for non-pending input request")
	}
	if !errs.IsCode(err, errs.CodeInputNotPending) {
		t.Errorf("expected CodeInputNotPending, got %v", err)
	}
}

// TestHITL_PendingInputs tests retrieving pending input requests.
func TestHITL_PendingInputs(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	skillReg := skills.NewRegistry(logger)
	agentReg := subagent.NewRegistry(logger)
	m := NewMaster(Config{}, config.HITLConfig{
		Enabled:      true,
		InputTimeout: 5 * time.Minute,
	}, agentReg, skillReg, nil, logger)

	// Manually register a pending input
	req := m.hitlBroker.RequestInput("task-1", "step-1", InputApproval, "test", []string{"approve"})
	_ = req

	pending := m.PendingInputs("task-1")
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending input, got %d", len(pending))
	}
	if pending[0].TaskID != "task-1" {
		t.Errorf("expected task-1, got %s", pending[0].TaskID)
	}

	// Different task ID should return empty
	pending2 := m.PendingInputs("task-99")
	if len(pending2) != 0 {
		t.Errorf("expected 0 pending inputs for task-99, got %d", len(pending2))
	}
}

// TestHITL_SubmitInput_InvalidAction tests validation of action field.
func TestHITL_SubmitInput_InvalidAction(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{Enabled: true})
	defer cancel()
	defer m.Stop()

	// Manually register a pending input so the action check is reached
	req := m.hitlBroker.RequestInput("task-1", "step-1", InputApproval, "test", []string{"approve"})

	err := m.SubmitInput(InputResponse{
		RequestID: req.ID,
		TaskID:    "task-1",
		Action:    "invalid_action",
	})
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
	if !errs.IsCode(err, errs.CodeInputInvalid) {
		t.Errorf("expected CodeInputInvalid, got %v", err)
	}
}

// TestHITL_SendCommand_InvalidType tests validation of command type.
func TestHITL_SendCommand_InvalidType(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{Enabled: true})
	defer cancel()
	defer m.Stop()

	err := m.SendCommand(UserCommand{
		Type:   UserCommandType("invalid"),
		TaskID: "task-1",
	})
	if err == nil {
		t.Fatal("expected error for invalid command type")
	}
	if !errs.IsCode(err, errs.CodeInputInvalid) {
		t.Errorf("expected CodeInputInvalid, got %v", err)
	}
}

// TestHITL_SubmitInput_TaskMismatch tests validation of task ID matching.
func TestHITL_SubmitInput_TaskMismatch(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{Enabled: true})
	defer cancel()
	defer m.Stop()

	// Register pending input for task-1
	req := m.hitlBroker.RequestInput("task-1", "step-1", InputApproval, "test", []string{"approve"})

	// Submit with mismatched task ID
	err := m.SubmitInput(InputResponse{
		RequestID: req.ID,
		TaskID:    "task-999",
		Action:    "approve",
	})
	if err == nil {
		t.Fatal("expected error for task mismatch")
	}
	if !errs.IsCode(err, errs.CodeInputInvalid) {
		t.Errorf("expected CodeInputInvalid, got %v", err)
	}
}

// TestHITL_WaitForInput_WithResponse tests the per-request channel mechanism.
func TestHITL_WaitForInput_WithResponse(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{
		Enabled:      true,
		InputTimeout: 2 * time.Second,
	})
	defer cancel()
	defer m.Stop()

	// Create a request
	req := m.hitlBroker.RequestInput("task-1", "step-1", InputApproval, "test approval", []string{"approve", "reject"})

	// Submit response in background
	go func() {
		time.Sleep(100 * time.Millisecond)
		err := m.SubmitInput(InputResponse{
			RequestID: req.ID,
			TaskID:    "task-1",
			Action:    "approve",
			Value:     "approved",
		})
		if err != nil {
			t.Errorf("SubmitInput failed: %v", err)
		}
	}()

	// Wait for input
	ctx := context.Background()
	resp, err := m.hitlBroker.WaitForInput(ctx, "task-1", req)
	if err != nil {
		t.Fatalf("waitForInput failed: %v", err)
	}

	if resp.Action != "approve" {
		t.Errorf("expected action 'approve', got %s", resp.Action)
	}
	if resp.Value != "approved" {
		t.Errorf("expected value 'approved', got %s", resp.Value)
	}
}

// TestHITL_WaitForInput_Timeout tests timeout handling.
func TestHITL_WaitForInput_Timeout(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{
		Enabled:      true,
		InputTimeout: 200 * time.Millisecond,
	})
	defer cancel()
	defer m.Stop()

	// Create a request
	req := m.hitlBroker.RequestInput("task-1", "step-1", InputClarification, "test timeout", nil)

	// Don't submit any response - should timeout
	ctx := context.Background()
	_, err := m.hitlBroker.WaitForInput(ctx, "task-1", req)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errs.IsCode(err, errs.CodeInputTimeout) {
		t.Errorf("expected CodeInputTimeout, got %v", err)
	}
}

func TestAskQuestionWithOptionsUsesChoiceRequest(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{
		Enabled:      true,
		InputTimeout: time.Second,
	})
	defer cancel()
	defer m.Stop()

	captured := make(chan *InputRequest, 1)
	captureBroadcast(t, m, captured)

	ctx := toolctx.WithSessionID(context.Background(), "session-choice")
	answerCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		answer, err := m.AskQuestion(ctx, "请选择安装方式", []string{"全局安装", "项目内安装"}, time.Second)
		if err != nil {
			errCh <- err
			return
		}
		answerCh <- answer
	}()

	var req *InputRequest
	select {
	case req = <-captured:
	case <-time.After(time.Second):
		t.Fatal("input_request not broadcast")
	}
	if req.Type != InputChoice {
		t.Fatalf("request type = %s, want %s", req.Type, InputChoice)
	}
	if req.SessionID != "session-choice" || req.TaskID != "session-choice" {
		t.Fatalf("session/task mismatch: session=%q task=%q", req.SessionID, req.TaskID)
	}
	if got := req.Options; len(got) != 2 || got[0] != "全局安装" || got[1] != "项目内安装" {
		t.Fatalf("options = %+v", got)
	}

	if err := m.SubmitInput(InputResponse{
		RequestID: req.ID,
		TaskID:    req.TaskID,
		Action:    "proceed",
		Value:     "项目内安装",
	}); err != nil {
		t.Fatalf("SubmitInput: %v", err)
	}

	select {
	case answer := <-answerCh:
		if answer != "项目内安装" {
			t.Fatalf("answer = %q", answer)
		}
	case err := <-errCh:
		t.Fatalf("AskQuestion returned error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("AskQuestion did not return")
	}
}

// TestHITL_ConcurrentRequests tests multiple concurrent input requests.
func TestHITL_ConcurrentRequests(t *testing.T) {
	m, cancel := setupHITLMaster(t, config.HITLConfig{
		Enabled:      true,
		InputTimeout: 5 * time.Second,
	})
	defer cancel()
	defer m.Stop()

	// Create 3 concurrent requests
	req1 := m.hitlBroker.RequestInput("task-1", "step-1", InputApproval, "req1", nil)
	req2 := m.hitlBroker.RequestInput("task-2", "step-1", InputApproval, "req2", nil)
	req3 := m.hitlBroker.RequestInput("task-3", "step-1", InputApproval, "req3", nil)

	// Submit responses in background
	go func() {
		time.Sleep(50 * time.Millisecond)
		m.SubmitInput(InputResponse{RequestID: req1.ID, TaskID: "task-1", Action: "approve", Value: "r1"})
		m.SubmitInput(InputResponse{RequestID: req2.ID, TaskID: "task-2", Action: "approve", Value: "r2"})
		m.SubmitInput(InputResponse{RequestID: req3.ID, TaskID: "task-3", Action: "approve", Value: "r3"})
	}()

	// Wait for all responses in parallel
	ctx := context.Background()
	done := make(chan struct{}, 3)

	go func() {
		resp, err := m.hitlBroker.WaitForInput(ctx, "task-1", req1)
		if err != nil || resp.Value != "r1" {
			t.Errorf("req1 failed: err=%v, value=%s", err, resp.Value)
		}
		done <- struct{}{}
	}()

	go func() {
		resp, err := m.hitlBroker.WaitForInput(ctx, "task-2", req2)
		if err != nil || resp.Value != "r2" {
			t.Errorf("req2 failed: err=%v, value=%s", err, resp.Value)
		}
		done <- struct{}{}
	}()

	go func() {
		resp, err := m.hitlBroker.WaitForInput(ctx, "task-3", req3)
		if err != nil || resp.Value != "r3" {
			t.Errorf("req3 failed: err=%v, value=%s", err, resp.Value)
		}
		done <- struct{}{}
	}()

	// Wait for all to complete
	timeout := time.After(3 * time.Second)
	for i := 0; i < 3; i++ {
		select {
		case <-done:
		case <-timeout:
			t.Fatal("timeout waiting for concurrent requests")
		}
	}
}

// TestInputRequest_JSON_BackwardCompat asserts that adding the optional
// ChoiceType field does NOT alter serialized bytes when ChoiceType is empty.
// Any drift here breaks the IM/Web event payload contract.
func TestInputRequest_JSON_BackwardCompat(t *testing.T) {
	fixed := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	req := InputRequest{
		ID:        "req-1",
		TaskID:    "task-1",
		Type:      InputApproval,
		Prompt:    "proceed?",
		CreatedAt: fixed,
	}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"id":"req-1","task_id":"task-1","type":"approval","prompt":"proceed?","created_at":"2026-04-17T10:00:00Z"}`
	if string(got) != want {
		t.Fatalf("backward-compat drift:\n got: %s\nwant: %s", got, want)
	}
}

// TestInputRequest_JSON_WithChoiceType verifies ChoiceType emits when set.
func TestInputRequest_JSON_WithChoiceType(t *testing.T) {
	fixed := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	req := InputRequest{
		ID:         "req-2",
		TaskID:     "task-2",
		Type:       InputChoice,
		Prompt:     "pick account",
		ChoiceType: "account_selector",
		CreatedAt:  fixed,
	}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"id":"req-2","task_id":"task-2","type":"choice","prompt":"pick account","choice_type":"account_selector","created_at":"2026-04-17T10:00:00Z"}`
	if string(got) != want {
		t.Fatalf("choice_type payload mismatch:\n got: %s\nwant: %s", got, want)
	}
}
