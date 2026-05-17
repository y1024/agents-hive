package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// SkillRecord 表示 hive_skills 表中的一条记录
type SkillRecord struct {
	Name      string    `json:"name"`
	UserID    string    `json:"user_id"` // "" 表示 public skill；非空表示 personal skill
	Content   string    `json:"content"`
	Level     string    `json:"level"` // "user" | "workspace" | "global"
	Path      string    `json:"path"`  // 原始 FS 路径（可为空）
	Revision  int       `json:"revision"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SkillStore 管理 DB 中的 skill 覆盖值
type SkillStore struct {
	pool       *pgxpool.Pool
	logger     *zap.Logger
	invalidate func(name, userID string) // 变更后触发热重载回调（2026-04 MAJOR 2 修正：携带 user_id）
}

// NewSkillStore 创建 SkillStore 实例
func NewSkillStore(pool *pgxpool.Pool, logger *zap.Logger) *SkillStore {
	return &SkillStore{pool: pool, logger: logger}
}

// SetInvalidate 注册缓存失效回调
func (s *SkillStore) SetInvalidate(fn func(name, userID string)) {
	s.invalidate = fn
}

// Get 查询指定 (name, userID) 的 skill；userID="" 查 public 层。
// 返回 (record, found, error)
func (s *SkillStore) Get(ctx context.Context, name, userID string) (*SkillRecord, bool, error) {
	var r SkillRecord
	var path *string
	err := s.pool.QueryRow(ctx, `
		SELECT name, user_id, content, level, path, revision, updated_by, created_at, updated_at
		FROM hive_skills WHERE name = $1 AND user_id = $2
	`, name, userID).Scan(&r.Name, &r.UserID, &r.Content, &r.Level, &path, &r.Revision, &r.UpdatedBy, &r.CreatedAt, &r.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if path != nil {
		r.Path = *path
	}
	return &r, true, nil
}

// Upsert 创建或更新 skill（revision CAS：若传入 revision > 0 且与 DB 不符则返回冲突错误）。
// userID="" 写入 public 层；非空写入 personal 层（复合主键 (name, user_id) 保证多租户隔离）。
func (s *SkillStore) Upsert(ctx context.Context, name, userID, content, level, path, updatedBy string, expectRevision int) error {
	if expectRevision > 0 {
		// CAS 检查：当前 (name, user_id) 的 revision 必须等于 expectRevision
		var cur int
		err := s.pool.QueryRow(ctx,
			`SELECT revision FROM hive_skills WHERE name = $1 AND user_id = $2`,
			name, userID,
		).Scan(&cur)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		if err == nil && cur != expectRevision {
			return ErrSkillConflict
		}
	}

	var pathArg *string
	if path != "" {
		pathArg = &path
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO hive_skills (name, user_id, content, level, path, updated_by, revision)
		VALUES ($1, $2, $3, $4, $5, $6, 1)
		ON CONFLICT (name, user_id) DO UPDATE
		SET content    = EXCLUDED.content,
		    level      = EXCLUDED.level,
		    path       = EXCLUDED.path,
		    updated_by = EXCLUDED.updated_by,
		    revision   = hive_skills.revision + 1,
		    updated_at = NOW()
	`, name, userID, content, level, pathArg, updatedBy)
	if err != nil {
		return err
	}
	if s.invalidate != nil {
		s.invalidate(name, userID)
	}
	return nil
}

