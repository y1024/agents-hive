package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chef-guo/agents-hive/internal/channel"
	"github.com/chef-guo/agents-hive/internal/channel/feishu"
	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/store"
	"go.uber.org/zap"
)

// --- handleHealth 测试 ---

func TestHandleHealth(t *testing.T) {
	handler, _, cleanup := newTestServerForSessions(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际得到 %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if resp["status"] != "healthy" {
		t.Errorf("期望 status=healthy，实际得到 %s", resp["status"])
	}
}

func TestHandleSwitchModelRequiresSessionID(t *testing.T) {
	handler, _, cleanup := newTestServerForSessions(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPut, "/api/v1/model", strings.NewReader(`{"name":"model-a"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "需要会话 ID") {
		t.Fatalf("response should mention missing session id, got: %s", rec.Body.String())
	}
}

func TestHandleSessionScopedModelSelection(t *testing.T) {
	handler, _, st, cleanup := newTestServerForSessionsWithStore(t)
	defer cleanup()

	requireModel := func(name, model string, isDefault bool) {
		t.Helper()
		if err := st.SaveLLMModel(context.Background(), &store.LLMModelRecord{
			Name:      name,
			Model:     model,
			Enabled:   true,
			IsDefault: isDefault,
		}); err != nil {
			t.Fatalf("save model %s: %v", name, err)
		}
	}
	requireModel("model-a", "gpt-5", true)
	requireModel("model-b", "o3-mini", false)

	create := func(name string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"name":"`+name+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create session status = %d; body=%s", rec.Code, rec.Body.String())
		}
		var resp CreateSessionResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		return resp.SessionID
	}

	sessionA := create("A")
	sessionB := create("B")

	body, _ := json.Marshal(map[string]string{"name": "model-b", "session_id": sessionA})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/model", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch model status = %d; body=%s", rec.Code, rec.Body.String())
	}

	getModels := func(sessionID string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/models?session_id="+sessionID, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list models status = %d; body=%s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Active string `json:"active"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode models response: %v", err)
		}
		return resp.Active
	}

	if got := getModels(sessionA); got != "model-b" {
		t.Fatalf("session A active model = %q, want model-b", got)
	}
	if got := getModels(sessionB); got != "model-a" {
		t.Fatalf("session B active model = %q, want default model-a", got)
	}
	record, err := st.LoadSession(context.Background(), sessionA)
	if err != nil {
		t.Fatalf("load session A: %v", err)
	}
	if record.SelectedModel != "model-b" {
		t.Fatalf("persisted selected_model = %q, want model-b", record.SelectedModel)
	}
}

