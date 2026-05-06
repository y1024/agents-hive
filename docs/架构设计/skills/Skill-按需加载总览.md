# Skills On-Demand（按需安装）总览

## 1. 目标

把 skill 从"服务启动时全量静态加载"升级为"运行期按 LLM/用户请求拉取 + 审批安装"，同时支持 public（管理员下发全体用户）与 personal（按租户隔离）两种 scope。

## 2. 整体架构

```
┌─────────────────────── Hive 主进程 ───────────────────────┐
│                                                          │
│  Master ──→ Spec Planner ──→ requirements: [...]         │
│       │                                                  │
│       ▼                                                  │
│  SpecSkillResolver.Resolve(reqs, userID)                 │
│    ├─ Registry.FindBySpecRequirements (local)            │
│    └─ Discovery.ResolveByRequirements (remote)           │
│                                                          │
│  LLM ──→ tool skill_install(name, scope, source)         │
│       │                                                  │
│       ▼                                                  │
│  HITL input_request{choice_type:"skill_install_confirmation"}
│       │                      │                           │
│       │ deny                  │ approve                  │
│       ▼                      ▼                           │
│   返回 user-declined        Discovery.PullOne            │
│                                │                         │
│                                ▼                         │
│                    $HIVE_DATA/skills/(public|users/<uid>)│
│                                │                         │
│                                ▼                         │
│              OverlayRegistry.RegisterFromPath            │
│                    (scope, userID)                       │
│                                │                         │
│                                ▼                         │
│            dbCache[{name,userID}] + fsCache              │
│                                │                         │
│                                ▼                         │
│         BroadcastGenericMessage("skill.install.progress")│
└──────────────────────────────────────────────────────────┘
```

### 2.1 关键组件

| 组件 | 文件 | 职责 |
|------|------|------|
| `Registry` | `internal/skills/registry.go` | FS 层加载 SKILL.md；`Get/List` 按 userID 过滤 |
| `OverlayRegistry` | `internal/skills/overlay_registry.go` | DB > FS 分层；dbCache 复合 key `{name,userID}` |
| `Discovery` | `internal/skills/discovery.go` | 远程 marketplace 拉取 + 本地缓存 |
| `SpecSkillResolver` | `internal/skills/spec_resolver.go` | 本地 miss → 远程 fallback 二级聚合 |
| `AdminChecker` | `internal/skills/admin_checker.go` | public scope 的准入，goroutine-safe |
| `skill_install` tool | `internal/tools/skill_install.go` | 6 阶段 HITL 安装 |
| `skill_search` tool | `internal/tools/skill_search.go` | 本地 + 远程合并列表 |

### 2.2 优先级（四层覆盖关系）

`OverlayRegistry.Get(name, userID)` 严格按如下顺序返回第一命中：

```
personal DB  >  personal FS  >  public DB  >  public FS
```

详见 `docs/架构设计/skills/Skill-Scope与覆盖关系.md` §3。

## 3. 用户对话样例

### 3.1 按需安装（happy path）

```
用户: 帮我生成一张"赛博朋克城市夜景"的图
AI:   (Spec Planner 抽取 requirement=image_generation → 本地无命中)
      (Discovery 远程查到 nuwa skill provides:["image_generation"])
AI:   本地尚未安装 nuwa 技能，需要安装吗？
      [tool] skill_install(name="nuwa", scope="personal", source="https://marketplace.../nuwa")
AI:   (input_request) 请确认安装 nuwa 到 personal 空间？
用户: 确认 (approve)
AI:   ✓ 已下载 nuwa@1.2.0 到 $HIVE_DATA/skills/users/alice/nuwa
      ✓ 已注册到 Registry
AI:   (重试原 LLM 调用，skill("nuwa", params...))
AI:   [图片渲染完成]
```

### 3.2 Self-heal（已在 tool 层提示）

用户直接调用未安装的 skill 时，`skill.go` 返回：

```json
{
  "error": "获取技能 \"nuwa\" 失败: not found",
  "suggested_action": {
    "tool": "skill_install",
    "args": { "name": "nuwa", "scope": "personal", "source": "https://..." },
    "reason": "skill \"nuwa\" 未在本地注册，可通过 skill_install 从 marketplace 安装"
  }
}
```

LLM 可根据该 hint 主动发起 skill_install。

## 4. 运维部署步骤

### 4.1 开启 on-demand（零破坏灰度）

```yaml
# config.yaml
agent:
  skills:
    on_demand_enabled: true           # ★ 主开关
    marketplace_urls:
      - https://skills.example.com/
    public_skills_dir: /opt/hive/skills/public
    personal_skills_dir: /var/lib/hive/skills/users
security:
  auth:
    enabled: true                     # public scope 强烈建议开 auth
```

### 4.2 热切换回滚

直接把 `on_demand_enabled` 改为 `false` 并滚动重启。已安装在 `$HIVE_DATA/skills/users/` 下的 personal skill **不受影响**，只是 `skill_install` / `skill_search` 工具不再注册。

### 4.3 Feature flag 组合

4 位矩阵见 `docs/架构设计/skills/Skill-Feature-Flag矩阵.md`：

- `specdriven_enabled`
- `subagent_mode`
- `skills_semantic_routing`
- `skills.on_demand_enabled`

16 组合中 10 组 valid + 6 组 bootstrap fail-fast。

### 4.4 日志 grep 锚点

启动时一行：

```
skills_feature_flags: specdriven=true subagent_mode=true semantic_routing=true on_demand=true
```

直接用 `grep skills_feature_flags` 定位当前实例激活组合。

## 5. 相关文档

- 协议：`docs/架构设计/Skill-市场协议.md`
- scope 语义：`docs/架构设计/skills/Skill-Scope与覆盖关系.md`
- 安全模型：`docs/架构设计/skills/Skill-安装安全模型.md`
- Feature flag：`docs/架构设计/skills/Skill-Feature-Flag矩阵.md`
- SubAgent 身份继承：`docs/subagent-identity-inheritance.md`
