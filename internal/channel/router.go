package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/imctx"
	"github.com/chef-guo/agents-hive/internal/master"
	"github.com/chef-guo/agents-hive/internal/observability"
)

// defaultTenantKey 是 IM 平台未传 tenant_key 时的回退值。
// 与 docs/渠道对接/feishu-bot/11-multi-tenant.md 一期约定一致：单租户场景
// 用 "default" 占位，避免改 BuildSessionID 的 4 段格式契约。
// 红队提醒：长期依赖 default 兜底 → 不同租户 session 串台 → 跨租户消息泄露。
const defaultTenantKey = "default"

func normalizeBindingTenantKey(tenantKey string) string {
	if tenantKey == "" {
		return defaultTenantKey
	}
	return tenantKey
}

func bindingKey(platform Platform, tenantKey, chatID string) string {
	return string(platform) + ":" + normalizeBindingTenantKey(tenantKey) + ":" + chatID
}

type imContextKey struct{}

type IMContextValue struct {
	SenderOpenID   string
	Platform       string
	ChatType       ChatType
	InternalUserID string
}

func IMContextFrom(ctx context.Context) (IMContextValue, bool) {
	if ctx == nil {
		return IMContextValue{}, false
	}
	v, ok := ctx.Value(imContextKey{}).(IMContextValue)
	return v, ok
}

// DedupBackend 是 P0-#9 抽象出的去重后端契约：
//
//	Check(ctx, id) → (dup, err)
//	  - dup=true：已见过此 id，调用方应跳过处理
//	  - err!=nil：后端故障；router 必须 fail-closed（视为 dup 拒绝处理）
//	    + 落 retry_queue（reason=dedup_backend）让运维感知 + 后续手工处理
//
// 红队：fail-open（即 err 时当作 fresh 继续处理）会让 dedup 后端故障的 N 秒里
// 同一条 webhook 重试包被处理 N 次（双扣费、双发卡）。fail-closed + retry_queue
// 才是合理的不变量——丢失少量条目优于双发。
//
// 单实例进程默认走 memoryDedupBackend（in-memory map，永远不超时不报错）；
// 多实例需要 Redis/etcd 后端时实现本接口替换即可。
type DedupBackend interface {
	Check(ctx context.Context, messageID string) (dup bool, err error)
	Stop()
}

// dedupTimeoutDefault P0-#9 不变量：dedup backend 必须 200ms 内回答。
// 多数缓存（in-memory、Redis 单 key GET）远小于此；超时 → 后端有故障，必须 fail-closed。
const dedupTimeoutDefault = 200 * time.Millisecond

// ErrDedupBackendDown 是 dedup backend 超时或返回故障时统一封装的 sentinel。
// router 通过 errors.Is 识别后写 RetryReasonDedupBackend。
var ErrDedupBackendDown = errors.New("dedup backend down (treated as duplicate, fail-closed)")

// messageDedup 简单的消息去重器，基于 MessageID 防止重复处理
type messageDedup struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	stopCh chan struct{}
}

func newMessageDedup() *messageDedup {
	d := &messageDedup{
		seen:   make(map[string]time.Time),
		stopCh: make(chan struct{}),
	}
	go d.cleanupLoop()
	return d
}

// cleanupLoop 后台清理过期条目，收到 stop 信号时退出
func (d *messageDedup) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.mu.Lock()
			now := time.Now()
			for id, ts := range d.seen {
				if now.Sub(ts) > 10*time.Minute {
					delete(d.seen, id)
				}
			}
			d.mu.Unlock()
		case <-d.stopCh:
			return
		}
	}
}

// stop 停止清理 goroutine
func (d *messageDedup) stop() {
	close(d.stopCh)
}

// isDuplicate 检查消息是否已处理过，返回 true 表示重复
func (d *messageDedup) isDuplicate(messageID string) bool {
	if messageID == "" {
		return false // 无 ID 的消息不去重
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.seen[messageID]; exists {
		return true
	}
	d.seen[messageID] = time.Now()
	return false
}

// memoryDedupBackend 把 messageDedup 包装成 DedupBackend 接口的 trivial 实现。
// 永远不超时、不返回错误；用于默认装配。
type memoryDedupBackend struct{ inner *messageDedup }

func (m *memoryDedupBackend) Check(_ context.Context, id string) (bool, error) {
	return m.inner.isDuplicate(id), nil
}
func (m *memoryDedupBackend) Stop() { m.inner.stop() }

// checkDedupFailClosed 是 P0-#9 的 hot path：用 200ms（或测试注入）短超时调 backend.Check，
// 任何超时 / 错误 / panic 一律 **fail-closed**（视为 dup 拒绝处理 + 落 retry_queue）。
//
// 不变量：
//   - backend nil → 永远 false（兼容老路径）
//   - backend 阻塞 > timeout → fail-closed（dup=true, err=ErrDedupBackendDown）
//   - backend panic → fail-closed（dup=true, err=ErrDedupBackendDown）
//   - 真正的 dup（backend 返回 (true, nil)） → dup=true, err=nil（不入 retry，正常去重）
//   - 真正的 fresh → dup=false, err=nil
func (r *Router) checkDedupFailClosed(parentCtx context.Context, messageID string) (bool, error) {
	r.mu.RLock()
	backend := r.dedupBackend
	timeout := r.dedupTimeout
	r.mu.RUnlock()
	if backend == nil {
		return false, nil
	}
	if timeout <= 0 {
		timeout = dedupTimeoutDefault
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	type res struct {
		dup bool
		err error
	}
	ch := make(chan res, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				ch <- res{dup: false, err: fmt.Errorf("dedup backend panic: %v", rec)}
			}
		}()
		dup, err := backend.Check(ctx, messageID)
		ch <- res{dup: dup, err: err}
	}()

	select {
	case <-ctx.Done():
		// 超时 / 父 ctx 取消都视为后端不可用，fail-closed
		return true, ErrDedupBackendDown
	case r := <-ch:
		if r.err != nil {
			// 后端报错：fail-closed
			return true, ErrDedupBackendDown
		}
		return r.dup, nil
	}
}

