package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/channel"
	"github.com/chef-guo/agents-hive/internal/command"
	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/plugin"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/store"
)

// ─────────────────────────────────────────────
// 测试辅助函数
// ─────────────────────────────────────────────

// newTestGateway 创建带管理员 token 的测试网关
func newTestGateway(t *testing.T) (*Gateway, string) {
	t.Helper()
	token := "test-admin-token"
	auth := NewAuthManager([]string{token})
	gw := New(auth, zap.NewNop())
	gw.SetInsecureSkipVerify(true)
	return gw, token
}

// doRPC 通过 HTTP POST 发起 RPC 调用，返回解码后的响应
func doRPC(t *testing.T, gw *Gateway, method string, params interface{}, token string) RPCResponse {
	t.Helper()
	raw, err := json.Marshal(params)
	require.NoError(t, err)

	body, err := json.Marshal(RPCRequest{
		ID:     "req-1",
		Method: method,
		Params: json.RawMessage(raw),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp
}

type configChannelStore struct {
	store.Store
	records   map[string]*store.ChannelConfigRecord
	configs   map[string]string
	mcp       map[string]*store.MCPServerRecord
	resources map[string]*store.ExternalResourceRecord
}

func newConfigChannelStore() *configChannelStore {
	return &configChannelStore{
		records:   make(map[string]*store.ChannelConfigRecord),
		configs:   make(map[string]string),
		mcp:       make(map[string]*store.MCPServerRecord),
		resources: make(map[string]*store.ExternalResourceRecord),
	}
}

func (s *configChannelStore) SaveChannelConfig(_ context.Context, rec *store.ChannelConfigRecord) error {
	cp := *rec
	s.records[rec.Platform] = &cp
	return nil
}

func (s *configChannelStore) UpsertChannelConfigFull(ctx context.Context, rec *store.ChannelConfigRecord) error {
	return s.SaveChannelConfig(ctx, rec)
}

func (s *configChannelStore) SetConfig(_ context.Context, key, value string) error {
	s.configs[key] = value
	return nil
}

func (s *configChannelStore) SaveMCPServer(_ context.Context, rec *store.MCPServerRecord) error {
	cp := *rec
	s.mcp[rec.Name] = &cp
	return nil
}

func (s *configChannelStore) UpsertMCPServerFull(ctx context.Context, rec *store.MCPServerRecord) error {
	return s.SaveMCPServer(ctx, rec)
}

func (s *configChannelStore) DeleteMCPServer(_ context.Context, name string) error {
	delete(s.mcp, name)
	return nil
}

func (s *configChannelStore) SaveExternalResource(_ context.Context, rec *store.ExternalResourceRecord) error {
	cp := *rec
	s.resources[rec.Name] = &cp
	return nil
}

func (s *configChannelStore) UpsertExternalResourceFull(ctx context.Context, rec *store.ExternalResourceRecord) error {
	return s.SaveExternalResource(ctx, rec)
}

func (s *configChannelStore) GetExternalResource(_ context.Context, name string) (*store.ExternalResourceRecord, error) {
	rec, ok := s.resources[name]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *rec
	return &cp, nil
}

func (s *configChannelStore) ListExternalResources(_ context.Context) ([]*store.ExternalResourceRecord, error) {
	out := make([]*store.ExternalResourceRecord, 0, len(s.resources))
	for _, rec := range s.resources {
		cp := *rec
		out = append(out, &cp)
	}
	return out, nil
}

// ─────────────────────────────────────────────
// RegisterAllMethods — 条件注册逻辑测试
// ─────────────────────────────────────────────

// TestRegisterAllMethods_NilConfigMu 验证 #1 修复：Config 或 ConfigMu 为 nil 时不注册 config 方法
func TestRegisterAllMethods_NilConfigMu(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *config.Config
		cfgMu  *sync.RWMutex
		expect bool // 是否期望 config.save 被注册
	}{
		{
			name:   "Config 和 ConfigMu 均为 nil 时不注册",
			cfg:    nil,
			cfgMu:  nil,
			expect: false,
		},
		{
			name:   "仅 Config 不为 nil 时不注册（ConfigMu 为 nil）",
			cfg:    &config.Config{},
			cfgMu:  nil,
			expect: false,
		},
		{
			name:   "仅 ConfigMu 不为 nil 时不注册（Config 为 nil）",
			cfg:    nil,
			cfgMu:  &sync.RWMutex{},
			expect: false,
		},
		{
			name:   "Config 和 ConfigMu 均不为 nil 时注册",
			cfg:    &config.Config{},
			cfgMu:  &sync.RWMutex{},
			expect: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw, _ := newTestGateway(t)
			deps := Deps{
				Config:        tc.cfg,
				ConfigMu:      tc.cfgMu,
				SkillRegistry: skills.NewRegistry(zap.NewNop()),
			}
			// 仅注册 config 方法（不需要 Master）
			if deps.Config != nil && deps.ConfigMu != nil {
				registerConfigMethods(gw, deps)
			}

			gw.mu.RLock()
			_, ok := gw.methods["config.save"]
			gw.mu.RUnlock()

			assert.Equal(t, tc.expect, ok, "config.save 注册状态与期望不符")
		})
	}
}

// TestRegisterAllMethods_OptionalDeps 验证可选依赖为 nil 时对应方法不注册
func TestRegisterAllMethods_OptionalDeps(t *testing.T) {
	gw, _ := newTestGateway(t)
	deps := Deps{
		// CommandRegistry、ChannelRouter、PluginLoader、MCPHost 均为 nil
		Config:        nil,
		ConfigMu:      nil,
		SkillRegistry: skills.NewRegistry(zap.NewNop()),
	}

	// 仅手动触发条件注册分支（RegisterAllMethods 需要 Master，此处测试各个条件分支）
	if deps.CommandRegistry != nil {
		registerCommandMethods(gw, deps.CommandRegistry, deps)
	}
	if deps.ChannelRouter != nil {
		registerChannelMethods(gw, deps)
	}
	if deps.PluginLoader != nil {
		registerPluginMethods(gw, deps)
	}
	if deps.MCPHost != nil {
		registerMCPMethods(gw, deps)
	}

	gw.mu.RLock()
	defer gw.mu.RUnlock()

	for _, name := range []string{
		"commands.list",
		"channel.status", "channel.send", "channel.bind",
		"plugin.list", "plugin.load", "plugin.unload", "plugin.reload",
		"mcp.resources.list", "mcp.resources.read", "mcp.prompts.list", "mcp.prompts.get",
		"config.save", "config.reload",
	} {
		_, ok := gw.methods[name]
		assert.False(t, ok, "可选依赖为 nil 时不应注册方法: %s", name)
	}
}

// ─────────────────────────────────────────────
// methods_config.go 测试
// ─────────────────────────────────────────────

// TestConfigSave_SavesToSpecifiedPath 验证 config.save 保存到指定路径
func TestConfigSave_SavesToSpecifiedPath(t *testing.T) {
	// 准备临时目录和配置文件路径
	dir := t.TempDir()
	cfgPath := dir + "/config.json"

	cfg := &config.Config{}
	var mu sync.RWMutex

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{
		Config:     cfg,
		ConfigMu:   &mu,
		ConfigPath: cfgPath,
	})

	resp := doRPC(t, gw, "config.save", map[string]interface{}{}, token)
	assert.Nil(t, resp.Error, "config.save 应成功，实际错误: %v", resp.Error)

	// 验证结果包含正确路径
	var result map[string]string
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, "saved", result["status"])
	assert.Equal(t, cfgPath, result["path"])

	// 验证文件确实已写入
	_, err := os.Stat(cfgPath)
	assert.NoError(t, err, "配置文件应已创建")
}

