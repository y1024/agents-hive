package gateway

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/collections"
	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/security"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/store"
)

const maskedSecretValue = security.RedactedValue

// ConfigUpdateRequest 运行时配置更新请求（白名单模式）
type ConfigUpdateRequest struct {
	HITL     *HITLUpdateRequest     `json:"hitl,omitempty"`
	Agent    *AgentUpdateRequest    `json:"agent,omitempty"`
	MCP      *MCPUpdateRequest      `json:"mcp,omitempty"`
	Channel  *ChannelUpdateRequest  `json:"channel,omitempty"`
	Security *SecurityUpdateRequest `json:"security,omitempty"`
}

// SecurityUpdateRequest 安全执行规则可更新字段
type SecurityUpdateRequest struct {
	DefaultPolicy  *string                  `json:"default_policy,omitempty"` // "allow" | "ask" | "deny"
	ExecRules      *[]config.ExecRuleConfig `json:"exec_rules,omitempty"`
	PermissionMode *string                  `json:"permission_mode,omitempty"` // "minimal" | "strict"
}

// HITLUpdateRequest HITL 相关可更新字段
type HITLUpdateRequest struct {
	Enabled         *bool                    `json:"enabled,omitempty"`
	PermissionRules *[]skills.PermissionRule `json:"permission_rules,omitempty"`
}

// AgentUpdateRequest Agent 相关可更新字段
type AgentUpdateRequest struct {
	Timeout      *string `json:"timeout,omitempty"`       // "30m" 格式
	ShellTimeout *string `json:"shell_timeout,omitempty"` // "30s" 格式
}

// ChannelUpdateRequest IM 通道相关可更新字段
type ChannelUpdateRequest struct {
	Enabled   *bool                  `json:"enabled,omitempty"`
	DingTalk  *DingTalkChannelPatch  `json:"dingtalk,omitempty"`
	Feishu    *FeishuChannelPatch    `json:"feishu,omitempty"`
	WeCom     *WeComChannelPatch     `json:"wecom,omitempty"`
	WeChatBot *WeChatBotChannelPatch `json:"wechatbot,omitempty"`
}

// DingTalkChannelPatch 钉钉通道可更新字段
type DingTalkChannelPatch struct {
	Enabled   *bool   `json:"enabled,omitempty"`
	AppKey    *string `json:"app_key,omitempty"`
	AppSecret *string `json:"app_secret,omitempty"`
	Token     *string `json:"token,omitempty"`
	AESKey    *string `json:"aes_key,omitempty"`
	AgentID   *int64  `json:"agent_id,omitempty"`
}

// FeishuChannelPatch 飞书通道可更新字段
type FeishuChannelPatch struct {
	Enabled             *bool                     `json:"enabled,omitempty"`
	AppID               *string                   `json:"app_id,omitempty"`
	AppSecret           *string                   `json:"app_secret,omitempty"`
	Region              *string                   `json:"region,omitempty"`
	VerificationToken   *string                   `json:"verification_token,omitempty"`
	EncryptKey          *string                   `json:"encrypt_key,omitempty"`
	EventEncryptEnabled *bool                     `json:"event_encrypt_enabled,omitempty"`
	IngressMode         *config.FeishuIngressMode `json:"ingress_mode,omitempty"`
	Reliability         *FeishuReliabilityPatch   `json:"reliability,omitempty"`
	LongconnEnabled     *bool                     `json:"longconn_enabled,omitempty"`
	WebhookURL          *string                   `json:"webhook_url,omitempty"`
	AckEmoji            *string                   `json:"ack_emoji,omitempty"`
	Renderer            *FeishuRendererPatch      `json:"renderer,omitempty"`
	Inbound             *FeishuInboundPatch       `json:"inbound,omitempty"`
	Governance          *FeishuGovernancePatch    `json:"governance,omitempty"`
	Identity            *FeishuIdentityPatch      `json:"identity,omitempty"`
	Outbound            *FeishuOutboundPatch      `json:"outbound,omitempty"`
	Security            *FeishuSecurityPatch      `json:"security,omitempty"`
	Push                *FeishuPushPatch          `json:"push,omitempty"`
}

// FeishuReliabilityPatch 飞书可靠性配置可更新字段
type FeishuReliabilityPatch struct {
	LongconnEnabled         *bool          `json:"longconn_enabled,omitempty"`
	LongconnGapFetchEnabled *bool          `json:"longconn_gap_fetch_enabled,omitempty"`
	HeartbeatStaleWindow    *time.Duration `json:"heartbeat_stale_window,omitempty"`
	GapFetchMaxWindow       *time.Duration `json:"gap_fetch_max_window,omitempty"`
}

