package mcphost

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/errs"
)

// MCPServerSpec 描述单个 MCP 服务端的连接参数
// 与 config.MCPServerConfig 结构对应，避免循环依赖（config → llm → mcphost → config）
type MCPServerSpec struct {
	Name      string            // 服务端名称（用于日志）
	Command   string            // stdio 模式的命令
	Args      []string          // stdio 模式的参数
	Env       map[string]string // stdio 模式的附加环境变量（叠加在父进程环境之上）
	Transport string            // "stdio"（默认）| "sse" | "http"
	URL       string            // SSE/HTTP 模式的服务端 URL
	Headers   map[string]string // 自定义 HTTP 头
	Timeout   time.Duration     // 超时时间
	OAuth     *OAuthConfig      // OAuth PKCE 配置（可选）
}

// BuildTransport 根据 MCPServerSpec 创建对应的传输层实例
// 如果配置了 OAuth，会自动创建 OAuthClient 并注入 AuthProvider
// tokenStore 可为 nil，此时 token 不会持久化
func BuildTransport(spec MCPServerSpec, tokenStore TokenStore, logger *zap.Logger) (Transport, error) {
	switch spec.Transport {
	case "sse":
		return buildSSETransport(spec, tokenStore, logger)
	case "http":
		return buildHTTPTransport(spec, tokenStore, logger)
	case "stdio", "":
		return buildStdioTransport(spec, logger)
	default:
		return nil, errs.New(errs.CodeMCPTransportFailed, "不支持的 MCP 传输类型: "+spec.Transport)
	}
}

// buildSSETransport 创建 SSE 传输层实例，包含 OAuth 集成
func buildSSETransport(spec MCPServerSpec, tokenStore TokenStore, logger *zap.Logger) (*SSETransport, error) {
	if spec.URL == "" {
		return nil, errs.New(errs.CodeMCPTransportFailed, "SSE 传输需要指定 URL")
	}

	cfg := SSETransportConfig{
		URL:     spec.URL,
		Headers: spec.Headers,
		Timeout: spec.Timeout,
	}

	// 如果配置了 OAuth，创建 OAuthClient 并设置 AuthProvider
	if spec.OAuth != nil {
		authProvider := buildOAuthProvider(*spec.OAuth, spec.URL, tokenStore, logger)
		cfg.AuthProvider = authProvider
		logger.Info("已为 SSE 传输配置 OAuth PKCE 认证",
			zap.String("服务端", spec.Name),
			zap.String("url", safeURLForLog(spec.URL)),
		)
	}

	return NewSSETransport(cfg, logger), nil
}

// buildHTTPTransport 创建 HTTP 传输层实例，包含 OAuth 集成
func buildHTTPTransport(spec MCPServerSpec, tokenStore TokenStore, logger *zap.Logger) (*HTTPTransport, error) {
	if spec.URL == "" {
		return nil, errs.New(errs.CodeMCPTransportFailed, "HTTP 传输需要指定 URL")
	}

	cfg := HTTPTransportConfig{
		URL:     spec.URL,
		Headers: spec.Headers,
		Timeout: spec.Timeout,
	}

	// 如果配置了 OAuth，创建 OAuthClient 并设置 AuthProvider
	if spec.OAuth != nil {
		authProvider := buildOAuthProvider(*spec.OAuth, spec.URL, tokenStore, logger)
		cfg.AuthProvider = authProvider
		logger.Info("已为 HTTP 传输配置 OAuth PKCE 认证",
			zap.String("服务端", spec.Name),
			zap.String("url", safeURLForLog(spec.URL)),
		)
	}

	return NewHTTPTransport(cfg, logger), nil
}

// buildStdioTransport 创建 stdio 传输层实例
func buildStdioTransport(spec MCPServerSpec, logger *zap.Logger) (*StdioTransport, error) {
	if spec.Command == "" {
		return nil, errs.New(errs.CodeMCPTransportFailed, "stdio 传输需要指定 command")
	}
	return NewStdioTransport(StdioTransportConfig{
		Command: spec.Command,
		Args:    spec.Args,
		Env:     spec.Env,
	}, logger), nil
}

// buildOAuthProvider 创建 OAuth 认证提供者闭包
func buildOAuthProvider(oauthCfg OAuthConfig, serverURL string, tokenStore TokenStore, logger *zap.Logger) func() (string, error) {
	oauthClient := NewOAuthClient(oauthCfg, tokenStore, logger)

	return func() (string, error) {
		token, err := oauthClient.GetAccessToken(context.Background(), serverURL)
		if err != nil {
			return "", err
		}
		return token, nil
	}
}
