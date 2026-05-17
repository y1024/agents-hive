package channel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/kb"
)

func (r *Router) handleKBCommand(ctx context.Context, msg InboundMessage, sessionID string) bool {
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return false
	}
	args := strings.Fields(content)
	if len(args) == 0 {
		return false
	}
	command := strings.ToLower(strings.TrimPrefix(args[0], "/"))
	if command != "kb" {
		return false
	}
	service := r.lookupKBService()
	if service == nil {
		r.sendKBCommandReply(ctx, msg, "KB 未启用或服务未初始化")
		return true
	}
	args = args[1:]
	if len(args) == 0 {
		r.sendKBCommandReply(ctx, msg, kbCommandHelp())
		return true
	}
	action := strings.ToLower(args[0])
	args = args[1:]
	domainID := kbCommandDomain(action, args)
	if domainID == "" {
		domainID = "generic"
	}
	scope := kb.ManagementScope{
		DomainID:   domainID,
		OwnerScope: kb.OwnerScopeUser,
		OwnerID:    ownerIDFromIMContext(ctx),
		Now:        time.Now(),
	}
	switch action {
	case "list", "ls":
		namespaces, err := service.ListNamespaces(ctx, scope, kb.ListNamespacesInput{Limit: 20})
		if err != nil {
			r.sendKBCommandReply(ctx, msg, "读取 KB namespace 失败: "+err.Error())
			return true
		}
		if len(namespaces) == 0 {
			r.sendKBCommandReply(ctx, msg, fmt.Sprintf("当前 domain=%s 没有可用 KB namespace", scope.DomainID))
			return true
		}
		lines := []string{fmt.Sprintf("可用 KB namespace (domain=%s):", scope.DomainID)}
		for _, namespace := range namespaces {
			lines = append(lines, fmt.Sprintf("- %s (%s)", namespace.Name, namespace.ID))
		}
		r.sendKBCommandReply(ctx, msg, strings.Join(lines, "\n"))
	case "use":
		if len(args) < 1 {
			r.sendKBCommandReply(ctx, msg, "用法: /kb use <namespace_id> [domain]")
			return true
		}
		namespaceID := strings.TrimSpace(args[0])
		if err := r.replaceSessionKBBindings(ctx, service, scope, sessionID, []string{namespaceID}); err != nil {
			r.sendKBCommandReply(ctx, msg, "绑定 KB 失败: "+err.Error())
			return true
		}
		r.updateKBSessionDomain(sessionID, scope.DomainID, true)
		r.sendKBCommandReply(ctx, msg, fmt.Sprintf("当前会话已绑定 KB: %s (domain=%s)", namespaceID, scope.DomainID))
	case "add":
		if len(args) < 1 {
			r.sendKBCommandReply(ctx, msg, "用法: /kb add <namespace_id> [domain]")
			return true
		}
		namespaceID := strings.TrimSpace(args[0])
		_, err := service.CreateBinding(ctx, scope, kb.CreateBindingInput{
			NamespaceID:   namespaceID,
			DomainID:      scope.DomainID,
			BindingType:   kb.BindingTypeSession,
			BindingTarget: sessionID,
			CreatedBy:     ownerIDFromIMContext(ctx),
		})
		if err != nil {
			r.sendKBCommandReply(ctx, msg, "绑定 KB 失败: "+err.Error())
			return true
		}
		r.updateKBSessionDomain(sessionID, scope.DomainID, true)
		r.sendKBCommandReply(ctx, msg, fmt.Sprintf("当前会话已追加 KB: %s (domain=%s)", namespaceID, scope.DomainID))
	case "current":
		bindings, err := service.ListBindingsForManagement(ctx, scope, kb.BindingQuery{Enabled: boolPtr(true)})
		if err != nil {
			r.sendKBCommandReply(ctx, msg, "读取当前 KB 失败: "+err.Error())
			return true
		}
		lines := []string{}
		for _, binding := range bindings {
			if binding.BindingType == kb.BindingTypeSession && binding.BindingTarget == sessionID {
				lines = append(lines, "- "+binding.NamespaceID)
			}
		}
		if len(lines) == 0 {
			r.sendKBCommandReply(ctx, msg, fmt.Sprintf("当前会话在 domain=%s 没有绑定 KB", scope.DomainID))
			return true
		}
		r.sendKBCommandReply(ctx, msg, fmt.Sprintf("当前会话 KB (domain=%s):\n%s", scope.DomainID, strings.Join(lines, "\n")))
	case "off":
		bindings, err := service.ListBindingsForManagement(ctx, scope, kb.BindingQuery{Enabled: boolPtr(true)})
		if err != nil {
			r.sendKBCommandReply(ctx, msg, "读取当前 KB 失败: "+err.Error())
			return true
		}
		disabled := 0
		for _, binding := range bindings {
			if binding.BindingType != kb.BindingTypeSession || binding.BindingTarget != sessionID {
				continue
			}
			if _, err := service.DisableBinding(ctx, scope, binding.ID); err != nil {
				r.sendKBCommandReply(ctx, msg, "关闭 KB 失败: "+err.Error())
				return true
			}
			disabled++
		}
		if disabled == 0 {
			r.sendKBCommandReply(ctx, msg, fmt.Sprintf("当前会话在 domain=%s 没有绑定 KB", scope.DomainID))
			return true
		}
		r.updateKBSessionDomain(sessionID, scope.DomainID, false)
		r.sendKBCommandReply(ctx, msg, fmt.Sprintf("已关闭当前会话 KB 绑定: %d 个 (domain=%s)", disabled, scope.DomainID))
	default:
		r.sendKBCommandReply(ctx, msg, kbCommandHelp())
	}
	return true
}