// Router 消息路由器，管理 IM 插件和消息绑定
type Router struct {
	plugins   map[Platform]ChannelPlugin
	bindings  map[string]string // "platform:tenantKey:chatID" → sessionID
	processor MessageProcessor
	dedup     *messageDedup
	debouncer *messageBatcher
	mu        sync.RWMutex
	logger    *zap.Logger
	// enrichCtx: 根据 IM sender 信息丰富 context（注入 user）
	// 返回 enriched ctx；找不到用户时返回原 ctx
	enrichCtx func(ctx context.Context, externalID, provider string) context.Context
	// ownerUserResolver: 根据 user-scoped IM 消息的 owner_user_id 解析内部用户。
	ownerUserResolver func(ctx context.Context, userID string) (*auth.User, error)

	// eventBus: EventBus subscriber contract（*master.Master 天然满足）。
	// 未注入时 renderer 路径降级为 legacy Send 路径。
	eventBus EventBusSubscriber
	// rendererEnabled: 平台级 renderer 开关；nil 视为全平台禁用（降级 legacy）。
	// bootstrap 按 cfg.<platform>.Renderer.Enabled 注入。
	rendererEnabled func(Platform) bool

	// retryQueue：业务处理失败时的兜底队列。nil 时降级为"仅日志"。
	// P0-#7 不变量：webhook/longconn 在 router.HandleMessage 失败时必须把消息塞回这里，
	// 否则消息在 handler 永返 nil 的语义下会被永久丢失。
	retryQueue RetryQueue

	// eventClaimer：P0-#8 两阶段事件认领。nil 时跳过（兼容老测试）。
	eventClaimer master.EventClaimer

	// dedupBackend：P0-#9 抽象出的 dedup 后端。nil 时不去重（兼容老测试）；
	// bootstrap 默认注入 memoryDedupBackend（包 messageDedup）。
	dedupBackend DedupBackend
	// dedupTimeout：dedup 查询的硬超时；0 走默认 200ms。
	// 单测可以注入更短或更长的值用于触发 fail-closed 路径。
	dedupTimeout time.Duration

	// resolvers：M1 消息摄取扩展，按平台注入 InboundContextResolver。
	// nil 时跳过 resolver 调用（兼容非飞书平台）。
	resolvers map[Platform]InboundContextResolver

	// metricsWriter：可选指标写入器。nil 时跳过 Phase 1 resolver metrics。
	metricsWriter observability.MetricsWriter
}

// NewRouter 创建消息路由器
func NewRouter(processor MessageProcessor, logger *zap.Logger) *Router {
	dedup := newMessageDedup()
	r := &Router{
		plugins:      make(map[Platform]ChannelPlugin),
		bindings:     make(map[string]string),
		processor:    processor,
		dedup:        dedup,
		logger:       logger,
		dedupBackend: &memoryDedupBackend{inner: dedup}, // P0-#9 默认包内存 dedup 为 backend
		dedupTimeout: dedupTimeoutDefault,
	}
	// debouncer 的 flush 回调直接调用 processMessage
	// debounce 消息不走 dedup（因为 MessageID 是最后一条的 ID，可能与独立消息重复），
	// 但 processMessage 内部仍会做 dedup 检查
	r.debouncer = newMessageBatcher(func(merged InboundMessage) {
		r.processMessage(merged)
	}, logger)
	return r
}

// SetDedupBackend 注入自定义 dedup 后端（P0-#9）。bootstrap 在多实例场景注入 Redis backend；
// 单测可以注入"hung backend"触发 fail-closed 验证。
func (r *Router) SetDedupBackend(b DedupBackend) {
	r.mu.Lock()
	r.dedupBackend = b
	r.mu.Unlock()
}

// SetDedupTimeout 调整 dedup 短超时（P0-#9）。0 → 重置为默认 200ms；负数 → 视为 0。
func (r *Router) SetDedupTimeout(d time.Duration) {
	if d < 0 {
		d = 0
	}
	r.mu.Lock()
	r.dedupTimeout = d
	r.mu.Unlock()
}

// SetContextEnricher 注入用户上下文丰富器（由 bootstrap 在 auth 启用时设置）
func (r *Router) SetContextEnricher(fn func(ctx context.Context, externalID, provider string) context.Context) {
	r.mu.Lock()
	r.enrichCtx = fn
	r.mu.Unlock()
}

// SetOwnerUserResolver 注入 user-scoped IM 消息的 owner 用户解析器。
func (r *Router) SetOwnerUserResolver(fn func(ctx context.Context, userID string) (*auth.User, error)) {
	r.mu.Lock()
	r.ownerUserResolver = fn
	r.mu.Unlock()
}

func (r *Router) contextEnricher() func(ctx context.Context, externalID, provider string) context.Context {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enrichCtx
}

func (r *Router) lookupOwnerUserResolver() func(ctx context.Context, userID string) (*auth.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ownerUserResolver
}

// SetEventBusSubscriber 注入 master 的订阅 API（*master.Master 满足契约）。
// bootstrap 在 Master 构造完成后调用；未注入时 renderer 路径自动降级为 legacy Send。
func (r *Router) SetEventBusSubscriber(s EventBusSubscriber) {
	r.mu.Lock()
	r.eventBus = s
	r.mu.Unlock()
}

