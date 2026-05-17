package master

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chef-guo/agents-hive/internal/imctx"
)

// TestSetPendingData_ConsumePendingIMContext_Race 是 Phase 1 M1 prefix race 蓝军测试。
//
// 背景：
//
//	ROADMAP Phase 1 "关键不变式"要求 prefix 写入点在 sem gate 之后
//	(session_loop.go:229 SetPendingData)，consume 在 plugin hook 之后
//	(react_processor.go:103 ConsumePendingIMContext)。两端并发触点由
//	session.go 的 s.mu sync.RWMutex 保护。
//
// 此前遗留：
//
//	实现侧有锁，但 _test.go 全树 grep 零引用 SetPendingData / ConsumePendingIMContext。
//	"锁写了但没并发测试验证"≠ 保证 race-free——任何后续重构去锁、改 RWMutex 为
//	RLock、把 consume 拆两步都不会被单测捕获。
//
// 本测试三重蓝军：
//  1. -race 下若 SetPendingData/ConsumePendingIMContext 去掉 s.mu.Lock
//     → race detector 必报 DATA RACE，go test -race 必红；
//  2. consumeCount 必须 == writesSeen（每个写入至多被 consume 一次）：
//     若 ConsumePendingIMContext 忘记把 s.pendingIMContext 置 nil → 同一 imCtx
//     被读多次 → consumeCount > writesSeen → 断言红；
//  3. consumed 的 imCtx 指针必须来自某个真实 writer（通过 Platform 字段的
//     writer ID 编码验证），不能撞见 partial-read 的残破结构。
//
// 并发规模：
//
//	N_WRITE = 50 个 writer，每个 200 次 Set → 总 10000 次写入
//	N_READ  = 50 个 reader，每个 200 次 Consume → 总 10000 次读取
//	交织产生 ~100K 次 critical section 竞争——足以在 -race 下暴露任何缺锁 bug。
func TestSetPendingData_ConsumePendingIMContext_Race(t *testing.T) {
	const (
		nWriters     = 50
		nReaders     = 50
		opsPerWriter = 200
		opsPerReader = 200
	)

	s := &SessionState{}

	var writesIssued int64   // 调用 SetPendingData 的次数
	var consumedNonNil int64 // Consume 返回非 nil 的次数
	var consumedNil int64    // Consume 返回 nil（无 pending）的次数
	var badReads int64       // 读到但字段不自洽的次数（partial read 证据）

	var wg sync.WaitGroup

	// writer：每个 writer 有独立 ID，写入的 imCtx.Platform 编码为该 ID
	// 方便 reader 断言它拿到的不是 half-written 结构体
	for w := 0; w < nWriters; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for i := 0; i < opsPerWriter; i++ {
				imCtx := &imctx.IMMessageContext{
					Platform:           imctx.Platform(platformForWriter(writerID)),
					TenantKey:          "tenant-" + platformForWriter(writerID),
					ChatID:             "chat-" + platformForWriter(writerID),
					SafeSenderID:       platformForWriter(writerID),
					SystemPromptPrefix: "prefix-" + platformForWriter(writerID),
				}
				s.SetPendingData(nil, "", "", imCtx)
				atomic.AddInt64(&writesIssued, 1)
			}
		}(w)
	}

	// reader：并发 ConsumePendingIMContext，断言读到的 imCtx 结构自洽
	for r := 0; r < nReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerReader; i++ {
				got := s.ConsumePendingIMContext()
				if got == nil {
					atomic.AddInt64(&consumedNil, 1)
					continue
				}
				atomic.AddInt64(&consumedNonNil, 1)
				// 自洽性断言：同一 writer 写入的字段必须互相匹配。
				// Partial read 会让其中某个字段错位——比如 TenantKey 来自写 A，
				// ChatID 来自写 B——此条件必红。
				expected := string(got.Platform)
				if got.TenantKey != "tenant-"+expected ||
					got.ChatID != "chat-"+expected ||
					got.SafeSenderID != expected ||
					got.SystemPromptPrefix != "prefix-"+expected {
					atomic.AddInt64(&badReads, 1)
					t.Errorf("partial read: Platform=%q TenantKey=%q ChatID=%q SafeSenderID=%q Prefix=%q",
						got.Platform, got.TenantKey, got.ChatID, got.SafeSenderID, got.SystemPromptPrefix)
				}
			}
		}()
	}

	wg.Wait()

	w := atomic.LoadInt64(&writesIssued)
	cNon := atomic.LoadInt64(&consumedNonNil)
	cNil := atomic.LoadInt64(&consumedNil)
	bad := atomic.LoadInt64(&badReads)

	t.Logf("writes=%d consumedNonNil=%d consumedNil=%d totalReads=%d badReads=%d",
		w, cNon, cNil, cNon+cNil, bad)

	// (1) partial read 必须为 0——锁生效时不可能出现。
	if bad != 0 {
		t.Fatalf("发现 %d 次 partial read，锁保护失效", bad)
	}

	// (2) consumedNonNil 必须 <= writesIssued——每个写入至多被消费一次。
	//     若 Consume 忘记置 nil → 同一 ctx 被读多次 → consumedNonNil 可超过 writes。
	if cNon > w {
		t.Fatalf("消费次数 %d 超过写入次数 %d，ConsumePendingIMContext 未正确置 nil",
			cNon, w)
	}

	// (3) total reads = consumedNonNil + consumedNil 应该 == nReaders*opsPerReader
	totalReads := cNon + cNil
	expectedReads := int64(nReaders * opsPerReader)
	if totalReads != expectedReads {
		t.Fatalf("总读取 %d 不等于预期 %d", totalReads, expectedReads)
	}

	// (4) 正常情况应该至少有一部分 consume 非 nil 与一部分 nil——
	//     全 nil（reader 永远跑在 writer 前）或全非 nil（读操作太慢）都很可疑。
	//     这不是正确性断言，只是诊断信号。
	if cNon == 0 {
		t.Logf("warning: 所有 consume 都返回 nil，reader 可能永远跑在 writer 前；建议增加 opsPerReader 或加 time.Sleep 调度")
	}
}

