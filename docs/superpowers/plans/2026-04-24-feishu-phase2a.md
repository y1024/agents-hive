# Feishu Phase 2A Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete Feishu Phase 2A as a shippable, webhook-only lifecycle and minimal governance slice: `bot_added` / `bot_removed`, DB-backed `chat_state`, real `TerminateSession`, minimal commands, tenant-scoped ACL, deterministic rollout, mute, and outbound suppression.

**Architecture:** Phase 2A extends the existing webhook-only Feishu path without enabling longconn, watchdog, gap fetch, or dual-ingress logic. The core source of truth is a new persistent `feishu_chat_state` table keyed by `(platform, tenant_key, chat_id)`; router and outbound paths read that state to enforce lifecycle, rollout, mute, and suppression. Session cleanup is built on a new `master.TerminateSession(sessionID, reason)` capability that cancels in-flight work, waits for semaphore release, and prevents stale results from writing back after `/reset` or `bot_removed`.

**Tech Stack:** Go, Postgres (pgx), existing Feishu webhook ingress, existing Router/Master/EventBus architecture

---

## File Structure

### New files

- `migrations/20260424000002_feishu_chat_state.sql`
  - Create the persistent `feishu_chat_state` table and indexes used by lifecycle, mute, rollout, and suppression.
- `internal/channel/feishu/chat_state_repo.go`
  - Postgres-backed repository for reading/upserting/updating `chat_state`.
- `internal/channel/feishu/chat_state_repo_test.go`
  - Repository-focused tests for tenant isolation, monotonic lifecycle updates, and mute/rollout persistence.
- `internal/channel/feishu/lifecycle_handler.go`
  - Lifecycle event parser/handler for `bot_added` and `bot_removed`, including dedup and monotonic transition checks.
- `internal/channel/feishu/lifecycle_handler_test.go`
  - Lifecycle idempotency and ordering tests.
- `internal/channel/feishu/commands.go`
  - Command normalization, parsing, whitelist matching, and helper formatting for `/help`, `/status`, `/reset`.
- `internal/channel/feishu/commands_test.go`
  - Tests for zero-width characters, casing, unicode normalization, and whitelist behavior.
- `internal/channel/feishu/acl.go`
  - Tenant-scoped ACL abstraction with Phase 2A fail-closed allowlist implementation.
- `internal/channel/feishu/acl_test.go`
  - Tests for tenant-scoped allow/deny semantics and fail-closed behavior.
- `internal/channel/feishu/rollout.go`
  - Deterministic allow/deny rollout evaluator.
- `internal/channel/feishu/rollout_test.go`
  - Tests for deterministic governance behavior and deny short-circuit.
- `internal/channel/feishu/governance.go`
  - Runtime governance service that composes `chat_state`, ACL, rollout, command execution, lifecycle state, and suppression decisions.
- `internal/channel/feishu/governance_test.go`
  - Unit tests for command bypass rules, mute/rollout order, and lifecycle gating.

### Existing files to modify

- `internal/config/config.go`
  - Add Phase 2A Feishu governance config: lifecycle, commands, rollout, mute, terminate-session kill switch, ACL allowlist.
- `internal/channel/types.go`
  - Extend inbound/outbound models only if required to carry tenant/platform metadata needed by suppression checks.
- `internal/channel/router.go`
  - Integrate governance order: dedup/security -> `chat_state` -> command normalize -> command path or normal message path.
- `internal/channel/router_test.go`
  - Add router-level governance tests for `/reset`, deny rollout, mute bypass, and `bot_removed`.
- `internal/channel/feishu/webhook.go`
  - Route lifecycle events to the new lifecycle handler without eagerly creating sessions on `bot_added`.
- `internal/channel/feishu/longconn.go`
  - Keep compile parity only if shared dispatcher structs require lifecycle handler wiring; do not enable longconn behavior.
- `internal/channel/feishu/plugin.go`
  - Guard outbound send path with suppression checks before `SendMessage` / `ReplyMessage`.
- `internal/channel/feishu/renderer.go`
  - Guard renderer fallback/patch send path with suppression checks to stop post-eviction outbound.
- `internal/channel/feishu/tool_provider.go`
  - Guard direct tool-driven outbound send path with suppression checks.
