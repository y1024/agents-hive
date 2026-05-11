package channel

import (
	"context"
	"fmt"
	"net/http"

	"github.com/chef-guo/agents-hive/internal/master"
)

// ChannelPlugin 定义 IM 通道插件接口
// 每个 IM 平台（钉钉、飞书、企业微信）实现此接口
type ChannelPlugin interface {
	// Platform 返回插件对应的平台标识
	Platform() Platform

	// Send 发送消息到 IM 平台
	Send(ctx context.Context, msg OutboundMessage) error

	// WebhookHandler 返回处理平台回调的 HTTP handler
	WebhookHandler() http.HandlerFunc

	// Verify 验证平台回调签名（可选，部分平台需要）
	Verify(r *http.Request) bool
}

// EventRenderer 是可选能力接口：实现此接口的 plugin 能把 harness 事件流
// 翻译为平台原生视图（飞书卡片 PATCH、钉钉/企微 markdown、未来 MCP/AG-UI 推流）。
//
// 契约（subscriber 端责任）：
//  1. **Session filter**：eventCh 是 EventBus 的全局订阅；renderer 必须按
//     `ev.SessionID == scope.SessionID` 过滤，未匹配的事件 continue。
//  2. **收敛退出**：ctx cancel 时，renderer 必须在 3s 内落地最后状态并 return。
//     超时视为重大违规（会阻塞 Router 的下一次请求处理）。
//  3. **错误上报**：任何返回 error 都应先尝试封装为 *RendererError 暴露
//     lastFullContent，便于 Router 走 plugin.Send(content) 兜底路径。
//     直接返回裸 error 等价于"内容丢失"。
//  4. **不关闭 channel**：eventCh 由 Router 提供、Router 通过 Unsubscribe 关闭。
//     renderer 只消费，不 close。
type EventRenderer interface {
	ChannelPlugin
	RenderEventStream(ctx context.Context, scope SessionScope, eventCh <-chan master.BroadcastMessage) error
}

// InboundControlResult 描述平台在 Router 分发前对入站消息的控制结果。
// 典型场景：
//   - 命令已处理：Handled=true, Response="..."
//   - rollout/mute 静默丢弃：Drop=true
//   - /reset 需要重绑会话：SessionIDOverride="new-session-id"
type InboundControlResult struct {
	Handled           bool
	Drop              bool
	Response          string
	SessionIDOverride string
	ModelOverride     string
}

// InboundController 是可选能力接口：允许平台在 Router 调度 LLM 前拦截命令或治理规则。
// channel 包只依赖这个抽象，不关心具体平台实现。
type InboundController interface {
	ControlInbound(ctx context.Context, msg InboundMessage, currentSessionID string) (InboundControlResult, error)
}

// PendingInputDetector 是可选能力接口：平台可告知 Router 当前会话是否正在等待
// 用户输入。Router 据此跳过 sender-level debounce，让澄清/选择/审批回复立即进入
// ControlInbound，而不是被当作新一轮普通消息延迟合并。
type PendingInputDetector interface {
	HasPendingInput(ctx context.Context, msg InboundMessage, currentSessionID string) bool
}

// InputCoordinator 是 IM 平台桥接 HITL 的最小契约。
// 它不改变权限策略，只负责把 master 已经产生的 pending input 显示到平台，
// 并把用户回复提交回同一个 request。
type InputCoordinator interface {
	PendingInputs(taskID string) []*master.InputRequest
	SubmitInput(resp master.InputResponse) error
}

// RendererError 包装 renderer 内部错误并暴露最后一次成功落地的完整内容，
// 供 Router 以 plugin.Send(baseMsg with LastContent) 做非流式兜底。
// 不带 LastContent 的 RendererError（LastContent==""）= 彻底失败，Router 会
// 走 NotifyError 路径。
type RendererError struct {
	Inner       error
	LastContent string
}

// Error 实现 error 接口。
func (e *RendererError) Error() string {
	if e == nil || e.Inner == nil {
		return "renderer error"
	}
	return fmt.Sprintf("renderer error: %v", e.Inner)
}

// Unwrap 支持 errors.Is / errors.As 解包 Inner。
func (e *RendererError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Inner
}

// WrapRendererErr 辅助构造 *RendererError。inner==nil 时返回 nil（无错误）。
// 调用方在 RenderEventStream 内部错误路径用此 helper 统一输出形态。
func WrapRendererErr(inner error, lastContent string) error {
	if inner == nil {
		return nil
	}
	return &RendererError{Inner: inner, LastContent: lastContent}
}
