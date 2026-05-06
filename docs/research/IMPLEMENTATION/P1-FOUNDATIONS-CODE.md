# P1 Foundations Implementation Status

> **当前口径**: 2026-04-30 `qa_up`。
>
> P1 不再作为待施工计划维护。本文件记录当前运行底座、入口、验证和边界。

## 最终能力

P1 已给质量闭环提供运行底座:

- observability label 治理, 避免高基数字段进入通用 metric labels。
- runtime policy 和 Admin 只读查看。
- dangerous operation 边界, 普通读取类操作减少打扰, 删除/覆盖/终端危险动作受 HITL 约束。
- delegation trace, subagent/ACP 质量事件和 replay/gallery 过滤维度。
- ACP permission bridge 不绕过本地策略。

## 当前实现入口

- Runtime policy: `internal/runtimepolicy`
- Observability label sanitizer: `internal/observability/labels.go`
- Dangerous operation / HITL: `internal/master/lifecycle.go`, `internal/security`
- Delegation emit: `internal/tools/spawn_agent.go`, `internal/master/quality_events.go`
- ACP server/client bridge: `internal/acpserver`, `internal/acpclient`
- Admin API: `/api/v1/admin/runtime/policy`
- Replay/gallery 前端: `frontend/src/pages/ReplayGallery.tsx`, `frontend/src/components/replay/EventDetailPanel.tsx`

## 当前测试入口

```bash
go test ./internal/observability ./internal/runtimepolicy ./internal/master ./internal/tools ./internal/acpserver ./internal/acpclient -run 'Label|Runtime|Safety|Delegation|ACP|Permission' -count=1
go test ./internal/api -run Runtime -count=1
```

## 权威边界

- Admin runtime policy 当前是只读查看,不是在线修改运行策略的控制面。
- ACP permission bridge 负责记录和转交危险操作语义,不代表远程 agent 可绕过本地 HITL。
- terminal 能力必须区分 Web/API/IM/ACP 场景,不能把 ACP client 的只读白名单误写成任意命令执行。
- Admin endpoints 只在 `authEngine != nil` 时注册。