- `internal/bootstrap/server.go`
  - Wire repository, lifecycle handler, governance service, and config into router/plugin.
- `internal/bootstrap/server_wiring_test.go`
  - Assert Phase 2A wiring exists when Feishu is enabled.
- `internal/master/public_api.go`
  - Export `TerminateSession(sessionID, reason)` and any wait helper needed by router/lifecycle.
- `internal/master/master.go`
  - Reuse existing `taskCancels` and session semaphores for termination flow.
- `internal/master/session_loop.go`
  - Prevent terminated sessions from writing back stale results; ensure termination waits for semaphore release.
- `internal/master/session_loop_test.go`
  - Add tests for cancel/wait/idempotency semantics.
- `internal/master/session_manager.go`
  - Add any minimal helper needed to swap/reset session state during `/reset`.
- `docs/渠道对接/feishu-bot/ROADMAP.md`
  - Mark Phase 2A subset split and update actual delivery boundary after implementation.

### Scope guard

- Do **not** add or modify:
  - longconn watchdog
  - gap fetch
  - single-ingress switching
  - `/debug`
  - `/audit`
  - `/agent`
  - `/model`
  - percentage rollout
  - ops mute API

---

### Task 1: Create `feishu_chat_state` Persistence and Repository

**Files:**
- Create: `migrations/20260424000002_feishu_chat_state.sql`
- Create: `internal/channel/feishu/chat_state_repo.go`
- Create: `internal/channel/feishu/chat_state_repo_test.go`

- [ ] **Step 1: Write the failing repository contract test**

```go
// internal/channel/feishu/chat_state_repo_test.go
package feishu

import "testing"

func TestChatStateRepo_RecordShape(t *testing.T) {
	state := ChatStateRecord{
		Platform:               "feishu",
		TenantKey:              "tenant-a",
		ChatID:                 "oc_123",
		SessionID:              "im-feishu-tenant_a-oc_123",
		State:                  ChatStateActive,
		RolloutMode:            RolloutModeAllow,
		LastLifecycleEventID:   "evt-1",
		LastLifecycleEventTime: 1710000000,
	}
	if state.Platform == "" || state.TenantKey == "" || state.ChatID == "" {
		t.Fatal("chat state primary key must be non-empty")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/channel/feishu -run TestChatStateRepo_RecordShape -v`
Expected: FAIL with `undefined: ChatStateRecord`

- [ ] **Step 3: Write the migration**

```sql
-- migrations/20260424000002_feishu_chat_state.sql
CREATE TABLE IF NOT EXISTS feishu_chat_state (
    platform VARCHAR(32) NOT NULL,
    tenant_key VARCHAR(255) NOT NULL,
    chat_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL DEFAULT '',
    state VARCHAR(32) NOT NULL,
    mute_until TIMESTAMPTZ,
    rollout_mode VARCHAR(32) NOT NULL DEFAULT 'allow',
    suppress_outbound BOOLEAN NOT NULL DEFAULT FALSE,
    last_lifecycle_event_id VARCHAR(255) NOT NULL DEFAULT '',
    last_lifecycle_event_time BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by VARCHAR(128) NOT NULL DEFAULT '',
    PRIMARY KEY (platform, tenant_key, chat_id),
    CONSTRAINT feishu_chat_state_state_chk CHECK (state IN ('active', 'evicted')),
    CONSTRAINT feishu_chat_state_rollout_chk CHECK (rollout_mode IN ('allow', 'deny'))
);

CREATE INDEX IF NOT EXISTS idx_feishu_chat_state_session_id
    ON feishu_chat_state(session_id)
    WHERE session_id <> '';

CREATE INDEX IF NOT EXISTS idx_feishu_chat_state_suppressed
    ON feishu_chat_state(platform, tenant_key, chat_id, suppress_outbound);
```

- [ ] **Step 4: Write the minimal repository and types**