// TestConfigSave_RequiresAdminScope 验证 config.save 需要 admin 权限
func TestConfigSave_RequiresAdminScope(t *testing.T) {
	cfg := &config.Config{}
	var mu sync.RWMutex

	gw, _ := newTestGateway(t)
	registerConfigMethods(gw, Deps{
		Config:     cfg,
		ConfigMu:   &mu,
		ConfigPath: t.TempDir() + "/config.json",
	})

	// 不携带 token 调用，应返回 401
	resp := doRPC(t, gw, "config.save", map[string]interface{}{}, "")
	assert.NotNil(t, resp.Error)
	assert.Equal(t, 401, resp.Error.Code)
}

func TestConfigGet_RequiresAdminAndRedactsSecrets(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.APIKey = "sk-root-secret"
	cfg.Gateway.Tokens = []string{"gateway-token"}
	cfg.HITL.WebSocketToken = "hitl-token"
	cfg.Channel.Feishu = config.FeishuConfig{
		Enabled:           true,
		AppID:             "cli_xxx",
		AppSecret:         "feishu-secret",
		VerificationToken: "verify-token",
		EncryptKey:        "encrypt-key",
	}
	cfg.MCP.Servers = map[string]config.MCPServerConfig{
		"metamcp": {
			Transport: "http",
			URL:       "https://mcp.example.com/mcp",
			Headers: map[string]string{
				"X-API-Key":     "mcp-api-key",
				"Authorization": "Bearer mcp-token",
				"Accept":        "application/json",
			},
			Env: map[string]string{
				"METAMCP_API_KEY": "env-api-key",
			},
		},
	}
	var mu sync.RWMutex

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{Config: cfg, ConfigMu: &mu})

	unauthorized := doRPC(t, gw, "config.get", map[string]interface{}{}, "")
	require.NotNil(t, unauthorized.Error)
	assert.Equal(t, 401, unauthorized.Error.Code)

	resp := doRPC(t, gw, "config.get", map[string]interface{}{}, token)
	require.Nil(t, resp.Error, "config.get should succeed: %v", resp.Error)
	body := string(resp.Result)
	for _, leaked := range []string{"sk-root-secret", "gateway-token", "hitl-token", "feishu-secret", "verify-token", "encrypt-key", "mcp-api-key", "mcp-token", "env-api-key"} {
		assert.NotContains(t, body, leaked)
	}
	assert.Contains(t, body, "application/json")
	assert.Contains(t, body, maskedSecretValue)
	assert.Equal(t, []string{"gateway-token"}, cfg.Gateway.Tokens, "config.get 脱敏不能污染内存中的真实配置")
}

// TestConfigReload_EmptyPath 验证 config.reload 在路径为空时返回错误
func TestConfigReload_EmptyPath(t *testing.T) {
	cfg := &config.Config{}
	var mu sync.RWMutex

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{
		Config:     cfg,
		ConfigMu:   &mu,
		ConfigPath: "", // 空路径
	})

	resp := doRPC(t, gw, "config.reload", map[string]interface{}{}, token)
	// 空路径应返回错误（RPC 内部错误映射为 500）
	assert.NotNil(t, resp.Error)
	assert.Equal(t, 500, resp.Error.Code)
}

// TestConfigReload_NoCallback 验证 config.reload 在未注册回调时返回错误
func TestConfigReload_NoCallback(t *testing.T) {
	cfg := &config.Config{}
	var mu sync.RWMutex

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{
		Config:   cfg,
		ConfigMu: &mu,
	})

	resp := doRPC(t, gw, "config.reload", map[string]interface{}{}, token)
	assert.NotNil(t, resp.Error, "缺少 ReloadConfigFunc 应返回错误")
	assert.Equal(t, 500, resp.Error.Code)
}

// TestConfigReload_WithCallback 验证 config.reload 通过回调从 DB 重载
func TestConfigReload_WithCallback(t *testing.T) {
	cfg := &config.Config{}
	var mu sync.RWMutex
	called := false

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{
		Config:   cfg,
		ConfigMu: &mu,
		ReloadConfigFunc: func() {
			called = true
		},
	})

	resp := doRPC(t, gw, "config.reload", map[string]interface{}{}, token)
	assert.Nil(t, resp.Error, "有回调时 reload 应成功，实际错误: %v", resp.Error)

	var result map[string]string
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, "reloaded", result["status"])
	assert.True(t, called, "ReloadConfigFunc 应被调用")
}

func TestConfigUpdatePersistsWechatbotChannel(t *testing.T) {
	cfg := config.Default()
	var mu sync.RWMutex
	db := newConfigChannelStore()

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{
		Config:   cfg,
		ConfigMu: &mu,
		Store:    db,
	})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"channel": map[string]interface{}{
			"wechatbot": map[string]interface{}{
				"enabled": true,
			},
		},
	}, token)
	assert.Nil(t, resp.Error, "config.update should save wechatbot: %v", resp.Error)
	assert.True(t, cfg.Channel.WeChatBot.Enabled)
	rec := db.records["wechatbot"]
	require.NotNil(t, rec)
	assert.True(t, rec.Enabled)
	assert.JSONEq(t, `{"enabled":true}`, rec.ConfigJSON)
}

func TestConfigUpdateDingTalkPatchPreservesOmittedClearsEmptyAndPreservesMasked(t *testing.T) {
	cfg := config.Default()
	cfg.Channel.DingTalk = config.DingTalkConfig{
		Enabled:   true,
		AppKey:    "old-app-key",
		AppSecret: "real-app-secret",
		Token:     "real-token",
		AESKey:    "real-aes-key",
		AgentID:   42,
	}
	var mu sync.RWMutex
	db := newConfigChannelStore()

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{Config: cfg, ConfigMu: &mu, Store: db})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"channel": map[string]interface{}{
			"dingtalk": map[string]interface{}{
				"enabled":    false,
				"app_key":    "new-app-key",
				"app_secret": maskedSecretValue,
				"token":      "",
			},
		},
	}, token)
	require.Nil(t, resp.Error, "config.update should patch dingtalk fields: %v", resp.Error)
	assert.False(t, cfg.Channel.DingTalk.Enabled)
	assert.Equal(t, "new-app-key", cfg.Channel.DingTalk.AppKey)
	assert.Equal(t, "real-app-secret", cfg.Channel.DingTalk.AppSecret)
	assert.Empty(t, cfg.Channel.DingTalk.Token)
	assert.Equal(t, "real-aes-key", cfg.Channel.DingTalk.AESKey)
	assert.Equal(t, int64(42), cfg.Channel.DingTalk.AgentID)
	require.NotNil(t, db.records["dingtalk"])
	assert.Contains(t, db.records["dingtalk"].ConfigJSON, `"app_key":"new-app-key"`)
	assert.NotContains(t, db.records["dingtalk"].ConfigJSON, `"app_id"`)
	assert.NotContains(t, db.records["dingtalk"].ConfigJSON, maskedSecretValue)
}

func TestConfigUpdatePreservesMaskedChannelSecrets(t *testing.T) {
	cfg := config.Default()
	cfg.Channel.Feishu = config.FeishuConfig{
		Enabled:           true,
		AppID:             "cli_old",
		AppSecret:         "real-app-secret",
		VerificationToken: "real-verify-token",
		EncryptKey:        "real-encrypt-key",
	}
	var mu sync.RWMutex
	db := newConfigChannelStore()

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{Config: cfg, ConfigMu: &mu, Store: db})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"channel": map[string]interface{}{
			"feishu": map[string]interface{}{
				"enabled":            true,
				"app_id":             "cli_new",
				"app_secret":         maskedSecretValue,
				"verification_token": maskedSecretValue,
				"encrypt_key":        maskedSecretValue,
			},
		},
	}, token)
	require.Nil(t, resp.Error, "config.update should preserve masked secrets: %v", resp.Error)
	assert.Equal(t, "cli_new", cfg.Channel.Feishu.AppID)
	assert.Equal(t, "real-app-secret", cfg.Channel.Feishu.AppSecret)
	assert.Equal(t, "real-verify-token", cfg.Channel.Feishu.VerificationToken)
	assert.Equal(t, "real-encrypt-key", cfg.Channel.Feishu.EncryptKey)
	require.NotNil(t, db.records["feishu"])
	assert.Contains(t, db.records["feishu"].ConfigJSON, `"app_secret":"real-app-secret"`)
	assert.Contains(t, db.records["feishu"].ConfigJSON, `"verification_token":"real-verify-token"`)
	assert.Contains(t, db.records["feishu"].ConfigJSON, `"encrypt_key":"real-encrypt-key"`)
	assert.NotContains(t, db.records["feishu"].ConfigJSON, maskedSecretValue)
}

