package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/specdriven"
	"github.com/chef-guo/agents-hive/internal/store"
)

// setupSpecStoreDB 走和 pg_tracker_integration_test.go 一样的 skip 约定：
// TEST_DATABASE_URL 未设则 skip（CI 的 specdriven gate 里不跑这条，另有 integration 阶段）。
// 建 Phase 2 的两张表——不复用 postgres_migrate.go 的 init（那个会一次性建全套，
// 测试隔离弱），就地单独建以便 DELETE 清理时不影响其他测试。
func setupSpecStoreDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL 未设置，跳过 spec_store PG 集成测试")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS hive_spec_changes (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'draft',
			title TEXT NOT NULL DEFAULT '',
			current_task_key TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL DEFAULT 1,
			updated_by TEXT NOT NULL DEFAULT '',
			parent_id TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS hive_spec_change_events (
			change_id TEXT NOT NULL REFERENCES hive_spec_changes(id) ON DELETE CASCADE,
			sequence INTEGER NOT NULL,
			event_type TEXT NOT NULL,
			prev_task_key TEXT NOT NULL DEFAULT '',
			new_task_key TEXT NOT NULL DEFAULT '',
			prev_status TEXT NOT NULL DEFAULT '',
			new_status TEXT NOT NULL DEFAULT '',
			actor_id TEXT NOT NULL DEFAULT '',
			payload JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (change_id, sequence)
		);
	`)
	require.NoError(t, err)
	// 清空避免上一次跑残留
	_, _ = pool.Exec(ctx, `DELETE FROM hive_spec_change_events; DELETE FROM hive_spec_changes;`)

	cleanup := func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM hive_spec_change_events; DELETE FROM hive_spec_changes;`)
		pool.Close()
	}
	return pool, cleanup
}

// TestSpecStore_UpsertInitialCreate：初次 create 成功，revision=1，event seq=1。
func TestSpecStore_UpsertInitialCreate(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	rec, seq, err := s.UpsertWithCAS(ctx, store.UpsertInput{
		ID:             "change-a",
		Status:         "draft",
		Title:          "Initial",
		CurrentTaskKey: "1",
		UpdatedBy:      "alice",
		ExpectRevision: 0,
		EventType:      "create",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, rec.Revision)
	assert.Equal(t, 1, seq)
	assert.Equal(t, "draft", rec.Status)

	got, found, err := s.Get(ctx, "change-a")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 1, got.Revision)
}

// TestSpecStore_DoubleCreateConflict：对已存在的 id 再 Create（ExpectRevision=0）
// 必须返回 ErrSpecChangeConflict——防重复 create 是 P0 红线之一。
func TestSpecStore_DoubleCreateConflict(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	_, _, err := s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "change-b", UpdatedBy: "alice", ExpectRevision: 0, EventType: "create",
	})
	require.NoError(t, err)

	_, _, err = s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "change-b", UpdatedBy: "bob", ExpectRevision: 0, EventType: "create",
	})
	assert.ErrorIs(t, err, store.ErrSpecChangeConflict)
}

// TestSpecStore_UpdateWrongRevisionConflict：CAS 核心语义——
// 读到 rev=1 两个 client 都去 update 并传 expect=1，只能一个赢。
func TestSpecStore_UpdateWrongRevisionConflict(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	_, _, err := s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "change-c", UpdatedBy: "alice", ExpectRevision: 0, EventType: "create",
	})
	require.NoError(t, err)

	// 正常 update rev 1 → 2
	_, _, err = s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "change-c", Status: "in_progress", UpdatedBy: "alice",
		ExpectRevision: 1, EventType: "status_change",
	})
	require.NoError(t, err)

	// 再用老 expect=1 去 update，已过时
	_, _, err = s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "change-c", Status: "complete", UpdatedBy: "bob",
		ExpectRevision: 1, EventType: "status_change",
	})
	assert.ErrorIs(t, err, store.ErrSpecChangeConflict)
}

// TestSpecStore_UpdateNonExistentConflict：针对不存在 id 传 ExpectRevision>0
// 必须 conflict（防 id 错认，attacker 用 expect=1 去无中生有）。
func TestSpecStore_UpdateNonExistentConflict(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	_, _, err := s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "ghost-id", UpdatedBy: "alice", ExpectRevision: 1, EventType: "status_change",
	})
	assert.ErrorIs(t, err, store.ErrSpecChangeConflict)
}

