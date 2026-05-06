# Feishu Phase 2B Ingress Switching Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement explicit Feishu `ingress_mode=webhook|longconn` with runtime gate-based single-ingress switching and hot reload, while preserving fail-closed semantics and never allowing dual ingress.

**Architecture:** Add a normalized Feishu ingress mode in config as the single source of truth, then route all bootstrap/reload decisions through that mode. Keep the HTTP Feishu webhook route permanently registered, but front it with a runtime gate handler that forwards only in `webhook` mode and rejects in `longconn` mode. Reload becomes a two-phase ingress state transition: block old ingress, stop old path, start new path, then commit mode.

**Tech Stack:** Go, existing `config`/`api`/`bootstrap`/`channel/feishu` packages, `httptest`, existing reload pipeline

---

## File Structure

### New files

- `internal/api/feishu_ingress_gate.go`
  - Runtime gate handler for the permanent Feishu webhook route.
- `internal/api/feishu_ingress_gate_test.go`
  - HTTP-level tests for webhook forwarding vs rejection by ingress mode.

### Existing files to modify

- `internal/config/config.go`
  - Add `FeishuIngressMode`, normalization helpers, and ingress validation updates.
- `internal/config/feishu_validate_test.go`
  - Extend validation coverage for explicit `ingress_mode` and legacy fallback mapping.
- `internal/channel/feishu/plugin.go`
  - Make plugin startup obey normalized ingress mode instead of raw `LongconnEnabled`.
- `internal/channel/feishu/plugin_test.go`
  - Cover plugin start/stop behavior under explicit ingress mode.
- `internal/api/server.go`
  - Register the permanent Feishu webhook route through the new gate handler.
- `internal/bootstrap/helpers.go`
  - Rework Feishu reload logic to perform fail-closed ingress transitions.
- `internal/bootstrap/helpers_test.go`
  - Add hot-switch tests for `webhook -> longconn`, `longconn -> webhook`, and startup failure.
- `internal/bootstrap/server.go`
  - Bootstrap Feishu using normalized ingress mode and inject gate dependencies.
- `docs/渠道对接/feishu-bot/ROADMAP.md`
  - Mark the ingress switching slice separately within Phase 2B.

## Scope Guard

- Do **not** implement watchdog in this plan.
- Do **not** implement gap fetch in this plan.
- Do **not** enable dual ingress, even temporarily.
- Do **not** add dynamic mux route removal; use gate handler instead.

### Task 1: Add Explicit `ingress_mode` to Feishu Config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/feishu_validate_test.go`

- [ ] **Step 1: Write the failing validation tests**

```go
func TestFeishuConfig_Validate_ExplicitIngressMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  FeishuConfig
		wantErr bool
	}{
		{
			name: "explicit_webhook_ok",
			cfg: FeishuConfig{IngressMode: "webhook"},
		},
		{
			name: "explicit_longconn_ok",
			cfg: FeishuConfig{IngressMode: "longconn"},
		},
		{
			name: "explicit_invalid_fails_closed",
			cfg: FeishuConfig{IngressMode: "bogus"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected validate error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected validate error: %v", err)
			}
		})
	}
}

func TestFeishuConfig_IngressMode_FallsBackToLegacyFields(t *testing.T) {
	if got := (FeishuConfig{LongconnEnabled: true}).ResolvedIngressMode(); got != FeishuIngressModeLongconn {
		t.Fatalf("want longconn, got %q", got)
	}
	if got := (FeishuConfig{}).ResolvedIngressMode(); got != FeishuIngressModeWebhook {
		t.Fatalf("want webhook default, got %q", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config -run 'TestFeishuConfig_(Validate_ExplicitIngressMode|IngressMode_FallsBackToLegacyFields)' -count=1`
Expected: FAIL with missing `IngressMode` / `ResolvedIngressMode`

- [ ] **Step 3: Add minimal ingress mode types and helpers**

