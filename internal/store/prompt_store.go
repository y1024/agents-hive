package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// PromptRecord 表示 hive_prompts 表中的一条记录
type PromptRecord struct {
	Key       string    `json:"key"`
	Language  string    `json:"language"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
}

// PromptStore 管理 DB 中的 prompt 覆盖值
type PromptStore struct {
	pool       *pgxpool.Pool
	logger     *zap.Logger
	invalidate func(key string) // PromptLoader 订阅回调，PG NOTIFY 触发时调用
}

// NewPromptStore 创建 PromptStore 实例
// invalidate 可为 nil（CLI 路径不需要跨实例通知）
func NewPromptStore(pool *pgxpool.Pool, logger *zap.Logger) *PromptStore {
	return &PromptStore{pool: pool, logger: logger}
}

// SetInvalidate 注册缓存失效回调（由 PromptLoader 调用，注入跨实例通知能力）
func (s *PromptStore) SetInvalidate(fn func(key string)) {
	s.invalidate = fn
}

// Get 查询指定 key 的 prompt（优先精确语言匹配，fallback 到通用覆盖 language=”）
// 返回 (content, found, error)
func (s *PromptStore) Get(ctx context.Context, key, language string) (string, bool, error) {
	// 先尝试精确语言匹配，再 fallback 到通用覆盖
	var content string
	err := s.pool.QueryRow(ctx, `
		SELECT content FROM hive_prompts
		WHERE key = $1 AND language = $2
	`, key, language).Scan(&content)
	if err == nil {
		return content, true, nil
	}
	if err != pgx.ErrNoRows {
		return "", false, err
	}

	// 精确语言未命中，尝试通用覆盖（language=''）
	if language != "" {
		err = s.pool.QueryRow(ctx, `
			SELECT content FROM hive_prompts
			WHERE key = $1 AND language = ''
		`, key).Scan(&content)
		if err == nil {
			return content, true, nil
		}
		if err != pgx.ErrNoRows {
			return "", false, err
		}
	}

	return "", false, nil
}

// Upsert 创建或更新 prompt 覆盖值
func (s *PromptStore) Upsert(ctx context.Context, key, language, content, updatedBy string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO hive_prompts (key, language, content, updated_at, updated_by)
		VALUES ($1, $2, $3, NOW(), $4)
		ON CONFLICT (key, language) DO UPDATE
		SET content = EXCLUDED.content,
		    updated_at = NOW(),
		    updated_by = EXCLUDED.updated_by
	`, key, language, content, updatedBy)
	if err != nil {
		return err
	}
	// 触发跨实例缓存失效回调（毫秒级，无需等 TTL）
	if s.invalidate != nil {
		s.invalidate(key)
	}
	return nil
}

// Delete 删除 prompt 覆盖值（恢复到文件默认值）
func (s *PromptStore) Delete(ctx context.Context, key, language string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM hive_prompts WHERE key = $1 AND language = $2
	`, key, language)
	if err != nil {
		return err
	}
	// 触发跨实例缓存失效回调
	if s.invalidate != nil {
		s.invalidate(key)
	}
	return nil
}

// List 列出所有已覆盖的 prompt，支持分页
// 返回 (records, total, error)
func (s *PromptStore) List(ctx context.Context, page, size int) ([]PromptRecord, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	offset := (page - 1) * size

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM hive_prompts`).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT key, language, content, updated_at, updated_by
		FROM hive_prompts
		ORDER BY key, language
		LIMIT $1 OFFSET $2
	`, size, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var records []PromptRecord
	for rows.Next() {
		var r PromptRecord
		if err := rows.Scan(&r.Key, &r.Language, &r.Content, &r.UpdatedAt, &r.UpdatedBy); err != nil {
			return nil, 0, err
		}
		records = append(records, r)
	}
	return records, total, rows.Err()
}

const notifyChannel = "hive_prompt_changed"

// StartNotifyListener 启动 PG LISTEN goroutine，监听跨实例 prompt 变更通知。
// 当其他实例更新 hive_prompts 时，NOTIFY trigger 触发，本实例收到通知后调用 invalidate 回调。
// ctx cancel 时 goroutine 自动退出。
func (s *PromptStore) StartNotifyListener(ctx context.Context, invalidate func(key string)) {
	if s.pool == nil {
		return
	}

	go func() {
		const maxBackoff = 32 * time.Second
		backoff := time.Second

		for ctx.Err() == nil {
			// 从 pool 获取一个专属连接用于 LISTEN
			pconn, err := s.pool.Acquire(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				s.logger.Warn("PG NOTIFY LISTEN 获取连接失败，准备重试",
					zap.Error(err), zap.Duration("backoff", backoff))
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					return
				}
				backoff = min(backoff*2, maxBackoff)
				continue
			}
			conn := pconn.Conn()

			// 执行 LISTEN 订阅
			if _, err := conn.Exec(ctx, "LISTEN "+notifyChannel); err != nil {
				s.logger.Warn("PG NOTIFY LISTEN 订阅失败，准备重试",
					zap.Error(err), zap.Duration("backoff", backoff))
				pconn.Release()
				if ctx.Err() != nil {
					return
				}
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					return
				}
				backoff = min(backoff*2, maxBackoff)
				continue
			}

			s.logger.Info("PG NOTIFY LISTEN 已启动", zap.String("channel", notifyChannel))
			backoff = time.Second // 成功连接后重置退避

			// WaitForNotification 阻塞直到收到通知或 ctx 取消
			disconnected := false
			for ctx.Err() == nil {
				n, err := conn.WaitForNotification(ctx)
				if err != nil {
					pconn.Release()
					if ctx.Err() != nil {
						s.logger.Info("PG NOTIFY LISTEN 已停止")
						return
					}
					s.logger.Warn("PG NOTIFY WaitForNotification 出错，准备重连",
						zap.Error(err), zap.Duration("backoff", backoff))
					select {
					case <-time.After(backoff):
					case <-ctx.Done():
						return
					}
					backoff = min(backoff*2, maxBackoff)
					disconnected = true
					break
				}
				if n == nil {
					continue
				}
				// 收到通知说明连接正常，重置退避
				backoff = time.Second
				s.logger.Debug("PG NOTIFY 收到变更通知",
					zap.String("channel", n.Channel),
					zap.String("payload", n.Payload),
				)
				// payload 是被更新的 prompt key
				if n.Payload != "" && invalidate != nil {
					invalidate(n.Payload)
				}
			}
			if !disconnected {
				// ctx 已取消，正常退出
				pconn.Release()
				s.logger.Info("PG NOTIFY LISTEN 已停止")
				return
			}
		}
	}()
}
