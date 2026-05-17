package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/errs"
	"github.com/chef-guo/agents-hive/internal/kb"
)

type createKBBindingRequest struct {
	NamespaceID   string `json:"namespace_id"`
	DomainID      string `json:"domain_id"`
	BindingType   string `json:"binding_type"`
	BindingTarget string `json:"binding_target"`
	EffectiveAt   string `json:"effective_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	OwnerScope    string `json:"owner_scope,omitempty"`
	OwnerID       string `json:"owner_id,omitempty"`
}

type updateKBBindingRequest struct {
	Enabled       *bool   `json:"enabled,omitempty"`
	BindingTarget *string `json:"binding_target,omitempty"`
	EffectiveAt   *string `json:"effective_at,omitempty"`
	ExpiresAt     *string `json:"expires_at,omitempty"`
	OwnerScope    string  `json:"owner_scope,omitempty"`
	OwnerID       string  `json:"owner_id,omitempty"`
}

type kbBindingResponse struct {
	ID            string         `json:"id"`
	NamespaceID   string         `json:"namespace_id"`
	DomainID      string         `json:"domain_id"`
	BindingType   kb.BindingType `json:"binding_type"`
	BindingTarget string         `json:"binding_target"`
	Enabled       bool           `json:"enabled"`
	EffectiveAt   time.Time      `json:"effective_at"`
	ExpiresAt     *time.Time     `json:"expires_at,omitempty"`
}

func (s *Server) handleKBListBindings(w http.ResponseWriter, r *http.Request) {
	if !s.requireKBService(w) {
		return
	}
	scope, ok := kbManagementScope(w, r)
	if !ok {
		return
	}
	var enabled *bool
	if raw := strings.TrimSpace(r.URL.Query().Get("enabled")); raw != "" {
		v := raw == "true" || raw == "1"
		enabled = &v
	}
	bindings, err := s.kbService.ListBindingsForManagement(r.Context(), scope, kb.BindingQuery{
		NamespaceID: strings.TrimSpace(r.URL.Query().Get("namespace_id")),
		Enabled:     enabled,
	})
	if err != nil {
		s.writeKBError(w, err)
		return
	}
	out := make([]kbBindingResponse, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, kbBindingToResponse(binding))
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": out})
}

func (s *Server) handleKBCreateBinding(w http.ResponseWriter, r *http.Request) {
	if !s.requireKBService(w) {
		return
	}
	scope, ok := kbManagementScope(w, r)
	if !ok {
		return
	}
	var req createKBBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	if rejectsOwnerOverride(req.OwnerScope, req.OwnerID) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "KB binding 不接受 owner 字段", Code: errs.CodeBadRequest})
		return
	}
	bindingType := kb.BindingType(strings.TrimSpace(req.BindingType))
	if (bindingType == kb.BindingTypeTenant || bindingType == kb.BindingTypeSystem) && !isAdminUser(r) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "tenant/system binding 需要管理员权限", Code: errs.CodePermissionDenied})
		return
	}
	effectiveAt, err := parseOptionalKBTime(req.EffectiveAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "effective_at 必须是 RFC3339 时间", Code: errs.CodeBadRequest})
		return
	}
	expiresAt, err := parseOptionalKBTimePtr(req.ExpiresAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "expires_at 必须是 RFC3339 时间", Code: errs.CodeBadRequest})
		return
	}
	domainID := strings.TrimSpace(req.DomainID)
	if domainID == "" {
		domainID = scope.DomainID
	}
	binding, err := s.kbService.CreateBinding(r.Context(), scope, kb.CreateBindingInput{
		NamespaceID:   strings.TrimSpace(req.NamespaceID),
		DomainID:      domainID,
		BindingType:   bindingType,
		BindingTarget: strings.TrimSpace(req.BindingTarget),
		EffectiveAt:   effectiveAt,
		ExpiresAt:     expiresAt,
		CreatedBy:     auth.UserIDFrom(r.Context()),
	})
	if err != nil {
		s.writeKBError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, kbBindingToResponse(*binding))
}

func (s *Server) handleKBUpdateBinding(w http.ResponseWriter, r *http.Request) {
	if !s.requireKBService(w) {
		return
	}
	bindingID := strings.TrimSpace(r.PathValue("id"))
	if bindingID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "需要 binding ID", Code: errs.CodeBadRequest})
		return
	}
	scope, ok := kbManagementScope(w, r)
	if !ok {
		return
	}
	var req updateKBBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	if rejectsOwnerOverride(req.OwnerScope, req.OwnerID) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "KB binding 不接受 owner 字段", Code: errs.CodeBadRequest})
		return
	}
	effectiveAt, err := parseOptionalKBTimePointer(req.EffectiveAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "effective_at 必须是 RFC3339 时间", Code: errs.CodeBadRequest})
		return
	}
	expiresAt, err := parseOptionalKBTimeDoublePointer(req.ExpiresAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "expires_at 必须是 RFC3339 时间", Code: errs.CodeBadRequest})
		return
	}
	binding, err := s.kbService.UpdateBinding(r.Context(), scope, bindingID, kb.UpdateBindingInput{
		Enabled:       req.Enabled,
		BindingTarget: req.BindingTarget,
		EffectiveAt:   effectiveAt,
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		s.writeKBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, kbBindingToResponse(*binding))
}

func (s *Server) handleKBDeleteBinding(w http.ResponseWriter, r *http.Request) {
	if !s.requireKBService(w) {
		return
	}
	bindingID := strings.TrimSpace(r.PathValue("id"))
	if bindingID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "需要 binding ID", Code: errs.CodeBadRequest})
		return
	}
	scope, ok := kbManagementScope(w, r)
	if !ok {
		return
	}
	binding, err := s.kbService.DisableBinding(r.Context(), scope, bindingID)
	if err != nil {
		s.writeKBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, kbBindingToResponse(*binding))
}

func (s *Server) handleKBEffectiveBindings(w http.ResponseWriter, r *http.Request) {
	if !s.requireKBService(w) {
		return
	}
	scope, ok := kbManagementScope(w, r)
	if !ok {
		return
	}
	effective, err := s.kbService.EffectiveBindings(r.Context(), kb.BindingResolveInput{
		DomainID:          scope.DomainID,
		OwnerScope:        scope.OwnerScope,
		OwnerID:           scope.OwnerID,
		UserID:            auth.UserIDFrom(r.Context()),
		TenantID:          strings.TrimSpace(r.URL.Query().Get("tenant_id")),
		AgentID:           strings.TrimSpace(r.URL.Query().Get("agent_id")),
		SessionTemplateID: strings.TrimSpace(r.URL.Query().Get("session_template_id")),
		SessionID:         strings.TrimSpace(r.URL.Query().Get("session_id")),
		Now:               scope.Now,
	})
	if err != nil {
		s.writeKBError(w, err)
		return
	}
	out := make([]kbBindingResponse, 0, len(effective))
	for _, item := range effective {
		out = append(out, kbBindingToResponse(item.Binding))
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": out})
}

func kbBindingToResponse(binding kb.Binding) kbBindingResponse {
	return kbBindingResponse{
		ID:            binding.ID,
		NamespaceID:   binding.NamespaceID,
		DomainID:      binding.DomainID,
		BindingType:   binding.BindingType,
		BindingTarget: binding.BindingTarget,
		Enabled:       binding.Enabled,
		EffectiveAt:   binding.EffectiveAt,
		ExpiresAt:     binding.ExpiresAt,
	}
}

func rejectsOwnerOverride(scope, id string) bool {
	return strings.TrimSpace(scope) != "" || strings.TrimSpace(id) != ""
}

func isAdminUser(r *http.Request) bool {
	user := auth.UserFrom(r.Context())
	return user != nil && user.Role == "admin"
}

func parseOptionalKBTime(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(raw))
}

func parseOptionalKBTimePtr(raw string) (*time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseOptionalKBTimePointer(raw *string) (*time.Time, error) {
	if raw == nil {
		return nil, nil
	}
	parsed, err := parseOptionalKBTime(*raw)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseOptionalKBTimeDoublePointer(raw *string) (**time.Time, error) {
	if raw == nil {
		return nil, nil
	}
	parsed, err := parseOptionalKBTimePtr(*raw)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
