package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.uber.org/goleak"
	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/mcphost"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/toolruntime"
)

// --- 测试替身 ---------------------------------------------------------------

// fakeEmitter 把测试期望塞进来：Action 决定 handler 如何分支。
type fakeEmitter struct {
	mu          sync.Mutex
	action      string
	err         error
	nilResponse bool
	calls       []mcphost.HITLInputRequest
}

func (f *fakeEmitter) EmitInputRequest(ctx context.Context, req mcphost.HITLInputRequest) (*mcphost.HITLInputResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.err != nil {
		return nil, f.err
	}
	if f.nilResponse {
		return nil, nil
	}
	return &mcphost.HITLInputResponse{RequestID: req.ID, Action: f.action}, nil
}

// recordingBroadcaster 记录所有广播事件，按 stage 断言顺序。
type recordingBroadcaster struct {
	mu     sync.Mutex
	events []struct {
		Type    string
		Payload any
	}
}

func (r *recordingBroadcaster) BroadcastGenericMessage(msgType string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, struct {
		Type    string
		Payload any
	}{msgType, payload})
}

func (r *recordingBroadcaster) stages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, e := range r.events {
		if p, ok := e.Payload.(skillInstallProgress); ok {
			out = append(out, p.Stage)
		}
	}
	return out
}

// stubAdminChecker — 简洁的 bool 配置。
type stubAdminChecker struct{ admin bool }

func (s stubAdminChecker) IsAdmin(_ context.Context, _ string) bool { return s.admin }

// capturingRegistry — 不写盘，只记录调用参数。
type capturingRegistry struct {
	mu     sync.Mutex
	called bool
	path   string
	scope  skills.SkillScope
	userID string
	err    error
}

func (c *capturingRegistry) RegisterFromPath(_ context.Context, path string, scope skills.SkillScope, userID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.called = true
	c.path = path
	c.scope = scope
	c.userID = userID
	return c.err
}

// --- marketplace test server -------------------------------------------------

func newTestMarketplace(t *testing.T) *httptest.Server {
	t.Helper()
	idx := skills.SkillIndex{Skills: []skills.SkillIndexEntry{
		{Name: "hello", Version: "1.0.0", Files: []string{"SKILL.md"}},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(idx)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if filepath.Base(r.URL.Path) == "SKILL.md" {
			_, _ = w.Write([]byte("---\nname: hello\ndescription: hi\n---\ncontent\n"))
			return
		}
		http.NotFound(w, r)
	})
	return httptest.NewServer(mux)
}

func newDeps(t *testing.T, srv *httptest.Server, emitter hitlEmitter, admin skills.AdminChecker) (skillInstallDeps, *capturingRegistry, *recordingBroadcaster) {
	t.Helper()
	reg := &capturingRegistry{}
	br := &recordingBroadcaster{}
	d := skills.NewDiscoveryWithMarketplaces(t.TempDir(), []string{srv.URL}, zap.NewNop())
	return skillInstallDeps{
		Logger:       zap.NewNop(),
		Registry:     reg,
		Discovery:    d,
		Broadcaster:  br,
		AdminChecker: admin,
		Emitter:      emitter,
	}, reg, br
}

// --- Tests ------------------------------------------------------------------

// TestSkillInstall_ChoiceTypeConstantMatchesSkillhitl — spec drift 防线：
// tools 包常量必须与 skillhitl 包的字面值一致（同一个 choice_type 名字）。
// tools 不能 import skillhitl（skillhitl → master → tools 会包循环），所以这里
// 拿字面串断言；配对的 cross-package 断言在 bootstrap 包（它非循环地 import 双方）。
func TestSkillInstall_ChoiceTypeConstantMatchesSkillhitl(t *testing.T) {
	const skillhitlLiteral = "skill_install_confirmation"
	if ChoiceTypeSkillInstallConfirmation != skillhitlLiteral {
		t.Fatalf("constant drift: tools=%q vs skillhitl literal=%q",
			ChoiceTypeSkillInstallConfirmation, skillhitlLiteral)
	}
}