// SetRendererEnabled 注入平台级 renderer 开关查询函数。
// fn(platform) == true 时对该平台启用 renderer 路径；false 或 fn==nil 走 legacy Send。
func (r *Router) SetRendererEnabled(fn func(Platform) bool) {
	r.mu.Lock()
	r.rendererEnabled = fn
	r.mu.Unlock()
}

// SetRetryQueue 注入 P0-#7 的 retry_queue。bootstrap 在装配阶段注入；
// 单测可以直接调用注入 MemoryRetryQueue 做断言。
func (r *Router) SetRetryQueue(q RetryQueue) {
	r.mu.Lock()
	r.retryQueue = q
	r.mu.Unlock()
}

// RetryQueue 暴露当前队列引用（plugin 端 wrapper 直接 enqueue 用）。
// 没有注入时返回 nil；nil receiver 上 Enqueue 是 no-op，下游可放心 chain。
func (r *Router) RetryQueue() RetryQueue {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.retryQueue
}

// SetEventClaimer 注入 P0-#8 两阶段事件认领器。bootstrap 在装配阶段注入；
// nil 时跳过 claim（兼容老测试）。
func (r *Router) SetEventClaimer(c master.EventClaimer) {
	r.mu.Lock()
	r.eventClaimer = c
	r.mu.Unlock()
}

// EventClaimer 暴露当前 claimer 引用（webhook handler 用于两阶段 claim）。
func (r *Router) EventClaimer() master.EventClaimer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.eventClaimer
}

// SetInboundContextResolver 注入平台级消息上下文解析器。
func (r *Router) SetInboundContextResolver(platform Platform, resolver InboundContextResolver) {
	r.mu.Lock()
	if r.resolvers == nil {
		r.resolvers = make(map[Platform]InboundContextResolver)
	}
	r.resolvers[platform] = resolver
	r.mu.Unlock()
}

// SetMetricsWriter 注入指标写入器。nil 表示关闭 router 侧指标。
func (r *Router) SetMetricsWriter(w observability.MetricsWriter) {
	r.mu.Lock()
	r.metricsWriter = w
	r.mu.Unlock()
}

func (r *Router) MetricsWriter() observability.MetricsWriter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.metricsWriter
}

// lookupResolver 查找平台对应的 InboundContextResolver，nil 表示未注入。
func (r *Router) lookupResolver(platform Platform) InboundContextResolver {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.resolvers == nil {
		return nil
	}
	return r.resolvers[platform]
}

// InboundContextResolver 返回平台对应的 resolver；nil 表示未注入。
func (r *Router) InboundContextResolver(platform Platform) InboundContextResolver {
	return r.lookupResolver(platform)
}

// emitMetric 异步写指标，永不阻断主路径。
func (r *Router) emitMetric(metric observability.Metric) {
	r.mu.RLock()
	writer := r.metricsWriter
	r.mu.RUnlock()
	if writer == nil {
		return
	}
	go func() {
		if err := writer.Record(context.Background(), metric); err != nil && r.logger != nil {
			r.logger.Debug("router metric write failed",
				zap.String("metric", metric.Name),
				zap.Error(err))
		}
	}()
}

// enqueueRetry 把一条 InboundMessage + 失败原因写入 retryQueue。
// nil retryQueue / Enqueue 失败 → 仅日志，永远不阻断业务路径。
// P0-#7 不变量：所有"业务失败 / panic / router-nil"调用点都收敛到这一个方法。
func (r *Router) enqueueRetry(msg InboundMessage, reason RetryReason, errMsg string) {
	q := r.RetryQueue()
	if q == nil {
		r.logger.Warn("retry_queue 未注入，仅日志兜底",
			zap.String("message_id", msg.MessageID),
			zap.String("platform", string(msg.Platform)),
			zap.String("reason", string(reason)),
			zap.String("error", errMsg))
		return
	}
	item := RetryItem{
		MessageID: msg.MessageID,
		Platform:  string(msg.Platform),
		TenantKey: msg.TenantKey,
		ChatID:    msg.ChatID,
		SenderID:  msg.SenderID,
		Reason:    reason,
		ErrorMsg:  errMsg,
	}
	if data, err := json.Marshal(msg); err == nil {
		item.Payload = data
	}
	if err := q.Enqueue(item); err != nil {
		r.logger.Error("retry_queue Enqueue 失败（消息走日志兜底）",
			zap.String("message_id", msg.MessageID),
			zap.String("reason", string(reason)),
			zap.Error(err))
	}
}

// RegisterPlugin 注册 IM 插件
func (r *Router) RegisterPlugin(plugin ChannelPlugin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins[plugin.Platform()] = plugin
	r.logger.Info("IM 插件已注册", zap.String("platform", string(plugin.Platform())))
}

// GetPlugin 获取指定平台的插件
func (r *Router) GetPlugin(platform Platform) (ChannelPlugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[platform]
	return p, ok
}

// UnregisterPlugin 注销指定平台的插件
func (r *Router) UnregisterPlugin(platform Platform) error {
	r.mu.Lock()
	plugin, ok := r.plugins[platform]
	if !ok {
		r.mu.Unlock()
		return nil // 插件不存在，直接返回
	}
	delete(r.plugins, platform)
	r.mu.Unlock()

	// 释放锁后停止插件，避免死锁
	if s, ok := plugin.(Stoppable); ok {
		if err := s.Stop(); err != nil {
			r.logger.Error("停止插件失败",
				zap.String("platform", string(platform)),
				zap.Error(err))
			return err
		}
	}

	r.logger.Info("插件已注销", zap.String("platform", string(platform)))
	return nil
}