func (r *Router) replaceSessionKBBindings(ctx context.Context, service KBCommandService, scope kb.ManagementScope, sessionID string, namespaceIDs []string) error {
	bindings, err := service.ListBindingsForManagement(ctx, scope, kb.BindingQuery{Enabled: boolPtr(true)})
	if err != nil {
		return err
	}
	wanted := make(map[string]struct{}, len(namespaceIDs))
	for _, namespaceID := range uniqueKBCommandStrings(namespaceIDs) {
		wanted[namespaceID] = struct{}{}
	}
	current := make(map[string]struct{}, len(wanted))
	for _, binding := range bindings {
		if binding.BindingType != kb.BindingTypeSession || binding.BindingTarget != sessionID {
			continue
		}
		if _, ok := wanted[binding.NamespaceID]; ok {
			current[binding.NamespaceID] = struct{}{}
			continue
		}
		if _, err := service.DisableBinding(ctx, scope, binding.ID); err != nil {
			return err
		}
	}
	for namespaceID := range wanted {
		if _, ok := current[namespaceID]; ok {
			continue
		}
		if _, err := service.CreateBinding(ctx, scope, kb.CreateBindingInput{
			NamespaceID:   namespaceID,
			DomainID:      scope.DomainID,
			BindingType:   kb.BindingTypeSession,
			BindingTarget: sessionID,
			CreatedBy:     ownerIDFromIMContext(ctx),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Router) sendKBCommandReply(ctx context.Context, msg InboundMessage, content string) {
	plugin, ok := r.GetPlugin(msg.Platform)
	if !ok {
		return
	}
	_ = plugin.Send(ctx, OutboundMessage{
		Platform:    msg.Platform,
		TenantKey:   msg.TenantKey,
		OwnerUserID: msg.OwnerUserID,
		ChatID:      msg.ChatID,
		Content:     content,
		ReplyTo:     msg.MessageID,
		ReplyToken:  msg.ReplyToken,
	})
}

func kbCommandHelp() string {
	return strings.Join([]string{
		"KB 命令:",
		"/kb list [domain] - 查看可用知识库",
		"/kb use <namespace_id> [domain] - 当前会话替换为指定知识库",
		"/kb add <namespace_id> [domain] - 当前会话追加知识库",
		"/kb current [domain] - 查看当前会话知识库",
		"/kb off [domain] - 关闭当前会话知识库",
	}, "\n")
}

func kbCommandDomain(action string, args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch action {
	case "use", "add":
		if len(args) >= 2 {
			return strings.TrimSpace(args[1])
		}
	default:
		return strings.TrimSpace(args[0])
	}
	return ""
}

func uniqueKBCommandStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func ownerIDFromIMContext(ctx context.Context) string {
	if userID := strings.TrimSpace(auth.UserIDFrom(ctx)); userID != "" {
		return userID
	}
	if im, ok := IMContextFrom(ctx); ok {
		if strings.TrimSpace(im.InternalUserID) != "" {
			return strings.TrimSpace(im.InternalUserID)
		}
		if strings.TrimSpace(im.SenderOpenID) != "" {
			return strings.TrimSpace(im.SenderOpenID)
		}
	}
	return "local"
}

func boolPtr(v bool) *bool {
	return &v
}
