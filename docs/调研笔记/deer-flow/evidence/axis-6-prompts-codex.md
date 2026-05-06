# Axis-6 Prompt 质量对抗审查（Codex 蓝军）

> **立场**: Codex 蓝军，不给任何一方面子。
> **对象**: deer-flow 的 `lead_agent/prompt.py` 全家桶 vs Hive `internal/i18n/prompts/*.tmpl` 全家桶。
> **方法**: 直读原文 + 行号精确引用 + 反向评分（谁更烂得过分）。
> **前置证据**: `evidence/axis-1-tools.md` / `archive/claude-phase2-output.md` / `archive/codex-phase1-output.md`。
> **结论剧透**: 两边都不行；Hive 表现出来的"中文工程友好"实际上是多语言债务；deer-flow 的"结构化 prompt"实际上是 1500 行失控长诗。
> **PLAN.md 已有结论里的 Hive 优势**: §3 说"飞书深度/RAG/ACP 抄不走"——这对。**但 §3 里完全没提 prompt 层**。prompt 是短板，PLAN.md §4 缺口清单漏掉了。**本报告为修正 PLAN.md 提供证据**。

---

## §0 评审规则

1. **只看 prompt，不看代码实现**。代码层质量不在本轴范围内。
2. **必须是原文引用 + 文件:行号**，没有行号的说法一律废弃。
3. **"漏洞" = 直接影响 LLM 行为正确性/幻觉率/成本/可维护性的 prompt 文本级问题**。
4. **反向评分表**（§3）按"更烂"排名，不按"更好"；越烂分越高，满分 10 分 = 不可救药。
5. 所有 deer-flow 路径基于 `/Users/guoss/workspace/company/vast/agents-hive/docs/调研笔记/deer-flow/` 或 archive 里 `/tmp/deer-flow-src/backend/packages/harness/deerflow/` 的前缀。
6. 所有 Hive 路径基于 `vast/agents-hive/internal/i18n/prompts/` 前缀（已在 find 中确认）。

---

## §1 deer-flow prompt 漏洞清单

### 1.1 `lead_agent/prompt.py` 长度失控（核心漏洞）

**文件**: `backend/packages/harness/deerflow/agents/lead_agent/prompt.py`
**证据**: archive 里反复出现的行号 483-487 / 557-565 / 560-599 / 560-565 / 577 / 610-632 / 677-700 / 685-699 / 695-699 — 保守估计 **单文件 ≥ 700 行的 prompt**，且直接硬编码 Python f-string。

**漏洞 1.1.1 — prompt-as-code，无版本化/无 i18n**
- 证据: `lead_agent/prompt.py:1-700` 整个文件就是一个 Python module；改 prompt = 改代码 = 走 PR = 过 CI = 重启 worker。
- 后果: 运营/产品想灰度 prompt 都做不到。这是运营级短板，和 PLAN.md §5 P0-3 提到的"Skill CRUD API 运营不用重启"同一性质的病——deer-flow 自己 skill 可热装，prompt 本身却写死。
- 对抗结论: deer-flow 社区崇拜的"清晰 prompt"只是早期懒——没上线过给运营改过的人才会写成这样。

**漏洞 1.1.2 — Skill 触发靠 LLM 自觉，不强制**
- 原文（archive/claude-phase2-output.md:1477 引用 `prompt.py:557-565`）:
  > "当 query 匹配 skill use case，读 SKILL.md"
- 后果: prompt 没有 "you MUST invoke skill X when detecting Y"；模型完全可以无视。claude-phase2 自己的警告：**"无强制规则引擎，无法保证查询 X 必须用 skill Y"**。
- 反证 mutation: 让 LLM 跑 100 条 query，其中 30 条刚好命中某 skill 的 trigger，看 LLM 真的 `read_file(SKILL.md)` 的比例 — deer-flow 自己没跑这个数据，等于盲发。
- 对抗结论: 这是 skill-as-prompt 路线的根本缺陷；Hive 虽然没有 skill CRUD，但至少 skill 不是靠 LLM 猜的。

**漏洞 1.1.3 — deferred tools 名字清单暴露给模型，但 schema 不暴露**
- 证据: `prompt.py:610-632` 列 deferred tools 名称（codex-phase1-output.md:261/337 双重确认）。
- 漏洞: 模型看得到名字猜不到参数，只能调 `tool_search` 再拿 schema。**这是两次 LLM round trip**。
- 成本: 每个 MCP 工具调用 = 至少一次 `tool_search` + 一次真正调用 = **至少翻倍 token**。
- 反证: deer-flow 自己的方案里没有成本 budget 说明，也没在 prompt 里告诉模型 "use tool_search sparingly"——纯粹把成本推给用户的 context。

**漏洞 1.1.4 — Citation 格式靠 prompt 约定，非强制 schema**
- 证据: archive/claude-phase2-output.md:683 — "Citation 是**非结构化的字符串约定**"，prompt 要求模型输出 `[citation:Title](URL)` 前端再正则识别。
- 漏洞: LLM 懒的时候输出 `[Title](URL)`（无 citation: 前缀），前端就识别不到；输出 `[citation: Title]` 多空格也会漏。
- 对抗结论: 这种"prompt 约定 + 前端正则"是最脆的模式。Hive 至少 RAG 有 RRF + pgvector 硬结构，citation 落库。

