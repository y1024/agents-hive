package feishu

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/channel"
	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/observability"
)

// Plugin 飞书 ChannelPlugin 实现
type Plugin struct {
	cfg        config.FeishuConfig
	client     *Client
	webhook    *WebhookHandler
	longConn   *LongConnClient
	governance *GovernanceService
	chatState  ChatStateRepo
	leaderGate ReliabilityLeaderGate
	logger     *zap.Logger
}

// New 创建飞书插件。cfg 按值传入，内部调 Normalize 兜底校验——即便 bootstrap 没走 Normalize
// （如测试直接构造 FeishuConfig{}），plugin 侧也会把 AckEmoji 回退到 "Get"、Renderer.ThrottleMs 回退到 300。
//
// HITL 桥接：当前签名保留 4 参数向后兼容；HITL 通过 WithHITLBridge 二次注入。
// bootstrap 在拿到 master.HITLBroker 后调用 plugin.WithHITLBridge(bridge) 完成接线。
func New(cfg config.FeishuConfig, router *channel.Router, logger *zap.Logger) *Plugin {
	cfg.Normalize(func(msg, original, fallback string) {
		logger.Warn(msg,
			zap.String("original", original),
			zap.String("fallback", fallback),
			zap.String("phase", "plugin_new"))
	})
	// Phase 7 P0:Region=intl/lark/international 时 SDK base URL 必须切到 Lark 实例
	// (open.larksuite.com),否则 Lark 客户连不上飞书 cn 端点。
	// 默认/cn → SDK 内置 open.feishu.cn,无需传 OpenBaseUrl。
	clientOpts := feishuClientOptionsForRegion(cfg.Region)
	client := NewClient(cfg.AppID, cfg.AppSecret, logger, clientOpts...)
	p := &Plugin{
		cfg:    cfg,
		client: client,
		logger: logger,
	}
	p.client.ApplyOutboundConfig(cfg)
	p.webhook = NewWebhookHandler(cfg.VerificationToken, cfg.EncryptKey, router, logger).
		WithEventEncryptEnabled(cfg.EventEncryptEnabled).
		WithClient(client)
	p.longConn = NewLongConnClient(cfg.AppID, cfg.AppSecret, client, router, logger)
	p.longConn.ConfigureReliability(
		cfg.HeartbeatStaleWindowResolved(),
		cfg.GapFetchMaxWindowResolved(),
		cfg.GapFetchEnabledResolved(),
	)
	p.longConn.WithReconnectWatchdogHooks(nil, func() {
		if !cfg.GapFetchEnabledResolved() {
			return
		}
		isLeader, err := p.tryAcquireReliabilityLeadership()
		if err != nil {
			p.logger.Warn("飞书 longconn 恢复后跳过自动 gap fetch：leader gate 检查失败",
				zap.Error(err))
			return
		}
		if !isLeader {
			p.logger.Info("飞书 longconn 恢复后跳过自动 gap fetch：当前实例不是 reliability leader")
			return
		}
		tenantKey := p.longConn.LastTenantKey()
		if tenantKey == "" {
			p.logger.Warn("飞书 longconn 恢复后跳过自动 gap fetch：缺少 tenant_key")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := p.ReplayPendingGapFetchForActiveChats(ctx, tenantKey); err != nil {
			p.logger.Warn("飞书 longconn 恢复后自动 gap fetch 失败",
				zap.String("tenant_key", tenantKey),
				zap.Error(err))
			return
		}
		p.logger.Info("飞书 longconn 恢复后自动 gap fetch 完成",
			zap.String("tenant_key", tenantKey))
	})
	return p
}

// WithHITLBridge 二次注入 HITL 桥接（webhook + longconn 同时启用 card.action.trigger 通路）。
// 必须在 Start() 之前调用——longconn dispatcher 在 Start() 里冻结。
func (p *Plugin) WithHITLBridge(bridge *FeishuHITLBridge) *Plugin {
	p.webhook = p.webhook.WithHITLBridge(bridge)
	p.longConn = p.longConn.WithHITLBridge(bridge)
	return p
}

// WithLifecycleHandler 注入机器人进群/退群生命周期处理器。
func (p *Plugin) WithLifecycleHandler(handler *LifecycleHandler) *Plugin {
	p.webhook = p.webhook.WithLifecycleHandler(handler)
	return p
}

// WithGovernance 注入飞书治理服务。
func (p *Plugin) WithGovernance(governance *GovernanceService) *Plugin {
	p.governance = governance
	return p
}

func (p *Plugin) WithChatStateRepo(repo ChatStateRepo) *Plugin {
	p.chatState = repo
	return p
}

func (p *Plugin) WithReliabilityLeaderGate(gate ReliabilityLeaderGate) *Plugin {
	p.leaderGate = gate
	return p
}

func (p *Plugin) DownloadAttachment(ctx context.Context, msg channel.InboundMessage, att channel.Attachment) (channel.AttachmentData, error) {
	if p == nil || p.client == nil {
		return channel.AttachmentData{}, nil
	}
	data, fileName, err := p.client.DownloadMessageResource(ctx, msg.MessageID, att.Key, att.Type)
	if err != nil {
		return channel.AttachmentData{}, err
	}
	if fileName == "" {
		fileName = att.FileName
	}
	return channel.AttachmentData{
		Data:     data,
		FileName: fileName,
		MimeType: feishuAttachmentMimeType(att.Type, fileName),
	}, nil
}

func feishuAttachmentMimeType(kind, filename string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "image":
		return "image/png"
	case "audio":
		return "audio/mpeg"
	case "video", "media":
		return "video/mp4"
	}
	if mt := mime.TypeByExtension(filepath.Ext(filename)); mt != "" {
		return mt
	}
	return "application/octet-stream"
}

