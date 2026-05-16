package accounting_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/accounting"
)

// setupTestDB 连接测试 PG，建 usage_records 表，返回 pool 和清理函数。
// 需要环境变量 TEST_DATABASE_URL，否则跳过。
func setupTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL 未设置，跳过 PG 集成测试")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS usage_records (
			id               BIGSERIAL PRIMARY KEY,
			session_id       TEXT NOT NULL,
			user_id          TEXT NOT NULL DEFAULT '',
			model            TEXT NOT NULL,
			prompt_tokens    BIGINT NOT NULL DEFAULT 0,
			completion_tokens BIGINT NOT NULL DEFAULT 0,
			cost_usd         DOUBLE PRECISION NOT NULL DEFAULT 0,
			task_type        TEXT NOT NULL DEFAULT '',
			quality_case_id  TEXT NOT NULL DEFAULT '',
			prompt_version   TEXT NOT NULL DEFAULT '',
			failure_type     TEXT NOT NULL DEFAULT '',
			final_status     TEXT NOT NULL DEFAULT '',
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS failure_type TEXT NOT NULL DEFAULT '';
		ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS final_status TEXT NOT NULL DEFAULT '';
	`)
	require.NoError(t, err)

	cleanup := func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM usage_records")
		pool.Close()
	}
	return pool, cleanup
}

func TestPgTracker_Record(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := accounting.NewPgTracker(pool, nil)
	ctx := context.Background()

	entry := accounting.UsageEntry{
		SessionID:        "sess-1",
		Model:            "gpt-5",
		PromptTokens:     100,
		CompletionTokens: 50,
		CostUSD:          0.0025,
	}
	require.NoError(t, tracker.Record(ctx, entry))

	// 验证写入
	summary, err := tracker.GetSessionCost(ctx, "sess-1")
	require.NoError(t, err)
	assert.Equal(t, int64(100), summary.TotalPromptTokens)
	assert.Equal(t, int64(50), summary.TotalCompletionTokens)
	assert.InDelta(t, 0.0025, summary.TotalCostUSD, 1e-9)
	assert.Equal(t, int64(1), summary.RequestCount)
}

func TestPgTracker_GetTotalCost_EmptyResult(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := accounting.NewPgTracker(pool, nil)
	ctx := context.Background()

	summary, err := tracker.GetTotalCost(ctx, accounting.CostFilter{SessionID: "nonexistent"})
	require.NoError(t, err)
	assert.Equal(t, float64(0), summary.TotalCostUSD)
	assert.Equal(t, int64(0), summary.RequestCount)
	assert.Empty(t, summary.ByModel)
}

func TestPgTracker_GetTotalCost_ByModel(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := accounting.NewPgTracker(pool, nil)
	ctx := context.Background()

	entries := []accounting.UsageEntry{
		{SessionID: "sess-2", Model: "gpt-5", PromptTokens: 100, CompletionTokens: 50, CostUSD: 0.001},
		{SessionID: "sess-2", Model: "gpt-5", PromptTokens: 200, CompletionTokens: 100, CostUSD: 0.002},
		{SessionID: "sess-2", Model: "claude-3-5-sonnet", PromptTokens: 300, CompletionTokens: 150, CostUSD: 0.003},
	}
	for _, e := range entries {
		require.NoError(t, tracker.Record(ctx, e))
	}

	summary, err := tracker.GetTotalCost(ctx, accounting.CostFilter{SessionID: "sess-2"})
	require.NoError(t, err)

	// Total 字段由 Go 侧聚合，与 ByModel 之和一致
	assert.Equal(t, int64(3), summary.RequestCount)
	assert.InDelta(t, 0.006, summary.TotalCostUSD, 1e-9)
	assert.Equal(t, int64(600), summary.TotalPromptTokens)
	assert.Equal(t, int64(300), summary.TotalCompletionTokens)

	// ByModel 明细
	require.Contains(t, summary.ByModel, "gpt-5")
	require.Contains(t, summary.ByModel, "claude-3-5-sonnet")
	assert.Equal(t, int64(2), summary.ByModel["gpt-5"].RequestCount)
	assert.Equal(t, int64(1), summary.ByModel["claude-3-5-sonnet"].RequestCount)

	// Total == sum(ByModel)
	var sumCost float64
	for _, mc := range summary.ByModel {
		sumCost += mc.CostUSD
	}
	assert.InDelta(t, summary.TotalCostUSD, sumCost, 1e-9)
}

func TestPgTracker_GetTotalCost_ConsistencyUnderConcurrentWrites(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := accounting.NewPgTracker(pool, nil)
	ctx := context.Background()

	// 并发写入 50 条记录
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = tracker.Record(ctx, accounting.UsageEntry{
				SessionID:    "sess-concurrent",
				Model:        "gpt-5",
				PromptTokens: 10, CompletionTokens: 5, CostUSD: 0.0001,
			})
		}()
	}
	wg.Wait()

	summary, err := tracker.GetTotalCost(ctx, accounting.CostFilter{SessionID: "sess-concurrent"})
	require.NoError(t, err)

	// 单次查询，TotalCostUSD 必须等于 ByModel 之和
	var sumCost float64
	for _, mc := range summary.ByModel {
		sumCost += mc.CostUSD
	}
	assert.InDelta(t, summary.TotalCostUSD, sumCost, 1e-9, "TotalCostUSD 与 ByModel 之和不一致")
	assert.Equal(t, summary.RequestCount, summary.ByModel["gpt-5"].RequestCount)
}

func TestPgTracker_GetSessionCost_IsolatesSession(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := accounting.NewPgTracker(pool, nil)
	ctx := context.Background()

	require.NoError(t, tracker.Record(ctx, accounting.UsageEntry{SessionID: "sess-A", Model: "m", PromptTokens: 100, CostUSD: 0.001}))
	require.NoError(t, tracker.Record(ctx, accounting.UsageEntry{SessionID: "sess-B", Model: "m", PromptTokens: 200, CostUSD: 0.002}))

	summaryA, err := tracker.GetSessionCost(ctx, "sess-A")
	require.NoError(t, err)
	assert.Equal(t, int64(100), summaryA.TotalPromptTokens)

	summaryB, err := tracker.GetSessionCost(ctx, "sess-B")
	require.NoError(t, err)
	assert.Equal(t, int64(200), summaryB.TotalPromptTokens)
}

func TestPgTracker_GetQualityCost_ByFailureAndStatus(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := accounting.NewPgTracker(pool, nil)
	ctx := context.Background()

	entries := []accounting.UsageEntry{
		{SessionID: "sess-q1", Model: "m", PromptTokens: 100, CompletionTokens: 20, CostUSD: 0.001, TaskType: "react", QualityCaseID: "aq01", PromptVersion: "p1", FailureType: "tool", FinalStatus: "fail"},
		{SessionID: "sess-q2", Model: "m", PromptTokens: 50, CompletionTokens: 10, CostUSD: 0.002, TaskType: "react", QualityCaseID: "aq02", PromptVersion: "p1", FailureType: "tool", FinalStatus: "fail"},
		{SessionID: "sess-q3", Model: "m", PromptTokens: 30, CompletionTokens: 5, CostUSD: 0.003, TaskType: "subagent", QualityCaseID: "aq03", PromptVersion: "p2", FailureType: "permission", FinalStatus: "needs_user"},
	}
	for _, entry := range entries {
		require.NoError(t, tracker.Record(ctx, entry))
	}

	summary, err := tracker.GetQualityCost(ctx)
	require.NoError(t, err)

	require.Contains(t, summary.ByFailureType, "tool")
	assert.Equal(t, int64(180), summary.ByFailureType["tool"].Tokens)
	assert.Equal(t, int64(2), summary.ByFailureType["tool"].RequestCount)
	assert.InDelta(t, 0.003, summary.ByFailureType["tool"].CostUSD, 1e-9)

	require.Contains(t, summary.ByFinalStatus, "fail")
	assert.Equal(t, int64(180), summary.ByFinalStatus["fail"].Tokens)
	assert.Equal(t, int64(2), summary.ByFinalStatus["fail"].RequestCount)
	require.Contains(t, summary.ByFinalStatus, "needs_user")
	assert.Equal(t, int64(35), summary.ByFinalStatus["needs_user"].Tokens)
}

func TestPgTracker_Cleanup_RetentionDaysValidation(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := accounting.NewPgTracker(pool, nil)
	ctx := context.Background()

	_, err := tracker.Cleanup(ctx, 0)
	assert.Error(t, err, "retentionDays=0 应返回 error")

	_, err = tracker.Cleanup(ctx, -1)
	assert.Error(t, err, "retentionDays=-1 应返回 error")
}

func TestPgTracker_Cleanup_DeletesOldRecords(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := accounting.NewPgTracker(pool, nil)
	ctx := context.Background()

	// 直接插入一条 created_at 为 100 天前的记录
	_, err := pool.Exec(ctx,
		`INSERT INTO usage_records (session_id, model, prompt_tokens, completion_tokens, cost_usd, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		"sess-old", "m", 10, 5, 0.001, time.Now().Add(-100*24*time.Hour),
	)
	require.NoError(t, err)

	// 插入一条新记录
	require.NoError(t, tracker.Record(ctx, accounting.UsageEntry{SessionID: "sess-new", Model: "m", PromptTokens: 10, CostUSD: 0.001}))

	// 清理 90 天前的记录，应删除 1 条
	deleted, err := tracker.Cleanup(ctx, 90)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// 新记录仍在
	summary, err := tracker.GetSessionCost(ctx, "sess-new")
	require.NoError(t, err)
	assert.Equal(t, int64(1), summary.RequestCount)
}

func TestAsyncRecorder_SubmitAndStop(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := accounting.NewPgTracker(pool, nil)
	ctx := context.Background()

	// 通过 AsyncRecorder 提交 10 条
	rec := accounting.NewAsyncRecorder(tracker, nil)
	for i := 0; i < 10; i++ {
		rec.Submit(accounting.UsageEntry{
			SessionID: "sess-async", Model: "gpt-5",
			PromptTokens: 10, CompletionTokens: 5, CostUSD: 0.0001,
		})
	}
	rec.Stop() // 等待 worker 写完

	summary, err := tracker.GetSessionCost(ctx, "sess-async")
	require.NoError(t, err)
	assert.Equal(t, int64(10), summary.RequestCount, "Stop() 后所有条目应已写入")
}