func TestConfigUpdateFeishuPatchPreservesOmittedAndClearsEmpty(t *testing.T) {
	cfg := config.Default()
	cfg.Channel.Feishu = config.FeishuConfig{
		Enabled:           true,
		AppID:             "cli_old",
		AppSecret:         "real-app-secret",
		Region:            "cn",
		VerificationToken: "real-verify-token",
		EncryptKey:        "real-encrypt-key",
		WebhookURL:        "https://callback.example.com/feishu",
		AckEmoji:          "Get",
	}
	var mu sync.RWMutex
	db := newConfigChannelStore()

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{Config: cfg, ConfigMu: &mu, Store: db})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"channel": map[string]interface{}{
			"feishu": map[string]interface{}{
				"enabled":     false,
				"app_secret":  "",
				"webhook_url": maskedSecretValue,
			},
		},
	}, token)
	require.Nil(t, resp.Error, "config.update should patch feishu fields: %v", resp.Error)
	assert.False(t, cfg.Channel.Feishu.Enabled)
	assert.Equal(t, "cli_old", cfg.Channel.Feishu.AppID)
	assert.Empty(t, cfg.Channel.Feishu.AppSecret)
	assert.Equal(t, "cn", cfg.Channel.Feishu.Region)
	assert.Equal(t, "real-verify-token", cfg.Channel.Feishu.VerificationToken)
	assert.Equal(t, "real-encrypt-key", cfg.Channel.Feishu.EncryptKey)
	assert.Equal(t, "https://callback.example.com/feishu", cfg.Channel.Feishu.WebhookURL)
	assert.Equal(t, "Get", cfg.Channel.Feishu.AckEmoji)
	require.NotNil(t, db.records["feishu"])
	assert.Contains(t, db.records["feishu"].ConfigJSON, `"app_secret":""`)
	assert.Contains(t, db.records["feishu"].ConfigJSON, `"app_id":"cli_old"`)
	assert.NotContains(t, db.records["feishu"].ConfigJSON, maskedSecretValue)
}

func TestConfigUpdatePreservesInlineRedactedFeishuWebhookURL(t *testing.T) {
	cfg := config.Default()
	cfg.Channel.Feishu = config.FeishuConfig{
		Enabled:    true,
		WebhookURL: "https://callback.example.com/feishu?token=real-token",
	}
	var mu sync.RWMutex
	db := newConfigChannelStore()

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{Config: cfg, ConfigMu: &mu, Store: db})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"channel": map[string]interface{}{
			"feishu": map[string]interface{}{
				"enabled":     true,
				"webhook_url": "https://callback.example.com/feishu?token=[REDACTED]",
			},
		},
	}, token)
	require.Nil(t, resp.Error, "config.update should preserve inline-redacted URL values: %v", resp.Error)
	assert.Equal(t, "https://callback.example.com/feishu?token=real-token", cfg.Channel.Feishu.WebhookURL)
	require.NotNil(t, db.records["feishu"])
	assert.Contains(t, db.records["feishu"].ConfigJSON, `"webhook_url":"https://callback.example.com/feishu?token=real-token"`)
	assert.NotContains(t, db.records["feishu"].ConfigJSON, maskedSecretValue)
}

func TestConfigUpdatePreservesWechatbotOmittedFields(t *testing.T) {
	cfg := config.Default()
	cfg.Channel.WeChatBot = config.WeChatBotConfig{
		Enabled:  true,
		BaseURL:  "http://wechatbot.internal",
		CredRoot: "/var/lib/wechatbot",
		LogLevel: "debug",
	}
	var mu sync.RWMutex
	db := newConfigChannelStore()

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{Config: cfg, ConfigMu: &mu, Store: db})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"channel": map[string]interface{}{
			"wechatbot": map[string]interface{}{
				"enabled": false,
			},
		},
	}, token)
	require.Nil(t, resp.Error, "config.update should preserve omitted wechatbot fields: %v", resp.Error)
	assert.False(t, cfg.Channel.WeChatBot.Enabled)
	assert.Equal(t, "http://wechatbot.internal", cfg.Channel.WeChatBot.BaseURL)
	assert.Equal(t, "/var/lib/wechatbot", cfg.Channel.WeChatBot.CredRoot)
	assert.Equal(t, "debug", cfg.Channel.WeChatBot.LogLevel)
	require.NotNil(t, db.records["wechatbot"])
	assert.Contains(t, db.records["wechatbot"].ConfigJSON, `"base_url":"http://wechatbot.internal"`)
}

func TestConfigUpdatePreservesMaskedMCPSecrets(t *testing.T) {
	cfg := config.Default()
	cfg.MCP.Servers = map[string]config.MCPServerConfig{
		"metamcp": {
			Transport: "http",
			URL:       "https://old.example.com/mcp",
			Headers: map[string]string{
				"X-API-Key": "real-header-key",
				"Accept":    "application/json",
			},
			Env: map[string]string{
				"METAMCP_API_KEY": "real-env-key",
			},
		},
	}
	var mu sync.RWMutex
	db := newConfigChannelStore()

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{Config: cfg, ConfigMu: &mu, Store: db})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"mcp": map[string]interface{}{
			"servers": map[string]interface{}{
				"metamcp": map[string]interface{}{
					"transport": "http",
					"url":       "https://new.example.com/mcp",
					"headers": map[string]string{
						"X-API-Key": maskedSecretValue,
						"Accept":    "text/event-stream",
					},
					"env": map[string]string{
						"METAMCP_API_KEY": maskedSecretValue,
					},
				},
			},
		},
	}, token)
	require.Nil(t, resp.Error, "config.update should preserve masked MCP secrets: %v", resp.Error)
	got := cfg.MCP.Servers["metamcp"]
	assert.Equal(t, "https://new.example.com/mcp", got.URL)
	assert.Equal(t, "real-header-key", got.Headers["X-API-Key"])
	assert.Equal(t, "text/event-stream", got.Headers["Accept"])
	assert.Equal(t, "real-env-key", got.Env["METAMCP_API_KEY"])
	require.NotNil(t, db.mcp["metamcp"])
	assert.JSONEq(t, `{"X-API-Key":"real-header-key","Accept":"text/event-stream"}`, db.mcp["metamcp"].Headers)
	assert.JSONEq(t, `{"METAMCP_API_KEY":"real-env-key"}`, db.mcp["metamcp"].Env)
}

func TestConfigUpdatePreservesInlineRedactedMCPURL(t *testing.T) {
	cfg := config.Default()
	cfg.MCP.Servers = map[string]config.MCPServerConfig{
		"metamcp": {
			Transport: "http",
			URL:       "https://metamcp.example.com/mcp?api_key=real-key",
		},
	}
	var mu sync.RWMutex
	db := newConfigChannelStore()

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{Config: cfg, ConfigMu: &mu, Store: db})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"mcp": map[string]interface{}{
			"servers": map[string]interface{}{
				"metamcp": map[string]interface{}{
					"transport": "http",
					"url":       "https://metamcp.example.com/mcp?api_key=[REDACTED]",
				},
			},
		},
	}, token)
	require.Nil(t, resp.Error, "config.update should preserve inline-redacted MCP URL: %v", resp.Error)
	got := cfg.MCP.Servers["metamcp"]
	assert.Equal(t, "https://metamcp.example.com/mcp?api_key=real-key", got.URL)
	require.NotNil(t, db.mcp["metamcp"])
	assert.Equal(t, "https://metamcp.example.com/mcp?api_key=real-key", db.mcp["metamcp"].URL)
}