func (p *Plugin) ReloadFromConfig(cfg config.FeishuConfig) error {
	if p == nil {
		return nil
	}
	p.cfg = cfg
	if p.client != nil {
		if err := p.client.ReloadFromConfig(cfg); err != nil {
			return err
		}
	}
	if p.webhook != nil {
		if err := p.webhook.ReloadFromConfig(cfg); err != nil {
			return err
		}
	}
	if p.longConn != nil {
		p.longConn.ConfigureReliability(
			cfg.HeartbeatStaleWindowResolved(),
			cfg.GapFetchMaxWindowResolved(),
			cfg.GapFetchEnabledResolved(),
		)
	}
	return nil
}

func (p *Plugin) SetMetricsWriter(w observability.MetricsWriter) {
	if p == nil {
		return
	}
	if p.client != nil {
		p.client.SetMetricsWriter(w)
	}
	if p.webhook != nil {
		p.webhook.SetMetricsWriter(w)
		if p.webhook.lifecycle != nil {
			p.webhook.lifecycle.WithMetricsWriter(w)
		}
		if p.webhook.hitlBridge != nil {
			p.webhook.hitlBridge.WithMetricsWriter(w)
		}
	}
	if p.longConn != nil && p.longConn.hitlBridge != nil {
		p.longConn.hitlBridge.WithMetricsWriter(w)
	}
}

func (p *Plugin) ControlInbound(ctx context.Context, msg channel.InboundMessage, currentSessionID string) (channel.InboundControlResult, error) {
	if p.governance == nil {
		return channel.InboundControlResult{}, nil
	}
	state, err := p.governance.CheckInbound(ctx, msg.TenantKey, msg.ChatID)
	if err != nil {
		return channel.InboundControlResult{}, err
	}
	cmd, ok := ParseCommand(msg.Content)
	if ok {
		response, nextSessionID, handled, err := p.governance.ExecuteCommand(ctx, msg, currentSessionID, *cmd)
		if err != nil {
			return channel.InboundControlResult{}, err
		}
		return channel.InboundControlResult{
			Handled:           handled,
			Response:          response,
			SessionIDOverride: nextSessionID,
		}, nil
	}
	if p.governance.ShouldDropNormalMessage(time.Now(), state) {
		return channel.InboundControlResult{Drop: true}, nil
	}
	result := channel.InboundControlResult{}
	if state != nil && state.ModelOverride != "" {
		result.ModelOverride = state.ModelOverride
	}
	return result, nil
}