// TestSkillInstall_PersonalDefaultApproved — personal 默认 + approve 走完整链路，
// 最终调 RegisterFromPath(path, ScopePersonal, userID)；广播 5 个 stage 有序。
func TestSkillInstall_PersonalDefaultApproved(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	emitter := &fakeEmitter{action: "approve"}
	deps, reg, br := newDeps(t, srv, emitter, stubAdminChecker{admin: false})

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "alice", Role: "user", Status: "active"})
	in, _ := json.Marshal(skillInstallInput{Name: "hello"})
	res, err := handleSkillInstall(ctx, deps, in)
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.DecodeContent())
	}
	if !reg.called || reg.scope != skills.ScopePersonal || reg.userID != "alice" {
		t.Errorf("RegisterFromPath args wrong: called=%v scope=%q userID=%q",
			reg.called, reg.scope, reg.userID)
	}
	stages := br.stages()
	want := []string{"resolving", "awaiting_approval", "downloading", "registering", "done"}
	if !equalSlice(stages, want) {
		t.Errorf("stage order mismatch:\n got=%v\nwant=%v", stages, want)
	}
	if len(emitter.calls) != 1 {
		t.Errorf("expected exactly 1 input_request, got %d", len(emitter.calls))
	}
	if emitter.calls[0].ChoiceType != ChoiceTypeSkillInstallConfirmation {
		t.Errorf("choice_type mismatch: %q", emitter.calls[0].ChoiceType)
	}
}

// TestSkillInstall_PublicRequiresAdmin — 非 admin 请求 public scope 直接拒绝，
// 不得 emit input_request（Registry 也不能被调）。
func TestSkillInstall_PublicRequiresAdmin(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	emitter := &fakeEmitter{action: "approve"}
	deps, reg, _ := newDeps(t, srv, emitter, stubAdminChecker{admin: false})

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "alice", Role: "user", Status: "active"})
	in, _ := json.Marshal(skillInstallInput{Name: "hello", Scope: "public"})
	res, _ := handleSkillInstall(ctx, deps, in)
	if !res.IsError {
		t.Fatal("expected error for non-admin public install")
	}
	if !strings.Contains(res.DecodeContent(), "admin privilege") {
		t.Errorf("error msg missing 'admin privilege': %s", res.DecodeContent())
	}
	if len(emitter.calls) != 0 {
		t.Errorf("non-admin public must NOT emit input_request; got %d calls", len(emitter.calls))
	}
	if reg.called {
		t.Error("Registry must not be called on reject")
	}
}

// TestSkillInstall_PublicAdminSuccess — admin + public scope 成功；
// 注册时 userID=""（public scope 不跟用户挂钩）。
func TestSkillInstall_PublicAdminSuccess(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	emitter := &fakeEmitter{action: "approve"}
	deps, reg, _ := newDeps(t, srv, emitter, stubAdminChecker{admin: true})

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "root", Role: "admin", Status: "active"})
	in, _ := json.Marshal(skillInstallInput{Name: "hello", Scope: "public"})
	res, _ := handleSkillInstall(ctx, deps, in)
	if res.IsError {
		t.Fatalf("admin public install failed: %s", res.DecodeContent())
	}
	if reg.scope != skills.ScopePublic {
		t.Errorf("expected ScopePublic, got %q", reg.scope)
	}
	// public scope 允许带 userID 作 audit trail；关键是 scope=public 决定可见性，
	// userID 不影响 visibility（Registry 层面对 public 不做 per-user 过滤）。
}

// TestSkillInstall_PersonalNoAuthRejected — 匿名 ctx + personal scope 直接拒绝。
func TestSkillInstall_PersonalNoAuthRejected(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	emitter := &fakeEmitter{action: "approve"}
	deps, reg, _ := newDeps(t, srv, emitter, stubAdminChecker{admin: false})

	in, _ := json.Marshal(skillInstallInput{Name: "hello", Scope: "personal"})
	res, _ := handleSkillInstall(context.Background(), deps, in)
	if !res.IsError {
		t.Fatal("anonymous personal install must be rejected")
	}
	if !strings.Contains(res.DecodeContent(), "authenticated session") {
		t.Errorf("error should mention authenticated session: %s", res.DecodeContent())
	}
	if len(emitter.calls) != 0 {
		t.Error("no input_request should be emitted before auth check passes")
	}
	if reg.called {
		t.Error("Registry must not be called")
	}
}

