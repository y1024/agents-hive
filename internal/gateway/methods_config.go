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
	Enabled   *bool                   `json:"enabled,omitempty"`
	DingTalk  *config.DingTalkConfig  `json:"dingtalk,omitempty"`
	Feishu    *config.FeishuConfig    `json:"feishu,omitempty"`
	WeCom     *config.WeComConfig     `json:"wecom,omitempty"`
	WeChatBot *config.WeChatBotConfig `json:"wechatbot,omitempty"`
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

			// 更新 HITL 配置（DB + 内存）
			if req.HITL != nil {
				if req.HITL.Enabled != nil {
					enabled := *req.HITL.Enabled
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
				if req.HITL.PermissionRules != nil {
					// 先写 DB
					if deps.Store != nil {
						rulesJSON, _ := json.Marshal(*req.HITL.PermissionRules)
						if err := deps.Store.SetConfig(ctx, "hitl.permission_rules", string(rulesJSON)); err != nil {
							zap.L().Error("持久化 hitl.permission_rules 失败", zap.Error(err))
						}
					}
					deps.Config.HITL.PermissionRules = *req.HITL.PermissionRules
					// 热更新到运行时 PermissionManager
					if deps.Master != nil {
						deps.Master.UpdatePermissionRules(*req.HITL.PermissionRules)
					}
				}
			}

			// 更新 Agent 配置（DB + 内存）
			if req.Agent != nil {
				if req.Agent.Timeout != nil {
					if d, err := parseDurationStr(*req.Agent.Timeout); err == nil {
						if deps.Store != nil {
							if err := deps.Store.SetConfig(ctx, "agent.timeout", *req.Agent.Timeout); err != nil {
								zap.L().Error("持久化 agent.timeout 失败", zap.Error(err))
							}
						}
						deps.Config.Agent.Timeout = d
					} else {
						return nil, errs.Wrap(errs.CodeInvalidArgument, "无效的超时时间格式", err)
					}
				}
				if req.Agent.ShellTimeout != nil {
					if d, err := parseDurationStr(*req.Agent.ShellTimeout); err == nil {
						if deps.Store != nil {
							if err := deps.Store.SetConfig(ctx, "agent.shell_timeout", *req.Agent.ShellTimeout); err != nil {
								zap.L().Error("持久化 agent.shell_timeout 失败", zap.Error(err))
							}
						}
						deps.Config.Agent.ShellTimeout = d
					} else {
						return nil, errs.Wrap(errs.CodeInvalidArgument, "无效的 Shell 超时时间格式", err)
					}
				}
			}

			// 更新 Channel 配置（同时写入数据库）
			// channel.enabled 父开关已改为从各通道 Enabled 状态自动推导，无需单独管理
			if req.Channel != nil {
				if req.Channel.DingTalk != nil {
					next := mergeDingTalkConfig(deps.Config.Channel.DingTalk, *req.Channel.DingTalk)
					deps.Config.Channel.DingTalk = next
					saveChannelToDB(ctx, deps.Store, "dingtalk", next)
				}
				if req.Channel.Feishu != nil {
					next := mergeFeishuConfig(deps.Config.Channel.Feishu, *req.Channel.Feishu)
					deps.Config.Channel.Feishu = next
					saveChannelToDB(ctx, deps.Store, "feishu", next)
				}
				if req.Channel.WeCom != nil {
					next := mergeWeComConfig(deps.Config.Channel.WeCom, *req.Channel.WeCom)
					deps.Config.Channel.WeCom = next
					saveChannelToDB(ctx, deps.Store, "wecom", next)
				}
				if req.Channel.WeChatBot != nil {
					next := mergeWeChatBotConfig(deps.Config.Channel.WeChatBot, *req.Channel.WeChatBot)
					deps.Config.Channel.WeChatBot = next
					saveChannelToDB(ctx, deps.Store, "wechatbot", next)
				}
			}

			// 更新 MCP 配置（同时写入数据库）
			if req.MCP != nil {
				if req.MCP.Timeout != nil {
					if d, err := parseDurationStr(*req.MCP.Timeout); err == nil {
						if deps.Store != nil {
							if err := deps.Store.SetConfig(ctx, "mcp.timeout", *req.MCP.Timeout); err != nil {
								zap.L().Error("持久化 mcp.timeout 失败", zap.Error(err))
							}
						}
						deps.Config.MCP.Timeout = d
					} else {
						return nil, errs.Wrap(errs.CodeInvalidArgument, "无效的 MCP 超时时间格式", err)
					}
				}
				if req.MCP.Servers != nil {
					if deps.Config.MCP.Servers == nil {
						deps.Config.MCP.Servers = make(map[string]config.MCPServerConfig)
					}
					for name, srv := range req.MCP.Servers {
						if srv == nil {
							// nil 表示删除该服务端
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
				}
			}

			// 更新 Security 配置（DB + 热更新运行时）
			if req.Security != nil {
				if req.Security.DefaultPolicy != nil {
					p := *req.Security.DefaultPolicy
					if p != "allow" && p != "ask" && p != "deny" {
						return nil, errs.New(errs.CodeInvalidArgument, "default_policy 必须为 allow、ask 或 deny")
					}
					if deps.Store != nil {
						if err := deps.Store.SetConfig(ctx, "security.default_policy", p); err != nil {
							zap.L().Error("持久化 security.default_policy 失败", zap.Error(err))
						}
					}
					deps.Config.Security.DefaultPolicy = p
				}
				if req.Security.ExecRules != nil {
					if deps.Store != nil {
						rulesJSON, _ := json.Marshal(*req.Security.ExecRules)
						if err := deps.Store.SetConfig(ctx, "security.exec_rules", string(rulesJSON)); err != nil {
							zap.L().Error("持久化 security.exec_rules 失败", zap.Error(err))
						}
					}
					deps.Config.Security.ExecRules = *req.Security.ExecRules
				}
				if req.Security.PermissionMode != nil {
					mode := *req.Security.PermissionMode
					if mode == "" {
						mode = "minimal"
					}
					if mode != "minimal" && mode != "strict" {
						return nil, errs.New(errs.CodeInvalidArgument, "permission_mode 必须为 minimal 或 strict")
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
				if deps.Master != nil && (req.Security.ExecRules != nil || req.Security.DefaultPolicy != nil) {
					deps.Master.UpdateSecurityConfig(deps.Config.Security.ExecRules, deps.Config.Security.DefaultPolicy)
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

	if err := db.SaveChannelConfig(ctx, &store.ChannelConfigRecord{
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

	if err := db.SaveMCPServer(ctx, &store.MCPServerRecord{
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

func mergeDingTalkConfig(existing, incoming config.DingTalkConfig) config.DingTalkConfig {
	incoming.AppSecret = mergeSecretString(existing.AppSecret, incoming.AppSecret)
	incoming.Token = mergeSecretString(existing.Token, incoming.Token)
	incoming.AESKey = mergeSecretString(existing.AESKey, incoming.AESKey)
	return incoming
}

func mergeFeishuConfig(existing, incoming config.FeishuConfig) config.FeishuConfig {
	incoming.AppSecret = mergeSecretString(existing.AppSecret, incoming.AppSecret)
	incoming.VerificationToken = mergeSecretString(existing.VerificationToken, incoming.VerificationToken)
	incoming.EncryptKey = mergeSecretString(existing.EncryptKey, incoming.EncryptKey)
	incoming.WebhookURL = mergeSecretString(existing.WebhookURL, incoming.WebhookURL)
	return incoming
}

func mergeWeComConfig(existing, incoming config.WeComConfig) config.WeComConfig {
	incoming.Secret = mergeSecretString(existing.Secret, incoming.Secret)
	incoming.Token = mergeSecretString(existing.Token, incoming.Token)
	incoming.EncodingAESKey = mergeSecretString(existing.EncodingAESKey, incoming.EncodingAESKey)
	return incoming
}

func mergeWeChatBotConfig(existing, incoming config.WeChatBotConfig) config.WeChatBotConfig {
	if incoming.BaseURL == "" {
		incoming.BaseURL = existing.BaseURL
	} else {
		incoming.BaseURL = mergeSecretString(existing.BaseURL, incoming.BaseURL)
	}
	if incoming.CredRoot == "" {
		incoming.CredRoot = existing.CredRoot
	} else {
		incoming.CredRoot = mergeSecretString(existing.CredRoot, incoming.CredRoot)
	}
	if incoming.LogLevel == "" {
		incoming.LogLevel = existing.LogLevel
	}
	return incoming
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
		return cloneStringMap(existing)
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

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
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