// Client 返回插件内部复用的飞书 API client，供 bootstrap 装配 resolver / tools。
func (p *Plugin) Client() *Client {
	return p.client
}

// Platform 返回平台标识
func (p *Plugin) Platform() channel.Platform {
	return channel.PlatformFeishu
}

// Send 发送消息到飞书
// 根据 MsgType 自动选择发送格式：
// - markdown/空: AI 回复包装为飞书 Markdown 卡片（interactive）
// - interactive: 原始卡片 JSON，直接发送
// - text: 纯文本
//
// Phase 0 P0-#11：所有出站文本 / 卡片均经 SanitizeOutboundMentions 过滤，
// 确保 bot 不会因 echo 用户输入或 AI 自由生成而触发 @所有人 / @here 等 broadcast。
func (p *Plugin) Send(ctx context.Context, msg channel.OutboundMessage) error {
	start := time.Now()
	if p.governance != nil {
		if err := p.governance.CheckOutbound(ctx, msg.TenantKey, msg.ChatID); err != nil {
			p.logger.Warn("飞书消息发送被 suppression 拦截",
				zap.String("tenant_key", msg.TenantKey),
				zap.String("chat_id", msg.ChatID),
				zap.Error(err))
			return nil
		}
	}
	// 出站前先对原始 Content 做 mention 白名单 sanitize，再走 buildMessageContent。
	// 注意：interactive 分支会把 msg.Content 视为整张卡片 JSON 透传，sanitize 在 JSON 层做也安全
	// （我们不会破坏合法 `<at user_id="ou_xxx">`，且 JSON 转义后的 \u200b 等也会被识别为零宽）。
	msg.Content = SanitizeOutboundMentions(msg.Content)

	// 确定消息类型和内容
	msgType, content := p.buildMessageContent(msg)

	// 大消息分块发送（仅对 text 类型分块，卡片消息不分块）
	if msgType == "text" {
		chunks := channel.ChunkText(msg.Content, 18000)
		for _, chunk := range chunks {
			textJSON, _ := json.Marshal(map[string]string{"text": chunk})
			if err := p.sendOne(ctx, msg, "text", string(textJSON)); err != nil {
				p.logger.Warn("飞书消息发送失败",
					zap.Duration("duration", time.Since(start)),
					zap.String("chat_id", msg.ChatID),
					zap.Error(err),
				)
				return err
			}
		}
		p.logger.Info("飞书消息发送完成",
			zap.Duration("duration", time.Since(start)),
			zap.String("chat_id", msg.ChatID),
			zap.Int("chunks", len(chunks)),
		)
		return nil
	}

	if err := p.sendOne(ctx, msg, msgType, content); err != nil {
		p.logger.Warn("飞书消息发送失败",
			zap.Duration("duration", time.Since(start)),
			zap.String("chat_id", msg.ChatID),
			zap.Error(err),
		)
		return err
	}
	p.logger.Info("飞书消息发送完成",
		zap.Duration("duration", time.Since(start)),
		zap.String("chat_id", msg.ChatID),
	)
	return nil
}

// buildMessageContent 根据 MsgType 构建飞书消息格式
func (p *Plugin) buildMessageContent(msg channel.OutboundMessage) (msgType string, content string) {
	switch msg.MsgType {
	case channel.MsgTypeInteractive:
		// 原始卡片 JSON，直接透传
		return "interactive", msg.Content
	case channel.MsgTypeText:
		// 纯文本
		return "text", ""
	case channel.MsgTypeImage:
		return "image", msg.Content
	case channel.MsgTypeFile:
		return "file", msg.Content
	default:
		// 默认: Markdown → 飞书卡片
		return "interactive", BuildMarkdownCard(msg.Content)
	}
}

// sendOne 发送单条消息
// reply 失败时自动 fallback 到 SendMessage（飞书 reply 接口对消息 ID 有时效性，长任务处理后可能过期）
func (p *Plugin) sendOne(ctx context.Context, msg channel.OutboundMessage, msgType, content string) error {
	if msg.ReplyTo != "" {
		if err := p.client.ReplyMessage(ctx, msg.ReplyTo, msgType, content); err != nil {
			p.logger.Warn("飞书 reply 失败，fallback 到 SendMessage",
				zap.String("reply_to", msg.ReplyTo),
				zap.String("chat_id", msg.ChatID),
				zap.Error(err),
			)
			return p.client.SendMessage(ctx, msg.ChatID, msgType, content)
		}
		return nil
	}
	return p.client.SendMessage(ctx, msg.ChatID, msgType, content)
}