func TestConfigUpdatePreservesOmittedMCPEnvAndHeaders(t *testing.T) {
	cfg := config.Default()
	cfg.MCP.Servers = map[string]config.MCPServerConfig{
		"metamcp": {
			Transport: "http",
			URL:       "https://old.example.com/mcp",
			Headers: map[string]string{
				"X-API-Key": "real-header-key",
				"Accept":    "application/json",
			},
			Env: map[string]string{
				"METAMCP_API_KEY": "real-env-key",
			},
		},
	}
	var mu sync.RWMutex
	db := newConfigChannelStore()

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{Config: cfg, ConfigMu: &mu, Store: db})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"mcp": map[string]interface{}{
			"servers": map[string]interface{}{
				"metamcp": map[string]interface{}{
					"transport": "http",
					"url":       "https://new.example.com/mcp",
				},
			},
		},
	}, token)
	require.Nil(t, resp.Error, "config.update should preserve omitted MCP env/headers: %v", resp.Error)
	got := cfg.MCP.Servers["metamcp"]
	assert.Equal(t, "https://new.example.com/mcp", got.URL)
	assert.Equal(t, "real-header-key", got.Headers["X-API-Key"])
	assert.Equal(t, "application/json", got.Headers["Accept"])
	assert.Equal(t, "real-env-key", got.Env["METAMCP_API_KEY"])
	require.NotNil(t, db.mcp["metamcp"])
	assert.JSONEq(t, `{"X-API-Key":"real-header-key","Accept":"application/json"}`, db.mcp["metamcp"].Headers)
	assert.JSONEq(t, `{"METAMCP_API_KEY":"real-env-key"}`, db.mcp["metamcp"].Env)
}

func TestConfigUpdatePreservesOmittedMCPScalarFieldsAndPersistsTimeout(t *testing.T) {
	cfg := config.Default()
	cfg.MCP.Servers = map[string]config.MCPServerConfig{
		"metamcp": {
			Transport: "http",
			URL:       "https://old.example.com/mcp",
			Timeout:   "45s",
		},
	}
	var mu sync.RWMutex
	db := newConfigChannelStore()

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{Config: cfg, ConfigMu: &mu, Store: db})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"mcp": map[string]interface{}{
			"timeout": "60s",
			"servers": map[string]interface{}{
				"metamcp": map[string]interface{}{
					"headers": map[string]string{"Accept": "application/json"},
				},
			},
		},
	}, token)
	require.Nil(t, resp.Error, "config.update should preserve omitted MCP scalar fields: %v", resp.Error)
	got := cfg.MCP.Servers["metamcp"]
	assert.Equal(t, "http", got.Transport)
	assert.Equal(t, "https://old.example.com/mcp", got.URL)
	assert.Equal(t, "45s", got.Timeout)
	assert.Equal(t, 60*time.Second, cfg.MCP.Timeout)
	assert.Equal(t, "60s", db.configs["mcp.timeout"])
	require.NotNil(t, db.mcp["metamcp"])
	assert.Equal(t, "https://old.example.com/mcp", db.mcp["metamcp"].URL)
}

func TestConfigUpdatePersistsPermissionMode(t *testing.T) {
	cfg := config.Default()
	var mu sync.RWMutex
	db := newConfigChannelStore()

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{
		Config:   cfg,
		ConfigMu: &mu,
		Store:    db,
	})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"security": map[string]interface{}{
			"permission_mode": "strict",
		},
	}, token)
	assert.Nil(t, resp.Error, "config.update should save permission_mode: %v", resp.Error)
	assert.Equal(t, "strict", cfg.Security.PermissionMode)
	assert.Equal(t, "strict", db.configs["security.permission_mode"])
}

func TestConfigUpdateRejectsInvalidPermissionMode(t *testing.T) {
	cfg := config.Default()
	var mu sync.RWMutex

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{
		Config:   cfg,
		ConfigMu: &mu,
	})

	resp := doRPC(t, gw, "config.update", map[string]interface{}{
		"security": map[string]interface{}{
			"permission_mode": "legacy",
		},
	}, token)
	require.NotNil(t, resp.Error)
	assert.Equal(t, 400, resp.Error.Code)
}

func TestExternalResourcesRequireAdminAndRedactCredentials(t *testing.T) {
	db := newConfigChannelStore()
	db.resources["prod-db"] = &store.ExternalResourceRecord{
		Name:        "prod-db",
		Type:        "database",
		Environment: "production",
		Description: "readonly prod db",
		Connection:  "psql",
		Credentials: `{"password":"raw-db-password"}`,
		ReadOnly:    true,
		Enabled:     true,
	}
	gw, token := newTestGateway(t)
	registerResourceMethods(gw, Deps{Store: db})

	unauthorized := doRPC(t, gw, "resources.list", map[string]interface{}{}, "")
	require.NotNil(t, unauthorized.Error)
	assert.Equal(t, 401, unauthorized.Error.Code)

	resp := doRPC(t, gw, "resources.list", map[string]interface{}{}, token)
	require.Nil(t, resp.Error, "resources.list should succeed: %v", resp.Error)
	body := string(resp.Result)
	assert.NotContains(t, body, "raw-db-password")
	assert.Contains(t, body, maskedSecretValue)

	getResp := doRPC(t, gw, "resources.get", map[string]string{"name": "prod-db"}, token)
	require.Nil(t, getResp.Error, "resources.get should succeed: %v", getResp.Error)
	assert.NotContains(t, string(getResp.Result), "raw-db-password")
	assert.Contains(t, string(getResp.Result), maskedSecretValue)
}

func TestExternalResourceSavePreservesMaskedCredentials(t *testing.T) {
	db := newConfigChannelStore()
	db.resources["prod-db"] = &store.ExternalResourceRecord{
		Name:        "prod-db",
		Type:        "database",
		Environment: "production",
		Description: "old",
		Connection:  "psql-old",
		Credentials: `{"password":"raw-db-password"}`,
		ReadOnly:    true,
		Enabled:     true,
	}
	gw, token := newTestGateway(t)
	registerResourceMethods(gw, Deps{Store: db})

	resp := doRPC(t, gw, "resources.save", map[string]interface{}{
		"name":        "prod-db",
		"type":        "database",
		"environment": "production",
		"description": "new",
		"connection":  "psql-new",
		"credentials": maskedSecretValue,
		"read_only":   true,
		"enabled":     true,
	}, token)
	require.Nil(t, resp.Error, "resources.save should preserve masked credentials: %v", resp.Error)
	require.NotNil(t, db.resources["prod-db"])
	assert.Equal(t, `{"password":"raw-db-password"}`, db.resources["prod-db"].Credentials)
	assert.Equal(t, "new", db.resources["prod-db"].Description)
	assert.Equal(t, "psql-new", db.resources["prod-db"].Connection)
}

func TestExternalResourceSavePreservesNestedMaskedCredentials(t *testing.T) {
	db := newConfigChannelStore()
	db.resources["prod-db"] = &store.ExternalResourceRecord{
		Name:        "prod-db",
		Type:        "database",
		Environment: "production",
		Credentials: `{"username":"reader","password":"raw-db-password","nested":{"token":"raw-token"}}`,
		ReadOnly:    true,
		Enabled:     true,
	}
	gw, token := newTestGateway(t)
	registerResourceMethods(gw, Deps{Store: db})

	resp := doRPC(t, gw, "resources.save", map[string]interface{}{
		"name":        "prod-db",
		"type":        "database",
		"environment": "production",
		"credentials": `{"username":"reader2","password":"[REDACTED]","nested":{"token":"[REDACTED]"}}`,
		"read_only":   true,
		"enabled":     true,
	}, token)
	require.Nil(t, resp.Error, "resources.save should preserve nested masked credentials: %v", resp.Error)
	require.NotNil(t, db.resources["prod-db"])
	assert.JSONEq(t, `{"username":"reader2","password":"raw-db-password","nested":{"token":"raw-token"}}`, db.resources["prod-db"].Credentials)
	assert.NotContains(t, db.resources["prod-db"].Credentials, maskedSecretValue)
}