// Bind 绑定 IM 通道到会话
func (r *Router) Bind(binding Binding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tenantKey := normalizeBindingTenantKey(binding.TenantKey)
	key := bindingKey(binding.Platform, tenantKey, binding.ChatID)
	r.bindings[key] = binding.SessionID
	r.logger.Info("IM 通道已绑定",
		zap.String("platform", string(binding.Platform)),
		zap.String("tenant_key", tenantKey),
		zap.String("chat_id", binding.ChatID),
		zap.String("session_id", binding.SessionID))
}

// Unbind 解除绑定
func (r *Router) Unbind(platform Platform, chatID string) {
	r.UnbindForTenant(platform, defaultTenantKey, chatID)
}

// UnbindForTenant 解除指定 tenant 的 IM 通道绑定。
func (r *Router) UnbindForTenant(platform Platform, tenantKey, chatID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bindings, bindingKey(platform, normalizeBindingTenantKey(tenantKey), chatID))
}

// LookupSession 查找绑定的会话 ID
func (r *Router) LookupSession(platform Platform, chatID string) string {
	return r.LookupSessionForTenant(platform, defaultTenantKey, chatID)
}

// LookupSessionForTenant 查找指定 tenant 绑定的会话 ID。
func (r *Router) LookupSessionForTenant(platform Platform, tenantKey, chatID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.bindings[bindingKey(platform, normalizeBindingTenantKey(tenantKey), chatID)]
}

// HandleMessage 处理从 IM 平台收到的消息
// 先做 MessageID 去重，再做 sender-level debounce（合并同一发送者的快速连续消息）
//
// P0-#9：dedup 经 fail-closed 短超时（默认 200ms）。后端故障 → 当 dup 处理 + 落 retry_queue。
func (r *Router) HandleMessage(ctx context.Context, msg InboundMessage) error {
	if r.hasPendingInputForInbound(ctx, msg) {
		msg.NoDebounce = true
	}

	// 消息去重：防止 webhook 重试或 webhook+longconn 并存时重复处理
	// 注意：仅对不走 debounce 的消息在此去重
	// 走 debounce 的消息在 processMessage 中去重，避免缓冲后被丢弃时 MessageID 被永久标记
	if msg.SenderID == "" && msg.MessageID != "" {
		dup, err := r.checkDedupFailClosed(ctx, msg.MessageID)
		if err != nil {
			// 后端故障 fail-closed：本条不处理 + 落 retry_queue 让运维感知
			r.logger.Warn("dedup backend fail-closed，丢弃本条 + 落 retry_queue",
				zap.String("message_id", msg.MessageID),
				zap.Error(err))
			r.enqueueRetry(msg, RetryReasonDedupBackend, err.Error())
			return nil
		}
		if dup {
			r.logger.Debug("跳过重复消息", zap.String("message_id", msg.MessageID))
			return nil
		}
	}

	// Debounce：同一发送者的快速连续消息合并后异步处理。
	// gap fetch / 历史回放必须逐条进入标准链路，显式跳过 debounce。
	// 返回 true 表示消息已缓冲，等待窗口到期后合并处理。
	if !msg.NoDebounce && r.debouncer.Add(msg) {
		return nil
	}

	// 无法 debounce 的消息（如 SenderID 为空）直接处理
	r.processMessage(msg)
	return nil
}

func (r *Router) hasPendingInputForInbound(ctx context.Context, msg InboundMessage) bool {
	plugin, ok := r.GetPlugin(msg.Platform)
	if !ok {
		return false
	}
	detector, ok := plugin.(PendingInputDetector)
	if !ok {
		return false
	}
	sessionID := r.LookupSessionForTenant(msg.Platform, msg.TenantKey, msg.ChatID)
	if sessionID == "" {
		tenantKey := msg.TenantKey
		if tenantKey == "" {
			tenantKey = defaultTenantKey
		}
		built, err := imctx.BuildSessionID(imctx.Platform(msg.Platform), tenantKey, msg.ChatID)
		if err != nil {
			return false
		}
		sessionID = built
	}
	return detector.HasPendingInput(ctx, msg, sessionID)
}

// processMessage 消息处理核心逻辑，由 HandleMessage 和 debouncer flush 回调调用
func (r *Router) processMessage(msg InboundMessage) {
	r.processMessageImpl(msg)
}