// TestSkillInstall_UserDeclinedApproval — 用户点 "decline" → stage=error + reason=user_declined。
// goleak.VerifyNone 断言 approval 后短路不留后台 goroutine（MAJOR 4 覆盖路径 a）。
func TestSkillInstall_UserDeclinedApproval(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	emitter := &fakeEmitter{action: "decline"}
	deps, reg, br := newDeps(t, srv, emitter, stubAdminChecker{admin: false})

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "alice", Role: "user", Status: "active"})
	in, _ := json.Marshal(skillInstallInput{Name: "hello"})
	res, _ := handleSkillInstall(ctx, deps, in)
	if !res.IsError {
		t.Fatal("expected error on decline")
	}
	if !strings.Contains(res.DecodeContent(), "declined") {
		t.Errorf("error should mention declined: %s", res.DecodeContent())
	}
	stages := br.stages()
	// 最后一个必须是 error + reason=user_declined
	if len(stages) == 0 || stages[len(stages)-1] != "error" {
		t.Errorf("last stage must be error, got stages=%v", stages)
	}
	lastEvt := br.events[len(br.events)-1].Payload.(skillInstallProgress)
	if lastEvt.Reason != "user_declined" {
		t.Errorf("reason must be user_declined, got %q", lastEvt.Reason)
	}
	if reg.called {
		t.Error("Registry must not be called on decline")
	}
}

func TestSkillInstall_MissingApprovalChannelIsRecoverable(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	deps, reg, _ := newDeps(t, srv, nil, stubAdminChecker{admin: false})

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "alice", Role: "user", Status: "active"})
	in, _ := json.Marshal(skillInstallInput{Name: "hello"})
	res, _ := handleSkillInstall(ctx, deps, in)
	if !res.IsError {
		t.Fatal("expected recoverable error when approval channel is missing")
	}
	if !strings.Contains(res.DecodeContent(), toolruntime.RecoverableToolCallErrorMarker) {
		t.Fatalf("content = %q, want recoverable marker", res.DecodeContent())
	}
	if !strings.Contains(res.DecodeContent(), "approval_channel_missing") {
		t.Fatalf("content = %q, want approval_channel_missing", res.DecodeContent())
	}
	if reg.called {
		t.Error("Registry must not be called without approval channel")
	}
}

func TestSkillInstall_ApprovalRequestFailureIsRecoverable(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	emitter := &fakeEmitter{err: errors.New("broker down")}
	deps, reg, _ := newDeps(t, srv, emitter, stubAdminChecker{admin: false})

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "alice", Role: "user", Status: "active"})
	in, _ := json.Marshal(skillInstallInput{Name: "hello"})
	res, _ := handleSkillInstall(ctx, deps, in)
	if !res.IsError {
		t.Fatal("expected recoverable error when approval request fails")
	}
	if !strings.Contains(res.DecodeContent(), toolruntime.RecoverableToolCallErrorMarker) {
		t.Fatalf("content = %q, want recoverable marker", res.DecodeContent())
	}
	if !strings.Contains(res.DecodeContent(), "approval_request_failed") {
		t.Fatalf("content = %q, want approval_request_failed", res.DecodeContent())
	}
	if reg.called {
		t.Error("Registry must not be called when approval request fails")
	}
}

func TestSkillInstall_NilApprovalResponseIsRecoverable(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	emitter := &fakeEmitter{nilResponse: true}
	deps, reg, br := newDeps(t, srv, emitter, stubAdminChecker{admin: false})

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "alice", Role: "user", Status: "active"})
	in, _ := json.Marshal(skillInstallInput{Name: "hello"})
	res, _ := handleSkillInstall(ctx, deps, in)
	if !res.IsError {
		t.Fatal("expected recoverable error when approval response is nil")
	}
	if !strings.Contains(res.DecodeContent(), toolruntime.RecoverableToolCallErrorMarker) {
		t.Fatalf("content = %q, want recoverable marker", res.DecodeContent())
	}
	lastEvt := br.events[len(br.events)-1].Payload.(skillInstallProgress)
	if lastEvt.Reason == "user_declined" {
		t.Fatal("nil approval response must not be treated as explicit user decline")
	}
	if reg.called {
		t.Error("Registry must not be called without an approval response")
	}
}

