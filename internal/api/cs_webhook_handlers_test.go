package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/cs"
)

func TestCSWebhookSubscriptionHandlers(t *testing.T) {
	srv := NewServer(config.ServerConfig{Host: "127.0.0.1", Port: 0}, config.HITLConfig{}, config.WebUIConfig{}, nil, nil, nil, "", nil, nil, nil, zap.NewNop())
	srv.SetCustomerService(cs.NewService(cs.NewMemoryStore()))
	body := `{"name":"desk","url":"https://example.com/hook"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cs/webhooks/subscriptions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/cs/webhooks/subscriptions", nil)
	rec = httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []cs.WebhookSubscription `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list = %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Name != "desk" {
		t.Fatalf("items = %+v", got.Items)
	}
}
