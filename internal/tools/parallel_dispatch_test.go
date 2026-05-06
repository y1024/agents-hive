package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/mcphost"
)

// mockBroadcaster 用于测试的广播器（并发安全）
type mockBroadcaster struct {
	mu             sync.Mutex
	groupEvents    []map[string]interface{}
	progressEvents []map[string]interface{}
}

func (m *mockBroadcaster) BroadcastTaskGroup(groupID string, status string, total int, tasks interface{}, results interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.groupEvents = append(m.groupEvents, map[string]interface{}{
		"group_id": groupID,
		"status":   status,
		"total":    total,
	})
}

func (m *mockBroadcaster) BroadcastTaskProgress(groupID string, taskID string, status string, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.progressEvents = append(m.progressEvents, map[string]interface{}{
		"group_id": groupID,
		"task_id":  taskID,
		"status":   status,
		"error":    errMsg,
	})
}

func TestParallelDispatch(t *testing.T) {
	tests := []struct {
		name          string
		callerType    CallerType
		input         interface{}
		executeFn     func(ctx context.Context, agentID string, instruction string, taskContext map[string]interface{}) (string, error)
		wantError     bool
		wantCompleted int
		wantFailed    int
		errContains   string
	}{
		{
			name:       "正常并行执行3个任务",
			callerType: CallerMaster,
			input: parallelDispatchInput{
				Tasks: []parallelTaskItem{
					{ID: "t1", AgentID: "research", Instruction: "分析代码"},
					{ID: "t2", AgentID: "general", Instruction: "写文档"},
					{ID: "t3", AgentID: "explore", Instruction: "审查代码"},
				},
			},
			executeFn: func(ctx context.Context, agentID string, instruction string, taskContext map[string]interface{}) (string, error) {
				return fmt.Sprintf("任务完成: %s - %s", agentID, instruction), nil
			},
			wantError:     false,
			wantCompleted: 3,
			wantFailed:    0,
		},
		{
			name:       "部分任务失败不影响其他",
			callerType: CallerMaster,
			input: parallelDispatchInput{
				Tasks: []parallelTaskItem{
					{ID: "t1", AgentID: "research", Instruction: "成功任务"},
					{ID: "t2", AgentID: "broken", Instruction: "失败任务"},
					{ID: "t3", AgentID: "general", Instruction: "另一个成功任务"},
				},
			},
			executeFn: func(ctx context.Context, agentID string, instruction string, taskContext map[string]interface{}) (string, error) {
				if agentID == "broken" {
					return "", errs.New(errs.CodeAgentNotFound, "agent not found")
				}
				return "成功", nil
			},
			wantError:     false,
			wantCompleted: 2,
			wantFailed:    1,
		},
		{
			name:       "超过最大任务数限制",
			callerType: CallerMaster,
			input: func() parallelDispatchInput {
				tasks := make([]parallelTaskItem, 11)
				for i := range tasks {
					tasks[i] = parallelTaskItem{
						ID:          fmt.Sprintf("t%d", i),
						Instruction: fmt.Sprintf("任务 %d", i),
					}
				}
				return parallelDispatchInput{Tasks: tasks}
			}(),
			wantError:   true,
			errContains: "任务数超过最大限制",
		},
		{
			name:       "非授权调用者拒绝",
			callerType: CallerSubAgent,
			input: parallelDispatchInput{
				Tasks: []parallelTaskItem{
					{Instruction: "测试"},
				},
			},
			wantError:   true,
			errContains: "仅允许 Master Agent 和固定 Agent 调用",
		},
		{
			name:       "空任务列表拒绝",
			callerType: CallerMaster,
			input: parallelDispatchInput{
				Tasks: []parallelTaskItem{},
			},
			wantError:   true,
			errContains: "tasks 不能为空",
		},
		{
			name:       "agent_id为空时返回错误",
			callerType: CallerMaster,
			input: parallelDispatchInput{
				Tasks: []parallelTaskItem{
					{Instruction: "无ID任务1"},
					{Instruction: "无ID任务2"},
				},
			},
			wantError:   true,
			errContains: "agent_id 不能为空",
		},
		{
			name:       "超过最大深度限制",
			callerType: CallerMaster,
			input: parallelDispatchInput{
				Tasks: []parallelTaskItem{
					{Instruction: "测试"},
				},
			},
			wantError:   true,
			errContains: "调用深度超过最大限制",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()
			host := mcphost.NewHost(logger)
			broadcaster := &mockBroadcaster{}

			executor := &mockTaskExecutor{executeFn: tt.executeFn}
			registerParallelDispatch(host, executor, broadcaster, logger, nil)

			// 构造调用上下文
			depth := 0
			if tt.name == "超过最大深度限制" {
				depth = maxDepth
			}
			ctx := WithToolContext(context.Background(), &ToolContext{
				CallerType: tt.callerType,
				CallerName: string(tt.callerType),
				Depth:      depth,
			})

			inputJSON, _ := json.Marshal(tt.input)
			result, err := host.ExecuteTool(ctx, "parallel_dispatch", inputJSON)
			if err != nil {
				t.Fatalf("调用工具失败: %v", err)
			}

			if tt.wantError {
				if !result.IsError {
					t.Fatalf("期望返回错误，但成功了: %s", string(result.Content))
				}
				var errMsg string
				json.Unmarshal(result.Content, &errMsg)
				if tt.errContains != "" {
					found := false
					if contains(errMsg, tt.errContains) {
						found = true
					}
					if !found {
						t.Errorf("错误消息 %q 不包含 %q", errMsg, tt.errContains)
					}
				}
				return
			}

			if result.IsError {
				t.Fatalf("工具返回错误: %s", string(result.Content))
			}

			// 解析返回结果
			var content string
			json.Unmarshal(result.Content, &content)

			var resultData struct {
				GroupID   string               `json:"group_id"`
				Total     int                  `json:"total"`
				Completed int                  `json:"completed"`
				Failed    int                  `json:"failed"`
				Results   []parallelTaskResult `json:"results"`
			}
			if err := json.Unmarshal([]byte(content), &resultData); err != nil {
				t.Fatalf("解析结果 JSON 失败: %v, content: %s", err, content)
			}

			if resultData.Completed != tt.wantCompleted {
				t.Errorf("期望完成 %d 个任务, 实际 %d", tt.wantCompleted, resultData.Completed)
			}
			if resultData.Failed != tt.wantFailed {
				t.Errorf("期望失败 %d 个任务, 实际 %d", tt.wantFailed, resultData.Failed)
			}

			// 验证广播事件
			if len(broadcaster.groupEvents) < 2 {
				t.Errorf("期望至少 2 个组事件（started + completed），实际 %d", len(broadcaster.groupEvents))
			}
		})
	}
}

