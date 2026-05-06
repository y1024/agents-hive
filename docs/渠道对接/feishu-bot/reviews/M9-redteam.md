# M9 · 可靠性 — 红队评审报告

> 基于对 `09-reliability.md`、SDK `larkws`/`service/im` 源码、和 `internal/channel/feishu/longconn.go` 的对抗性审计。

## 方法

- 直读 SDK:`~/go/pkg/mod/github.com/larksuite/oapi-sdk-go/v3@v3.5.3/ws/client.go`、`service/im/v1/message.go`
- 对比 M9 文档断言 vs SDK 实际接口
- 攻击 dedup fail-open、gap fetch 边界、retry queue 并发、chat affinity rehash

## P0 发现(设计级致命)

### P0-M9-01:`larkws.WithStatusChangeHandler` 在 SDK v3.5.3 根本不存在

**证据**:`~/go/pkg/mod/github.com/larksuite/oapi-sdk-go/v3@v3.5.3/ws/client.go` 全量扫描 `^func With`,**只有 5 个 ClientOption**:
```
WithEventHandler
WithLogLevel
WithLogger
WithAutoReconnect
WithDomain
```
**没有** `WithStatusChangeHandler`。且 `client.Start` 实现为 `select{}` 永久阻塞,**无状态事件回调接口**。

**影响**:M9 §4.3 所有基于 `case larkws.StatusReconnecting/Connected` 的 gap fetch 触发时机的方案**无法实施**。一期实施者按文档写直接 compile error。

**补救**:
1. **方案 A(推荐)**:自己在 `client.Start()` goroutine 外围开第二个 watchdog goroutine,维护 `lastEventTs atomic.Int64`,每个 EventHandler 更新之;watchdog 每 10s 检查 `now - lastEventTs > 60s` → 认为断连 → 记 `reconnectStartedAt`,主动 `Close()` + 重建 Client(触发 SDK 内部重连的同时我方拿到触发点)。Client 重新有事件进来时 `watchdog` 看到 `lastEventTs` 更新 → 认定重连完成 → 发起 gap fetch `(reconnectStartedAt - 10s, now)`。
2. **方案 B**:给 SDK 提 PR 添加 `WithStatusChangeHandler`,但一期不可依赖。
3. 文档 M9 §4.3 整段重写,不再假设 SDK 提供状态事件。

---

### P0-M9-02:`Im.Message.List` 缺 `ContainerIdType`,直接 API 报错

**证据**:SDK `service/im/v1/message.go` 的 `ListMessageReqBuilder` 定义 `ContainerIdType` 为**必填** query param(取值 `chat`/`thread`),但 M9 §4.3 示例
```go
client.Im.Message.List(ctx, larkim.NewListMessageReqBuilder().StartTime(...)...Build())
```
缺这个参数。调用时飞书 API 直接 400 `invalid_param:container_id_type is required`。

**补救**:M9 §4.3 改写为:
```go
req := larkim.NewListMessageReqBuilder().
    ContainerIdType("chat").
    ContainerId(chatID).
    StartTime(strconv.FormatInt(sinceTs, 10)).
    EndTime(strconv.FormatInt(now-30, 10)).  // 见 P0-M9-03
    PageSize(50).
    Build()
```

---

### P0-M9-03:gap fetch 无 EndTime,大群拉回全部消息

**证据**:SDK `StartTime` 是 string 秒级时间戳,同样有 `EndTime`。M9 §4.3 只传 StartTime → 拉 `[since, 服务端 now]` 的全部消息。

**攻击/失误场景**:
- 1000 人活跃群 + longconn 断 10 min → gap fetch 回 ~2k 条消息 + 分页循环;
- 同时多副本(多 chat 并行)→ API 调用爆炸 → 触发 tenant rate limit → 其他 API 也挂;
- 边界时刻 `[lastEventTs, now]` 与 longconn 恢复后的新消息重叠 → M9 dedup 能挡,但浪费 API 额度。

**补救**:
1. `EndTime = now - 30s`,留 30s 给 longconn 正常处理新消息;
2. 分页+并发度限流(每 chat 最多 3 并发,全局 10 并发);
3. 单次 gap fetch 消息数硬上限(比如 500 条/chat),超过记 warn + 只拉最旧 500 条,其他告警让运维手工补偿。

---

### P0-M9-04:`listActiveChats` 盲区 — 断线期间零活跃的群被跳过