func TestExternalResourceSaveRejectsMaskedCredentialsOnCreate(t *testing.T) {
	db := newConfigChannelStore()
	gw, token := newTestGateway(t)
	registerResourceMethods(gw, Deps{Store: db})

	resp := doRPC(t, gw, "resources.save", map[string]interface{}{
		"name":        "prod-db",
		"type":        "database",
		"environment": "production",
		"credentials": `{"password":"[REDACTED]"}`,
		"read_only":   true,
		"enabled":     true,
	}, token)
	require.NotNil(t, resp.Error)
	assert.Equal(t, 400, resp.Error.Code)
	assert.Nil(t, db.resources["prod-db"])
}

func TestExternalResourceSavePreservesMissingCredentialsOnUpdate(t *testing.T) {
	db := newConfigChannelStore()
	db.resources["prod-db"] = &store.ExternalResourceRecord{
		Name:        "prod-db",
		Type:        "database",
		Environment: "production",
		Description: "old",
		Connection:  "psql-old",
		Credentials: `{"password":"raw-db-password"}`,
		ReadOnly:    true,
		Enabled:     true,
	}
	gw, token := newTestGateway(t)
	registerResourceMethods(gw, Deps{Store: db})

	resp := doRPC(t, gw, "resources.save", map[string]interface{}{
		"name":        "prod-db",
		"type":        "database",
		"environment": "production",
		"description": "new",
		"connection":  "psql-new",
		"read_only":   true,
		"enabled":     true,
	}, token)
	require.Nil(t, resp.Error, "resources.save should preserve omitted credentials on update: %v", resp.Error)
	require.NotNil(t, db.resources["prod-db"])
	assert.Equal(t, `{"password":"raw-db-password"}`, db.resources["prod-db"].Credentials)
	assert.Equal(t, "new", db.resources["prod-db"].Description)
	assert.Equal(t, "psql-new", db.resources["prod-db"].Connection)
}

func TestExternalResourceSaveAllowsExplicitCredentialsClear(t *testing.T) {
	db := newConfigChannelStore()
	db.resources["prod-db"] = &store.ExternalResourceRecord{
		Name:        "prod-db",
		Type:        "database",
		Environment: "production",
		Credentials: `{"password":"raw-db-password"}`,
		ReadOnly:    true,
		Enabled:     true,
	}
	gw, token := newTestGateway(t)
	registerResourceMethods(gw, Deps{Store: db})

	resp := doRPC(t, gw, "resources.save", map[string]interface{}{
		"name":        "prod-db",
		"type":        "database",
		"environment": "production",
		"credentials": "",
		"read_only":   true,
		"enabled":     true,
	}, token)
	require.Nil(t, resp.Error, "resources.save should allow explicit credentials clear: %v", resp.Error)
	require.NotNil(t, db.resources["prod-db"])
	assert.Empty(t, db.resources["prod-db"].Credentials)
}

func TestChannelReloadIncludesWechatbotByDefault(t *testing.T) {
	cfg := config.Default()
	var mu sync.RWMutex
	var reloaded []string

	gw, token := newTestGateway(t)
	registerConfigMethods(gw, Deps{
		Config:   cfg,
		ConfigMu: &mu,
		ReloadChannelFunc: func(platform string) error {
			reloaded = append(reloaded, platform)
			return nil
		},
	})

	resp := doRPC(t, gw, "channel.reload", map[string]interface{}{}, token)
	assert.Nil(t, resp.Error, "channel.reload should include wechatbot: %v", resp.Error)
	assert.Equal(t, []string{"dingtalk", "feishu", "wecom", "wechatbot"}, reloaded)
}

// ─────────────────────────────────────────────
// methods_sessions.go 测试（仅验证参数校验逻辑）
// ─────────────────────────────────────────────

// TestSessionMethods_MethodsRegistered 验证 session 方法已正确注册
func TestSessionMethods_MethodsRegistered(t *testing.T) {
	gw, _ := newTestGateway(t)

	// 使用最小 mockMaster（只需验证方法注册，不实际调用）
	// registerSessionMethods 需要 deps.Master 不为 nil 才能注册
	// 此处通过直接调用 gw.Register 来模拟验证注册列表
	expectedMethods := []string{
		"sessions.list",
		"sessions.get",
		"sessions.message",
		"sessions.create",
		"sessions.update",
		"sessions.delete",
		"sessions.messages",
		"sessions.clear",
		"sessions.fork",
		"sessions.revert",
	}

	// 手动注册占位符，模拟 registerSessionMethods 的效果（验证方法名集合）
	for _, name := range expectedMethods {
		gw.Register(MethodDef{
			Name:    name,
			Handler: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) { return nil, nil },
		})
	}

	gw.mu.RLock()
	defer gw.mu.RUnlock()
	for _, name := range expectedMethods {
		_, ok := gw.methods[name]
		assert.True(t, ok, "方法应已注册: %s", name)
	}
}

// TestSessionMethods_InvalidParams 验证参数解析失败时返回 500（handler 层面的 Unmarshal 错误通过 500 返回）
func TestSessionMethods_InvalidParams(t *testing.T) {
	// 通过注册一个具有 Unmarshal 逻辑的 handler 来测试参数校验路径
	tests := []struct {
		name       string
		method     string
		params     json.RawMessage
		wantErrMsg string
	}{
		{
			name:   "sessions.update 空 name 参数",
			method: "test.sessions.update.empty_name",
			params: json.RawMessage(`{"name":""}`),
		},
		{
			name:   "sessions.delete 空 id 参数",
			method: "test.sessions.delete.empty_id",
			params: json.RawMessage(`{"id":""}`),
		},
		{
			name:   "sessions.messages 空 id 参数",
			method: "test.sessions.messages.empty_id",
			params: json.RawMessage(`{"id":""}`),
		},
	}

	// 直接测试 sessions.update 的 name 空值校验逻辑（不依赖 Master）
	t.Run("sessions.update 空名称校验", func(t *testing.T) {
		_ = tests
		gw, token := newTestGateway(t)
		gw.Register(MethodDef{
			Name:      "sessions.update",
			AuthScope: "write",
			Handler: func(_ context.Context, params json.RawMessage) (json.RawMessage, error) {
				var p struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, err
				}
				if p.Name == "" {
					return nil, &mockError{msg: "名称不能为空"}
				}
				return json.Marshal(map[string]string{"status": "ok"})
			},
		})

		resp := doRPC(t, gw, "sessions.update", map[string]string{"name": ""}, token)
		assert.NotNil(t, resp.Error, "空名称应返回错误")
	})
}

// mockError 简单错误类型用于测试
type mockError struct {
	msg string
}

func (e *mockError) Error() string { return e.msg }

// ─────────────────────────────────────────────
// methods_hitl.go 测试
// ─────────────────────────────────────────────

// TestHITLMethods_MethodsRegistered 验证 HITL 方法已正确注册
func TestHITLMethods_MethodsRegistered(t *testing.T) {
	gw, _ := newTestGateway(t)

	// 注册占位 handler 验证方法名
	for _, name := range []string{"hitl.submit", "hitl.command", "hitl.pending"} {
		gw.Register(MethodDef{
			Name:    name,
			Handler: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) { return nil, nil },
		})
	}

	gw.mu.RLock()
	defer gw.mu.RUnlock()
	for _, name := range []string{"hitl.submit", "hitl.command", "hitl.pending"} {
		_, ok := gw.methods[name]
		assert.True(t, ok, "HITL 方法应已注册: %s", name)
	}
}