// FeishuRendererPatch 飞书渲染器配置可更新字段
type FeishuRendererPatch struct {
	Disabled          *bool `json:"disabled,omitempty"`
	ThrottleMs        *int  `json:"throttle_ms,omitempty"`
	ShowAgentProgress *bool `json:"show_agent_progress,omitempty"`
}

// FeishuInboundPatch 飞书入站配置可更新字段
type FeishuInboundPatch struct {
	EnableContextResolver *bool `json:"enable_context_resolver,omitempty"`
}

// FeishuIdentityPatch 飞书身份配置可更新字段
type FeishuIdentityPatch struct {
	UserCacheSize     *int    `json:"user_cache_size,omitempty"`
	UserCacheTTLSec   *int    `json:"user_cache_ttl_sec,omitempty"`
	EnableGroupEnrich *bool   `json:"enable_group_enrich,omitempty"`
	NameLocale        *string `json:"name_locale,omitempty"`
}

// FeishuOutboundPatch 飞书出站配置可更新字段
type FeishuOutboundPatch struct {
	GlobalQPS            *int  `json:"global_qps,omitempty"`
	PerChatQPS           *int  `json:"per_chat_qps,omitempty"`
	MaxRetries           *int  `json:"max_retries,omitempty"`
	EnableBinaryTransfer *bool `json:"enable_binary_transfer,omitempty"`
}

// FeishuSecurityPatch 飞书安全配置可更新字段
type FeishuSecurityPatch struct {
	PermissionDegradeThreshold *int `json:"permission_degrade_threshold,omitempty"`
}

// FeishuPushPatch 飞书主动推送配置可更新字段
type FeishuPushPatch struct {
	Enabled           *bool `json:"enabled,omitempty"`
	PerChatPerMinute  *int  `json:"per_chat_per_minute,omitempty"`
	IdempotencyTTLSec *int  `json:"idempotency_ttl_sec,omitempty"`
}

// FeishuGovernancePatch 飞书治理配置可更新字段
type FeishuGovernancePatch struct {
	CommandACL        *FeishuCommandACLPatch `json:"command_acl,omitempty"`
	ModelAllowlist    *[]string              `json:"model_allowlist,omitempty"`
	DebugEnabled      *bool                  `json:"debug_enabled,omitempty"`
	MultiAgentEnabled *bool                  `json:"multi_agent_enabled,omitempty"`
}

// FeishuCommandACLPatch 飞书命令 ACL 配置可更新字段
type FeishuCommandACLPatch struct {
	ResetAllowlist *map[string][]string `json:"reset_allowlist,omitempty"`
}

// WeComChannelPatch 企业微信通道可更新字段
type WeComChannelPatch struct {
	Enabled        *bool   `json:"enabled,omitempty"`
	CorpID         *string `json:"corp_id,omitempty"`
	AgentID        *int    `json:"agent_id,omitempty"`
	Secret         *string `json:"secret,omitempty"`
	Token          *string `json:"token,omitempty"`
	EncodingAESKey *string `json:"encoding_aes_key,omitempty"`
}

// WeChatBotChannelPatch 个人微信通道可更新字段
type WeChatBotChannelPatch struct {
	Enabled  *bool   `json:"enabled,omitempty"`
	BaseURL  *string `json:"base_url,omitempty"`
	CredRoot *string `json:"cred_root,omitempty"`
	LogLevel *string `json:"log_level,omitempty"`
}

// MCPUpdateRequest MCP 相关可更新字段
type MCPUpdateRequest struct {
	Timeout *string                        `json:"timeout,omitempty"` // "30s" 格式
	Servers map[string]*MCPServerUpdateReq `json:"servers,omitempty"` // 键为服务端名称；值为 nil 表示删除
}

// MCPServerUpdateReq 单个 MCP 服务端更新
type MCPServerUpdateReq struct {
	Command   *string           `json:"command,omitempty"`
	Args      *[]string         `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Transport *string           `json:"transport,omitempty"`
	URL       *string           `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Timeout   *string           `json:"timeout,omitempty"`
}

