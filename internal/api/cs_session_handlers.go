package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/chef-guo/agents-hive/internal/cs"
	"github.com/chef-guo/agents-hive/internal/errs"
)

func (s *Server) handleCSCreateSession(w http.ResponseWriter, r *http.Request) {
	service := s.customerService()
	if service == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "客服服务未配置", Code: errs.CodeUnavailable})
		return
	}
	var req cs.CreateSessionInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "无效的请求体", Code: errs.CodeBadRequest})
		return
	}
	req.Scope = s.csOwnerScope(r)
	session, err := service.CreateSession(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), Code: errs.CodeBadRequest})
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) handleCSGetSessionState(w http.ResponseWriter, r *http.Request) {
	service := s.customerService()
	if service == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "客服服务未配置", Code: errs.CodeUnavailable})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "需要客服 session ID", Code: errs.CodeBadRequest})
		return
	}
	snapshot, err := service.GetSessionState(r.Context(), s.csOwnerScope(r), id)
	if err != nil {
		writeCSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleCSCancelSessionEscalation(w http.ResponseWriter, r *http.Request) {
	service := s.customerService()
	if service == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "客服服务未配置", Code: errs.CodeUnavailable})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "需要客服 session ID", Code: errs.CodeBadRequest})
		return
	}
	snapshot, err := service.CancelSessionEscalation(r.Context(), s.csOwnerScope(r), id)
	if err != nil {
		writeCSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func writeCSError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := errs.CodeBadRequest
	if errors.Is(err, cs.ErrNotFound) {
		status = http.StatusNotFound
		code = errs.CodeNotFound
	}
	writeJSON(w, status, ErrorResponse{Error: err.Error(), Code: code})
}