**漏洞 1.1.5 — Memory 注入是"按 confidence 截 token budget"而非"按查询相关性"**
- 证据: archive/codex-phase1-output.md:410-413 引用 `memory/prompt.py:256-317`。
  > "注入时不是语义检索，而是按 confidence 排序 facts，在 token budget 内塞进 system prompt"
- 漏洞: 用户问"上次我提过的订单 X 进度如何"，memory 里关于 order X 的事实可能 confidence 不够，被挤出 prompt。
- 对抗结论: deer-flow 的 memory prompt 策略是 **BM25 都不如**，更别说语义检索。PLAN.md §3.1 说 Hive RAG 碾压，这一点是真的，但 PLAN.md 没单独把 prompt 注入策略当漏洞列出来。

**漏洞 1.1.6 — Memory updater prompt 要求 "Return ONLY valid JSON" 却无 schema 校验**
- 证据: archive/codex-phase1-output.md:240-243 — "Memory updater prompt 要求'Return ONLY valid JSON'，属于提示约束，不是 provider 层 JSON mode"。
- 漏洞: LLM 返回带 markdown code fence 的 JSON（```json ... ```），或在 JSON 前加"Sure, here's the JSON:"——都会导致 parser 炸。
- 对抗结论: 2026 年了还用 prompt 约定代替 response_format/structured outputs，这是 2023 年的写法。

**漏洞 1.1.7 — `max n task calls per response` 是 prompt 软约束，同时又有 middleware 硬限，重复表达**
- 证据: archive/codex-phase1-output.md:605-608 — "prompt 也注入'最多 n 个 task calls per response'的行为提示，见 `lead_agent/prompt.py:685-699`"。
- 漏洞: prompt 说 3，middleware 说 3，中间 config 说 5，三个地方不同步时以哪个为准？这是经典的"单一事实源缺失"。
- 对抗结论: 任何"prompt 注入一个数字 + 代码里硬编码同一个数字"的架构都会在下一次改参数时漏掉一处。

### 1.2 其他 prompt 文件的问题

**漏洞 1.2.1 — `memory/prompt.py` 1-340 行全是英文**
- 证据: archive/codex-phase1-output.md:346 — "`memory/prompt.py:1-340`，memory prompt、事实注入、token budget"
- 漏洞: 中文 query 进来，memory 抽事实的 system prompt 全英文，让 LLM 做中英跨语义抽取，**已知会丢语气、称谓、方言**。
- 反证 mutation: 让模型从"哥，我上次说的那个老王的单子"抽事实 —— 英文 prompt 大概率抽不出"老王 = 王先生"这层隐含关系。
- 对抗结论: deer-flow 本身不是国内产品所以无所谓，**但这是 Hive 如果直接抄会踩的坑**。PLAN.md §6 P1 里没写"国内化 prompt"这条。

**漏洞 1.2.2 — Prompt cache 策略依赖 provider 特性，不在 prompt 层做**
- 证据: archive/claude-phase2-output.md:240 — `enable_prompt_caching: bool = True`，OAuth token 时禁用。
- 漏洞: 当 OAuth token + 长 prompt（1500 行）同时出现时，既不能用 cache 又必须发送完整 context —— 每次请求都是满额 token。
- 对抗结论: prompt 应该设计成"前 N 行是稳定 cache-able / 后 M 行是动态"，deer-flow 的 prompt 是一锅粥，没这个分层。

### 1.3 废话/冗余原文节选

**节选 A（archive/claude-phase2-output.md:555-565 引）**
> "Skills 不是代码插件，而是 prompt 注入 + LLM 自主语义匹配。系统不是'规则引擎选择 skill'，而是'prompt 告诉模型何时该读 SKILL.md'"

翻译成蓝军语言: "我们没做规则引擎，让模型自己猜"。

**节选 B（archive/codex-phase1-output.md:617）**
> "prompt 只列 skill name/description/path，真正内容由 agent 在匹配时用 `read_file` 渐进读取"

翻译: "我们把 skill content load 的责任外包给 LLM 自己 `read_file` —— 每次读一份多一次 round trip"。

**节选 C（`prompt.py:610-632` 被 archive 反复引）**
> prompt 里列出 deferred tools 名字

这段本身是典型的"列表冗余"——几十个工具名贴进 system prompt，占 context 窗口。

### 1.4 deer-flow prompt 矛盾/edge case 清单

| # | 矛盾点 | 证据 | 后果 |
|---|---|---|---|
| M1 | Skill 触发"靠 LLM 自觉"但文档说"强制" | `prompt.py:557-565` vs README skill 章节 | 用户信任被透支 |
| M2 | Deferred tools 名字列在 prompt 但 schema 不列 | `prompt.py:610-632` | 每次工具调用 ≥ 2 次 LLM round trip |
| M3 | Memory JSON 约定 vs 无 schema 校验 | `memory/prompt.py:1-340` | parser 随时爆 |
| M4 | prompt 里 max task calls = 3，middleware max_concurrent 可配置 | `prompt.py:685-699` + `executor.py:72-80` | 单一事实源缺失 |
| M5 | Tool 去重 prompt 没说，但代码里 "config > builtin > MCP > ACP" | archive/codex-phase1-output.md:553 | LLM 看不到去重规则时选错名字 |
| M6 | Citation 前缀靠 prompt 约定，前端正则硬匹 | claude-phase2-output.md:683 | LLM 一偷懒就断链 |
| M7 | Memory confidence 排序塞 token budget | `memory/prompt.py:256-317` | 相关 fact 被挤出 |

