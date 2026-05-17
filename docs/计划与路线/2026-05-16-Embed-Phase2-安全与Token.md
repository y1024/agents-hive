# Phase 2：Scoped Token 与安全隔离（修正版）

> **执行优先级:** P0
> **目标:** 先定义可嵌入 Agent 的配置源和 scoped token 安全边界，再开放任何 `/api/v1/embed/...` 接口。

## 核心修正

Embed API 不能先于安全模型实现。旧计划把安全放在 Phase 2，但实际执行必须把本文件作为 P0。

另外，浏览器端 token 天然会暴露。安全目标不是“隐藏 token”，而是让暴露后的 blast radius 足够小：

- 短有效期。
- 绑定 tenant、agent、widget、origin。
- 绑定可用工具和风险等级。
- 可撤销、可限流、可审计。
- 不具备后台 admin 权限。

## 可嵌入 Agent 配置源

当前 `/api/v1/agents` 不是业务可发布 Agent 的可靠来源。P0 需要先落一个配置源。

推荐表：

```sql
CREATE TABLE IF NOT EXISTS hive_embed_agent_configs (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    domain_id       TEXT NOT NULL DEFAULT 'generic',
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    base_prompt_key TEXT NOT NULL DEFAULT '',
    base_prompt     TEXT NOT NULL DEFAULT '',
    default_tools   JSONB NOT NULL DEFAULT '[]'::jsonb,
    allowed_origins JSONB NOT NULL DEFAULT '[]'::jsonb,
    context_schema  JSONB NOT NULL DEFAULT '{}'::jsonb,
    ui_config       JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    version         INTEGER NOT NULL DEFAULT 1,
    published_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_embed_agent_configs_tenant ON hive_embed_agent_configs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_embed_agent_configs_domain ON hive_embed_agent_configs(domain_id);
```

最小字段说明：

- `default_tools`: 此 Agent 在 embed 模式下最多可用的工具集合。
- `allowed_origins`: 默认允许加载该 Agent 的业务域名。
- `context_schema`: 允许注入的业务上下文字段、类型、大小限制。
- `ui_config`: Widget 展示配置，例如名称、头像、欢迎语、主题色。
- `domain_id`: 默认 `generic`；非 `generic` 必须通过 host-side `domainpolicy` 校验。
- `version`: 发布配置版本，变更发布时递增；MVP 不做完整版本历史表。

没有这个配置源时，不应实现 `/api/v1/embed/agents/{agentID}`。

## Token Claims

```go
type EmbedClaims struct {
    jwt.RegisteredClaims

    TokenID   string `json:"tid"`
    TenantID  string `json:"tenant_id"`
    WidgetID  string `json:"widget_id"`

    AgentIDs       []string `json:"agent_ids"`
    AllowedOrigins []string `json:"allowed_origins"`
    AllowedTools   []string `json:"allowed_tools,omitempty"`
    Scopes         []string `json:"scopes"`

    MaxSessions       int `json:"max_sessions,omitempty"`
    RateLimitPerMin   int `json:"rate_limit_per_min,omitempty"`
    MaxMessagesPerDay int `json:"max_messages_per_day,omitempty"`
}
```

约束：

- `exp` 必填，浏览器 token 默认 30 分钟，允许范围 5-60 分钟。
- `jti` 或 `TokenID` 必填，用于撤销和审计。
- `AgentIDs` 生产不支持 `*`；内部测试可临时支持，但必须由测试配置显式打开。
- `AllowedOrigins` 必须是精确 origin 或受控 wildcard，例如 `https://*.acme.com`。
- `Scopes` 最小为 `session:create`、`message:write`、`stream:read`。

## Token 存储

```sql
CREATE TABLE IF NOT EXISTS hive_embed_tokens (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    widget_id       TEXT NOT NULL,
    agent_ids       JSONB NOT NULL DEFAULT '[]'::jsonb,
    scopes          JSONB NOT NULL DEFAULT '[]'::jsonb,
    allowed_origins JSONB NOT NULL DEFAULT '[]'::jsonb,
    allowed_tools   JSONB NOT NULL DEFAULT '[]'::jsonb,
    token_hash      TEXT NOT NULL UNIQUE,
    expires_at      TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_embed_tokens_hash ON hive_embed_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_embed_tokens_tenant ON hive_embed_tokens(tenant_id);
CREATE INDEX IF NOT EXISTS idx_embed_tokens_widget ON hive_embed_tokens(widget_id);
```