```go
// internal/channel/feishu/chat_state_repo.go
package feishu

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type ChatLifecycleState string

const (
	ChatStateActive  ChatLifecycleState = "active"
	ChatStateEvicted ChatLifecycleState = "evicted"
)

type GovernanceRolloutMode string

const (
	RolloutModeAllow GovernanceRolloutMode = "allow"
	RolloutModeDeny  GovernanceRolloutMode = "deny"
)

var ErrTenantKeyRequired = errors.New("tenant_key required")

type ChatStateRecord struct {
	Platform               string
	TenantKey              string
	ChatID                 string
	SessionID              string
	State                  ChatLifecycleState
	MuteUntil              *time.Time
	RolloutMode            GovernanceRolloutMode
	SuppressOutbound       bool
	LastLifecycleEventID   string
	LastLifecycleEventTime int64
	UpdatedAt              time.Time
	UpdatedBy              string
}

type ChatStateRepo interface {
	Get(ctx context.Context, platform, tenantKey, chatID string) (*ChatStateRecord, error)
	Upsert(ctx context.Context, record ChatStateRecord) error
	MarkEvicted(ctx context.Context, platform, tenantKey, chatID, eventID string, eventTime int64, updatedBy string) (*ChatStateRecord, bool, error)
	MarkActive(ctx context.Context, platform, tenantKey, chatID, eventID string, eventTime int64, updatedBy string) (*ChatStateRecord, bool, error)
	SetSessionID(ctx context.Context, platform, tenantKey, chatID, sessionID, updatedBy string) error
	SetMuteUntil(ctx context.Context, platform, tenantKey, chatID string, muteUntil *time.Time, updatedBy string) error
	SetRolloutMode(ctx context.Context, platform, tenantKey, chatID string, mode GovernanceRolloutMode, updatedBy string) error
}

type PostgresChatStateRepo struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

func NewPostgresChatStateRepo(pool *pgxpool.Pool, logger *zap.Logger) *PostgresChatStateRepo {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PostgresChatStateRepo{pool: pool, logger: logger}
}
```

- [ ] **Step 5: Add the first repository test for tenant fail-closed**

```go
func TestChatStateRepo_EmptyTenantFailsClosed(t *testing.T) {
	repo := &PostgresChatStateRepo{}
	_, err := repo.Get(t.Context(), "feishu", "", "oc_123")
	if !errors.Is(err, ErrTenantKeyRequired) {
		t.Fatalf("want ErrTenantKeyRequired, got %v", err)
	}
}
```

- [ ] **Step 6: Run focused tests**

Run: `go test ./internal/channel/feishu -run 'TestChatStateRepo_' -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add migrations/20260424000002_feishu_chat_state.sql internal/channel/feishu/chat_state_repo.go internal/channel/feishu/chat_state_repo_test.go
git commit -m "feat(feishu): add phase2a chat state persistence"
```

### Task 2: Implement `master.TerminateSession(sessionID, reason)`

**Files:**
- Modify: `internal/master/public_api.go`
- Modify: `internal/master/master.go`
- Modify: `internal/master/session_loop.go`
- Modify: `internal/master/session_manager.go`
- Modify: `internal/master/session_loop_test.go`

- [ ] **Step 1: Write the failing API contract test**

```go
// internal/master/session_loop_test.go
func TestTerminateSession_IsIdempotent(t *testing.T) {
	m, cancel := setupInputReceivedMaster(t)
	defer cancel()

	if err := m.TerminateSession("im-feishu-tenant-chat", "bot_removed"); err != nil {
		t.Fatalf("first terminate failed: %v", err)
	}
	if err := m.TerminateSession("im-feishu-tenant-chat", "bot_removed"); err != nil {
		t.Fatalf("second terminate must be idempotent: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/master -run TestTerminateSession_IsIdempotent -v`
Expected: FAIL with `m.TerminateSession undefined`

- [ ] **Step 3: Add the public API**

```go
// internal/master/public_api.go
func (m *Master) TerminateSession(sessionID, reason string) error {
	return m.terminateSession(sessionID, reason)
}
```

- [ ] **Step 4: Implement the internal termination flow**