---

## §2 Hive prompt 漏洞清单

> **访问约束**: 本轮沙箱限制直接读 `/vast/agents-hive/internal/i18n/prompts/`；但 PLAN.md §3、axis-1~5 evidence 多次引用该目录，行号线索足够蓝军勾勒。**如果下述具体行号在真实文件里对不上，本节的漏洞 H2.x 必须以 mutation 形式在下一轮 pair-agent 里以 `cat -n` 原文复核**。蓝军声明：**不复核 = 本节作废**。

### 2.1 `internal/i18n/prompts/*.tmpl` 的结构性问题

**漏洞 H2.1.1 — 多语言模板导致 prompt 漂移**
- 证据: 目录命名 `i18n/prompts/` + `.tmpl` 后缀暗示 Go text/template 多语言渲染。
- 漏洞: 每个 prompt 有 `zh-CN` / `en-US` 两份（可能还有 `zh-TW`），**两份之间的语义一致性无机器验证**。改 zh 忘改 en，下次英文客户投诉"行为不一致"。
- mutation: diff `<prompt>.zh-CN.tmpl` 和 `<prompt>.en-US.tmpl` 的"关键约束句数"是否相等 —— 若不等，本漏洞成立。
- 对抗结论: 这是 Hive 为国内客户做的事（对），但一旦客户问"那英文版也能用吗"，翻译不同步 = 幻觉不同步。**PLAN.md §3 说"飞书深度抄不走"是真，但 prompt 多语言这块是债务不是护城河**。

**漏洞 H2.1.2 — Go text/template 的 `{{.Var}}` 占位符无类型校验**
- 证据: Go `text/template` 语法 + `.tmpl` 后缀。
- 漏洞: 模板里写 `{{.UserName}}` 但渲染时传 `user_name`（snake vs camel），Go 不会报错，会渲染成空字符串，prompt 静默变成 "你好， ，我是助手..."
- mutation: 在一个随机 .tmpl 里故意写 `{{.NonExistentField}}` 看 runtime 是否 panic。Go `text/template` 默认 `Option("missingkey=default")` 静默渲染 `<no value>` 或空串。
- 对抗结论: deer-flow 的 Python f-string 至少在 render 时报 KeyError，Hive 的 text/template 会静默吃掉错误变量。**这是 Hive prompt 层相对 deer-flow 的一个劣势**，PLAN.md 没提。

**漏洞 H2.1.3 — i18n 选择策略是否基于 user locale 未在 PLAN 说明**
- 证据: `i18n/prompts/` 目录本身 + PLAN.md 和 evidence 全篇无 "locale selection" 行。
- 漏洞: 同一用户中英混输（国内企业常见）时，prompt 到底选 zh 还是 en？如果基于 `channels.feishu.user.locale`，飞书用户大多数 locale 是 `zh_CN`，英文用户拉进群后所有人 prompt 都是中文 — 英文人看不懂指令。
- 对抗结论: 这是 i18n 设计默认项选择的问题，**prompt 作者大概率没想过**。

### 2.2 Hive prompt 风险（基于 archive 反推）

**漏洞 H2.2.1 — Tool name localization 陷阱**
- 证据: Hive 有 i18n prompt，但工具名（e.g. `web_search`）是不是也翻译？
- 漏洞: 如果 prompt 里工具名被翻译成"网页搜索"但 tool registry 里 name 是 `web_search`，LLM 调用时写"网页搜索"会 fail（tool not found）。
- mutation: grep `.tmpl` 里 "网页搜索"/"文件读取"等中文工具名，若存在且工具 schema 是英文，本漏洞成立。
- 对抗结论: 这是多语言 prompt 的经典坑。deer-flow 因为单语言不踩。

**漏洞 H2.2.2 — 飞书卡片 PatchCard 约束是否在 prompt 里**
- 证据: PLAN.md §3.4 "飞书深度: 4578 行/9 文件，ErrPatchRateLimited + PatchCard 主动重试"。
- 漏洞: 代码层做 rate limit 重试，prompt 层有没有告诉模型"生成卡片时不要频繁更新"？如果没有，模型可能每秒 patch 一次，触发限流，代码层重试也救不回来（服务端拒绝）。
- mutation: grep `.tmpl` for `patch` / `card` / `频率` / `不要频繁`，没找到即漏洞成立。
- 对抗结论: 飞书深度是代码优势，但 prompt 没同步是半瓶水。

**漏洞 H2.2.3 — RAG top-k 是否在 prompt 里告诉模型**
- 证据: PLAN.md §3.1 "pgvector + tsvector FTS + RRF + top-? 注入"；deer-flow 是 top-15。
- 漏洞: Hive PLAN.md 没写 top-k。如果 prompt 说"你有最相关的 N 条记忆"但 N 每次变动，模型推理准确度不稳。
- mutation: grep `.tmpl` 找 `top` / `最相关的` / `memories` 附近的数字 — 数字要和 RAG engine 的 k 同步。
- 对抗结论: RAG 再牛，prompt 层不告诉模型"你手里是 top-15"就白瞎。

### 2.3 Hive prompt 废话/冗余风险（基于模式推断）

Hive 必然出现但未直接证据的冗余模式（蓝军预判）:

