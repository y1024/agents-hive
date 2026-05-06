# M11 · 多租户(ISV 场景)

> 一个 Hive 实例服务多个飞书应用(app) / 多个租户。当前仍未进入真正多租户实现，但 Phase 0/1 需要把若干前向兼容硬前置落到代码，而不是只写文档。

## 1. 场景与决策

### 1.1 场景

| 模式 | 描述 | 是否必要 |
|---|---|---|
| 单 app 单租户 | 自建应用,只在本公司用 | ✅ 一期默认 |
| 单 app 多租户 | 自建应用但拉给多个飞书企业用(罕见) | 可选 |
| ISV 应用 | 公开应用,任意企业都能安装 | 🟡 预留接口,一期不实现 |
| 一个 Hive 接多个 app(多品牌) | 同一部署服务多个独立应用 | 🟡 预留接口 |

### 1.2 一期决策

- 代码里**所有**飞书 API 调用、session 路由、dedup key、ACL 查询都**必须携带 `tenant_key`** 作为维度
- `FeishuClient` 当前仍是单实例使用方式,但 `ClientRegistry.Get(tenantKey) → *Client` stub 已落地,一期 registry 里只放一个
- DB 表凡涉及飞书数据都带 `tenant_key` 字段(dedup、chat binding、audit、retry queue 已在各模块写进去)
- 预留 `TenantResolver` 接口已落地,一期返固定 tenant(`default`)

**当前真实状态**:
- `TenantResolver` / `ClientRegistry` stub 已实现并有单测
- `session_id` 已从第一天带 `tenant_key`
- 现有部分飞书 metric 已开始补 `tenant_key_hash`
- 真正的多 tenant client 路由、tenant config overlay、ISV 安装流程仍未实现

## 2. 数据模型

### 2.1 Tenant 注册

```sql
CREATE TABLE IF NOT EXISTS feishu_tenants (
    tenant_key    TEXT PRIMARY KEY,
    app_id        TEXT NOT NULL,
    app_secret    TEXT NOT NULL,  -- 加密存储(使用 secret manager)
    encrypt_key   TEXT,           -- 可选
    verification_token TEXT,
    app_type      TEXT NOT NULL,  -- "self" / "isv"
    region        TEXT NOT NULL DEFAULT 'cn',  -- "cn" = Feishu, "intl" = Lark
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    installed_at  TIMESTAMPTZ,
    last_seen_at  TIMESTAMPTZ,
    config_json   JSONB,  -- tenant 级 override(rate limit、灰度等)
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 2.2 现有表扩展 `tenant_key` 列

所有 M8/M9/M10 表都已声明 `tenant_key TEXT NOT NULL`,一期写入固定值 `"default"`,M11 启用时开始填真实 tenant_key。

## 3. 接口预留

### 3.1 TenantResolver

```go
// internal/channel/feishu/tenant.go
type TenantResolver interface {
    // FromEvent 从飞书 event 里提取 tenant_key。
    // 一期实现:返固定 "default"
    // M11:读 event.Header.TenantKey
    FromEvent(event *larkevent.EventV2Base) string

    // FromHTTP 从 HTTP 请求提取(push API 由调用方声明)
    FromHTTP(r *http.Request) (string, error)
}

// 一期 stub:
type SingleTenantResolver struct{ TenantKey string }
func (s *SingleTenantResolver) FromEvent(_ *larkevent.EventV2Base) string { return s.TenantKey }
func (s *SingleTenantResolver) FromHTTP(_ *http.Request) (string, error) { return s.TenantKey, nil }
```

### 3.2 ClientRegistry

```go
type ClientRegistry interface {
    Get(tenantKey string) (*Client, error)
    Register(tenantKey string, client *Client) error
    Unregister(tenantKey string) error
    List() []string
}

// 一期 stub:单实例
type SingleClientRegistry struct {
    client *Client
    key    string
}
func (s *SingleClientRegistry) Get(tenantKey string) (*Client, error) {
    if tenantKey != s.key && tenantKey != "default" {
        return nil, fmt.Errorf("tenant not found: %s", tenantKey)
    }
    return s.client, nil
}
```

所有 `*Client` 的持有者改为 `ClientRegistry`,使用时 `reg.Get(tenantKey).XxxAPI(...)`。

## 4. Router 路由扩展

`router.go processMessageImpl`:

```go
tenantKey := r.tenantResolver.FromEvent(event)
client, err := r.clientRegistry.Get(tenantKey)
if err != nil {
    metrics.Counter("feishu.tenant.unknown", 1, "tenant_key_hash", hashShort(tenantKey))
    return  // 未注册 tenant 的 event drop
}
// 把 client 放进 ctx,下游从 ctx 取
ctx = context.WithValue(ctx, clientCtxKey{}, client)
```

下游(resolver / lifecycle handler / outbound 等)**不再直接引用 `*Client`**,都从 ctx 取。

## 5. ISV 安装/卸载(预留)

### 5.1 事件

- `app.installed_v2` → ISV 应用被某企业安装 → 向 `feishu_tenants` 插入一行
- `app.uninstalled_v2` → 卸载 → `enabled = false`(不删,保留审计)

### 5.2 Webhook 支持多 app

一个 HTTP endpoint `/webhook/feishu` 需要能处理多个 app 的 encrypt_key。larkcallback 内置 dispatcher 不支持多 key 多路复用,需要**按路径区分**:

```
/webhook/feishu/{app_id}   # 每个 app 一个 endpoint
```

从 URL path 取 `app_id` → 查 `feishu_tenants.app_id → tenant_key` → 选对应 dispatcher 解密。

### 5.3 飞书后台配置

每个接入的企业需要在飞书后台把 webhook URL 填 `https://<hive-host>/webhook/feishu/<app_id>`。