// processMessageImpl 实际执行消息处理（内部方法，供 goroutine 调用）
//
// P0-#7 不变量：
//   - 顶层 panic recover：debouncer flush goroutine / webhook async goroutine 的最后一道防线，
//     panic 后必须落 retry_queue（reason=handler_panic），不得让 panic 冒出导致 process abort
//   - processor 失败：在 processViaLegacySend / processViaRenderer 内部各自落队（reason=handler_error）
func (r *Router) processMessageImpl(msg InboundMessage) {
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("router.processMessageImpl panic recovered",
				zap.String("message_id", msg.MessageID),
				zap.Any("panic", rec))
			r.enqueueRetry(msg, RetryReasonHandlerPanic, fmt.Sprintf("processMessageImpl panic: %v", rec))
		}
	}()
	// debounce 路径的消息在此去重（合并后使用最后一条的 MessageID）。
	// P0-#9 fail-closed：debouncer flush 已经异步，无父 ctx 可用，启用 Background+短超时。
	if msg.SenderID != "" && msg.MessageID != "" {
		dup, err := r.checkDedupFailClosed(context.Background(), msg.MessageID)
		if err != nil {
			r.logger.Warn("dedup backend fail-closed（debounce 后），丢弃 + 落 retry_queue",
				zap.String("message_id", msg.MessageID),
				zap.Error(err))
			r.enqueueRetry(msg, RetryReasonDedupBackend, err.Error())
			return
		}
		if dup {
			r.logger.Debug("跳过重复消息（debounce 后）", zap.String("message_id", msg.MessageID))
			return
		}
	}
	// 注意：此处使用 context.Background() 而非调用方传入的 ctx。
	// 原因：debouncer flush 回调是异步执行的，原始请求 ctx 在 debounce 窗口期间
	// 可能已经超时或取消。使用 Background() 确保合并后的消息能完整处理。
	// 已知限制：上游 trace ID 等 context 信息在 debounce 路径下会丢失。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// IM 用户关联：仅私聊且有 SenderID 时尝试注入用户上下文
	// 群聊 session 不关联 user_id（群聊归属不明确）
	// IM 路径不经过 auth middleware，IsAuthEnabled=false，checkSessionAccess 会放行
	imValue := IMContextValue{
		SenderOpenID: msg.SenderID,
		Platform:     string(msg.Platform),
		ChatType:     msg.ChatType,
	}
	if msg.OwnerUserID != "" {
		user, ok := r.resolveOwnerUser(ctx, msg)
		if !ok {
			return
		}
		ctx = auth.WithUser(ctx, user)
		imValue.InternalUserID = user.ID
	} else {
		enrichCtx := r.contextEnricher()
		if enrichCtx != nil && msg.SenderID != "" {
			ctx = enrichCtx(ctx, msg.SenderID, platformToProvider(msg.Platform))
			if u := auth.UserFrom(ctx); u != nil {
				imValue.InternalUserID = u.ID
			}
		}
	}
	ctx = context.WithValue(ctx, imContextKey{}, imValue)

	// 查找绑定的会话
	sessionID := r.LookupSessionForTenant(msg.Platform, msg.TenantKey, msg.ChatID)
	if sessionID == "" {
		// 无手动绑定时，自动为每个 IM 聊天创建稳定的独立 session
		// 防止 IM 消息 fallback 到人类活跃 session，污染前端会话列表
		//
		// P0-#10：唯一入口。禁止字面量拼接形如 im + 平台名 + chatID 的 session_id，
		// 必须经 imctx.BuildSessionID（统一 4 段格式 + dash 转义）。
		tenantKey := msg.TenantKey
		if tenantKey == "" {
			tenantKey = defaultTenantKey
		}
		built, err := imctx.BuildSessionID(imctx.Platform(msg.Platform), tenantKey, msg.ChatID)
		if err != nil {
			// BuildSessionID 仅在三段任一为空时报错。Platform/ChatID 在 router 入口
			// 已保证非空（webhook/longconn 早就过滤），此处只可能是 ChatID 真为空——
			// 没有 chat 维度无法路由，直接落日志返回，避免拿空 session_id 污染 journal。
			r.logger.Error("BuildSessionID 失败，丢弃消息",
				zap.String("platform", string(msg.Platform)),
				zap.String("chat_id", msg.ChatID),
				zap.Error(err))
			return
		}
		sessionID = built
		r.Bind(Binding{Platform: msg.Platform, TenantKey: tenantKey, ChatID: msg.ChatID, SessionID: sessionID})
	}

	r.logger.Info("路由消息",
		zap.String("platform", string(msg.Platform)),
		zap.String("chat_id", msg.ChatID),
		zap.String("session_id", sessionID),
		zap.String("sender", msg.SenderName))

	// 获取平台插件
	plugin, ok := r.GetPlugin(msg.Platform)
	if !ok {
		r.logger.Error("平台未注册", zap.String("platform", string(msg.Platform)))
		return
	}
	if controller, ok := plugin.(InboundController); ok {
		modelOverride := ""
		result, err := controller.ControlInbound(ctx, msg, sessionID)
		if err != nil {
			r.logger.Error("平台入站控制失败",
				zap.String("platform", string(msg.Platform)),
				zap.String("message_id", msg.MessageID),
				zap.Error(err))
			r.NotifyError(ctx, msg, err)
			return
		}
		if result.SessionIDOverride != "" && result.SessionIDOverride != sessionID {
			sessionID = result.SessionIDOverride
			r.Bind(Binding{Platform: msg.Platform, TenantKey: normalizeBindingTenantKey(msg.TenantKey), ChatID: msg.ChatID, SessionID: sessionID})
		}
		if result.ModelOverride != "" {
			modelOverride = result.ModelOverride
		}
		if result.Handled {
			if result.Response != "" {
				if err := plugin.Send(ctx, OutboundMessage{
					Platform:    msg.Platform,
					TenantKey:   msg.TenantKey,
					OwnerUserID: msg.OwnerUserID,
					ChatID:      msg.ChatID,
					Content:     result.Response,
					ReplyTo:     msg.MessageID,
					ReplyToken:  msg.ReplyToken,
				}); err != nil {
					r.logger.Error("发送命令回复失败",
						zap.String("message_id", msg.MessageID),
						zap.Error(err))
				}
			}
			return
		}
		if result.Drop {
			return
		}
		if renderer, isRenderer := plugin.(EventRenderer); isRenderer && r.shouldUseRenderer(plugin.Platform()) {
			r.processViaRenderer(ctx, renderer, plugin, msg, sessionID)
			return
		}
		imCtx := r.resolveInboundContext(ctx, &msg)
		r.processViaLegacySend(ctx, plugin, msg, sessionID, modelOverride, imCtx)
		return
	}
	if renderer, isRenderer := plugin.(EventRenderer); isRenderer && r.shouldUseRenderer(plugin.Platform()) {
		r.processViaRenderer(ctx, renderer, plugin, msg, sessionID)
		return
	}
	imCtx := r.resolveInboundContext(ctx, &msg)
	r.processViaLegacySend(ctx, plugin, msg, sessionID, "", imCtx)
}