**证据**:M9 §4.3 定义 activeChats = "最近 10 min 有 event 的 chat"。

**攻击路径**:
- T=0 longconn 断
- T=5 min Chat A 有群员 @bot 发消息 → 飞书侧堆积,本地未收
- T=15 min longconn 重连 → `listActiveChats` 看到 "最近 10 min 无 event" 里 Chat A 不在(因为断线期间本地看不到事件)
- Chat A 静默跳过 → 这条消息永远丢

**补救**:gap fetch 的 chat 集合不能用"本地最近事件",必须是"bot 所在**全部** chat":
```go
// client.Im.Chat.List 拉 bot 所在 chat 列表(分页)
// 每个 chat_id 独立 gap fetch
```
若 chat 过多(>500),按 chat_id hash 分桶并并行;每 chat 以"本地见过的最新 event_ts"为 StartTime。

---

### P0-M9-05:DistributedDedup fail-open 在 DB 短暂不可用时致双处理

**证据**:M9 §2 说 "DB 挂了 fail-open 不拒绝处理"。

**攻击场景**(蓝军易构造):
- Postgres 主备切换,5s 内 INSERT 超时
- 副本 A 收到 event X,ClaimEvent 超时 → fail-open → 处理 → agent 回复"答案 1"
- 副本 B 同时收到同一 event X(飞书 longconn 广播到双入口;或 webhook+longconn 并存),ClaimEvent 也超时 → fail-open → 处理 → agent 回复"答案 2"
- 用户群里看到两条回复,体验破碎

**补救**:三级策略
1. DB 短超时(<2s,单次重试 1 次)→ 第二次仍失败 fail-**closed**,handler 返 nil 不处理,让 M9 retry queue 兜底;
2. DB 长期不可用(断路器 open)→ 降级到 Redis SETEX 做二级 dedup(Redis 独立可用性);
3. Redis 也挂 → 本副本 LRU 内存 dedup(同副本保护),同时每分钟 metric `feishu.dedup.degraded_to_memory` 触发 page;
4. 绝不 fail-open 到"允许双处理"。

M9 §2 重写 fail 策略。

---

### P0-M9-06:ClaimEvent 单阶段 → claim 后崩溃 = 消息永失

**证据**:M9 §2 用
```sql
INSERT INTO feishu_event_dedup(event_id, ...) VALUES (...)
ON CONFLICT DO NOTHING RETURNING event_id
```
返回 event_id 即视为 claim 成功。

**致命路径**:
- T=0 Replica A ClaimEvent 成功,`feishu_event_dedup` 行已写
- T=1 Replica A 开始处理,agent call LLM
- T=5 Replica A pod OOM / 被 kubelet 杀
- T=6 飞书重投(因 Replica A 未返 200,参见 P0-M8-02)
- T=7 Replica B 收到同 event,ClaimEvent 看 `ON CONFLICT DO NOTHING` → 空返回 → 视作"已处理" → drop
- **消息永久丢失,无人察觉**

**补救**:两阶段 dedup
```sql
CREATE TABLE feishu_event_dedup (
    event_id    TEXT PRIMARY KEY,
    tenant_key  TEXT NOT NULL,
    claimed_at  TIMESTAMPTZ NOT NULL,
    processed   BOOLEAN NOT NULL DEFAULT FALSE,
    processed_at TIMESTAMPTZ,
    worker_id   TEXT NOT NULL,
    ...
);
```
- Phase 1:`INSERT ... ON CONFLICT (event_id) DO UPDATE SET claimed_at=NOW(), worker_id=... WHERE processed=FALSE AND claimed_at < NOW()-INTERVAL '2 minutes' RETURNING event_id` — 只有"未处理且超 2 分钟"的已 claim event 可被新 worker 抢占;
- Phase 2:处理成功后 `UPDATE ... SET processed=TRUE, processed_at=NOW()`;
- Worker 扫 `processed=FALSE AND claimed_at < NOW()-INTERVAL '2 minutes'` 重试。

M9 §2 必须改写 ClaimEvent 为两阶段。

---

### P0-M9-07:retry_queue 并发 worker 无 `FOR UPDATE SKIP LOCKED`

**证据**:M9 §7 retry_queue 描述没说多 worker 如何不抢同一条。默认 SQL
```sql
SELECT * FROM feishu_outbound_retry_queue
WHERE next_retry_at <= NOW() AND retry_count < 5
LIMIT 10
```
多 worker 会抢同一条 → 重复投递。