func TestHandleFeishuHealth_Disabled(t *testing.T) {
	logger := zap.NewNop()
	srv := NewServer(
		config.ServerConfig{Port: 0},
		config.HITLConfig{},
		config.WebUIConfig{},
		nil,
		nil,
		&config.Config{},
		"",
		nil,
		nil,
		nil,
		logger,
	)
	req := httptest.NewRequest("GET", "/api/v1/health/feishu", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际得到 %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp["status"] != "disabled" {
		t.Fatalf("status = %v, want disabled", resp["status"])
	}
}

func TestHandleFeishuHealth_Enabled(t *testing.T) {
	logger := zap.NewNop()
	srv := NewServer(
		config.ServerConfig{Port: 0},
		config.HITLConfig{},
		config.WebUIConfig{},
		nil,
		nil,
		&config.Config{
			Channel: config.ChannelConfig{
				Feishu: config.FeishuConfig{
					Enabled:           true,
					AppID:             "cli_xxx",
					AppSecret:         "secret",
					VerificationToken: "token",
					EncryptKey:        "encrypt",
				},
			},
		},
		"",
		nil,
		nil,
		nil,
		logger,
	)

	req := httptest.NewRequest("GET", "/api/v1/health/feishu", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际得到 %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp["status"] != "healthy" {
		t.Fatalf("status = %v, want healthy", resp["status"])
	}
	if resp["platform"] != "feishu" {
		t.Fatalf("platform = %v, want feishu", resp["platform"])
	}
	if resp["token_configured"] != true {
		t.Fatalf("token_configured = %v, want true", resp["token_configured"])
	}
	if resp["verification_configured"] != true {
		t.Fatalf("verification_configured = %v, want true", resp["verification_configured"])
	}
	if resp["encrypt_key_configured"] != true {
		t.Fatalf("encrypt_key_configured = %v, want true", resp["encrypt_key_configured"])
	}
}

func TestHandleFeishuHealth_DegradedReturns503(t *testing.T) {
	logger := zap.NewNop()
	srv := NewServer(
		config.ServerConfig{Port: 0},
		config.HITLConfig{},
		config.WebUIConfig{},
		nil,
		nil,
		&config.Config{
			Channel: config.ChannelConfig{
				Feishu: config.FeishuConfig{
					Enabled:           true,
					AppID:             "cli_xxx",
					AppSecret:         "secret",
					VerificationToken: "token",
					EncryptKey:        "encrypt",
					Security: config.FeishuSecurityConfig{
						PermissionDegradeThreshold: 2,
					},
				},
			},
		},
		"",
		nil,
		nil,
		nil,
		logger,
	)

	client := &feishu.Client{}
	client.ApplySecurityConfig(2)
	now := time.Now()
	client.ObserveAPIErrorForTest(errors.New("code=99991663"), now.Add(-2*time.Minute))
	client.ObserveAPIErrorForTest(errors.New("code=10013"), now.Add(-1*time.Minute))
	srv.SetFeishuHealthClient(client)

	req := httptest.NewRequest("GET", "/api/v1/health/feishu", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("期望状态码 503，实际得到 %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp["status"] != "degraded" {
		t.Fatalf("status = %v, want degraded", resp["status"])
	}
	if resp["degraded"] != true {
		t.Fatalf("degraded = %v, want true", resp["degraded"])
	}
}

func TestHandleFeishuHealth_UsesRouterPluginClientWhenDirectPointerMissing(t *testing.T) {
	logger := zap.NewNop()
	router := channel.NewRouter(nil, logger)
	plugin := feishu.New(config.FeishuConfig{
		Enabled: true,
	}, router, logger)
	plugin.Client().ApplySecurityConfig(2)
	now := time.Now()
	plugin.Client().ObserveAPIErrorForTest(errors.New("code=99991663"), now.Add(-2*time.Minute))
	plugin.Client().ObserveAPIErrorForTest(errors.New("code=10013"), now.Add(-1*time.Minute))
	router.RegisterPlugin(plugin)

	srv := NewServer(
		config.ServerConfig{Port: 0},
		config.HITLConfig{},
		config.WebUIConfig{},
		nil,
		nil,
		&config.Config{
			Channel: config.ChannelConfig{
				Feishu: config.FeishuConfig{
					Enabled: true,
					Security: config.FeishuSecurityConfig{
						PermissionDegradeThreshold: 2,
					},
				},
			},
		},
		"",
		router,
		nil,
		nil,
		logger,
	)

	req := httptest.NewRequest("GET", "/api/v1/health/feishu", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("期望状态码 503，实际得到 %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp["status"] != "degraded" {
		t.Fatalf("status = %v, want degraded", resp["status"])
	}
}

// --- handleListAgents 测试 ---

func TestHandleListAgents(t *testing.T) {
	handler, _, cleanup := newTestServerForSessions(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/v1/agents", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际得到 %d", rec.Code)
	}

	var resp []AgentInfo
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	// 测试环境未注册代理，应返回空数组
	if len(resp) != 0 {
		t.Errorf("期望空代理列表，实际得到 %d 个代理", len(resp))
	}
}

// --- handleListSkills 测试 ---

func TestHandleListSkills(t *testing.T) {
	handler, _, cleanup := newTestServerForSessions(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/v1/skills", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际得到 %d", rec.Code)
	}

	// 响应应为 JSON 数组（可能为空）
	var resp []json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
}

// --- handleSubmitInput 测试 ---

func TestHandleSubmitInput(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		body       string
		wantStatus int
		wantError  bool
	}{
		{
			name:       "无效 JSON 请求体",
			url:        "/api/v1/tasks/task-123/input",
			body:       "not-json{",
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:       "有效请求体但无匹配的待处理输入",
			url:        "/api/v1/tasks/task-123/input",
			body:       `{"request_id":"req-1","value":"yes","action":"approve"}`,
			wantStatus: http.StatusBadRequest, // 无匹配的 pending input 会返回错误
			wantError:  true,
		},
		{
			name:       "空请求体",
			url:        "/api/v1/tasks/task-456/input",
			body:       `{}`,
			wantStatus: http.StatusBadRequest, // 无匹配的 pending input
			wantError:  true,
		},
	}

	handler, _, cleanup := newTestServerForSessions(t)
	defer cleanup()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tt.url, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("期望状态码 %d，实际得到 %d; body: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}

			if tt.wantError {
				var errResp ErrorResponse
				if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
					t.Fatalf("解析错误响应失败: %v", err)
				}
				if errResp.Error == "" {
					t.Error("期望错误消息非空")
				}
			}
		})
	}
}