// WebhookHandler 返回飞书事件回调处理器
func (p *Plugin) WebhookHandler() http.HandlerFunc {
	return p.webhook.ServeHTTP
}

// Verify 验证飞书回调签名
func (p *Plugin) Verify(r *http.Request) bool {
	return true // 签名验证在 WebhookHandler 内部处理
}

// Start 启动飞书长连接。
func (p *Plugin) Start() error {
	mode := p.cfg.ResolvedIngressMode()
	if mode != config.FeishuIngressModeLongconn {
		p.logger.Info("飞书当前不是 longconn 模式，跳过长连接启动",
			zap.String("ingress_mode", string(mode)))
		return nil
	}
	return p.longConn.Start(context.Background())
}

// Stop 停止飞书长连接，实现 channel.Stoppable 接口
func (p *Plugin) Stop() error {
	if p != nil && p.leaderGate != nil {
		if err := p.leaderGate.Close(); err != nil {
			return err
		}
	}
	return p.longConn.Stop()
}

// ReplayPendingGapFetch 在 longconn 恢复后，按显式 chat 目标执行一次历史回补。
// 只消费当前待处理窗口，不负责发现“应该回补哪些 chat”。
func (p *Plugin) ReplayPendingGapFetch(ctx context.Context, tenantKey, chatID string) error {
	return p.ReplayPendingGapFetches(ctx, tenantKey, []string{chatID})
}

func (p *Plugin) ReplayPendingGapFetches(ctx context.Context, tenantKey string, chatIDs []string) error {
	if p == nil || p.longConn == nil {
		return nil
	}
	return p.longConn.ReplayGapFetchWindows(ctx, tenantKey, chatIDs)
}

func (p *Plugin) ReplayPendingGapFetchForActiveChats(ctx context.Context, tenantKey string) error {
	if p == nil || p.longConn == nil || p.chatState == nil {
		return nil
	}
	records, err := p.chatState.ListActive(ctx, string(channel.PlatformFeishu), tenantKey)
	if err != nil {
		return err
	}
	chatIDs := make([]string, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record.ChatID == "" {
			continue
		}
		if _, ok := seen[record.ChatID]; ok {
			continue
		}
		seen[record.ChatID] = struct{}{}
		chatIDs = append(chatIDs, record.ChatID)
	}
	return p.ReplayPendingGapFetches(ctx, tenantKey, chatIDs)
}

func (p *Plugin) ReliabilityStatus() LongConnReliabilityStatus {
	if p == nil || p.longConn == nil {
		return LongConnReliabilityStatus{}
	}
	if _, err := p.tryAcquireReliabilityLeadership(); err != nil {
		p.logger.Warn("获取飞书 reliability leader 状态失败", zap.Error(err))
	}
	return p.longConn.ReliabilityStatus()
}

// LongConnStartedForTest 返回 longconn 是否已启动，仅供测试断言使用。
func (p *Plugin) LongConnStartedForTest() bool {
	if p == nil || p.longConn == nil {
		return false
	}
	return p.longConn.started
}

// SetLongConnStartHookForTest 覆盖 longconn 启动逻辑，仅供测试使用。
func (p *Plugin) SetLongConnStartHookForTest(hook func(context.Context) error) {
	if p == nil || p.longConn == nil {
		return
	}
	p.longConn.startHook = hook
}

func (p *Plugin) tryAcquireReliabilityLeadership() (bool, error) {
	if p == nil || p.longConn == nil {
		return false, nil
	}
	if p.leaderGate == nil {
		p.longConn.setReliabilityLeader(true)
		return true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	isLeader, err := p.leaderGate.TryAcquire(ctx)
	if err != nil {
		p.longConn.setReliabilityLeader(false)
		return false, err
	}
	p.longConn.setReliabilityLeader(isLeader)
	return isLeader, nil
}