// TestSpecStore_EventMonotonic：连续多次成功 update，event sequence 必须 1,2,3...
// 任何 gap 都说明 AppendEvent 出 bug。
func TestSpecStore_EventMonotonic(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	_, seq, err := s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "change-d", UpdatedBy: "alice", ExpectRevision: 0, EventType: "create",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, seq)

	_, seq, err = s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "change-d", Status: "in_progress", UpdatedBy: "alice",
		ExpectRevision: 1, EventType: "status_change",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, seq)

	_, seq, err = s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "change-d", CurrentTaskKey: "2.1", UpdatedBy: "alice",
		ExpectRevision: 2, EventType: "task_advance",
	})
	require.NoError(t, err)
	assert.Equal(t, 3, seq)

	events, err := s.ListEvents(ctx, "change-d", 0)
	require.NoError(t, err)
	require.Len(t, events, 3)
	for i, ev := range events {
		assert.Equal(t, i+1, ev.Sequence)
	}
}

// TestSpecStore_AppendEvent_Inverse：独立 AppendEvent（不改主表）
// 可以 emit revert 事件。sequence 接在现有流尾。
func TestSpecStore_AppendEvent_Inverse(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	_, _, err := s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "change-e", UpdatedBy: "alice", ExpectRevision: 0, EventType: "create",
	})
	require.NoError(t, err)

	seq, err := s.AppendEvent(ctx, store.SpecChangeEvent{
		ChangeID:  "change-e",
		EventType: "inverse_revert",
		ActorID:   "system",
		Payload:   json.RawMessage(`{"reason":"intake downgrade"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, seq)

	events, err := s.ListEvents(ctx, "change-e", 0)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "create", events[0].EventType)
	assert.Equal(t, "inverse_revert", events[1].EventType)
	assert.JSONEq(t, `{"reason":"intake downgrade"}`, string(events[1].Payload))
}

// TestSpecStore_ListByUser：分页按 updated_at DESC。
func TestSpecStore_ListByUser(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	for _, id := range []string{"p1", "p2", "p3"} {
		_, _, err := s.UpsertWithCAS(ctx, store.UpsertInput{
			ID: id, UpdatedBy: "alice", ExpectRevision: 0, EventType: "create",
		})
		require.NoError(t, err)
	}
	_, _, err := s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "other", UpdatedBy: "bob", ExpectRevision: 0, EventType: "create",
	})
	require.NoError(t, err)

	list, total, err := s.ListByUser(ctx, "alice", 1, 50)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, list, 3)
	for _, r := range list {
		assert.Equal(t, "alice", r.UpdatedBy)
	}
}

// TestSpecStore_ConcurrentUpdate：双 client 并发 CAS，恰好一个赢一个拿 Conflict。
// 这是 tasks.md 2.9 的集成验收，覆盖 Guard 2 的核心不变量：绝无"双写"。
func TestSpecStore_ConcurrentUpdate(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	_, _, err := s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "race-1", UpdatedBy: "alice", ExpectRevision: 0, EventType: "create",
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	var successCount, conflictCount int32
	const workers = 8
	wg.Add(workers)
	for idx := range workers {
		go func(workerIdx int) {
			defer wg.Done()
			_, _, uerr := s.UpsertWithCAS(ctx, store.UpsertInput{
				ID:             "race-1",
				Status:         "in_progress",
				UpdatedBy:      "worker",
				ExpectRevision: 1, // 全都用老 rev，只能一个赢
				EventType:      "status_change",
			})
			switch {
			case uerr == nil:
				atomic.AddInt32(&successCount, 1)
			case errors.Is(uerr, store.ErrSpecChangeConflict):
				atomic.AddInt32(&conflictCount, 1)
			default:
				t.Errorf("worker %d 非预期错误: %v", workerIdx, uerr)
			}
		}(idx)
	}
	wg.Wait()

	assert.Equal(t, int32(1), atomic.LoadInt32(&successCount), "只能有一个 CAS 赢家")
	assert.Equal(t, int32(workers-1), atomic.LoadInt32(&conflictCount), "其余全部冲突")

	// 验证主表只 +1，事件只追加一条
	got, _, err := s.Get(ctx, "race-1")
	require.NoError(t, err)
	assert.Equal(t, 2, got.Revision, "revision 只能从 1 涨到 2")

	events, err := s.ListEvents(ctx, "race-1", 0)
	require.NoError(t, err)
	require.Len(t, events, 2, "create + 唯一赢家的 status_change")
}

// TestSpecStore_RetentionSweep_DeletesOldCompleted：过期的 completed change → 删。
// 活跃状态（draft/planning/active/in_progress/blocked）即使老也必须保留。
func TestSpecStore_RetentionSweep_DeletesOldCompleted(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	// 种 4 行：2 个 completed（一新一旧）+ 1 个 active（老）+ 1 个 draft（老）
	// 老 = updated_at 往前调 10 天；新 = 默认 now()
	mustCreate := func(id, status string, ageDays int) {
		_, _, err := s.UpsertWithCAS(ctx, store.UpsertInput{
			ID: id, Status: status, Title: id, UpdatedBy: "alice",
			ExpectRevision: 0, EventType: "create",
		})
		require.NoError(t, err)
		if ageDays > 0 {
			// 手动回调 updated_at——模拟"老记录"
			// pgx v5.8.0 严格：`$1 || ' days'` 要求 $1 被 encode 成 text，但 int
			// 在 text concat 上下文下没有 auto encode plan → 用 make_interval 代替
			// （PG 原生函数，接 int days 参数），兼容性 + 类型安全都更强。
			_, err := pool.Exec(ctx,
				`UPDATE hive_spec_changes SET updated_at = NOW() - make_interval(days => $1) WHERE id = $2`,
				ageDays, id)
			require.NoError(t, err)
		}
	}
	mustCreate("old-completed", "completed", 10)
	mustCreate("new-completed", "completed", 0)
	mustCreate("old-active", "active", 10)
	mustCreate("old-draft", "draft", 10)

	// cutoff = 5 天前；应该删 old-completed，跳过 new-completed/old-active/old-draft
	cutoff := time.Now().Add(-5 * 24 * time.Hour)
	deleted, skipped, err := s.RetentionSweep(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted, "只删 old-completed")
	assert.Equal(t, 2, skipped, "old-active + old-draft 在保护名单内被跳过")

	// 验证：old-completed 没了，其它都在
	_, found, _ := s.Get(ctx, "old-completed")
	assert.False(t, found, "old-completed 必须被删")
	_, found, _ = s.Get(ctx, "new-completed")
	assert.True(t, found, "new-completed 不够老，保留")
	_, found, _ = s.Get(ctx, "old-active")
	assert.True(t, found, "old-active 受保护，绝不删")
	_, found, _ = s.Get(ctx, "old-draft")
	assert.True(t, found, "old-draft 受保护，绝不删")
}

// TestSpecStore_RetentionSweep_RejectsFutureCutoff：cutoff 在未来 → 拒绝。
// 防运维手滑把 cutoff 写反时把整表扫光。
func TestSpecStore_RetentionSweep_RejectsFutureCutoff(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	_, _, err := s.RetentionSweep(ctx, time.Now().Add(24*time.Hour))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "past timestamp")

	_, _, err = s.RetentionSweep(ctx, time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "past timestamp")
}

// TestSpecStore_RetentionSweep_CascadesEvents：删除 change 时，events 跟着 CASCADE。
func TestSpecStore_RetentionSweep_CascadesEvents(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	_, _, err := s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "to-die", Status: "completed", Title: "rip", UpdatedBy: "alice",
		ExpectRevision: 0, EventType: "create",
	})
	require.NoError(t, err)
	_, err = s.AppendEvent(ctx, store.SpecChangeEvent{
		ChangeID: "to-die", EventType: "extra",
	})
	require.NoError(t, err)

	// 回调 updated_at
	_, err = pool.Exec(ctx,
		`UPDATE hive_spec_changes SET updated_at = NOW() - INTERVAL '10 days' WHERE id = 'to-die'`)
	require.NoError(t, err)

	deleted, _, err := s.RetentionSweep(ctx, time.Now().Add(-5*24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	// events 应该被 CASCADE 删
	events, err := s.ListEvents(ctx, "to-die", 0)
	require.NoError(t, err)
	assert.Len(t, events, 0, "events 必须跟 change 一起被 CASCADE 清掉")
}

// TestCASScenarioLabels_EnumLocked 纯单元测试（不需 PG）：锁死 specdriven.AllowedCASConflictScenarios
// 白名单的 3 个字面量，与 spec_store.go 三个 emitConflict call site 的 string literal 做一一绑定。
//
// 蓝军目标：如果有人往 spec_store.go 里加了第 4 种 scenario 却忘记更新 specdriven/metrics.go
// 的 Allowed 列表（或反向，enum 改 string 但 emit 点没改），本测试立刻红。
//
// Sprint 2.3 / Codex R5-3：label cardinality 在代码层面被枚举锁死。
func TestCASScenarioLabels_EnumLocked(t *testing.T) {
	// 预期集合（与 spec_store.go 三个 emitConflict 字面量 1:1）
	expected := map[specdriven.CASConflictScenario]bool{
		specdriven.CASScenarioDuplicateCreate: true, // "duplicate_create"
		specdriven.CASScenarioGhostID:         true, // "ghost_id"
		specdriven.CASScenarioStaleRevision:   true, // "stale_revision"
	}

	got := specdriven.AllowedCASConflictScenarios
	require.Len(t, got, 3, "CAS scenario label 锁定 3 条；新增请同步 spec_store.emitConflict call sites")

	seen := map[specdriven.CASConflictScenario]int{}
	for _, s := range got {
		seen[s]++
		assert.True(t, expected[s], "未在白名单中的 scenario: %q", s)
	}
	for s, count := range seen {
		assert.Equal(t, 1, count, "scenario %q 出现 %d 次（应仅一次）", s, count)
	}

	// 显式锁字面量：防有人改了 const 值导致 metric label 漂到新名字。
	assert.Equal(t, "duplicate_create", string(specdriven.CASScenarioDuplicateCreate))
	assert.Equal(t, "ghost_id", string(specdriven.CASScenarioGhostID))
	assert.Equal(t, "stale_revision", string(specdriven.CASScenarioStaleRevision))
}

// TestCASConflict_ScenarioLabelsIndependent PG 集成测试：驱动三路 CAS conflict，
// 每路独立验证观察者回调接到的 scenario label 正确。防：
//  1. 某一 case 回调漏 emit（Codex R5-3 红线）
//  2. label 字符串与 enum 白名单不一致（cardinality 漂移）
//  3. 不同 case 被错误映射到同一 label（例如 ghost_id 错写成 duplicate_create）
//
// Sprint 2.3 准入条件。
func TestCASConflict_ScenarioLabelsIndependent(t *testing.T) {
	pool, cleanup := setupSpecStoreDB(t)
	defer cleanup()
	s := store.NewSpecChangeStore(pool, nil)
	ctx := context.Background()

	var mu sync.Mutex
	var captured []string
	s.SetConflictObserver(func(scenario string) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, scenario)
	})

	// Setup：先创一条 change-x 供后续冲突路径触发
	_, _, err := s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "scen-x", UpdatedBy: "alice", ExpectRevision: 0, EventType: "create",
	})
	require.NoError(t, err)

	// Path 1: ghost_id — 不存在 id 传 ExpectRevision>0
	_, _, err = s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "ghost-never-exists", UpdatedBy: "bob", ExpectRevision: 3, EventType: "status_change",
	})
	assert.ErrorIs(t, err, store.ErrSpecChangeConflict)

	// Path 2: duplicate_create — 已存在 id 传 ExpectRevision=0
	_, _, err = s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "scen-x", UpdatedBy: "bob", ExpectRevision: 0, EventType: "create",
	})
	assert.ErrorIs(t, err, store.ErrSpecChangeConflict)

	// Path 3: stale_revision — 存在 id 但 ExpectRevision 不匹配
	_, _, err = s.UpsertWithCAS(ctx, store.UpsertInput{
		ID: "scen-x", UpdatedBy: "bob", ExpectRevision: 99, EventType: "status_change",
	})
	assert.ErrorIs(t, err, store.ErrSpecChangeConflict)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, captured, 3, "三路 CAS 冲突每条必须 emit 一次 cas_conflict_total")

	// 验证顺序与 scenario 绑定
	assert.Equal(t, "ghost_id", captured[0], "第 1 次应为 ghost_id（不存在 id + expect>0）")
	assert.Equal(t, "duplicate_create", captured[1], "第 2 次应为 duplicate_create（已存在 id + expect=0）")
	assert.Equal(t, "stale_revision", captured[2], "第 3 次应为 stale_revision（已存在 id + expect 不匹配 curRevision）")

	// 交叉校验白名单：捕获的每个 label 必须在 specdriven.AllowedCASConflictScenarios 内
	allowed := map[string]bool{}
	for _, s := range specdriven.AllowedCASConflictScenarios {
		allowed[string(s)] = true
	}
	for _, c := range captured {
		assert.True(t, allowed[c], "captured label %q 不在 AllowedCASConflictScenarios 白名单（cardinality 漂移）", c)
	}
}
