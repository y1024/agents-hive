# Feishu Phase 0 Completion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the remaining missing items for Feishu Phase 0: Distributed Dedup (Postgres), TenantResolver/ClientRegistry stubs, and Retry Queue worker skeleton.

**Architecture:** 
- `DistributedDedup`: Replace `MemoryEventClaimer` with a Postgres-backed implementation satisfying `master.EventClaimer`. It uses two-phase claim (`claimed_at`, `processed_at`) to ensure no message is lost if a worker crashes before completion.
- `TenantResolver` & `ClientRegistry`: Create interface stubs that hardcode single-tenant logic for now, but lock in the architecture for M11 (Multi-tenant).
- `RetryQueue`: Create the SQL migration for `feishu_outbound_retry_queue` and a minimal worker loop skeleton to process failed messages.

**Tech Stack:** Go, Postgres (pgx), oapi-sdk-go

---

### Task 1: Create SQL Migrations for Dedup & Retry Queue

**Files:**
- Create: `migrations/20260423000000_feishu_dedup.sql`
- Create: `migrations/20260423000001_feishu_retry_queue.sql`

- [ ] **Step 1: Write dedup migration**

```sql
-- migrations/20260423000000_feishu_dedup.sql
CREATE TABLE IF NOT EXISTS feishu_event_dedup (
    event_id VARCHAR(255) PRIMARY KEY,
    claimed_at TIMESTAMP WITH TIME ZONE,
    processed BOOLEAN NOT NULL DEFAULT FALSE,
    processed_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX idx_feishu_dedup_claimed_unprocessed ON feishu_event_dedup(claimed_at) WHERE processed = FALSE;
```

- [ ] **Step 2: Write retry queue migration**

```sql
-- migrations/20260423000001_feishu_retry_queue.sql
CREATE TABLE IF NOT EXISTS feishu_outbound_retry_queue (
    id SERIAL PRIMARY KEY,
    message_id VARCHAR(255) NOT NULL,
    platform VARCHAR(50) NOT NULL,
    chat_id VARCHAR(255) NOT NULL,
    sender_id VARCHAR(255) NOT NULL,
    reason VARCHAR(100) NOT NULL,
    error_msg TEXT,
    payload JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    next_retry_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    retry_count INT DEFAULT 0
);
CREATE INDEX idx_feishu_retry_queue_next ON feishu_outbound_retry_queue(next_retry_at) WHERE retry_count < 5;
```

- [ ] **Step 3: Commit**

```bash
git add migrations/*.sql
git commit -m "feat(feishu): add migrations for distributed dedup and retry queue"
```

### Task 2: Implement Postgres EventClaimer (Distributed Dedup)

**Files:**
- Modify: `internal/channel/feishu/dedup.go` (if exists, else create)
- Test: `internal/channel/feishu/dedup_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/channel/feishu/dedup_test.go
package feishu

import (
	"testing"
	"time"

	"github.com/chef-guo/agents-hive/internal/master"
)

func TestPostgresEventClaimer_ImplementsInterface(t *testing.T) {
	var _ master.EventClaimer = (*PostgresEventClaimer)(nil)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/channel/feishu -run TestPostgresEventClaimer_ImplementsInterface`
Expected: FAIL with "undefined: PostgresEventClaimer"

- [ ] **Step 3: Write implementation**