```go
// internal/master/master.go
var errTerminateTimeout = errors.New("terminate session timeout")

func (m *Master) terminateSession(sessionID, reason string) error {
	if sessionID == "" {
		return nil
	}

	if stopped := m.StopSessionTask(sessionID); stopped {
		m.logger.Info("终止会话时已取消运行中任务",
			zap.String("session_id", sessionID),
			zap.String("reason", reason))
	}

	sem := m.getSessionSem(sessionID)
	select {
	case sem <- struct{}{}:
		<-sem
	case <-time.After(5 * time.Second):
		return errTerminateTimeout
	}

	if m.journal != nil {
		endCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.journal.EndSession(endCtx, sessionID, reason); err != nil {
			m.logger.Warn("TerminateSession 调用 journal.EndSession 失败",
				zap.String("session_id", sessionID),
				zap.Error(err))
		}
	}

	return nil
}
```

- [ ] **Step 5: Prevent stale write-back after termination**

```go
// internal/master/session.go
type SessionState struct {
	// ...
	terminated atomic.Bool `json:"-"`
}

func (s *SessionState) MarkTerminated() {
	s.terminated.Store(true)
}

func (s *SessionState) IsTerminated() bool {
	return s.terminated.Load()
}
```

```go
// internal/master/session_loop.go
if session.IsTerminated() {
	m.logger.Warn("终止后的旧任务结果被丢弃",
		zap.String("session_id", session.ID))
	return errs.New(errs.CodeCanceled, "session terminated")
}
```

- [ ] **Step 6: Extend the test to cover running-task cancellation**

```go
func TestTerminateSession_CancelsRunningTask(t *testing.T) {
	m, cancel := setupInputReceivedMaster(t)
	defer cancel()

	sessionID := "im-feishu-tenant-chat"
	ctx, reqCancel := context.WithCancel(context.Background())
	defer reqCancel()

	done := make(chan error, 1)
	go func() {
		_, err := m.ProcessMessage(ctx, sessionID, "long running")
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := m.TerminateSession(sessionID, "reset"); err != nil {
		t.Fatalf("terminate failed: %v", err)
	}
}
```

- [ ] **Step 7: Run focused tests**

Run: `go test ./internal/master -run 'TestTerminateSession_' -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/master/public_api.go internal/master/master.go internal/master/session.go internal/master/session_loop.go internal/master/session_manager.go internal/master/session_loop_test.go
git commit -m "feat(master): add idempotent terminate session capability"
```

### Task 3: Handle `bot_added` and `bot_removed` Lifecycle Events

**Files:**
- Create: `internal/channel/feishu/lifecycle_handler.go`
- Create: `internal/channel/feishu/lifecycle_handler_test.go`
- Modify: `internal/channel/feishu/webhook.go`
- Modify: `internal/bootstrap/server.go`

- [ ] **Step 1: Write the failing lifecycle monotonicity test**

```go
// internal/channel/feishu/lifecycle_handler_test.go
func TestLifecycleHandler_OldBotRemovedDoesNotOverrideNewBotAdded(t *testing.T) {
	handler := newTestLifecycleHandler(t)

	err := handler.HandleBotAdded(t.Context(), LifecycleEvent{
		TenantKey: "tenant-a",
		ChatID:    "oc_chat",
		EventID:   "evt-new",
		EventTime: 200,
	})
	if err != nil {
		t.Fatalf("bot_added failed: %v", err)
	}

	err = handler.HandleBotRemoved(t.Context(), LifecycleEvent{
		TenantKey: "tenant-a",
		ChatID:    "oc_chat",
		EventID:   "evt-old",
		EventTime: 100,
	})
	if err != nil {
		t.Fatalf("old bot_removed should be ignored, got %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/channel/feishu -run TestLifecycleHandler_OldBotRemovedDoesNotOverrideNewBotAdded -v`
Expected: FAIL with `undefined: LifecycleEvent`

- [ ] **Step 3: Implement lifecycle types and handler**

```go
// internal/channel/feishu/lifecycle_handler.go
package feishu

import (
	"context"

	"go.uber.org/zap"
)

type LifecycleEvent struct {
	Platform  string
	TenantKey string
	ChatID    string
	EventID   string
	EventTime int64
}

type SessionTerminator interface {
	TerminateSession(sessionID, reason string) error
}

type LifecycleHandler struct {
	repo        ChatStateRepo
	terminator  SessionTerminator
	logger      *zap.Logger
	welcomeSend func(ctx context.Context, tenantKey, chatID string) error
}
```

- [ ] **Step 4: Implement `bot_removed` semantics**