// Delete 删除 skill 覆盖（恢复到 FS 默认值）。userID="" 删 public 层。
func (s *SkillStore) Delete(ctx context.Context, name, userID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM hive_skills WHERE name = $1 AND user_id = $2`,
		name, userID,
	)
	if err != nil {
		return err
	}
	if s.invalidate != nil {
		s.invalidate(name, userID)
	}
	return nil
}

// List 列出所有 DB skill，支持分页
// 返回 (records, total, error)
func (s *SkillStore) List(ctx context.Context, page, size int) ([]SkillRecord, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 500 {
		size = 50
	}
	offset := (page - 1) * size

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM hive_skills`).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT name, user_id, content, level, path, revision, updated_by, created_at, updated_at
		FROM hive_skills ORDER BY name, user_id
		LIMIT $1 OFFSET $2
	`, size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var records []SkillRecord
	for rows.Next() {
		var r SkillRecord
		var path *string
		if err := rows.Scan(&r.Name, &r.UserID, &r.Content, &r.Level, &path, &r.Revision, &r.UpdatedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, 0, err
		}
		if path != nil {
			r.Path = *path
		}
		records = append(records, r)
	}
	return records, total, rows.Err()
}

// LoadAll 加载所有 DB skill（启动时用于填充内存缓存）
func (s *SkillStore) LoadAll(ctx context.Context) ([]SkillRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name, user_id, content, level, path, revision, updated_by, created_at, updated_at
		FROM hive_skills ORDER BY name, user_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []SkillRecord
	for rows.Next() {
		var r SkillRecord
		var path *string
		if err := rows.Scan(&r.Name, &r.UserID, &r.Content, &r.Level, &path, &r.Revision, &r.UpdatedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if path != nil {
			r.Path = *path
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// skillNotifyPayload 是 hive_skills_notify trigger 发送的 JSON payload。
// 兼容旧版（仅 name 的纯字符串）：JSON 解析失败时 fallback 为 {Name: raw, UserID: ""}。
type skillNotifyPayload struct {
	Name   string `json:"name"`
	UserID string `json:"user_id"`
	Op     string `json:"op"` // INSERT | UPDATE | DELETE
}

// StartNotifyListener 启动 PG LISTEN goroutine，监听跨实例 skill 变更通知。
// invalidate 回调带 (name, userID)：userID="" 表示 public 层变更。
func (s *SkillStore) StartNotifyListener(ctx context.Context, invalidate func(name, userID string)) {
	if s.pool == nil {
		return
	}
	go func() {
		const maxBackoff = 32 * time.Second
		backoff := time.Second
		for ctx.Err() == nil {
			pconn, err := s.pool.Acquire(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				s.logger.Warn("skill PG NOTIFY LISTEN 获取连接失败", zap.Error(err))
				sleepBackoff(ctx, &backoff, maxBackoff)
				continue
			}
			conn := pconn.Conn()
			if _, err := conn.Exec(ctx, "LISTEN hive_skill_changed"); err != nil {
				s.logger.Warn("skill PG NOTIFY LISTEN 订阅失败", zap.Error(err))
				pconn.Release()
				sleepBackoff(ctx, &backoff, maxBackoff)
				continue
			}
			s.logger.Info("skill PG NOTIFY LISTEN 已启动")
			backoff = time.Second
			for ctx.Err() == nil {
				n, err := conn.WaitForNotification(ctx)
				if err != nil {
					pconn.Release()
					if ctx.Err() != nil {
						return
					}
					s.logger.Warn("skill PG NOTIFY WaitForNotification 出错，准备重连", zap.Error(err))
					sleepBackoff(ctx, &backoff, maxBackoff)
					break
				}
				if n != nil && n.Payload != "" && invalidate != nil {
					name, userID := parseSkillPayload(n.Payload)
					if name != "" {
						invalidate(name, userID)
					}
				}
			}
		}
	}()
}

// parseSkillPayload 解析 pg_notify 的 payload。
// 新格式：JSON `{"name":"...","user_id":"...","op":"..."}`。
// 老格式（回滚期兼容）：单纯 name 字符串，user_id 默认 ""。
func parseSkillPayload(raw string) (name, userID string) {
	var p skillNotifyPayload
	if err := json.Unmarshal([]byte(raw), &p); err == nil && p.Name != "" {
		return p.Name, p.UserID
	}
	return raw, ""
}

// sleepBackoff 等待退避时间，支持 ctx cancel 提前退出并自动翻倍退避
func sleepBackoff(ctx context.Context, backoff *time.Duration, max time.Duration) {
	select {
	case <-time.After(*backoff):
	case <-ctx.Done():
	}
	*backoff = min(*backoff*2, max)
}

// ErrSkillConflict 表示 CAS revision 冲突（HTTP 412）
var ErrSkillConflict = &skillConflictError{}

type skillConflictError struct{}

func (e *skillConflictError) Error() string { return "skill revision conflict" }