// registerConfigMethods 注册配置管理相关 RPC 方法
func registerConfigMethods(gw *Gateway, deps Deps) {
	// config.save — 保存当前配置到文件
	gw.Register(MethodDef{
		Name:        "config.save",
		Description: "保存当前配置到文件",
		AuthScope:   "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			configPath := deps.ConfigPath
			if configPath == "" {
				homeDir, err := os.UserHomeDir()
				if err != nil {
					return nil, errs.Wrap(errs.CodeInternal, "无法获取用户主目录", err)
				}
				configPath = filepath.Join(homeDir, ".claw", "config.json")
				if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
					return nil, errs.Wrap(errs.CodeInternal, "创建配置目录失败", err)
				}
			}

			deps.ConfigMu.RLock()
			err := deps.Config.SaveToFile(configPath)
			deps.ConfigMu.RUnlock()

			if err != nil {
				return nil, errs.Wrap(errs.CodeInternal, "保存配置失败", err)
			}

			return json.Marshal(map[string]string{
				"status": "saved",
				"path":   configPath,
			})
		},
	})

	// config.reload — 从数据库重新加载所有运行时配置
	gw.Register(MethodDef{
		Name:        "config.reload",
		Description: "从数据库重新加载所有运行时配置",
		AuthScope:   "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			if deps.ReloadConfigFunc == nil {
				return nil, errs.New(errs.CodeInternal, "配置重载回调未注册")
			}

			// 从 DB 全量重载到内存 Config
			deps.ConfigMu.Lock()
			deps.ReloadConfigFunc()
			deps.ConfigMu.Unlock()

			// 热重载 AI 服务路由器（从 DB 重新加载 provider/model 配置）
			if deps.AIRouter != nil {
				if err := deps.AIRouter.Reload(ctx); err != nil {
					zap.L().Warn("AI 路由器热重载失败", zap.Error(err))
				}
			}

			return json.Marshal(map[string]string{
				"status": "reloaded",
			})
		},
	})

	// config.get — 读取当前配置（脱敏后返回）
	gw.Register(MethodDef{
		Name:        "config.get",
		Description: "读取当前运行时配置（API Key 等敏感字段已脱敏）",
		AuthScope:   "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			deps.ConfigMu.RLock()
			cfg := *deps.Config // 值拷贝
			deps.ConfigMu.RUnlock()

			redacted, err := redactRuntimeConfigView(cfg)
			if err != nil {
				return nil, errs.Wrap(errs.CodeInternal, "脱敏运行时配置失败", err)
			}

			return json.Marshal(redacted)
		},
	})

	// config.update — 在线修改配置并热更新到运行时（白名单模式）
	gw.Register(MethodDef{
		Name:        "config.update",
		Description: "在线修改配置并热更新到运行时",
		AuthScope:   "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			var req ConfigUpdateRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, errs.Wrap(errs.CodeInvalidArgument, "解析配置更新请求失败", err)
			}

			deps.ConfigMu.Lock()
			defer deps.ConfigMu.Unlock()

			if req.HITL != nil {
				if err := applyHITLPatch(ctx, deps, req.HITL); err != nil {
					return nil, err
				}
			}
			if req.Agent != nil {
				if err := applyAgentPatch(ctx, deps, req.Agent); err != nil {
					return nil, err
				}
			}
			if req.Channel != nil {
				if err := applyChannelPatch(ctx, deps, req.Channel); err != nil {
					return nil, err
				}
			}
			if req.MCP != nil {
				if err := applyMCPPatch(ctx, deps, req.MCP); err != nil {
					return nil, err
				}
			}
			if req.Security != nil {
				if err := applySecurityPatch(ctx, deps, req.Security); err != nil {
					return nil, err
				}
			}

			return json.Marshal(map[string]string{
				"status": "updated",
			})
		},
	})

	// channel.reload — 热重载 IM 通道插件
	gw.Register(MethodDef{
		Name:        "channel.reload",
		Description: "热重载 IM 通道插件（卸载旧插件并用新配置重建）",
		AuthScope:   "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Platform string `json:"platform"` // "dingtalk" | "feishu" | "wecom"；空=全部重载
			}
			if params != nil {
				_ = json.Unmarshal(params, &p)
			}

			if deps.ReloadChannelFunc == nil {
				return nil, errs.New(errs.CodeInternal, "IM 通道热重载回调未注册")
			}

			platforms := []string{p.Platform}
			if p.Platform == "" {
				platforms = []string{"dingtalk", "feishu", "wecom", "wechatbot"}
			}

			reloaded := make([]string, 0, len(platforms))
			for _, platform := range platforms {
				if err := deps.ReloadChannelFunc(platform); err != nil {
					return nil, errs.Wrap(errs.CodeInternal, "重载通道失败: "+platform, err)
				}
				reloaded = append(reloaded, platform)
			}

			return json.Marshal(map[string]any{
				"status":   "reloaded",
				"channels": reloaded,
			})
		},
	})

	// mcp.reload — 热重载 MCP 服务端连接
	gw.Register(MethodDef{
		Name:        "mcp.reload",
		Description: "热重载 MCP 服务端连接（关闭旧连接并用新配置重连）",
		AuthScope:   "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			var p struct {
				Name string `json:"name"` // 服务端名称；空=全部重载
			}
			if params != nil {
				_ = json.Unmarshal(params, &p)
			}

			if deps.ReloadMCPFunc == nil {
				return nil, errs.New(errs.CodeInternal, "MCP 热重载回调未注册")
			}

			if p.Name != "" {
				// 重载指定服务端
				if err := deps.ReloadMCPFunc(p.Name); err != nil {
					return nil, errs.Wrap(errs.CodeInternal, "重载 MCP 服务端失败: "+p.Name, err)
				}
				return json.Marshal(map[string]any{
					"status":  "reloaded",
					"servers": []string{p.Name},
				})
			}

			// 重载全部：从配置中读取所有 MCP 服务端名称
			deps.ConfigMu.RLock()
			serverNames := make([]string, 0, len(deps.Config.MCP.Servers))
			for name := range deps.Config.MCP.Servers {
				serverNames = append(serverNames, name)
			}
			deps.ConfigMu.RUnlock()

			reloaded := make([]string, 0, len(serverNames))
			for _, name := range serverNames {
				if err := deps.ReloadMCPFunc(name); err != nil {
					return nil, errs.Wrap(errs.CodeInternal, "重载 MCP 服务端失败: "+name, err)
				}
				reloaded = append(reloaded, name)
			}

			return json.Marshal(map[string]any{
				"status":  "reloaded",
				"servers": reloaded,
			})
		},
	})
}

