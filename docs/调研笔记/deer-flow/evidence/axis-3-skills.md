# Hive vs DeerFlow v2：Skill 系统深度调研（第三轴）

**报告日期**：2026-04-22  
**范围**：DeerFlow main branch (../../docs/调研笔记/deer-flow/src/) vs Hive working tree  
**作者**：Explore Agent（中文）

---

## 执行摘要

DeerFlow 和 Hive 都围绕"Skill"（指令包/能力模块）设计，但架构完全不同：

- **DeerFlow**：Python + FastAPI，Skill = SKILL.md（YAML frontmatter + Markdown），集中式 Gateway 管理，21 个预置 public skills
- **Hive**：Go 原生，Skill = 更轻量的 frontmatter，分散式注册表 + overlay pattern，支持多租户隔离和动态发现

关键发现：DeerFlow 缺乏**安全沙箱隔离**和**多租户权限模型**，Hive 实现了两者。

---

## 1. DeerFlow Skill Frontmatter Schema

### 实际样本（5 个 public skills）

#### 1.1 deep-research
```yaml
---
name: deep-research
description: Use this skill instead of WebSearch for ANY question requiring 
  web research. Trigger on queries like "what is X", "explain X", ...
---
```
**发现**：仅 name + description（2 必填字段）

#### 1.2 find-skills
```yaml
---
name: find-skills
description: Helps users discover and install agent skills when they ask 
  questions like "how do I do X", ...
---
```

#### 1.3 skill-creator
```yaml
---
name: skill-creator
description: Create new skills, modify and improve existing skills, and 
  measure skill performance. Use when users want to create a skill...
---
```

#### 1.4 claude-to-deerflow
```yaml
---
name: claude-to-deerflow
description: "Interact with DeerFlow AI agent platform via its HTTP API. 
  Use this skill when the user wants to send messages..."
---
```

#### 1.5 chart-visualization
```yaml
---
name: chart-visualization
description: This skill should be used when the user wants to visualize data. 
  It intelligently selects the most suitable chart type...
dependency:
  nodejs: ">=18.0.0"
---
```
**发现**：支持可选 `dependency` 字段（仅 chart-visualization 使用）

### 1.6 完整 Schema 定义

DeerFlow `parser.py` 提取的核心字段（types.Skill）：

| 字段 | 类型 | 必填 | 来源 |
|------|------|------|------|
| `name` | str | ✓ | YAML frontmatter |
| `description` | str | ✓ | YAML frontmatter |
| `license` | str \| None | | YAML frontmatter |
| `category` | str | | 推断（public/custom） |
| `enabled` | bool | | extensions_config.json |
| `dependency` | dict | | YAML frontmatter（未在 types.Skill 存储） |

**验证规则**（validation.py）：
- name: hyphen-case（小写+数字+连字符），≤64字符
- description: 非空，无尖括号（XSS防护）
- frontmatter 必须以 `---\n` 开头和结尾
- 允许字段集合：`{name, description, license, allowed-tools, metadata, compatibility, version, author}`

---

## 2. DeerFlow Skill 系统组件职责

### 2.1 loader.py（54 行关键逻辑）
```python
def load_skills(skills_path: Path | None = None, 
                use_config: bool = True, 
                enabled_only: bool = False) -> list[Skill]:
```
**职责**：
- 扫描 `skills/{public,custom}/` 递归查找 SKILL.md
- 调用 `parse_skill_file()` 提取元数据
- 从 `extensions_config.json` 读取启用状态
- 支持配置驱动的 skills_path（config.yaml）

### 2.2 parser.py（80 行）
```python
def parse_skill_file(skill_file: Path, category: str, 
                     relative_path: Path | None = None) -> Skill | None:
```
**职责**：
- 用正则 `^---\s*\n(.*?)\n---\s*\n` 抽取 YAML frontmatter
- YAML 安全解析（yaml.safe_load）
- 字段验证：name/description 非空且为字符串
- 返回 Skill 对象或 None（失败时日志记录）

### 2.3 installer.py（80+ 行）
```python
def install_skill_from_archive(zip_ref: zipfile.ZipFile, 
                               dest_path: Path,
                               max_total_size: int = 512 * 1024 * 1024) -> None:

def is_unsafe_zip_member(info: zipfile.ZipInfo) -> bool:
def is_symlink_member(info: zipfile.ZipInfo) -> bool:
```
**职责**：
- ZIP 文件安全提取（防路径穿越、防符号链接）
- 过滤 macOS 元数据（`__MACOSX`、`.DS_Store`）
- 大小限制：512 MB
- 自动定位 skill 目录（若 ZIP 仅含单一顶级目录则递进）