**补救**:
```sql
BEGIN;
SELECT * FROM feishu_outbound_retry_queue
WHERE next_retry_at <= NOW() AND retry_count < 5 AND status='pending'
ORDER BY next_retry_at
LIMIT 10
FOR UPDATE SKIP LOCKED;
-- 处理
UPDATE ... SET status='processing', worker_id=..., picked_at=NOW() WHERE id IN (...);
COMMIT;
```
M9 §7 明写 SKIP LOCKED + 超时未 ack 自动回到 pending。

---

### P0-M9-08:retry_queue 存完整消息 payload → PII 合规

**证据**:M9 §7 表 schema `payload JSONB` 存整个消息内容 + 保留 7 天。消息内容含聊天历史、用户信息、文件链接。

**合规风险**:GDPR/合规要求"数据最小化",消息明文留 7 天 = 违规面。安全审计要求 at-rest 加密。

**补救**:
1. 只存**重建投递所需最小字段**:`(chat_id, template_id, template_params, api_method, idempotency_key)`,不存原文 prompt;
2. 或全字段 AES-GCM 加密后入库(密钥 per-tenant,密钥轮转跟 M8);
3. 审计 retention 明确 7 天 hard delete(M8-redteam P1-M8-13);
4. M9 §7 schema 改成最小字段列表。

---

### P0-M9-09:gap fetch × listActiveChats 触发飞书 API rate limit

**证据**:飞书 tenant-level `im:message:list` 单 tenant 默认 50 QPS。一次 gap fetch 里 N 个 chat 并行 → 瞬时峰值轻易破 50 QPS → 被限流 → 部分 chat gap fetch 失败 → M9 §4.3 未声明失败后果。

**补救**:
1. gap fetch 必须 token bucket 限流,默认 30 QPS 全局预算(给其他 API 留 40%);
2. 失败 chat 入 `gap_fetch_failed` queue 后台 retry;
3. Prometheus metric `feishu.api.rate_limited{api_method}` + alert。
4. M9 §4.3 加"API 额度预算"子章节。

---

### P0-M9-10:chat affinity 扩缩容时消息乱序

**证据**:M9 §6 提 "k8s session affinity based on hash(chat_id) % replicas"。但 k8s 缩容 3→2 时,某个 chat 的 hash 落点变化,同 chat 的消息一部分打到旧副本一部分打到新副本。

**攻击/失误**:单 chat 里消息 A→B→C,由于 affinity 重建,B 落在副本 X,A/C 落在副本 Y,**handler 内存里 "per-chat 顺序队列" 被打散** → agent 看到的顺序变成 A-C-B。

**补救**:不要依赖副本内存做顺序,消息顺序由:
1. DB `feishu_event_dedup.first_seen` 时间戳全局有序;
2. `master.SessionState` 的 pending 队列内部按 first_seen 排序;
3. session_loop 拉队列时用 `ORDER BY first_seen ASC LIMIT 1 FOR UPDATE SKIP LOCKED`,跨副本安全。
4. M9 §6 明写"顺序保证来自 DB,不来自副本亲和"。

---

### P0-M9-11:retry 5 次后无死信路径

**证据**:M9 §7 提 `retry_count < 5` 的 WHERE,但 `= 5` 时后续如何没声明。

**影响**:失败消息静默留在表里不再处理,运维不知情。

**补救**:
1. `retry_count >= 5 AND status='pending'` → 触发 `status='dead_letter'` UPDATE;
2. 死信 metric `feishu.outbound.dead_letter{reason}` 每次 bump + page;
3. 运维 CLI `ops feishu retry --dead-letter=true --since=X` 手工 replay;
4. M9 §7 新增 §7.5 死信处理。

---

### P0-M9-12:session_id 格式与 M11 多租户冲突

**证据**:M9 §4/§6 不同位置提到 session_id = "im-feishu-{chat_id}",但 M11 §8 强制 "im-feishu-{tenant_key}-{chat_id}"(一期 tenant_key="default" 占位)。M9 未遵守。

**影响**:Phase 0 实施时随意选一版,后期 M11 启用真 tenant 需回头改所有 session id 生成点 + 数据迁移。

**补救**:M9 全文统一为 `im-feishu-{tenantKey}-{chatID}`,一期 tenantKey="default"。M9 §6 加交叉引用 M11 §8。

---

### P0-M9-13:longconn 与 webhook 双入口重复事件