func applyHITLPatch(ctx context.Context, deps Deps, patch *HITLUpdateRequest) error {
	if patch == nil {
		return nil
	}
	if patch.Enabled != nil {
		enabled := *patch.Enabled
		if deps.Store != nil {
			val := "false"
			if enabled {
				val = "true"
			}
			if err := deps.Store.SetConfig(ctx, "hitl.enabled", val); err != nil {
				zap.L().Error("持久化 hitl.enabled 失败", zap.Error(err))
			}
		}
		deps.Config.HITL.Enabled = enabled
		if deps.Master != nil {
			deps.Master.SetHITLEnabled(enabled)
		}
	}
	if patch.PermissionRules != nil {
		if deps.Store != nil {
			rulesJSON, _ := json.Marshal(*patch.PermissionRules)
			if err := deps.Store.SetConfig(ctx, "hitl.permission_rules", string(rulesJSON)); err != nil {
				zap.L().Error("持久化 hitl.permission_rules 失败", zap.Error(err))
			}
		}
		deps.Config.HITL.PermissionRules = *patch.PermissionRules
		if deps.Master != nil {
			deps.Master.UpdatePermissionRules(*patch.PermissionRules)
		}
	}
	return nil
}

func applyAgentPatch(ctx context.Context, deps Deps, patch *AgentUpdateRequest) error {
	if patch == nil {
		return nil
	}
	if patch.Timeout != nil {
		d, err := parseDurationStr(*patch.Timeout)
		if err != nil {
			return errs.Wrap(errs.CodeInvalidArgument, "无效的超时时间格式", err)
		}
		if deps.Store != nil {
			if err := deps.Store.SetConfig(ctx, "agent.timeout", *patch.Timeout); err != nil {
				zap.L().Error("持久化 agent.timeout 失败", zap.Error(err))
			}
		}
		deps.Config.Agent.Timeout = d
	}
	if patch.ShellTimeout != nil {
		d, err := parseDurationStr(*patch.ShellTimeout)
		if err != nil {
			return errs.Wrap(errs.CodeInvalidArgument, "无效的 Shell 超时时间格式", err)
		}
		if deps.Store != nil {
			if err := deps.Store.SetConfig(ctx, "agent.shell_timeout", *patch.ShellTimeout); err != nil {
				zap.L().Error("持久化 agent.shell_timeout 失败", zap.Error(err))
			}
		}
		deps.Config.Agent.ShellTimeout = d
	}
	return nil
}

func applyChannelPatch(ctx context.Context, deps Deps, patch *ChannelUpdateRequest) error {
	if patch == nil {
		return nil
	}
	if patch.DingTalk != nil {
		next := applyDingTalkChannelPatch(deps.Config.Channel.DingTalk, patch.DingTalk)
		deps.Config.Channel.DingTalk = next
		saveChannelToDB(ctx, deps.Store, "dingtalk", next)
	}
	if patch.Feishu != nil {
		next := applyFeishuChannelPatch(deps.Config.Channel.Feishu, patch.Feishu)
		deps.Config.Channel.Feishu = next
		saveChannelToDB(ctx, deps.Store, "feishu", next)
	}
	if patch.WeCom != nil {
		next := applyWeComChannelPatch(deps.Config.Channel.WeCom, patch.WeCom)
		deps.Config.Channel.WeCom = next
		saveChannelToDB(ctx, deps.Store, "wecom", next)
	}
	if patch.WeChatBot != nil {
		next := applyWeChatBotChannelPatch(deps.Config.Channel.WeChatBot, patch.WeChatBot)
		deps.Config.Channel.WeChatBot = next
		saveChannelToDB(ctx, deps.Store, "wechatbot", next)
	}
	return nil
}