func TestParallelDispatch_ConcurrencyLimit(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	var running int32
	var maxRunning int32

	executor := &mockTaskExecutor{
		executeFn: func(ctx context.Context, agentID string, instruction string, taskContext map[string]interface{}) (string, error) {
			current := atomic.AddInt32(&running, 1)
			// 记录最大并发数
			for {
				old := atomic.LoadInt32(&maxRunning)
				if current <= old || atomic.CompareAndSwapInt32(&maxRunning, old, current) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond) // 模拟执行时间
			atomic.AddInt32(&running, -1)
			return "完成", nil
		},
	}

	registerParallelDispatch(host, executor, nil, logger, nil)

	ctx := WithToolContext(context.Background(), &ToolContext{
		CallerType: CallerMaster,
		CallerName: "master",
		Depth:      0,
	})

	// 5 个任务，并发限制为 2
	input := parallelDispatchInput{
		Tasks: []parallelTaskItem{
			{AgentID: "explore", Instruction: "任务1"},
			{AgentID: "explore", Instruction: "任务2"},
			{AgentID: "explore", Instruction: "任务3"},
			{AgentID: "explore", Instruction: "任务4"},
			{AgentID: "explore", Instruction: "任务5"},
		},
		MaxConcurrency: 2,
	}

	inputJSON, _ := json.Marshal(input)
	result, err := host.ExecuteTool(ctx, "parallel_dispatch", inputJSON)
	if err != nil {
		t.Fatalf("调用工具失败: %v", err)
	}
	if result.IsError {
		t.Fatalf("工具返回错误: %s", string(result.Content))
	}

	// 验证最大并发数不超过限制
	maxConcurrent := atomic.LoadInt32(&maxRunning)
	if maxConcurrent > 2 {
		t.Errorf("最大并发数 %d 超过限制 2", maxConcurrent)
	}
}

