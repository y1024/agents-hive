package bootstrap

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/chef-guo/agents-hive/internal/channel"
	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/imcore"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/observability"
	"go.uber.org/zap"
)

func TestRegisterIMAPIServiceRespectsEnabledFlag(t *testing.T) {
	host := mcphost.NewHost(zap.NewNop())
	cfg := config.Default()
	cfg.Agent.IMAPI.Enabled = false

	registerIMAPIService(&ServerComponents{MCPHost: host}, cfg, zap.NewNop(), imcore.NewService())

	if _, err := host.GetTool("im_api"); err == nil {
		t.Fatal("im_api should not be registered when agent.im_api.enabled=false")
	}
}

func TestRegisterIMAPIServiceRegistersToolWithDryRunOption(t *testing.T) {
	host := mcphost.NewHost(zap.NewNop())
	cfg := config.Default()
	cfg.Agent.IMAPI.Enabled = true
	cfg.Agent.IMAPI.ForceDryRun = true
	adapter := &bootstrapIMAdapter{platform: imcore.PlatformFeishu}

	registerIMAPIService(&ServerComponents{MCPHost: host}, cfg, zap.NewNop(), imcore.NewService(adapter))

	if _, err := host.GetTool("im_api"); err != nil {
		t.Fatalf("im_api should be registered: %v", err)
	}
	result, err := host.ExecuteTool(context.Background(), "im_api", json.RawMessage(`{"action":"send_message","platform":"feishu","recipient_id":"ou_1","content":"hi"}`))
	if err != nil {
		t.Fatalf("execute im_api: %v", err)
	}
	if result.IsError {
		t.Fatalf("im_api returned error: %s", result.DecodeContent())
	}
	if !adapter.lastTarget.DryRun {
		t.Fatal("ForceDryRun should force adapter target dry_run=true")
	}
}

func TestRegisterIMAPIServiceUsesChannelRouterMetricsWriter(t *testing.T) {
	host := mcphost.NewHost(zap.NewNop())
	cfg := config.Default()
	cfg.Agent.IMAPI.Enabled = true
	writer := &bootstrapCaptureMetricsWriter{}
	router := channel.NewRouter(nil, zap.NewNop())
	router.SetMetricsWriter(writer)

	registerIMAPIService(&ServerComponents{
		MCPHost:       host,
		ChannelRouter: router,
	}, cfg, zap.NewNop(), imcore.NewService(&bootstrapIMAdapter{platform: imcore.PlatformFeishu}))

	result, err := host.ExecuteTool(context.Background(), "im_api", json.RawMessage(`{"action":"send_message","platform":"feishu","recipient_id":"ou_1","content":"hi"}`))
	if err != nil {
		t.Fatalf("execute im_api: %v", err)
	}
	if result.IsError {
		t.Fatalf("im_api returned error: %s", result.DecodeContent())
	}

	metric := writer.waitMetric(t, "im_send_unified_path_total", "success")
	if metric.Labels["tool_name"] != "im_api" || metric.Labels["operation"] != "send_message" || metric.Labels["im"] != "feishu" {
		t.Fatalf("unexpected metric labels: %+v", metric.Labels)
	}
}

type bootstrapCaptureMetricsWriter struct {
	mu      sync.Mutex
	metrics []observability.Metric
}

func (w *bootstrapCaptureMetricsWriter) Record(_ context.Context, metric observability.Metric) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.metrics = append(w.metrics, metric)
	return nil
}

func (w *bootstrapCaptureMetricsWriter) waitMetric(t *testing.T, name, status string) observability.Metric {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		for _, metric := range w.metrics {
			if metric.Name == name && metric.Labels["status"] == status {
				w.mu.Unlock()
				return metric
			}
		}
		w.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	t.Fatalf("metric %q with status=%q not found, got %+v", name, status, w.metrics)
	return observability.Metric{}
}

type bootstrapIMAdapter struct {
	platform   imcore.Platform
	lastTarget imcore.SendTarget
}

func (a *bootstrapIMAdapter) Platform() imcore.Platform { return a.platform }

func (a *bootstrapIMAdapter) Capabilities() []imcore.Capability {
	return []imcore.Capability{imcore.CapabilitySendText}
}

func (a *bootstrapIMAdapter) SearchRecipients(context.Context, imcore.CallerScope, string, int) ([]imcore.Recipient, error) {
	return nil, nil
}

func (a *bootstrapIMAdapter) ListRecentConversations(context.Context, imcore.CallerScope, int) ([]imcore.Recipient, error) {
	return nil, nil
}

func (a *bootstrapIMAdapter) ResolveRecipient(_ context.Context, _ imcore.CallerScope, input imcore.RecipientLookup) (imcore.Recipient, error) {
	return imcore.Recipient{Platform: a.platform, ID: input.RecipientID, Kind: "user", CanSend: true, SendState: "ready"}, nil
}

func (a *bootstrapIMAdapter) SendMessage(_ context.Context, _ imcore.CallerScope, target imcore.SendTarget) (imcore.SendResult, error) {
	a.lastTarget = target
	return imcore.SendResult{Platform: a.platform, TargetID: target.RecipientID, TargetKind: "user", Delivered: !target.DryRun, DryRun: target.DryRun}, nil
}