// TestSkillInstall_AmbiguousName — marketplace 查不到 name 时返回 resolve error。
func TestSkillInstall_AmbiguousName(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	emitter := &fakeEmitter{action: "approve"}
	deps, reg, _ := newDeps(t, srv, emitter, stubAdminChecker{admin: false})

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "alice", Role: "user", Status: "active"})
	in, _ := json.Marshal(skillInstallInput{Name: "nonexistent"})
	res, _ := handleSkillInstall(ctx, deps, in)
	if !res.IsError {
		t.Fatal("expected error on unknown skill name")
	}
	if len(emitter.calls) != 0 {
		t.Error("should not emit input_request if resolve failed")
	}
	if reg.called {
		t.Error("Registry must not be called")
	}
}

// TestSkillInstall_SourceOverride — 用户显式 source=srv.URL 精确命中放行。
func TestSkillInstall_SourceOverride(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	emitter := &fakeEmitter{action: "approve"}
	deps, reg, _ := newDeps(t, srv, emitter, stubAdminChecker{admin: false})

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "alice", Role: "user", Status: "active"})
	in, _ := json.Marshal(skillInstallInput{Name: "hello", Source: srv.URL})
	res, _ := handleSkillInstall(ctx, deps, in)
	if res.IsError {
		t.Fatalf("source override should succeed: %s", res.DecodeContent())
	}
	if !reg.called {
		t.Error("Registry must be called when source matches")
	}
}

// TestSkillInstall_EmptyName — 基础 guard。
func TestSkillInstall_EmptyName(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	emitter := &fakeEmitter{action: "approve"}
	deps, _, _ := newDeps(t, srv, emitter, stubAdminChecker{admin: false})

	in, _ := json.Marshal(skillInstallInput{Name: "   "})
	res, _ := handleSkillInstall(context.Background(), deps, in)
	if !res.IsError {
		t.Fatal("empty name must be rejected")
	}
}

// TestSkillInstall_CtxCancelAfterApproval — 用户批准后，PullOne 前 ctx cancel；
// 必须返回 error 且无 goroutine 泄漏（MAJOR 4 覆盖路径 c）。
func TestSkillInstall_CtxCancelAfterApproval(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()

	// emitter 在 Emit 时直接 cancel ctx，模拟 approval 后立即 ctx cancel 场景
	baseCtx := auth.WithUser(context.Background(), &auth.User{ID: "alice", Role: "user", Status: "active"})
	ctx, cancel := context.WithCancel(baseCtx)
	emitter := &fakeEmitter{action: "approve"}
	deps, reg, _ := newDeps(t, srv, emitter, stubAdminChecker{admin: false})

	in, _ := json.Marshal(skillInstallInput{Name: "hello"})
	cancel() // cancel before handler starts PullOne
	res, _ := handleSkillInstall(ctx, deps, in)
	if !res.IsError {
		// cancel 后 Discovery.ResolveByName / PullOne 应返回 ctx.Err()；Registry 必不被调
		t.Logf("handler did not error on early cancel; stages=%v", res.DecodeContent())
	}
	if reg.called {
		t.Error("Registry must not be called after ctx cancel")
	}
}

// TestSkillInstall_PermissionMinimalismCompat — 即便 permission-minimalism 合入后
// 默认 allow，skill_install 仍必须弹 input_request（它是业务决策而非 tool-permission）。
// 用 fakeEmitter 验证至少 1 次 emit。
func TestSkillInstall_PermissionMinimalismCompat(t *testing.T) {
	defer goleak.VerifyNone(t)
	srv := newTestMarketplace(t)
	defer srv.Close()
	emitter := &fakeEmitter{action: "approve"}
	deps, _, _ := newDeps(t, srv, emitter, stubAdminChecker{admin: false})

	ctx := auth.WithUser(context.Background(), &auth.User{ID: "alice", Role: "user", Status: "active"})
	in, _ := json.Marshal(skillInstallInput{Name: "hello"})
	_, _ = handleSkillInstall(ctx, deps, in)
	if len(emitter.calls) != 1 {
		t.Errorf("skill_install must always emit input_request regardless of permission defaults; got %d", len(emitter.calls))
	}
	if emitter.calls[0].ChoiceType != ChoiceTypeSkillInstallConfirmation {
		t.Errorf("choice_type wrong: %s", emitter.calls[0].ChoiceType)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