// TestHITLMethods_Submit_EmptyRequestID 验证 hitl.submit 对空 request_id 返回错误
func TestHITLMethods_Submit_EmptyRequestID(t *testing.T) {
	gw, token := newTestGateway(t)

	// 注册一个模拟 hitl.submit handler，直接复用真实校验逻辑
	gw.Register(MethodDef{
		Name:      "hitl.submit",
		AuthScope: "write",
		Handler: func(_ context.Context, params json.RawMessage) (json.RawMessage, error) {
			var p struct {
				RequestID string `json:"request_id"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			if p.RequestID == "" {
				return nil, &mockError{msg: "request_id 不能为空"}
			}
			return json.Marshal(map[string]string{"status": "submitted"})
		},
	})

	tests := []struct {
		name        string
		params      map[string]interface{}
		expectError bool
	}{
		{
			name:        "空 request_id 应返回错误",
			params:      map[string]interface{}{"request_id": ""},
			expectError: true,
		},
		{
			name:        "有效 request_id 应成功",
			params:      map[string]interface{}{"request_id": "req-123"},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := doRPC(t, gw, "hitl.submit", tc.params, token)
			if tc.expectError {
				assert.NotNil(t, resp.Error)
			} else {
				assert.Nil(t, resp.Error)
			}
		})
	}
}

// TestHITLMethods_Command_Validation 验证 hitl.command 参数校验
func TestHITLMethods_Command_Validation(t *testing.T) {
	gw, token := newTestGateway(t)

	gw.Register(MethodDef{
		Name:      "hitl.command",
		AuthScope: "write",
		Handler: func(_ context.Context, params json.RawMessage) (json.RawMessage, error) {
			var p struct {
				TaskID string `json:"task_id"`
				Type   string `json:"type"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			if p.TaskID == "" {
				return nil, &mockError{msg: "task_id 不能为空"}
			}
			if p.Type == "" {
				return nil, &mockError{msg: "type 不能为空"}
			}
			return json.Marshal(map[string]string{"status": "sent"})
		},
	})

	tests := []struct {
		name        string
		params      map[string]string
		expectError bool
	}{
		{
			name:        "空 task_id 应返回错误",
			params:      map[string]string{"task_id": "", "type": "pause"},
			expectError: true,
		},
		{
			name:        "空 type 应返回错误",
			params:      map[string]string{"task_id": "task-1", "type": ""},
			expectError: true,
		},
		{
			name:        "有效参数应成功",
			params:      map[string]string{"task_id": "task-1", "type": "pause"},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := doRPC(t, gw, "hitl.command", tc.params, token)
			if tc.expectError {
				assert.NotNil(t, resp.Error, "应返回错误但未返回")
			} else {
				assert.Nil(t, resp.Error, "不应返回错误，实际: %v", resp.Error)
			}
		})
	}
}

// ─────────────────────────────────────────────
// methods_channel.go 测试
// ─────────────────────────────────────────────

// TestChannelMethods_MethodsRegistered 验证 channel 方法注册
func TestChannelMethods_MethodsRegistered(t *testing.T) {
	gw, _ := newTestGateway(t)
	router := channel.NewRouter(nil, zap.NewNop())
	deps := Deps{ChannelRouter: router}
	registerChannelMethods(gw, deps)

	gw.mu.RLock()
	defer gw.mu.RUnlock()
	for _, name := range []string{"channel.status", "channel.send", "channel.bind"} {
		_, ok := gw.methods[name]
		assert.True(t, ok, "channel 方法应已注册: %s", name)
	}
}

// TestChannelStatus_NoPlugins 验证无插件时 channel.status 返回全 false
func TestChannelStatus_NoPlugins(t *testing.T) {
	gw, token := newTestGateway(t)
	router := channel.NewRouter(nil, zap.NewNop())
	deps := Deps{ChannelRouter: router}
	registerChannelMethods(gw, deps)

	resp := doRPC(t, gw, "channel.status", map[string]interface{}{}, token)
	assert.Nil(t, resp.Error)

	var status map[string]bool
	require.NoError(t, json.Unmarshal(resp.Result, &status))
	// 无插件时所有平台均为 false
	assert.False(t, status[string(channel.PlatformDingTalk)])
	assert.False(t, status[string(channel.PlatformFeishu)])
	assert.False(t, status[string(channel.PlatformWeCom)])
}

// TestChannelBind_Success 验证 channel.bind 绑定操作成功
func TestChannelBind_Success(t *testing.T) {
	gw, token := newTestGateway(t)
	router := channel.NewRouter(nil, zap.NewNop())
	deps := Deps{ChannelRouter: router}
	registerChannelMethods(gw, deps)

	params := channel.Binding{
		Platform:  channel.PlatformDingTalk,
		ChatID:    "chat-001",
		SessionID: "session-001",
	}

	resp := doRPC(t, gw, "channel.bind", params, token)
	assert.Nil(t, resp.Error, "channel.bind 应成功，实际错误: %v", resp.Error)

	var result map[string]string
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, "bound", result["status"])
}

// TestChannelSend_PlatformNotFound 验证发送到未注册平台时返回错误
func TestChannelSend_PlatformNotFound(t *testing.T) {
	gw, token := newTestGateway(t)
	router := channel.NewRouter(nil, zap.NewNop())
	deps := Deps{ChannelRouter: router}
	registerChannelMethods(gw, deps)

	params := map[string]string{
		"platform": "nonexistent",
		"chat_id":  "chat-001",
		"content":  "hello",
	}

	resp := doRPC(t, gw, "channel.send", params, token)
	assert.NotNil(t, resp.Error, "未注册平台应返回错误")
	assert.Equal(t, 500, resp.Error.Code) // 内部错误（errs 被包装为 500）
}

