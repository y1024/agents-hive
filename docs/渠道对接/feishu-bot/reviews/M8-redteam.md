# M8 · 安全合规 — 红队评审报告

> 本报告基于对 larksuite/oapi-sdk-go/v3@v3.5.3 源码的直接审计 + 对 `08-security.md` 的对抗性攻击分析。所有断言都带 SDK 源码 line-number 证据或具体攻击路径。

## 方法

- 直读 SDK 源码:`~/go/pkg/mod/github.com/larksuite/oapi-sdk-go/v3@v3.5.3/`
  - `event/dispatcher/dispatcher.go` (86-395 行)
  - `event/event.go` (50-88 行 EventDecrypt)
  - `event/dispatcher/callback_dispatch.go`
- 枚举 `08-security.md` 每个安全断言,构造绕过路径
- 关注 SDK 自身的假设(签名语义、错误路径、HTTP 状态码)

## P0 发现(设计级致命)

### P0-M8-01:SDK 签名无 replay 窗口校验

**证据**:SDK 只做 `SHA256(timestamp+nonce+key+body) == signature`,**不校验 timestamp 与 now 的差值**。源码:`event/dispatcher/dispatcher.go` 读入 req 后只走 `ParseReq → DecryptEvent`,没有任何 `time.Since(timestamp) > window → reject` 路径。

**攻击**:攻击者一旦截获/获得过一条合法 signed body(泄露 CDN 日志、中间代理、调试环境),可对生产 webhook 无限重放。虽然 DB dedup 会拦截 event_id 重复,但 dedup 表 TTL 3 天后清除 → 第 4 天同一 event 重放成功,触发历史消息被当作新消息处理(agent 被旧内容驱动)。

**补救**:M8 §2.2 webhook 在调 dispatcher 之前,手写 middleware:
```go
ts, _ := strconv.ParseInt(r.Header.Get("X-Lark-Request-Timestamp"), 10, 64)
if math.Abs(float64(time.Now().Unix()-ts)) > 300 {  // ±5 min
    metrics.Counter("feishu.security.replay_window_reject", 1)
    http.Error(w, "timestamp skew", 400); return
}
```
M8 §2 必须新增 §2.5 replay window,明确 300s 窗口 + reject metric。

---

### P0-M8-02:SDK handler 返 err → 500 → 飞书重投 → 绕过 dedup

**证据**:`dispatcher.go:371-373`
```go
err = handler.Handle(ctx, eventMsg)
if err != nil { return nil, err }
```
上游 `processError` 把 err 转成 `500 StatusInternalServerError`(行 63)。飞书服务端对 5xx 默认重试最多 3 次(5s/30s/300s)。

**攻击 / 业务失误路径**:
- 若 M9 `DistributedDedup.ClaimEvent` 成功 → 然后业务代码 panic(比如 cloud-doc API 401)→ handler 返 err → 飞书重投。
- 重投时 ClaimEvent 看到已 claim → drop(fail-open 或 fail-closed 都 drop)→ **消息被静默丢失**。
- 更严重:同一 event 若 agent 已经开始推理(但尚未 commit 回复),在 replica A 崩溃中断,飞书重投到 replica B,replica B 看 dedup 认为已处理 → drop → **用户永远收不到回复**。

**补救**:
1. SDK handler wrapper **永远返 nil**,把业务错误写入 `feishu_outbound_retry_queue` 或 dead_letter;
2. 飞书自己的重试完全由我方主动拒绝,不依赖 HTTP 5xx 语义;
3. ClaimEvent 分两阶段(见 M9-redteam P0-M9-06):claim 成功后必须 UPDATE processed=true,否则 2 min 后允许 reclaim。

---

### P0-M8-03:`isEncrypted()` 辅助函数在 SDK 中不存在

**证据**:grep `~/go/pkg/mod/github.com/larksuite/oapi-sdk-go/v3@v3.5.3` 无 `isEncrypted` 符号。larkcallback 包也未导出此函数。M8 §2.2 示例代码里出现的 `if isEncrypted(req) { ... }` 是**凭空写出**,会 compile error。

**影响**:一期实施者照抄就炸;更严重的是给人"SDK 能区分加密/明文"的错误印象。实际判断需要解析 body JSON 看有无 `encrypt` 字段。