## 6. Tenant 级 Config

tenant 特有 config(rate limit 额度、是否开 push、白名单等)存在 `feishu_tenants.config_json`,Router 查 chat binding 前先 overlay:

```go
effectiveCfg := globalCfg
if tenantCfg != nil {
    effectiveCfg = merge(globalCfg, tenantCfg)
}
```

可由 tenant 的管理员自行通过 API 调整,不影响其他 tenant。

## 7. 隔离要求

| 维度 | 隔离方式 |
|---|---|
| Session | `session_id = "im-feishu-{tenant_key}-{chat_id}"` |
| Dedup | 当前 schema `event_id PRIMARY KEY` 依赖飞书 `event_id` 全局唯一(跨 tenant 不冲突);`tenant_key` 列存在用于审计 / metric label,**不进主键**。若未来观察到 `event_id` 跨 tenant 碰撞,再迁移到 `(tenant_key, event_id)` 复合主键 |
| Cache(UserCache) | `key = tenantKey + ":" + openID` |
| Metric label | 所有 feishu metric 加 `tenant_key_hash` label(不是明文,避免 PII 暴露) |
| Audit log | 每行必含 tenant_key |
| Rate limit | 全局 bucket + per-tenant bucket + per-chat bucket |

## 8. 一期落地要求(Phase 0 硬性,不可推迟)

> **CEO 决议(2026-04-22)·红队 M9 P0-12 修正**:`session_id` 格式必须从**第一天**就带 `tenant_key`,否则 M11 真正启用多租户时要做全表迁移,且 dedup/session/audit 交叉一致性会全面回头改。

**即使不实现多租户,Phase 0 MVP 必须做到**:

1. 所有新建 DB 表带 `tenant_key TEXT NOT NULL DEFAULT 'default'` 列
2. 所有 metric 加 `tenant_key_hash` label(值为 `"default"`)
3. **Session ID 格式固定为** `im-feishu-{tenant_key}-{chat_id}`,由 `feishu/session_id.go` `BuildSessionID(tenantKey, chatID string) string` 唯一入口构造。源码层 CI grep 禁止别的拼法。
4. `TenantResolver` / `ClientRegistry` 接口就位,stub 实现返固定值 `"default"`
5. 日志字段 `tenant_key` 一律透传
6. Dedup 表 `tenant_key` 非空(Phase 0 `feishu_event_dedup` 已定义)
7. Retry queue 骨架表 `tenant_key` 非空(Phase 0 `feishu_outbound_retry_queue` 骨架)

**Phase 0 不做**(M11 真启用时再补):
- ISV 安装流程
- 多 app webhook 路由
- Tenant 级 config UI

当真正需要多租户时,按本文 §5 实现安装流程 + §6 tenant config overlay。**因为 session_id / dedup / metric / cache 的 tenant_key 维度 Phase 0 就位,Router / Plugin / Master 代码零改动**。

## 9. 测试

一期单测:
- SingleClientRegistry.Get("default") 返固定 client;其他 key 返 err
- Session ID 格式含 tenant_key
- Dedup 表 `tenant_key` 列存在且非空
- 所有 metric 断言含 `tenant_key_hash` label

预留测试(M11 真实现时):
- 多 tenant 注册后,A 发的消息不影响 B 的 UserCache
- ISV 安装 event → tenant 行写入;卸载 event → enabled=false
- 跨 tenant 越权(tenant A 的 API 带 tenant B 的 chat_id)→ 拒绝

## 10. 代码锚点

| 位置 | 作用 | 一期 |
|---|---|---|
| `internal/channel/feishu/tenant.go` | **新建**,TenantResolver 接口 + SingleTenantResolver | ✅ 必做 |
| `internal/channel/feishu/client_registry.go` | **新建**,ClientRegistry 接口 + SingleClientRegistry | ✅ 必做 |
| `internal/channel/feishu/tenant_repo.go` | **新建**,feishu_tenants DB 访问 | M11 真做时 |
| `migrations/<ts>_feishu_tenants.sql` | **新建** | M11 真做时 |
| 所有 M8/M9/M10 新建表 | 带 `tenant_key` 列 | ✅ 一期就加 |
| `internal/channel/router.go` | tenantResolver + clientRegistry 注入点 | ✅ 一期就接 |
| `internal/bootstrap/server.go` | 装配 SingleTenantResolver + SingleClientRegistry | ✅ 一期就接 |