**证据**:M9 §1 架构图同时标 longconn 和 webhook。飞书后台可同时开启两种推送方式,事件会**双投**。DB dedup 能挡,但:
- 架构暗含 "双入口并存"
- 日志告警里会看到每 event `feishu.dedup.hit` 几乎 100%,正常信噪丢失
- API rate limit 双倍压力

**补救**:
1. 文档 M9 §1 明写"生产环境 longconn XOR webhook,不并存";
2. 启动时检测(读飞书后台 config 或自声明 mode),若检测到双开 → 启动 warning 日志;
3. 若业务必须并存(longconn 用于开发/私有化,webhook 用于生产 ISV),则在路由层加 `source` label,metric 分路统计。

---

### P0-M9-14:DistributedDedup 表无 tenant_key 索引导致扫表慢

**证据**:M9 §2 schema 有 `tenant_key TEXT NOT NULL` 但只做 PK(event_id),`tenant_key` 无索引。多租户启用后按 tenant 查询(清理/审计) → seq scan。

**补救**:
```sql
CREATE INDEX idx_feishu_dedup_tenant ON feishu_event_dedup(tenant_key, first_seen DESC);
CREATE INDEX idx_feishu_dedup_unprocessed ON feishu_event_dedup(processed, claimed_at)
WHERE processed=FALSE;  -- partial index for reclaim scan
```
M9 §2 schema 补两个二级索引。

---

### P1-M9-15:dedup 表 TTL 清理无 checkpoint

**证据**:M9 §2 说 3 天 TTL。DELETE 批量删 100k 行会锁表。

**补救**:按 `first_seen` 分区(日分区),TTL = `DETACH PARTITION + DROP`,O(1) 无锁。M9 §2 schema 改月/日分区。

---

### P1-M9-16:gap fetch 重启后无持久化 `lastEventTs`

**证据**:M9 §4.3 `lastEventTs` 是进程内存。副本重启后从 `now` 重算,重启期间的消息**永久丢失**(因为没有人补偿)。

**补救**:`lastEventTs` 持久化到 Postgres 表 `feishu_longconn_checkpoint(replica_id, tenant_key, last_event_ts)` 每 30s 写一次。重启时读此值,无记录则 `now-5min` 兜底 + 写告警。

---

## 修正后 Phase 0 必须加的工作量(Δ)

| 项 | 文档改动 | 代码改动 |
|---|---|---|
| P0-M9-01 watchdog 替代 status handler | M9 §4.3 重写 | longconn watchdog goroutine |
| P0-M9-02 Im.Message.List ContainerIdType | M9 §4.3 修示例 | 实施时注意 |
| P0-M9-03 EndTime + 并发限流 | M9 §4.3 新增 | gap fetch budget |
| P0-M9-04 全量 chat 集合 | M9 §4.3 重写 | Im.Chat.List + 分桶 |
| P0-M9-05 三级 fail 策略 | M9 §2 重写 | dedup + redis + lru |
| P0-M9-06 两阶段 claim | M9 §2 重写 schema | reclaim worker |
| P0-M9-07 FOR UPDATE SKIP LOCKED | M9 §7 明写 | SQL 补 |
| P0-M9-08 payload 最小化 | M9 §7 schema 改 | queue payload reshape |
| P0-M9-10 顺序来自 DB | M9 §6 明写 | session_loop ordering |
| P0-M9-11 死信 | M9 §7.5 新增 | status enum + CLI |
| P0-M9-12 session_id 含 tenant_key | M9 全文 | router.ensureSession |
| P0-M9-13 单入口原则 | M9 §1 声明 | 启动检测 |
| P0-M9-14 索引 | M9 §2 schema | SQL 补 |
| P1-M9-15 分区 TTL | M9 §2 schema | migration |
| P1-M9-16 checkpoint 持久化 | M9 §4.3 | checkpoint table |

## 核心判断

M9 原稿**基于一个不存在的 SDK API**(P0-M9-01),连带 §4.3 整段失效;dedup 的 fail-open 和单阶段 claim 是**系统性丢消息面**(P0-M9-05/06);retry queue 缺并发控制和死信(P0-M9-07/11)。**Phase 0 不能只做 "distributed dedup" 四个字,必须把两阶段 claim + fail-closed 策略 + watchdog 都做完**,否则"可靠性"就是纸面 SLA。

其他(API 预算、checkpoint 持久化)可延到 Phase 5,但文档必须在 Phase 0 就修正。