func (r *Router) resolveInboundContext(ctx context.Context, msg *InboundMessage) *imctx.IMMessageContext {
	if msg == nil {
		return nil
	}
	resolver := r.lookupResolver(msg.Platform)
	if resolver == nil {
		return nil
	}
	startedAt := time.Now()
	rc, rcancel := context.WithTimeout(ctx, 3*time.Second)
	defer rcancel()

	imCtx, err := resolver.Resolve(rc, msg)
	durationMs := time.Since(startedAt).Milliseconds()
	r.emitMetric(observability.Metric{
		Name:  MetricFeishuResolverDuration,
		Value: float64(durationMs),
		Labels: map[string]any{
			"platform":        string(msg.Platform),
			"status":          map[bool]string{true: "error", false: "ok"}[err != nil],
			"tenant_key_hash": TenantKeyHashLabel(msg.TenantKey),
		},
		Ts: time.Now(),
	})
	if err != nil {
		r.logger.Warn("inbound context resolver failed, degrading",
			zap.String("platform", string(msg.Platform)),
			zap.Error(err))
		return nil
	}
	if msg.Platform == PlatformFeishu {
		refCount := 0
		if imCtx != nil {
			refCount = len(imCtx.References)
		}
		r.emitMetric(observability.Metric{
			Name:  MetricFeishuInboundRefsCount,
			Value: float64(refCount),
			Labels: map[string]any{
				"chat_type":       string(msg.ChatType),
				"tenant_key_hash": TenantKeyHashLabel(msg.TenantKey),
			},
			Ts: time.Now(),
		})
	}
	return imCtx
}

func (r *Router) resolveOwnerUser(ctx context.Context, msg InboundMessage) (*auth.User, bool) {
	if msg.TenantKey != msg.OwnerUserID {
		errMsg := "owner_user_id 与 tenant_key 不一致"
		r.logger.Error(errMsg,
			zap.String("message_id", msg.MessageID),
			zap.String("platform", string(msg.Platform)),
			zap.String("tenant_key", msg.TenantKey),
			zap.String("owner_user_id", msg.OwnerUserID))
		r.enqueueRetry(msg, RetryReasonHandlerError, errMsg)
		return nil, false
	}

	resolver := r.lookupOwnerUserResolver()
	if resolver == nil {
		errMsg := "owner user resolver 未注入"
		r.logger.Error(errMsg,
			zap.String("message_id", msg.MessageID),
			zap.String("owner_user_id", msg.OwnerUserID))
		r.enqueueRetry(msg, RetryReasonHandlerError, errMsg)
		return nil, false
	}

	user, err := resolver(ctx, msg.OwnerUserID)
	if err != nil {
		errMsg := "owner user resolver 失败: " + err.Error()
		r.logger.Error("owner user resolver 失败",
			zap.String("message_id", msg.MessageID),
			zap.String("owner_user_id", msg.OwnerUserID),
			zap.Error(err))
		r.enqueueRetry(msg, RetryReasonHandlerError, errMsg)
		return nil, false
	}
	if user == nil {
		errMsg := "owner user 不存在"
		r.logger.Error(errMsg,
			zap.String("message_id", msg.MessageID),
			zap.String("owner_user_id", msg.OwnerUserID))
		r.enqueueRetry(msg, RetryReasonHandlerError, errMsg)
		return nil, false
	}
	if user.Status != "active" {
		errMsg := "owner user 非 active"
		r.logger.Error(errMsg,
			zap.String("message_id", msg.MessageID),
			zap.String("owner_user_id", msg.OwnerUserID),
			zap.String("status", user.Status))
		r.enqueueRetry(msg, RetryReasonHandlerError, errMsg)
		return nil, false
	}
	return user, true
}

// shouldUseRenderer 读 Router 的 rendererEnabled 与 eventBus 字段决定是否走 renderer 路径。
// 读取用 RLock 保证与 SetEventBusSubscriber / SetRendererEnabled 的写入并发安全。
func (r *Router) shouldUseRenderer(platform Platform) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.eventBus == nil || r.rendererEnabled == nil {
		return false
	}
	return r.rendererEnabled(platform)
}

// processViaLegacySend 是 pre-change 的一次性 Send 路径，对不实现 EventRenderer 的平台
// 与 renderer 未启用时的降级路径保持 bit-identical 行为。
//
// P0-#7：dispatchProcess 失败时，除原有的 NotifyError 行为外，必须把消息塞进 retry_queue。
// 否则消息在 handler 永返 nil + NotifyError best-effort 的语义下会被永久丢失。
func (r *Router) processViaLegacySend(ctx context.Context, plugin ChannelPlugin, msg InboundMessage, sessionID string, modelOverride string, imCtx *imctx.IMMessageContext) {
	resp, err := r.dispatchProcess(ctx, sessionID, msg, modelOverride, false, imCtx)
	if err != nil {
		r.logger.Error("处理消息失败，落 retry_queue",
			zap.String("message_id", msg.MessageID),
			zap.Error(err))
		r.enqueueRetry(msg, RetryReasonHandlerError, err.Error())
		r.NotifyError(ctx, msg, err)
		return
	}

	replyContent := resp.Content
	if replyContent == "" {
		replyContent = resp.Message
	}
	if replyContent == "" {
		return // 无内容可回复
	}
	// 防御性 JSON 检测：如果回复内容是纯 JSON，包装为代码块以提高可读性
	replyContent = wrapRawJSON(replyContent)

	outMsg := OutboundMessage{
		Platform:    msg.Platform,
		TenantKey:   msg.TenantKey,
		OwnerUserID: msg.OwnerUserID,
		ChatID:      msg.ChatID,
		Content:     replyContent,
		ReplyTo:     msg.MessageID,
		ReplyToken:  msg.ReplyToken,
	}
	if err := plugin.Send(ctx, outMsg); err != nil {
		r.logger.Error("发送回复失败",
			zap.String("message_id", msg.MessageID),
			zap.Error(err))
	}
}

