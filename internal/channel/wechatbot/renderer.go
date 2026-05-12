package wechatbot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/channel"
	"github.com/chef-guo/agents-hive/internal/master"
)

const (
	wechatRendererFinalTimeout = 3 * time.Second
)

type wechatRendererState struct {
	lastAssistantContent string
	lastSentContent      string
	renderedInputs       map[string]bool
}

func (p *Plugin) RenderEventStream(ctx context.Context, scope channel.SessionScope, eventCh <-chan master.BroadcastMessage) error {
	state := &wechatRendererState{renderedInputs: make(map[string]bool)}
	for {
		select {
		case <-ctx.Done():
			p.finalRendererFlush(scope, state)
			return ctx.Err()
		case ev, ok := <-eventCh:
			if !ok {
				return nil
			}
			if ev.SessionID != "" && ev.SessionID != scope.SessionID {
				continue
			}
			if err := p.renderWechatEvent(ctx, scope, state, ev); err != nil {
				return channel.WrapRendererErr(err, state.lastSentContent)
			}
		}
	}
}

func (p *Plugin) renderWechatEvent(ctx context.Context, scope channel.SessionScope, state *wechatRendererState, ev master.BroadcastMessage) error {
	switch ev.Type {
	case master.EventTypeInputReceived:
		return p.sendRendererText(ctx, scope, state, "收到，正在处理...")
	case master.EventTypeMessage:
		text, partial, role, hasToolCalls := extractWechatMessagePayload(ev.Payload)
		if text == "" {
			return nil
		}
		if role == "tool" {
			return p.sendRendererText(ctx, scope, state, formatWechatToolMessage(ev.Payload, text))
		}
		if role != "" && role != "assistant" {
			return nil
		}
		state.lastAssistantContent = text
		if hasToolCalls {
			partial = true
		}
		if partial {
			return nil
		}
		return p.sendRendererText(ctx, scope, state, text)
	case master.EventTypeInputRequest:
		req, ok := ev.Payload.(*master.InputRequest)
		if !ok || req == nil {
			return nil
		}
		if state.renderedInputs[req.ID] {
			return nil
		}
		state.renderedInputs[req.ID] = true
		return p.sendRendererText(ctx, scope, state, formatWechatInputRequest(req))
	case master.EventTypeToolCall:
		line := formatWechatToolCall(ev.Payload)
		if line == "" {
			return nil
		}
		return p.sendRendererText(ctx, scope, state, line)
	case master.EventTypeError:
		text := extractWechatErrorText(ev.Payload)
		if text == "" {
			return nil
		}
		return p.sendRendererText(ctx, scope, state, "处理失败："+text)
	case master.EventTypeAgentStatus:
		text := formatWechatAgentStatus(ev.Payload)
		if text == "" {
			return nil
		}
		return p.sendRendererText(ctx, scope, state, text)
	default:
		return nil
	}
}

func (p *Plugin) sendRendererText(ctx context.Context, scope channel.SessionScope, state *wechatRendererState, text string) error {
	text = strings.TrimSpace(text)
	if text == "" || text == state.lastSentContent {
		return nil
	}
	if scope.OwnerUserID == "" || scope.TenantKey == "" || scope.ChatID == "" {
		return fmt.Errorf("wechatbot renderer missing owner/tenant/chat scope")
	}
	msg := channel.OutboundMessage{
		Platform:    channel.PlatformWeChatBot,
		TenantKey:   scope.TenantKey,
		OwnerUserID: scope.OwnerUserID,
		ChatID:      scope.ChatID,
		Content:     text,
		ReplyTo:     scope.ReplyToID,
		ReplyToken:  scope.ReplyToken,
	}
	if err := p.Send(ctx, msg); err != nil {
		if p.logger != nil {
			p.logger.Warn("wechatbot renderer 发送失败",
				zap.String("session_id", scope.SessionID),
				zap.String("chat_id", scope.ChatID),
				zap.Int("content_len", len([]rune(text))),
				zap.Error(err))
		}
		return err
	}
	state.lastSentContent = text
	return nil
}

func (p *Plugin) finalRendererFlush(scope channel.SessionScope, state *wechatRendererState) {
	if state == nil || strings.TrimSpace(state.lastAssistantContent) == "" || state.lastAssistantContent == state.lastSentContent {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), wechatRendererFinalTimeout)
	defer cancel()
	if err := p.sendRendererText(ctx, scope, state, state.lastAssistantContent); err != nil && p.logger != nil {
		p.logger.Warn("wechatbot renderer 最终 flush 失败",
			zap.String("session_id", scope.SessionID),
			zap.Error(err))
	}
}