```go
type FeishuIngressMode string

const (
	FeishuIngressModeWebhook  FeishuIngressMode = "webhook"
	FeishuIngressModeLongconn FeishuIngressMode = "longconn"
)

type FeishuConfig struct {
	// ...
	IngressMode FeishuIngressMode `json:"ingress_mode,omitempty"`
}

func (c FeishuConfig) ResolvedIngressMode() FeishuIngressMode {
	if c.IngressMode != "" {
		return c.IngressMode
	}
	if c.LongconnEnabled {
		return FeishuIngressModeLongconn
	}
	return FeishuIngressModeWebhook
}
```

- [ ] **Step 4: Update validation to use explicit mode**

```go
func (c FeishuConfig) Validate() error {
	switch c.ResolvedIngressMode() {
	case FeishuIngressModeWebhook, FeishuIngressModeLongconn:
	default:
		return errs.New(errs.CodeConfigInvalid,
			fmt.Sprintf("feishu ingress_mode must be %q or %q, got %q",
				FeishuIngressModeWebhook, FeishuIngressModeLongconn, c.IngressMode))
	}
	if c.IngressMode == "" && c.LongconnEnabled && c.WebhookURL != "" {
		return errs.New(errs.CodeConfigInvalid,
			"feishu: dual ingress detected — longconn_enabled=true 与 webhook_url 同时配置，"+
				"生产环境必须二选一（红队链 B：双投 → 双回复）")
	}
	return nil
}
```

- [ ] **Step 5: Run focused config tests**

Run: `go test ./internal/config -run 'TestFeishuConfig_' -count=1`
Expected: PASS

### Task 2: Add Permanent Feishu Webhook Gate Handler

**Files:**
- Create: `internal/api/feishu_ingress_gate.go`
- Create: `internal/api/feishu_ingress_gate_test.go`
- Modify: `internal/api/server.go`

- [ ] **Step 1: Write the failing gate handler tests**

```go
func TestFeishuIngressGateHandler_ForwardsInWebhookMode(t *testing.T) {
	called := false
	handler := NewFeishuIngressGateHandler(
		func() config.FeishuIngressMode { return config.FeishuIngressModeWebhook },
		func() http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}
		},
		zap.NewNop(),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/channel/feishu/webhook", nil)
	handler.ServeHTTP(rec, req)

	if !called || rec.Code != http.StatusOK {
		t.Fatalf("expected webhook forwarding, called=%v code=%d", called, rec.Code)
	}
}

func TestFeishuIngressGateHandler_RejectsInLongconnMode(t *testing.T) {
	handler := NewFeishuIngressGateHandler(
		func() config.FeishuIngressMode { return config.FeishuIngressModeLongconn },
		func() http.HandlerFunc {
			t.Fatal("must not forward in longconn mode")
			return nil
		},
		zap.NewNop(),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/channel/feishu/webhook", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/api -run 'TestFeishuIngressGateHandler_' -count=1`
Expected: FAIL with missing `NewFeishuIngressGateHandler`

- [ ] **Step 3: Add the minimal gate handler**

```go
type FeishuIngressGateHandler struct {
	modeFn    func() config.FeishuIngressMode
	handlerFn func() http.HandlerFunc
	logger    *zap.Logger
}

func NewFeishuIngressGateHandler(
	modeFn func() config.FeishuIngressMode,
	handlerFn func() http.HandlerFunc,
	logger *zap.Logger,
) *FeishuIngressGateHandler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &FeishuIngressGateHandler{modeFn: modeFn, handlerFn: handlerFn, logger: logger}
}

func (h *FeishuIngressGateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.modeFn == nil || h.modeFn() != config.FeishuIngressModeWebhook {
		http.NotFound(w, r)
		return
	}
	h.handlerFn()(w, r)
}
```

- [ ] **Step 4: Wire Server.SetChannelRouter through the gate**