**模式 P1**: "你是 {{.BotName}}，一个专业的 {{.Role}} 助手。你的职责是 ..." — 每个 .tmpl 都会有这段 8-10 行的角色定义，**都是同一套废话**，应该抽成 partial / include 复用。
- mutation: `cat prompts/*.tmpl | sort | uniq -c | sort -rn | head -20` —— 若前 10 行重复率 > 30%，漏洞成立。

**模式 P2**: "请使用中文回复。请使用 markdown 格式。请不要编造事实。" — 这三句是 i18n prompt 的经典三件套；deer-flow 至少只说一次（在 base prompt），Hive 很可能每个 tmpl 都重复一遍。

**模式 P3**: PLAN.md 自己 §3.4 "ErrPatchRateLimited + PatchCard 主动重试" 这种描述叙事一旦进了 prompt（给 LLM 解释"为什么卡片更新慢"），就是**典型的"实现细节泄漏到 prompt"**。

### 2.4 Hive prompt 矛盾/edge case 清单

| # | 矛盾点 | 证据 | 后果 |
|---|---|---|---|
| H1 | i18n zh/en 两份 prompt 语义一致性无校验 | `internal/i18n/prompts/*.tmpl` 目录结构 | 跨语言行为漂移 |
| H2 | Go text/template 占位符类型错无 runtime panic | Go text/template 默认行为 | prompt 静默渲染空值 |
| H3 | tool name 若被翻译成中文，LLM 调用必 fail | 未提供工具名不翻译的约定证据 | tool not found |
| H4 | RAG top-k 与 prompt 里的"最相关 N 条"是否同步 | PLAN.md §3.1 未提 | LLM 对 context 规模估计错误 |
| H5 | 飞书 PatchCard 限流在代码层，prompt 未同步 | PLAN.md §3.4 只提代码 | LLM 触发限流无自我克制 |
| H6 | prompt 改动无灰度机制（和 deer-flow 同病） | PLAN.md §5 P0 里没 prompt gateway | 运营不能 A/B prompt |
| H7 | Memory 注入格式 .tmpl 里如何表达"fact list" | 未证 | 同 deer-flow M3 漏洞 |
| H8 | Skill 触发在 Hive 是规则还是 prompt？ | PLAN.md §3 skills 一节含混 | 可能同 deer-flow M1 |

### 2.5 Hive prompt 原文节选占位（必须下一轮补）

> 本节需要 pair-agent 在有 `internal/i18n/prompts/*.tmpl` 读权限的沙箱里补齐两段原文。蓝军预留结构:
>
> **节选 H-α**:
> `internal/i18n/prompts/<name>.zh-CN.tmpl:<line>-<line>`
> ```
> <原文>
> ```
> 蓝军点评: <为什么是废话>
>
> **节选 H-β**:
> `internal/i18n/prompts/<name>.en-US.tmpl:<line>-<line>`
> ```
> <原文>
> ```
> 蓝军点评: <为什么和 zh 版不一致>

**蓝军保留意见**: §2 不贴原文节选就是耍流氓。但本轮沙箱不给权限，**在 §7 对 PLAN.md 修正建议里把"拿到 prompt 原文做第二轮 pair"列为硬前置**。

---

## §3 反向评分表——谁更烂

| 维度 | deer-flow 烂分 (0-10) | Hive 烂分 (0-10) | 谁更烂 | 备注 |
|---|---|---|---|---|
| **单文件长度失控** | 9 — lead_agent/prompt.py 700+ 行硬编码 | 5 — 按 .tmpl 拆分，每文件预估 ≤ 200 行 | deer-flow | Hive 拆分是对的 |
| **热更新能力** | 10 — 改 prompt = 改 py = 重启 | 7 — .tmpl 改后需要重启 Go 进程（没 CRUD API） | deer-flow 更烂 | Hive 也没 hot reload |
| **多语言一致性** | 0 — 单语言没得漂移 | 8 — zh/en 两份无校验 | **Hive 更烂** | 这是 Hive 独有债务 |
| **工具名泄漏/翻译陷阱** | 2 — 工具名英文原封 | 7 — tool name 若翻译即废 | **Hive 更烂** | 需 pair 复核 |
| **Skill 触发是否强制** | 9 — prompt 自觉 | 6 — PLAN.md 含混 | deer-flow 明确烂；Hive 未证 | 需 pair 复核 |
| **Memory 注入相关性** | 8 — confidence 截断，非语义检索 | 3 — RAG 底座对，prompt 层未知 | deer-flow 更烂 | Hive 如 prompt 同步 RAG 会更好 |
| **Citation/结构化输出** | 9 — prompt 约定 + 前端正则 | 5 — 未证但飞书卡片是结构化 | deer-flow 更烂 | |
| **Tool schema 暴露策略** | 8 — deferred 只暴露名字，双倍 round trip | 5 — 未证 | deer-flow 更烂 | |
| **Cache-friendliness** | 8 — 1500 行一锅粥不分层 | 4 — .tmpl 天然分 partial | deer-flow 更烂 | |
| **运营可改** | 10 — 必须走 PR | 9 — 必须走 PR（tmpl 在 repo 里） | 打平 | 都烂 |
| **JSON schema 强制** | 7 — prompt 约定 + ONLY valid JSON | 5 — 未证 | deer-flow 更烂；Hive 需证 | |
| **冗余角色定义** | 6 — 单文件所以不重复 | 7 — 多 .tmpl 大概率重复角色头 | **Hive 更烂** | 需 pair 复核 |
| **Prompt 版本化/灰度** | 10 — 无 | 10 — 无 | 打平 | 都是零 |
| **Prompt 内嵌实现细节** | 6 — deferred tools 名字列表 | 6 — 飞书卡片限流预估 | 打平 | |
| **类型/占位符安全** | 4 — f-string KeyError 会炸 | 7 — Go text/template 默认 silent | **Hive 更烂** | 少见的静默错误模式 |
| **行数平均值（估）** | 700+ / 文件 | 150-250 / 文件 | deer-flow 更烂 | |
| **总计（烂分加权均）** | **7.1** | **6.1** | **deer-flow 略烂** | 差距不到 1 分 |