```go
// internal/channel/feishu/dedup.go
package feishu

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/chef-guo/agents-hive/internal/master"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type PostgresEventClaimer struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

func NewPostgresEventClaimer(pool *pgxpool.Pool, logger *zap.Logger) *PostgresEventClaimer {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PostgresEventClaimer{pool: pool, logger: logger}
}

func (c *PostgresEventClaimer) ClaimEvent(eventID string, lease time.Duration) (master.ClaimToken, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond) // fail-closed short timeout
	defer cancel()

	var processed bool
	var claimedAt *time.Time
	err := c.pool.QueryRow(ctx, "SELECT processed, claimed_at FROM feishu_event_dedup WHERE event_id = $1", eventID).Scan(&processed, &claimedAt)
	if err == nil {
		if processed {
			return master.ClaimToken{}, false
		}
		if claimedAt != nil && time.Since(*claimedAt) < lease {
			return master.ClaimToken{}, false // Someone else is processing it
		}
	}

	tokenBytes := make([]byte, 16)
	rand.Read(tokenBytes)
	nonce := hex.EncodeToString(tokenBytes)

	now := time.Now()
	_, err = c.pool.Exec(ctx, `
		INSERT INTO feishu_event_dedup (event_id, claimed_at, processed) 
		VALUES ($1, $2, FALSE)
		ON CONFLICT (event_id) DO UPDATE 
		SET claimed_at = EXCLUDED.claimed_at 
		WHERE feishu_event_dedup.processed = FALSE AND (feishu_event_dedup.claimed_at IS NULL OR feishu_event_dedup.claimed_at < $3)
	`, eventID, now, now.Add(-lease))

	if err != nil {
		c.logger.Error("Failed to claim event (fail-closed)", zap.String("event_id", eventID), zap.Error(err))
		return master.ClaimToken{}, false // Fail-closed: if DB fails, we return false so Feishu retries later
	}

	return master.ClaimToken{
		EventID:  eventID,
		IssuedAt: now,
		Nonce:    nonce,
	}, true
}

func (c *PostgresEventClaimer) CompleteEvent(token master.ClaimToken) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	res, err := c.pool.Exec(ctx, `
		UPDATE feishu_event_dedup 
		SET processed = TRUE, processed_at = $1 
		WHERE event_id = $2 AND processed = FALSE
	`, time.Now(), token.EventID)

	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return master.ErrClaimTokenMismatch
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/channel/feishu -run TestPostgresEventClaimer_ImplementsInterface`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/channel/feishu/dedup*
git commit -m "feat(feishu): implement PostgresEventClaimer for distributed dedup (P0-#8, P0-#9)"
```

### Task 3: Implement Reclaim Worker

**Files:**
- Create: `internal/channel/feishu/reclaim_worker.go`

- [ ] **Step 1: Write implementation**

```go
// internal/channel/feishu/reclaim_worker.go
package feishu

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type ReclaimWorker struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
	stopCh chan struct{}
}

func NewReclaimWorker(pool *pgxpool.Pool, logger *zap.Logger) *ReclaimWorker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ReclaimWorker{
		pool:   pool,
		logger: logger,
		stopCh: make(chan struct{}),
	}
}

func (w *ReclaimWorker) Start() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-w.stopCh:
				return
			case <-ticker.C:
				w.reclaimStale()
			}
		}
	}()
}

func (w *ReclaimWorker) Stop() {
	close(w.stopCh)
}

func (w *ReclaimWorker) reclaimStale() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Clear claimed_at for claims older than 90s
	res, err := w.pool.Exec(ctx, `
		UPDATE feishu_event_dedup 
		SET claimed_at = NULL 
		WHERE processed = FALSE AND claimed_at < $1
	`, time.Now().Add(-90*time.Second))

	if err != nil {
		w.logger.Error("Failed to reclaim stale events", zap.Error(err))
		return
	}
	
	if rows := res.RowsAffected(); rows > 0 {
		w.logger.Info("Reclaimed stale events", zap.Int64("count", rows))
	}
}
```

- [ ] **Step 2: Check formatting and syntax**
Run: `go build ./internal/channel/feishu`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add internal/channel/feishu/reclaim_worker.go
git commit -m "feat(feishu): add ReclaimWorker to clear stale event claims (P0-#8)"
```

### Task 4: Implement TenantResolver & ClientRegistry (Single-tenant Stubs)

**Files:**
- Create: `internal/channel/feishu/tenant.go`
- Create: `internal/channel/feishu/client_registry.go`

- [ ] **Step 1: Write TenantResolver stub**