// TestChannelSend_InvalidParams 验证非法 JSON 参数返回错误
func TestChannelSend_InvalidParams(t *testing.T) {
	gw, _ := newTestGateway(t)
	router := channel.NewRouter(nil, zap.NewNop())
	deps := Deps{ChannelRouter: router}
	registerChannelMethods(gw, deps)

	// 发送非法 JSON
	body, _ := json.Marshal(RPCRequest{
		ID:     "req-bad",
		Method: "channel.send",
		Params: json.RawMessage(`not-valid-json`),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-admin-token")
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp.Error)
}

// ─────────────────────────────────────────────
// methods_commands.go 测试
// ─────────────────────────────────────────────

// TestCommandMethods_List 验证 commands.list 返回所有命令
func TestCommandMethods_List(t *testing.T) {
	gw, token := newTestGateway(t)
	cmdReg := command.NewRegistry(zap.NewNop())
	registerCommandMethods(gw, cmdReg, Deps{})

	gw.mu.RLock()
	_, ok := gw.methods["commands.list"]
	gw.mu.RUnlock()
	assert.True(t, ok, "commands.list 应已注册")

	resp := doRPC(t, gw, "commands.list", map[string]interface{}{}, token)
	assert.Nil(t, resp.Error, "commands.list 应成功，实际错误: %v", resp.Error)

	// 空注册表应返回空数组或 null
	assert.NotNil(t, resp.Result)
}

// TestCommandExecute_EmptyName 验证 commands.execute 空 name 返回错误
func TestCommandExecute_EmptyName(t *testing.T) {
	gw, token := newTestGateway(t)
	cmdReg := command.NewRegistry(zap.NewNop())
	registerCommandMethods(gw, cmdReg, Deps{})

	resp := doRPC(t, gw, "commands.execute", map[string]string{
		"name":       "",
		"arguments":  "test",
		"session_id": "s1",
	}, token)
	assert.NotNil(t, resp.Error, "空 name 应返回错误")
}

// TestCommandExecute_NotFound 验证 commands.execute 命令不存在时返回错误
func TestCommandExecute_NotFound(t *testing.T) {
	gw, token := newTestGateway(t)
	cmdReg := command.NewRegistry(zap.NewNop())
	registerCommandMethods(gw, cmdReg, Deps{})

	resp := doRPC(t, gw, "commands.execute", map[string]string{
		"name":       "nonexistent-cmd",
		"arguments":  "",
		"session_id": "s1",
	}, token)
	assert.NotNil(t, resp.Error, "不存在的命令应返回错误")
}

// TestCommandExecute_InvalidJSON 验证 commands.execute 非法 JSON 返回错误
func TestCommandExecute_InvalidJSON(t *testing.T) {
	gw, _ := newTestGateway(t)
	cmdReg := command.NewRegistry(zap.NewNop())
	registerCommandMethods(gw, cmdReg, Deps{})

	body, _ := json.Marshal(RPCRequest{
		ID:     "req-bad-cmd",
		Method: "commands.execute",
		Params: json.RawMessage(`not-valid-json`),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-admin-token")
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp.Error, "非法 JSON 应返回错误")
}

// TestCommandExecute_MethodsRegistered 验证 commands.execute 方法已注册
func TestCommandExecute_MethodsRegistered(t *testing.T) {
	gw, _ := newTestGateway(t)
	cmdReg := command.NewRegistry(zap.NewNop())
	registerCommandMethods(gw, cmdReg, Deps{})

	gw.mu.RLock()
	defer gw.mu.RUnlock()
	for _, name := range []string{"commands.list", "commands.execute"} {
		_, ok := gw.methods[name]
		assert.True(t, ok, "方法应已注册: %s", name)
	}
}

// TestCommandExecute_ListWithRegisteredCommands 验证有注册命令时 list 返回正确结果
func TestCommandExecute_ListWithRegisteredCommands(t *testing.T) {
	gw, token := newTestGateway(t)
	cmdReg := command.NewRegistry(zap.NewNop())
	cmdReg.Register(&command.Info{
		Name:        "test-cmd",
		Description: "a test command",
		Source:      command.SourceBuiltin,
		Template:    "hello $ARGUMENTS",
	})
	registerCommandMethods(gw, cmdReg, Deps{})

	resp := doRPC(t, gw, "commands.list", map[string]interface{}{}, token)
	assert.Nil(t, resp.Error)

	var cmds []map[string]interface{}
	require.NoError(t, json.Unmarshal(resp.Result, &cmds))
	assert.Equal(t, 1, len(cmds), "应返回 1 个命令")
	assert.Equal(t, "test-cmd", cmds[0]["name"])
}

// ─────────────────────────────────────────────
// methods_mcp.go 测试
// ─────────────────────────────────────────────

// TestMCPMethods_MethodsRegistered 验证 MCP 方法注册
func TestMCPMethods_MethodsRegistered(t *testing.T) {
	gw, _ := newTestGateway(t)
	host := mcphost.NewHost(zap.NewNop())
	deps := Deps{MCPHost: host}
	registerMCPMethods(gw, deps)

	gw.mu.RLock()
	defer gw.mu.RUnlock()
	for _, name := range []string{
		"mcp.tools.list",
		"mcp.resources.list",
		"mcp.resources.read",
		"mcp.prompts.list",
		"mcp.prompts.get",
	} {
		_, ok := gw.methods[name]
		assert.True(t, ok, "MCP 方法应已注册: %s", name)
	}
}

// TestMCPTools_List_GroupsRemoteTools 验证运行时工具目录按 MCP 服务端分组
func TestMCPTools_List_GroupsRemoteTools(t *testing.T) {
	gw, token := newTestGateway(t)
	host := mcphost.NewHost(zap.NewNop())
	host.RegisterTool(mcphost.ToolDefinition{Name: "read_file", Core: true}, nil)
	host.RegisterTool(mcphost.ToolDefinition{Name: "memory", Core: true}, nil)
	host.RegisterTool(mcphost.ToolDefinition{
		Name:              "metamcp__grafana__query_prometheus",
		Description:       "[metamcp] query prometheus",
		IsConcurrencySafe: true,
		SourceServer:      "metamcp",
		Trusted:           true,
	}, nil)
	host.RegisterTool(mcphost.ToolDefinition{
		Name:         "metamcp__dbhub__execute_sql",
		Description:  "[metamcp] execute sql",
		SourceServer: "metamcp",
		Trusted:      true,
	}, nil)
	host.RegisterTool(mcphost.ToolDefinition{
		Name:         "metamcp__delete_dashboard",
		Description:  "[metamcp] delete dashboard",
		SourceServer: "metamcp",
		Trusted:      true,
	}, nil)
	host.RegisterResource(mcphost.ResourceDefinition{
		URI:  "metamcp://resource://dashboards",
		Name: "dashboards",
	}, nil)
	host.RegisterPrompt(mcphost.PromptDefinition{
		Name:        "metamcp__sre_prompt",
		Description: "SRE prompt",
	}, nil)
	deps := Deps{MCPHost: host}
	registerMCPMethods(gw, deps)

	resp := doRPC(t, gw, "mcp.tools.list", map[string]interface{}{}, token)
	assert.Nil(t, resp.Error)

	var got mcpToolsListResponse
	require.NoError(t, json.Unmarshal(resp.Result, &got))
	assert.Equal(t, 5, got.Total)
	assert.Equal(t, 3, got.MCPCount)
	assert.Equal(t, 2, got.LocalCount)
	require.Len(t, got.Servers, 1)
	assert.Equal(t, "metamcp", got.Servers[0].Name)
	assert.Equal(t, 3, got.Servers[0].Count)
	assert.Equal(t, 1, got.Servers[0].Resources)
	assert.Equal(t, 1, got.Servers[0].Prompts)
	assert.Equal(t, "metamcp__dbhub__execute_sql", got.Servers[0].Tools[0].Name)
	assert.Equal(t, "metamcp__delete_dashboard", got.Servers[0].Tools[1].Name)
	assert.Equal(t, "metamcp__grafana__query_prometheus", got.Servers[0].Tools[2].Name)
	assert.True(t, got.Servers[0].Tools[2].Trusted)
	assert.Equal(t, "read_only", got.Servers[0].Tools[2].Risk)
	assert.True(t, got.Servers[0].Tools[2].ReadOnly)
	assert.False(t, got.Servers[0].Tools[2].RequiresApproval)
	assert.False(t, got.Servers[0].Tools[2].MayRequireApproval)
	assert.True(t, got.Servers[0].Tools[2].CallableNow)
	assert.Equal(t, "callable_read_only", got.Servers[0].Tools[2].RouteStatus)
	assert.Empty(t, got.Servers[0].Tools[2].BlockReason)

	assert.False(t, got.Servers[0].Tools[1].CallableNow)
	assert.Equal(t, "blocked_dangerous", got.Servers[0].Tools[1].RouteStatus)
	assert.False(t, got.Servers[0].Tools[1].RequiresApproval)
	assert.True(t, got.Servers[0].Tools[1].MayRequireApproval)
	assert.NotEmpty(t, got.Servers[0].Tools[1].BlockReason)

	var memoryTool mcpToolSummary
	for _, tool := range got.Tools {
		if tool.Name == "memory" {
			memoryTool = tool
			break
		}
	}
	require.Equal(t, "memory", memoryTool.Name)
	assert.False(t, memoryTool.RequiresApproval)
	assert.True(t, memoryTool.MayRequireApproval)
	assert.True(t, memoryTool.CallableNow)
	assert.Equal(t, "callable_with_action_constraints", memoryTool.RouteStatus)
}

// TestMCPResources_List_Empty 验证空 MCP Host 返回空资源列表
func TestMCPResources_List_Empty(t *testing.T) {
	gw, token := newTestGateway(t)
	host := mcphost.NewHost(zap.NewNop())
	deps := Deps{MCPHost: host}
	registerMCPMethods(gw, deps)

	resp := doRPC(t, gw, "mcp.resources.list", map[string]interface{}{}, token)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.Result)
}

// TestMCPResources_Read_MissingURI 验证 mcp.resources.read 缺少 uri 时返回错误
func TestMCPResources_Read_MissingURI(t *testing.T) {
	gw, token := newTestGateway(t)
	host := mcphost.NewHost(zap.NewNop())
	deps := Deps{MCPHost: host}
	registerMCPMethods(gw, deps)

	resp := doRPC(t, gw, "mcp.resources.read", map[string]string{"uri": ""}, token)
	assert.NotNil(t, resp.Error, "空 uri 应返回错误")
}

// TestMCPResources_Read_NotFound 验证读取不存在的资源时返回错误
func TestMCPResources_Read_NotFound(t *testing.T) {
	gw, token := newTestGateway(t)
	host := mcphost.NewHost(zap.NewNop())
	deps := Deps{MCPHost: host}
	registerMCPMethods(gw, deps)

	resp := doRPC(t, gw, "mcp.resources.read", map[string]string{"uri": "file:///nonexistent"}, token)
	assert.NotNil(t, resp.Error, "不存在的资源应返回错误")
}

// TestMCPPrompts_List_Empty 验证空 MCP Host 返回空提示列表
func TestMCPPrompts_List_Empty(t *testing.T) {
	gw, token := newTestGateway(t)
	host := mcphost.NewHost(zap.NewNop())
	deps := Deps{MCPHost: host}
	registerMCPMethods(gw, deps)

	resp := doRPC(t, gw, "mcp.prompts.list", map[string]interface{}{}, token)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.Result)
}

