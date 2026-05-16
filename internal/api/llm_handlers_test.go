package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/security"
	"github.com/chef-guo/agents-hive/internal/store"
)

// newTestServerForLLM 创建带 MemoryStore 的测试服务器，专门用于 LLM handler 测试。
// 不需要 master、authEngine 等组件。
func newTestServerForLLM(t *testing.T) (http.Handler, *store.MemoryStore) {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	st := store.NewMemoryStore()

	srv := &Server{
		store:  st,
		logger: logger,
	}

	mux := http.NewServeMux()
	// 直接注册 LLM 路由（绕过 authEngine guard，专注 handler 逻辑）
	mux.HandleFunc("GET /api/v1/admin/llm/providers", srv.handleAdminListLLMProviders)
	mux.HandleFunc("POST /api/v1/admin/llm/providers", srv.handleAdminCreateLLMProvider)
	mux.HandleFunc("PATCH /api/v1/admin/llm/providers/{name}", srv.handleAdminUpdateLLMProvider)
	mux.HandleFunc("DELETE /api/v1/admin/llm/providers/{name}", srv.handleAdminDeleteLLMProvider)
	mux.HandleFunc("GET /api/v1/admin/llm/models", srv.handleAdminListLLMModels)
	mux.HandleFunc("POST /api/v1/admin/llm/models", srv.handleAdminCreateLLMModel)
	mux.HandleFunc("PATCH /api/v1/admin/llm/models/{name}", srv.handleAdminUpdateLLMModel)
	mux.HandleFunc("DELETE /api/v1/admin/llm/models/{name}", srv.handleAdminDeleteLLMModel)

	return mux, st
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewReader(b)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// ── Test 1: List providers — api_key 脱敏验证 ──────────────────────────────

func TestLLM_ListProviders_MaskAPIKey(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	// 写入一个有长 api_key 的 provider
	_ = st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name: "openai-1", ProviderType: "openai", APIKey: "sk-1234567890abcdef",
		Enabled: true, APIFormat: "chat", ServiceType: "llm", ConfigJSON: "{}",
	})

	rec := doJSON(t, handler, "GET", "/api/v1/admin/llm/providers", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Providers) != 1 {
		t.Fatalf("期望 1 个 provider，得到 %d", len(resp.Providers))
	}
	key := resp.Providers[0]["api_key"].(string)
	// 应该以 **** 包含脱敏
	if key == "sk-1234567890abcdef" {
		t.Errorf("api_key 未脱敏，原始值暴露: %s", key)
	}
	if len(key) < 8 || key[4:8] != "****" {
		t.Errorf("脱敏格式不符合预期（首4末4****），得到: %s", key)
	}
}

func TestLLM_ListProviders_RedactsConfigJSONSecrets(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name: "openai-1", ProviderType: "openai", APIKey: "sk-1234567890abcdef",
		Enabled: true, APIFormat: "chat", ServiceType: "llm",
		ConfigJSON: `{"reasoning_effort":"high","nested":{"api_key":"raw-config-key"},"message":"client_secret=raw-inline-secret"}`,
	})

	rec := doJSON(t, handler, "GET", "/api/v1/admin/llm/providers", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	cfg := resp.Providers[0]["config_json"].(string)
	for _, leaked := range []string{"raw-config-key", "raw-inline-secret"} {
		if strings.Contains(cfg, leaked) {
			t.Fatalf("provider config_json 泄露敏感值 %q: %s", leaked, cfg)
		}
	}
	if !strings.Contains(cfg, security.RedactedValue) || !strings.Contains(cfg, `"reasoning_effort":"high"`) {
		t.Fatalf("provider config_json 脱敏结果异常: %s", cfg)
	}
}

// ── Test 2: Create 重复名返回 409 ──────────────────────────────────────────

func TestLLM_CreateProvider_DuplicateReturns409(t *testing.T) {
	handler, _ := newTestServerForLLM(t)

	body := map[string]any{"name": "dup", "provider_type": "openai"}
	rec := doJSON(t, handler, "POST", "/api/v1/admin/llm/providers", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("第一次创建应成功，得到 %d", rec.Code)
	}

	rec2 := doJSON(t, handler, "POST", "/api/v1/admin/llm/providers", body)
	if rec2.Code != http.StatusConflict {
		t.Errorf("重复创建应返回 409，得到 %d: %s", rec2.Code, rec2.Body.String())
	}
}