```go
// internal/channel/feishu/tenant.go
package feishu

// TenantResolver provides tenant context (Phase 0 stub).
// Used to strictly enforce multi-tenant prefix format im-feishu-{tenantKey}-{chatID}.
type TenantResolver interface {
	ResolveTenantKey() string
}

type SingleTenantResolver struct {
	AppID string // Defaults to the single AppID for Phase 0
}

func NewSingleTenantResolver(appID string) *SingleTenantResolver {
	return &SingleTenantResolver{AppID: appID}
}

func (r *SingleTenantResolver) ResolveTenantKey() string {
	return r.AppID
}
```

- [ ] **Step 2: Write ClientRegistry stub**

```go
// internal/channel/feishu/client_registry.go
package feishu

// ClientRegistry provides access to tenant-specific clients (Phase 0 stub).
type ClientRegistry interface {
	GetClient(tenantKey string) (*Client, error)
}

type SingleClientRegistry struct {
	client *Client
}

func NewSingleClientRegistry(client *Client) *SingleClientRegistry {
	return &SingleClientRegistry{client: client}
}

func (r *SingleClientRegistry) GetClient(tenantKey string) (*Client, error) {
	return r.client, nil
}
```

- [ ] **Step 3: Check formatting and syntax**
Run: `go build ./internal/channel/feishu`
Expected: Success

- [ ] **Step 4: Commit**

```bash
git add internal/channel/feishu/tenant.go internal/channel/feishu/client_registry.go
git commit -m "feat(feishu): add TenantResolver and ClientRegistry stubs for Phase 0 (P0-#10)"
```

### Task 5: Implement Retry Queue Worker Skeleton

**Files:**
- Create: `internal/channel/feishu/retry_queue.go`

- [ ] **Step 1: Write retry queue skeleton**

```go
// internal/channel/feishu/retry_queue.go
package feishu

import (
	"context"
	"time"

	"github.com/chef-guo/agents-hive/internal/channel"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type PostgresRetryQueue struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

func NewPostgresRetryQueue(pool *pgxpool.Pool, logger *zap.Logger) *PostgresRetryQueue {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PostgresRetryQueue{pool: pool, logger: logger}
}

func (q *PostgresRetryQueue) Enqueue(item channel.RetryItem) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := q.pool.Exec(ctx, `
		INSERT INTO feishu_outbound_retry_queue 
		(message_id, platform, chat_id, sender_id, reason, error_msg, payload, next_retry_at) 
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, item.MessageID, item.Platform, item.ChatID, item.SenderID, string(item.Reason), item.ErrorMsg, item.Payload, time.Now().Add(5*time.Minute))
	
	if err != nil {
		q.logger.Error("Failed to enqueue retry item", zap.String("msg_id", item.MessageID), zap.Error(err))
	}
	return err
}

type RetryQueueWorker struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
	stopCh chan struct{}
}

func NewRetryQueueWorker(pool *pgxpool.Pool, logger *zap.Logger) *RetryQueueWorker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &RetryQueueWorker{pool: pool, logger: logger, stopCh: make(chan struct{})}
}

func (w *RetryQueueWorker) Start() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-w.stopCh:
				return
			case <-ticker.C:
				w.processQueue()
			}
		}
	}()
}

func (w *RetryQueueWorker) Stop() {
	close(w.stopCh)
}

func (w *RetryQueueWorker) processQueue() {
	// Skeleton: actual processing will be fleshed out in Phase 5
	// This satisfies Phase 0 need for Enqueue (PostgresRetryQueue) 
	// while laying groundwork.
	w.logger.Debug("Retry queue worker tick")
}
```

- [ ] **Step 2: Check formatting and syntax**
Run: `go build ./internal/channel/feishu`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add internal/channel/feishu/retry_queue.go
git commit -m "feat(feishu): add PostgresRetryQueue and skeleton RetryQueueWorker (P0-#7)"
```
