package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/errs"
)

// 编译期接口合规检查
var _ MemoryStore = (*PostgresMemoryStore)(nil)
var _ GovernanceStore = (*PostgresMemoryStore)(nil)
var _ MemoryEmbeddingSyncer = (*PostgresMemoryStore)(nil)

// PostgresMemoryStore PostgreSQL 记忆存储实现
// 使用 tsvector + GIN 索引实现全文搜索，共享主 PostgresStore 的连接池
type PostgresMemoryStore struct {
	pool     *pgxpool.Pool
	logger   *zap.Logger
	vecStore VectorStore
	embedder EmbeddingProvider
	// embedding goroutine 并发控制
	embeddingSem chan struct{}
	embeddingWg  sync.WaitGroup
}

// NewPostgresMemoryStore 创建 PostgreSQL 记忆存储
func NewPostgresMemoryStore(pool *pgxpool.Pool, logger *zap.Logger) (*PostgresMemoryStore, error) {
	if pool == nil {
		return nil, errs.New(errs.CodeInvalidArgument, "连接池不能为空")
	}
	return &PostgresMemoryStore{pool: pool, logger: logger, embeddingSem: make(chan struct{}, 5)}, nil
}

// Save 保存新记忆，返回分配的 ID
func (s *PostgresMemoryStore) Save(ctx context.Context, record *MemoryRecord) (int64, error) {
	if record == nil {
		return 0, errs.New(errs.CodeInvalidInput, "记忆记录不能为空")
	}
	if record.Content == "" {
		return 0, errs.New(errs.CodeInvalidInput, "记忆内容不能为空")
	}
	if record.Type == "" {
		record.Type = MemoryTypeUser
	}
	if !ValidMemoryTypes[record.Type] {
		return 0, errs.New(errs.CodeInvalidInput, fmt.Sprintf("无效的记忆类型: %s", record.Type))
	}

	tagsJSON, err := json.Marshal(record.Tags)
	if err != nil {
		tagsJSON = []byte("[]")
	}

	metadataJSON := record.Metadata
	if metadataJSON == nil {
		metadataJSON = json.RawMessage("{}")
	}

	var id int64
	err = s.pool.QueryRow(ctx,
		`INSERT INTO memories (type, content, tags, session_id, metadata, user_id)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		string(record.Type), record.Content, string(tagsJSON), record.SessionID, string(metadataJSON), record.UserID,
	).Scan(&id)
	if err != nil {
		return 0, errs.Wrap(errs.CodeMemoryWriteFailed, "保存记忆失败", err)
	}

	s.logger.Debug("保存记忆成功", zap.Int64("id", id), zap.String("type", string(record.Type)))

	// 异步生成 embedding（不阻塞主流程，使用独立 ctx，不受 caller ctx 取消影响）
	if s.embedder != nil && s.vecStore != nil {
		s.embeddingWg.Add(1)
		go func() {
			defer s.embeddingWg.Done()
			// 用信号量限制并发数
			timer := time.NewTimer(5 * time.Minute)
			select {
			case s.embeddingSem <- struct{}{}:
				timer.Stop()
				defer func() { <-s.embeddingSem }()
			case <-timer.C:
				// 兜底：信号量满时最多等 5 分钟，避免 goroutine 泄漏
				return
			}
			s.generateEmbedding(context.Background(), id, record.Content)
		}()
	}

	return id, nil
}

// Get 按 ID 获取记忆（同时更新 accessed_at 和 access_count）
func (s *PostgresMemoryStore) Get(ctx context.Context, id int64) (*MemoryRecord, error) {
	userID := auth.UserIDFrom(ctx)
	var row pgx.Row
	if userID != "" {
		row = s.pool.QueryRow(ctx,
			`UPDATE memories
			 SET accessed_at = NOW(), access_count = access_count + 1
			 WHERE id = $1 AND user_id = $2
			 RETURNING id, user_id, type, content, tags, session_id, metadata,
			           created_at, updated_at, accessed_at, access_count`, id, userID)
	} else {
		row = s.pool.QueryRow(ctx,
			`UPDATE memories
			 SET accessed_at = NOW(), access_count = access_count + 1
			 WHERE id = $1
			 RETURNING id, user_id, type, content, tags, session_id, metadata,
			           created_at, updated_at, accessed_at, access_count`, id)
	}

	record, err := scanPgMemoryRecord(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errs.New(errs.CodeMemoryNotFound, fmt.Sprintf("记忆 %d 未找到", id))
		}
		return nil, errs.Wrap(errs.CodeMemoryReadFailed, "读取记忆失败", err)
	}
	return record, nil
}

// Update 更新已有记忆的内容、标签和元数据
func (s *PostgresMemoryStore) Update(ctx context.Context, record *MemoryRecord) error {
	if record == nil || record.ID == 0 {
		return errs.New(errs.CodeInvalidInput, "记忆记录或 ID 无效")
	}

	tagsJSON, err := json.Marshal(record.Tags)
	if err != nil {
		tagsJSON = []byte("[]")
	}

	metadataJSON, err := s.metadataForUpdate(ctx, record)
	if err != nil {
		return err
	}

	var ct pgconn.CommandTag
	if record.UserID != "" {
		ct, err = s.pool.Exec(ctx,
			`UPDATE memories SET content = $1, tags = $2, metadata = $3, updated_at = NOW()
			 WHERE id = $4 AND user_id = $5`,
			record.Content, string(tagsJSON), string(metadataJSON), record.ID, record.UserID,
		)
	} else {
		ct, err = s.pool.Exec(ctx,
			`UPDATE memories SET content = $1, tags = $2, metadata = $3, updated_at = NOW()
			 WHERE id = $4`,
			record.Content, string(tagsJSON), string(metadataJSON), record.ID,
		)
	}
	if err != nil {
		return errs.Wrap(errs.CodeMemoryWriteFailed, "更新记忆失败", err)
	}
	if ct.RowsAffected() == 0 {
		return errs.New(errs.CodeMemoryNotFound, fmt.Sprintf("记忆 %d 未找到", record.ID))
	}

	s.logger.Debug("更新记忆成功", zap.Int64("id", record.ID))

	// 异步重新生成 embedding（使用独立 ctx，不受 caller ctx 取消影响）
	if s.embedder != nil && s.vecStore != nil {
		s.embeddingWg.Add(1)
		go func() {
			defer s.embeddingWg.Done()
			// 用信号量限制并发数
			timer := time.NewTimer(5 * time.Minute)
			select {
			case s.embeddingSem <- struct{}{}:
				timer.Stop()
				defer func() { <-s.embeddingSem }()
			case <-timer.C:
				return
			}
			s.generateEmbedding(context.Background(), record.ID, record.Content)
		}()
	}

	return nil
}

func (s *PostgresMemoryStore) metadataForUpdate(ctx context.Context, record *MemoryRecord) (json.RawMessage, error) {
	var existing string
	var err error
	if record.UserID != "" {
		err = s.pool.QueryRow(ctx, `SELECT metadata FROM memories WHERE id = $1 AND user_id = $2`, record.ID, record.UserID).Scan(&existing)
	} else {
		err = s.pool.QueryRow(ctx, `SELECT metadata FROM memories WHERE id = $1`, record.ID).Scan(&existing)
	}
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, errs.New(errs.CodeMemoryNotFound, fmt.Sprintf("记忆 %d 未找到", record.ID))
		}
		return nil, errs.Wrap(errs.CodeMemoryReadFailed, "读取记忆元数据失败", err)
	}

	metadataJSON := PreserveGovernanceOnUpdate(json.RawMessage(existing), record.Metadata)
	if metadataJSON == nil {
		metadataJSON = json.RawMessage("{}")
	}
	return metadataJSON, nil
}

// Delete 删除记忆
func (s *PostgresMemoryStore) Delete(ctx context.Context, id int64) error {
	userID := auth.UserIDFrom(ctx)
	var ct pgconn.CommandTag
	var err error
	if userID != "" {
		ct, err = s.pool.Exec(ctx, `DELETE FROM memories WHERE id = $1 AND user_id = $2`, id, userID)
	} else {
		ct, err = s.pool.Exec(ctx, `DELETE FROM memories WHERE id = $1`, id)
	}
	if err != nil {
		return errs.Wrap(errs.CodeMemoryWriteFailed, "删除记忆失败", err)
	}
	if ct.RowsAffected() == 0 {
		return errs.New(errs.CodeMemoryNotFound, fmt.Sprintf("记忆 %d 未找到", id))
	}

	// 同步删除向量索引
	if s.vecStore != nil {
		_ = s.vecStore.Remove(ctx, id)
	}

	s.logger.Debug("删除记忆成功", zap.Int64("id", id))
	return nil
}

// SetEmbedding 设置向量搜索组件（启用 embedding 后调用）
func (s *PostgresMemoryStore) SetEmbedding(embedder EmbeddingProvider, vecStore VectorStore) {
	s.embedder = embedder
	s.vecStore = vecStore
}

// generateEmbedding 生成并保存 embedding 向量（后台执行）
func (s *PostgresMemoryStore) generateEmbedding(ctx context.Context, id int64, content string) {
	vectors, err := s.embedder.Embed(ctx, []string{content})
	if err != nil {
		s.logger.Debug("生成 embedding 失败", zap.Int64("id", id), zap.Error(err))
		return
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return
	}

	vec := vectors[0]
	blob := encodeFloat32s(vec)

	// 双写：BYTEA 列（向后兼容）
	_, err = s.pool.Exec(ctx,
		`UPDATE memories SET embedding = $1 WHERE id = $2`, blob, id)
	if err != nil {
		s.logger.Debug("保存 embedding 失败", zap.Int64("id", id), zap.Error(err))
		return
	}

	// 写入 VectorStore（InMemoryVecStore 或 PgVectorStore）
	if err := s.vecStore.Add(ctx, id, vec); err != nil {
		s.logger.Debug("写入向量索引失败", zap.Int64("id", id), zap.Error(err))
	}
	s.logger.Debug("embedding 已生成并保存", zap.Int64("id", id), zap.Int("dim", len(vec)))
}

func (s *PostgresMemoryStore) SyncMemoryEmbedding(ctx context.Context, memoryID int64, vector []float32, status MemoryEmbeddingStatus) error {
	if memoryID == 0 {
		return errs.New(errs.CodeInvalidInput, "记忆 ID 不能为空")
	}
	if len(vector) == 0 {
		return errs.New(errs.CodeInvalidInput, "embedding vector 不能为空")
	}
	if status.VectorSpace == "" {
		status.VectorSpace = DefaultVectorSpaceName
	}
	if status.EmbeddingState == "" {
		status.EmbeddingState = EmbeddingStateReady
	}
	if status.UpdatedAt.IsZero() {
		status.UpdatedAt = time.Now()
	}
	blob := encodeFloat32s(vector)
	var existing string
	if err := s.pool.QueryRow(ctx, `SELECT metadata FROM memories WHERE id=$1`, memoryID).Scan(&existing); err != nil {
		if err == pgx.ErrNoRows {
			return errs.New(errs.CodeMemoryNotFound, fmt.Sprintf("记忆 %d 未找到", memoryID))
		}
		return errs.Wrap(errs.CodeMemoryReadFailed, "读取记忆元数据失败", err)
	}
	meta := EncodeVectorSpace(json.RawMessage(existing), VectorSpaceMetadata{
		Name:           status.VectorSpace,
		EmbeddingState: status.EmbeddingState,
		MigratedAt:     status.UpdatedAt,
	})
	if _, err := s.pool.Exec(ctx, `UPDATE memories SET embedding=$1, metadata=$2, updated_at=NOW() WHERE id=$3`, blob, string(meta), memoryID); err != nil {
		return errs.Wrap(errs.CodeMemoryWriteFailed, "同步 embedding 失败", err)
	}
	if s.vecStore != nil {
		if err := s.vecStore.Add(ctx, memoryID, vector); err != nil {
			return err
		}
	}
	return nil
}

// Search 全文搜索记忆（tsvector + ts_rank 排序，中文回退 ILIKE）
func (s *PostgresMemoryStore) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	if opts.Query == "" {
		return &SearchResult{}, nil
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}

	// 第一步：尝试 tsvector 全文搜索
	memories, err := s.searchTSVector(ctx, opts, limit)
	if err != nil {
		s.logger.Debug("tsvector 搜索失败，回退到 ILIKE", zap.Error(err))
	}

	// 第二步：如果 tsvector 无结果，回退到 ILIKE 模糊搜索（支持中文）
	if len(memories) == 0 {
		memories, err = s.searchILike(ctx, opts, limit)
		if err != nil {
			return nil, errs.Wrap(errs.CodeMemorySearchFailed, "搜索记忆失败", err)
		}
	}

	return &SearchResult{
		Memories: memories,
		Total:    len(memories),
	}, nil
}

// searchTSVector 使用 tsvector + ts_rank 搜索
func (s *PostgresMemoryStore) searchTSVector(ctx context.Context, opts SearchOptions, limit int) ([]MemoryRecord, error) {
	// 构建 tsquery：将用户输入按空格拆分为 & 连接的词
	tsQuery := buildTSQuery(opts.Query)

	query := `SELECT id, user_id, type, content, tags, session_id, metadata,
		created_at, updated_at, accessed_at, access_count,
		ts_rank(search_vector, to_tsquery('simple', $1)) AS score
	FROM memories
	WHERE search_vector @@ to_tsquery('simple', $1)`

	args := []any{tsQuery}
	argIdx := 2

	// 用户隔离过滤
	if opts.UserID != "" {
		query += fmt.Sprintf(" AND user_id = $%d", argIdx)
		args = append(args, opts.UserID)
		argIdx++
	}
	// 可选过滤
	if opts.Type != "" {
		query += fmt.Sprintf(" AND type = $%d", argIdx)
		args = append(args, string(opts.Type))
		argIdx++
	}
	if opts.SessionID != "" {
		query += fmt.Sprintf(" AND session_id = $%d", argIdx)
		args = append(args, opts.SessionID)
		argIdx++
	}
	if len(opts.Tags) > 0 {
		for _, tag := range opts.Tags {
			query += fmt.Sprintf(" AND tags ILIKE $%d", argIdx)
			args = append(args, "%"+tag+"%")
			argIdx++
		}
	}

	query += fmt.Sprintf(" ORDER BY score DESC LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPgSearchRows(rows, true)
}

// searchILike 使用 ILIKE 模糊搜索（中文回退方案）
func (s *PostgresMemoryStore) searchILike(ctx context.Context, opts SearchOptions, limit int) ([]MemoryRecord, error) {
	query := `SELECT id, user_id, type, content, tags, session_id, metadata,
		created_at, updated_at, accessed_at, access_count
	FROM memories WHERE TRUE`

	var args []any
	argIdx := 1

	// 用户隔离过滤
	if opts.UserID != "" {
		query += fmt.Sprintf(" AND user_id = $%d", argIdx)
		args = append(args, opts.UserID)
		argIdx++
	}
	// 将查询拆分为关键词，每个关键词都必须匹配（AND 逻辑）
	keywords := strings.Fields(opts.Query)
	for _, kw := range keywords {
		query += fmt.Sprintf(" AND content ILIKE $%d", argIdx)
		args = append(args, "%"+kw+"%")
		argIdx++
	}

	if opts.Type != "" {
		query += fmt.Sprintf(" AND type = $%d", argIdx)
		args = append(args, string(opts.Type))
		argIdx++
	}
	if opts.SessionID != "" {
		query += fmt.Sprintf(" AND session_id = $%d", argIdx)
		args = append(args, opts.SessionID)
		argIdx++
	}
	if len(opts.Tags) > 0 {
		for _, tag := range opts.Tags {
			query += fmt.Sprintf(" AND tags ILIKE $%d", argIdx)
			args = append(args, "%"+tag+"%")
			argIdx++
		}
	}

	query += fmt.Sprintf(" ORDER BY accessed_at DESC LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPgSearchRows(rows, false)
}

// List 列出记忆
func (s *PostgresMemoryStore) List(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	query := `SELECT id, user_id, type, content, tags, session_id, metadata,
		created_at, updated_at, accessed_at, access_count
	FROM memories WHERE TRUE`

	var args []any
	argIdx := 1

	// 用户隔离过滤
	if opts.UserID != "" {
		query += fmt.Sprintf(" AND user_id = $%d", argIdx)
		args = append(args, opts.UserID)
		argIdx++
	}
	if opts.Type != "" {
		query += fmt.Sprintf(" AND type = $%d", argIdx)
		args = append(args, string(opts.Type))
		argIdx++
	}
	if opts.SessionID != "" {
		query += fmt.Sprintf(" AND session_id = $%d", argIdx)
		args = append(args, opts.SessionID)
		argIdx++
	}
	if len(opts.Tags) > 0 {
		for _, tag := range opts.Tags {
			query += fmt.Sprintf(" AND tags ILIKE $%d", argIdx)
			args = append(args, "%"+tag+"%")
			argIdx++
		}
	}

	query += " ORDER BY accessed_at DESC"

	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	query += fmt.Sprintf(" LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, errs.Wrap(errs.CodeMemoryReadFailed, "列出记忆失败", err)
	}
	defer rows.Close()

	memories, err := scanPgSearchRows(rows, false)
	if err != nil {
		return nil, err
	}

	// 获取总数（同步 user_id 过滤，避免泄露跨用户计数）
	var total int
	countQuery := `SELECT COUNT(*) FROM memories WHERE TRUE`
	var countArgs []any
	countArgIdx := 1
	if opts.UserID != "" {
		countQuery += fmt.Sprintf(" AND user_id = $%d", countArgIdx)
		countArgs = append(countArgs, opts.UserID)
		countArgIdx++
	}
	if opts.Type != "" {
		countQuery += fmt.Sprintf(" AND type = $%d", countArgIdx)
		countArgs = append(countArgs, string(opts.Type))
		countArgIdx++
	}
	if opts.SessionID != "" {
		countQuery += fmt.Sprintf(" AND session_id = $%d", countArgIdx)
		countArgs = append(countArgs, opts.SessionID)
	}
	_ = s.pool.QueryRow(ctx, countQuery, countArgs...).Scan(&total)

	return &SearchResult{
		Memories: memories,
		Total:    total,
	}, nil
}

// Stats 返回记忆统计信息
func (s *PostgresMemoryStore) Stats(ctx context.Context) (*MemoryStats, error) {
	stats := &MemoryStats{
		ByType: make(map[string]int),
	}

	userID := auth.UserIDFrom(ctx)
	var rows pgx.Rows
	var err error
	if userID != "" {
		rows, err = s.pool.Query(ctx, `SELECT type, COUNT(*) FROM memories WHERE user_id = $1 GROUP BY type`, userID)
	} else {
		rows, err = s.pool.Query(ctx, `SELECT type, COUNT(*) FROM memories GROUP BY type`)
	}
	if err != nil {
		return nil, errs.Wrap(errs.CodeMemoryReadFailed, "统计记忆失败", err)
	}
	defer rows.Close()

	for rows.Next() {
		var memType string
		var count int
		if err := rows.Scan(&memType, &count); err != nil {
			continue
		}
		stats.ByType[memType] = count
		stats.Total += count
	}

	if userID != "" {
		_ = s.pool.QueryRow(ctx, `SELECT MIN(created_at)::text FROM memories WHERE user_id = $1`, userID).Scan(&stats.OldestAt)
		_ = s.pool.QueryRow(ctx, `SELECT MAX(created_at)::text FROM memories WHERE user_id = $1`, userID).Scan(&stats.NewestAt)
	} else {
		_ = s.pool.QueryRow(ctx, `SELECT MIN(created_at)::text FROM memories`).Scan(&stats.OldestAt)
		_ = s.pool.QueryRow(ctx, `SELECT MAX(created_at)::text FROM memories`).Scan(&stats.NewestAt)
	}

	return stats, nil
}

func (s *PostgresMemoryStore) GovernanceStats(ctx context.Context, opts GovernanceQueryOptions) (GovernanceStats, error) {
	search := opts.Search
	if search.Limit <= 0 {
		search.Limit = 1000
	}
	result, err := s.List(ctx, search)
	if err != nil {
		return GovernanceStats{}, err
	}
	return AnalyzeGovernance(result.Memories, opts.Now, opts.MinConfidence), nil
}

func (s *PostgresMemoryStore) PruneGovernance(ctx context.Context, plan GovernancePrunePlan) (GovernancePruneResult, error) {
	result := GovernancePruneResult{
		Matched:   len(plan.DeleteIDs),
		DeleteIDs: append([]int64(nil), plan.DeleteIDs...),
		Reasons:   plan.Reasons,
	}
	for _, id := range plan.DeleteIDs {
		if err := s.Delete(ctx, id); err != nil {
			return result, err
		}
		result.Deleted++
	}
	return result, nil
}

// Close 释放存储资源（不关闭共享连接池）
func (s *PostgresMemoryStore) Close() error {
	// 等待所有 embedding goroutine 完成
	s.embeddingWg.Wait()
	return nil
}

// BackfillEmbeddingVec 将历史 BYTEA 向量迁移到 embedding_vec 列
// 分批处理，避免大表锁。返回迁移的记录数。
func (s *PostgresMemoryStore) BackfillEmbeddingVec(ctx context.Context, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	var total int
	for {
		rows, err := s.pool.Query(ctx,
			`SELECT id, embedding FROM memories
			 WHERE embedding IS NOT NULL AND embedding_vec IS NULL
			 LIMIT $1`, batchSize)
		if err != nil {
			return total, err
		}

		batch := 0
		for rows.Next() {
			var id int64
			var blob []byte
			if err := rows.Scan(&id, &blob); err != nil {
				continue
			}
			vec := decodeFloat32s(blob)
			if len(vec) == 0 {
				continue
			}
			vecStr := float32sToVecLiteral(vec)
			if _, err := s.pool.Exec(ctx,
				`UPDATE memories SET embedding_vec = $1::vector WHERE id = $2`,
				vecStr, id); err != nil {
				s.logger.Warn("回填向量失败", zap.Int64("id", id), zap.Error(err))
				continue
			}
			batch++
		}
		rows.Close()

		total += batch
		if batch == 0 {
			break
		}
		s.logger.Info("回填进度", zap.Int("batch", batch), zap.Int("total", total))
	}
	return total, nil
}

// buildTSQuery 将用户输入转换为 tsquery 格式
// "hello world" -> "hello & world"
func buildTSQuery(input string) string {
	words := strings.Fields(input)
	if len(words) == 0 {
		return ""
	}

	// 转义特殊字符，用 & 连接
	escaped := make([]string, 0, len(words))
	for _, w := range words {
		// 去除 tsquery 特殊字符
		w = strings.NewReplacer(
			"&", "", "|", "", "!", "", "(", "", ")", "",
			"<", "", ">", "", "'", "", ":", "",
		).Replace(w)
		w = strings.TrimSpace(w)
		if w != "" {
			escaped = append(escaped, "'"+w+"'")
		}
	}
	return strings.Join(escaped, " & ")
}

// scanPgMemoryRecord 从单行查询结果扫描记忆记录
func scanPgMemoryRecord(row pgx.Row) (*MemoryRecord, error) {
	var (
		id          int64
		userID      string
		memType     string
		content     string
		tags        string
		sessionID   string
		metadata    string
		createdAt   time.Time
		updatedAt   time.Time
		accessedAt  time.Time
		accessCount int
	)

	err := row.Scan(&id, &userID, &memType, &content, &tags, &sessionID, &metadata,
		&createdAt, &updatedAt, &accessedAt, &accessCount)
	if err != nil {
		return nil, err
	}

	record := &MemoryRecord{
		ID:          id,
		UserID:      userID,
		Type:        MemoryType(memType),
		Content:     content,
		SessionID:   sessionID,
		AccessCount: accessCount,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		AccessedAt:  accessedAt,
	}
	_ = json.Unmarshal([]byte(tags), &record.Tags)
	if metadata != "" {
		record.Metadata = json.RawMessage(metadata)
	}
	return record, nil
}

// scanPgSearchRows 扫描搜索结果行
func scanPgSearchRows(rows pgx.Rows, hasScore bool) ([]MemoryRecord, error) {
	var memories []MemoryRecord
	for rows.Next() {
		var (
			id          int64
			userID      string
			memType     string
			content     string
			tags        string
			sessionID   string
			metadata    string
			createdAt   time.Time
			updatedAt   time.Time
			accessedAt  time.Time
			accessCount int
			score       float64
		)

		var err error
		if hasScore {
			err = rows.Scan(&id, &userID, &memType, &content, &tags, &sessionID, &metadata,
				&createdAt, &updatedAt, &accessedAt, &accessCount, &score)
		} else {
			err = rows.Scan(&id, &userID, &memType, &content, &tags, &sessionID, &metadata,
				&createdAt, &updatedAt, &accessedAt, &accessCount)
		}
		if err != nil {
			continue
		}

		record := MemoryRecord{
			ID:          id,
			UserID:      userID,
			Type:        MemoryType(memType),
			Content:     content,
			SessionID:   sessionID,
			Score:       score,
			AccessCount: accessCount,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			AccessedAt:  accessedAt,
		}
		_ = json.Unmarshal([]byte(tags), &record.Tags)
		if metadata != "" {
			record.Metadata = json.RawMessage(metadata)
		}
		memories = append(memories, record)
	}
	return memories, nil
}