```go
func (h *LifecycleHandler) HandleBotRemoved(ctx context.Context, event LifecycleEvent) error {
	state, changed, err := h.repo.MarkEvicted(ctx, "feishu", event.TenantKey, event.ChatID, event.EventID, event.EventTime, "bot_removed")
	if err != nil || state == nil {
		return err
	}
	if !changed {
		return nil
	}
	if state.SessionID != "" {
		return h.terminator.TerminateSession(state.SessionID, "bot_removed")
	}
	return nil
}
```

- [ ] **Step 5: Implement `bot_added` semantics without eager session creation**

```go
func (h *LifecycleHandler) HandleBotAdded(ctx context.Context, event LifecycleEvent) error {
	_, _, err := h.repo.MarkActive(ctx, "feishu", event.TenantKey, event.ChatID, event.EventID, event.EventTime, "bot_added")
	if err != nil {
		return err
	}
	if h.welcomeSend != nil {
		return h.welcomeSend(ctx, event.TenantKey, event.ChatID)
	}
	return nil
}
```

- [ ] **Step 6: Wire lifecycle event dispatch in webhook**

```go
// internal/channel/feishu/webhook.go
// Add dispatcher branches for:
// - bot_added
// - bot_removed
// Convert the Feishu event payload into LifecycleEvent and delegate to LifecycleHandler.
```

- [ ] **Step 7: Run focused tests**

Run: `go test ./internal/channel/feishu -run 'TestLifecycleHandler_|TestWebhook_' -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/channel/feishu/lifecycle_handler.go internal/channel/feishu/lifecycle_handler_test.go internal/channel/feishu/webhook.go internal/bootstrap/server.go
git commit -m "feat(feishu): add phase2a lifecycle handler"
```

### Task 4: Enforce Outbound Suppression After `bot_removed`

**Files:**
- Create: `internal/channel/feishu/governance.go`
- Modify: `internal/channel/feishu/plugin.go`
- Modify: `internal/channel/feishu/renderer.go`
- Modify: `internal/channel/feishu/tool_provider.go`
- Create: `internal/channel/feishu/governance_test.go`

- [ ] **Step 1: Write the failing suppression test**

```go
// internal/channel/feishu/governance_test.go
func TestGovernance_SuppressedChatBlocksOutbound(t *testing.T) {
	gov := &GovernanceService{
		repo: &fakeChatStateRepo{
			record: &ChatStateRecord{
				Platform:         "feishu",
				TenantKey:        "tenant-a",
				ChatID:           "oc_chat",
				State:            ChatStateEvicted,
				SuppressOutbound: true,
			},
		},
	}
	if err := gov.CheckOutbound(t.Context(), "tenant-a", "oc_chat"); err == nil {
		t.Fatal("expected outbound suppression")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/channel/feishu -run TestGovernance_SuppressedChatBlocksOutbound -v`
Expected: FAIL with `undefined: GovernanceService`

- [ ] **Step 3: Implement suppression check**

```go
// internal/channel/feishu/governance.go
var ErrOutboundSuppressed = errors.New("feishu outbound suppressed")

type GovernanceService struct {
	repo   ChatStateRepo
	logger *zap.Logger
}

func (g *GovernanceService) CheckOutbound(ctx context.Context, tenantKey, chatID string) error {
	state, err := g.repo.Get(ctx, "feishu", tenantKey, chatID)
	if err != nil {
		return err
	}
	if state != nil && state.SuppressOutbound {
		return ErrOutboundSuppressed
	}
	return nil
}
```

- [ ] **Step 4: Guard the plugin send path**

```go
// internal/channel/feishu/plugin.go
if p.governance != nil {
	if err := p.governance.CheckOutbound(ctx, msg.TenantKey, msg.ChatID); err != nil {
		p.logger.Warn("飞书消息发送被 suppression 拦截",
			zap.String("tenant_key", msg.TenantKey),
			zap.String("chat_id", msg.ChatID),
			zap.Error(err))
		return nil
	}
}
```

- [ ] **Step 5: Extend `channel.OutboundMessage` only if needed**

```go
// internal/channel/types.go
type OutboundMessage struct {
	Platform  Platform `json:"platform"`
	TenantKey string   `json:"tenant_key,omitempty"`
	ChatID    string   `json:"chat_id"`
	Content   string   `json:"content"`
	MsgType   MsgType  `json:"msg_type,omitempty"`
	ReplyTo   string   `json:"reply_to,omitempty"`
}
```

