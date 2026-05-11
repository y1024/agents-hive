package channel

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// debounceWindow 同一发送者连续消息的合并窗口
const debounceWindow = 2 * time.Second

// pendingBatch 待合并的消息批次
type pendingBatch struct {
	messages   []InboundMessage
	timer      *time.Timer
	generation int // 每次 Reset 递增，防止过期 callback flush 错误的批次
}

// messageBatcher 基于发送者的消息合并器
// key = "platform:chatID:senderID"，在 debounceWindow 内的连续消息合并为一条
type messageBatcher struct {
	mu      sync.Mutex
	wg      sync.WaitGroup // 追踪 in-flight flush goroutine
	pending map[string]*pendingBatch
	stopped bool
	flush   func(merged InboundMessage) // 合并后的消息回调
	logger  *zap.Logger
}

func newMessageBatcher(flush func(merged InboundMessage), logger *zap.Logger) *messageBatcher {
	return &messageBatcher{
		pending: make(map[string]*pendingBatch),
		flush:   flush,
		logger:  logger,
	}
}

// senderKey 生成发送者唯一标识。
// tenant/owner 必须参与 key，避免 user-scoped IM 在相同外部 peer ID 下串批。
func senderKey(msg InboundMessage) string {
	return string(msg.Platform) + ":" + normalizeBindingTenantKey(msg.TenantKey) + ":" + msg.OwnerUserID + ":" + msg.ChatID + ":" + msg.SenderID
}

// Add 添加消息到合并队列，返回 true 表示消息被缓冲（等待合并），false 表示无需合并直接处理
// 当 SenderID 为空时不做 debounce（无法识别发送者）
func (b *messageBatcher) Add(msg InboundMessage) bool {
	if msg.SenderID == "" {
		return false
	}

	key := senderKey(msg)

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.stopped {
		return false
	}

	if batch, ok := b.pending[key]; ok {
		// 已有待合并批次，追加消息
		batch.messages = append(batch.messages, msg)
		// 递增 generation，使旧 timer callback 失效
		batch.generation++
		gen := batch.generation
		batch.timer.Stop()
		batch.timer = time.AfterFunc(debounceWindow, func() {
			b.flushBatch(key, gen)
		})
		b.logger.Debug("消息加入合并批次",
			zap.String("sender", key),
			zap.Int("batch_size", len(batch.messages)),
		)
		return true
	}

	// 新批次：启动计时器，到期后 flush
	batch := &pendingBatch{
		messages:   []InboundMessage{msg},
		generation: 0,
	}
	batch.timer = time.AfterFunc(debounceWindow, func() {
		b.flushBatch(key, 0)
	})
	b.pending[key] = batch
	return true
}

// flushBatch 合并并发送一个批次
// gen 参数用于防止过期 timer callback flush 错误的批次
func (b *messageBatcher) flushBatch(key string, gen int) {
	b.mu.Lock()
	batch, ok := b.pending[key]
	if !ok || batch.generation != gen || b.stopped {
		// 批次已被新消息重置（generation 不匹配）或已停止
		b.mu.Unlock()
		return
	}
	delete(b.pending, key)
	b.wg.Add(1)
	b.mu.Unlock()

	defer b.wg.Done()

	merged := mergeBatch(batch.messages)
	b.logger.Info("合并消息批次",
		zap.String("sender", key),
		zap.Int("count", len(batch.messages)),
	)
	b.flush(merged)
}

// mergeBatch 将多条消息合并为一条。
// 策略：以最后一条消息为基准保留 scope / metadata / reply token，只合并 Content。
func mergeBatch(messages []InboundMessage) InboundMessage {
	if len(messages) == 1 {
		return messages[0]
	}

	merged := messages[len(messages)-1]
	var content string
	for i, msg := range messages {
		if i > 0 {
			content += "\n"
		}
		content += msg.Content
	}

	merged.Content = content
	return merged
}

// Stop 优雅停止：先标记停止防止新 flush，再停止所有计时器，最后等待 in-flight flush 完成
func (b *messageBatcher) Stop() {
	b.mu.Lock()
	b.stopped = true
	for key, batch := range b.pending {
		batch.timer.Stop()
		delete(b.pending, key)
	}
	b.mu.Unlock()

	// 等待所有 in-flight flush 回调完成
	b.wg.Wait()
}