func TestParallelDispatch_Registration(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	executor := &mockTaskExecutor{}
	registerParallelDispatch(host, executor, nil, logger, nil)

	// 验证工具已注册
	tools := host.ListTools()
	found := false
	for _, tool := range tools {
		if tool.Name == "parallel_dispatch" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("parallel_dispatch 工具未注册")
	}
}

func TestParallelDispatch_TaskTimeout(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	executor := &mockTaskExecutor{
		executeFn: func(ctx context.Context, agentID string, instruction string, taskContext map[string]interface{}) (string, error) {
			// 模拟慢任务：等待 context 取消或超时
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(10 * time.Second):
				return "完成", nil
			}
		},
	}

	registerParallelDispatch(host, executor, nil, logger, nil)

	ctx := WithToolContext(context.Background(), &ToolContext{
		CallerType: CallerMaster,
		CallerName: "master",
		Depth:      0,
	})

	// 超时设为 1 秒，任务需要 10 秒
	input := parallelDispatchInput{
		Tasks: []parallelTaskItem{
			{ID: "slow-1", AgentID: "explore", Instruction: "慢任务"},
		},
		TimeoutSeconds: 1,
	}

	inputJSON, _ := json.Marshal(input)
	result, err := host.ExecuteTool(ctx, "parallel_dispatch", inputJSON)
	if err != nil {
		t.Fatalf("调用工具失败: %v", err)
	}
	if result.IsError {
		t.Fatalf("工具返回错误: %s", string(result.Content))
	}

	// 解析结果：应该有 1 个失败任务（超时）
	var content string
	json.Unmarshal(result.Content, &content)
	var resultData struct {
		Failed  int                  `json:"failed"`
		Results []parallelTaskResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(content), &resultData); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	if resultData.Failed != 1 {
		t.Errorf("期望 1 个失败任务（超时），实际 %d", resultData.Failed)
	}
}

func TestParallelDispatch_ContextCancellation(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	var started int32
	executor := &mockTaskExecutor{
		executeFn: func(ctx context.Context, agentID string, instruction string, taskContext map[string]interface{}) (string, error) {
			atomic.AddInt32(&started, 1)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(30 * time.Second):
				return "完成", nil
			}
		},
	}

	registerParallelDispatch(host, executor, nil, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithToolContext(ctx, &ToolContext{
		CallerType: CallerMaster,
		CallerName: "master",
		Depth:      0,
	})

	input := parallelDispatchInput{
		Tasks: []parallelTaskItem{
			{AgentID: "explore", Instruction: "任务1"},
			{AgentID: "explore", Instruction: "任务2"},
		},
	}

	// 200ms 后取消 context
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	inputJSON, _ := json.Marshal(input)
	result, err := host.ExecuteTool(ctx, "parallel_dispatch", inputJSON)
	if err != nil {
		// context 取消可能直接返回 error，这也是预期行为
		return
	}
	if result.IsError {
		// 也是预期行为
		return
	}

	// 如果正常返回结果，所有任务应该都是失败的（被取消）
	var content string
	json.Unmarshal(result.Content, &content)
	var resultData struct {
		Completed int `json:"completed"`
		Failed    int `json:"failed"`
	}
	if err := json.Unmarshal([]byte(content), &resultData); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	if resultData.Completed > 0 {
		t.Errorf("取消后不应有完成的任务，实际完成 %d 个", resultData.Completed)
	}
}

