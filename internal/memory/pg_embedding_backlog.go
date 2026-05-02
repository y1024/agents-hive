package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGEmbeddingBacklog struct {
	pool *pgxpool.Pool
}

func NewPGEmbeddingBacklog(pool *pgxpool.Pool) *PGEmbeddingBacklog {
	return &PGEmbeddingBacklog{pool: pool}
}

func (b *PGEmbeddingBacklog) Enqueue(ctx context.Context, job EmbeddingBacklogJob) (int64, error) {
	if b == nil || b.pool == nil {
		return 0, fmt.Errorf("embedding backlog not configured")
	}
	if job.MemoryID == 0 {
		return 0, errors.New("memory id is required")
	}
	if strings.TrimSpace(job.Content) == "" {
		return 0, errors.New("content is required")
	}
	now := job.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	if job.NextRunAt.IsZero() {
		job.NextRunAt = now
	}
	if job.VectorSpace == "" {
		job.VectorSpace = DefaultVectorSpaceName
	}
	var id int64
	err := b.pool.QueryRow(ctx, `
INSERT INTO embedding_backlog
	(memory_id, user_id, content, vector_space, status, attempts, claimed_by, claimed_at, next_run_at, last_error, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
RETURNING id`,
		job.MemoryID,
		strings.TrimSpace(job.UserID),
		job.Content,
		job.VectorSpace,
		EmbeddingBacklogStatusPending,
		0,
		"",
		nullableTime(job.ClaimedAt),
		job.NextRunAt,
		"",
		now,
		job.UpdatedAt,
	).Scan(&id)
	return id, err
}

func (b *PGEmbeddingBacklog) ClaimNext(ctx context.Context, workerID string, now time.Time) (*EmbeddingBacklogJob, error) {
	if b == nil || b.pool == nil {
		return nil, fmt.Errorf("embedding backlog not configured")
	}
	if now.IsZero() {
		now = time.Now()
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var job EmbeddingBacklogJob
	row := tx.QueryRow(ctx, `
SELECT id, memory_id, user_id, content, vector_space, status, attempts, created_at, updated_at, claimed_by, claimed_at, next_run_at, last_error
FROM embedding_backlog
WHERE (
	status IN ($1, $2)
	OR (status = $3 AND (claimed_at IS NULL OR claimed_at <= $4))
) AND next_run_at <= $5
ORDER BY next_run_at ASC, id ASC
FOR UPDATE SKIP LOCKED
LIMIT 1`,
		EmbeddingBacklogStatusPending,
		EmbeddingBacklogStatusFailed,
		EmbeddingBacklogStatusClaimed,
		now.Add(-EmbeddingBacklogClaimLease),
		now,
	)
	if err := scanEmbeddingBacklogJob(row, &job); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE embedding_backlog
SET status=$2, claimed_by=$3, claimed_at=$4, updated_at=$4
WHERE id=$1`,
		job.ID,
		EmbeddingBacklogStatusClaimed,
		strings.TrimSpace(workerID),
		now,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	job.Status = EmbeddingBacklogStatusClaimed
	job.ClaimedBy = strings.TrimSpace(workerID)
	job.ClaimedAt = now
	job.UpdatedAt = now
	return &job, nil
}

func (b *PGEmbeddingBacklog) MarkDone(ctx context.Context, id int64, now time.Time) error {
	if b == nil || b.pool == nil {
		return fmt.Errorf("embedding backlog not configured")
	}
	if now.IsZero() {
		now = time.Now()
	}
	ct, err := b.pool.Exec(ctx, `
UPDATE embedding_backlog
SET status=$2, updated_at=$3, next_run_at=$3, last_error=''
WHERE id=$1`,
		id,
		EmbeddingBacklogStatusDone,
		now,
	)
	return requireEmbeddingBacklogRow(ct, err)
}

func (b *PGEmbeddingBacklog) MarkFailed(ctx context.Context, id int64, jobErr error, nextRunAt time.Time, now time.Time) error {
	if b == nil || b.pool == nil {
		return fmt.Errorf("embedding backlog not configured")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if nextRunAt.IsZero() {
		nextRunAt = now
	}
	message := ""
	if jobErr != nil {
		message = jobErr.Error()
	}
	ct, err := b.pool.Exec(ctx, `
UPDATE embedding_backlog
SET status=$2, attempts=attempts+1, updated_at=$3, next_run_at=$4, last_error=$5
WHERE id=$1`,
		id,
		EmbeddingBacklogStatusFailed,
		now,
		nextRunAt,
		message,
	)
	return requireEmbeddingBacklogRow(ct, err)
}

func (b *PGEmbeddingBacklog) Stats(ctx context.Context) (EmbeddingBacklogStats, error) {
	if b == nil || b.pool == nil {
		return EmbeddingBacklogStats{}, fmt.Errorf("embedding backlog not configured")
	}
	rows, err := b.pool.Query(ctx, `SELECT status, COUNT(*) FROM embedding_backlog GROUP BY status`)
	if err != nil {
		return EmbeddingBacklogStats{}, err
	}
	defer rows.Close()
	stats := EmbeddingBacklogStats{ByState: map[EmbeddingBacklogStatus]int{}}
	for rows.Next() {
		var status EmbeddingBacklogStatus
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return EmbeddingBacklogStats{}, err
		}
		stats.ByState[status] = count
		stats.Total += count
	}
	if err := rows.Err(); err != nil {
		return EmbeddingBacklogStats{}, err
	}
	return stats, nil
}

func (b *PGEmbeddingBacklog) Get(ctx context.Context, id int64) (EmbeddingBacklogJob, bool, error) {
	if b == nil || b.pool == nil {
		return EmbeddingBacklogJob{}, false, fmt.Errorf("embedding backlog not configured")
	}
	var job EmbeddingBacklogJob
	row := b.pool.QueryRow(ctx, `
SELECT id, memory_id, user_id, content, vector_space, status, attempts, created_at, updated_at, claimed_by, claimed_at, next_run_at, last_error
FROM embedding_backlog
WHERE id=$1`, id)
	if err := scanEmbeddingBacklogJob(row, &job); err != nil {
		if err == pgx.ErrNoRows {
			return EmbeddingBacklogJob{}, false, nil
		}
		return EmbeddingBacklogJob{}, false, err
	}
	return job, true, nil
}

func requireEmbeddingBacklogRow(ct pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("embedding backlog job not found")
	}
	return nil
}

type embeddingBacklogScanner interface {
	Scan(dest ...any) error
}

func scanEmbeddingBacklogJob(row embeddingBacklogScanner, job *EmbeddingBacklogJob) error {
	var claimedAt *time.Time
	if err := row.Scan(
		&job.ID,
		&job.MemoryID,
		&job.UserID,
		&job.Content,
		&job.VectorSpace,
		&job.Status,
		&job.Attempts,
		&job.CreatedAt,
		&job.UpdatedAt,
		&job.ClaimedBy,
		&claimedAt,
		&job.NextRunAt,
		&job.LastError,
	); err != nil {
		return err
	}
	if claimedAt != nil {
		job.ClaimedAt = *claimedAt
	}
	return nil
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func sortEmbeddingBacklogJobsForTest(jobs []EmbeddingBacklogJob) {
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].ID < jobs[j].ID
	})
}
