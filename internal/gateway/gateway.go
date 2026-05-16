package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/errs"
)

// RPCRequest JSON-RPC 风格请求
type RPCRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// RPCResponse JSON-RPC 风格响应
type RPCResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// RPCError RPC 错误信息
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MethodHandler RPC 方法处理函数
type MethodHandler func(ctx context.Context, params json.RawMessage) (json.RawMessage, error)

// MethodDef 方法定义
type MethodDef struct {
	Name        string
	Description string
	Handler     MethodHandler
	AuthScope   string // "read", "write", "admin"；空字符串=无需认证
}

// Gateway 统一 RPC 网关
type Gateway struct {
	methods            map[string]MethodDef
	auth               *AuthManager
	rateLimiter        *IPRateLimiter // IP 速率限制器，防止暴力破解认证端点
	logger             *zap.Logger
	mu                 sync.RWMutex
	insecureSkipVerify bool // 跳过 WebSocket 来源检查（仅开发环境）
}

// defaultRateLimit 默认速率限制：每分钟 60 次请求
const defaultRateLimit = 60

// defaultMaxBodySize 默认请求体大小限制：10MB
const defaultMaxBodySize = 10 * 1024 * 1024

// New 创建新的 Gateway 实例，内置 IP 速率限制（每分钟 60 次）
func New(auth *AuthManager, logger *zap.Logger) *Gateway {
	return &Gateway{
		methods:            make(map[string]MethodDef),
		auth:               auth,
		rateLimiter:        NewIPRateLimiter(defaultRateLimit, time.Minute),
		logger:             logger,
		insecureSkipVerify: false,
	}
}

// SetInsecureSkipVerify 设置是否跳过 WebSocket 来源检查（仅开发环境使用）
func (g *Gateway) SetInsecureSkipVerify(skip bool) {
	g.insecureSkipVerify = skip
}

// Register 注册 RPC 方法
func (g *Gateway) Register(method MethodDef) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.methods[method.Name] = method
}

