package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/imcore"
	"github.com/chef-guo/agents-hive/internal/imctx"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/observability"
	"github.com/chef-guo/agents-hive/internal/toolctx"
)

type imAPIInput struct {
	Action         string `json:"action"`
	Platform       string `json:"platform"`
	Query          string `json:"query,omitempty"`
	RecipientID    string `json:"recipient_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	ExternalIDType string `json:"external_id_type,omitempty"`
	Content        string `json:"content,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

type IMAPIToolOptions struct {
	ForceDryRun   bool
	MetricsWriter observability.MetricsWriter
}

const (
	metricIMSendLegacyPathTotal  = "im_send_legacy_path_total"
	metricIMSendUnifiedPathTotal = "im_send_unified_path_total"
)

func RegisterIMAPITool(host *mcphost.Host, logger *zap.Logger, service *imcore.Service) {
	RegisterIMAPIToolWithOptions(host, logger, service, IMAPIToolOptions{})
}

func RegisterIMAPIToolWithOptions(host *mcphost.Host, logger *zap.Logger, service *imcore.Service, options IMAPIToolOptions) {
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"search_recipients", "list_recent_conversations", "resolve_recipient", "send_message"},
				"description": "IM 操作",
			},
			"platform": map[string]any{
				"type":        "string",
				"enum":        []string{"feishu", "wechatbot", "wecom", "dingtalk"},
				"description": "目标 IM 平台",
			},
			"query":           map[string]any{"type": "string", "description": "联系人或会话搜索关键词"},
			"recipient_id":    map[string]any{"type": "string", "description": "im_api 返回的平台中立 recipient id"},
			"conversation_id": map[string]any{"type": "string", "description": "im_api 返回的平台中立 conversation id 或平台 chat id"},
			"external_id_type": map[string]any{
				"type":        "string",
				"description": "可选的平台 ID 类型，兼容旧入口迁移；模型通常不需要填写",
			},
			"content": map[string]any{"type": "string", "description": "要发送的纯文本内容"},
			"limit":   map[string]any{"type": "integer", "description": "返回数量，默认 10"},
			"dry_run": map[string]any{"type": "boolean", "description": "为 true 时只校验目标和权限，不真实发送"},
		},
		"required": []string{"action", "platform"},
	})

	host.RegisterTool(mcphost.ToolDefinition{
		Name:        "im_api",
		Description: "统一 IM 工具。用于飞书、个人微信、企业微信、钉钉的联系人/会话发现和消息发送。按平台能力执行；不支持的能力会返回明确错误。",
		InputSchema: schema,
	}, func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
		startedAt := time.Now()
		writeAudit := func(audit imAPIAuditFields) {
			logIMAPIAudit(ctx, logger, startedAt, audit)
		}
		var params imAPIInput
		if err := json.Unmarshal(input, &params); err != nil {
			writeAudit(imAPIAuditFields{
				Status: "error",
			})
			return errorResult("解析参数失败: " + err.Error()), nil
		}
		if service == nil {
			writeAudit(imAPIAuditFields{
				Action:     params.Action,
				Platform:   params.Platform,
				Status:     "error",
				DryRun:     params.DryRun,
				TargetKind: imAPITargetKind(params, ""),
				ContentLen: len(params.Content),
				TargetHash: imAPITargetHash(params, ""),
			})
			recordIMSendPathMetric(ctx, options.MetricsWriter, metricIMSendUnifiedPathTotal, "im_api", params.Platform, params.Action, "error", logger)
			return errorResult("im_api 未配置 IM service"), nil
		}
		platform := imcore.Platform(params.Platform)
		switch params.Action {
		case "search_recipients":
			items, err := service.SearchRecipients(ctx, platform, params.Query, params.Limit)
			if err != nil {
				writeAudit(imAPIAuditFields{
					Action:     params.Action,
					Platform:   params.Platform,
					Status:     "error",
					DryRun:     params.DryRun,
					TargetKind: imAPITargetKind(params, ""),
					ContentLen: len(params.Content),
					TargetHash: imAPITargetHash(params, ""),
				})
				return errorResult(err.Error()), nil
			}
			writeAudit(imAPIAuditFields{
				Action:      params.Action,
				Platform:    params.Platform,
				Status:      "success",
				DryRun:      params.DryRun,
				TargetKind:  imAPITargetKind(params, ""),
				ContentLen:  len(params.Content),
				ResultCount: len(items),
				TargetHash:  imAPITargetHash(params, ""),
			})
			return jsonToolResult(items), nil
		case "list_recent_conversations":
			items, err := service.ListRecentConversations(ctx, platform, params.Limit)
			if err != nil {
				writeAudit(imAPIAuditFields{
					Action:     params.Action,
					Platform:   params.Platform,
					Status:     "error",
					DryRun:     params.DryRun,
					TargetKind: imAPITargetKind(params, ""),
					ContentLen: len(params.Content),
					TargetHash: imAPITargetHash(params, ""),
				})
				return errorResult(err.Error()), nil
			}
			writeAudit(imAPIAuditFields{
				Action:      params.Action,
				Platform:    params.Platform,
				Status:      "success",
				DryRun:      params.DryRun,
				TargetKind:  imAPITargetKind(params, ""),
				ContentLen:  len(params.Content),
				ResultCount: len(items),
				TargetHash:  imAPITargetHash(params, ""),
			})
			return jsonToolResult(items), nil
		case "resolve_recipient":
			item, err := service.ResolveRecipient(ctx, platform, imcore.RecipientLookup{
				Query:          params.Query,
				RecipientID:    params.RecipientID,
				ConversationID: params.ConversationID,
				ExternalIDType: params.ExternalIDType,
			})
			if err != nil {
				writeAudit(imAPIAuditFields{
					Action:     params.Action,
					Platform:   params.Platform,
					Status:     "error",
					DryRun:     params.DryRun,
					TargetKind: imAPITargetKind(params, ""),
					ContentLen: len(params.Content),
					TargetHash: imAPITargetHash(params, ""),
				})
				return errorResult(err.Error()), nil
			}
			writeAudit(imAPIAuditFields{
				Action:      params.Action,
				Platform:    params.Platform,
				Status:      "success",
				DryRun:      params.DryRun,
				TargetKind:  imAPITargetKind(params, item.Kind),
				ContentLen:  len(params.Content),
				ResultCount: 1,
				TargetHash:  imAPITargetHash(params, item.ID),
			})
			return jsonToolResult(item), nil
		case "send_message":
			result, audit, err := executeIMAPISendMessage(ctx, service, params, options)
			writeAudit(audit)
			recordIMSendPathMetric(ctx, options.MetricsWriter, metricIMSendUnifiedPathTotal, "im_api", params.Platform, params.Action, audit.Status, logger)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			return jsonToolResult(result), nil
		default:
			writeAudit(imAPIAuditFields{
				Action:     params.Action,
				Platform:   params.Platform,
				Status:     "error",
				DryRun:     params.DryRun,
				TargetKind: imAPITargetKind(params, ""),
				ContentLen: len(params.Content),
				TargetHash: imAPITargetHash(params, ""),
			})
			return errorResult(fmt.Sprintf("不支持的 im_api action: %s", params.Action)), nil
		}
	})
}

