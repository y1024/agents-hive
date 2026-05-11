package channel

import (
	"context"
	"encoding/json"
	"time"

	"github.com/chef-guo/agents-hive/internal/imctx"
	"github.com/chef-guo/agents-hive/internal/master"
)

// Platform 标识 IM 平台
type Platform string

const (
	PlatformDingTalk  Platform = "dingtalk"
	PlatformFeishu    Platform = "feishu"
	PlatformWeCom     Platform = "wecom"
	PlatformWeChatBot Platform = "wechatbot"
)

// ChatType 聊天类型
type ChatType string

const (
	ChatDirect  ChatType = "direct"
	ChatGroup   ChatType = "group"
	ChatChannel ChatType = "channel"
)

// MessageProcessor 消息处理接口，解耦 Router 与 Master
// Phase 1: Master 实现此接口
// Phase 4: ControlPlane 实现此接口
type MessageProcessor interface {
	ProcessMessage(ctx context.Context, sessionID string, input string) (master.TaskResponse, error)
}

// IMMessageProcessor 是可选扩展接口：支持透传平台原消息 ID（IM 通道独有，Web/CLI 不需要）。
// Master 实现此接口；更轻量的 processor 可以只实现 MessageProcessor。
// Router 通过类型断言获取，断言失败时 fallback 到 ProcessMessage（空 channelMessageID）。
//
// 前置条件：实现此接口的类型必须同时实现 MessageProcessor——sidecar 是"扩展"而非"替换"，
// Router 对 r.processor 的静态类型断言基于 MessageProcessor，IMMessageProcessor 仅在
// "已满足 MessageProcessor"的前提下额外解锁 IM 元数据透传能力。
//
// 契约（im-streaming-reply spec 69-73）：channelMessageID 写入 SessionRequest.ChannelMessageID，
// harness 在广播 input_received 事件时透传给 renderer 做平台 ack。
//
// 方法名与 MessageProcessor.ProcessMessage 不同，避免接口方法签名冲突。
type IMMessageProcessor interface {
	ProcessMessageFromIM(
		ctx context.Context,
		sessionID string,
		input string,
		channelMessageID string,
		modelOverride string,
		ackAlreadyEmitted bool,
		imCtx *imctx.IMMessageContext,
	) (master.TaskResponse, error)
}

// EventBusSubscriber 是 Router 依赖的 master 订阅契约最小子集。
// 抽成接口避免 channel → master 的硬耦合，并方便测试 mock。
// master.Master 天然满足该接口（SubscribeWSBroadcast / UnsubscribeWSBroadcast）。
type EventBusSubscriber interface {
	SubscribeWSBroadcast() (uint64, chan master.BroadcastMessage)
	UnsubscribeWSBroadcast(subID uint64)
	BroadcastSessionMessage(sessionID string, msg master.BroadcastMessage)
}

// InboundContextResolver 在 dedup/debounce 之后、dispatchProcess 之前被调用。
// 可修改 msg.References、填 ParentContent、构造 SystemPromptPrefix。
// 失败必须 degrade（返回 nil + warn），绝不阻断消息处理。
type InboundContextResolver interface {
	Resolve(ctx context.Context, msg *InboundMessage) (*imctx.IMMessageContext, error)
}

// SessionScope 渲染上下文：携带 renderer 需要的会话/用户/平台 ID 与消息 ID，
// 使 renderer 无需回查任何状态即可完成"事件 → 平台 API 调用"的翻译。
// SessionID 是 subscriber 端 filter EventBus 事件的唯一依据。
type SessionScope struct {
	SessionID   string `json:"session_id"`              // 用于 filter EventBus 事件
	TenantKey   string `json:"tenant_key,omitempty"`    // 平台租户/owner 作用域
	OwnerUserID string `json:"owner_user_id,omitempty"` // user-scoped 平台 owner（wechatbot）
	ChatID      string `json:"chat_id,omitempty"`       // 平台 chat / open_chat_id
	ReplyToID   string `json:"reply_to_id,omitempty"`   // 用户原消息 ID（飞书 reply_message_id）
	ReplyToken  string `json:"reply_token,omitempty"`   // 平台私有回复上下文（wechatbot=iLink context_token）
	UserID      string `json:"user_id,omitempty"`       // 平台 user_id（用于 @ 或权限）
	MessageID   string `json:"message_id,omitempty"`    // 用户原消息 ID，用于 ack 表情
}