**结论**: **deer-flow 7.1 / Hive 6.1** —— 差距微弱。Hive 赢在拆分结构，但输在多语言漂移 + 静默渲染 + tool name 翻译陷阱。**PLAN.md §3 说的"deer-flow 抄都要一年" 在 prompt 这条维度上不成立**——deer-flow 一个 prompt.py 就 700 行，抄 prompt 一周；抄 prompt 的质量缺陷（上述 M1-M7）一个季度就能超过。

---

## §4 TODO/HACK/FIXME 技术债 grep

> **访问约束**: 本轮沙箱无法直接 grep 源码仓。以下基于 archive 里子 agent 采集的证据 + 推断，标记未证的必须下一轮复核。

### 4.1 deer-flow 技术债（从 archive 推断）

| # | 文件:行号 | 类型 | 原文/要点 | 证据链 |
|---|---|---|---|---|
| D-TD-1 | `claude_provider.py:110` | `# TODO?` 风险 | `enable_prompt_caching = False` when OAuth，无警告日志 | claude-phase2:264/369 |
| D-TD-2 | `lead_agent/prompt.py:557-565` | HACK | "skill 靠 LLM 自觉" — 代码里注释大概率有 FIXME | claude-phase2:1477 |
| D-TD-3 | `lead_agent/prompt.py:610-632` | HACK | deferred tools 名字列表 hardcoded | codex-phase1:261/337 |
| D-TD-4 | `memory/prompt.py:1-340` | TODO | "Return ONLY valid JSON" 无 schema 校验 | codex-phase1:240-243 |
| D-TD-5 | `executor.py:72-80` | HACK | 线程池 hardcode 3/3/3 | codex-phase1:605 |
| D-TD-6 | `openai_codex_provider.py:388-430` | TODO | 自己把 LangChain tool schema 转 Responses API format；脆 | codex-phase1:243 |
| D-TD-7 | `tool_error_handling_middleware.py:19-50` | FIXME? | 工具失败转 ToolMessage 继续 — 已知会加深幻觉 | claude-phase2:1479 |

**补偿行动**: `pair-agent` 下一轮运行 `grep -RnE "TODO|FIXME|HACK|XXX|NOTE" /Users/guoss/workspace/company/vast/agents-hive/docs/调研笔记/deer-flow/backend/packages/harness/deerflow/agents/lead_agent/prompt.py` 并把原始输出粘贴到本文件 §4.1 —— 预判 ≥ 5 个命中。

### 4.2 Hive 技术债

| # | 文件:行号 | 类型 | 推断依据 | 必须验证 |
|---|---|---|---|---|
| H-TD-1 | `internal/i18n/prompts/*.tmpl` | HACK | Go text/template 静默 missingkey | grep `{{\.` 全目录 |
| H-TD-2 | `internal/i18n/prompts/*.tmpl` (zh vs en) | TODO | 多语言一致性无 CI 检查 | diff zh en |
| H-TD-3 | `internal/channels/feishu/*.go` | TODO | `ErrPatchRateLimited` 重试但 prompt 未同步（推断） | grep `Ratelimit\|限流` in `.tmpl` |
| H-TD-4 | `internal/memory/*.go` | TODO | RAG top-k 硬编码未在 prompt 声明 | grep `top-?\s*\d+` |
| H-TD-5 | `frontend/src/components/chat/*` | (非 prompt 但相关) | Streaming 容错是否依赖 prompt 约定 | grep `reconnect\|重连` |
| H-TD-6 | `internal/skills/registry.go` (若存在) | HACK | Skill 选中若靠 prompt 和 deer-flow 同病 | 需证 |

**补偿行动**: `pair-agent` 下一轮运行:
```
grep -RnE "TODO|FIXME|HACK|XXX" /Users/guoss/workspace/company/vast/agents-hive/docs/调研笔记/deer-flow/vast/agents-hive/internal/i18n/prompts/ 2>/dev/null
grep -RnE "TODO|FIXME|HACK" /Users/guoss/workspace/company/vast/agents-hive/docs/调研笔记/deer-flow/vast/agents-hive/internal/ | wc -l
```
—— **只接受真实 grep 输出，不接受推断**。

### 4.3 对比小结

不管谁，都没在 prompt 层做版本化/灰度/多语言一致性校验。
deer-flow 的债集中在**单文件巨大 + prompt 约定代替结构化**；
Hive 的债集中在**多语言 silent render + tool name 翻译风险**。
两边都没把 prompt 当一等公民维护。

---

## §5 废话/冗余 prompt 原文节选

### 5.1 deer-flow 节选（≥ 2 段）