// ── Test 3: Update api_key="****" 不覆盖现有 key ────────────────────────────

func TestLLM_UpdateProvider_MaskedKeyNotOverwritten(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name: "p1", ProviderType: "openai", APIKey: "original-secret-key",
		Enabled: true, APIFormat: "chat", ServiceType: "llm", ConfigJSON: "{}",
	})

	// 发送 api_key="****"（前端在 edit 模式下留空/掩码值）
	body := map[string]any{"api_key": "****", "enabled": false}
	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/providers/p1", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	// 验证原始 key 未被覆盖
	p, _ := st.GetLLMProvider(ctx, "p1")
	if p.APIKey != "original-secret-key" {
		t.Errorf("api_key 被 **** 覆盖，期望保留原值，得到: %s", p.APIKey)
	}
}

func TestLLM_UpdateProvider_MaskedLongKeyNotOverwritten(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name: "p1", ProviderType: "openai", APIKey: "sk-original-secret",
		Enabled: true, APIFormat: "chat", ServiceType: "llm", ConfigJSON: "{}",
	})

	body := map[string]any{"api_key": "sk-x****fFiE"}
	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/providers/p1", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	p, _ := st.GetLLMProvider(ctx, "p1")
	if p.APIKey != "sk-original-secret" {
		t.Fatalf("api_key 被脱敏值覆盖，得到: %s", p.APIKey)
	}
}

func TestLLM_UpdateProvider_PreservesMaskedConfigJSONSecrets(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name: "p1", ProviderType: "openai", APIKey: "sk-original-secret",
		Enabled: true, APIFormat: "chat", ServiceType: "llm",
		ConfigJSON: `{"reasoning_effort":"low","nested":{"api_key":"real-config-key"},"plain":"old"}`,
	})

	body := map[string]any{
		"config_json": `{"reasoning_effort":"high","nested":{"api_key":"[REDACTED]"},"plain":"new"}`,
	}
	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/providers/p1", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	p, _ := st.GetLLMProvider(ctx, "p1")
	for _, bad := range []string{security.RedactedValue, "****"} {
		if strings.Contains(p.ConfigJSON, bad) {
			t.Fatalf("脱敏占位被写入 provider config_json: %s", p.ConfigJSON)
		}
	}
	if !strings.Contains(p.ConfigJSON, `"api_key":"real-config-key"`) ||
		!strings.Contains(p.ConfigJSON, `"reasoning_effort":"high"`) ||
		!strings.Contains(p.ConfigJSON, `"plain":"new"`) {
		t.Fatalf("provider config_json 未正确保留真实 secret 并更新普通字段: %s", p.ConfigJSON)
	}
}