// InboundMessage 从 IM 平台收到的统一消息结构
//
// TenantKey 是平台多租户标识（飞书 tenant_key / 钉钉 corpId / 企微 corp_id /
// 微信留空），由 channel 平台插件从事件元数据提取。Router 调用
// imctx.BuildSessionID 拼装 session_id 时强依赖该字段——空值会被回退为
// "default"，但**严禁**生产路径长期依赖 default 兜底，否则不同租户的会话
// 将串到同一 session 主键，导致跨租户消息泄露。
type InboundMessage struct {
	MessageID   string       `json:"message_id"`
	Platform    Platform     `json:"platform"`
	TenantKey   string       `json:"tenant_key,omitempty"`
	OwnerUserID string       `json:"owner_user_id,omitempty"`
	ChatType    ChatType     `json:"chat_type"`
	ChatID      string       `json:"chat_id"`
	SenderID    string       `json:"sender_id"`
	SenderName  string       `json:"sender_name"`
	Content     string       `json:"content"`
	MessageType string       `json:"message_type,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	// ReplyToken 是平台私有的短期回复上下文。
	// wechatbot 使用 iLink context_token；飞书/钉钉/企微保持空值。
	ReplyToken string `json:"reply_token,omitempty"`
	// NoDebounce 用于历史回放 / gap fetch 这类“必须逐条重放”的场景。
	// 为 true 时 Router 跳过 sender-level debounce，避免同一发送者的多条历史消息被错误合并。
	NoDebounce bool `json:"no_debounce,omitempty"`
	// M1 消息摄取扩展（零值兼容非飞书平台）
	References   []imctx.DocRef  `json:"references,omitempty"`
	ParentID     string          `json:"parent_id,omitempty"`
	RootID       string          `json:"root_id,omitempty"`
	Mentions     []imctx.Mention `json:"mentions,omitempty"`
	BotMentioned bool            `json:"bot_mentioned,omitempty"`
	RawData      json.RawMessage `json:"raw_data,omitempty"`
	Timestamp    time.Time       `json:"timestamp"`
}

// Attachment 是消息中可下载的资源引用（平台无关）。
type Attachment struct {
	Type     string `json:"type"`
	Key      string `json:"key"`
	FileName string `json:"file_name,omitempty"`
}

// MsgType 消息格式类型
type MsgType string

const (
	MsgTypeText        MsgType = "text"        // 纯文本
	MsgTypeMarkdown    MsgType = "markdown"    // Markdown（平台自动包装为卡片）
	MsgTypeInteractive MsgType = "interactive" // 飞书卡片 JSON（原始格式）
	MsgTypeImage       MsgType = "image"       // 图片消息
	MsgTypeFile        MsgType = "file"        // 文件消息
)

// OutboundMessage 发送到 IM 平台的统一消息结构
type OutboundMessage struct {
	Platform    Platform `json:"platform"`
	TenantKey   string   `json:"tenant_key,omitempty"`
	OwnerUserID string   `json:"owner_user_id,omitempty"`
	ChatID      string   `json:"chat_id"`
	Content     string   `json:"content"`
	MsgType     MsgType  `json:"msg_type,omitempty"` // 消息格式，默认 text
	ReplyTo     string   `json:"reply_to,omitempty"`
	// ReplyToken 是平台私有的短期回复上下文。
	// wechatbot 使用 iLink context_token；飞书/钉钉/企微保持空值。
	ReplyToken string `json:"reply_token,omitempty"`
}

// Binding IM 通道与会话的绑定关系
type Binding struct {
	Platform  Platform `json:"platform"`
	TenantKey string   `json:"tenant_key,omitempty"`
	ChatID    string   `json:"chat_id"`
	SessionID string   `json:"session_id"`
}