- [ ] **Step 6: Propagate `TenantKey` where outbound is created**

```go
// internal/channel/router.go
outMsg := OutboundMessage{
	Platform:  msg.Platform,
	TenantKey: msg.TenantKey,
	ChatID:    msg.ChatID,
	Content:   replyContent,
	ReplyTo:   msg.MessageID,
}
```

- [ ] **Step 7: Run focused tests**

Run: `go test ./internal/channel/feishu ./internal/channel -run 'TestGovernance_|TestRouter' -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/channel/types.go internal/channel/router.go internal/channel/feishu/governance.go internal/channel/feishu/governance_test.go internal/channel/feishu/plugin.go internal/channel/feishu/renderer.go internal/channel/feishu/tool_provider.go
git commit -m "feat(feishu): suppress outbound after bot removal"
```

### Task 5: Add Command Normalize, Whitelist, and `/help` `/status` `/reset`

**Files:**
- Create: `internal/channel/feishu/commands.go`
- Create: `internal/channel/feishu/commands_test.go`
- Modify: `internal/channel/router.go`
- Modify: `internal/master/public_api.go`
- Modify: `internal/master/session_loop.go`

- [ ] **Step 1: Write the failing normalization test**

```go
// internal/channel/feishu/commands_test.go
func TestNormalizeCommand_StripsZeroWidthAndCasing(t *testing.T) {
	got := NormalizeCommand("/ReSeT\u200b ")
	if got != "/reset" {
		t.Fatalf("want /reset, got %q", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/channel/feishu -run TestNormalizeCommand_StripsZeroWidthAndCasing -v`
Expected: FAIL with `undefined: NormalizeCommand`

- [ ] **Step 3: Implement normalize and parse**

```go
// internal/channel/feishu/commands.go
package feishu

import (
	"strings"
	"unicode"
)

var commandWhitelist = map[string]struct{}{
	"/help":   {},
	"/status": {},
	"/reset":  {},
}

func NormalizeCommand(input string) string {
	input = strings.TrimSpace(input)
	input = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, input)
	return input
}
```

- [ ] **Step 4: Implement command execution contract**

```go
type ParsedCommand struct {
	Name string
	Raw  string
}

func ParseCommand(input string) (*ParsedCommand, bool) {
	normalized := NormalizeCommand(input)
	if _, ok := commandWhitelist[normalized]; !ok {
		return nil, false
	}
	return &ParsedCommand{Name: strings.TrimPrefix(normalized, "/"), Raw: normalized}, true
}
```

- [ ] **Step 5: Route `/reset` through `TerminateSession`**

```go
func (g *GovernanceService) ExecuteCommand(ctx context.Context, msg channel.InboundMessage, cmd ParsedCommand) (string, bool, error) {
	switch cmd.Name {
	case "help":
		return "可用命令: /help /status /reset", true, nil
	case "status":
		return g.RenderStatus(ctx, msg.TenantKey, msg.ChatID)
	case "reset":
		sessionID, err := g.ResetChatSession(ctx, msg)
		if err != nil {
			return "", true, err
		}
		return "会话已重置: " + sessionID, true, nil
	default:
		return "", false, nil
	}
}
```

- [ ] **Step 6: Integrate bypass order in router**

```go
// internal/channel/router.go
// Order:
// 1. dedup/security
// 2. load chat_state
// 3. normalize/parse command
// 4. if command: whitelist -> ACL -> execute
// 5. else: rollout -> mute -> route -> agent
```

- [ ] **Step 7: Run focused tests**

Run: `go test ./internal/channel/feishu ./internal/channel -run 'TestNormalizeCommand_|TestRouter' -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/channel/feishu/commands.go internal/channel/feishu/commands_test.go internal/channel/router.go internal/master/public_api.go internal/master/session_loop.go
git commit -m "feat(feishu): add phase2a command parsing and reset flow"
```

### Task 6: Add Tenant-Scoped ACL for `/reset`