func TestLLM_UpdateProvider_PreservesInlineRedactedConfigJSONValues(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name: "p1", ProviderType: "openai", APIKey: "sk-original-secret",
		Enabled: true, APIFormat: "chat", ServiceType: "llm",
		ConfigJSON: `{"callback_url":"https://callback.example.com/hook?token=real-token","plain":"old"}`,
	})

	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/providers/p1", map[string]any{
		"config_json": `{"callback_url":"https://callback.example.com/hook?token=[REDACTED]","plain":"new"}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	p, _ := st.GetLLMProvider(ctx, "p1")
	if strings.Contains(p.ConfigJSON, security.RedactedValue) {
		t.Fatalf("内联脱敏占位被写入 provider config_json: %s", p.ConfigJSON)
	}
	if !strings.Contains(p.ConfigJSON, `"callback_url":"https://callback.example.com/hook?token=real-token"`) ||
		!strings.Contains(p.ConfigJSON, `"plain":"new"`) {
		t.Fatalf("provider config_json 未正确保留内联脱敏真实值: %s", p.ConfigJSON)
	}
}

func TestLLM_CreateProvider_RejectsInvalidConfigJSON(t *testing.T) {
	handler, _ := newTestServerForLLM(t)

	rec := doJSON(t, handler, "POST", "/api/v1/admin/llm/providers", map[string]any{
		"name":          "bad-config",
		"provider_type": "openai",
		"config_json":   `{"api_key":`,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 config_json 应返回 400，得到 %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLLM_UpdateProvider_RejectsInvalidConfigJSON(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name: "p1", ProviderType: "openai", APIKey: "sk-original-secret",
		Enabled: true, APIFormat: "chat", ServiceType: "llm", ConfigJSON: `{"safe":true}`,
	})

	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/providers/p1", map[string]any{
		"config_json": `{"api_key":`,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 config_json 应返回 400，得到 %d: %s", rec.Code, rec.Body.String())
	}
	p, _ := st.GetLLMProvider(ctx, "p1")
	if p.ConfigJSON != `{"safe":true}` {
		t.Fatalf("非法 config_json 不应覆盖原值，得到: %s", p.ConfigJSON)
	}
}

func TestLLM_UpdateProvider_RejectsInvalidBaseURL(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name: "p1", ProviderType: "openai", APIKey: "sk-original-secret",
		BaseURL: "https://api.example.com", Enabled: true, APIFormat: "chat", ServiceType: "llm", ConfigJSON: "{}",
	})

	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/providers/p1", map[string]any{"base_url": "xiyun"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 base_url 应返回 400，得到 %d: %s", rec.Code, rec.Body.String())
	}

	p, _ := st.GetLLMProvider(ctx, "p1")
	if p.BaseURL != "https://api.example.com" {
		t.Fatalf("非法 base_url 不应写入，得到: %s", p.BaseURL)
	}
}

// ── Test 4: Update is_default 清除其他 provider 的默认标记 ─────────────────

func TestLLM_UpdateProvider_SetDefaultClearsOthers(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name: "p1", ProviderType: "openai", IsDefault: true,
		Enabled: true, APIFormat: "chat", ServiceType: "llm", ConfigJSON: "{}",
	})
	_ = st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name: "p2", ProviderType: "anthropic", IsDefault: false,
		Enabled: true, APIFormat: "chat", ServiceType: "llm", ConfigJSON: "{}",
	})

	// 将 p2 设为 default
	isDefault := true
	body := map[string]any{"is_default": isDefault}
	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/providers/p2", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	// p1 应不再是 default
	p1, _ := st.GetLLMProvider(ctx, "p1")
	if p1.IsDefault {
		t.Errorf("p1 仍然是默认，期望被清除")
	}
}

// ── Test 5: Delete provider 级联删除关联 models ─────────────────────────────

func TestLLM_DeleteProvider_CascadesModels(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMProvider(ctx, &store.LLMProviderRecord{
		Name: "openai", ProviderType: "openai",
		Enabled: true, APIFormat: "chat", ServiceType: "llm", ConfigJSON: "{}",
	})
	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "gpt4", ProviderName: "openai", Model: "gpt-5", ConfigJSON: "{}",
	})

	rec := doJSON(t, handler, "DELETE", "/api/v1/admin/llm/providers/openai", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("删除应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	// 关联 model 应被级联删除
	models, _ := st.ListLLMModels(ctx)
	for _, m := range models {
		if m.Name == "gpt4" {
			t.Errorf("级联删除失败，gpt4 model 仍然存在")
		}
	}
}

// ── Test 6: Update 记录不存在返回 404（非 500）─────────────────────────────

func TestLLM_UpdateProvider_NotFoundReturns404(t *testing.T) {
	handler, _ := newTestServerForLLM(t)

	body := map[string]any{"enabled": true}
	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/providers/nonexistent", body)
	if rec.Code != http.StatusNotFound {
		t.Errorf("期望 404，得到 %d: %s", rec.Code, rec.Body.String())
	}
}

// ── Test 7: Create model 重复名返回 409 ────────────────────────────────────

func TestLLM_CreateModel_DuplicateReturns409(t *testing.T) {
	handler, _ := newTestServerForLLM(t)

	body := map[string]any{"name": "m1", "model": "gpt-5"}
	rec := doJSON(t, handler, "POST", "/api/v1/admin/llm/models", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("第一次创建应成功，得到 %d", rec.Code)
	}

	rec2 := doJSON(t, handler, "POST", "/api/v1/admin/llm/models", body)
	if rec2.Code != http.StatusConflict {
		t.Errorf("重复创建应返回 409，得到 %d: %s", rec2.Code, rec2.Body.String())
	}
}

// ── Test 8: Update model api_key="****" 不覆盖现有 key ──────────────────────

func TestLLM_UpdateModel_MaskedKeyNotOverwritten(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "m1", ProviderName: "p1", Model: "gpt-5",
		APIKey: "model-secret", ConfigJSON: "{}",
	})

	body := map[string]any{"api_key": "****"}
	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/models/m1", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	m, _ := st.GetLLMModel(ctx, "m1")
	if m.APIKey != "model-secret" {
		t.Errorf("api_key 被 **** 覆盖，期望保留原值，得到: %s", m.APIKey)
	}
}

func TestLLM_UpdateModel_MaskedLongKeyNotOverwritten(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "m1", ProviderName: "p1", Model: "gpt-5",
		APIKey: "model-secret", ConfigJSON: "{}",
	})

	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/models/m1", map[string]any{"api_key": "sk-x****fFiE"})
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	m, _ := st.GetLLMModel(ctx, "m1")
	if m.APIKey != "model-secret" {
		t.Fatalf("api_key 被脱敏值覆盖，得到: %s", m.APIKey)
	}
}

func TestLLM_UpdateModel_EmptyKeyClearsInvalidStoredOverride(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "m1", ProviderName: "p1", Model: "gpt-5",
		APIKey: "xiyun", ConfigJSON: "{}",
	})

	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/models/m1", map[string]any{"api_key": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	m, _ := st.GetLLMModel(ctx, "m1")
	if m.APIKey != "" {
		t.Fatalf("无效 model 级 api_key 应被清空以继承 provider，得到: %s", m.APIKey)
	}
}

func TestLLM_UpdateModel_RejectsInvalidBaseURL(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "m1", ProviderName: "p1", Model: "gpt-5",
		BaseURL: "https://api.example.com", ConfigJSON: "{}",
	})

	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/models/m1", map[string]any{"base_url": "xiyun"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 base_url 应返回 400，得到 %d: %s", rec.Code, rec.Body.String())
	}

	m, _ := st.GetLLMModel(ctx, "m1")
	if m.BaseURL != "https://api.example.com" {
		t.Fatalf("非法 base_url 不应写入，得到: %s", m.BaseURL)
	}
}

func TestLLM_ListModels_MasksAPIKey(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "m1", ProviderName: "p1", Model: "gpt-5",
		APIKey: "sk-1234567890abcdef", ConfigJSON: "{}",
	})

	rec := doJSON(t, handler, "GET", "/api/v1/admin/llm/models", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	key := resp.Models[0]["api_key"].(string)
	if key == "sk-1234567890abcdef" || key != "sk-1****cdef" {
		t.Fatalf("model api_key 未正确脱敏，得到: %s", key)
	}
}

func TestLLM_ListModels_RedactsConfigJSONSecrets(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "m1", ProviderName: "p1", Model: "gpt-5",
		APIKey:     "sk-1234567890abcdef",
		ConfigJSON: `{"interactive_service_tier":"priority","headers":{"authorization":"Bearer raw-token"}}`,
	})

	rec := doJSON(t, handler, "GET", "/api/v1/admin/llm/models", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	cfg := resp.Models[0]["config_json"].(string)
	if strings.Contains(cfg, "raw-token") {
		t.Fatalf("model config_json 泄露敏感值: %s", cfg)
	}
	if !strings.Contains(cfg, security.RedactedValue) || !strings.Contains(cfg, `"interactive_service_tier":"priority"`) {
		t.Fatalf("model config_json 脱敏结果异常: %s", cfg)
	}
}

func TestLLM_UpdateModel_PreservesMaskedConfigJSONSecrets(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "m1", ProviderName: "p1", Model: "gpt-5",
		ConfigJSON: `{"headers":{"authorization":"Bearer real-token"},"cost_tier":2}`,
	})

	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/models/m1", map[string]any{
		"config_json": `{"headers":{"authorization":"[REDACTED]"},"cost_tier":3}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	m, _ := st.GetLLMModel(ctx, "m1")
	if strings.Contains(m.ConfigJSON, security.RedactedValue) || strings.Contains(m.ConfigJSON, "****") {
		t.Fatalf("脱敏占位被写入 model config_json: %s", m.ConfigJSON)
	}
	if !strings.Contains(m.ConfigJSON, `"authorization":"Bearer real-token"`) ||
		!strings.Contains(m.ConfigJSON, `"cost_tier":3`) {
		t.Fatalf("model config_json 未正确保留真实 secret 并更新普通字段: %s", m.ConfigJSON)
	}
}

func TestLLM_UpdateModel_PreservesInlineRedactedConfigJSONValues(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name:       "m1",
		Model:      "gpt-5",
		ConfigJSON: `{"proxy_url":"https://proxy.example.com?client_secret=real-secret","cost_tier":2}`,
	})

	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/models/m1", map[string]any{
		"config_json": `{"proxy_url":"https://proxy.example.com?client_secret=[REDACTED]","cost_tier":3}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	m, _ := st.GetLLMModel(ctx, "m1")
	if strings.Contains(m.ConfigJSON, security.RedactedValue) {
		t.Fatalf("内联脱敏占位被写入 model config_json: %s", m.ConfigJSON)
	}
	if !strings.Contains(m.ConfigJSON, `"proxy_url":"https://proxy.example.com?client_secret=real-secret"`) ||
		!strings.Contains(m.ConfigJSON, `"cost_tier":3`) {
		t.Fatalf("model config_json 未正确保留内联脱敏真实值: %s", m.ConfigJSON)
	}
}

func TestLLM_CreateModel_RejectsInvalidConfigJSON(t *testing.T) {
	handler, _ := newTestServerForLLM(t)

	rec := doJSON(t, handler, "POST", "/api/v1/admin/llm/models", map[string]any{
		"name":        "bad-config",
		"model":       "gpt-5",
		"config_json": `{"api_key":`,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 config_json 应返回 400，得到 %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLLM_UpdateModel_RejectsInvalidConfigJSON(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "m1", ProviderName: "p1", Model: "gpt-5", ConfigJSON: `{"safe":true}`,
	})

	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/models/m1", map[string]any{
		"config_json": `{"api_key":`,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 config_json 应返回 400，得到 %d: %s", rec.Code, rec.Body.String())
	}
	m, _ := st.GetLLMModel(ctx, "m1")
	if m.ConfigJSON != `{"safe":true}` {
		t.Fatalf("非法 config_json 不应覆盖原值，得到: %s", m.ConfigJSON)
	}
}

func TestLLM_UpdateModel_RenamesModel(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "gpt-5.2", ProviderName: "openai", Model: "gpt-5.4",
		APIKey: "model-secret", ConfigJSON: "{}",
	})

	body := map[string]any{"name": "gpt-5.4", "model": "gpt-5.4"}
	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/models/gpt-5.2", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	if _, err := st.GetLLMModel(ctx, "gpt-5.2"); err == nil {
		t.Fatal("旧模型名称仍可读取，期望已被 rename")
	}
	m, err := st.GetLLMModel(ctx, "gpt-5.4")
	if err != nil {
		t.Fatalf("新模型名称读取失败: %v", err)
	}
	if m.Model != "gpt-5.4" {
		t.Fatalf("Model = %q, want gpt-5.4", m.Model)
	}
	models, err := st.ListLLMModels(ctx)
	if err != nil {
		t.Fatalf("ListLLMModels failed: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models count = %d, want 1", len(models))
	}
}

func TestLLM_UpdateModel_RenameAndSetDefaultClearsOthers(t *testing.T) {
	handler, st := newTestServerForLLM(t)
	ctx := t.Context()

	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "old-default", ProviderName: "openai", Model: "gpt-5",
		IsDefault: true, ConfigJSON: "{}",
	})
	_ = st.SaveLLMModel(ctx, &store.LLMModelRecord{
		Name: "gpt-5.2", ProviderName: "openai", Model: "gpt-5.4",
		IsDefault: false, ConfigJSON: "{}",
	})

	body := map[string]any{"name": "gpt-5.4", "model": "gpt-5.4", "is_default": true}
	rec := doJSON(t, handler, "PATCH", "/api/v1/admin/llm/models/gpt-5.2", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("更新应成功，得到 %d: %s", rec.Code, rec.Body.String())
	}

	oldDefault, err := st.GetLLMModel(ctx, "old-default")
	if err != nil {
		t.Fatalf("读取旧默认模型失败: %v", err)
	}
	if oldDefault.IsDefault {
		t.Fatal("旧默认模型仍为默认，期望被清除")
	}
	newDefault, err := st.GetLLMModel(ctx, "gpt-5.4")
	if err != nil {
		t.Fatalf("读取新默认模型失败: %v", err)
	}
	if !newDefault.IsDefault {
		t.Fatal("重命名后的模型未设为默认")
	}
}