func executeIMAPISendMessage(ctx context.Context, service *imcore.Service, params imAPIInput, options IMAPIToolOptions) (imcore.SendResult, imAPIAuditFields, error) {
	dryRun := params.DryRun || options.ForceDryRun
	audit := imAPIAuditFields{
		Action:     "send_message",
		Platform:   params.Platform,
		Status:     "error",
		DryRun:     dryRun,
		TargetKind: imAPITargetKind(params, ""),
		ContentLen: len(params.Content),
		TargetHash: imAPITargetHash(params, ""),
	}
	if service == nil {
		return imcore.SendResult{}, audit, fmt.Errorf("im_api 未配置 IM service")
	}
	result, err := service.SendMessage(ctx, imcore.SendTarget{
		Platform:       imcore.Platform(params.Platform),
		RecipientID:    params.RecipientID,
		ConversationID: params.ConversationID,
		ExternalIDType: params.ExternalIDType,
		Content:        params.Content,
		DryRun:         dryRun,
	})
	if err != nil {
		return imcore.SendResult{}, audit, err
	}
	audit.Status = "success"
	audit.TargetKind = imAPITargetKind(params, result.TargetKind)
	audit.ResultCount = 1
	audit.TargetHash = imAPITargetHash(params, result.TargetID)
	return result, audit, nil
}