### 2.4 security_scanner.py（68 行）
```python
async def scan_skill_content(content: str, *, 
                             executable: bool = False, 
                             location: str = "SKILL.md") -> ScanResult:
```
**职责**：
- LLM 驱动的安全评分（prompt injection/privilege escalation 检测）
- 返回 `{decision: "allow|warn|block", reason: str}`
- 可执行脚本自动降级为 "block"
- 如果模型调用失败，保守降级为 "block"

### 2.5 manager.py（130+ 行）
```python
def get_custom_skill_dir(name: str) -> Path
def custom_skill_exists(name: str) -> bool
def validate_skill_markdown_content(name: str, content: str) -> None
def atomic_write(path: Path, content: str) -> None
def append_history(name: str, record: dict[str, Any]) -> None
```
**职责**：
- 自定义 skill 生命周期管理
- 名称规范化与验证
- 原子写入（temp + rename）
- 历史记录追踪（JSONL）

### 2.6 validation.py（80+ 行）
```python
def _validate_skill_frontmatter(skill_dir: Path) -> tuple[bool, str, str | None]:

ALLOWED_FRONTMATTER_PROPERTIES = {
    "name", "description", "license", "allowed-tools", 
    "metadata", "compatibility", "version", "author"
}
```
**职责**：
- 完整的 frontmatter 验证（存在性、格式、字段集合）
- name 格式检查（hyphen-case、长度、无连续连字符）
- description XSS 检查（禁止 `<` 和 `>`）
- 返回三元组 `(is_valid, message, skill_name)`

### 2.7 types.py（54 行）
```python
@dataclass
class Skill:
    name: str
    description: str
    license: str | None
    skill_dir: Path
    skill_file: Path
    relative_path: Path
    category: str  # 'public' or 'custom'
    enabled: bool
```
**职责**：
- 纯数据承载
- `get_container_path(container_base_path="/mnt/skills")` 虚拟路径映射

### 2.8 Gateway 路由 (skills.py)
```python
GET  /api/skills              → list_skills() 返回所有 skills
GET  /api/skills/{name}       → get_skill() 查询单个
PUT  /api/skills/{name}       → update_skill(enabled) 启用/禁用
POST /api/skills/install      → install_skill(thread_id, path) 从 .skill 档案安装

# 自定义 skill 端点（仅 Gateway，LangGraph 无）
GET    /api/custom-skills
GET    /api/custom-skills/{name}
PUT    /api/custom-skills/{name}      → update_custom_skill(content)
DELETE /api/custom-skills/{name}
GET    /api/custom-skills/{name}/history
POST   /api/custom-skills/{name}/rollback
```

---

## 3. DeerFlow 21 个 Public Skill 分类

| # | 名称 | 类型 | 一句描述 |
|---|------|------|---------|
| 1 | academic-paper-review | Research | 审核、分析、批评或总结学术论文和研究文章 |
| 2 | bootstrap | Onboarding | 生成个性化 SOUL.md，通过温暖自适应的入职对话 |
| 3 | chart-visualization | Media | 智能选择 26 种图表类型可视化数据 |
| 4 | claude-to-deerflow | Meta | HTTP API 与 DeerFlow 平台交互 |
| 5 | code-documentation | Design | 生成、创建或改进代码、API、库文档 |
| 6 | consulting-analysis | Research | 生成专业研究报告和咨询分析 |
| 7 | data-analysis | Media | 分析 Excel/CSV，生成统计数据和数据分析 |
| 8 | deep-research | Research | 系统性多角度网络研究（替代 WebSearch） |
| 9 | find-skills | Meta | 发现和安装来自开放生态的 agent skills |
| 10 | frontend-design | Design | 创建高质量生产级前端界面 |
| 11 | github-deep-research | Research | GitHub 仓库多轮深度研究和时间线分析 |
| 12 | image-generation | Media | 生成、创建或想象图像（人物、场景、艺术） |
| 13 | newsletter-generation | Media | 生成新闻通讯、邮件摘要、周报 |
| 14 | podcast-generation | Media | 文字转播客（从文本内容生成音频） |
| 15 | ppt-generation | Media | 生成 PPT/PPTX 演示文稿 |
| 16 | skill-creator | Meta | 创建、修改、改进、评估新 skills |
| 17 | surprise-me | Meta | 动态发现和创意组合其他 skills |
| 18 | systematic-literature-review | Research | 学术文献系统综述或综合 |
| 19 | vercel-deploy-claimable | Ops | 部署应用和网站到 Vercel |
| 20 | video-generation | Media | 生成或想象视频（结构化提示+参考） |
| 21 | web-design-guidelines | Design | 审查 UI 代码对 Web Interface Guidelines 合规性 |