func formatWechatInputRequest(req *master.InputRequest) string {
	var sb strings.Builder
	switch req.Type {
	case master.InputApproval, master.InputPermission:
		if req.ToolName != "" {
			sb.WriteString("需要确认操作：")
			sb.WriteString(req.ToolName)
			sb.WriteString("\n\n")
		}
		if req.Prompt != "" {
			sb.WriteString(req.Prompt)
			sb.WriteString("\n\n")
		}
		sb.WriteString("请回复“批准”或“拒绝”。")
	case master.InputConfirmation:
		if req.Prompt != "" {
			sb.WriteString(req.Prompt)
			sb.WriteString("\n\n")
		}
		sb.WriteString("请回复“继续”“跳过”或“取消”。")
	case master.InputClarification, master.InputChoice:
		sb.WriteString(req.Prompt)
		if len(req.Options) > 0 {
			sb.WriteString("\n\n可选项：")
			for i, opt := range req.Options {
				sb.WriteString(fmt.Sprintf("\n%d. %s", i+1, opt))
			}
		}
		sb.WriteString("\n\n请直接回复。")
	default:
		sb.WriteString(req.Prompt)
		if sb.Len() == 0 {
			sb.WriteString("请补充信息。")
		}
	}
	return strings.TrimSpace(sb.String())
}

func formatWechatAgentStatus(payload any) string {
	var status, msg, errText string
	switch v := payload.(type) {
	case map[string]any:
		status, _ = v["status"].(string)
		msg, _ = v["message"].(string)
		errText, _ = v["error"].(string)
	}
	switch status {
	case "completed":
		return "完成。"
	case "error":
		if errText != "" {
			return "任务失败：" + errText
		}
		return "任务失败。"
	case "paused":
		if msg != "" {
			return "已暂停：" + msg
		}
		return "已暂停。"
	default:
		return ""
	}
}

func formatWechatToolMessage(payload any, text string) string {
	toolName := ""
	isErr := false
	switch v := payload.(type) {
	case map[string]any:
		toolName, _ = v["tool_name"].(string)
		isErr, _ = v["is_error"].(bool)
	}
	prefix := "执行结果"
	if toolName != "" {
		prefix += "：" + toolName
	}
	if isErr {
		prefix = strings.Replace(prefix, "执行结果", "执行失败", 1)
	}
	return prefix + "\n" + strings.TrimSpace(text)
}

type wechatToolCallPayload struct {
	name   string
	status string
	err    string
}

func formatWechatToolCall(payload any) string {
	tc := extractWechatToolCallPayload(payload)
	if tc.name == "" {
		return ""
	}
	switch tc.status {
	case "start":
		return "正在执行：" + tc.name
	case "error":
		if tc.err != "" {
			return "执行失败：" + tc.name + "\n" + tc.err
		}
		return "执行失败：" + tc.name
	default:
		return ""
	}
}

func extractWechatToolCallPayload(payload any) wechatToolCallPayload {
	var out wechatToolCallPayload
	switch v := payload.(type) {
	case master.ToolCallEvent:
		out.name = v.ToolName
		out.status = v.Status
		out.err = v.Error
	case *master.ToolCallEvent:
		if v != nil {
			out.name = v.ToolName
			out.status = v.Status
			out.err = v.Error
		}
	case map[string]any:
		out.name, _ = v["tool_name"].(string)
		out.status, _ = v["status"].(string)
		out.err, _ = v["error"].(string)
	}
	return out
}

func extractWechatMessagePayload(payload any) (text string, partial bool, role string, hasToolCalls bool) {
	switch v := payload.(type) {
	case string:
		return v, false, "", false
	case map[string]any:
		if c, ok := v["content"].(string); ok {
			text = c
		} else if c, ok := v["text"].(string); ok {
			text = c
		} else if c, ok := v["message"].(string); ok {
			text = c
		}
		if p, ok := v["partial"].(bool); ok {
			partial = p
		}
		if r, ok := v["role"].(string); ok {
			role = r
		}
		switch tcs := v["tool_calls"].(type) {
		case []any:
			hasToolCalls = len(tcs) > 0
		case []map[string]any:
			hasToolCalls = len(tcs) > 0
		}
	}
	return text, partial, role, hasToolCalls
}

func extractWechatErrorText(payload any) string {
	switch v := payload.(type) {
	case string:
		return v
	case error:
		return v.Error()
	case map[string]any:
		if m, ok := v["message"].(string); ok && m != "" {
			return m
		}
		if e, ok := v["error"].(string); ok {
			return e
		}
	}
	return ""
}