// --- handleSendCommand 测试 ---

func TestHandleSendCommand(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		body       string
		wantStatus int
		wantError  bool
	}{
		{
			name:       "无效 JSON 请求体",
			url:        "/api/v1/tasks/task-123/command",
			body:       "not-json{",
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:       "无效命令类型",
			url:        "/api/v1/tasks/task-123/command",
			body:       `{"type":"invalid-type"}`,
			wantStatus: http.StatusInternalServerError,
			wantError:  true,
		},
		{
			name:       "有效暂停命令",
			url:        "/api/v1/tasks/task-123/command",
			body:       `{"type":"pause"}`,
			wantStatus: http.StatusOK,
			wantError:  false,
		},
		{
			name:       "有效取消命令",
			url:        "/api/v1/tasks/task-456/command",
			body:       `{"type":"cancel"}`,
			wantStatus: http.StatusOK,
			wantError:  false,
		},
	}

	handler, _, cleanup := newTestServerForSessions(t)
	defer cleanup()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tt.url, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("期望状态码 %d，实际得到 %d; body: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}

			if tt.wantError {
				var errResp ErrorResponse
				if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
					t.Fatalf("解析错误响应失败: %v", err)
				}
				if errResp.Error == "" {
					t.Error("期望错误消息非空")
				}
			} else {
				var resp map[string]string
				if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
					t.Fatalf("解析成功响应失败: %v", err)
				}
				if resp["status"] != "已接受" {
					t.Errorf("期望 status=已接受，实际得到 %s", resp["status"])
				}
			}
		})
	}
}

// --- handleGetPendingInput 测试 ---

func TestHandleGetPendingInput(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		wantStatus int
	}{
		{
			name:       "有效任务 ID 返回空数组",
			url:        "/api/v1/tasks/task-123/pending-input",
			wantStatus: http.StatusOK,
		},
		{
			name:       "不存在的任务 ID 也返回空数组",
			url:        "/api/v1/tasks/nonexistent/pending-input",
			wantStatus: http.StatusOK,
		},
	}

	handler, _, cleanup := newTestServerForSessions(t)
	defer cleanup()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("期望状态码 %d，实际得到 %d; body: %s", tt.wantStatus, rec.Code, rec.Body.String())
			}

			// 应返回 JSON 数组（空数组）
			var resp []json.RawMessage
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("解析响应失败: %v", err)
			}

			if len(resp) != 0 {
				t.Errorf("期望空数组，实际得到 %d 个元素", len(resp))
			}
		})
	}
}
