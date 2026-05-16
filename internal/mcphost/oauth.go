package mcphost

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/errs"
)

// OAuthConfig OAuth 配置
// 与 config.OAuthConfig 结构一致，避免循环依赖（config → llm → mcphost → config）
type OAuthConfig struct {
	ClientID     string   `json:"client_id" yaml:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty" yaml:"client_secret,omitempty"`
	AuthURL      string   `json:"auth_url" yaml:"auth_url"`
	TokenURL     string   `json:"token_url" yaml:"token_url"`
	Scopes       []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
}

// OAuthToken OAuth token
type OAuthToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Scopes       string    `json:"scopes,omitempty"`
}

// TokenStore token 持久化接口
type TokenStore interface {
	SaveToken(ctx context.Context, serverURL string, token *OAuthToken) error
	LoadToken(ctx context.Context, serverURL string) (*OAuthToken, error)
	DeleteToken(ctx context.Context, serverURL string) error
}

// OAuthClient OAuth PKCE 客户端
type OAuthClient struct {
	config     OAuthConfig
	tokenStore TokenStore
	logger     *zap.Logger
}

// NewOAuthClient 创建 OAuth 客户端
func NewOAuthClient(config OAuthConfig, store TokenStore, logger *zap.Logger) *OAuthClient {
	return &OAuthClient{
		config:     config,
		tokenStore: store,
		logger:     logger,
	}
}

// GetAccessToken 获取有效的 access token
// 1. 先从 store 加载缓存的 token
// 2. 如果 token 未过期，直接返回
// 3. 如果有 refresh_token，尝试刷新
// 4. 否则启动完整的 PKCE 流程
func (c *OAuthClient) GetAccessToken(ctx context.Context, serverURL string) (string, error) {
	// 尝试从缓存加载
	cached, err := c.tokenStore.LoadToken(ctx, serverURL)
	if err == nil && cached != nil {
		// 检查是否过期（留 30 秒缓冲）
		if !cached.ExpiresAt.IsZero() && time.Now().After(cached.ExpiresAt.Add(-30*time.Second)) {
			c.logger.Info("OAuth token 即将过期，尝试刷新", zap.String("server_url", safeURLForLog(serverURL)))

			// 尝试用 refresh_token 刷新
			if cached.RefreshToken != "" {
				newToken, refreshErr := c.refreshToken(ctx, cached.RefreshToken)
				if refreshErr == nil {
					if saveErr := c.tokenStore.SaveToken(ctx, serverURL, newToken); saveErr != nil {
						c.logger.Warn("保存刷新后的 token 失败", zap.Error(saveErr))
					}
					return newToken.TokenType + " " + newToken.AccessToken, nil
				}
				c.logger.Warn("刷新 token 失败，将重新授权", zap.Error(refreshErr))
			}
		} else {
			// token 有效
			return cached.TokenType + " " + cached.AccessToken, nil
		}
	}

	// 执行完整的 PKCE 授权流程
	token, err := c.AuthorizePKCE(ctx)
	if err != nil {
		return "", err
	}

	// 保存到 store
	if saveErr := c.tokenStore.SaveToken(ctx, serverURL, token); saveErr != nil {
		c.logger.Warn("保存 OAuth token 失败", zap.Error(saveErr))
	}

	return token.TokenType + " " + token.AccessToken, nil
}