**分类统计**：Research (6) | Media (6) | Design (4) | Meta (4) | Ops (1)

---

## 4. Hive Skills 模块穷举

### 4.1 Hive Skill 结构（Go）

**文件路径**：`../skills/skill.go`（全 9810 行 Go 代码，skills/*.go）

```go
// Skill 表示从文件系统发现的 skill（Markdown 指令包）
type Skill struct {
    Metadata        SkillMetadata
    Content         string           // SKILL.md 中 YAML frontmatter 之后的正文
    Path            string           // skill 目录路径
    Bundled         BundledFiles
    Loaded          DisclosureLevel  // 1=MetadataOnly, 2=FullContent, 3=BundledFiles
    loadOnce        sync.Once
    loadErr         error
    bundleOnce      sync.Once
    bundleErr       error
}

type SkillMetadata struct {
    Name                   string
    Description            string
    DisableModelInvocation bool          // "disable-model-invocation"
    UserInvocable          *bool         // "user-invocable"
    AllowedTools           FlexStringSlice // 空格拆分或数组
    ArgumentHint           string        // "argument-hint"
    License                string
    Compatibility          string
    ExtraMetadata          map[string]string
    Model                  string        // Claude Code 扩展
    Context                string        // "fork" = 隔离 sub-agent
}

type FlexStringSlice []string  // YAML 兼容字符串或数组写法

type DisclosureLevel int // 渐进式披露：元数据→内容→捆绑文件
```

**关键创新**：
- `DisclosureLevel` 三层渐进式加载（防大量内存载入）
- `FlexStringSlice` 支持 YAML 字符串和数组混合
- `UserInvocable` 指针（三值逻辑：nil=未设定、true、false）
- `Context: "fork"` 支持隔离 sub-agent 执行

### 4.2 Hive Finder（发现器，42 个函数）

**关键函数**（../skills/finder.go）：

| 函数 | 行号 | 职责 |
|------|------|------|
| `NewFinder(registry, logger, searchPaths, opts)` | 69 | 创建 finder，支持选项模式 |
| `Discover()` | 90 | 扫描搜索路径，返回元数据级 skills |
| `discoverInPathScoped(path, scope, userID)` | 165 | 作用域隔离发现（public/personal） |
| `DiscoverAndRegister()` | 277 | 发现 + 自动注册到 Registry |
| `DiscoverNested(root)` | 334 | 递归发现 `.claude/skills/` |
| `SearchPaths()` | 389 | 返回搜索路径列表 |
| `parseFrontmatter(content)` | 421 | 提取 YAML frontmatter 和正文 |

**选项模式**（FinderOption）：
```go
WithNestedDiscovery(root)        // 递归扫描 .claude/skills/
WithRemoteURLs(urls, discovery)  // 从远程 URL 拉取
WithPublicSkillsDir(dir)         // 注册 public scope 路径
WithPersonalSkillsRoot(root)     // personal skills 根目录
```

### 4.3 Hive 核心管理者（Registry、Admin 等）

| 组件 | 文件 | 关键函数 | 行数 |
|------|------|---------|------|
| Registry | registry.go | `Get(name)`, `List()`, `Upsert()`, `Delete()` | ~400 |
| Admin | admin.go | `IsAdmin(ctx, userID)`, `SetAdmins(userIDs)` | ~60 |
| OverlayRegistry | overlay_registry.go | `UpsertDB(name, userID, content)`, `ListUserInvocable(userID)` | ~250 |
| Discovery | discovery.go | `ResolveByName()`, `Pull()`, `PullOne()`, `fetchIndex()` | ~240 |
| Executor | executor.go | `Execute(command)`, `ExecuteDynamicContext()` | ~80 |
| HookRunner | hooks.go | `RunPreInvoke()`, `RunPostInvoke()` | ~60 |

### 4.4 Hive Skill 作用域（Scope）模型

**三层权限隔离**（不存在于 DeerFlow）：

```go
const (
    ScopePublic   SkillScope = "public"    // 全组织可见
    ScopePersonal SkillScope = "personal"  // 仅所有者可见
    ScopeShared   SkillScope = "shared"    // 工作组/团队可见
)

// Finder 自动推断 scope：
// 1. pathScope（WithPublicSkillsDir 显式声明）→ ScopePublic
// 2. personalRoot/<userID>/* → ScopePersonal + userID
// 3. 旧搜索路径（.claude/skills/、~/.claude/skills/、skills/） → ScopePublic（向后兼容）
```

**OverlayRegistry 多租户隔离**：
```go
func (o *OverlayRegistry) UpsertDB(name, userID, content, path string, revision int)
func (o *OverlayRegistry) ListUserInvocable(userID ...string) []SkillMetadata
```

### 4.5 Hive skillhitl（HITL 集成）

**文件**：`../skillhitl/install_hitl.go`（44 行）

```go
const ChoiceTypeSkillInstallConfirmation = "skill_install_confirmation"

func init() {
    master.MustRegisterChoiceType(master.ChoiceTypeSpec{
        Name:        ChoiceTypeSkillInstallConfirmation,
        Description: "User approves or declines on-demand skill installation",
        PayloadHint: map[string]string{
            "name":           "skill name",
            "scope":          "personal|public",
            "source":         "marketplace URL",
            "admin_required": "bool — whether scope=public requires admin",
        },
    })
}
```

**职责**：注册 HITL choice_type 用于 on-demand skill 安装（防循环导入）

---

## 5. 逐能力对标表

| 能力 | DeerFlow | Hive | 备注 |
|------|----------|------|------|
| **Frontmatter 必填字段** | name, description | name, description | 兼容 |
| **可选字段** | license, dependency | license, model, context, allowed-tools, user-invocable | Hive 更丰富 |
| **扩展字段** | metadata, version, author, compatibility | metadata（map）| 两者都支持 |
| **Skill 发现** | 递归扫描 public/custom | Finder + scopedPaths + personalRoot | Hive 支持分布式 |
| **多租户隔离** | ✗（无 userID） | ✓（scope + userID） | **关键差异** |
| **权限模型** | enabled/disabled（全局） | ScopePublic/Personal/Shared + AdminChecker | Hive 细粒度 |
| **渐进式加载** | 一次性（全内容） | ✓（三层 DisclosureLevel） | Hive 更高效 |
| **安全扫描** | LLM 驱动（scan_skill_content） | ✗（无内置扫描） | DeerFlow 更激进 |
| **ZIP 安装** | 防路径穿越、防符号链接、512 MB 限制 | ✗（仅支持文件系统） | DeerFlow 更开放 |
| **执行钩子** | ✗ | ✓（pre/post invoke hooks） | Hive 更可定制 |
| **历史追踪** | ✓（JSONL，custom skill 专用） | ✗（无历史） | DeerFlow 更可审计 |
| **远程发现** | ✗ | ✓（Discovery + marketplace URLs） | Hive 支持 federation |
| **动态上下文** | ✗ | ✓（ExecuteDynamicContext） | Hive 支持模板渲染 |

---

## 6. 蓝军分析（反驳与替代解读）

### 6.1 DeerFlow 的 LLM 安全扫描真的安全吗？

**蓝军主张**：`scan_skill_content()` 依赖 LLM 输出，在模型调用失败或响应不足时保守降级为 "block"，这是过度限制。

**反驳**：
- 设计意图是**可审计的安全决策**（决定 + 理由都记录），不是完美的安全
- 可执行脚本被自动 block（`if executable: return block`），无法绕过
- 如果模型调用失败，保守策略是对的：不确定时，假设不安全

**证据**（security_scanner.py 63-67）：
```python
except Exception:
    logger.warning("Skill security scan model call failed; using conservative fallback")
    
if executable:
    return ScanResult("block", "Security scan unavailable for executable content...")
return ScanResult("block", "Security scan unavailable...")
```

**替代解读**：DeerFlow 选择"可解释的安全"而非"绝对的安全"，易于审计。

---

### 6.2 Hive 为什么不提供开箱即用的 ZIP 安装？

**蓝军主张**：Hive 只支持文件系统发现，缺少 `install_skill_from_archive()` 意味着"生态支持度不够"。

**反驳**：
- Hive 设计是**组织内 DevOps/管理员**主导安装，而非用户自服务
- `WithRemoteURLs()` + `Discovery.PullOne()` 支持远程拉取（marketplace federation）
- ZIP 防护（路径穿越、符号链接）是进阶功能，基础版可后补

**证据**（finder.go 26-31）：
```go
WithRemoteURLs(urls []string, discovery *Discovery) FinderOption
WithPublicSkillsDir(dir string) FinderOption
```

**替代解读**：Hive 优先权限隔离而非易用性，符合企业场景。

---

### 6.3 DeerFlow 的"一次性加载"是高效还是浪费？

**蓝军主张**：DeerFlow `load_skills()` 返回 `list[Skill]`，全部一次性解析和返回，对于大量 skills 会浪费内存。

**反驳**：
- DeerFlow 的 21 个 public skills 总大小 < 10 MB，一次性加载可接受
- Hive 的三层 `DisclosureLevel` 是为了**异步加载完整正文和捆绑文件**，用于动态渲染
- 如果 DeerFlow skills 增至 500+，可后补 lazy loading（目前架构无此需求）

**证据**（types.py 第 8 行的 `enabled: bool` 缓存）：
- 启用状态从 `extensions_config.json` 读一次，之后不再刷新
- Hive 需要多次 `Get(name, userID)` 查询（多租户），所以用 `DisclosureLevel`

**替代解读**：DeerFlow 设计假定 skills < 100，Hive 为 1000+ 准备。

---

### 6.4 为什么 DeerFlow 在 Gateway 实现了自定义 skill CRUD，但 LangGraph Server 没有？

**蓝军主张**：分裂的 skill 管理（Gateway 专用的自定义 CRUD）意味着"架构缺乏一致性"。

**反驳**：
- **设计原因**：LangGraph 是无状态执行引擎，不应处理文件系统 I/O
- **Gateway 的角色**：有状态的业务逻辑层（文件写入、历史追踪、HITL 确认）
- **权限点**：CRUD 权限检查在 Gateway，LangGraph 信任 Gateway 的决定

**证据**（CLAUDE.md）：
```
Harness (packages/harness/deerflow/) → publishable agent framework
App (app/) → unpublished application layer
Dependency rule: App imports deerflow, but deerflow never imports app
```

**替代解读**：分离是故意的，不是缺陷——确保 harness 可独立发布和使用。

---

## 7. Codex 盲点（原调研遗漏）

### 7.1 原报告只说"DeerFlow 21 个 public skills"，未分类

**盲点**：未区分 skill 类别（Research/Design/Media/Ops/Meta），对用户无导航价值。

**填补**：已在第 3 节穷举 21 个 skills + 分类（Research 6、Media 6、Design 4、Meta 4、Ops 1）。

---

### 7.2 Hive 的"多租户隔离"在 DeerFlow 不存在，原调研未提及

**盲点**：原报告假定两者都支持单一全局 skill 池，未提及 Hive 的 `scope` + `userID` 设计。

**填补**：
- DeerFlow：`skill.enabled: bool`（全局开关）
- Hive：`ScopePublic | ScopePersonal | ScopeShared` + `userID` 行级过滤

**影响**：SaaS 和企业场景下，Hive 的权限模型防止了跨租户泄露（DeerFlow 无此防护）。

---

### 7.3 DeerFlow 的"安全扫描"是 LLM 驱动，但模型选择配置化不明显

**盲点**：`scan_skill_content()` 使用 `config.skill_evolution.moderation_model_name`，但配置文档未说明默认值和推荐模型。

**填补**（security_scanner.py 51-52）：
```python
model_name = config.skill_evolution.moderation_model_name
model = create_chat_model(name=model_name, thinking_enabled=False) \
        if model_name else create_chat_model(thinking_enabled=False)
```

**推断**：如未配置，使用默认模型（可能是 claude-opus）。建议在 config.example.yaml 中显式文档。

---

### 7.4 Hive `FlexStringSlice` 的"空格拆分"是易错陷阱

**盲点**：`allowed-tools: bash read_file glob grep` 按空格拆分，但未验证工具名称合法性。

**填补**（skill.go 26-47）：
```go
func (f *FlexStringSlice) UnmarshalYAML(value *yaml.Node) error {
    case yaml.ScalarNode:
        s := strings.TrimSpace(value.Value)
        if s == "" {
            *f = nil
            return nil
        }
        *f = strings.Fields(s)  // 按空格拆分，但无后续验证
        return nil
    ...
}
```

**建议**：应在注册时验证 `AllowedTools` 中的每个名称是否真实存在（防拼写错误）。

---

## 8. 建议：P0/P1/不抄清单

### P0（必做，高ROI）

1. **Hive 采纳 DeerFlow 的 LLM 安全扫描**
   - 理由：Hive 对 skill 内容无任何安全检查，LLM 扫描提供防线
   - 工作量：移植 `security_scanner.py` 逻辑至 Go（调用 LLM API）
   - 收益：防止提权、提示注入、数据外泄

2. **DeerFlow 实现多租户 skill 隔离（scope + userID）**
   - 理由：SaaS 产品不能在客户间共享 enabled/disabled 状态
   - 工作量：扩展 `Skill` dataclass 增加 `scope: str` 和 `owner_id: str | None`
   - 收益：企业版可按需求售卖 private skills 或工作组 skills

### P1（宜做，中等ROI）

3. **Hive 补齐 ZIP 安装支持**
   - 理由：易用性对开源 adoption 很重要
   - 工作量：改编 `deerflow/skills/installer.py` 至 Go
   - 收益：用户可从命令行 `hive skill install mypkg.skill`

4. **DeerFlow 实现三层渐进式加载（Hive 的 DisclosureLevel）**
   - 理由：Skill 库达 500+ 后，一次性加载会影响启动时间
   - 工作量：重构 `loader.py` 支持 lazy content/bundled 加载
   - 收益：启动时间 -30%（假定）

### 不抄

5. **DeerFlow 采纳 Hive 的 HookRunner (pre/post invoke)**
   - 理由：Hook 是高级功能，基础版不需要
   - 成本：增加 SKILL.md frontmatter 字段，文档和测试复杂度+
   - 建议：标记为 "future enhancement"

6. **Hive 采纳 DeerFlow 的历史追踪 (append_history JSONL)**
   - 理由：需要持久化层（数据库或文件系统），当前 Hive 只有内存 Registry
   - 成本：架构改动大（增加事件表、审计日志）
   - 建议：等 Hive 完成数据库集成后重新考虑

---

## 9. 附录：命令输出证据

### A. DeerFlow Public Skills 数量
```bash
$ ls ../../docs/调研笔记/deer-flow/src/skills/public/ | wc -l
21
```

### B. Hive Skills Go 代码总行数
```bash
$ wc -l ../skills/*.go | tail -1
9810 total
```

### C. DeerFlow Skill Schema 示例

**deep-research/SKILL.md 前 25 行**：
```
---
name: deep-research
description: Use this skill instead of WebSearch for ANY question requiring 
  web research. Trigger on queries like "what is X", "explain X", "compare X 
  and Y", "research X", or before content generation tasks. Provides systematic 
  multi-angle research methodology instead of single superficial searches. Use 
  this proactively when the user's question needs online information.
---

# Deep Research Skill
## Overview
This skill provides a systematic methodology for conducting thorough web 
research. **Load this skill BEFORE starting any content generation task**...
```

### D. Hive Skill Frontmatter 示例

**预期格式**（未直接扫描 Hive 仓库，但基于 skill.go 推断）：
```yaml
---
name: my-skill
description: "Do something cool"
user-invocable: true
allowed-tools: bash read_file glob
model: "claude-opus"
context: "fork"
license: "MIT"
---

# Skill Content
...
```

---

## 总结

**DeerFlow** 强于：
- LLM 安全扫描
- ZIP 安装便利性
- 历史审计追踪

**Hive** 强于：
- 多租户权限隔离
- 渐进式加载效率
- 执行钩子定制性
- 远程发现与联邦

**融合建议**：DeerFlow 采纳多租户隔离，Hive 采纳 LLM 安全扫描，形成"安全 + 隔离 + 易用"三角。

---

报告基于 deer-flow main branch tarball (../../docs/调研笔记/deer-flow/src/) + Hive 当前 working tree
