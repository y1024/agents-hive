package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chef-guo/agents-hive/internal/asset"
	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/errs"
)

const (
	defaultAssetSignedURLTTL = 5 * time.Minute
	maxAssetSignedURLTTL     = 15 * time.Minute
)

type resolveAssetResponse struct {
	URL       string `json:"url"`
	ExpiresIn int64  `json:"expires_in"`
	MimeType  string `json:"mime_type"`
	Filename  string `json:"filename,omitempty"`
	Size      int64  `json:"size"`
}

type assetGCResponse struct {
	Scanned int      `json:"scanned"`
	Orphans int      `json:"orphans"`
	Deleted int      `json:"deleted"`
	Keys    []string `json:"keys,omitempty"`
	DryRun  bool     `json:"dry_run"`
}

func (s *Server) handleResolveAsset(w http.ResponseWriter, r *http.Request) {
	if s.assetService == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "资产服务未初始化", Code: errs.CodeInternal})
		return
	}
	if s.assetAccessResolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "资产访问策略未配置", Code: errs.CodeInternal})
		return
	}
	uri := asset.AssetURI(strings.TrimSpace(r.URL.Query().Get("uri")))
	if uri == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "需要 asset uri", Code: errs.CodeBadRequest})
		return
	}
	rc, err := resolveContextFromRequest(r)
	if err != nil {
		status := http.StatusForbidden
		if errors.Is(err, asset.ErrInvalidUploadOpts) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error(), Code: errs.CodePermissionDenied})
		return
	}
	ttl := parseAssetTTL(r.URL.Query().Get("ttl"))
	url, rec, err := s.assetService.ResolveAsset(r.Context(), uri, rc, s.assetAccessResolver, ttl)
	if err != nil {
		status := http.StatusInternalServerError
		code := errs.CodeInternal
		switch {
		case errors.Is(err, asset.ErrInvalidAssetURI), errors.Is(err, asset.ErrInvalidObjectKey):
			status = http.StatusBadRequest
			code = errs.CodeBadRequest
		case errors.Is(err, asset.ErrNotFound):
			status = http.StatusNotFound
			code = errs.CodeNotFound
		case errors.Is(err, asset.ErrAccessDenied):
			status = http.StatusForbidden
			code = errs.CodePermissionDenied
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error(), Code: code})
		return
	}
	url, err = s.rewriteLocalAssetURL(url, ttl)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "资产代理链接生成失败", Code: errs.CodeInternal})
		return
	}
	writeJSON(w, http.StatusOK, resolveAssetResponse{
		URL:       url,
		ExpiresIn: int64(ttl / time.Second),
		MimeType:  rec.MimeType,
		Filename:  rec.Filename,
		Size:      rec.Size,
	})
}

func (s *Server) handleProxyAsset(w http.ResponseWriter, r *http.Request) {
	if s.assetService == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "资产服务未初始化", Code: errs.CodeInternal})
		return
	}
	uri := asset.AssetURI(strings.TrimSpace(r.URL.Query().Get("uri")))
	expires := strings.TrimSpace(r.URL.Query().Get("expires"))
	sig := strings.TrimSpace(r.URL.Query().Get("sig"))
	if uri == "" || expires == "" || sig == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "资产代理参数不完整", Code: errs.CodeBadRequest})
		return
	}
	exp, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "资产代理链接已过期", Code: errs.CodePermissionDenied})
		return
	}
	if !s.verifyAssetProxySignature(uri.String(), expires, sig) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "资产代理签名无效", Code: errs.CodePermissionDenied})
		return
	}
	data, rec, err := s.assetService.Download(r.Context(), uri)
	if err != nil {
		status := http.StatusInternalServerError
		code := errs.CodeInternal
		switch {
		case errors.Is(err, asset.ErrInvalidAssetURI), errors.Is(err, asset.ErrInvalidObjectKey):
			status = http.StatusBadRequest
			code = errs.CodeBadRequest
		case errors.Is(err, asset.ErrNotFound):
			status = http.StatusNotFound
			code = errs.CodeNotFound
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error(), Code: code})
		return
	}
	if rec.MimeType != "" {
		w.Header().Set("Content-Type", rec.MimeType)
	}
	if rec.Filename != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", rec.Filename))
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	_, _ = w.Write(data)
}