// AuthorizePKCE 执行 PKCE 授权流程
// 1. 生成 code_verifier (43-128 字符, RFC 7636)
// 2. code_challenge = BASE64URL(SHA256(code_verifier))
// 3. 启动本地 HTTP 回调服务器
// 4. 构建授权 URL 并打印让用户手动打开
// 5. 等待回调收到 authorization code
// 6. 用 code + code_verifier 交换 token
func (c *OAuthClient) AuthorizePKCE(ctx context.Context) (*OAuthToken, error) {
	// 生成 PKCE 参数
	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		return nil, errs.Wrap(errs.CodeMCPOAuthFailed, "生成 PKCE code_verifier 失败", err)
	}
	codeChallenge := generateCodeChallenge(codeVerifier)

	// 启动本地回调服务器
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errs.Wrap(errs.CodeMCPOAuthFailed, "启动本地回调服务器失败", err)
	}
	defer listener.Close()

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return nil, errs.New(errs.CodeMCPOAuthFailed, "无法获取监听端口")
	}
	port := tcpAddr.Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	// 等待授权码的 channel
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	// 配置回调处理器
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errMsg := r.URL.Query().Get("error")
			if errMsg == "" {
				errMsg = "未收到授权码"
			}
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "授权失败: %s", errMsg)
			select {
			case errCh <- errs.New(errs.CodeMCPOAuthFailed, "OAuth 授权失败: "+errMsg):
			default:
			}
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><body><h2>授权成功！</h2><p>请返回终端继续操作。</p></body></html>")
		select {
		case codeCh <- code:
		default:
		}
	})

	server := &http.Server{Handler: mux}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			c.logger.Error("回调服务器异常退出", zap.Error(serveErr))
		}
	}()
	defer server.Close()

	// 构建授权 URL
	authParams := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.config.ClientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	if len(c.config.Scopes) > 0 {
		authParams.Set("scope", strings.Join(c.config.Scopes, " "))
	}

	authURL := c.config.AuthURL + "?" + authParams.Encode()
	fmt.Println("请在浏览器中打开以下链接完成授权:")
	fmt.Println(authURL)

	// 等待授权码（60 秒超时）
	timeout := 60 * time.Second
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-timeoutCtx.Done():
		return nil, errs.New(errs.CodeMCPOAuthFailed, "等待 OAuth 授权超时（60秒）")
	case err := <-errCh:
		return nil, err
	case code := <-codeCh:
		c.logger.Info("收到 OAuth 授权码")
		return c.exchangeCode(ctx, code, codeVerifier, redirectURI)
	}
}

// tokenResponse token 端点的响应结构
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

// refreshToken 使用 refresh_token 获取新的 access_token
func (c *OAuthClient) refreshToken(ctx context.Context, refreshTok string) (*OAuthToken, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshTok},
		"client_id":     {c.config.ClientID},
	}
	if c.config.ClientSecret != "" {
		data.Set("client_secret", c.config.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.TokenURL,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, errs.Wrap(errs.CodeMCPOAuthFailed, "创建刷新 token 请求失败", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errs.Wrap(errs.CodeMCPOAuthFailed, "刷新 token 请求失败", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, errs.Wrap(errs.CodeMCPOAuthFailed, "读取刷新 token 响应失败", err)
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, errs.Wrap(errs.CodeMCPOAuthFailed, "解析刷新 token 响应失败", err)
	}

	if tokenResp.Error != "" {
		return nil, errs.New(errs.CodeMCPOAuthFailed,
			fmt.Sprintf("刷新 token 失败: %s (%s)", tokenResp.Error, tokenResp.ErrorDesc))
	}

	token := &OAuthToken{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		Scopes:       tokenResp.Scope,
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}
	// 如果服务端未返回新的 refresh_token，保留原来的
	if token.RefreshToken == "" {
		token.RefreshToken = refreshTok
	}

	return token, nil
}

// exchangeCode 用 authorization code 交换 token
func (c *OAuthClient) exchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (*OAuthToken, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {c.config.ClientID},
		"code_verifier": {codeVerifier},
	}
	if c.config.ClientSecret != "" {
		data.Set("client_secret", c.config.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.TokenURL,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, errs.Wrap(errs.CodeMCPOAuthFailed, "创建 token 交换请求失败", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errs.Wrap(errs.CodeMCPOAuthFailed, "token 交换请求失败", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, errs.Wrap(errs.CodeMCPOAuthFailed, "读取 token 交换响应失败", err)
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, errs.Wrap(errs.CodeMCPOAuthFailed, "解析 token 交换响应失败", err)
	}

	if tokenResp.Error != "" {
		return nil, errs.New(errs.CodeMCPOAuthFailed,
			fmt.Sprintf("token 交换失败: %s (%s)", tokenResp.Error, tokenResp.ErrorDesc))
	}

	if tokenResp.AccessToken == "" {
		return nil, errs.New(errs.CodeMCPOAuthFailed, "token 交换响应中缺少 access_token")
	}

	token := &OAuthToken{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		Scopes:       tokenResp.Scope,
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	return token, nil
}

// generateCodeVerifier 生成 PKCE code verifier (RFC 7636)
// 返回 43-128 个字符的随机字符串，由 [A-Z] / [a-z] / [0-9] / "-" / "." / "_" / "~" 组成
func generateCodeVerifier() (string, error) {
	// 生成 32 字节随机数据，base64url 编码后约 43 个字符
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// base64url 无填充编码
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generateCodeChallenge 生成 PKCE code challenge
// code_challenge = BASE64URL(SHA256(code_verifier))
func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
