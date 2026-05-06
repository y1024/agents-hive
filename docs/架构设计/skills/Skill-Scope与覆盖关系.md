# Public vs Personal Skills（scope 语义 / 路径 / 权限 / 覆盖）

## 1. scope 定义

| scope | 使用者 | 安装者 | 存储 | 准入 |
|-------|--------|--------|------|------|
| `public` | 全体登录用户 | 仅 admin | `$HIVE_DATA/skills/public/<name>/` | `AdminChecker.IsAdmin(userID) = true` |
| `personal` | 仅安装者自己 | 任意登录用户 | `$HIVE_DATA/skills/users/<uid>/<name>/` | 无需额外检查 |

`personal` 是默认 scope。LLM 如果未指定 scope，`skill_install` 默认写入 personal 空间（owner 心智：用户的东西默认在用户目录）。

## 2. 存储路径契约

### 2.1 FS 层

```
$HIVE_DATA/skills/
├── public/                 ← Finder.PublicSkillsDir
│   ├── translator/
│   └── docgen/
└── users/                  ← Finder.PersonalSkillsDir
    ├── alice/
    │   └── nuwa/           ← personal skill
    └── bob/
        └── notion-sync/
```

路径即 scope inference（`scope_test.go` 契约）：

- `…/skills/public/<name>/` → scope=public
- `…/skills/users/<uid>/<name>/` → scope=personal, userID=<uid>
- SKILL.md frontmatter 显式 `scope:` 字段可覆盖 inference

### 2.2 DB 层（OverlayRegistry）

PostgreSQL 表 `skills` 列 `user_id VARCHAR NOT NULL DEFAULT ''`：

| userID | 语义 |
|--------|------|
| `""` (空串) | public skill |
| `"alice"` | alice 的 personal skill |

dbCache key 是复合类型：

```go
type dbCacheKey struct {
    Name   string
    UserID string
}
```

pg_notify payload 同时携带 `name` + `user_id`，避免两用户推同名 personal skill 时互相覆盖（详见 `design.md` §DB schema migration）。

## 3. 四层优先级（Overlay 顺序）

`OverlayRegistry.Get(name, userID)` 严格按以下顺序返回第一命中：

```
1. personal DB   (dbCache[{name, userID}])
2. personal FS   (fsRegistry.Get(name, userID=userID))
3. public DB     (dbCache[{name, ""}])
4. public FS     (fsRegistry.Get(name, userID=""))
```

`List(userID)` 合并：

- personal 层（DB+FS）过滤同名重复，personal DB 优先
- public 层（DB+FS）过滤同名重复，public DB 优先
- 最终列表：personal 优先暴露（同名 public 被遮蔽）

测试锚点：`internal/skills/registry_test.go` 四层 fixture 场景。

## 4. 权限矩阵

| 操作 | scope=public | scope=personal |
|------|--------------|----------------|
| `skill_install` | `AdminChecker.IsAdmin(userID)` 必须为 true，否则拒绝（return `{error: "permission denied"}`） | 无需 admin；只要 `auth.UserIDFrom(ctx) != ""` 即可 |
| `skill_search` | 始终可见 | 仅本人可见；跨租户返回 not-found |
| `skill_get`（隐式，skill.go） | 始终可见 | 仅本人可见 |
| HITL 审批 | 仍走 `input_request{choice_type:"skill_install_confirmation"}` | 同左 |

AdminChecker 语义：

- `auth.Enabled=true` → `NewAuthAdminChecker()` 查 DB 的 role
- `auth.Enabled=false` → `NewDenyAllAdminChecker()` default-deny（避免未 auth 环境下 public 被任意人写入）

详见 `docs/架构设计/skills/Skill-安装安全模型.md`。

## 5. 跨租户隔离证据

验收 §15.6 构造 alice + bob：

1. alice 调 `skill_install(name="nuwa", scope="personal")` → 写入 `users/alice/nuwa/`
2. bob 调 `skill("nuwa", ...)` → Registry.Get("nuwa", "bob") 四层全 miss → not-found
3. bob 调 `skill_search` → 列表不含 nuwa

测试锚点：`internal/skills/registry_test.go` 的 cross-tenant 场景 + `internal/tools/skill_search_test.go` 的 visibility 矩阵。

## 6. 覆盖关系示例

已存在 public `translator@1.0`；alice 再装 personal `translator@2.0`：

| 调用者 | `Get("translator", userID)` 返回 |
|--------|--------------------------------|
| alice | personal `translator@2.0` |
| bob | public `translator@1.0` |

这是预期：personal 覆盖 public，允许用户用自己的版本试错；不污染其他租户。

## 7. 删除语义（follow-up）

当前未暴露 `skill_uninstall` 工具。运维侧直接删目录或 DB 行，Registry 的 pg_notify 事件会自动 invalidate dbCache。用户侧的 uninstall 工具计划在 follow-up change 补齐。
