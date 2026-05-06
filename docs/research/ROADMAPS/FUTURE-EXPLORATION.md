# Future Exploration Roadmap

> **定位**：未来能力版图中的研究线
>
> **优先级**：P5
>
> **目标**：保留远期方向，但不让研究项挤占当前质量闭环
>
> **边界**：本文是 `FINAL-PLAN.md` 的能力地图，不是独立实施计划；代码施工以 `IMPLEMENTATION/` 为准。

## 1. 覆盖范围

- GEPA / prompt 自动优化。
- 自我改进。
- 轨迹反思。
- 自动 skill 生成。
- 自动工具生成。
- 长周期策略学习。
- spec-driven 主执行路径替换或增强 ReAct。

## 2. 当前口径

这些方向不能作为“自动改生产系统”的默认排期，但其中的“建议生成”和“失败样例沉淀”必须进入质量闭环。

原因：

- Hive 当前已有大量能力，短期瓶颈是质量闭环，不是研究概念不足。
- 自我改进、自动 skill、GEPA 如果没有 eval 和门禁，会放大错误。
- spec-driven 已有代码和 eval harness，但 ReAct 主执行路径尚未由它稳定驱动，不能作为近期主线替代。

拆分原则：

- **近期允许进入 P0**：失败轨迹生成 golden task 候选、prompt diff 建议、tool description 改写建议、skill 草稿。
- **近期禁止进入生产默认路径**：自动发布 prompt、自动安装 skill、自动放宽危险操作边界、自动替换执行路径。

## 3. 允许的小实验

小实验必须满足：

- 不改生产默认路径。
- 不影响 P0/P1 施工。
- 有明确假设。
- 有离线数据集。
- 有收益指标。
- 失败结果也进入 `EVIDENCE/`。

可做的实验：

- 用失败轨迹生成 golden task 候选。
- 用 eval 结果建议 prompt diff，但不自动上线。
- 从成功任务中提取 skill 草稿，但必须人工 review。
- 对 spec-driven runner 做离线 case 扩充。
- 对 memory distill 做离线比较。
- 从 replay/journal 生成最小可复现 case，包括输入、期望工具、失败分类、risk、expected_status。
- 从 tool-choice failure 生成 tool description 改写建议，并附带预期 eval diff。

近期纳入 P0 的最小产物：

- `regression_candidate`：从失败 session 写入 DB 的候选记录。
- `prompt_diff_suggestion`：只读建议，不写入 PromptManager。
- `skill_draft`：只读草稿，不安装、不发布。
- `tool_description_suggestion`：只读建议，不改 schema。

## 4. 禁止事项

- 不允许自动修改生产 prompt。
- 不允许自动安装或发布 skill。
- 不允许自动放宽危险操作边界。
- 不允许让研究 runner 替代 ReAct 默认执行。
- 不允许没有指标的“自治优化”进入主线。
- 不允许自动改变 tool profile/filter 可见性。
- 不允许自动把 DB regression candidate 标记为 required case。

## 5. 进入正式排期的条件

研究项升级前必须满足：

- `AGENT-QUALITY.md` 已有稳定 eval harness。
- `FOUNDATIONS.md` 已有质量事件、危险操作边界、回滚和审计。
- 离线实验显示明确收益。
- 有灰度和回滚路径。
- 有明确负责人和验收指标。

## 6. 研究到工程的转化规则

1. 研究结论先进入 `EVIDENCE/`。
2. 通过离线 eval 后，进入对应专题路线图。
3. 通过灰度和线上指标后，才进入 `FINAL-PLAN.md` 的执行优先级。
4. 没有评测收益的研究不转工程。

例外：失败轨迹生成 DB regression candidate、prompt diff 建议、skill 草稿属于 P0 质量闭环的辅助产物，不需要等研究线升级；但它们必须保持候选/草稿状态，不能自动修改生产配置。