// HandleHTTP 处理 HTTP RPC 请求 (POST /api/v1/rpc)
func (g *Gateway) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	// IP 速率限制检查：防止暴力破解认证端点。
	// 使用速率限制器自身的 ExtractClientIP，确保代理头信任策略一致。
	clientIP := g.rateLimiter.ExtractClientIP(r)
	if !g.rateLimiter.Allow(clientIP) {
		g.logger.Warn("HTTP 请求超出速率限制",
			zap.String("client_ip", clientIP),
			zap.String("path", r.URL.Path),
		)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"请求过于频繁，请稍后重试","code":429}`))
		return
	}

	token := r.Header.Get("Authorization")
	authToken, authErr := g.auth.Authenticate(token)
	if authErr != nil {
		g.logger.Debug("HTTP 认证失败", zap.Error(authErr))
	}

	// 请求体大小限制，防止超大 JSON 载荷导致资源耗尽
	r.Body = http.MaxBytesReader(w, r.Body, defaultMaxBodySize)

	var req RPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// 区分请求体过大和格式错误
		if err.Error() == "http: request body too large" {
			writeRPCError(w, "", 413, "请求体超出大小限制")
			return
		}
		writeRPCError(w, "", 400, "请求体无效")
		return
	}

	resp := g.dispatch(r.Context(), req, authToken)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleWebSocket 处理 WebSocket RPC 连接 (GET /api/v1/rpc/ws)
func (g *Gateway) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// IP 速率限制检查：防止暴力破解 WebSocket 认证端点。
	// 使用速率限制器自身的 ExtractClientIP，确保代理头信任策略一致。
	clientIP := g.rateLimiter.ExtractClientIP(r)
	if !g.rateLimiter.Allow(clientIP) {
		g.logger.Warn("WebSocket 请求超出速率限制",
			zap.String("client_ip", clientIP),
		)
		http.Error(w, "请求过于频繁，请稍后重试", http.StatusTooManyRequests)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: g.insecureSkipVerify,
	})
	if err != nil {
		g.logger.Error("WebSocket 连接接受失败", zap.Error(err))
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	token := r.URL.Query().Get("token")
	authToken, authErr := g.auth.Authenticate(token)
	if authErr != nil {
		g.logger.Warn("WebSocket 认证失败", zap.Error(authErr))
	}

	for {
		var req RPCRequest
		if err := wsjson.Read(ctx, conn, &req); err != nil {
			return
		}
		resp := g.dispatch(ctx, req, authToken)
		if err := wsjson.Write(ctx, conn, resp); err != nil {
			return
		}
	}
}

func (g *Gateway) dispatch(ctx context.Context, req RPCRequest, authToken *AuthToken) RPCResponse {
	// 验证请求字段
	if req.Method == "" {
		return RPCResponse{ID: req.ID, Error: &RPCError{Code: 400, Message: "缺少 method 字段"}}
	}
	if req.ID == "" {
		return RPCResponse{ID: req.ID, Error: &RPCError{Code: 400, Message: "缺少 id 字段"}}
	}

	g.mu.RLock()
	method, ok := g.methods[req.Method]
	g.mu.RUnlock()

	if !ok {
		return RPCResponse{ID: req.ID, Error: &RPCError{Code: 404, Message: "方法未找到: " + req.Method}}
	}

	if method.AuthScope != "" && !g.hasScope(ctx, authToken, method.AuthScope) {
		return RPCResponse{ID: req.ID, Error: &RPCError{Code: 401, Message: "未授权"}}
	}

	// 当 Gateway 配置了 token（非开放模式）时，标记 auth 已启用。
	// 这样 checkSessionAccess 能正确区分"auth 启用但无 user"→拒绝，
	// 而不是误判为"auth 未启用"→放行所有 session。
	// Gateway token 是机器级 token，无 user 维度，因此不注入 WithUser。
	// 结果：Gateway 调用 session 相关方法时，若系统 auth 已启用，
	// 需要 session 的 user_id 为空（遗留无主 session）才能访问。
	g.auth.mu.RLock()
	hasTokens := len(g.auth.tokens) > 0
	g.auth.mu.RUnlock()
	if hasTokens {
		ctx = auth.WithAuthEnabled(ctx)
	}

	result, err := method.Handler(ctx, req.Params)
	if err != nil {
		g.logger.Error("RPC 方法执行失败",
			zap.String("method", req.Method),
			zap.String("request_id", req.ID),
			zap.Error(err))
		return RPCResponse{ID: req.ID, Error: rpcErrorFromError(err)}
	}
	return RPCResponse{ID: req.ID, Result: result}
}

func (g *Gateway) hasScope(ctx context.Context, authToken *AuthToken, scope string) bool {
	// 明确的 Gateway 机器令牌始终按 gateway.tokens scope 判定。
	if authToken != nil && g.auth.HasScope(authToken, scope) {
		return true
	}

	user := auth.UserFrom(ctx)
	if user != nil && user.Status == "active" {
		if user.Role == "admin" {
			return true
		}
		return scope == "read"
	}

	// 只有整个 HTTP auth 未启用时，才允许 Gateway 的无 token 本地开放模式。
	// 若 WebUI/JWT auth 已启用，即使 gateway.tokens 为空，也不能让 RPC 管理方法匿名开放。
	if auth.IsAuthEnabled(ctx) {
		return false
	}
	return g.auth.HasScope(authToken, scope)
}

func rpcErrorFromError(err error) *RPCError {
	var appErr *errs.Error
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case errs.CodeInvalidInput, errs.CodeInvalidArgument, errs.CodeBadRequest, errs.CodeInvalidRequest:
			return &RPCError{Code: 400, Message: appErr.Message}
		case errs.CodeNotFound:
			return &RPCError{Code: 404, Message: appErr.Message}
		case errs.CodePermissionDenied:
			return &RPCError{Code: 403, Message: appErr.Message}
		}
	}
	return &RPCError{Code: 500, Message: "内部服务错误"}
}

func writeRPCError(w http.ResponseWriter, id string, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(RPCResponse{
		ID:    id,
		Error: &RPCError{Code: code, Message: msg},
	})
}
