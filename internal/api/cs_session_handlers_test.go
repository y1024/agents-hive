package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/cs"
)

func TestCSSessionHandlersCreateStateAndCancel(t *testing.T) {
	store := cs.NewMemoryStore()
	service := cs.NewService(store)
	srv := NewServer(config.ServerConfig{Host: "127.0.0.1", Port: 0}, config.HITLConfig{}, config.WebUIConfig{}, nil, nil, nil, "", nil, nil, nil, zap.NewNop())
	srv.SetCustomerService(service)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/cs/sessions", strings.NewReader(`{"external_user_ref":"buyer-1"}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "u1", Status: "active"}))
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var session cs.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session = %v", err)
	}
	if session.Scope.OwnerID != "u1" || session.State != cs.SessionStateAIHandling {
		t.Fatalf("session = %+v", session)
	}

	_, err := service.CreateEscalation(req.Context(), cs.CreateEscalationInput{
		Scope:     cs.OwnerScope{OwnerID: "u1"},
		SessionID: session.ID,
		Subject:   "need human",
		Summary:   "refund exception",
	})
	if err != nil {
		t.Fatalf("CreateEscalation = %v", err)
	}

	stateReq := httptest.NewRequest(http.MethodGet, "/api/v1/cs/sessions/"+session.ID+"/state", nil)
	stateReq = stateReq.WithContext(auth.WithUser(stateReq.Context(), &auth.User{ID: "u1", Status: "active"}))
	stateRec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(stateRec, stateReq)
	if stateRec.Code != http.StatusOK {
		t.Fatalf("state status = %d body=%s", stateRec.Code, stateRec.Body.String())
	}
	var snapshot cs.SessionStateSnapshot
	if err := json.Unmarshal(stateRec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode state = %v", err)
	}
	if snapshot.Session.State != cs.SessionStateEscalatePending || snapshot.Escalation == nil {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	cancelReq := httptest.NewRequest(http.MethodPost, "/api/v1/cs/sessions/"+session.ID+"/escalate/cancel", nil)
	cancelReq = cancelReq.WithContext(auth.WithUser(cancelReq.Context(), &auth.User{ID: "u1", Status: "active"}))
	cancelRec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", cancelRec.Code, cancelRec.Body.String())
	}
	if err := json.Unmarshal(cancelRec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode cancel = %v", err)
	}
	if snapshot.Session.State != cs.SessionStateAIHandling || snapshot.Escalation == nil || snapshot.Escalation.Status != cs.EscalationCanceled {
		t.Fatalf("cancel snapshot = %+v", snapshot)
	}
}

func TestCSSessionHandlersOwnerScoped(t *testing.T) {
	service := cs.NewService(cs.NewMemoryStore())
	srv := NewServer(config.ServerConfig{Host: "127.0.0.1", Port: 0}, config.HITLConfig{}, config.WebUIConfig{}, nil, nil, nil, "", nil, nil, nil, zap.NewNop())
	srv.SetCustomerService(service)

	session, err := service.CreateSession(context.Background(), cs.CreateSessionInput{
		Scope: cs.OwnerScope{OwnerID: "u1"},
	})
	if err != nil {
		t.Fatalf("CreateSession = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cs/sessions/"+session.ID+"/state", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: "u2", Status: "active"}))
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-owner status = %d body=%s", rec.Code, rec.Body.String())
	}
}