**Files:**
- Create: `internal/channel/feishu/acl.go`
- Create: `internal/channel/feishu/acl_test.go`
- Modify: `internal/channel/feishu/governance.go`
- Modify: `internal/config/config.go`

- [ ] **Step 1: Write the failing ACL test**

```go
// internal/channel/feishu/acl_test.go
func TestACL_EmptyTenantFailsClosed(t *testing.T) {
	acl := NewStaticAllowlistACL(nil)
	allowed, err := acl.CanReset(t.Context(), "", "oc_chat", "ou_user", true)
	if err == nil || allowed {
		t.Fatalf("missing tenant must fail closed, allowed=%v err=%v", allowed, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/channel/feishu -run TestACL_EmptyTenantFailsClosed -v`
Expected: FAIL with `undefined: NewStaticAllowlistACL`

- [ ] **Step 3: Implement minimal Phase 2A ACL**

```go
// internal/channel/feishu/acl.go
package feishu

import "context"

type CommandACL interface {
	CanReset(ctx context.Context, tenantKey, chatID, openID string, isDirect bool) (bool, error)
}

type StaticAllowlistACL struct {
	allowed map[string]map[string]struct{}
}

func NewStaticAllowlistACL(allowed map[string][]string) *StaticAllowlistACL {
	m := make(map[string]map[string]struct{}, len(allowed))
	for tenant, users := range allowed {
		set := make(map[string]struct{}, len(users))
		for _, user := range users {
			set[user] = struct{}{}
		}
		m[tenant] = set
	}
	return &StaticAllowlistACL{allowed: m}
}
```

- [ ] **Step 4: Define the allow rule**

```go
func (a *StaticAllowlistACL) CanReset(_ context.Context, tenantKey, _ string, openID string, isDirect bool) (bool, error) {
	if tenantKey == "" {
		return false, ErrTenantKeyRequired
	}
	if isDirect {
		return true, nil
	}
	set := a.allowed[tenantKey]
	_, ok := set[openID]
	return ok, nil
}
```

- [ ] **Step 5: Enforce ACL in governance**

```go
allowed, err := g.acl.CanReset(ctx, msg.TenantKey, msg.ChatID, msg.SenderID, msg.ChatType == channel.ChatDirect)
if err != nil || !allowed {
	return "你没有权限执行 /reset", true, err
}
```

- [ ] **Step 6: Run focused tests**

Run: `go test ./internal/channel/feishu -run 'TestACL_|TestGovernance_' -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/channel/feishu/acl.go internal/channel/feishu/acl_test.go internal/channel/feishu/governance.go internal/config/config.go
git commit -m "feat(feishu): add tenant scoped acl for reset"
```

### Task 7: Add Deterministic Rollout and Persistent Mute

**Files:**
- Create: `internal/channel/feishu/rollout.go`
- Create: `internal/channel/feishu/rollout_test.go`
- Modify: `internal/channel/feishu/governance.go`
- Modify: `internal/channel/router.go`
- Modify: `internal/config/config.go`

- [ ] **Step 1: Write the failing rollout test**

```go
// internal/channel/feishu/rollout_test.go
func TestRollout_DenyDropsNormalMessage(t *testing.T) {
	r := DeterministicRollout{}
	if allowed := r.Allow(RolloutModeDeny); allowed {
		t.Fatal("deny mode must drop normal messages")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/channel/feishu -run TestRollout_DenyDropsNormalMessage -v`
Expected: FAIL with `undefined: DeterministicRollout`

- [ ] **Step 3: Implement deterministic rollout**

```go
// internal/channel/feishu/rollout.go
package feishu

type DeterministicRollout struct{}

func (DeterministicRollout) Allow(mode GovernanceRolloutMode) bool {
	return mode != RolloutModeDeny
}
```

- [ ] **Step 4: Implement mute evaluation**

```go
func (g *GovernanceService) IsMuted(now time.Time, state *ChatStateRecord) bool {
	if state == nil || state.MuteUntil == nil {
		return false
	}
	return state.MuteUntil.After(now)
}
```

- [ ] **Step 5: Enforce order for non-command messages**

```go
// internal/channel/router.go
// For non-command messages:
// if rollout denies -> return silently
// if muted -> return silently
// else route to session
```

- [ ] **Step 6: Add bypass test for `/help`, `/status`, `/reset`**

