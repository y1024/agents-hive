package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/kb"
)

func TestKBCreateBindingRejectsOwnerOverride(t *testing.T) {
	srv := &Server{kbService: &fakeKBManagementService{}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kb/bindings?domain_id=support", strings.NewReader(`{
		"namespace_id":"ns-1",
		"binding_type":"agent",
		"binding_target":"master",
		"owner_id":"other"
	}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "user-1", Role: "admin"}))
	rec := httptest.NewRecorder()

	srv.handleKBCreateBinding(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestKBCreateBindingRejectsTenantForNonAdmin(t *testing.T) {
	srv := &Server{kbService: &fakeKBManagementService{}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kb/bindings?domain_id=support", strings.NewReader(`{
		"namespace_id":"ns-1",
		"binding_type":"tenant",
		"binding_target":"tenant-1"
	}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "user-1", Role: "user"}))
	rec := httptest.NewRecorder()

	srv.handleKBCreateBinding(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}

func TestKBCreateBindingDerivesOwnerFromAuthUser(t *testing.T) {
	service := &fakeKBManagementService{}
	srv := &Server{kbService: service}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kb/bindings?domain_id=support", strings.NewReader(`{
		"namespace_id":"ns-1",
		"binding_type":"agent",
		"binding_target":"master"
	}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "user-1", Role: "admin"}))
	rec := httptest.NewRecorder()

	srv.handleKBCreateBinding(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, kb.OwnerScopeUser, service.lastMgmtScope.OwnerScope)
	require.Equal(t, "user-1", service.lastMgmtScope.OwnerID)
	require.Equal(t, "support", service.lastMgmtScope.DomainID)
}

func TestKBCreateBindingRejectsDomainMismatch(t *testing.T) {
	srv := &Server{kbService: &fakeKBManagementService{
		createBindingFn: func(ctx context.Context, scope kb.ManagementScope, input kb.CreateBindingInput) (*kb.Binding, error) {
			if input.DomainID != scope.DomainID {
				return nil, kb.ErrInvalidInput
			}
			return &kb.Binding{ID: "bind-1", NamespaceID: input.NamespaceID, DomainID: input.DomainID, OwnerScope: scope.OwnerScope, OwnerID: scope.OwnerID, BindingType: input.BindingType, BindingTarget: input.BindingTarget, Enabled: true, EffectiveAt: time.Now()}, nil
		},
	}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kb/bindings?domain_id=support", strings.NewReader(`{
		"namespace_id":"ns-1",
		"domain_id":"generic",
		"binding_type":"agent",
		"binding_target":"master"
	}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "user-1", Role: "admin"}))
	rec := httptest.NewRecorder()

	srv.handleKBCreateBinding(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestKBDeleteBindingSoftDisables(t *testing.T) {
	srv := &Server{kbService: &fakeKBManagementService{}}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/kb/bindings/bind-1?domain_id=support", nil)
	req.SetPathValue("id", "bind-1")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "user-1", Role: "admin"}))
	rec := httptest.NewRecorder()

	srv.handleKBDeleteBinding(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"enabled":false`)
}

func TestKBEffectiveBindingsReturnsResolverResult(t *testing.T) {
	srv := &Server{kbService: &fakeKBManagementService{}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/kb/effective-bindings?domain_id=support&agent_id=master", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "user-1", Role: "admin"}))
	rec := httptest.NewRecorder()

	srv.handleKBEffectiveBindings(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"namespace_id":"ns-1"`)
}