func (s *Server) handleAdminAssetGC(w http.ResponseWriter, r *http.Request) {
	if s.assetService == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "资产服务未初始化", Code: errs.CodeInternal})
		return
	}
	dryRun := true
	rawDryRun := strings.TrimSpace(r.URL.Query().Get("dry_run"))
	if rawDryRun != "" {
		dryRun = rawDryRun != "false" && rawDryRun != "0"
	}
	result, err := s.assetService.GCOrphanObjects(r.Context(), asset.GCOptions{
		Prefix: strings.TrimSpace(r.URL.Query().Get("prefix")),
		DryRun: dryRun,
	})
	if err != nil {
		status := http.StatusInternalServerError
		code := errs.CodeInternal
		if errors.Is(err, asset.ErrInvalidNamespace) {
			status = http.StatusBadRequest
			code = errs.CodeBadRequest
		}
		writeJSON(w, status, ErrorResponse{Error: err.Error(), Code: code})
		return
	}
	writeJSON(w, http.StatusOK, assetGCResponse{
		Scanned: len(result.ScannedKeys),
		Orphans: len(result.OrphanKeys),
		Deleted: len(result.DeletedKeys),
		Keys:    result.OrphanKeys,
		DryRun:  dryRun,
	})
}

func resolveContextFromRequest(r *http.Request) (asset.ResolveContext, error) {
	q := r.URL.Query()
	rc := asset.ResolveContext{
		SessionID: strings.TrimSpace(q.Get("session_id")),
		DomainID:  strings.TrimSpace(q.Get("domain_id")),
		Purpose:   strings.TrimSpace(q.Get("purpose")),
		Extra: map[string]string{
			"tenant_id":           strings.TrimSpace(q.Get("tenant_id")),
			"agent_id":            strings.TrimSpace(q.Get("agent_id")),
			"session_template_id": strings.TrimSpace(q.Get("session_template_id")),
		},
	}
	if strings.TrimSpace(q.Get("owner_scope")) != "" || strings.TrimSpace(q.Get("owner_id")) != "" {
		return asset.ResolveContext{}, asset.ErrInvalidUploadOpts
	}
	user := auth.UserFrom(r.Context())
	if user == nil || strings.TrimSpace(user.ID) == "" {
		if auth.IsAuthEnabled(r.Context()) {
			return asset.ResolveContext{}, asset.ErrAccessDenied
		}
		rc.UserID = localAssetOwnerID
		rc.OwnerScope = "user"
		rc.OwnerID = localAssetOwnerID
		if rc.Purpose == "" {
			rc.Purpose = "local_asset"
		}
		return rc, nil
	}
	rc.UserID = user.ID
	rc.OwnerScope = "user"
	rc.OwnerID = user.ID
	if rc.Purpose == "" {
		rc.Purpose = "user_asset"
	}
	return rc, nil
}

const localAssetOwnerID = "local"

func parseAssetTTL(raw string) time.Duration {
	if raw == "" {
		return defaultAssetSignedURLTTL
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return defaultAssetSignedURLTTL
	}
	ttl := time.Duration(seconds) * time.Second
	if ttl > maxAssetSignedURLTTL {
		return maxAssetSignedURLTTL
	}
	return ttl
}

func (s *Server) rewriteLocalAssetURL(rawURL string, ttl time.Duration) (string, error) {
	if !strings.HasPrefix(rawURL, "local://asset/") {
		return rawURL, nil
	}
	expires := fmt.Sprintf("%d", time.Now().Add(ttl).Unix())
	q := make([]string, 0, 3)
	keyWithQuery := strings.TrimPrefix(rawURL, "local://asset/")
	key, _, _ := strings.Cut(keyWithQuery, "?")
	proxyURI, err := asset.AssetURIFromObjectKey(key)
	if err != nil {
		return "", err
	}
	q = append(q, "uri="+url.QueryEscape(proxyURI.String()))
	q = append(q, "expires="+expires)
	q = append(q, "sig="+s.signAssetProxy(proxyURI.String(), expires))
	return "/api/v1/assets/proxy?" + strings.Join(q, "&"), nil
}

func (s *Server) signAssetProxy(uri, expires string) string {
	mac := hmac.New(sha256.New, s.assetProxySecret)
	_, _ = mac.Write([]byte(uri))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(expires))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) verifyAssetProxySignature(uri, expires, sig string) bool {
	want := s.signAssetProxy(uri, expires)
	return hmac.Equal([]byte(sig), []byte(want))
}