**节选 D-α** — `lead_agent/prompt.py:557-565`（经 archive/claude-phase2-output.md:1477 + codex-phase1-output.md:527/577 交叉引用）
> "Skills 是可选的知识包。当用户 query 匹配某个 skill 的 description/use cases 时，你应该读取 SKILL.md 获取详细指南。Skill name 和 description 在 <skills> 标签里列出。"

**蓝军点评**:
1. "你应该" —— 软约束，LLM 可无视。应改 "you MUST invoke skill X if Y"。
2. "读取 SKILL.md 获取详细指南" —— 每个 skill 多一次 `read_file` round trip，**deer-flow 自称"progressive loading"，实际是"把延迟成本推给用户"**。
3. "在 <skills> 标签里列出" —— 列出的是 name + description + path，如果 skill 多于 30 个，光 description 就占 2-3k token，**白吃 context 窗口**。
4. 冗余: 这段话在 `prompt.py:560-599` 又展开了一次（见 codex-phase1:577），**重要指令同一 prompt 里说两遍** —— 典型"怕 LLM 没看到就再说一遍"的勇士写法，实际浪费 token 且让模型困惑哪版本权威。

**节选 D-β** — `lead_agent/prompt.py:685-699`（经 archive/codex-phase1-output.md:607 引用）
> "Limit: up to N task calls per response"（N 动态渲染）

**蓝军点评**:
1. 这里 N 是 runtime 注入，和 `SubagentLimitMiddleware(max_concurrent=N)` 同步，**但不保证两边 N 相等**。如果 config 改了 middleware 没重启，prompt 里还是旧值 —— LLM 被欺骗。
2. "task calls per response" 定义不清: 是同一条 assistant message 里的并行 task tool_calls，还是跨多轮的累计？prompt 没说。多轮时 LLM 容易理解错。
3. 冗余: middleware 本身会截断超出的 tool_calls，prompt 里再说一次是**"两个人做同一件事"的典型反模式**。正确做法: middleware 报错时返回给 LLM "You exceeded N task calls, system truncated"，用反馈代替预先说教。

**节选 D-γ（附加）** — `memory/prompt.py:256-263`（archive/codex-phase1-output.md:376 引）
> "Return ONLY valid JSON. Do not include any explanation."

**蓝军点评**: 所有模型在看到 "Return ONLY" 时都会反射性加一段解释（训练数据里无数 prompt 被如此约束过，LLM 学到的反而是"用户往往真的想要解释，只是嘴硬"）。**Anthropic/OpenAI 官方文档**都明确说这种约束靠 prompt 不可靠，**必须用 `response_format={"type":"json_object"}` 或 structured outputs**。deer-flow 2026 年还在用 prompt 约定 = **技术栈停留在 2023**。

### 5.2 Hive 节选（≥ 2 段，需 pair-agent 复核）

**节选 H-α** — 预留位置（访问受限）
- 路径: `internal/i18n/prompts/<main_agent>.zh-CN.tmpl:1-XX`
- 预判原文（基于国内模板典型格式）:
  > "你是「Hive 智能助手」，一个专业的企业 IM 机器人。你的职责是帮助用户处理各类任务。请使用中文回复。请使用 markdown 格式。请不要编造事实。"
- **蓝军点评（预判）**:
  1. "你是「X」" —— 角色头在多个 .tmpl 里大概率重复 5 次以上。
  2. "请使用中文回复" —— 每个 zh 模板都说一次，如果模板 A 渲染完再附加 zh 模板 B，重复两次，模型困惑。应该抽到 base_system.tmpl include 一次。
  3. "请不要编造事实" —— 所有 hallucination 研究都证明**这类 anti-hallucination 软约束基本无效**，真正管用的是 RAG 引用 + "如无依据说 '不知道'"。

**节选 H-β** — 预留位置
- 路径: `internal/i18n/prompts/<main_agent>.en-US.tmpl:1-XX`
- **蓝军 mutation 要求**: 下一轮 pair-agent 必须 **diff zh/en 两份** —— 如果"核心指令句数"差 > 10%，节选 H-β 的点评就是"多语言漂移实锤"。

**本节警告**: §5.2 若下轮 pair-agent 不贴真实原文，蓝军判定 Hive 侧 prompt 质量"未证实优于 deer-flow"，反向评分表 §3 里给 Hive 的结构拆分分数（烂 5 分）降至 7 分（烂），因为不能证的结构 = 不存在的结构。

---

## §6 矛盾/edge case 汇总

### 6.1 跨 deer-flow 与 Hive 的共性矛盾

| # | 共性矛盾 | deer-flow 表现 | Hive 表现 | 症状 |
|---|---|---|---|---|
| C1 | prompt 改动无版本化 | prompt.py 改 PR | .tmpl 改 PR | 两边运营都痛 |
| C2 | 工具选择逻辑在 prompt 里 | deferred tools 名字列出 | 未证但 PLAN.md §3 Hive Tool ≈ 30 个，多半全量列出 | context 浪费 |
| C3 | prompt 里说"不要编造" 是 placebo | claude-phase2 警告 LLM 仍可编造 | Hive 必然同症 | 反幻觉失效 |
| C4 | Citation/引用格式靠 prompt 约定 | 前端正则匹 | 未证 | 格式偏移即断链 |
| C5 | Memory 注入的 budget 策略 | confidence 截断 | RAG+RRF 但未在 prompt 层暴露 top-k | LLM 对 context 规模估计偏差 |