func applyMCPPatch(ctx context.Context, deps Deps, patch *MCPUpdateRequest) error {
	if patch == nil {
		return nil
	}
	if patch.Timeout != nil {
		d, err := parseDurationStr(*patch.Timeout)
		if err != nil {
			return errs.Wrap(errs.CodeInvalidArgument, "无效的 MCP 超时时间格式", err)
		}
		if deps.Store != nil {
			if err := deps.Store.SetConfig(ctx, "mcp.timeout", *patch.Timeout); err != nil {
				zap.L().Error("持久化 mcp.timeout 失败", zap.Error(err))
			}
		}
		deps.Config.MCP.Timeout = d
	}
	if patch.Servers == nil {
		return nil
	}
	if deps.Config.MCP.Servers == nil {
		deps.Config.MCP.Servers = make(map[string]config.MCPServerConfig)
	}
	for name, srv := range patch.Servers {
		if srv == nil {
			delete(deps.Config.MCP.Servers, name)
			if deps.Store != nil {
				if err := deps.Store.DeleteMCPServer(ctx, name); err != nil {
					zap.L().Error("删除 MCP 服务端记录失败", zap.String("name", name), zap.Error(err))
				}
			}
			continue
		}
		existing := deps.Config.MCP.Servers[name]
		next := mergeMCPServerUpdate(existing, srv)
		deps.Config.MCP.Servers[name] = next
		zap.L().Info("收到 MCP 服务端配置更新",
			zap.String("name", name),
			zap.String("transport", next.Transport),
			zap.String("url", safeURLForLog(next.URL)),
			zap.Strings("header_keys", sortedStringMapKeys(next.Headers)),
			zap.Bool("has_x_api_key", next.Headers["X-API-Key"] != ""),
			zap.Bool("has_authorization", next.Headers["Authorization"] != ""),
		)
		saveMCPServerToDB(ctx, deps.Store, name, next)
	}
	return nil
}

func applySecurityPatch(ctx context.Context, deps Deps, patch *SecurityUpdateRequest) error {
	if patch == nil {
		return nil
	}
	if patch.DefaultPolicy != nil {
		p := *patch.DefaultPolicy
		if p != "allow" && p != "ask" && p != "deny" {
			return errs.New(errs.CodeInvalidArgument, "default_policy 必须为 allow、ask 或 deny")
		}
		if deps.Store != nil {
			if err := deps.Store.SetConfig(ctx, "security.default_policy", p); err != nil {
				zap.L().Error("持久化 security.default_policy 失败", zap.Error(err))
			}
		}
		deps.Config.Security.DefaultPolicy = p
	}
	if patch.ExecRules != nil {
		if deps.Store != nil {
			rulesJSON, _ := json.Marshal(*patch.ExecRules)
			if err := deps.Store.SetConfig(ctx, "security.exec_rules", string(rulesJSON)); err != nil {
				zap.L().Error("持久化 security.exec_rules 失败", zap.Error(err))
			}
		}
		deps.Config.Security.ExecRules = *patch.ExecRules
	}
	if patch.PermissionMode != nil {
		mode := *patch.PermissionMode
		if mode == "" {
			mode = "minimal"
		}
		if mode != "minimal" && mode != "strict" {
			return errs.New(errs.CodeInvalidArgument, "permission_mode 必须为 minimal 或 strict")
		}
		if deps.Store != nil {
			if err := deps.Store.SetConfig(ctx, "security.permission_mode", mode); err != nil {
				zap.L().Error("持久化 security.permission_mode 失败", zap.Error(err))
			}
		}
		deps.Config.Security.PermissionMode = mode
		if deps.Master != nil {
			deps.Master.UpdatePermissionMode(mode)
		}
	}
	if deps.Master != nil && (patch.ExecRules != nil || patch.DefaultPolicy != nil) {
		deps.Master.UpdateSecurityConfig(deps.Config.Security.ExecRules, deps.Config.Security.DefaultPolicy)
	}
	return nil
}