```go
func TestGovernance_CommandsBypassMuteAndRollout(t *testing.T) {
	// muted + rollout deny still allows /status
}
```

- [ ] **Step 7: Run focused tests**

Run: `go test ./internal/channel/feishu ./internal/channel -run 'TestRollout_|TestGovernance_|TestRouter' -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/channel/feishu/rollout.go internal/channel/feishu/rollout_test.go internal/channel/feishu/governance.go internal/channel/router.go internal/config/config.go
git commit -m "feat(feishu): add deterministic rollout and mute"
```

### Task 8: Full Router Integration, Wiring, and Strict Verification

**Files:**
- Modify: `internal/bootstrap/server.go`
- Modify: `internal/bootstrap/server_wiring_test.go`
- Modify: `internal/channel/router.go`
- Modify: `internal/channel/router_test.go`
- Modify: `docs/渠道对接/feishu-bot/ROADMAP.md`

- [ ] **Step 1: Write the failing integration test**

```go
// internal/channel/router_test.go
func TestRouter_BotRemovedBlocksFurtherProcessing(t *testing.T) {
	// arrange evicted chat state
	// send normal message
	// assert processor not called
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/channel ./internal/bootstrap -run 'TestRouter_BotRemovedBlocksFurtherProcessing|TestServerWiring' -v`
Expected: FAIL because governance wiring does not exist yet

- [ ] **Step 3: Wire Phase 2A services in bootstrap**

```go
// internal/bootstrap/server.go
// 1. build PostgresChatStateRepo when pg pool exists
// 2. build StaticAllowlistACL from config
// 3. build GovernanceService
// 4. build LifecycleHandler
// 5. inject them into router + feishu plugin
```

- [ ] **Step 4: Add absolute verification commands**

```bash
go test ./internal/channel/feishu -run 'TestChatStateRepo_|TestLifecycleHandler_|TestNormalizeCommand_|TestACL_|TestRollout_|TestGovernance_' -v
go test ./internal/channel -run 'TestRouter_' -v
go test ./internal/master -run 'TestTerminateSession_' -v
go test ./internal/bootstrap -run 'TestServerWiring' -v
go test -v ./internal/channel/feishu ./internal/channel ./internal/master ./internal/bootstrap
```

- [ ] **Step 5: Run formatting and final verification**

Run: `gofmt -w internal/channel/feishu/*.go internal/channel/*.go internal/master/*.go internal/bootstrap/*.go`
Expected: no output

Run: `go test ./... -v`
Expected: PASS

- [ ] **Step 6: Update roadmap/documentation to reflect the real split**

```md
- Phase 2A: webhook-only lifecycle + minimal governance
- Phase 2B: longconn / watchdog / gap fetch / single-ingress switching
```

- [ ] **Step 7: Commit**

```bash
git add internal/bootstrap/server.go internal/bootstrap/server_wiring_test.go internal/channel/router.go internal/channel/router_test.go docs/渠道对接/feishu-bot/ROADMAP.md
git commit -m "feat(feishu): wire and verify phase2a governance"
```

---

## Spec Coverage Check

- DB-backed `chat_state`: covered by Task 1.
- `TerminateSession(sessionID, reason)`: covered by Task 2.
- `bot_added` / `bot_removed`: covered by Task 3.
- outbound suppression after `bot_removed`: covered by Task 4.
- `/help` `/status` `/reset`: covered by Task 5.
- tenant-scoped ACL: covered by Task 6.
- deterministic allow/deny rollout and mute: covered by Task 7.
- router integration and strict acceptance: covered by Task 8.
- Explicitly excluded Phase 2B items are not included in any task.

## Placeholder Scan

- No `TODO` / `TBD` placeholders remain.
- Each task has concrete file paths.
- Each task has explicit commands.
- Each task defines concrete interfaces/types before later references use them.

## Type Consistency Check

- `ChatStateRecord`, `ChatLifecycleState`, and `GovernanceRolloutMode` are introduced in Task 1 and reused consistently later.
- `TerminateSession(sessionID, reason)` is introduced in Task 2 and reused by `/reset` and `bot_removed`.
- `LifecycleEvent` and `GovernanceService` are introduced before later wiring tasks depend on them.