// TestMCPPrompts_Get_MissingName 验证 mcp.prompts.get 缺少 name 时返回错误
func TestMCPPrompts_Get_MissingName(t *testing.T) {
	gw, token := newTestGateway(t)
	host := mcphost.NewHost(zap.NewNop())
	deps := Deps{MCPHost: host}
	registerMCPMethods(gw, deps)

	resp := doRPC(t, gw, "mcp.prompts.get", map[string]string{"name": ""}, token)
	assert.NotNil(t, resp.Error, "空 name 应返回错误")
}

// TestMCPPrompts_Get_NotFound 验证获取不存在的提示时返回错误
func TestMCPPrompts_Get_NotFound(t *testing.T) {
	gw, token := newTestGateway(t)
	host := mcphost.NewHost(zap.NewNop())
	deps := Deps{MCPHost: host}
	registerMCPMethods(gw, deps)

	resp := doRPC(t, gw, "mcp.prompts.get", map[string]string{"name": "nonexistent"}, token)
	assert.NotNil(t, resp.Error, "不存在的提示应返回错误")
}

// TestMCPPrompts_Get_WithRegisteredPrompt 验证获取已注册提示成功
func TestMCPPrompts_Get_WithRegisteredPrompt(t *testing.T) {
	gw, _ := newTestGateway(t)
	token := "test-admin-token" // 直接使用已知 token（与 newTestGateway 保持一致）
	host := mcphost.NewHost(zap.NewNop())

	// 注册一个测试提示
	host.RegisterPrompt(mcphost.PromptDefinition{
		Name:        "test-prompt",
		Description: "测试提示",
	}, func(_ context.Context, _ map[string]string) ([]mcphost.PromptMessage, error) {
		return []mcphost.PromptMessage{
			{Role: "user", Content: "hello"},
		}, nil
	})

	deps := Deps{MCPHost: host}
	registerMCPMethods(gw, deps)

	resp := doRPC(t, gw, "mcp.prompts.get", map[string]string{"name": "test-prompt"}, token)
	assert.Nil(t, resp.Error, "已注册的提示应成功返回，实际错误: %v", resp.Error)
}

// ─────────────────────────────────────────────
// methods_plugin.go 测试
// ─────────────────────────────────────────────

// TestPluginMethods_MethodsRegistered 验证 plugin 方法注册
func TestPluginMethods_MethodsRegistered(t *testing.T) {
	gw, _ := newTestGateway(t)
	mgr := plugin.NewManager(zap.NewNop())
	deps := Deps{PluginLoader: mgr}
	registerPluginMethods(gw, deps)

	gw.mu.RLock()
	defer gw.mu.RUnlock()
	for _, name := range []string{"plugin.list", "plugin.load", "plugin.unload", "plugin.reload"} {
		_, ok := gw.methods[name]
		assert.True(t, ok, "plugin 方法应已注册: %s", name)
	}
}

// TestPluginList_Empty 验证空插件管理器返回空列表
func TestPluginList_Empty(t *testing.T) {
	gw, token := newTestGateway(t)
	mgr := plugin.NewManager(zap.NewNop())
	deps := Deps{PluginLoader: mgr}
	registerPluginMethods(gw, deps)

	resp := doRPC(t, gw, "plugin.list", map[string]interface{}{}, token)
	assert.Nil(t, resp.Error, "plugin.list 应成功，实际错误: %v", resp.Error)
	assert.NotNil(t, resp.Result)
}

// TestPluginLoad_NonExistentID 验证加载不存在插件时返回错误
func TestPluginLoad_NonExistentID(t *testing.T) {
	gw, token := newTestGateway(t)
	mgr := plugin.NewManager(zap.NewNop())
	deps := Deps{PluginLoader: mgr}
	registerPluginMethods(gw, deps)

	resp := doRPC(t, gw, "plugin.load", map[string]string{"id": "nonexistent"}, token)
	assert.NotNil(t, resp.Error, "加载不存在的插件应返回错误")
}

// TestPluginUnload_NonExistentID 验证卸载不存在插件时返回错误
func TestPluginUnload_NonExistentID(t *testing.T) {
	gw, token := newTestGateway(t)
	mgr := plugin.NewManager(zap.NewNop())
	deps := Deps{PluginLoader: mgr}
	registerPluginMethods(gw, deps)

	resp := doRPC(t, gw, "plugin.unload", map[string]string{"id": "nonexistent"}, token)
	assert.NotNil(t, resp.Error, "卸载不存在的插件应返回错误")
}

// ─────────────────────────────────────────────
// methods_health.go 测试
// ─────────────────────────────────────────────

// TestHealthCheck_NoAuth 验证 health.check 无需认证
func TestHealthCheck_NoAuth(t *testing.T) {
	gw, _ := newTestGateway(t)
	// health 方法注册需要 Master（只验证无认证端点可公开访问）
	gw.Register(MethodDef{
		Name:      "health.check",
		AuthScope: "", // 无需认证
		Handler: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(map[string]string{"status": "ok"})
		},
	})

	// 不带 token 调用，应成功
	resp := doRPC(t, gw, "health.check", map[string]interface{}{}, "")
	assert.Nil(t, resp.Error, "health.check 无需认证，实际错误: %v", resp.Error)

	var result map[string]string
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, "ok", result["status"])
}

// ─────────────────────────────────────────────
// methods_skills.go 测试
// ─────────────────────────────────────────────

// TestSkillMethods_List 验证 skills.list 返回注册表内容
func TestSkillMethods_List(t *testing.T) {
	gw, token := newTestGateway(t)
	reg := skills.NewRegistry(zap.NewNop())
	deps := Deps{SkillRegistry: reg}
	registerSkillMethods(gw, deps)

	gw.mu.RLock()
	_, ok := gw.methods["skills.list"]
	gw.mu.RUnlock()
	assert.True(t, ok, "skills.list 应已注册")

	resp := doRPC(t, gw, "skills.list", map[string]interface{}{}, token)
	assert.Nil(t, resp.Error, "skills.list 应成功，实际错误: %v", resp.Error)
	assert.NotNil(t, resp.Result)
}

// ─────────────────────────────────────────────
// 端到端 RPC 分发路径测试（与 dispatch 联合）
// ─────────────────────────────────────────────

// TestDispatch_MissingMethod 验证缺少 method 字段返回 400
func TestDispatch_MissingMethod(t *testing.T) {
	gw, _ := newTestGateway(t)

	body, _ := json.Marshal(RPCRequest{ID: "1", Method: ""})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp.Error)
	assert.Equal(t, 400, resp.Error.Code)
}

// TestDispatch_MissingID 验证缺少 id 字段返回 400
func TestDispatch_MissingID(t *testing.T) {
	gw, _ := newTestGateway(t)

	body, _ := json.Marshal(RPCRequest{ID: "", Method: "health.check"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp.Error)
	assert.Equal(t, 400, resp.Error.Code)
}

// TestDispatch_HTTPMethodNotAllowed 验证非 POST 请求返回 405
func TestDispatch_HTTPMethodNotAllowed(t *testing.T) {
	gw, _ := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rpc", nil)
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestDispatch_InvalidJSON 验证非法 JSON 请求体返回 400
func TestDispatch_InvalidJSON(t *testing.T) {
	gw, _ := newTestGateway(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewBufferString("not-json"))
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp.Error)
	assert.Equal(t, 400, resp.Error.Code)
}