**补救**:M8 §2.2 改写为:
```go
var probe struct{ Encrypt string `json:"encrypt"` }
_ = json.Unmarshal(body, &probe)
if probe.Encrypt == "" && cfg.EncryptKey != "" {
    return errPlainInEncryptedMode
}
```

---

### P0-M8-04:SDK EventDecrypt 不校验 AES-CBC padding

**证据**:`event/event.go:57-88`
```go
mode := cipher.NewCBCDecrypter(block, iv)
mode.CryptBlocks(buf, buf)
n := strings.Index(string(buf), "{")
if n == -1 { n = 0 }
m := strings.LastIndex(string(buf), "}")
```
**PKCS7 padding 完全不校验**,直接按 `{...}` 截取。

**影响评估**:理论上可做 CBC bit-flip 改首尾字节,但飞书 signature 校验是 `SHA256(ts+nonce+key+body_cipher)`,body_cipher 被改一 bit → signature mismatch → dispatcher 拒绝。**所以签名正确时 CBC-bit-flip 无法独立利用**。但结合 P0-M8-01(replay 无窗口)构成:攻击者能原样重放、不能篡改。

**补救**:接受 SDK 的这一弱点,以 P0-M8-01 的 timestamp 窗口 + M9 DB dedup 作为兜底。M8 §安全假设章节需明写"不依赖 SDK 做 MAC 强度保证,replay 防御=timestamp+dedup 二重"。

---

### P0-M8-05:未注册 event 返 200+error-body,静默丢事件

**证据**:`dispatcher.go:341-353`
```go
handler := dispatcher.eventType2EventHandler[eventType]
if handler == nil {
    // 返 HTTP 200 + body 是 err 字符串,只打 Error log
    return eventResp, nil
}
```

**影响**:对飞书"好"(不会重投),但对我方业务:
- 未来新事件类型(比如飞书发布 `im.message.reaction_updated_v2`)→ 我方未注册 → 静默丢 → 业务不知情。
- 若 HITL 按钮的事件类型更新(比如 `card.action.trigger_v2`)→ 按钮按了无反应,运维看不到告警。

**补救**:
1. M7 侧新增 metric `feishu.event.unhandled_type{type_hash}`,Logger 注入 hook,SDK 每打一条 "not found handler" Error 时 bump metric。
2. 告警阈值:任何未知 type 出现 >10 次/分钟触发 page。
3. 兜底 `OnP2CustomizedEvent` 或 `OnEventV2`(若 SDK 提供)注册一个 catch-all,写 audit dead_letter。

---

### P0-M8-06:`ReloadFromConfig` 热轮转竞态窗口

**证据**:M8 §6 方案是 `sync.RWMutex` 保护 `sdkClient *Client` 引用,但 dispatcher 是在 client 初始化时构造的不可变对象(`dispatcher.NewEventDispatcher(token, key)` 返 `*EventDispatcher`,内部 encryptKey/verificationToken 是值拷贝)。

**攻击窗口**:
```
T=0    副本启动,dispatcher_v1 持 key_v1
T=10   SRE rotate encrypt_key,写 secret manager
T=20   ReloadFromConfig 触发 → 构造 dispatcher_v2 持 key_v2
T=20.001  RWLock 切换 sdkClient 指针
```
期间**飞行中**的 HTTP 请求若已进入 dispatcher_v1.Handle() 未完成,继续用 key_v1 处理;新请求用 dispatcher_v2。飞书服务端是否继续用旧 key 加密(旧密文) vs 新 key 加密(新密文)由飞书侧缓存决定,存在**飞书仍发旧密文 + 本地已轮到新 key** 的窗口 → 新 dispatcher 用 key_v2 解旧密文 → DecryptErr → 500 → 飞书重试 → 消息积压。

**补救**:
1. Reload 不重建 dispatcher,改成 dispatcher 持 `atomic.Pointer[KeyMaterial]`,同时 hold 新旧两把 key 共 60s,每条消息解密两把都试;
2. 或飞书管理台两阶段 rotate:先在本地加 key_v2,在飞书台激活 key_v2,等飞书流量全切 key_v2 后本地删 key_v1。
3. M8 §6 必须新增"双 key 并存窗口 60s"明确说明 + 对应 metric `feishu.security.key_rotation_window`。

---

### P0-M8-07:`@all` sanitizer 只禁一种形态,其他路径全开

**证据**:M8 §3.2 只说"禁 @所有人",未枚举飞书消息里的 @all 表达方式。SDK 文档/代码可见以下载体:

| 载体 | 结构 |
|---|---|
| text 消息 | `"@_all"` (user_id_type=user_id 的特殊值) 或 `"@all"` 字符串 |
| post 富文本 | `tag: "at"` + `user_id: "all"` |
| interactive 卡片 | `"tag":"at","user_id":"all"` 或 `at_all:true` 元素 |
| merge_forward | 嵌套消息里任一 @all |
| image/file 消息 | `caption` 字段里的文本 @all |
| 卡片模板 | 模板变量 rendered 后变成 @all |

**攻击**:agent 被 prompt-injection 诱导输出卡片 JSON,里面 `"at_all":true` → sanitizer 只过 text 字段 → 绕过。

**补救**:sanitizer 必须是**白名单**模式:
1. 卡片 JSON 递归遍历,拒绝任何 `{tag:"at", user_id:"all"|"all_members"}` 节点;
2. 拒绝顶层 `at_all / at_chat_all` 布尔字段;
3. text 正则 `@_?all\b` case-insensitive;
4. merge_forward 递归进入嵌套消息应用同规则;
5. 模板渲染后**二次 sanitize**(渲染前不可见的 @all 变量只在渲染后显示)。

M8 §3 需要展开成表格+代码示例。

---

### P0-M8-08:PII grep CI gate 可简单绕过

**证据**:M8 §5.2 CI gate
```bash
grep -E '^\+.*logger\.(Info|Debug).*("open_id"|"union_id"|"user_id")' diff
```

**绕过**:
- `logger.Info("sender", openID)` — 变量名 `openID` 不在 regex 里
- `logger.Info("uid", o.Id)` — 字段访问
- 拼字符串 `"open_"+"id"` → grep 看不到
- zap.String("sender", userObj.GetOpenId()) — method call
- %v 格式化整个 struct:`logger.Info("evt", zap.Any("msg", msg))` msg 里嵌 open_id

**补救**:
1. **AST 级**检查(用 go/ast):扫所有 logger.* call,参数不得包含 `open_id`/`union_id`/`user_id` **字段**或**类型** `*larkim.UserId`;
2. 强制只能通过 `imctx.SafeSenderID()` 取得并 log 的值;
3. 让 logger wrapper 自己对 open_id-like 值做脱敏(字符串匹配 `/^ou_[a-f0-9]{40,}$/` 自动 hash)——**纵深防御**。
4. 增加 pre-commit hook + CI + runtime(日志采样审计 → 发现 open_id 原值告警)三重。

M8 §5.2 需要升级为 AST + runtime 双重。

---

### P0-M8-09:HealthStatus 跨副本不一致 → 降级决策错乱

**证据**:M8 §4 `PermissionDeniedCount` 是**本副本内存** rolling 窗口。3 副本部署时:
- 副本 A 碰到 10 次 `permission_denied` → degraded=true,快速失败返用户错误
- 副本 B 尚未碰到 → normal
- k8s service 轮询打到 B,用户无感 → 监控看不到
- 打到 A 用户收到 fail-fast 错误

**攻击**:攻击者只向 A 发 bot permission 相关请求(通过 session affinity hash 控制),让 A 降级,B 保持 normal,监控静默。

**补救**:`feishu_health_status(tenant_key, key, count, window_start)` 入 Postgres,每副本每 5s flush + 读。或用 Redis SETEX counter。M8 §4 加"跨副本 health 共享" 子章节。

---

### P0-M8-10:debug echo 不经 sanitizer 导致 PII 泄露

**证据**:M10 §8.2 `debug_mode=true` 时 bot 先回一条折叠卡片含"原始 feishu event payload"。M8 §3 sanitizer 只管"agent 输出",不管"debug echo"。

**攻击**:群管 `/debug on`,群员发含手机号/身份证的消息 → bot 把原始 payload 折叠卡片 echo 回群 → 群员 C 在消息历史里搜 `/debug` → 看到大段历史 PII。

**补救**:
1. debug echo payload 必须先过 sanitizer(PII 字段脱敏 + sender_id 替换为 SafeSenderID);
2. debug 卡片默认"仅群管可见"(飞书卡片 only_visible_to);
3. 文档 M8 §3 + M10 §8 同步声明。

---

### P0-M8-11:webhook URL 被探测 + 压力放大