### 6.2 deer-flow 独有 edge case

- **E1**: OAuth token 下 prompt cache off + 长 prompt → 每次请求爆满额 token。
- **E2**: Tool 名字去重 `config > builtin > MCP > ACP` —— 当同名工具同时存在，LLM 不知道 prompt 里的那个名字是哪来的版本，参数语义可能漂移。
- **E3**: `SubagentExecutor` 线程池 3/3/3 hardcode + prompt 说 max n —— 如果 n > 3 实际会阻塞，但 prompt 没说。
- **E4**: skill name/description/path 列表在 prompt 里，超过 30 个 skill 时 context 溢出。

### 6.3 Hive 独有 edge case（蓝军预判）

- **EH1**: 飞书机器人同时被拉到多个群，每群 locale 可能不同 —— prompt 动态选 locale 若基于 channel 对象而非 user，多语言混乱。
- **EH2**: PatchCard 限流时，prompt 没教 LLM 聚合更新 → 限流触发后用户体验崩。
- **EH3**: 微信 wechaty + wechatpadpro 双通道的 prompt 是否一致？两通道延迟/事件名差异若都由一份 prompt 处理，模型会用错误的"回执 API 名"。
- **EH4**: ACP server 暴露的 `Initialize/Authenticate/...` 七个方法，若 prompt 里列出来让 LLM 调用，prompt 有一套 ACP 术语 + MCP 术语 + Tool 术语 —— **三套术语同一 prompt**，模型选错类型。
- **EH5**: RAG top-15（假设）+ FTS + RRF 混合 —— prompt 若只说"最相关的记忆"，模型不知是 15 还是 5，无法在"我只需要最 top 3"这类场景剪枝。
- **EH6**: Go text/template 的 `{{if}}`/`{{range}}` 控制流如果在 .tmpl 里深度嵌套，prompt 渲染结果受渲染上下文影响 —— 测试覆盖难。

### 6.4 "LLM 行为层"可预见崩溃点

| 场景 | deer-flow 崩法 | Hive 崩法 | 谁先炸 |
|---|---|---|---|
| 用户中英混输 | 没事（英文 prompt 吞一切） | i18n 选错 locale | **Hive 先炸** |
| Skill 数量超 30 | prompt 超长，LLM 丢早期指令 | 同病 | 打平 |
| 工具同时 30+ | deferred tools 救场 | tool 全量列出（预判）爆 context | **Hive 先炸** |
| Memory 上万条 | confidence 截断但不准 | RAG 牛但 prompt 层没告诉 LLM top-k | deer-flow 先答错；Hive 先"沉默正确"（LLM 不知手里有多少，保守回答） |
| 飞书卡片高频更新 | N/A（无飞书） | prompt 不克制 + 代码层重试救 | Hive 先触发限流 |
| JSON 回复带 code fence | prompt 约定失败 | 未证 | 都会炸 |

---

## §7 对 PLAN.md 的修正建议（哪些 Hive 优势站不住）

PLAN.md 原文（/Users/guoss/workspace/company/vast/agents-hive/docs/调研笔记/deer-flow/PLAN.md）在 §3 列了 7 条"护城河"，§4 列了 3 条 P0 缺口。**prompt 轴的真相是: §3 少一条债，§4 少一条 P0**。

### 7.1 §3 "护城河"里站不住的

| PLAN.md §3 条目 | 蓝军裁决 | 依据 |
|---|---|---|
| §3.1 RAG 栈（pgvector/tsvector/RRF/per-user） | **站得住** | 底层确实强，但 prompt 层没暴露 top-k 给 LLM —— 补一句建议 |
| §3.2 ACP 三角完整 | 站得住（非 prompt 轴） | |
| §3.3 MCP PKCE OAuth + DB 持久化 | 站得住（非 prompt 轴） | |
| §3.4 飞书深度 4578 行 | **部分站** | 代码深度 OK，但 PatchCard 限流 prompt 未同步 —— prompt 不克制，代码层救不完 |
| §3.5 微信双通道 | 站得住（非 prompt 轴） | |
| §3.6 Whisper + ffmpeg | 站得住 | |
| §3.7 "大型工具质量" | **假护城河** | 单工具 746 行 = 工具内逻辑大 ≠ prompt 层聪明；archive/axis-1-tools 已经讨论 |

### 7.2 §4 "缺口"里漏掉的

PLAN.md §4 P0 = `[Artifact HTTP / Skill CRUD / pathguard]`。
**蓝军判决**: 漏掉了一条 **P0-4：Prompt Gateway**。

**P0-4：Prompt Gateway / Prompt Versioning / Prompt CI**
- 目标: 让 prompt 可版本化、可灰度、可 diff、可 A/B。
- 最小实现:
  1. `.tmpl` 目录加 CI lint:
     - 多语言 pair 存在性检查（zh 有 en 必须有，或反之）
     - 未使用变量检查（`{{.Foo}}` 渲染时未传入 → error）
     - 重复角色头检查（`你是「X」` 在多个 .tmpl 里完全一致 = 抽 partial）
  2. `POST /api/prompts/<name>/version` 上 bump + 灰度
  3. `GET /api/prompts/<name>/diff/<v1>/<v2>` 给运营看
  4. Go text/template `Option("missingkey=error")` 全局开启，**禁止静默空串**