只存 token hash，不存明文。创建 token 时只返回一次明文。

## Admin API

需要普通后台 JWT + admin role：

```
POST   /api/v1/admin/embed/agents
GET    /api/v1/admin/embed/agents
PATCH  /api/v1/admin/embed/agents/{id}
DELETE /api/v1/admin/embed/agents/{id}

POST   /api/v1/admin/embed/tokens
GET    /api/v1/admin/embed/tokens
DELETE /api/v1/admin/embed/tokens/{id}
```

创建 token 请求：

```json
{
  "tenant_id": "acme-corp",
  "widget_id": "checkout-assistant",
  "agent_ids": ["customer-service"],
  "scopes": ["session:create", "message:write", "stream:read"],
  "allowed_origins": ["https://portal.acme.com"],
  "allowed_tools": ["kb.doc.meta", "kb.doc.structure", "kb.section.text", "order_lookup"],
  "expires_in": "30m",
  "max_sessions": 20,
  "rate_limit_per_min": 60
}
```

## Embed Middleware

P0 需要新增独立 middleware，不走普通用户 JWT 逻辑：

1. 从 `Authorization: Bearer` 提取 token。
2. 验证 JWT 签名、过期时间、issuer、audience。
3. 计算 hash，查询是否存在且未撤销。
4. 校验 Origin。
5. 校验 scope。
6. 应用 rate limit 和并发 session 限制。
7. 把 `EmbedClaims` 放入 request context。

路由注册时必须显式挂载：

```go
embed := s.embedMiddleware(http.HandlerFunc(s.handleEmbedCreateSession))
```

不要依赖全局 auth middleware 的默认放行规则。如果全局 middleware 会拦截 `/api/v1/*`，需要明确把 `/api/v1/embed/` 加入 public bypass，再由 embed middleware 接管。

## Origin 与 CORS

规则：

- `Origin` 缺失时，浏览器请求拒绝；服务端 SDK 可走单独 server token 或明确配置。
- 返回 `Access-Control-Allow-Origin` 必须是匹配到的具体 origin，不能在带凭据场景返回 `*`。
- `OPTIONS` preflight 也必须验证请求 origin 是否允许。
- wildcard 只允许子域匹配，不允许 `https://evilacme.com` 误匹配 `*.acme.com`。

## Browser Token 策略

生产接入方式：

```html
<script
  src="https://cdn.example.com/hive-widget.js"
  data-api-url="https://hive.example.com"
  data-agent="customer-service"
  data-token-endpoint="/api/hive/embed-token"
  async>
</script>
```

Widget 向业务系统自己的 `/api/hive/embed-token` 请求短期 token。业务系统后端再调用 Hive Admin/API 签发短期 scoped token。

仅开发和内网 PoC 允许：

```html
<script data-token="short_lived_scoped_token"></script>
```

禁止把长期平台 token、admin token、MCP token、第三方 API key 写入 HTML。

## 审计

每个安全决策记录字段：

- `tenant_id`
- `widget_id`
- `agent_id`
- `token_id`
- `origin`
- `session_id`
- `decision`
- `reason`

至少记录以下事件：

- token created
- token revoked
- token denied
- origin denied
- agent denied
- scope denied
- rate limited
- session created
- session closed

## 测试要求

- `go test ./internal/auth -run Embed -v`
- `go test ./internal/api -run TestEmbedAuth -v`
- `go test ./internal/store -run EmbedToken -v`
- `go test ./... -v`

关键用例：

- 过期 token 被拒绝。
- revoked token 被拒绝。
- token 不包含 `message:write` 时不能发送消息。
- origin 精确匹配通过，伪相似域名拒绝。
- preflight 不泄漏宽松 CORS。
- token allowlist 与 agent allowlist 取交集。
