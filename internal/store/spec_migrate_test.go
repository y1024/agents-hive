package store_test

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/store"
)

// TestMigration_DownReverts — Sprint 3.2 (harden-spec-driven-phase2, task 12.16, Codex R5-2)
// 技术可逆性保险：up→down→up 循环对 3 张 spec 表必须 schema-identical，且 down 后
// 新插入的 events sequence 从 1 起（防残留事故串号）。
//
// 运维现实（runbook §4）：生产环境**不**调用 DropSpecTables——回退路径是
// mode=legacy 短路，表休眠零开销。本 test 只证明"需要时可以 drop+recreate"的
// 技术能力，不改变 runbook §4 的"不可回退项"操作约束。
//
// 覆盖 4 组断言（蓝军自检：缺任一 → mutation 可能溜过）：
//  1. schema 一致性：up→down→up 后三表 + 索引 + trigger + function 全部重建
//  2. 数据清零：down 后残留数据不可见（COUNT=0）
//  3. sequence 起点复位：新 change_id 的 MAX(sequence) 从 1 起，不被老数据串号
//  4. 幂等：down→down / up→up 不报错（defensive against drill 重跑）
func TestMigration_DownReverts(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL 未设置，跳过 migration down-reverts 集成测试")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	// 确保干净起点——上次跑残留清理（无论 up 过没）
	_ = store.DropSpecTables(ctx, pool)

	// ── Phase 1: up ───────────────────────────────────────────────────
	require.NoError(t, store.MigrateSpecTables(ctx, pool), "first up must succeed")

	// 种数据：c1 + 3 events（sequence=1..3）+ 1 session_state 行
	_, err = pool.Exec(ctx,
		`INSERT INTO hive_spec_changes (id, status) VALUES ('mig-c1', 'draft')`)
	require.NoError(t, err)
	for seq := 1; seq <= 3; seq++ {
		_, err := pool.Exec(ctx,
			`INSERT INTO hive_spec_change_events (change_id, sequence, event_type)
			 VALUES ($1, $2, 'test')`,
			"mig-c1", seq)
		require.NoError(t, err, "seed event seq=%d", seq)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO hive_spec_session_state (session_id, active_change_id)
		 VALUES ('mig-s1', 'mig-c1')`)
	require.NoError(t, err)

	// Schema fingerprint 前（tables + columns + indexes + triggers + functions）
	fpBefore := specSchemaFingerprint(t, ctx, pool)
	require.NotEmpty(t, fpBefore, "fingerprint must be non-empty after up")

	// ── Phase 2: down ─────────────────────────────────────────────────
	require.NoError(t, store.DropSpecTables(ctx, pool), "down must succeed")

	// 表物理消失
	for _, table := range []string{
		"hive_spec_changes",
		"hive_spec_change_events",
		"hive_spec_session_state",
	} {
		require.False(t, tableExists(t, ctx, pool, table),
			"table %s must be dropped after down", table)
	}
	// trigger/function 也消失（防孤儿）
	require.False(t, functionExists(t, ctx, pool, "hive_spec_changes_notify"),
		"hive_spec_changes_notify function must be dropped (prevents orphaned plpgsql)")

	// 幂等：重复 down 不报错
	require.NoError(t, store.DropSpecTables(ctx, pool),
		"drop must be idempotent (second drop = no-op)")

	// ── Phase 3: up again ─────────────────────────────────────────────
	require.NoError(t, store.MigrateSpecTables(ctx, pool), "second up must succeed")

	// 幂等：重复 up 不报错（CREATE IF NOT EXISTS 兜底）
	require.NoError(t, store.MigrateSpecTables(ctx, pool),
		"migrate must be idempotent (second up = no-op)")

	// Schema fingerprint 后 —— 必须与之前 bit-for-bit 一致
	fpAfter := specSchemaFingerprint(t, ctx, pool)
	require.Equal(t, fpBefore, fpAfter,
		"schema fingerprint drift after up→down→up — DDL asymmetry detected")

	// ── Phase 4: sequence 起点 + 数据清零 ────────────────────────────
	// c1 的 events 应该全部消失（被 drop cascade）
	var residueCount int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM hive_spec_change_events WHERE change_id = 'mig-c1'`).Scan(&residueCount)
	require.NoError(t, err)
	require.Equal(t, 0, residueCount,
		"old events must be gone after down (防残留事故串号)")

	// c1 本身也应该消失
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM hive_spec_changes WHERE id = 'mig-c1'`).Scan(&residueCount)
	require.NoError(t, err)
	require.Equal(t, 0, residueCount, "old change row must be gone after down")

	// session_state 也清零
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM hive_spec_session_state WHERE session_id = 'mig-s1'`).Scan(&residueCount)
	require.NoError(t, err)
	require.Equal(t, 0, residueCount, "old session state must be gone after down")

	// 新插 c2 + 1 event（sequence=1）—— MAX(sequence) 必须 = 1，不被老数据影响
	_, err = pool.Exec(ctx,
		`INSERT INTO hive_spec_changes (id, status) VALUES ('mig-c2', 'draft')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO hive_spec_change_events (change_id, sequence, event_type)
		 VALUES ('mig-c2', 1, 'test')`)
	require.NoError(t, err)

	var maxSeq int
	err = pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(sequence), 0) FROM hive_spec_change_events WHERE change_id = 'mig-c2'`).Scan(&maxSeq)
	require.NoError(t, err)
	require.Equal(t, 1, maxSeq,
		"new change_id sequence must start at 1 (防串号：old events 不应污染新 counter)")

	// ── cleanup ──────────────────────────────────────────────────────
	_, _ = pool.Exec(ctx, `DELETE FROM hive_spec_change_events WHERE change_id = 'mig-c2'`)
	_, _ = pool.Exec(ctx, `DELETE FROM hive_spec_changes WHERE id = 'mig-c2'`)
}

// specSchemaFingerprint 生成 3 张 spec 表的 schema fingerprint：
// tables + columns (name+type) + indexes (name+column set) + trigger + function。
// 返回排序后的换行拼接字符串——字节级 Equal 即 schema 一致。
func specSchemaFingerprint(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var parts []string

	targets := []string{"hive_spec_changes", "hive_spec_change_events", "hive_spec_session_state"}

	// Columns：name + data_type + is_nullable
	for _, table := range targets {
		rows, err := pool.Query(ctx, `
			SELECT column_name, data_type, is_nullable
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1
			ORDER BY ordinal_position`, table)
		require.NoError(t, err)
		for rows.Next() {
			var name, dataType, nullable string
			require.NoError(t, rows.Scan(&name, &dataType, &nullable))
			parts = append(parts, "col:"+table+"."+name+":"+dataType+":"+nullable)
		}
		rows.Close()
	}

	// Indexes：indexname + indexdef（包含列集合）
	rows, err := pool.Query(ctx, `
		SELECT indexname, indexdef
		FROM pg_indexes
		WHERE schemaname = 'public'
		  AND tablename = ANY($1)
		ORDER BY indexname`, targets)
	require.NoError(t, err)
	for rows.Next() {
		var name, def string
		require.NoError(t, rows.Scan(&name, &def))
		parts = append(parts, "idx:"+name+":"+def)
	}
	rows.Close()

	// Triggers
	rows, err = pool.Query(ctx, `
		SELECT tgname, pg_get_triggerdef(oid) AS def
		FROM pg_trigger
		WHERE NOT tgisinternal
		  AND tgrelid = ANY($1::regclass[])
		ORDER BY tgname`, []string{
		"hive_spec_changes",
	})
	require.NoError(t, err)
	for rows.Next() {
		var name, def string
		require.NoError(t, rows.Scan(&name, &def))
		parts = append(parts, "trg:"+name+":"+def)
	}
	rows.Close()

	// Functions（hive_spec_changes_notify）
	rows, err = pool.Query(ctx, `
		SELECT proname
		FROM pg_proc
		WHERE proname = 'hive_spec_changes_notify'
		ORDER BY proname`)
	require.NoError(t, err)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		parts = append(parts, "fn:"+name)
	}
	rows.Close()

	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func tableExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname = 'public' AND tablename = $1)`,
		name).Scan(&exists)
	require.NoError(t, err)
	return exists
}

func functionExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_proc WHERE proname = $1)`,
		name).Scan(&exists)
	require.NoError(t, err)
	return exists
}