```go
func (s *Server) SetChannelRouter(r *channel.Router) {
	if r == nil {
		return
	}
	s.mux.HandleFunc("POST /api/v1/channel/feishu/webhook", NewFeishuIngressGateHandler(
		func() config.FeishuIngressMode {
			s.configMu.RLock()
			defer s.configMu.RUnlock()
			if s.config == nil {
				return config.FeishuIngressModeWebhook
			}
			return s.config.Channel.Feishu.ResolvedIngressMode()
		},
		func() http.HandlerFunc {
			return r.WebhookHandler(channel.PlatformFeishu)
		},
		s.logger,
	).ServeHTTP)
	// keep others unchanged
}
```

- [ ] **Step 5: Run focused API tests**

Run: `go test ./internal/api -run 'TestFeishuIngressGateHandler_' -count=1`
Expected: PASS

### Task 3: Rework Feishu Plugin Startup and Reload for Single-Ingress Switching

**Files:**
- Modify: `internal/channel/feishu/plugin.go`
- Modify: `internal/channel/feishu/plugin_test.go`
- Modify: `internal/bootstrap/helpers.go`
- Modify: `internal/bootstrap/helpers_test.go`
- Modify: `internal/bootstrap/server.go`

- [ ] **Step 1: Write the failing plugin/reload tests**

```go
func TestPluginStart_OnlyStartsLongconnInLongconnMode(t *testing.T) {
	logger := zap.NewNop()
	router := channel.NewRouter(nil, logger)

	webhookPlugin := New(config.FeishuConfig{IngressMode: config.FeishuIngressModeWebhook}, router, logger)
	webhookPlugin.Start()
	if webhookPlugin.longConn.started {
		t.Fatal("webhook mode must not start longconn")
	}

	longconnPlugin := New(config.FeishuConfig{IngressMode: config.FeishuIngressModeLongconn}, router, logger)
	longconnPlugin.Start()
	if !longconnPlugin.longConn.started {
		t.Fatal("longconn mode must start longconn")
	}
}
```

```go
func TestBuildReloadChannelFunc_FeishuIngressSwitch_WebhookToLongconn(t *testing.T) {
	logger := zap.NewNop()
	router := channel.NewRouter(nil, logger)
	cfg := &config.Config{Channel: config.ChannelConfig{Feishu: config.FeishuConfig{
		Enabled: true,
		IngressMode: config.FeishuIngressModeWebhook,
	}}}
	var mu sync.RWMutex

	fn := BuildReloadChannelFunc(cfg, router, nil, nil, nil, nil, &mu, logger)
	if err := fn("feishu"); err != nil {
		t.Fatalf("initial webhook reload failed: %v", err)
	}

	cfg.Channel.Feishu.IngressMode = config.FeishuIngressModeLongconn
	if err := fn("feishu"); err != nil {
		t.Fatalf("switch to longconn failed: %v", err)
	}

	plugin, ok := router.GetPlugin(channel.PlatformFeishu)
	if !ok {
		t.Fatal("expected feishu plugin after reload")
	}
	fs := plugin.(*feishu.Plugin)
	if !fs.LongConnStartedForTest() {
		t.Fatal("expected longconn started after switch")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/channel/feishu ./internal/bootstrap -run 'TestPluginStart_OnlyStartsLongconnInLongconnMode|TestBuildReloadChannelFunc_FeishuIngressSwitch_' -count=1`
Expected: FAIL because plugin/reload still read raw `LongconnEnabled`

- [ ] **Step 3: Make plugin startup obey `ResolvedIngressMode()`**

```go
func (p *Plugin) Start() {
	if p.cfg.ResolvedIngressMode() != config.FeishuIngressModeLongconn {
		p.logger.Info("飞书当前不是 longconn 模式，跳过长连接启动",
			zap.String("ingress_mode", string(p.cfg.ResolvedIngressMode())))
		return
	}
	p.longConn.Start(context.Background())
}
```

- [ ] **Step 4: Update reload path to switch by ingress mode**

```go
mode := channelCfg.Feishu.ResolvedIngressMode()
// always unregister old plugin first
// rebuild plugin with new mode
// register plugin
// plugin.Start() only starts longconn when mode==longconn
// renderer/resolver wiring remains unchanged
```

