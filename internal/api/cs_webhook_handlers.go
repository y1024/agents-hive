package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/cs"
	"github.com/chef-guo/agents-hive/internal/errs"
)

func (s *Server) handleCSCreateWebhookSubscription(w http.ResponseWriter, r *http.Request) {
	service := s.customerService()
	if service == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "客服服务未配置", Code: errs.CodeUnavailable})
		return
	}
	var req cs.CreateSubscriptionInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	req.Scope = s.csOwnerScope(r)
	rec, err := service.CreateSubscription(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeBadRequest})
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleCSListWebhookSubscriptions(w http.ResponseWriter, r *http.Request) {
	service := s.customerService()
	if service == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "客服服务未配置", Code: errs.CodeUnavailable})
		return
	}
	items, err := service.ListSubscriptions(r.Context(), s.csOwnerScope(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeStoreError})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCSListDLQ(w http.ResponseWriter, r *http.Request) {
	service := s.customerService()
	if service == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "客服服务未配置", Code: errs.CodeUnavailable})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := service.ListDLQ(r.Context(), s.csOwnerScope(r), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error(), Code: errs.CodeStoreError})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCSRetryDLQ(w http.ResponseWriter, r *http.Request) {
	service := s.customerService()
	if service == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "客服服务未配置", Code: errs.CodeUnavailable})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	rec, err := service.RetryDLQ(r.Context(), s.csOwnerScope(r), id)
	if err != nil {
		writeCSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) customerService() *cs.Service {
	if s == nil {
		return nil
	}
	return s.customerServiceBackend
}

func (s *Server) csOwnerScope(r *http.Request) cs.OwnerScope {
	ownerID := strings.TrimSpace(auth.UserIDFrom(r.Context()))
	if ownerID == "" {
		ownerID = cs.DefaultOwnerID
	}
	return cs.OwnerScope{
		DomainID:   cs.DefaultDomainID,
		OwnerScope: cs.DefaultOwnerScope,
		OwnerID:    ownerID,
	}.Normalized()
}
