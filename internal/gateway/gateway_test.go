package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/errs"
)

func TestGatewayAuth_AllowsAdminUserContext(t *testing.T) {
	logger := zap.NewNop()
	authMgr := NewAuthManager([]string{"machine-token"})
	gw := New(authMgr, logger)

	gw.Register(MethodDef{
		Name:      "admin.method",
		AuthScope: "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(map[string]string{"status": "ok"})
		},
	})

	body, _ := json.Marshal(RPCRequest{ID: "1", Method: "admin.method"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID:     "u1",
		Role:   "admin",
		Status: "active",
	}))
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Nil(t, resp.Error)
}

func TestGatewayAuth_RejectsNonAdminUserContextForAdminScope(t *testing.T) {
	logger := zap.NewNop()
	authMgr := NewAuthManager([]string{"machine-token"})
	gw := New(authMgr, logger)

	gw.Register(MethodDef{
		Name:      "admin.method",
		AuthScope: "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			return nil, errs.New(errs.CodeInternal, "should not run")
		},
	})

	body, _ := json.Marshal(RPCRequest{ID: "1", Method: "admin.method"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID:     "u1",
		Role:   "user",
		Status: "active",
	}))
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	json.NewDecoder(w.Body).Decode(&resp)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, 401, resp.Error.Code)
}

func TestGatewayAuth_AllowsReadScopeForActiveUserContext(t *testing.T) {
	logger := zap.NewNop()
	authMgr := NewAuthManager([]string{"machine-token"})
	gw := New(authMgr, logger)

	gw.Register(MethodDef{
		Name:      "read.method",
		AuthScope: "read",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(map[string]string{"status": "ok"})
		},
	})

	body, _ := json.Marshal(RPCRequest{ID: "1", Method: "read.method"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID:     "u1",
		Role:   "user",
		Status: "active",
	}))
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Nil(t, resp.Error)
}

func TestGatewayAuth_RejectsAnonymousWhenHTTPAuthEnabledEvenWithoutGatewayTokens(t *testing.T) {
	logger := zap.NewNop()
	authMgr := NewAuthManager(nil)
	gw := New(authMgr, logger)

	gw.Register(MethodDef{
		Name:      "admin.method",
		AuthScope: "admin",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			return nil, errs.New(errs.CodeInternal, "should not run")
		},
	})

	body, _ := json.Marshal(RPCRequest{ID: "1", Method: "admin.method"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	req = req.WithContext(auth.WithAuthEnabled(req.Context()))
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	json.NewDecoder(w.Body).Decode(&resp)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, 401, resp.Error.Code)
}

func TestGatewayDispatch(t *testing.T) {
	logger := zap.NewNop()
	auth := NewAuthManager(nil)
	gw := New(auth, logger)

	gw.Register(MethodDef{
		Name:        "test.echo",
		Description: "测试回声",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			return params, nil
		},
	})

	// 测试正常调用
	body, _ := json.Marshal(RPCRequest{ID: "1", Method: "test.echo", Params: json.RawMessage(`{"msg":"hello"}`)})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	json.NewDecoder(w.Body).Decode(&resp)
	assert.Equal(t, "1", resp.ID)
	assert.Nil(t, resp.Error)
	assert.Equal(t, `{"msg":"hello"}`, string(resp.Result))
}

func TestGatewayMethodNotFound(t *testing.T) {
	logger := zap.NewNop()
	auth := NewAuthManager(nil)
	gw := New(auth, logger)

	body, _ := json.Marshal(RPCRequest{ID: "1", Method: "nonexistent"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	json.NewDecoder(w.Body).Decode(&resp)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, 404, resp.Error.Code)
}

func TestGatewayAuth(t *testing.T) {
	logger := zap.NewNop()
	auth := NewAuthManager([]string{"secret-token"})
	gw := New(auth, logger)

	gw.Register(MethodDef{
		Name:      "protected.method",
		AuthScope: "read",
		Handler: func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(map[string]string{"status": "ok"})
		},
	})

	// 无 token 应返回 401
	body, _ := json.Marshal(RPCRequest{ID: "1", Method: "protected.method"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	w := httptest.NewRecorder()
	gw.HandleHTTP(w, req)

	var resp RPCResponse
	json.NewDecoder(w.Body).Decode(&resp)
	assert.NotNil(t, resp.Error)
	assert.Equal(t, 401, resp.Error.Code)

	// 有 token 应返回成功
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/rpc", bytes.NewReader(body))
	req2.Header.Set("Authorization", "Bearer secret-token")
	w2 := httptest.NewRecorder()
	gw.HandleHTTP(w2, req2)

	var resp2 RPCResponse
	json.NewDecoder(w2.Body).Decode(&resp2)
	assert.Nil(t, resp2.Error)
}
