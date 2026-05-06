# Marketplace 协议（index.json schema + 自建指南）

## 1. 协议概览

Hive 从 marketplace HTTP 端点按需拉取 skill 元数据与源码。Marketplace 实质是一个"静态 HTTP 服务 + 若干 tar.gz 归档"。不需要状态，不需要鉴权（可选）。

```
GET  {marketplace_base}/index.json           → 全量 skill 列表
GET  {marketplace_base}/skills/<name>.tar.gz → 对应 skill 源码包
```

## 2. `index.json` schema

```jsonc
{
  "version": "1",                       // 协议版本（当前固定为 "1"）
  "updated_at": "2026-04-20T00:00:00Z", // ISO-8601；可选
  "skills": [
    {
      "name": "nuwa",                   // 必填；对应 SKILL.md frontmatter.name
      "version": "1.2.0",               // 必填；semver
      "description": "AI 图像生成",     // 必填；作用于 skill_search 的 summary
      "tags": ["image", "ai"],          // 可选；用于过滤
      "provides_requirements": [        // 可选；与 spec-driven-cognition 对齐
        "image_generation",
        "chinese_prompt"
      ],
      "source": "skills/nuwa.tar.gz",   // 必填；相对 URL 或绝对 URL
      "checksum": "sha256:abc123…",     // 可选；follow-up 强制化，详见 §5
      "min_hive_version": "2.3.0",      // 可选；向前兼容
      "author": "hive-core-team",       // 可选
      "license": "Apache-2.0"           // 可选
    }
  ]
}
```

字段契约（Discovery 解析路径 `internal/skills/discovery.go`）：

- 未知字段 **忽略**（向前兼容）
- 缺失必填字段 → 该条目被丢弃并记一条 WARN 日志
- `source` 支持相对/绝对 URL；相对时基于 `marketplace_base`

## 3. 与 `spec-driven-cognition` 的字段对齐

`provides_requirements` 在以下两处语义一致：

| 位置 | 文件 / 字段 |
|------|-------------|
| 本地 SKILL.md frontmatter | `internal/skills/skill.go` → `Skill.ProvidesRequirements` |
| 远程 marketplace | `index.json` → `skills[].provides_requirements` |

查询方法分工：

| 方法 | 范围 | 调用方 |
|------|------|--------|
| `Registry.FindBySpecRequirements(reqs)` | **本地**已注册 skill | spec-driven-subagents 原有接口 |
| `Discovery.ResolveByRequirements(reqs)` | **远程** marketplace | 本 change 新增 |
| `SpecSkillResolver.Resolve(reqs, userID)` | 聚合：local → remote fallback | 本 change 新增 |

## 4. 自建 marketplace 指南

### 4.1 最简静态站点

```
mysite/
├── index.json
└── skills/
    ├── nuwa.tar.gz
    └── translator.tar.gz
```

任意 Nginx / S3 / GitHub Pages 都能托管。服务端不需要逻辑。

### 4.2 `nuwa.tar.gz` 内部布局

```
nuwa/
├── SKILL.md          ← frontmatter 必须与 index.json 字段一致
├── scripts/
│   └── generate.sh
└── README.md
```

建议解压后的顶层目录名与 `name` 字段保持一致，但这**不是硬性要求**。
Hive 的加载器与 Claude 官方逻辑对齐：**始终以 `SKILL.md` frontmatter 中的 `name` 作为唯一标识**，目录名仅作为物理组织形式存在（允许重命名、符号链接或增加 namespace 前缀如 `gstack-*`）。

### 4.3 SKILL.md frontmatter 模板

```markdown
---
name: nuwa
version: 1.2.0
description: AI 图像生成（适配中文 prompt）
tags: [image, ai]
provides_requirements:
  - image_generation
  - chinese_prompt
---

# 使用说明
...
```

### 4.4 签名（follow-up）

当前 checksum 字段是 **optional**。生产环境建议：

```bash
sha256sum nuwa.tar.gz  # → abc123…
# 把结果写入 index.json 的 skills[i].checksum: "sha256:abc123…"
```

Discovery 侧的校验强制化计划在单独的 follow-up change 推进，见 `docs/架构设计/skills/Skill-安装安全模型.md` §3。

## 5. 版本 pinning

客户端侧通过 `skill_install(name="nuwa", version="1.2.0")` 传参。marketplace 的 `index.json` 只列**当前可安装的一版**；历史版本托管在独立归档（如 `skills/nuwa@1.1.0.tar.gz`），客户端按 `source` 字段直接拉。

## 6. 常见错误

| 症状 | 排查点 |
|------|--------|
| `index.json parse failed` | 字段类型；`provides_requirements` 必须是数组 |
| `skill download checksum mismatch` | 开启 checksum 后；重新生成 sha256 |
| `skill not listed by skill_search` | `name` 大小写；frontmatter 与 index.json 不一致 |
| `remote Discovery returns 0 hits` | Discovery 的 `marketplace_urls` 未配置；grep `skills_feature_flags: … on_demand=true` |
