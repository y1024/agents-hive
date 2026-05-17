package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chef-guo/agents-hive/internal/asset"
	"github.com/chef-guo/agents-hive/internal/auth"
	"go.uber.org/zap"
)

func TestHandleResolveAssetRequiresOwnerAndResolver(t *testing.T) {
	ctx := context.Background()
	local, err := asset.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	svc, err := asset.NewService(local, newAPIAssetMetaStore())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	uri, err := svc.Upload(ctx, []byte("image"), asset.UploadOpts{
		Namespace:  "im/user/u1",
		Filename:   "image.png",
		MimeType:   "image/png",
		OwnerScope: "user",
		OwnerID:    "u1",
	})
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	srv := &Server{assetService: svc, logger: zap.NewNop()}
	srv.assetProxySecret = []byte("test-secret")
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/assets/resolve?uri="+uri.String(), nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "u1", Status: "active"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing resolver status = %d, want %d body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	srv.assetAccessResolver = asset.AccessResolverFunc(func(context.Context, *asset.AssetRecord, asset.ResolveContext) error {
		return asset.ErrAccessDenied
	})
	req = httptest.NewRequest(http.MethodGet, "/api/v1/assets/resolve?uri="+uri.String(), nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "u1", Status: "active"}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("blocked resolve status = %d, want %d body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	srv.assetAccessResolver = asset.AllowAllResolver{}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/assets/resolve?uri="+uri.String()+"&ttl=120", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "u1", Status: "active"}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed resolve status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resolved resolveAssetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resolved); err != nil {
		t.Fatalf("decode resolve response error = %v body=%s", err, rec.Body.String())
	}
	if !strings.HasPrefix(resolved.URL, "/api/v1/assets/proxy?") || strings.Contains(resolved.URL, "local://") || strings.Contains(resolved.URL, "file://") {
		t.Fatalf("resolve should return proxy URL without local provider details: %+v", resolved)
	}
	req = httptest.NewRequest(http.MethodGet, resolved.URL, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "image" {
		t.Fatalf("proxy status=%d body=%q", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/assets/resolve?uri="+uri.String()+"&owner_scope=user&owner_id=u1", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "u1", Status: "active"}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("query owner status = %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/assets/resolve?uri="+uri.String(), nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("auth-disabled resolve for non-local owner status = %d, want %d body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestHandleResolveAssetAllowsLocalOwnerWhenAuthDisabled(t *testing.T) {
	ctx := context.Background()
	local, err := asset.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	svc, err := asset.NewService(local, newAPIAssetMetaStore())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	uri, err := svc.Upload(ctx, []byte("local artifact"), asset.UploadOpts{
		Namespace:  "agent/user/local/session/s1",
		Filename:   "artifact.md",
		MimeType:   "text/markdown",
		OwnerScope: "user",
		OwnerID:    "local",
		Tags: map[string]string{
			"source_kind": "agent_artifact",
			"session_id":  "s1",
		},
	})
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	srv := &Server{assetService: svc, assetAccessResolver: asset.AllowAllResolver{}, logger: zap.NewNop()}
	srv.assetProxySecret = []byte("test-secret")
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/assets/resolve?uri="+uri.String()+"&purpose=agent_artifact&session_id=s1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleResolveAgentArtifactRequiresSessionID(t *testing.T) {
	ctx := context.Background()
	local, err := asset.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	svc, err := asset.NewService(local, newAPIAssetMetaStore())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	uri, err := svc.Upload(ctx, []byte("artifact"), asset.UploadOpts{
		Namespace:  "agent/user/u1/session/s1",
		Filename:   "artifact.md",
		MimeType:   "text/markdown",
		OwnerScope: "user",
		OwnerID:    "u1",
		Tags: map[string]string{
			"source_kind": "agent_artifact",
			"session_id":  "s1",
		},
	})
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	srv := &Server{
		assetService: svc,
		assetAccessResolver: asset.AccessResolverFunc(func(_ context.Context, rec *asset.AssetRecord, rc asset.ResolveContext) error {
			if rec.Tags["source_kind"] == "agent_artifact" {
				if rc.SessionID == "" || rec.Tags["session_id"] != rc.SessionID {
					return asset.ErrAccessDenied
				}
			}
			return nil
		}),
		logger: zap.NewNop(),
	}
	srv.assetProxySecret = []byte("test-secret")
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/assets/resolve?uri="+uri.String()+"&purpose=agent_artifact", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "u1", Status: "active"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing session status = %d, want 403 body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/assets/resolve?uri="+uri.String()+"&purpose=agent_artifact&session_id=s1", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "u1", Status: "active"}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("matching session status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleResolveChatAttachmentRequiresSessionID(t *testing.T) {
	ctx := context.Background()
	local, err := asset.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	svc, err := asset.NewService(local, newAPIAssetMetaStore())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	uri, err := svc.Upload(ctx, []byte("attachment"), asset.UploadOpts{
		Namespace:  "chat/user/u1/session/s1",
		Filename:   "attachment.txt",
		MimeType:   "text/plain",
		OwnerScope: "user",
		OwnerID:    "u1",
		Tags: map[string]string{
			"source_kind": "chat_attachment",
			"session_id":  "s1",
		},
	})
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	srv := &Server{
		assetService: svc,
		assetAccessResolver: asset.AccessResolverFunc(func(_ context.Context, rec *asset.AssetRecord, rc asset.ResolveContext) error {
			if rec.Tags["source_kind"] == "chat_attachment" {
				if rc.SessionID == "" || rec.Tags["session_id"] != rc.SessionID {
					return asset.ErrAccessDenied
				}
			}
			return nil
		}),
		logger: zap.NewNop(),
	}
	srv.assetProxySecret = []byte("test-secret")
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/assets/resolve?uri="+uri.String()+"&purpose=chat_attachment", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "u1", Status: "active"}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing session status = %d, want 403 body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/assets/resolve?uri="+uri.String()+"&purpose=chat_attachment&session_id=s1", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "u1", Status: "active"}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("matching session status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}

type apiAssetMetaStore struct {
	mu      sync.Mutex
	byKey   map[string][]*asset.AssetRecord
	byOwner map[string]*asset.AssetRecord
}

func newAPIAssetMetaStore() *apiAssetMetaStore {
	return &apiAssetMetaStore{
		byKey:   map[string][]*asset.AssetRecord{},
		byOwner: map[string]*asset.AssetRecord{},
	}
}

func (s *apiAssetMetaStore) Save(_ context.Context, rec *asset.AssetRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *rec
	if cp.ID == "" {
		cp.ID = "test-" + cp.OwnerID
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}
	*rec = cp
	s.byKey[cp.Key] = append(s.byKey[cp.Key], &cp)
	s.byOwner[apiAssetOwnerHashKey(cp.Namespace, cp.ContentHash, cp.OwnerScope, cp.OwnerID)] = &cp
	return nil
}

func (s *apiAssetMetaStore) GetByKey(_ context.Context, key string) (*asset.AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs := s.byKey[key]
	if len(recs) == 0 {
		return nil, asset.ErrNotFound
	}
	cp := *recs[0]
	return &cp, nil
}

func (s *apiAssetMetaStore) GetByKeyForOwner(_ context.Context, key, ownerScope, ownerID string) (*asset.AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.byKey[key] {
		if rec.OwnerScope == ownerScope && rec.OwnerID == ownerID {
			cp := *rec
			return &cp, nil
		}
	}
	return nil, asset.ErrNotFound
}

func (s *apiAssetMetaStore) GetByHash(_ context.Context, namespace, contentHash string) (*asset.AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, recs := range s.byKey {
		for _, rec := range recs {
			if rec.Namespace == namespace && rec.ContentHash == contentHash {
				cp := *rec
				return &cp, nil
			}
		}
	}
	return nil, asset.ErrNotFound
}

func (s *apiAssetMetaStore) GetByHashForOwner(_ context.Context, namespace, contentHash, ownerScope, ownerID string) (*asset.AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.byOwner[apiAssetOwnerHashKey(namespace, contentHash, ownerScope, ownerID)]
	if rec == nil {
		return nil, asset.ErrNotFound
	}
	cp := *rec
	return &cp, nil
}

func (s *apiAssetMetaStore) ListByNamespace(_ context.Context, namespace string) ([]asset.AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []asset.AssetRecord
	for _, recs := range s.byKey {
		for _, rec := range recs {
			if rec.Namespace == namespace {
				out = append(out, *rec)
			}
		}
	}
	return out, nil
}

func (s *apiAssetMetaStore) Delete(_ context.Context, id string) error {
	return nil
}

func apiAssetOwnerHashKey(namespace, hash, scope, owner string) string {
	return namespace + "\x00" + hash + "\x00" + scope + "\x00" + owner
}