// dispatchProcess 桥接 MessageProcessor / IMMessageProcessor 两个接口：
// processor 若实现 IMMessageProcessor 且 msg.MessageID 非空 → 透传 ChannelMessageID + imCtx；
// 否则退回 ProcessMessage（保持旧行为 bit-identical）。
// 契约：im-streaming-reply spec line 69-73（plugin MUST set req.ChannelMessageID to platform-side message ID）。
func (r *Router) dispatchProcess(ctx context.Context, sessionID string, msg InboundMessage, modelOverride string, ackAlreadyEmitted bool, imCtx *imctx.IMMessageContext) (master.TaskResponse, error) {
	if imp, ok := r.processor.(IMMessageProcessor); ok && msg.MessageID != "" {
		return imp.ProcessMessageFromIM(ctx, sessionID, msg.Content, msg.MessageID, modelOverride, ackAlreadyEmitted, imCtx)
	}
	return r.processor.ProcessMessage(ctx, sessionID, msg.Content)
}

func (r *Router) emitInputReceived(sessionID, channelMessageID string) {
	if r.eventBus == nil || sessionID == "" {
		return
	}
	r.eventBus.BroadcastSessionMessage(sessionID, master.BroadcastMessage{
		Type: master.EventTypeInputReceived,
		Payload: master.InputReceivedEvent{
			SessionID:        sessionID,
			ChannelMessageID: channelMessageID,
		},
	})
}

// rendererDrainDelay 是 ProcessMessage 返回后、UnsubscribeWSBroadcast 前的 drain 窗口。
// 给尚未到 renderer 的 in-flight 事件留出消费时间；过短会丢尾部事件，过长延迟下一消息。
// 200ms 是 spec 5.2 明确给出的阈值。
const rendererDrainDelay = 200 * time.Millisecond

// processViaRenderer 是 subscriber-based 编排路径：
//  1. 先 Subscribe（保证订阅在 ProcessMessage 广播 input_received 之前就位）
//  2. 起 renderer goroutine 消费 eventCh
//  3. 主 goroutine 调 ProcessMessage
//  4. ProcessMessage 返回后 drain 200ms → Unsubscribe（EventBus 自动 close eventCh）→ wait renderer
//  5. renderer 返回 *RendererError → 用 LastContent 走 plugin.Send 兜底；裸 error → 日志警告，不兜底
//  6. ProcessMessage 本身错误 → 沿用 NotifyError，不再重复发错（renderer 已监听 error 事件）
func (r *Router) processViaRenderer(ctx context.Context, renderer EventRenderer, plugin ChannelPlugin, msg InboundMessage, sessionID string) {
	scope := SessionScope{
		SessionID:   sessionID,
		TenantKey:   msg.TenantKey,
		OwnerUserID: msg.OwnerUserID,
		ChatID:      msg.ChatID,
		UserID:      msg.SenderID,
		MessageID:   msg.MessageID,
		ReplyToID:   msg.MessageID,
		ReplyToken:  msg.ReplyToken,
	}

	// 先拿订阅（eventCh 由 EventBus 提供、在 Unsubscribe 时 close），
	// 再调 ProcessMessage；否则 harness 先广播 input_received 时订阅尚未就位会丢事件。
	subID, eventCh := r.eventBus.SubscribeWSBroadcast()

	rendCtx, rendCancel := context.WithCancel(ctx)
	defer rendCancel()

	rendErrCh := make(chan error, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				r.logger.Error("renderer goroutine panic recovered",
					zap.String("platform", string(plugin.Platform())),
					zap.String("session_id", sessionID),
					zap.Any("panic", rec))
				rendErrCh <- &RendererError{Inner: errs.New(errs.CodeInternal, "renderer panic"), LastContent: ""}
				return
			}
		}()
		rendErrCh <- renderer.RenderEventStream(rendCtx, scope, eventCh)
	}()

	r.emitInputReceived(sessionID, msg.MessageID)

	imCtx := r.resolveInboundContext(ctx, &msg)
	procResp, procErr := r.dispatchProcess(ctx, sessionID, msg, "", true, imCtx)

	// drain 200ms → Unsubscribe（触发 eventCh close）→ 等 renderer goroutine 退出
	select {
	case <-time.After(rendererDrainDelay):
	case <-ctx.Done():
	}
	r.eventBus.UnsubscribeWSBroadcast(subID)

	var rendErr error
	select {
	case rendErr = <-rendErrCh:
	case <-time.After(3 * time.Second):
		// renderer 违反契约：ctx cancel 后 3s 内未收敛。强制取消并继续，
		// 避免阻塞下一条 inbound 消息。日志记录，后续运维定位。
		rendCancel()
		r.logger.Error("renderer goroutine 超时未返回（>3s）",
			zap.String("platform", string(plugin.Platform())),
			zap.String("session_id", sessionID))
		rendErr = errs.New(errs.CodeInternal, "renderer drain timeout")
	}

	if procErr != nil {
		// renderer 已通过 error 事件得知失败，不再重复 send；
		// 走 NotifyError 只在 renderer 自身也失败时做兜底。
		r.logger.Error("处理消息失败（renderer 路径），落 retry_queue",
			zap.String("message_id", msg.MessageID),
			zap.Error(procErr))
		// P0-#7：renderer 路径失败也必须入 retry_queue。renderer 兜底是 UX 行为，与 retry 持久化正交。
		r.enqueueRetry(msg, RetryReasonHandlerError, procErr.Error())
		if rendErr != nil {
			r.NotifyError(ctx, msg, procErr)
		}
		return
	}

	// renderer 成功收敛 → 由 renderer 内部完成最终 PATCH，Router 不再兜底 Send
	if rendErr == nil {
		return
	}

	// renderer 返回 *RendererError：用 LastContent 走 plugin.Send 兜底
	var rerr *RendererError
	if errors.As(rendErr, &rerr) && rerr.LastContent != "" {
		r.logger.Warn("renderer 失败，回退到 plugin.Send 兜底",
			zap.String("message_id", msg.MessageID),
			zap.Error(rendErr))
		fallback := OutboundMessage{
			Platform:    msg.Platform,
			TenantKey:   msg.TenantKey,
			OwnerUserID: msg.OwnerUserID,
			ChatID:      msg.ChatID,
			Content:     wrapRawJSON(rerr.LastContent),
			ReplyTo:     msg.MessageID,
			ReplyToken:  msg.ReplyToken,
		}
		if sendErr := plugin.Send(ctx, fallback); sendErr != nil {
			r.logger.Error("renderer 兜底 Send 也失败",
				zap.String("message_id", msg.MessageID),
				zap.Error(sendErr))
		}
		return
	}

	// renderer 返回裸 error 或 LastContent 为空 → 只记日志，兜底 fallback 已无内容可发
	r.logger.Warn("renderer 失败但无 LastContent，无法兜底",
		zap.String("message_id", msg.MessageID),
		zap.Error(rendErr),
		zap.Any("task_resp", procResp))
}

