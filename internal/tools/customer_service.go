package tools

import (
	"context"
	"encoding/json"
	"strings"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/cs"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/toolctx"
)

type customerServiceTool interface {
	CreateEscalation(ctx context.Context, input cs.CreateEscalationInput) (cs.Escalation, error)
	CancelEscalation(ctx context.Context, scope cs.OwnerScope, id string) (cs.Escalation, error)
}

type customerServiceToolMarker interface {
	CustomerServiceTool() customerServiceTool
}

type escalateToHumanInput struct {
	Subject  string          `json:"subject"`
	Summary  string          `json:"summary"`
	Priority string          `json:"priority,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type cancelEscalationInput struct {
	EscalationID string `json:"escalation_id"`
}

func registerCustomerServiceTools(host *mcphost.Host, logger *zap.Logger, service customerServiceTool) {
	registerEscalateToHuman(host, logger, service)
	registerCancelEscalation(host, logger, service)
}

func registerEscalateToHuman(host *mcphost.Host, logger *zap.Logger, service customerServiceTool) {
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subject":  map[string]any{"type": "string", "description": "转人工工单标题"},
			"summary":  map[string]any{"type": "string", "description": "面向人工客服的上下文摘要"},
			"priority": map[string]any{"type": "string", "description": "可选优先级，例如 low/normal/high/urgent"},
			"metadata": map[string]any{"type": "object", "description": "可选结构化上下文"},
		},
		"required": []string{"subject", "summary"},
	})
	host.RegisterTool(mcphost.ToolDefinition{
		Name:        "escalate_to_human",
		Description: "创建客服转人工请求。工具只写入客服 webhook outbox，由服务端策略控制后续投递。",
		InputSchema: schema,
	}, func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
		if service == nil {
			return errorResult("客服转人工服务未配置"), nil
		}
		var params escalateToHumanInput
		if err := json.Unmarshal(input, &params); err != nil {
			return errorResult("解析参数失败: " + err.Error()), nil
		}
		rec, err := service.CreateEscalation(ctx, cs.CreateEscalationInput{
			Scope:     customerServiceScopeFromContext(ctx),
			SessionID: toolctx.GetSessionID(ctx),
			Subject:   params.Subject,
			Summary:   params.Summary,
			Priority:  params.Priority,
			Metadata:  params.Metadata,
		})
		if err != nil {
			return errorResult("创建转人工请求失败: " + err.Error()), nil
		}
		return jsonToolResult(rec), nil
	})
	_ = logger
}

func registerCancelEscalation(host *mcphost.Host, logger *zap.Logger, service customerServiceTool) {
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"escalation_id": map[string]any{"type": "string", "description": "escalate_to_human 返回的转人工请求 ID"},
		},
		"required": []string{"escalation_id"},
	})
	host.RegisterTool(mcphost.ToolDefinition{
		Name:        "cancel_escalation",
		Description: "取消尚未完成的客服转人工请求，并通过客服 webhook outbox 通知外部系统。",
		InputSchema: schema,
	}, func(ctx context.Context, input json.RawMessage) (*mcphost.ToolResult, error) {
		if service == nil {
			return errorResult("客服转人工服务未配置"), nil
		}
		var params cancelEscalationInput
		if err := json.Unmarshal(input, &params); err != nil {
			return errorResult("解析参数失败: " + err.Error()), nil
		}
		if strings.TrimSpace(params.EscalationID) == "" {
			return errorResult("escalation_id 参数不能为空"), nil
		}
		rec, err := service.CancelEscalation(ctx, customerServiceScopeFromContext(ctx), params.EscalationID)
		if err != nil {
			return errorResult("取消转人工请求失败: " + err.Error()), nil
		}
		return jsonToolResult(rec), nil
	})
	_ = logger
}

func customerServiceScopeFromContext(ctx context.Context) cs.OwnerScope {
	runtime, _ := KBRuntimeContextFromContext(ctx)
	ownerScope := string(runtime.OwnerScope)
	if ownerScope == "" {
		ownerScope = cs.DefaultOwnerScope
	}
	ownerID := strings.TrimSpace(runtime.OwnerID)
	if ownerID == "" {
		ownerID = strings.TrimSpace(auth.UserIDFrom(ctx))
	}
	if ownerID == "" {
		ownerID = cs.DefaultOwnerID
	}
	return cs.OwnerScope{
		DomainID:   strings.TrimSpace(runtime.DomainID),
		OwnerScope: ownerScope,
		OwnerID:    ownerID,
	}.Normalized()
}