func recordIMSendPathMetric(ctx context.Context, writer observability.MetricsWriter, name, toolName, platform, operation, status string, logger *zap.Logger) {
	if writer == nil || strings.TrimSpace(name) == "" {
		return
	}
	if strings.TrimSpace(operation) == "" {
		operation = "send_message"
	}
	if operation != "send_message" {
		return
	}
	if strings.TrimSpace(platform) == "" {
		platform = "unknown"
	}
	if strings.TrimSpace(status) == "" {
		status = "unknown"
	}

	metric := observability.Metric{
		Name:  name,
		Value: 1,
		Labels: map[string]any{
			"tool_name": toolName,
			"operation": operation,
			"im":        platform,
			"status":    status,
		},
		Ts: time.Now(),
	}
	go func() {
		if err := writer.Record(context.Background(), metric); err != nil && logger != nil {
			logger.Debug("IM send metric write failed",
				zap.String("metric", name),
				zap.Error(err))
		}
	}()
}

type imAPIAuditFields struct {
	Action      string
	Platform    string
	Status      string
	DryRun      bool
	TargetKind  string
	ContentLen  int
	ResultCount int
	TargetHash  string
}

func logIMAPIAudit(ctx context.Context, logger *zap.Logger, startedAt time.Time, audit imAPIAuditFields) {
	if logger == nil {
		return
	}
	if audit.TargetKind == "" {
		audit.TargetKind = "none"
	}

	fields := []zap.Field{
		zap.String("tool", "im_api"),
		zap.String("action", audit.Action),
		zap.String("platform", audit.Platform),
		zap.String("status", audit.Status),
		zap.Bool("dry_run", audit.DryRun),
		zap.String("target_kind", audit.TargetKind),
		zap.Int("content_len", audit.ContentLen),
		zap.Int("result_count", audit.ResultCount),
		zap.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	}
	if audit.TargetHash != "" {
		fields = append(fields, zap.String("target_id_hash", audit.TargetHash))
	}
	if tc := toolctx.GetToolContext(ctx); tc != nil {
		if tc.TraceID != "" {
			fields = append(fields, zap.String("trace_id", tc.TraceID))
		}
		if tc.SpanID != "" {
			fields = append(fields, zap.String("span_id", tc.SpanID))
		}
		if tc.ParentSpanID != "" {
			fields = append(fields, zap.String("parent_span_id", tc.ParentSpanID))
		}
		if turnID := tc.TurnIDOrTraceID(); turnID != "" {
			fields = append(fields, zap.String("turn_id", turnID))
		}
		if tc.ToolCallID != "" {
			fields = append(fields, zap.String("tool_call_id", tc.ToolCallID))
		}
	}

	logger.Info("im_api 审计", fields...)
}

func imAPITargetKind(params imAPIInput, resultKind string) string {
	if resultKind != "" {
		return resultKind
	}
	if params.ConversationID != "" {
		return "conversation"
	}
	if params.RecipientID != "" {
		return "recipient"
	}
	switch params.Action {
	case "search_recipients", "resolve_recipient":
		return "recipient"
	case "list_recent_conversations":
		return "conversation"
	default:
		return "none"
	}
}

func imAPITargetHash(params imAPIInput, resultID string) string {
	if resultID != "" {
		return imctx.SafeSenderID(resultID)
	}
	if params.ConversationID != "" {
		return imctx.SafeSenderID(params.ConversationID)
	}
	if params.RecipientID != "" {
		return imctx.SafeSenderID(params.RecipientID)
	}
	return ""
}

func jsonToolResult(v any) *mcphost.ToolResult {
	raw, _ := json.Marshal(v)
	return textResult(string(raw))
}