**证据**:M8 §2 没声明 URL 层防御。无 signature 的请求(扫描器探测)会进入 SDK dispatcher → `ParseReq → DecryptEvent` 失败 → `processError` 返 500 + 打 Error log。

**攻击**:
- 攻击者对 `/webhook/feishu` 发 10k QPS 空 POST → 每条 500 + Error log → 日志系统爆炸 + 合法请求延迟;
- 或伪造 `X-Lark-Request-Timestamp` 探测 timing oracle。

**补救**:
1. Nginx/ingress 上 URL 级 rate limit(per IP 10 rps);
2. SDK 入口前自己 middleware:`Header["X-Lark-Signature"] == ""` 直接 401,不走 SDK,不打 Error log;
3. metric `feishu.webhook.signature_missing{ip_prefix_hash}` 超阈值触发 IP ban。

---

### P0-M8-12:audit log 同步写 Postgres 成瓶颈

**证据**:M8 §7 没声明 audit 是 sync 还是 async。从典型实现看是 handler 内部直接 INSERT → 每条消息至少一次 DB I/O → 高峰期 handler 被 DB 阻塞。

**攻击 / 失误**:飞书大群活动(几百人 @bot)→ 千 QPS → DB 连接池耗尽 → 所有 handler 超时 → 飞书 5 xx 重试风暴 → M9 dedup 正常但 retry_queue 爆炸。

**补救**:
1. audit 走 buffered channel + 独立 flusher goroutine(batch 100 或 1s);
2. channel 满时 drop + metric+告警,绝不阻塞 handler;
3. 关键安全事件(key rotation, permission revoked)同步写,业务事件 async。
4. M8 §7 声明 "audit 异步 + 批量,业务关键路径永不因 audit 阻塞"。

---

### P1-M8-13:audit log retention 未定义

表只增不减,365 天后数十 GB。补 retention job:按月分区,保留 N 个月,之外 drop partition。M8 §7 新增 §7.4 retention。

---

### P1-M8-14:verification_token 进 error 日志

SDK `processError` 把 err.Error() 塞进 `logger.Error`,signature mismatch 的 err 包含 expected signature → 间接暴露部分秘密状态(虽非 token 本身,但便于字典攻击)。

**补救**:注入自定义 Logger(SDK 支持),signature 相关 err 统一脱敏成 "signature verification failed"。

---

## 修正后 Phase 0 必须加的工作量(Δ)

| 项 | 文档改动 | 代码改动 |
|---|---|---|
| P0-M8-01 replay 窗口 middleware | M8 §2.5 新增 | webhook.go 新增 TimestampGuard |
| P0-M8-02 handler 吞 err | M8 §2.3 改写 | dispatcher wrapper 永返 nil |
| P0-M8-03 isEncrypted 修正 | M8 §2.2 改写 | 手写 probe 逻辑 |
| P0-M8-05 未注册 event 告警 | M7 metric 表新增 | Logger hook |
| P0-M8-06 双 key 并存 | M8 §6 改写 | KeyMaterial atomic.Pointer |
| P0-M8-07 @all 白名单递归 | M8 §3 展开 | sanitizer AST |
| P0-M8-08 AST 级 PII gate | M8 §5.2 升级 | go/ast tool + runtime log hook |
| P0-M8-09 health 跨副本 | M8 §4 新增 | Postgres/Redis counter |
| P0-M8-10 debug 过 sanitizer | M8 §3 + M10 §8 | debug handler 串 sanitizer |
| P0-M8-11 URL 层防御 | M8 §2 新增 §2.6 | middleware + nginx rate limit |
| P0-M8-12 audit 异步 | M8 §7 新增 | channel + flusher |
| P0-M8-13 retention | M8 §7.4 新增 | 月分区 + job |
| P0-M8-14 logger 脱敏 | M8 §5 新增 | custom logger impl |

## 核心判断

M8 原稿是**架构骨架**(SDK-only + encrypt/sig wire + sanitizer 方向),但缺**攻击面枚举**和**边界条件证明**。上述 12 个 P0 全部发生在"文档未声明但实施者必踩"的地方。Phase 0 不能只做 "encrypt_key wire", 必须把 P0-M8-01/02/06/07/08/11 **列入 Phase 0**,否则上线就是 P0 事故面。

其他(P0-M8-09/10/12/13/14)可延后到 Phase 5,但文档必须在 Phase 0 就补齐,防止遗忘。
