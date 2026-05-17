// Package master — event_claim.go 实现 P0-#8 两阶段事件认领（two-phase claim）。
//
// # 红队闭环
//
// 旧 dedup 行为：`Seen(eventID)` 返回 dup → 跳过；返回 fresh → 处理。
// 红队链 D：业务在"标记 seen"之后崩溃前未真正完成 → 重启后此 eventID 被永久标记为已处理 → 卡死的事件再也不会重试 → 用户消息黑洞。
//
// 两阶段语义：
//
//	Phase 1: ClaimEvent(eventID, lease) → (token, ok)
//	  - ok=true：本进程成功"占住"该 eventID，但状态 = claimed（in-flight，**未**完成）
//	  - ok=false：另一个 worker 已 claim（或事件已 complete）
//
//	Phase 2: CompleteEvent(eventID, token)
//	  - 把状态从 claimed → completed，dedup 才正式生效（后续 ClaimEvent 持续返回 ok=false）
//
//	Reclaim(now)：lease 过期（默认 30s）的 claimed 事件被回收，由 ReclaimWorker 重新派回 retry_queue。
//
// 红队 mutation：把 CompleteEvent 调用直接删掉 → reclaim worker 在 lease 过期后必然
// 重新拿到该 eventID → 单测可以断言"同一 eventID 在崩溃后仍能被新 worker 拿到 lease"。
package master

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// DefaultClaimLease 是 P0-#8 默认 lease 时长。
// 30s 与任务说明一致：足够覆盖正常处理时长（debounce 2s + LLM 5–10s），但不会让真崩溃事件长时间卡住。
const DefaultClaimLease = 30 * time.Second

// DefaultReclaimInterval 是 ReclaimWorker 默认轮询间隔。
const DefaultReclaimInterval = 5 * time.Second

// ClaimToken 是 ClaimEvent 颁发的不透明 token。
// 携带 eventID、issued-at、随机 nonce，用于 CompleteEvent 防止"完成别人正在重新 claim 的事件"。
type ClaimToken struct {
	EventID  string
	Nonce    uint64
	IssuedAt time.Time
}

// ClaimState 记录某个 eventID 的当前状态，便于运维快速定位。
type ClaimState int

const (
	ClaimStateUnknown   ClaimState = iota // 不存在
	ClaimStateClaimed                     // 有 worker 占住，未完成
	ClaimStateCompleted                   // 已完成（dedup 生效）
)

// EventClaimer 是 P0-#8 的最小契约。
//
// 不变量：
//   - ClaimEvent 在状态=Completed 时返回 ok=false（dedup 生效）
//   - ClaimEvent 在状态=Claimed 且 lease 未过期时返回 ok=false
//   - ClaimEvent 在状态=Claimed 且 lease 过期时**直接 reclaim 并返回新 token**（救活孤立 claim）
//   - CompleteEvent 必须用 ClaimEvent 颁发的 token 调用；token 不匹配返回 ErrClaimTokenMismatch
//   - Reclaim 由后台 worker 周期性调用，把过期 claim 写入 onReclaim 回调
type EventClaimer interface {
	ClaimEvent(eventID string, lease time.Duration) (ClaimToken, bool)
	CompleteEvent(token ClaimToken) error
	State(eventID string) ClaimState
	Reclaim(now time.Time) []ClaimToken
}

// ErrClaimTokenMismatch 表示 CompleteEvent 收到的 token 与当前持有人不一致。
// 可能原因：worker 提交太晚（lease 已过期 + 被 reclaim + 新 worker 拿到）；不应当作 retry 触发器，仅日志即可。
var ErrClaimTokenMismatch = errors.New("event_claim: token mismatch (lease expired and reclaimed?)")

// ErrClaimNotFound 表示 CompleteEvent 收到一个未知的 eventID（已 expire 出窗）。
var ErrClaimNotFound = errors.New("event_claim: event not found (already evicted?)")

// MemoryEventClaimer 默认内存实现。
//
// 适用场景：单进程；多实例需要 Redis/DB 后端，但接口不变。
//
// 状态机：
//
//	ClaimEvent(fresh)             → claimed{token:T1}
//	CompleteEvent(T1)             → completed
//	ClaimEvent(claimed:T1, expired)→ reclaim → claimed{token:T2}（旧 T1 永远 mismatch）
//	ClaimEvent(completed)         → ok=false（dedup 生效）
//
// 完成后的 eventID 在 completedTTL 之后被 GC，避免无限增长。
type MemoryEventClaimer struct {
	mu sync.Mutex

	claimed   map[string]*claimEntry // claimed/in-flight
	completed map[string]time.Time   // completed eventID → 完成时间，用于 GC
	logger    *zap.Logger

	completedTTL time.Duration
	nonce        atomic.Uint64
	now          func() time.Time
}

type claimEntry struct {
	token   ClaimToken
	expires time.Time
}

