# Hive Research Hub

> 本目录已经重构为“单一入口 + 专题路线图 + 证据 + 归档”结构。
> 
> **执行入口只有一个**：[`FINAL-PLAN.md`](./FINAL-PLAN.md)
>
> **当前最终方案依据**：[`EVIDENCE/CODE-AUDIT-2026-04-28.md`](./EVIDENCE/CODE-AUDIT-2026-04-28.md)
>
> **代码级施工入口**：[`IMPLEMENTATION/`](./IMPLEMENTATION/)

## 如何使用

### 1. 要决定现在做什么

先读：
- `FINAL-PLAN.md`

这是唯一权威方案，负责回答：
- Hive 当前的总目标是什么
- 未来 3-6 个月的优先级是什么
- 哪些能力立即做，哪些延后做，哪些只保留在版图里
- 每条专题路线图的角色是什么

### 2. 要展开看某条路线

再读：
- `ROADMAPS/AGENT-QUALITY.md`
- `ROADMAPS/FOUNDATIONS.md`
- `ROADMAPS/CAPABILITY-SURFACE.md`
- `ROADMAPS/MEMORY-AND-CONTEXT.md`
- `ROADMAPS/MULTI-AGENT-AND-ACP.md`
- `ROADMAPS/FUTURE-EXPLORATION.md`

这些文档是从属路线图：
- 可以展开某条线
- 不可以单独定义全局优先级
- 不可以替代 `FINAL-PLAN.md`
- 不可以直接当代码施工清单使用
- 只作为最终方案的能力地图和上下文，不再表达另一套分期实施计划

### 3. 要真正写代码

进入：
- `IMPLEMENTATION/README.md`
- `IMPLEMENTATION/P0-AGENT-QUALITY-CODE.md`
- `IMPLEMENTATION/P1-FOUNDATIONS-CODE.md`
- `IMPLEMENTATION/P2-MEMORY-CONTEXT-CODE.md`

这些文档负责回答：
- 新增/修改哪些文件
- 结构体、接口、函数签名怎么设计
- 代码接入点在哪里
- 测试文件怎么写
- 跑哪些命令验收

当前代码级计划以 `IMPLEMENTATION/README.md` 和 `IMPLEMENTATION/PHASING.md` 为入口。P3/P4/P5 只执行已收口的质量闭环 vertical slice；完整产品化、生态化、研究扩张仍不进入当前施工。

候选用例采用 DB candidate pool：生成态、审核态、晋升态都在 `agentquality_candidates` 表中管理；只有人工晋升后的正式 required golden case 才进入 `internal/agentquality/testdata` 文件，作为版本化门禁资产。

### 4. 要看判断依据

去：
- `EVIDENCE/`

这里保留调研、review、现状盘点和对标证据。
这些文档用于支撑最终判断，不再作为施工入口。

优先级：
- 先看 `EVIDENCE/CODE-AUDIT-2026-04-28.md`。
- 再看 `EVIDENCE/HIVE-EXISTING-CAPABILITIES.md`。
- `EVIDENCE/GAP-INVENTORY.md` 是历史对标输入，部分结论已经被当前代码推翻，不能单独作为施工依据。

### 5. 要查历史方案

去：
- `ARCHIVE/`

这里保留旧计划、旧分层 spec、旧依赖排序。
**禁止把归档文档当作当前执行依据。**

## 目录结构

```text
docs/research/
├── README.md
├── FINAL-PLAN.md
├── ROADMAPS/
├── IMPLEMENTATION/
├── EVIDENCE/
└── ARCHIVE/
```

## 规则

1. 团队讨论“接下来做什么”时，只引用 `FINAL-PLAN.md`。
2. 团队讨论“某条线怎么展开”时，引用对应 `ROADMAPS/*.md`。
3. 团队讨论“为什么这样定”时，引用 `EVIDENCE/`。
4. `ARCHIVE/` 只保留历史，不再承接新的排期和决策。
5. 新计划必须先查代码现状，禁止把已有能力写成待建设能力。
6. 不再使用 `v1/v2` 方案说法；版本号只允许作为外部产品、协议、schema 或测试 fixture 的客观标识。
7. 写代码前必须进入 `IMPLEMENTATION/`，路线图不再承担施工细节。