Implementation rule:
- never return to a state where both old webhook path and new longconn path are active
- on rebuild/start failure, return error and leave ingress gate closed to webhook if mode requires longconn

- [ ] **Step 5: Run focused plugin/bootstrap tests**

Run: `go test ./internal/channel/feishu ./internal/bootstrap -run 'TestPluginStart_|TestBuildReloadChannelFunc_' -count=1`
Expected: PASS

### Task 4: Strict Acceptance Tests and Docs Update

**Files:**
- Modify: `internal/bootstrap/helpers_test.go`
- Modify: `docs/渠道对接/feishu-bot/ROADMAP.md`

- [ ] **Step 1: Add the failing hot-switch acceptance tests**

```go
func TestBuildReloadChannelFunc_FeishuIngressSwitch_LongconnToWebhook(t *testing.T) {
	logger := zap.NewNop()
	router := channel.NewRouter(nil, logger)
	cfg := &config.Config{Channel: config.ChannelConfig{Feishu: config.FeishuConfig{
		Enabled: true,
		IngressMode: config.FeishuIngressModeLongconn,
	}}}
	var mu sync.RWMutex

	fn := BuildReloadChannelFunc(cfg, router, nil, nil, nil, nil, &mu, logger)
	if err := fn("feishu"); err != nil {
		t.Fatalf("initial longconn reload failed: %v", err)
	}

	cfg.Channel.Feishu.IngressMode = config.FeishuIngressModeWebhook
	if err := fn("feishu"); err != nil {
		t.Fatalf("switch to webhook failed: %v", err)
	}

	plugin, _ := router.GetPlugin(channel.PlatformFeishu)
	fs := plugin.(*feishu.Plugin)
	if fs.LongConnStartedForTest() {
		t.Fatal("expected longconn stopped after switching back to webhook")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/bootstrap -run 'TestBuildReloadChannelFunc_FeishuIngressSwitch_' -count=1`
Expected: FAIL until both switch directions are handled

- [ ] **Step 3: Update roadmap to mark the ingress switching slice**

```md
## Phase 2B.1: single-ingress switching

- explicit `ingress_mode=webhook|longconn`
- permanent webhook route + runtime gate
- hot reload switches one ingress off before the other turns on
```

- [ ] **Step 4: Run final strict verification**

Run: `gofmt -w internal/config/config.go internal/config/feishu_validate_test.go internal/api/server.go internal/api/feishu_ingress_gate.go internal/api/feishu_ingress_gate_test.go internal/channel/feishu/plugin.go internal/channel/feishu/plugin_test.go internal/bootstrap/helpers.go internal/bootstrap/helpers_test.go internal/bootstrap/server.go`
Expected: no output

Run: `go test ./internal/config ./internal/api ./internal/channel/feishu ./internal/bootstrap -count=1`
Expected: PASS

- [ ] **Step 5: Run focused ingress acceptance commands**

Run: `go test ./internal/config -run 'TestFeishuConfig_' -count=1`
Expected: PASS

Run: `go test ./internal/api -run 'TestFeishuIngressGateHandler_' -count=1`
Expected: PASS

Run: `go test ./internal/channel/feishu -run 'TestPluginStart_OnlyStartsLongconnInLongconnMode' -count=1`
Expected: PASS

Run: `go test ./internal/bootstrap -run 'TestBuildReloadChannelFunc_FeishuIngressSwitch_' -count=1`
Expected: PASS

## Spec Coverage Check

- explicit `ingress_mode` source of truth: Task 1
- permanent webhook route + runtime gate: Task 2
- runtime hot switching without dual ingress: Task 3 and Task 4
- fail-closed startup/reload behavior: Task 3 and Task 4
- docs boundary update: Task 4

## Placeholder Scan

- No `TODO` or `TBD` placeholders remain.
- Each task names exact files.
- Each task contains exact commands and expected results.