// NewMemoryEventClaimer 创建内存 claimer。
//   - completedTTL <= 0 → 默认 1h（覆盖飞书重试窗口）
//   - logger nil → Nop
//   - now nil → time.Now（测试可注入固定 now）
func NewMemoryEventClaimer(completedTTL time.Duration, logger *zap.Logger) *MemoryEventClaimer {
	if completedTTL <= 0 {
		completedTTL = time.Hour
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &MemoryEventClaimer{
		claimed:      make(map[string]*claimEntry),
		completed:    make(map[string]time.Time),
		logger:       logger,
		completedTTL: completedTTL,
		now:          time.Now,
	}
}

// SetNow 注入测试用时钟。
func (c *MemoryEventClaimer) SetNow(fn func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = fn
}

// ClaimEvent 实现两阶段第一步。
func (c *MemoryEventClaimer) ClaimEvent(eventID string, lease time.Duration) (ClaimToken, bool) {
	if eventID == "" {
		return ClaimToken{}, false
	}
	if lease <= 0 {
		lease = DefaultClaimLease
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.gcCompletedLocked(now)

	if _, done := c.completed[eventID]; done {
		// dedup 生效
		return ClaimToken{}, false
	}
	if existing, ok := c.claimed[eventID]; ok {
		if existing.expires.After(now) {
			// 仍在 lease 内：拒绝
			return ClaimToken{}, false
		}
		// lease 已过期 → 自动 reclaim：旧 token 永久失效，重新颁发新 token。
		c.logger.Warn("event_claim 自动 reclaim 过期 claim",
			zap.String("event_id", eventID),
			zap.Time("prev_expires", existing.expires))
	}
	tok := ClaimToken{
		EventID:  eventID,
		Nonce:    c.nonce.Add(1),
		IssuedAt: now,
	}
	c.claimed[eventID] = &claimEntry{
		token:   tok,
		expires: now.Add(lease),
	}
	return tok, true
}

// CompleteEvent 实现两阶段第二步。
func (c *MemoryEventClaimer) CompleteEvent(token ClaimToken) error {
	if token.EventID == "" {
		return ErrClaimNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.claimed[token.EventID]
	if !ok {
		return ErrClaimNotFound
	}
	if entry.token.Nonce != token.Nonce {
		return ErrClaimTokenMismatch
	}
	delete(c.claimed, token.EventID)
	c.completed[token.EventID] = c.now()
	return nil
}

// State 返回当前 eventID 的状态（运维/测试用）。
func (c *MemoryEventClaimer) State(eventID string) ClaimState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.completed[eventID]; ok {
		return ClaimStateCompleted
	}
	if _, ok := c.claimed[eventID]; ok {
		return ClaimStateClaimed
	}
	return ClaimStateUnknown
}

// Reclaim 把 lease 过期的 claim 全部摘除并返回旧 token 列表。
// 调用方（典型为 ReclaimWorker）应据此把对应事件重新派回 retry_queue 触发再处理。
//
// 不变量：返回的 token 之后**绝不**会出现在任何 ClaimEvent 颁发结果里——它们已经死了。
func (c *MemoryEventClaimer) Reclaim(now time.Time) []ClaimToken {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []ClaimToken
	for id, entry := range c.claimed {
		if entry.expires.Before(now) || entry.expires.Equal(now) {
			out = append(out, entry.token)
			delete(c.claimed, id)
			c.logger.Warn("event_claim Reclaim：lease 过期，事件回归待处理",
				zap.String("event_id", id),
				zap.Time("expired_at", entry.expires))
		}
	}
	return out
}

func (c *MemoryEventClaimer) gcCompletedLocked(now time.Time) {
	if c.completedTTL <= 0 {
		return
	}
	cutoff := now.Add(-c.completedTTL)
	for id, t := range c.completed {
		if t.Before(cutoff) {
			delete(c.completed, id)
		}
	}
}

// ReclaimWorker 是 P0-#8 后台 worker：
//   - 周期性调 EventClaimer.Reclaim
//   - 把回收的 token 通过 onReclaim 回调交给上层（典型路径：写入 retry_queue.RetryReasonClaimLost）
//
// 调用方负责传 stop channel 或 context 控制生命周期。
type ReclaimWorker struct {
	claimer   EventClaimer
	interval  time.Duration
	logger    *zap.Logger
	onReclaim func(token ClaimToken)
	stopCh    chan struct{}
	stopOnce  sync.Once
	doneCh    chan struct{}
	now       func() time.Time
}

// NewReclaimWorker 构造 worker。interval<=0 取默认 5s；onReclaim nil 时仅日志。
func NewReclaimWorker(claimer EventClaimer, interval time.Duration, onReclaim func(token ClaimToken), logger *zap.Logger) *ReclaimWorker {
	if interval <= 0 {
		interval = DefaultReclaimInterval
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ReclaimWorker{
		claimer:   claimer,
		interval:  interval,
		logger:    logger,
		onReclaim: onReclaim,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		now:       time.Now,
	}
}

// SetNow 注入测试时钟。
func (w *ReclaimWorker) SetNow(fn func() time.Time) {
	w.now = fn
}

// Start 在 goroutine 中开始轮询。可幂等多次调用（仅第一次生效）。
func (w *ReclaimWorker) Start() {
	go w.run()
}

func (w *ReclaimWorker) run() {
	defer close(w.doneCh)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-t.C:
			w.tick()
		}
	}
}

// tick 执行一次 reclaim 扫描；导出便于测试不开 worker 也能断言。
func (w *ReclaimWorker) tick() {
	tokens := w.claimer.Reclaim(w.now())
	for _, tok := range tokens {
		w.logger.Warn("ReclaimWorker 发现孤立 claim，回派 retry_queue",
			zap.String("event_id", tok.EventID),
			zap.Uint64("nonce", tok.Nonce),
			zap.Time("issued_at", tok.IssuedAt))
		if w.onReclaim != nil {
			w.onReclaim(tok)
		}
	}
}

// Tick 公开 wrapper，单测/运维可以手动驱动一次扫描。
func (w *ReclaimWorker) Tick() {
	w.tick()
}

// Stop 优雅停 worker。幂等。
func (w *ReclaimWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	<-w.doneCh
}
