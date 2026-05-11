package wechatbot

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/channel"
	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/master"
	"github.com/chef-guo/agents-hive/internal/observability"
)

var _ channel.EventRenderer = (*Plugin)(nil)
var _ channel.InboundController = (*Plugin)(nil)
var _ channel.PendingInputDetector = (*Plugin)(nil)

// Plugin 把官方 wechatbot 接入统一 IM Router。
type Plugin struct {
	registry         *BotRegistry
	logger           *zap.Logger
	inputCoordinator channel.InputCoordinator
}

func NewPlugin(registry *BotRegistry, logger *zap.Logger) *Plugin {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Plugin{registry: registry, logger: logger}
}

func (p *Plugin) WithInputCoordinator(coordinator channel.InputCoordinator) *Plugin {
	if p == nil {
		return nil
	}
	p.inputCoordinator = coordinator
	return p
}

func (p *Plugin) Platform() channel.Platform {
	return channel.PlatformWeChatBot
}

func (p *Plugin) Send(ctx context.Context, msg channel.OutboundMessage) error {
	if msg.OwnerUserID == "" {
		return errors.New("wechatbot send requires owner_user_id")
	}
	if msg.TenantKey != msg.OwnerUserID {
		return errors.New("wechatbot send requires tenant_key == owner_user_id")
	}
	if msg.ChatID == "" {
		return errors.New("wechatbot send requires chat_id")
	}
	if p.registry == nil {
		return errors.New("wechatbot registry not initialized")
	}
	inst, ok := p.registry.Get(msg.OwnerUserID)
	if !ok {
		return errors.New("wechatbot not connected")
	}
	if msg.ReplyToken != "" {
		return inst.Reply(ctx, msg.ChatID, msg.ReplyToken, msg.Content)
	}
	return inst.Send(ctx, msg.ChatID, msg.Content)
}

func (p *Plugin) WebhookHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "wechatbot uses long polling", http.StatusNotFound)
	}
}

func (p *Plugin) Verify(*http.Request) bool {
	return true
}

func (p *Plugin) HasPendingInput(_ context.Context, _ channel.InboundMessage, currentSessionID string) bool {
	return p.pendingInput(currentSessionID) != nil
}

func (p *Plugin) ControlInbound(_ context.Context, msg channel.InboundMessage, currentSessionID string) (channel.InboundControlResult, error) {
	req := p.pendingInput(currentSessionID)
	if req == nil {
		return channel.InboundControlResult{}, nil
	}
	resp, help, ok := buildInputResponseFromWechat(req, msg.Content)
	if !ok {
		return channel.InboundControlResult{Handled: true, Response: help}, nil
	}
	if err := p.inputCoordinator.SubmitInput(resp); err != nil {
		if errs.IsCode(err, errs.CodeInputNotPending) {
			return channel.InboundControlResult{Handled: true, Response: "这个请求已经处理或已超时，请重新发起。"}, nil
		}
		return channel.InboundControlResult{}, err
	}
	return channel.InboundControlResult{Handled: true, Response: "已收到，继续处理。"}, nil
}

func (p *Plugin) pendingInput(sessionID string) *master.InputRequest {
	if p == nil || p.inputCoordinator == nil || sessionID == "" {
		return nil
	}
	pending := p.inputCoordinator.PendingInputs(sessionID)
	if len(pending) == 0 {
		return nil
	}
	var selected *master.InputRequest
	for _, req := range pending {
		if req == nil {
			continue
		}
		if selected == nil || req.CreatedAt.After(selected.CreatedAt) {
			selected = req
		}
	}
	return selected
}

func buildInputResponseFromWechat(req *master.InputRequest, raw string) (master.InputResponse, string, bool) {
	text := strings.TrimSpace(raw)
	resp := master.InputResponse{
		RequestID: req.ID,
		TaskID:    req.TaskID,
		Value:     text,
	}
	switch req.Type {
	case master.InputClarification, master.InputChoice:
		if selected, ok := optionByIndex(text, req.Options); ok {
			resp.Value = selected
		}
		return resp, "", true
	case master.InputApproval, master.InputPermission:
		if action, ok := parseApprovalAction(text); ok {
			resp.Action = action
			if action == "approve" && text != "" && !isApprovalWord(text) {
				resp.Value = text
			}
			return resp, "", true
		}
		return resp, "请回复“批准”或“拒绝”。如果要调整参数，请回复批准并附上 JSON。", false
	case master.InputConfirmation:
		if action, ok := parseConfirmationAction(text); ok {
			resp.Action = action
			return resp, "", true
		}
		return resp, "请回复“继续”“跳过”或“取消”。", false
	default:
		return resp, "", true
	}
}

func optionByIndex(text string, options []string) (string, bool) {
	if len(options) == 0 || text == "" {
		return "", false
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 1 || n > len(options) {
		return "", false
	}
	return options[n-1], true
}

func parseApprovalAction(text string) (string, bool) {
	normalized := normalizeActionText(text)
	switch normalized {
	case "批准", "同意", "可以", "确认", "是", "yes", "y", "ok", "approve", "approved", "proceed":
		return "approve", true
	case "拒绝", "不同意", "不行", "否", "no", "n", "reject", "rejected", "cancel", "取消":
		return "reject", true
	default:
		return "", false
	}
}

func parseConfirmationAction(text string) (string, bool) {
	normalized := normalizeActionText(text)
	switch normalized {
	case "继续", "执行", "确认", "可以", "是", "yes", "y", "ok", "proceed", "approve":
		return "proceed", true
	case "跳过", "skip":
		return "skip", true
	case "取消", "停止", "不", "否", "no", "n", "cancel", "reject":
		return "cancel", true
	default:
		return "", false
	}
}

func isApprovalWord(text string) bool {
	_, ok := parseApprovalAction(text)
	return ok
}

func normalizeActionText(text string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(text)), "。.!！ ")
}

func (p *Plugin) Stop() error {
	if p.registry == nil {
		return nil
	}
	return p.registry.Stop()
}

func (p *Plugin) SetMetricsWriter(w observability.MetricsWriter) {
	if p == nil || p.registry == nil {
		return
	}
	p.registry.SetMetricsWriter(w)
}

func (p *Plugin) SetConfig(cfg Config) {
	if p == nil || p.registry == nil {
		return
	}
	p.registry.SetConfig(cfg)
}

func (p *Plugin) Registry() *BotRegistry {
	return p.registry
}