// WebhookHandler 返回指定平台的 webhook HTTP handler
func (r *Router) WebhookHandler(platform Platform) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		plugin, ok := r.GetPlugin(platform)
		if !ok {
			http.Error(w, "平台未注册", http.StatusNotFound)
			return
		}
		plugin.WebhookHandler()(w, req)
	}
}

// Stoppable 可选接口，支持优雅关闭的插件应实现此接口
type Stoppable interface {
	Stop() error
}

// Stop 停止所有插件、debouncer 和 dedup 清理 goroutine
// 先复制插件列表释放锁，再逐个停止，避免持锁调用 plugin.Stop() 导致死锁
func (r *Router) Stop() {
	// 先停止 debouncer，等待 in-flight flush 完成
	r.debouncer.Stop()
	// 停止 dedup 清理 goroutine
	r.dedup.stop()

	r.mu.RLock()
	type entry struct {
		platform Platform
		plugin   ChannelPlugin
	}
	toStop := make([]entry, 0, len(r.plugins))
	for p, pl := range r.plugins {
		toStop = append(toStop, entry{p, pl})
	}
	r.mu.RUnlock()

	for _, item := range toStop {
		if s, ok := item.plugin.(Stoppable); ok {
			if err := s.Stop(); err != nil {
				r.logger.Error("停止插件失败",
					zap.String("platform", string(item.platform)),
					zap.Error(err))
			}
		}
	}
	r.logger.Info("消息路由器已停止")
}

// SendMessage 发送消息到指定平台。
func (r *Router) SendMessage(ctx context.Context, req imctx.SendRequest) error {
	// 获取平台插件
	p := Platform(req.Platform)
	plugin, ok := r.GetPlugin(p)
	if !ok {
		return errs.New(errs.CodeChannelPlatformNotFound, "平台未注册: "+string(req.Platform))
	}

	// 构造消息
	msg := OutboundMessage{
		Platform:    p,
		TenantKey:   req.TenantKey,
		OwnerUserID: req.OwnerUserID,
		ChatID:      req.ChatID,
		Content:     req.Content,
		MsgType:     MsgType(req.MsgType),
		ReplyTo:     req.ReplyTo,
		ReplyToken:  req.ReplyToken,
	}

	// 发送消息
	if err := plugin.Send(ctx, msg); err != nil {
		return errs.Wrap(errs.CodeChannelSendFailed, "发送消息失败", err)
	}

	return nil
}

// NotifyError 消息处理失败时尝试通知用户
// 最大努力发送，失败只记日志不返回错误
func (r *Router) NotifyError(ctx context.Context, msg InboundMessage, processErr error) {
	plugin, ok := r.GetPlugin(msg.Platform)
	if !ok {
		return
	}

	errMsg := OutboundMessage{
		Platform:    msg.Platform,
		TenantKey:   msg.TenantKey,
		OwnerUserID: msg.OwnerUserID,
		ChatID:      msg.ChatID,
		Content:     "抱歉，消息处理失败，请稍后重试。",
		ReplyTo:     msg.MessageID,
		ReplyToken:  msg.ReplyToken,
	}

	if err := plugin.Send(ctx, errMsg); err != nil {
		r.logger.Warn("发送错误通知失败",
			zap.String("message_id", msg.MessageID),
			zap.Error(err))
	}
}

// wrapRawJSON 检测内容是否为纯 JSON；若是则包装为 Markdown 代码块。
func wrapRawJSON(content string) string {
	trimmed := strings.TrimSpace(content)
	if !IsRawJSON(trimmed) {
		return content
	}
	return "```json\n" + trimmed + "\n```"
}

// platformToProvider 将 Platform 转换为 auth provider 名称
func platformToProvider(p Platform) string {
	switch p {
	case PlatformFeishu:
		return "feishu"
	case PlatformDingTalk:
		return "dingtalk"
	default:
		return string(p)
	}
}