// saveChannelToDB 将 IM 通道配置写入数据库
func saveChannelToDB(ctx context.Context, db store.Store, platform string, cfg any) {
	if db == nil {
		return
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return
	}

	// 通过 JSON 反序列化获取 enabled 字段
	var enabledMap struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(data, &enabledMap)

	if err := db.UpsertChannelConfigFull(ctx, &store.ChannelConfigRecord{
		Platform:   platform,
		Enabled:    enabledMap.Enabled,
		ConfigJSON: string(data),
	}); err != nil {
		zap.L().Error("持久化 channel 配置失败", zap.String("platform", platform), zap.Error(err))
	}
}

// saveMCPServerToDB 将 MCP 服务端配置写入数据库
func saveMCPServerToDB(ctx context.Context, db store.Store, name string, srv config.MCPServerConfig) {
	if db == nil {
		return
	}
	argsJSON, _ := json.Marshal(srv.Args)
	envJSON, _ := json.Marshal(srv.Env)
	headersJSON, _ := json.Marshal(srv.Headers)

	transport := srv.Transport
	if transport == "" {
		transport = "stdio"
	}
	timeout := srv.Timeout
	if timeout == "" {
		timeout = "30s"
	}

	if err := db.UpsertMCPServerFull(ctx, &store.MCPServerRecord{
		Name:      name,
		Transport: transport,
		Command:   srv.Command,
		Args:      string(argsJSON),
		Env:       string(envJSON),
		URL:       srv.URL,
		Headers:   string(headersJSON),
		Timeout:   timeout,
		Enabled:   true,
	}); err != nil {
		zap.L().Error("持久化 MCP 服务端配置失败", zap.String("name", name), zap.Error(err))
		return
	}
	zap.L().Info("MCP 服务端配置已持久化",
		zap.String("name", name),
		zap.String("transport", transport),
		zap.String("url", safeURLForLog(srv.URL)),
		zap.Strings("header_keys", sortedStringMapKeys(srv.Headers)),
		zap.Bool("has_x_api_key", srv.Headers["X-API-Key"] != ""),
		zap.Bool("has_authorization", srv.Headers["Authorization"] != ""),
	)
}

func redactRuntimeConfigView(cfg config.Config) (map[string]any, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, err
	}
	redacted, err := security.RedactSecrets(view)
	if err != nil {
		return nil, err
	}
	out, ok := redacted.(map[string]any)
	if !ok {
		return nil, errs.New(errs.CodeInternal, "运行时配置脱敏结果类型异常")
	}
	redactGatewayTokens(out)
	return out, nil
}

func redactGatewayTokens(view map[string]any) {
	gateway, ok := view["gateway"].(map[string]any)
	if !ok {
		return
	}
	tokens, ok := gateway["tokens"].([]any)
	if !ok {
		return
	}
	for i, token := range tokens {
		if s, ok := token.(string); ok && s != "" {
			tokens[i] = maskedSecretValue
		}
	}
	gateway["tokens"] = tokens
}

func applyDingTalkChannelPatch(existing config.DingTalkConfig, patch *DingTalkChannelPatch) config.DingTalkConfig {
	next := existing
	if patch == nil {
		return next
	}
	if patch.Enabled != nil {
		next.Enabled = *patch.Enabled
	}
	if patch.AppKey != nil {
		next.AppKey = mergeSecretString(existing.AppKey, *patch.AppKey)
	}
	if patch.AppSecret != nil {
		next.AppSecret = mergeSecretString(existing.AppSecret, *patch.AppSecret)
	}
	if patch.Token != nil {
		next.Token = mergeSecretString(existing.Token, *patch.Token)
	}
	if patch.AESKey != nil {
		next.AESKey = mergeSecretString(existing.AESKey, *patch.AESKey)
	}
	if patch.AgentID != nil {
		next.AgentID = *patch.AgentID
	}
	return next
}