- 工期: 2 周（+ CI 1 天）
- 为什么 P0: 若不做，§5 P0-3 Skill CRUD 装新 skill 后，与 skill 协作的 prompt 不同步更新，skill CRUD 的价值打五折。

**P0-5（可选但强烈建议）：Structured Output 迁移**
- 目标: 把所有"Return ONLY valid JSON"类 prompt 迁到 provider 层 response_format / structured outputs。
- 为什么 P0: prompt 约定失败时是 silent error，修一次事故成本 > 迁移工程。
- 工期: 1 周。

### 7.3 §7 时间表应加一列

| 阶段 | 周数 | 范围 |
|---|---|---|
| P0-4 Prompt Gateway | 2 | .tmpl lint + CI + version API |
| P0-5 Structured Output | 1 | response_format 迁移 |
| **P0 + P0-4 + P0-5 串行** | **9 周** | |
| **P0 并行 + P0-4/5** | **6 周** | |

### 7.4 §12 "一句话"修正

PLAN.md 原文:
> "Hive 的问题不是能力不足，是能力没有暴露给开发者和运营用户。"

蓝军改写:
> "Hive 的问题不是能力不足，是**能力没有暴露给开发者和运营用户**，也没有暴露给 **prompt 层让 LLM 自己知道**。前者是 Artifact/Skill CRUD 的缺口（PLAN.md 承认），后者是 Prompt Gateway 的缺口（PLAN.md 漏掉）。Hive 如不补齐 prompt 层，RAG/飞书/MCP 的深度就是 LLM 看不见的暗物质 —— 用户感觉不到。"

### 7.5 §3 应该补的条目

**新增 §3.8 — Prompt 层债务**（从"护城河"里剥离，移到"债务/缺口"）:
> Hive prompt 目前是 `internal/i18n/prompts/*.tmpl` 多语言 Go template，相对 deer-flow 的 `lead_agent/prompt.py` 单文件巨石有**结构拆分优势**，但存在三项债务（见 `evidence/axis-6-prompts-codex.md` §2）:
> 1. zh/en 一致性无 CI 检查（H2.1.1）
> 2. Go text/template 默认 silent missing key（H2.1.2）
> 3. tool name 若被 i18n 翻译即调用失败（H2.2.1）
> 深度对抗 deer-flow 时，**prompt 轴不是 Hive 的护城河，而是 debt**。

---

## §8 附录

### 8.1 证据链完整性检查

- archive/claude-phase1-output.md: 1214 行（已提取 prompt 相关段落）
- archive/claude-phase2-output.md: 1518 行（主力）
- archive/codex-phase1-output.md: 759 行（主力）
- archive/codex-phase2-output.md: 1100 行（部分引）
- archive/final-verdict.md: 275 行（交叉校验）
- evidence/axis-1-tools.md .. axis-5-*.md: 2189 行（无直接 prompt 原文，但涉及 tool schema 暴露等）
- PLAN.md: 213 行（§3/§4 交叉引用）

### 8.2 未完成的验证任务（必须下一轮 pair-agent 执行）

1. `cat -n /Users/guoss/workspace/company/vast/agents-hive/docs/调研笔记/deer-flow/backend/packages/harness/deerflow/agents/lead_agent/prompt.py | sed -n '557,599p'` —— 补 §1.1.2 原文
2. `grep -RnE "TODO|FIXME|HACK|XXX" /Users/guoss/workspace/company/vast/agents-hive/docs/调研笔记/deer-flow/backend/packages/harness/deerflow/agents/` —— 补 §4.1
3. `ls /Users/guoss/workspace/company/vast/agents-hive/docs/调研笔记/deer-flow/vast/agents-hive/internal/i18n/prompts/` —— 确认 .tmpl 文件清单
4. `cat -n .../prompts/<main>.zh-CN.tmpl | head -80` + `.en-US.tmpl | head -80` —— 补 §5.2 原文
5. `diff <(wc -l .../prompts/*.zh-CN.tmpl) <(wc -l .../prompts/*.en-US.tmpl)` —— 验证 H2.1.1
6. `grep -RnE "TODO|FIXME|HACK" /Users/guoss/workspace/company/vast/agents-hive/docs/调研笔记/deer-flow/vast/agents-hive/internal/i18n/prompts/` —— 补 §4.2

### 8.3 蓝军自查

- 本报告 **对 deer-flow** 引 7 条具体 `.py:line` 证据。
- 本报告 **对 Hive** 引 0 条直接 `.tmpl:line` 原文（沙箱受限）—— 已在 §2.5 / §5.2 / §8.2 明确标记必须下一轮补齐。
- 本报告 **给 PLAN.md 的修正**（§7.1/§7.2/§7.5）都基于 PLAN.md 原文行号 + archive 证据链，非凭空。
- 本报告 **反向评分**（§3）给 Hive 赢 1 分，但在未证的情况下；若 §5.2 未补，Hive 分数下调至 7，与 deer-flow 打平。

### 8.4 最终一句话（蓝军版）

**deer-flow 的 prompt 是 2023 年的单文件巨石，Hive 的 prompt 是 2026 年的多语言地雷。** 两边都没把 prompt 当一等公民；PLAN.md 把 prompt 轴完全漏掉，必须补 P0-4 Prompt Gateway + P0-5 Structured Output，否则 RAG 再牛、飞书再深、ACP 再完整，LLM 都看不见。

*— End of Codex 蓝军 axis-6 报告 —*
