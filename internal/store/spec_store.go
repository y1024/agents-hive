package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// SpecChangeRecord 是 hive_spec_changes 表的行模型。
// Phase 2 设计（design.md D6）：revision 用来 CAS，防止并发 update 覆盖；
// current_task_key 是"当前进行中 task"的 lightweight 锚点——重实况靠 events 表回放。
type SpecChangeRecord struct {
	ID             string    `json:"id"`
	Status         string    `json:"status"`
	Title          string    `json:"title"`
	CurrentTaskKey string    `json:"current_task_key"`
	Revision       int       `json:"revision"`
	UpdatedBy      string    `json:"updated_by"`
	ParentID       string    `json:"parent_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SpecChangeEvent 是 hive_spec_change_events 表的行模型。
// 事件流是唯一 source of truth——spec_changes 主表只是投影。
// sequence per change_id 严格单调，任何 reader 都可以 (change_id, sequence)
// 回放到任意时刻状态，不依赖 updated_at 这种时钟敏感字段。
type SpecChangeEvent struct {
	ChangeID    string          `json:"change_id"`
	Sequence    int             `json:"sequence"`
	EventType   string          `json:"event_type"`
	PrevTaskKey string          `json:"prev_task_key,omitempty"`
	NewTaskKey  string          `json:"new_task_key,omitempty"`
	PrevStatus  string          `json:"prev_status,omitempty"`
	NewStatus   string          `json:"new_status,omitempty"`
	ActorID     string          `json:"actor_id,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// SpecChangeStore 是 spec-driven Phase 2 的一致性基座。
// Guard 2（并发 continuation 决策）+ Guard 3（intake/planner 幂等）都用它做 CAS。
//
// 设计要点（对比 skill_store.go:62 的改进）：
//   - Upsert 用单事务 UPDATE ... WHERE revision = $expected：rows_affected=0
//     语义即冲突。skill_store 那个 SELECT→INSERT 中间存在 TOCTOU 窗口，
//     两 client 都能读到 revision=3 然后双写（INSERT ON CONFLICT 会都成功）。
//   - AppendEvent 在同一事务里 SELECT MAX(sequence)+1，避免事件漏号。
//
// CASConflictObserver 在 UpsertWithCAS 每次冲突时被回调。scenario 取自
// specdriven.CASConflictScenario 白名单（duplicate_create / ghost_id / stale_revision），
// 供 Master 把计数打入 specdriven.cas_conflict_total{scenario} metric。
//
// 设计：store 不直接依赖 observability 包——解耦 + 可测（测试注入 slice append 观察）。
// nil 安全：observer 为 nil 时不 emit（保持 Phase 1 兼容）。
// Sprint 2.3 / Codex R5-3：三路 CAS 冲突每条都必须 emit，不能只 happy-path。
type CASConflictObserver func(scenario string)

// UpsertObserver 在 UpsertWithCAS 成功 commit 后被回调（CAS 冲突分支不算）。
// Master wire 它把计数打入 specdriven.spec_change_upsert_total——这是
// cas_conflict_total 的 SLO 分母。Round 5 评审 G1：分母不存在则 SLO 公式悬空。
//
// 设计：与 CASConflictObserver 同样的解耦模式（store 不直接 import specdriven），
// nil 安全。
type UpsertObserver func()

type SpecChangeStore struct {
	pool             *pgxpool.Pool
	logger           *zap.Logger
	conflictObserver CASConflictObserver
	upsertObserver   UpsertObserver
}

// NewSpecChangeStore 构造 store。logger 可为 nil（走 zap.NewNop）。
func NewSpecChangeStore(pool *pgxpool.Pool, logger *zap.Logger) *SpecChangeStore {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &SpecChangeStore{pool: pool, logger: logger}
}

// SetConflictObserver 注入 CAS 冲突观察者（Sprint 2.3）。nil 即关闭 emit。
// 设计：独立 setter 而非 New 参数——Master 在 store 构造后、wire metric pipeline 前
// 可以分两阶段设置；Phase 1 call site 不动。
func (s *SpecChangeStore) SetConflictObserver(obs CASConflictObserver) {
	s.conflictObserver = obs
}

// SetUpsertObserver 注入成功 upsert 观察者（Round 5 G1 修复）。nil 即关闭 emit。
func (s *SpecChangeStore) SetUpsertObserver(obs UpsertObserver) {
	s.upsertObserver = obs
}

// emitConflict 内部 helper：nil 安全 + 单一 emit 点，便于日后换实现。
func (s *SpecChangeStore) emitConflict(scenario string) {
	if s.conflictObserver != nil {
		s.conflictObserver(scenario)
	}
}

// emitUpsertSuccess 内部 helper：commit 成功后调用，nil 安全。
func (s *SpecChangeStore) emitUpsertSuccess() {
	if s.upsertObserver != nil {
		s.upsertObserver()
	}
}

// Get 按 id 读取 spec change 主记录。未找到返回 (nil, false, nil)。
func (s *SpecChangeStore) Get(ctx context.Context, id string) (*SpecChangeRecord, bool, error) {
	var r SpecChangeRecord
	var parent *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, status, title, current_task_key, revision, updated_by, parent_id, created_at, updated_at
		FROM hive_spec_changes WHERE id = $1
	`, id).Scan(&r.ID, &r.Status, &r.Title, &r.CurrentTaskKey, &r.Revision, &r.UpdatedBy, &parent, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if parent != nil {
		r.ParentID = *parent
	}
	return &r, true, nil
}

// UpsertInput 是 UpsertWithCAS 的输入参数。
// ExpectRevision=0 语义：第一次插入（不存在则创建，存在则冲突）。
// ExpectRevision>0 语义：更新已有记录，DB 当前 revision 必须等于此值。
// EventType 至少要给——审计链不能有空格。
type UpsertInput struct {
	ID             string
	Status         string
	Title          string
	CurrentTaskKey string
	UpdatedBy      string
	ParentID       string
	ExpectRevision int

	EventType    string
	EventPayload json.RawMessage
}

// UpsertWithCAS 在单事务里执行 read → check revision → update → increment → AppendEvent。
// 任一阶段失败整体 rollback——事件表不会出现"写了事件但主表没更新"的半截状态。
//
// 冲突语义：
//   - ExpectRevision=0 且 id 已存在 → ErrSpecChangeConflict（防重复 create）
//   - ExpectRevision>0 且 id 不存在 → ErrSpecChangeConflict（防错认 id）
//   - ExpectRevision>0 且 DB revision 不匹配 → ErrSpecChangeConflict（CAS 标准语义）
//
// 返回写入后的 record（revision 已 +1）和新事件 sequence。
func (s *SpecChangeStore) UpsertWithCAS(ctx context.Context, in UpsertInput) (*SpecChangeRecord, int, error) {
	if in.ID == "" {
		return nil, 0, fmt.Errorf("spec change id required")
	}
	if in.EventType == "" {
		return nil, 0, fmt.Errorf("spec change event_type required (audit chain must not have gaps)")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // 成功 path 下 Commit 后 Rollback 是 no-op

	// 1. 读当前状态
	var curRevision int
	var curStatus, curTaskKey string
	err = tx.QueryRow(ctx, `
		SELECT revision, status, current_task_key
		FROM hive_spec_changes WHERE id = $1 FOR UPDATE
	`, in.ID).Scan(&curRevision, &curStatus, &curTaskKey)
	exists := !errors.Is(err, pgx.ErrNoRows)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, fmt.Errorf("read current: %w", err)
	}

	// 2. CAS 检查 — Sprint 2.3 / Codex R5-3：每条 case 都 emit cas_conflict_total{scenario}。
	// scenario 取值锁死在 specdriven.CASConflictScenario 白名单（见 specdriven/metrics.go）。
	// 这里用裸字符串而非 import specdriven.CASScenario* 是为了让 store 包不反向依赖 specdriven
	// （循环依赖预防）；对应白名单在 TestCASConflict_ScenarioLabelsIndependent 里 1:1 锁死。
	switch {
	case !exists && in.ExpectRevision != 0:
		s.emitConflict("ghost_id")
		return nil, 0, ErrSpecChangeConflict
	case exists && in.ExpectRevision == 0:
		s.emitConflict("duplicate_create")
		return nil, 0, ErrSpecChangeConflict
	case exists && in.ExpectRevision != curRevision:
		s.emitConflict("stale_revision")
		return nil, 0, ErrSpecChangeConflict
	}

	// 3. 写入主表（insert 或 update）
	var newRec SpecChangeRecord
	var parentArg *string
	if in.ParentID != "" {
		p := in.ParentID
		parentArg = &p
	}
	if !exists {
		err = tx.QueryRow(ctx, `
			INSERT INTO hive_spec_changes (id, status, title, current_task_key, revision, updated_by, parent_id)
			VALUES ($1, $2, $3, $4, 1, $5, $6)
			RETURNING id, status, title, current_task_key, revision, updated_by, parent_id, created_at, updated_at
		`, in.ID, in.Status, in.Title, in.CurrentTaskKey, in.UpdatedBy, parentArg).Scan(
			&newRec.ID, &newRec.Status, &newRec.Title, &newRec.CurrentTaskKey,
			&newRec.Revision, &newRec.UpdatedBy, &parentArg, &newRec.CreatedAt, &newRec.UpdatedAt,
		)
	} else {
		// CAS UPDATE：显式带 WHERE revision = $expected，rows_affected=0 即已被别人抢写
		tag, execErr := tx.Exec(ctx, `
			UPDATE hive_spec_changes
			SET status = $2, title = $3, current_task_key = $4,
			    updated_by = $5, parent_id = $6,
			    revision = revision + 1, updated_at = NOW()
			WHERE id = $1 AND revision = $7
		`, in.ID, in.Status, in.Title, in.CurrentTaskKey, in.UpdatedBy, parentArg, in.ExpectRevision)
		if execErr != nil {
			return nil, 0, fmt.Errorf("update: %w", execErr)
		}
		if tag.RowsAffected() == 0 {
			// race backstop：经过 L146 的 curRevision==ExpectRevision 检查但 UPDATE 时 rows=0，
			// 说明在同事务内 SELECT FOR UPDATE 与 UPDATE 之间某 cause 让 revision 变了。
			// 归类为 stale_revision（与 L146 同语义，只是发现点不同）。
			s.emitConflict("stale_revision")
			return nil, 0, ErrSpecChangeConflict
		}
		err = tx.QueryRow(ctx, `
			SELECT id, status, title, current_task_key, revision, updated_by, parent_id, created_at, updated_at
			FROM hive_spec_changes WHERE id = $1
		`, in.ID).Scan(
			&newRec.ID, &newRec.Status, &newRec.Title, &newRec.CurrentTaskKey,
			&newRec.Revision, &newRec.UpdatedBy, &parentArg, &newRec.CreatedAt, &newRec.UpdatedAt,
		)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("scan after write: %w", err)
	}
	if parentArg != nil {
		newRec.ParentID = *parentArg
	}

	// 4. AppendEvent in-tx（sequence 用 MAX+1，对 PG MVCC 也安全——整张事件表
	//    对 change_id 维度的 FOR UPDATE 在上面主表那步已经拿到了）。
	seq, err := appendEventTx(ctx, tx, in.ID, in.EventType,
		curTaskKey, in.CurrentTaskKey, curStatus, in.Status, in.UpdatedBy, in.EventPayload)
	if err != nil {
		return nil, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("commit: %w", err)
	}
	// Round 5 G1：commit 成功才计入分母。冲突早 return 不算（防止给 fallback 率
	// 注水——分母越大率越小，会掩盖真实问题）。
	s.emitUpsertSuccess()
	return &newRec, seq, nil
}

// AppendEvent 独立追加一条事件（不改主表）。
// 典型场景：revert 场景下 emit 一条 inverse 事件做审计链延续。
// 该调用自己开事务保证 sequence 单调。
func (s *SpecChangeStore) AppendEvent(ctx context.Context, ev SpecChangeEvent) (int, error) {
	if ev.ChangeID == "" || ev.EventType == "" {
		return 0, fmt.Errorf("change_id and event_type required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := appendEventTx(ctx, tx, ev.ChangeID, ev.EventType,
		ev.PrevTaskKey, ev.NewTaskKey, ev.PrevStatus, ev.NewStatus, ev.ActorID, ev.Payload)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return seq, nil
}

// appendEventTx 内部实现：在给定 tx 里追加事件，sequence = 当前 MAX(seq)+1。
// 必须在已经拿到 change_id 主表行锁的事务中调用——否则两个并行 tx 都读 MAX=3
// 然后都写 4，会撞 primary key 冲突（这是正确行为但 caller 需要准备 retry）。
func appendEventTx(ctx context.Context, tx pgx.Tx,
	changeID, eventType, prevTaskKey, newTaskKey, prevStatus, newStatus, actorID string,
	payload json.RawMessage,
) (int, error) {
	var maxSeq *int
	if err := tx.QueryRow(ctx, `
		SELECT MAX(sequence) FROM hive_spec_change_events WHERE change_id = $1
	`, changeID).Scan(&maxSeq); err != nil {
		return 0, fmt.Errorf("read max seq: %w", err)
	}
	nextSeq := 1
	if maxSeq != nil {
		nextSeq = *maxSeq + 1
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO hive_spec_change_events
			(change_id, sequence, event_type, prev_task_key, new_task_key,
			 prev_status, new_status, actor_id, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, changeID, nextSeq, eventType, prevTaskKey, newTaskKey,
		prevStatus, newStatus, actorID, []byte(payload)); err != nil {
		return 0, fmt.Errorf("insert event: %w", err)
	}
	return nextSeq, nil
}

// ListEvents 返回某个 change 的事件流，按 sequence 升序。limit<=0 不限。
func (s *SpecChangeStore) ListEvents(ctx context.Context, changeID string, limit int) ([]SpecChangeEvent, error) {
	query := `
		SELECT change_id, sequence, event_type, prev_task_key, new_task_key,
		       prev_status, new_status, actor_id, payload, created_at
		FROM hive_spec_change_events
		WHERE change_id = $1
		ORDER BY sequence ASC
	`
	args := []any{changeID}
	if limit > 0 {
		query += ` LIMIT $2`
		args = append(args, limit)
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpecChangeEvent
	for rows.Next() {
		var ev SpecChangeEvent
		var payloadBytes []byte
		if err := rows.Scan(&ev.ChangeID, &ev.Sequence, &ev.EventType,
			&ev.PrevTaskKey, &ev.NewTaskKey, &ev.PrevStatus, &ev.NewStatus,
			&ev.ActorID, &payloadBytes, &ev.CreatedAt); err != nil {
			return nil, err
		}
		if len(payloadBytes) > 0 {
			ev.Payload = json.RawMessage(payloadBytes)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ListByUser 返回某个 user 最近 update 的 spec changes，按 updated_at DESC。
// page 从 1 开始，size 1..200 之间。
func (s *SpecChangeStore) ListByUser(ctx context.Context, userID string, page, size int) ([]SpecChangeRecord, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	offset := (page - 1) * size

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM hive_spec_changes WHERE updated_by = $1`, userID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, status, title, current_task_key, revision, updated_by, parent_id, created_at, updated_at
		FROM hive_spec_changes
		WHERE updated_by = $1
		ORDER BY updated_at DESC
		LIMIT $2 OFFSET $3
	`, userID, size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []SpecChangeRecord
	for rows.Next() {
		var r SpecChangeRecord
		var parent *string
		if err := rows.Scan(&r.ID, &r.Status, &r.Title, &r.CurrentTaskKey,
			&r.Revision, &r.UpdatedBy, &parent, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, 0, err
		}
		if parent != nil {
			r.ParentID = *parent
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// RetentionProtectedStatuses 列出所有 retention sweeper **绝不允许删除**的 change 状态。
// 原则（design.md TG8 §8.4）：任何进行中 / 规划中 / 阻塞中的 change 都是"活的"，
// 清理这类记录会让 session 下一次 ingress 对不上 spec——Guard 2 CAS 失败率飙升。
//
// 这个集合是 append-only——新增状态只能加、绝不能删。任何试图把 "in_progress"
// 从这个表里拿掉的 PR 必须被 review 拦下。
var RetentionProtectedStatuses = []string{
	"draft",       // 刚 propose 还没开始实现
	"planning",    // planner 正在规划
	"active",      // session 正在基于它做事
	"in_progress", // 与 active 语义等价，兼容历史数据
	"blocked",     // 阻塞中的 change 是问题信号，绝不静默清理
}

// RetentionSweep 删除 cutoff 之前**且状态不在保护名单**的 change + 关联 events。
//
// 注意：retention sweeper 不是 GDPR/compliance 删除——那种 deletion 走单独路径
// 且必须审批。RetentionSweep 只负责打扫已 archive/completed/rejected 的老 change
// 以控制表增长，对应 SLO："7 天前关单的 change 行数 < 10k"。
//
// 返回：
//   - deleted: 本次删除的 change 行数（events 通过 ON DELETE CASCADE 一起清）
//   - skipped: cutoff 之前但状态在保护名单内、因此**未删**的行数（运维可读的信号）
//   - err: 数据库错误
//
// 硬规约：
//   - 事务内先 SELECT 出候选 id（用 status 过滤保护集），再 DELETE by id，
//     防止并发的 UpsertWithCAS 把一个 active change 升级后又被 sweep 误删
//   - protected 状态列表来自常量 RetentionProtectedStatuses，不可外部注入
func (s *SpecChangeStore) RetentionSweep(ctx context.Context, cutoff time.Time) (deleted, skipped int, err error) {
	if cutoff.IsZero() || cutoff.After(time.Now()) {
		return 0, 0, errors.New("retention sweep: cutoff must be a past timestamp")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, 0, fmt.Errorf("retention sweep begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx) // commit 成功后 rollback 是 no-op
	}()

	// 统计 cutoff 之前、状态受保护的 change 数——给 metric 用
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM hive_spec_changes
		WHERE updated_at < $1 AND status = ANY($2)
	`, cutoff, RetentionProtectedStatuses).Scan(&skipped); err != nil {
		return 0, 0, fmt.Errorf("retention sweep count protected: %w", err)
	}

	// 真正删除：cutoff 之前且状态**不在**保护名单
	tag, err := tx.Exec(ctx, `
		DELETE FROM hive_spec_changes
		WHERE updated_at < $1 AND NOT (status = ANY($2))
	`, cutoff, RetentionProtectedStatuses)
	if err != nil {
		return 0, 0, fmt.Errorf("retention sweep delete: %w", err)
	}
	deleted = int(tag.RowsAffected())

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("retention sweep commit: %w", err)
	}

	if s.logger != nil {
		s.logger.Info("spec retention sweep completed",
			zap.Time("cutoff", cutoff),
			zap.Int("deleted", deleted),
			zap.Int("skipped_protected", skipped),
		)
	}
	return deleted, skipped, nil
}
