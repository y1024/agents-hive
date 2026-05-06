# skill_install 安全模型

## 1. HITL（Human-in-the-loop）审批

### 1.1 为什么不用 `PermissionRule.Ask`？

`permission-minimalism` change 把 `createPermissionPromptFn` 重构成**默认 `Granted: true`**，只有 `bash` 与 `security.shell_tool_names` 命中的工具才跑 policy。其余工具的 `PermissionRule.Ask` 形同虚设。

→ 依赖 `{skill_install: Ask}` 默认权限规则的设计会被静默 bypass。

### 1.2 使用的审批通道

`skill_install` 改走**业务决策 HITL 通道**：

```go
emitter.EmitInputRequest(ctx, &mcphost.InputRequest{
    SessionID:  sessionID,
    ChoiceType: "skill_install_confirmation",
    Payload: map[string]any{
        "name":   params.Name,
        "scope":  params.Scope,
        "source": params.Source,
    },
})
```

- **choice_type 注册**：`internal/master/choice_type_registry.go` 的 `init()` 显式调 `MustRegisterChoiceType(ChoiceTypeSpec{Name: "skill_install_confirmation", …})`
- **Handler 契约**：首次 emit 前 `IsRegisteredChoiceType("skill_install_confirmation")` 必须为 true（`host_emit.go:32-34` 会硬校验 `ErrUnregisteredChoiceType`）
- **前端渲染**：IM EventRenderer / WebSocket 前端 `useHiveAgentEvents` 按 `choice_type` 分发到审批 UI

### 1.3 审批 payload 字段

| 字段 | 语义 | 前端渲染建议 |
|------|------|-------------|
| `name` | skill 名称 | 粗体标题 |
| `scope` | `public` / `personal` | 颜色区分：public = 警告色 |
| `source` | marketplace URL | 链接或摘要显示 |
| `version` | 可选版本号 | 副标题 |
| `provides_requirements` | 可选 | 只读列表 |

### 1.4 超时 / 拒绝 / 取消

| 状态 | 返回给 LLM 的 tool_result |
|------|-------------------------|
| 用户 approve | 进入 downloading/registering 阶段 |
| 用户 deny | `{error: "user declined skill_install", suggested_action: null}` |
| 超时（默认 120s） | `{error: "skill_install confirmation timeout"}` |
| ctx cancel（会话断开） | goroutine 退出，stage worker 清理（见 §4） |

## 2. 准入（AdminChecker）

### 2.1 接口契约

```go
type AdminChecker interface {
    IsAdmin(ctx context.Context, userID string) bool
}
```

**goroutine-safe 要求**（D16）：实现必须支持并发调用。默认实现使用 `atomic.Pointer` 或 `sync.RWMutex` 包裹 DB 视图；热更新时直接 swap pointer，读路径零锁。

### 2.2 两种实现

| 场景 | 实现 | 语义 |
|------|------|------|
| `auth.Enabled=true` | `NewAuthAdminChecker()` | 查 user 表的 `role=admin` |
| `auth.Enabled=false` | `NewDenyAllAdminChecker()` | 始终 false（**default-deny**） |

default-deny 是强硬约束：没有 auth 的环境下，任何人都不得写入 public 空间。这比"默认允许"安全。

### 2.3 scope=personal 的免检

`skill_install(scope="personal")` 不调 AdminChecker，只要 `auth.UserIDFrom(ctx) != ""` 就可继续。路径落在 `users/<uid>/`，跨租户不可见。

## 3. 未签名 skill 风险 + checksum

### 3.1 当前现状

- `index.json` 的 `checksum` 字段是 **optional**
- Discovery 目前**不强制校验**（follow-up）
- 安装来源的安全性完全依赖 marketplace HTTPS + 运维信任

### 3.2 风险

| 风险 | 后果 |
|------|------|
| 中间人篡改 tar.gz | 安装恶意 SKILL.md 脚本 |
| marketplace 域名劫持 | 全体用户拉到恶意版本 |
| skill 脚本执行敏感命令 | 取决于 Hive 工具栈权限（`shell_tool_names` 屏障） |

### 3.3 运维信任链建议

1. Marketplace URL 必须是 **HTTPS**
2. 生产环境白名单 `marketplace_urls`，不要开放"任意 URL"
3. 内部 marketplace 启用 checksum 字段并本地维护 sha256 清单
4. skill 脚本层的 shell 执行仍受 `security.shell_tool_names` + `bash` 权限规则约束（和普通工具一致）

### 3.4 Checksum 强制化 follow-up

后续 change（`skill-install-checksum-enforcement`）将：

- `Discovery.PullOne` 下载后计算 sha256
- 若 index 提供 checksum，不匹配即中止安装并回滚
- 若不提供 checksum，记 WARN 日志但不阻断（灰度期兼容）

## 4. Goroutine 生命周期

`skill_install` 有 6 阶段：`resolving → awaiting_approval → downloading → registering → done/error`。每个阶段都可能是异步 goroutine。

**安全契约**（D17，对应 §15.17 验收）：

- 所有 stage worker 必须监听 `ctx.Done()`
- `ctx cancel` 时：停止 HTTP 下载、删除半下载的临时目录、回滚 DB 行
- 单测用 `go.uber.org/goleak.VerifyNone(t)` 确保 decline / timeout / mid-download cancel 三路径都无泄漏

## 5. 审计日志

每次 `skill_install` 成功/失败都写结构化日志：

```
skill_install: userID=alice name=nuwa scope=personal source=https://… 
               stage=done duration_ms=1234 version=1.2.0
```

便于事后审计"谁在什么时间安装了什么 skill"。`grep skill_install` + 时间范围即可出审计报告。