func TestParallelDispatch_TimeoutUpperBound(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	var receivedCtx context.Context
	executor := &mockTaskExecutor{
		executeFn: func(ctx context.Context, agentID string, instruction string, taskContext map[string]interface{}) (string, error) {
			receivedCtx = ctx
			return "完成", nil
		},
	}

	registerParallelDispatch(host, executor, nil, logger, nil)

	ctx := WithToolContext(context.Background(), &ToolContext{
		CallerType: CallerMaster,
		CallerName: "master",
		Depth:      0,
	})

	// 设置超时为 1 小时（超过 30 分钟上限）
	input := parallelDispatchInput{
		Tasks: []parallelTaskItem{
			{AgentID: "explore", Instruction: "测试"},
		},
		TimeoutSeconds: 3600, // 1 小时
	}

	inputJSON, _ := json.Marshal(input)
	result, err := host.ExecuteTool(ctx, "parallel_dispatch", inputJSON)
	if err != nil {
		t.Fatalf("调用工具失败: %v", err)
	}
	if result.IsError {
		t.Fatalf("工具返回错误: %s", string(result.Content))
	}

	// 验证任务完成了（上限截断不影响正常任务）
	var content string
	json.Unmarshal(result.Content, &content)
	var resultData struct {
		Completed int `json:"completed"`
	}
	json.Unmarshal([]byte(content), &resultData)
	if resultData.Completed != 1 {
		t.Errorf("期望 1 个完成任务，实际 %d", resultData.Completed)
	}

	// 验证 context 有 deadline 且不超过 30 分钟
	if receivedCtx != nil {
		deadline, ok := receivedCtx.Deadline()
		if ok {
			remaining := time.Until(deadline)
			if remaining > 31*time.Minute {
				t.Errorf("超时应被截断到 30 分钟，实际 %v", remaining)
			}
		}
	}
}

func TestParallelDispatch_InvalidJSON(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	executor := &mockTaskExecutor{}
	registerParallelDispatch(host, executor, nil, logger, nil)

	ctx := WithToolContext(context.Background(), &ToolContext{
		CallerType: CallerMaster,
		CallerName: "master",
		Depth:      0,
	})

	input := json.RawMessage(`{"invalid json`)
	result, err := host.ExecuteTool(ctx, "parallel_dispatch", input)
	if err != nil {
		t.Fatalf("调用工具失败: %v", err)
	}
	if !result.IsError {
		t.Fatal("期望返回 JSON 解析错误，但成功了")
	}
}

func TestParallelDispatch_SystemAgentDenyList(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	called := false
	executor := &mockTaskExecutor{
		executeFn: func(ctx context.Context, agentID string, instruction string, taskContext map[string]interface{}) (string, error) {
			called = true
			return "不应该执行", nil
		},
	}
	registerParallelDispatch(host, executor, nil, logger, nil)

	ctx := WithToolContext(context.Background(), &ToolContext{
		CallerType: CallerMaster,
		CallerName: "master",
		Depth:      0,
	})

	// 测试系统 Agent 被拒绝
	input, _ := json.Marshal(parallelDispatchInput{
		Tasks: []parallelTaskItem{
			{AgentID: "compaction", Instruction: "测试"},
		},
	})

	result, err := host.ExecuteTool(ctx, "parallel_dispatch", input)
	if err != nil {
		t.Fatalf("调用工具失败: %v", err)
	}
	if !result.IsError {
		t.Fatal("系统 Agent compaction 应被拒绝，但成功了")
	}
	if called {
		t.Fatal("系统 Agent 不应触发 ExecuteTask")
	}
}

func TestParallelDispatch_NestedToolGateRejectsFixedAgentBeforeDispatch(t *testing.T) {
	logger := zap.NewNop()
	host := mcphost.NewHost(logger)

	called := false
	executor := &mockTaskExecutor{
		executeFn: func(ctx context.Context, agentID string, instruction string, taskContext map[string]interface{}) (string, error) {
			called = true
			return "不应该执行", nil
		},
	}
	gate := &testNestedToolGate{denyTool: "parallel_dispatch"}
	registerParallelDispatch(host, executor, nil, logger, nil, gate)

	ctx := WithToolContext(context.Background(), &ToolContext{
		CallerType: CallerFixedAgent,
		CallerName: "fixed-general",
		Depth:      0,
	})
	input, _ := json.Marshal(parallelDispatchInput{
		Tasks: []parallelTaskItem{
			{AgentID: "explore", Instruction: "测试 plan mode gate"},
		},
	})

	result, err := host.ExecuteTool(ctx, "parallel_dispatch", input)

	if err != nil {
		t.Fatalf("调用工具失败: %v", err)
	}
	if !result.IsError {
		t.Fatalf("期望 plan mode gate 拒绝 parallel_dispatch，实际成功: %s", string(result.Content))
	}
	if called {
		t.Fatal("plan mode gate 拒绝后不应执行任何并行任务")
	}
	var errMsg string
	_ = json.Unmarshal(result.Content, &errMsg)
	if !contains(errMsg, "plan mode gate denied") {
		t.Fatalf("错误消息 %q 不包含 plan mode gate denied", errMsg)
	}
}