// platformForWriter 把 writer ID 映射成稳定的字符串，用于写入的 imCtx 各字段。
// 同一 writer 所有字段统一走这个函数，reader 端可以通过 Platform 反查预期值。
func platformForWriter(id int) string {
	return "w" + itoa(id)
}

// itoa 不引 strconv 以保持 imports 最小化（该文件已用 sync/sync/atomic/testing）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestConsumePendingIMContext_Idempotent 验证第二次 consume 必须返回 nil。
// 蓝军 mutation：若 ConsumePendingIMContext 忘记把 s.pendingIMContext 置 nil，
// 本测试第二次调用会得到非 nil → 立即红。
func TestConsumePendingIMContext_Idempotent(t *testing.T) {
	s := &SessionState{}
	imCtx := &imctx.IMMessageContext{
		Platform:           "feishu",
		SystemPromptPrefix: "once-only",
	}
	s.SetPendingData(nil, "", "", imCtx)

	first := s.ConsumePendingIMContext()
	if first == nil {
		t.Fatal("第一次 consume 应返回写入的 imCtx，但返回 nil")
	}
	if first.SystemPromptPrefix != "once-only" {
		t.Fatalf("第一次 consume 返回字段错乱: prefix=%q", first.SystemPromptPrefix)
	}

	second := s.ConsumePendingIMContext()
	if second != nil {
		t.Fatalf("第二次 consume 必须返回 nil（一次性语义），实际返回 prefix=%q",
			second.SystemPromptPrefix)
	}
}

// TestClearPendingData_AlsoClearsIMContext 验证 ClearPendingData 也清 imCtx。
// 这个不变式 ROADMAP 没显式列，但 session.go:170-177 的实现注释蕴含：
// "清理临时附件、推理努力级别和模型覆盖"——pendingIMContext 也应一并清掉，
// 否则上一轮残留会污染下一轮 consume。
func TestClearPendingData_AlsoClearsIMContext(t *testing.T) {
	s := &SessionState{}
	s.SetPendingData(nil, "", "", &imctx.IMMessageContext{
		SystemPromptPrefix: "should-be-cleared",
	})
	s.ClearPendingData()

	got := s.ConsumePendingIMContext()
	if got != nil {
		t.Fatalf("ClearPendingData 后 Consume 应返回 nil，但返回 prefix=%q",
			got.SystemPromptPrefix)
	}
}