func applyFeishuChannelPatch(existing config.FeishuConfig, patch *FeishuChannelPatch) config.FeishuConfig {
	next := existing
	if patch == nil {
		return next
	}
	if patch.Enabled != nil {
		next.Enabled = *patch.Enabled
	}
	if patch.AppID != nil {
		next.AppID = mergeSecretString(existing.AppID, *patch.AppID)
	}
	if patch.AppSecret != nil {
		next.AppSecret = mergeSecretString(existing.AppSecret, *patch.AppSecret)
	}
	if patch.Region != nil {
		next.Region = *patch.Region
	}
	if patch.VerificationToken != nil {
		next.VerificationToken = mergeSecretString(existing.VerificationToken, *patch.VerificationToken)
	}
	if patch.EncryptKey != nil {
		next.EncryptKey = mergeSecretString(existing.EncryptKey, *patch.EncryptKey)
	}
	if patch.EventEncryptEnabled != nil {
		next.EventEncryptEnabled = *patch.EventEncryptEnabled
	}
	if patch.IngressMode != nil {
		next.IngressMode = *patch.IngressMode
	}
	if patch.Reliability != nil {
		next.Reliability = applyFeishuReliabilityPatch(next.Reliability, patch.Reliability)
	}
	if patch.LongconnEnabled != nil {
		next.LongconnEnabled = *patch.LongconnEnabled
	}
	if patch.WebhookURL != nil {
		next.WebhookURL = mergeSecretString(existing.WebhookURL, *patch.WebhookURL)
	}
	if patch.AckEmoji != nil {
		next.AckEmoji = *patch.AckEmoji
	}
	if patch.Renderer != nil {
		next.Renderer = applyFeishuRendererPatch(next.Renderer, patch.Renderer)
	}
	if patch.Inbound != nil {
		next.Inbound = applyFeishuInboundPatch(next.Inbound, patch.Inbound)
	}
	if patch.Governance != nil {
		next.Governance = applyFeishuGovernancePatch(next.Governance, patch.Governance)
	}
	if patch.Identity != nil {
		next.Identity = applyFeishuIdentityPatch(next.Identity, patch.Identity)
	}
	if patch.Outbound != nil {
		next.Outbound = applyFeishuOutboundPatch(next.Outbound, patch.Outbound)
	}
	if patch.Security != nil {
		next.Security = applyFeishuSecurityPatch(next.Security, patch.Security)
	}
	if patch.Push != nil {
		next.Push = applyFeishuPushPatch(next.Push, patch.Push)
	}
	return next
}

func applyFeishuReliabilityPatch(existing config.FeishuReliabilityConfig, patch *FeishuReliabilityPatch) config.FeishuReliabilityConfig {
	next := existing
	if patch.LongconnEnabled != nil {
		next.LongconnEnabled = *patch.LongconnEnabled
	}
	if patch.LongconnGapFetchEnabled != nil {
		next.LongconnGapFetchEnabled = *patch.LongconnGapFetchEnabled
	}
	if patch.HeartbeatStaleWindow != nil {
		next.HeartbeatStaleWindow = *patch.HeartbeatStaleWindow
	}
	if patch.GapFetchMaxWindow != nil {
		next.GapFetchMaxWindow = *patch.GapFetchMaxWindow
	}
	return next
}

func applyFeishuRendererPatch(existing config.FeishuRendererConfig, patch *FeishuRendererPatch) config.FeishuRendererConfig {
	next := existing
	if patch.Disabled != nil {
		next.Disabled = *patch.Disabled
	}
	if patch.ThrottleMs != nil {
		next.ThrottleMs = *patch.ThrottleMs
	}
	if patch.ShowAgentProgress != nil {
		next.ShowAgentProgress = *patch.ShowAgentProgress
	}
	return next
}

func applyFeishuInboundPatch(existing config.FeishuInboundConfig, patch *FeishuInboundPatch) config.FeishuInboundConfig {
	next := existing
	if patch.EnableContextResolver != nil {
		next.EnableContextResolver = patch.EnableContextResolver
	}
	return next
}

func applyFeishuGovernancePatch(existing config.FeishuGovernanceConfig, patch *FeishuGovernancePatch) config.FeishuGovernanceConfig {
	next := existing
	if patch.CommandACL != nil && patch.CommandACL.ResetAllowlist != nil {
		next.CommandACL.ResetAllowlist = *patch.CommandACL.ResetAllowlist
	}
	if patch.ModelAllowlist != nil {
		next.ModelAllowlist = append([]string(nil), (*patch.ModelAllowlist)...)
	}
	if patch.DebugEnabled != nil {
		next.DebugEnabled = *patch.DebugEnabled
	}
	if patch.MultiAgentEnabled != nil {
		next.MultiAgentEnabled = *patch.MultiAgentEnabled
	}
	return next
}

func applyFeishuIdentityPatch(existing config.FeishuIdentityConfig, patch *FeishuIdentityPatch) config.FeishuIdentityConfig {
	next := existing
	if patch.UserCacheSize != nil {
		next.UserCacheSize = *patch.UserCacheSize
	}
	if patch.UserCacheTTLSec != nil {
		next.UserCacheTTLSec = *patch.UserCacheTTLSec
	}
	if patch.EnableGroupEnrich != nil {
		next.EnableGroupEnrich = patch.EnableGroupEnrich
	}
	if patch.NameLocale != nil {
		next.NameLocale = *patch.NameLocale
	}
	return next
}

func applyFeishuOutboundPatch(existing config.FeishuOutboundConfig, patch *FeishuOutboundPatch) config.FeishuOutboundConfig {
	next := existing
	if patch.GlobalQPS != nil {
		next.GlobalQPS = *patch.GlobalQPS
	}
	if patch.PerChatQPS != nil {
		next.PerChatQPS = *patch.PerChatQPS
	}
	if patch.MaxRetries != nil {
		next.MaxRetries = *patch.MaxRetries
	}
	if patch.EnableBinaryTransfer != nil {
		next.EnableBinaryTransfer = *patch.EnableBinaryTransfer
	}
	return next
}

func applyFeishuSecurityPatch(existing config.FeishuSecurityConfig, patch *FeishuSecurityPatch) config.FeishuSecurityConfig {
	next := existing
	if patch.PermissionDegradeThreshold != nil {
		next.PermissionDegradeThreshold = *patch.PermissionDegradeThreshold
	}
	return next
}

func applyFeishuPushPatch(existing config.FeishuPushConfig, patch *FeishuPushPatch) config.FeishuPushConfig {
	next := existing
	if patch.Enabled != nil {
		next.Enabled = *patch.Enabled
	}
	if patch.PerChatPerMinute != nil {
		next.PerChatPerMinute = *patch.PerChatPerMinute
	}
	if patch.IdempotencyTTLSec != nil {
		next.IdempotencyTTLSec = *patch.IdempotencyTTLSec
	}
	return next
}

func applyWeComChannelPatch(existing config.WeComConfig, patch *WeComChannelPatch) config.WeComConfig {
	next := existing
	if patch == nil {
		return next
	}
	if patch.Enabled != nil {
		next.Enabled = *patch.Enabled
	}
	if patch.CorpID != nil {
		next.CorpID = *patch.CorpID
	}
	if patch.AgentID != nil {
		next.AgentID = *patch.AgentID
	}
	if patch.Secret != nil {
		next.Secret = mergeSecretString(existing.Secret, *patch.Secret)
	}
	if patch.Token != nil {
		next.Token = mergeSecretString(existing.Token, *patch.Token)
	}
	if patch.EncodingAESKey != nil {
		next.EncodingAESKey = mergeSecretString(existing.EncodingAESKey, *patch.EncodingAESKey)
	}
	return next
}

func applyWeChatBotChannelPatch(existing config.WeChatBotConfig, patch *WeChatBotChannelPatch) config.WeChatBotConfig {
	next := existing
	if patch == nil {
		return next
	}
	if patch.Enabled != nil {
		next.Enabled = *patch.Enabled
	}
	if patch.BaseURL != nil {
		next.BaseURL = mergeSecretString(existing.BaseURL, *patch.BaseURL)
	}
	if patch.CredRoot != nil {
		next.CredRoot = mergeSecretString(existing.CredRoot, *patch.CredRoot)
	}
	if patch.LogLevel != nil {
		next.LogLevel = *patch.LogLevel
	}
	return next
}

func mergeMCPServerUpdate(existing config.MCPServerConfig, incoming *MCPServerUpdateReq) config.MCPServerConfig {
	if incoming == nil {
		return existing
	}
	next := existing
	if incoming.Command != nil {
		next.Command = mergeSecretString(existing.Command, *incoming.Command)
	}
	if incoming.Args != nil {
		next.Args = append([]string(nil), (*incoming.Args)...)
	}
	if incoming.Env != nil {
		next.Env = mergeSecretStringMap(existing.Env, incoming.Env)
	}
	if incoming.Transport != nil {
		next.Transport = *incoming.Transport
	}
	if incoming.URL != nil {
		next.URL = mergeSecretString(existing.URL, *incoming.URL)
	}
	if incoming.Headers != nil {
		next.Headers = mergeSecretStringMap(existing.Headers, incoming.Headers)
	}
	if incoming.Timeout != nil {
		next.Timeout = *incoming.Timeout
	}
	return next
}

func mergeSecretString(existing, incoming string) string {
	if isMaskedSecretString(incoming) {
		return existing
	}
	return incoming
}

func mergeSecretStringMap(existing, incoming map[string]string) map[string]string {
	if incoming == nil {
		return collections.CloneStringMap(existing)
	}
	out := make(map[string]string, len(incoming))
	for k, v := range incoming {
		if isMaskedSecretString(v) {
			out[k] = existing[k]
			continue
		}
		out[k] = v
	}
	return out
}

func isMaskedSecretString(v string) bool {
	return security.HasRedactedMarker(v)
}

func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func safeURLForLog(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// parseDurationStr 解析时间字符串（如 "30m"、"60s"），支持 time.ParseDuration 格式
func parseDurationStr(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	return time.ParseDuration(s)
}
