package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chef-guo/agents-hive/internal/specdriven"
)

// fakeRunner 是一个可编排的 Runner：默认"按 fixture 预期正确应答"，
// 各方法行为可通过字段覆盖以精准命中 runCaseOnce 里的每条分支。
// TG2+ 实装的 real Runner 未就绪，behavior gate 的覆盖由本 fake 驱动——
// 任何新增反例都应该先在本 fake 下复现，再进真实 runner。
type fakeRunner struct {
	resolveErr       error
	decisionOverride *specdriven.Decision
	planErr          error
	planOverride     *specdriven.Plan
	fallbackErr      error
	// echoWantError：true 时在 WantError 非空处回射该子串为 error，让 AllFixturesPass 对
	// 含 WantError 的 fixture（fm03/fm06）依然 tautologically 绿。默认 false 保持
	// TestRunCaseOnce_ErrorPaths/want_error_missing 原语义（WantError 声明但 runner 不 return err）。
	echoWantError bool
}

func (r fakeRunner) ResolveContinuation(_ context.Context, c Case) (specdriven.Decision, error) {
	if r.resolveErr != nil {
		return specdriven.Decision{}, r.resolveErr
	}
	if r.decisionOverride != nil {
		return *r.decisionOverride, nil
	}
	if r.echoWantError && c.WantError != "" {
		return specdriven.Decision{}, errors.New(c.WantError)
	}
	return specdriven.Decision{
		Kind:      specdriven.DecisionKind(c.WantContinuation.Decision),
		ChangeID:  c.WantContinuation.ChangeID,
		AskReason: c.WantContinuation.AskReason,
	}, nil
}

func (r fakeRunner) Plan(_ context.Context, c Case) (*specdriven.Plan, error) {
	if r.planErr != nil {
		return nil, r.planErr
	}
	if r.planOverride != nil {
		return r.planOverride, nil
	}
	if c.WantPlan == nil {
		return &specdriven.Plan{}, nil
	}
	cp := *c.WantPlan
	cp.Steps = append([]specdriven.PlanStep(nil), c.WantPlan.Steps...)
	return &cp, nil
}

func (r fakeRunner) ExecuteFallback(_ context.Context, _ Case) error { return r.fallbackErr }

// anonRunner 允许按需装配各方法，绕开 fakeRunner 的"默认回声"语义。
type anonRunner struct {
	resolve  func(context.Context, Case) (specdriven.Decision, error)
	plan     func(context.Context, Case) (*specdriven.Plan, error)
	fallback func(context.Context, Case) error
}

func (a anonRunner) ResolveContinuation(ctx context.Context, c Case) (specdriven.Decision, error) {
	if a.resolve == nil {
		return specdriven.Decision{Kind: specdriven.DecisionKind(c.WantContinuation.Decision)}, nil
	}
	return a.resolve(ctx, c)
}

func (a anonRunner) Plan(ctx context.Context, c Case) (*specdriven.Plan, error) {
	if a.plan == nil {
		return &specdriven.Plan{}, nil
	}
	return a.plan(ctx, c)
}

func (a anonRunner) ExecuteFallback(ctx context.Context, c Case) error {
	if a.fallback == nil {
		return nil
	}
	return a.fallback(ctx, c)
}

// TestHarnessBehavior_AllFixturesPass 是 TG2+ behavior gate 的骨架闭环：
// 对 testdata 下全部 fm* fixture 跑 Harness.RunFixtures，fakeRunner 按 want_* 应答，
// 断言 Summary.Passed == Summary.Total 且 RequiredFailed/OptionalFailed 都为空。
// 同时覆盖 RunFixtures 主循环、equalPlan 的 happy path、canonicalJSON、Summary.String()。
func TestHarnessBehavior_AllFixturesPass(t *testing.T) {
	cases, err := LoadAll("testdata")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	h := Harness{Runner: fakeRunner{echoWantError: true}}
	summary := h.RunFixtures(t, cases)

	if summary.Total != len(cases) {
		t.Errorf("Summary.Total = %d, want %d", summary.Total, len(cases))
	}
	if summary.Passed != summary.Total {
		t.Errorf("Summary.Passed = %d, want %d (all)", summary.Passed, summary.Total)
	}
	if summary.RequiredPassed != summary.RequiredTotal {
		t.Errorf("RequiredPassed %d != RequiredTotal %d", summary.RequiredPassed, summary.RequiredTotal)
	}
	if len(summary.RequiredFailed) != 0 || len(summary.OptionalFailed) != 0 {
		t.Errorf("unexpected failures: required=%v optional=%v",
			summary.RequiredFailed, summary.OptionalFailed)
	}

	got := summary.String()
	for _, needle := range []string{"eval summary:", "passed=", "required=", "optional_failed=", "total="} {
		if !strings.Contains(got, needle) {
			t.Errorf("Summary.String missing %q: %s", needle, got)
		}
	}
}

// TestRunCaseOnce_ErrorPaths 直接测纯函数 runCaseOnce 的每条错误路径。
// 避开 t.Run 父-子失败联动（Go testing 硬行为），用 error 返回做断言。
// 此设计让 behavior gate 的单条业务逻辑可以脱离 testing.T 独立验证。
func TestRunCaseOnce_ErrorPaths(t *testing.T) {
	basePlan := &specdriven.Plan{Steps: []specdriven.PlanStep{
		{TaskKey: "1.1", ToolName: "bash", Args: map[string]any{"cmd": "ls"}},
	}}
	mkCase := func(name string, mut func(*Case)) LoadedCase {
		c := Case{
			Name:     name,
			UserID:   "u-test",
			Input:    "do the thing",
			Required: false,
			WantContinuation: WantContinuation{
				Decision: "resume", ChangeID: "c-x",
			},
		}
		if mut != nil {
			mut(&c)
		}
		return LoadedCase{Path: filepath.Join("testdata", name+".json"), Case: c}
	}

	// 特殊 runner：返回 nil *Plan，用于测试 equalPlan nil 分支
	nilPlanRunner := anonRunner{
		resolve: func(_ context.Context, c Case) (specdriven.Decision, error) {
			return specdriven.Decision{
				Kind: specdriven.DecisionKind(c.WantContinuation.Decision), ChangeID: c.WantContinuation.ChangeID,
			}, nil
		},
		plan: func(_ context.Context, _ Case) (*specdriven.Plan, error) { return nil, nil },
	}

	tests := []struct {
		name    string
		lc      LoadedCase
		runner  Runner
		wantErr string // substring expected in returned error; empty = must be nil
	}{
		{
			name:   "happy_resume",
			lc:     mkCase("happy_resume", nil),
			runner: fakeRunner{},
		},
		{
			name: "want_error_matches",
			lc: mkCase("want_error_matches", func(c *Case) {
				c.WantError = "boom"
			}),
			runner: fakeRunner{resolveErr: errors.New("boom: CAS conflict")},
		},
		{
			name: "want_error_missing",
			lc: mkCase("want_error_missing", func(c *Case) {
				c.WantError = "expected"
			}),
			runner:  fakeRunner{},
			wantErr: "expected error containing",
		},
		{
			name: "want_error_mismatch",
			lc: mkCase("want_error_mismatch", func(c *Case) {
				c.WantError = "needle"
			}),
			runner:  fakeRunner{resolveErr: errors.New("totally different")},
			wantErr: "error mismatch",
		},
		{
			name:    "resolve_unexpected_error",
			lc:      mkCase("resolve_unexpected_error", nil),
			runner:  fakeRunner{resolveErr: errors.New("unexpected")},
			wantErr: "ResolveContinuation",
		},
		{
			name: "decision_kind_mismatch",
			lc:   mkCase("decision_kind_mismatch", nil),
			runner: fakeRunner{decisionOverride: &specdriven.Decision{
				Kind: specdriven.DecisionNew,
			}},
			wantErr: "decision mismatch",
		},
		{
			name: "resume_change_id_mismatch",
			lc:   mkCase("resume_change_id_mismatch", nil),
			runner: fakeRunner{decisionOverride: &specdriven.Decision{
				Kind: specdriven.DecisionResume, ChangeID: "c-wrong",
			}},
			wantErr: "resume change_id mismatch",
		},
		{
			name: "ask_reason_mismatch",
			lc: mkCase("ask_reason_mismatch", func(c *Case) {
				c.WantContinuation = WantContinuation{Decision: "ask", AskReason: "expected_reason"}
			}),
			runner: fakeRunner{decisionOverride: &specdriven.Decision{
				Kind: specdriven.DecisionAsk, AskReason: "different_reason",
			}},
			wantErr: "ask reason mismatch",
		},
		{
			name: "plan_match",
			lc: mkCase("plan_match", func(c *Case) {
				c.WantPlan = basePlan
			}),
			runner: fakeRunner{},
		},
		{
			name: "plan_returns_error",
			lc: mkCase("plan_returns_error", func(c *Case) {
				c.WantPlan = basePlan
			}),
			runner:  fakeRunner{planErr: errors.New("llm timeout")},
			wantErr: "Plan:",
		},
		{
			name: "plan_step_count_diff",
			lc: mkCase("plan_step_count_diff", func(c *Case) {
				c.WantPlan = basePlan
			}),
			runner: fakeRunner{planOverride: &specdriven.Plan{
				Steps: []specdriven.PlanStep{
					{TaskKey: "1.1", ToolName: "bash"},
					{TaskKey: "1.2", ToolName: "bash"},
				},
			}},
			wantErr: "step count",
		},
		{
			name: "plan_task_key_diff",
			lc: mkCase("plan_task_key_diff", func(c *Case) {
				c.WantPlan = basePlan
			}),
			runner: fakeRunner{planOverride: &specdriven.Plan{
				Steps: []specdriven.PlanStep{
					{TaskKey: "9.9", ToolName: "bash", Args: map[string]any{"cmd": "ls"}},
				},
			}},
			wantErr: "task_key",
		},
		{
			name: "plan_tool_name_diff",
			lc: mkCase("plan_tool_name_diff", func(c *Case) {
				c.WantPlan = basePlan
			}),
			runner: fakeRunner{planOverride: &specdriven.Plan{
				Steps: []specdriven.PlanStep{
					{TaskKey: "1.1", ToolName: "python", Args: map[string]any{"cmd": "ls"}},
				},
			}},
			wantErr: "tool_name",
		},
		{
			name: "plan_args_diff",
			lc: mkCase("plan_args_diff", func(c *Case) {
				c.WantPlan = basePlan
			}),
			runner: fakeRunner{planOverride: &specdriven.Plan{
				Steps: []specdriven.PlanStep{
					{TaskKey: "1.1", ToolName: "bash", Args: map[string]any{"cmd": "rm -rf /"}},
				},
			}},
			wantErr: "args diff",
		},
		{
			name: "plan_nil_returned",
			lc: mkCase("plan_nil_returned", func(c *Case) {
				c.WantPlan = basePlan
			}),
			runner:  nilPlanRunner,
			wantErr: "plan is nil",
		},
		{
			name: "fallback_happy",
			lc: mkCase("fallback_happy", func(c *Case) {
				c.WantFallback = true
			}),
			runner: fakeRunner{},
		},
		{
			name: "fallback_error",
			lc: mkCase("fallback_error", func(c *Case) {
				c.WantFallback = true
			}),
			runner:  fakeRunner{fallbackErr: errors.New("fallback boom")},
			wantErr: "ExecuteFallback",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := runCaseOnce(context.Background(), tc.lc, tc.runner)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want nil err, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want err containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want err containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestRunCaseAgainstRunner_ThinWrapper 验证 wrapper 层：成功 no-op，失败调用 t.Fatal。
// 不直接验证 Fatal 行为（会终止测试），只验证 happy path 不 panic。
func TestRunCaseAgainstRunner_ThinWrapper(t *testing.T) {
	lc := LoadedCase{
		Path: "testdata/wrapper_happy.json",
		Case: Case{
			Input:            "x",
			WantContinuation: WantContinuation{Decision: "new"},
		},
	}
	runCaseAgainstRunner(t, lc, fakeRunner{})
}

// TestEqualPlan 覆盖 equalPlan 各独立返回点（happy / nil / step-count / task_key / tool_name / args）。
func TestEqualPlan(t *testing.T) {
	want := &specdriven.Plan{Steps: []specdriven.PlanStep{
		{TaskKey: "1.1", ToolName: "bash", Args: map[string]any{"cmd": "ls"}},
	}}

	if err := equalPlan(want, &specdriven.Plan{Steps: []specdriven.PlanStep{
		{TaskKey: "1.1", ToolName: "bash", Args: map[string]any{"cmd": "ls"}},
	}}); err != nil {
		t.Fatalf("happy path returned err: %v", err)
	}

	cases := []struct {
		name string
		got  *specdriven.Plan
		sub  string
	}{
		{"nil", nil, "plan is nil"},
		{"step_count", &specdriven.Plan{}, "step count"},
		{"task_key", &specdriven.Plan{Steps: []specdriven.PlanStep{
			{TaskKey: "9.9", ToolName: "bash", Args: map[string]any{"cmd": "ls"}},
		}}, "task_key"},
		{"tool_name", &specdriven.Plan{Steps: []specdriven.PlanStep{
			{TaskKey: "1.1", ToolName: "python", Args: map[string]any{"cmd": "ls"}},
		}}, "tool_name"},
		{"args", &specdriven.Plan{Steps: []specdriven.PlanStep{
			{TaskKey: "1.1", ToolName: "bash", Args: map[string]any{"cmd": "rm -rf /"}},
		}}, "args diff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := equalPlan(want, tc.got)
			if err == nil || !strings.Contains(err.Error(), tc.sub) {
				t.Fatalf("want err containing %q, got %v", tc.sub, err)
			}
		})
	}
}

// TestEqualPlan_ArgsEncodeError 覆盖 canonicalJSON 失败分支（unencodable Args）。
func TestEqualPlan_ArgsEncodeError(t *testing.T) {
	unencodable := make(chan int)
	want := &specdriven.Plan{Steps: []specdriven.PlanStep{
		{TaskKey: "1.1", ToolName: "bash", Args: unencodable},
	}}
	got := &specdriven.Plan{Steps: []specdriven.PlanStep{
		{TaskKey: "1.1", ToolName: "bash", Args: map[string]any{"cmd": "ls"}},
	}}
	if err := equalPlan(want, got); err == nil || !strings.Contains(err.Error(), "want.args encode") {
		t.Fatalf("want 'want.args encode' error, got %v", err)
	}
	if err := equalPlan(got, want); err == nil || !strings.Contains(err.Error(), "got.args encode") {
		t.Fatalf("want 'got.args encode' error, got %v", err)
	}
}

// TestCanonicalJSON 覆盖 nil / normal / 不可编码 三条分支。
func TestCanonicalJSON(t *testing.T) {
	if b, err := canonicalJSON(nil); err != nil || string(b) != "null" {
		t.Errorf("nil case: b=%s err=%v", b, err)
	}
	if b, err := canonicalJSON(map[string]any{"a": 1, "b": 2}); err != nil || len(b) == 0 {
		t.Errorf("map case: b=%s err=%v", b, err)
	}
	if _, err := canonicalJSON(make(chan int)); err == nil {
		t.Error("expected error for unencodable chan type")
	}
}

// TestValidateSchema 覆盖 ValidateSchema 每条失败分支。
func TestValidateSchema(t *testing.T) {
	base := Case{Input: "x", WantContinuation: WantContinuation{Decision: "new"}}
	if err := ValidateSchema(base); err != nil {
		t.Fatalf("base happy path: %v", err)
	}
	if err := ValidateSchema(Case{Input: "x", WantContinuation: WantContinuation{Decision: "resume"}}); err == nil ||
		!strings.Contains(err.Error(), "change_id") {
		t.Errorf("resume missing change_id: got %v", err)
	}
	if err := ValidateSchema(Case{Input: "x", WantContinuation: WantContinuation{Decision: "resume", ChangeID: "c-x"}}); err != nil {
		t.Errorf("resume happy: %v", err)
	}
	if err := ValidateSchema(Case{Input: "x", WantContinuation: WantContinuation{Decision: "ask"}}); err == nil ||
		!strings.Contains(err.Error(), "ask_reason") {
		t.Errorf("ask missing reason: got %v", err)
	}
	if err := ValidateSchema(Case{Input: "x", WantContinuation: WantContinuation{Decision: "ask", AskReason: "why"}}); err != nil {
		t.Errorf("ask happy: %v", err)
	}
	if err := ValidateSchema(Case{Input: "x"}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("empty decision: got %v", err)
	}
	if err := ValidateSchema(Case{Input: "x", WantContinuation: WantContinuation{Decision: "bogus"}}); err == nil ||
		!strings.Contains(err.Error(), "not in") {
		t.Errorf("unknown decision: got %v", err)
	}
	if err := ValidateSchema(Case{WantContinuation: WantContinuation{Decision: "new"}}); err == nil ||
		!strings.Contains(err.Error(), "input") {
		t.Errorf("empty input: got %v", err)
	}
	if err := ValidateSchema(Case{
		Input: "x", WantContinuation: WantContinuation{Decision: "new"},
		StoreState: &StoreState{Revision: 2, ExportedRevision: 5},
	}); err == nil || !strings.Contains(err.Error(), "exported_revision") {
		t.Errorf("store state rev < exported: got %v", err)
	}
	if err := ValidateSchema(Case{
		Input: "x", WantContinuation: WantContinuation{Decision: "new"},
		StoreState: &StoreState{Revision: 5, ExportedRevision: 5},
	}); err != nil {
		t.Errorf("store state happy: %v", err)
	}
}

// TestRequiredSetComplete_Negative 补齐 RequiredSetComplete 两条失败分支（missing / required=false）。
// happy path 已由 TestEvalFixtures 间接覆盖，这里专攻反坑。
func TestRequiredSetComplete_Negative(t *testing.T) {
	if err := RequiredSetComplete(nil); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("empty set: want missing err, got %v", err)
	}
	lc := LoadedCase{
		Path: "testdata/fm01_wrong_continuation.json",
		Case: Case{Required: false},
	}
	err := RequiredSetComplete([]LoadedCase{lc})
	if err == nil || (!strings.Contains(err.Error(), "not marked required") && !strings.Contains(err.Error(), "missing")) {
		t.Errorf("non-required fixture: want required-flag err, got %v", err)
	}
}

// TestHarnessValidate 覆盖 Harness.validate 的 nil Runner 分支（fail-closed 关键词锚点）。
func TestHarnessValidate(t *testing.T) {
	err := Harness{}.validate()
	if err == nil {
		t.Fatal("want err for nil Runner")
	}
	for _, needle := range []string{"Runner", "nil", "fail-closed"} {
		if !strings.Contains(err.Error(), needle) {
			t.Errorf("validate err missing %q: %v", needle, err)
		}
	}
	if err := (Harness{Runner: fakeRunner{}}).validate(); err != nil {
		t.Errorf("wired Runner should pass validate: %v", err)
	}
}

// TestLoadCase_Errors 覆盖 LoadCase 的错误分支（不存在、非法 JSON、未知字段、尾随数据）。
func TestLoadCase_Errors(t *testing.T) {
	dir := t.TempDir()

	if _, err := LoadCase(filepath.Join(dir, "nonexistent.json")); err == nil {
		t.Error("expected read error for missing file")
	}

	bad := filepath.Join(dir, "unknown_field.json")
	if err := os.WriteFile(bad, []byte(`{"input":"x","bogus_field":1,"want_continuation":{"decision":"new"}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadCase(bad); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("want decode err, got %v", err)
	}

	trail := filepath.Join(dir, "trailing.json")
	if err := os.WriteFile(trail, []byte(`{"input":"x","want_continuation":{"decision":"new"}}{"extra":1}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadCase(trail); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Errorf("want trailing err, got %v", err)
	}
}

// TestLoadAll_Errors 覆盖 LoadAll 的不存在目录 / 单条 fixture 坏的级联失败。
func TestLoadAll_Errors(t *testing.T) {
	if _, err := LoadAll("/nonexistent-path-for-eval-test"); err == nil {
		t.Error("expected err on missing dir")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`not json`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadAll(dir); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("want decode err from LoadAll, got %v", err)
	}
	clean := t.TempDir()
	if err := os.Mkdir(filepath.Join(clean, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clean, "notes.txt"), []byte("irrelevant"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadAll(clean)
	if err != nil {
		t.Fatalf("LoadAll clean: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 cases, got %d", len(got))
	}
}

// naiveRunner 是 Sprint 2.1 引入的"朴素实现"——模拟一个还没落地任何业务
// 逻辑的 spec runner 最可能写出的 buggy-naive 默认：
//   - ResolveContinuation: 永远返回 resume + SessionState.ActiveChangeID，
//     不看 userText、不看 FocusMRU、不识别 keyword、不做 spec_ref capability
//     check、不做 FS/DB divergence 检测
//   - Plan: 返回 nil（不调 LLM）
//   - ExecuteFallback: 直接返回 nil（不执行任何 reload / fail-closed）
//
// 用途：对 fm01/03/05/06 four required fixture 跑 runCaseOnce，Sprint 2.1
// 规约是这 4 条必须全部返回 non-nil error——fixture 才算"真反例"（而不是
// fakeRunner echo-back 掩盖下的假绿）。
type naiveRunner struct{}

func (naiveRunner) ResolveContinuation(_ context.Context, c Case) (specdriven.Decision, error) {
	return specdriven.Decision{
		Kind:     specdriven.DecisionResume,
		ChangeID: c.SessionState.ActiveChangeID,
	}, nil
}

func (naiveRunner) Plan(_ context.Context, _ Case) (*specdriven.Plan, error) {
	return &specdriven.Plan{}, nil
}

func (naiveRunner) ExecuteFallback(_ context.Context, _ Case) error { return nil }

// TestHarnessBehavior_NaiveRunner_FMRequiredFail 是 Sprint 2.1 反例化硬锁：
// 4 条 required fixture（fm01/03/05/06）在 naiveRunner 下跑 runCaseOnce，
// 必须全部返回 non-nil error——证明 fixture 能 catch 朴素实现的反例，
// 不是 fakeRunner echo-back 下的 tautology。
//
// 对照实验（task 12.9 DONE 条款）：把 naiveRunner 换回 fakeRunner → 4 条全绿
// = 证明反例化前 fakeRunner 的"默认按 want 回射"遮蔽了真反例。
func TestHarnessBehavior_NaiveRunner_FMRequiredFail(t *testing.T) {
	cases, err := LoadAll("testdata")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	byName := map[string]LoadedCase{}
	for _, lc := range cases {
		byName[lc.Case.Name] = lc
	}
	// Sprint 2.1/2.2 蓝军：固化 err 信号子串，防"假失败"——naiveRunner 是为某个
	// 具体原因失败，不是为随机原因失败。没有 wantSignal 的 fixture 说明反例
	// 机制不明确，应在 fixture 里补 want_error 或其它 want_* 字段。
	expectedSignal := map[string]string{
		"fm01_wrong_continuation": "resume change_id",  // change_id mismatch branch
		"fm02_cas_conflict":       "CAS conflict",      // want_error echo (naive missing)
		"fm03_fs_db_divergence":   "FS/DB divergence",  // want_error echo (naive missing)
		"fm04_planner_drift":      "decision mismatch", // want=new got=resume
		"fm05_spec_ref_poisoning": "decision mismatch", // want=ask got=resume
		"fm06_lock_reentrancy":    "lock reentrancy",   // want_error echo (naive missing)
		"fm07_compaction_loss":    "resume change_id",  // want=c-exp got=c-main
		"fm08_eval_gate":          "decision mismatch", // want=new got=resume
	}
	for _, name := range []string{
		"fm01_wrong_continuation",
		"fm02_cas_conflict",
		"fm03_fs_db_divergence",
		"fm04_planner_drift",
		"fm05_spec_ref_poisoning",
		"fm06_lock_reentrancy",
		"fm07_compaction_loss",
		"fm08_eval_gate",
	} {
		t.Run(name, func(t *testing.T) {
			lc, ok := byName[name]
			if !ok {
				t.Fatalf("fixture %s not loaded", name)
			}
			err := runCaseOnce(context.Background(), lc, naiveRunner{})
			if err == nil {
				t.Fatalf("Sprint 2.1 counterexample regression: %s must fail under naiveRunner but got nil (fixture is tautological echo-back)", name)
			}
			sig := expectedSignal[name]
			if sig == "" {
				t.Fatalf("%s: missing expectedSignal (blue army R1 guard)", name)
			}
			if !strings.Contains(err.Error(), sig) {
				t.Fatalf("%s: naive err should contain signal %q but got %q (counterexample may be failing for wrong reason)", name, sig, err.Error())
			}
		})
	}
}

// TestFakeRunner_DefaultMustFailFM01 是最小反例锁：裸 naiveRunner{} + fm01
// 必须出错。独立于上面 table-driven test，显式留下 fm01 的"canary"
// 断言，防 Sprint 2.1 被静默回滚到 echo-back 假绿。
func TestFakeRunner_DefaultMustFailFM01(t *testing.T) {
	lc, err := LoadCase("testdata/fm01_wrong_continuation.json")
	if err != nil {
		t.Fatalf("load fm01: %v", err)
	}
	if err := runCaseOnce(context.Background(), lc, naiveRunner{}); err == nil {
		t.Fatal("fm01 must fail under naiveRunner (naive returns active=c-auth-rework; want=c-billing-fix)")
	}
}

// TestContrastEchoBackVsNaive 对照实验显式锁：同一 fixture 在 echo-back
// fakeRunner 下必 PASS，在 naiveRunner 下必 FAIL。解耦了"fixture 表达能力"
// 和"Runner 实现正确性"两件事——前者保证 schema/字段够用，后者保证反例
// 真能 catch buggy runner。
func TestContrastEchoBackVsNaive(t *testing.T) {
	cases, err := LoadAll("testdata")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	byName := map[string]LoadedCase{}
	for _, lc := range cases {
		byName[lc.Case.Name] = lc
	}
	for _, name := range []string{
		"fm01_wrong_continuation",
		"fm02_cas_conflict",
		"fm03_fs_db_divergence",
		"fm04_planner_drift",
		"fm05_spec_ref_poisoning",
		"fm06_lock_reentrancy",
		"fm07_compaction_loss",
		"fm08_eval_gate",
	} {
		t.Run(name, func(t *testing.T) {
			lc := byName[name]
			// echo-back must pass
			if err := runCaseOnce(context.Background(), lc, fakeRunner{echoWantError: true}); err != nil {
				t.Fatalf("%s: echo-back fakeRunner must PASS (tautology baseline), got: %v", name, err)
			}
			// naive must fail
			if err := runCaseOnce(context.Background(), lc, naiveRunner{}); err == nil {
				t.Fatalf("%s: naiveRunner must FAIL (counterexample gate), got nil", name)
			}
		})
	}
}

// TestCanonicalJSON_FM04IntegerArg 锁 Sprint 1.1 的 canonicalJSON 整数保真：
// fm04 fixture 带 {"timeout": 30} 整数 arg，若 canonicalJSON 退化到 float64 路径
// （把 30 变成 30.0），equalPlan 会误报 mismatch；本测试显式断言同值不同字面量
// 经规范化后 byte-equal，且 int 与 float 字面量必 diff。
//
// 防倒退：任何对 canonicalJSON 的修改若丢失 UseNumber / json.Number 路径都必在此红。
func TestCanonicalJSON_FM04IntegerArg(t *testing.T) {
	intArgs := map[string]any{"timeout": 30}
	// json.Unmarshal 到 any 会把 30 变 float64(30)——这是原 bug 场景
	var unmarshaled any
	if err := json.Unmarshal([]byte(`{"timeout": 30}`), &unmarshaled); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	floatArgs := unmarshaled // 含 float64(30)

	intBytes, err := canonicalJSON(intArgs)
	if err != nil {
		t.Fatalf("canonicalJSON(int): %v", err)
	}
	floatBytes, err := canonicalJSON(floatArgs)
	if err != nil {
		t.Fatalf("canonicalJSON(float): %v", err)
	}
	// Sprint 1.1 红线：int literal vs 已经被 float64 坍塌后的值，经 canonicalJSON
	// 规范化后必须输出相同 byte（UseNumber 路径让 30 字面量保留）。
	if string(intBytes) != string(floatBytes) {
		// 允许两种合法结果：都是 "30" 或都是 "30.0"——但必须一致。
		// 若 intBytes="30" 而 floatBytes="30" 说明 canonicalJSON 已把
		// float64(30) 也规范化回整数字面量（最理想路径）。不等则代表退化。
		t.Errorf("canonicalJSON integer preservation failed: int=%s float=%s", intBytes, floatBytes)
	}
	// 显式 diff 锁：{"n":30} vs {"n":30.5} 必须 byte-diff（防"任何数字都规范化成一样"的过激实现）。
	a, _ := canonicalJSON(map[string]any{"n": 30})
	b, _ := canonicalJSON(map[string]any{"n": 30.5})
	if string(a) == string(b) {
		t.Fatalf("canonicalJSON over-collapsed: {30} == {30.5}")
	}

	// 端到端：fm04 fixture 通过 fakeRunner(echo) 经 equalPlan 必绿。
	lc, err := LoadCase("testdata/fm04_planner_drift.json")
	if err != nil {
		t.Fatalf("load fm04: %v", err)
	}
	if err := runCaseOnce(context.Background(), lc, fakeRunner{echoWantError: true, decisionOverride: &specdriven.Decision{Kind: specdriven.DecisionNew}}); err != nil {
		t.Fatalf("fm04 echo-back must PASS with integer timeout arg: %v", err)
	}
}

// compile-time assertions
var _ Runner = anonRunner{}
var _ Runner = fakeRunner{}
var _ Runner = naiveRunner{}

// stubLogger 捕获 recordCaseResult 输出的 WARN 日志；无需 *testing.T。
type stubLogger struct {
	lines []string
}

func (s *stubLogger) Logf(format string, args ...any) {
	s.lines = append(s.lines, fmt.Sprintf(format, args...))
}

// TestSummary_RecordCaseResult 穷举 Summary.recordCaseResult 的三路分支——
// ok=true / required-fail / optional-fail（+ WARN log）——without 靠 t.Run 失败
// 传递来触发（否则 parent test 会被 subtest 失败染红）。Sprint 2.4 蓝军 R1 产物：
// 把 RunFixtures 主循环里的 switch 抽出后，这三条分支终于能在"test 全绿"前提下
// 被真实覆盖；per-function coverage 从 73.1% 提升到 ≥85%。
func TestSummary_RecordCaseResult(t *testing.T) {
	mkRequired := func() LoadedCase {
		return LoadedCase{Path: "testdata/mock_required.json", Case: Case{Name: "mock_required", Required: true}}
	}
	mkOptional := func() LoadedCase {
		return LoadedCase{Path: "testdata/mock_optional.json", Case: Case{Name: "mock_optional", Required: false}}
	}

	t.Run("ok_required", func(t *testing.T) {
		s := &Summary{}
		logger := &stubLogger{}
		s.recordCaseResult(true, mkRequired(), logger)
		if s.Passed != 1 || s.RequiredPassed != 1 {
			t.Fatalf("required ok: want Passed=1 RequiredPassed=1, got %+v", s)
		}
		if len(s.RequiredFailed) != 0 || len(s.OptionalFailed) != 0 {
			t.Fatalf("required ok: no failures expected, got %+v", s)
		}
		if len(logger.lines) != 0 {
			t.Fatalf("required ok: expected no WARN, got %v", logger.lines)
		}
	})

	t.Run("ok_optional", func(t *testing.T) {
		s := &Summary{}
		logger := &stubLogger{}
		s.recordCaseResult(true, mkOptional(), logger)
		if s.Passed != 1 || s.RequiredPassed != 0 {
			t.Fatalf("optional ok: want Passed=1 RequiredPassed=0, got %+v", s)
		}
		if len(logger.lines) != 0 {
			t.Fatalf("optional ok: expected no WARN, got %v", logger.lines)
		}
	})

	t.Run("required_fail", func(t *testing.T) {
		s := &Summary{}
		logger := &stubLogger{}
		s.recordCaseResult(false, mkRequired(), logger)
		if s.Passed != 0 || s.RequiredPassed != 0 {
			t.Fatalf("required fail: want Passed=0 RequiredPassed=0, got %+v", s)
		}
		if len(s.RequiredFailed) != 1 || s.RequiredFailed[0] != "mock_required.json" {
			t.Fatalf("required fail: want RequiredFailed=[mock_required.json], got %v", s.RequiredFailed)
		}
		if len(logger.lines) != 0 {
			t.Fatalf("required fail: no WARN expected (required 是硬红，不是 WARN), got %v", logger.lines)
		}
	})

	t.Run("optional_fail_emits_warn", func(t *testing.T) {
		s := &Summary{}
		logger := &stubLogger{}
		s.recordCaseResult(false, mkOptional(), logger)
		if len(s.OptionalFailed) != 1 || s.OptionalFailed[0] != "mock_optional.json" {
			t.Fatalf("optional fail: want OptionalFailed=[mock_optional.json], got %v", s.OptionalFailed)
		}
		if len(s.RequiredFailed) != 0 {
			t.Fatalf("optional fail 不应污染 RequiredFailed: %v", s.RequiredFailed)
		}
		if len(logger.lines) != 1 {
			t.Fatalf("optional fail: want 1 WARN line, got %d: %v", len(logger.lines), logger.lines)
		}
		if !strings.Contains(logger.lines[0], "WARN optional fixture failed") ||
			!strings.Contains(logger.lines[0], "mock_optional.json") {
			t.Fatalf("optional fail: WARN 格式错，got %q", logger.lines[0])
		}
	})

	t.Run("multi_cases_accumulate", func(t *testing.T) {
		s := &Summary{Total: 5}
		logger := &stubLogger{}
		s.recordCaseResult(true, mkRequired(), logger)
		s.recordCaseResult(true, mkRequired(), logger)
		s.recordCaseResult(false, mkRequired(), logger)
		s.recordCaseResult(false, mkOptional(), logger)
		s.recordCaseResult(false, mkOptional(), logger)
		if s.Passed != 2 || s.RequiredPassed != 2 {
			t.Fatalf("accumulate: Passed/RequiredPassed wrong: %+v", s)
		}
		if len(s.RequiredFailed) != 1 {
			t.Fatalf("accumulate: RequiredFailed want 1 got %d", len(s.RequiredFailed))
		}
		if len(s.OptionalFailed) != 2 {
			t.Fatalf("accumulate: OptionalFailed want 2 got %d", len(s.OptionalFailed))
		}
		if len(logger.lines) != 2 {
			t.Fatalf("accumulate: 2 optional fail → 2 WARN, got %d", len(logger.lines))
		}
	})
}

// TestHarness_Preflight 覆盖 RunFixtures 原先两条 t.Fatal 路径——
// 抽成 error 接口后可直接单测，不再被 testing.T 的父-子失败联动卡住。
func TestHarness_Preflight(t *testing.T) {
	t.Parallel()

	t.Run("nil_runner_rejected", func(t *testing.T) {
		var h Harness // Runner 未注入
		// R3 tautology fix: 传合规 required-set，让 validate() 失败成为唯一可能原因
		complete := []LoadedCase{
			{Path: "fm01.json", Case: Case{Required: true}},
			{Path: "fm02.json", Case: Case{Required: true}},
			{Path: "fm03.json", Case: Case{Required: true}},
			{Path: "fm04.json", Case: Case{Required: true}},
			{Path: "fm05.json", Case: Case{Required: true}},
			{Path: "fm06.json", Case: Case{Required: true}},
			{Path: "fm07.json", Case: Case{Required: true}},
			{Path: "fm08.json", Case: Case{Required: true}},
		}
		err := h.preflight(complete)
		if err == nil {
			t.Fatal("nil Runner must fail preflight (D7 fail-closed)")
		}
		// err 必须来自 validate 而非 required-set wrap
		if strings.Contains(err.Error(), "required-set incomplete") {
			t.Fatalf("wrong branch: expected validate error, got required-set wrap: %v", err)
		}
	})

	t.Run("incomplete_required_set_rejected", func(t *testing.T) {
		h := Harness{Runner: noopRunner{}}
		// 只给 fm01，缺 fm02-fm08（Path-based 识别，Required=true 合规）
		cases := []LoadedCase{{Path: "fm01.json", Case: Case{Required: true}}}
		err := h.preflight(cases)
		if err == nil {
			t.Fatal("incomplete required-set must fail preflight")
		}
		if !strings.Contains(err.Error(), "required-set incomplete") {
			t.Fatalf("expected wrapped required-set error, got: %v", err)
		}
	})

	t.Run("complete_required_set_passes", func(t *testing.T) {
		h := Harness{Runner: noopRunner{}}
		cases := []LoadedCase{
			{Path: "fm01.json", Case: Case{Required: true}},
			{Path: "fm02.json", Case: Case{Required: true}},
			{Path: "fm03.json", Case: Case{Required: true}},
			{Path: "fm04.json", Case: Case{Required: true}},
			{Path: "fm05.json", Case: Case{Required: true}},
			{Path: "fm06.json", Case: Case{Required: true}},
			{Path: "fm07.json", Case: Case{Required: true}},
			{Path: "fm08.json", Case: Case{Required: true}},
		}
		if err := h.preflight(cases); err != nil {
			t.Fatalf("complete required-set must pass, got: %v", err)
		}
	})
}

// TestSummary_TerminalGate 覆盖 RunFixtures 最后一条 t.Fatal 路径——
// 抽成 Summary.terminalGate() error 之后可以直接测两条分支。
func TestSummary_TerminalGate(t *testing.T) {
	t.Parallel()

	t.Run("empty_required_failed_passes", func(t *testing.T) {
		s := Summary{Total: 8, Passed: 8, RequiredTotal: 8, RequiredPassed: 8}
		if err := s.terminalGate(); err != nil {
			t.Fatalf("no required failure → gate must pass, got: %v", err)
		}
	})

	t.Run("any_required_failed_blocks", func(t *testing.T) {
		s := Summary{RequiredFailed: []string{"fm02.json"}}
		err := s.terminalGate()
		if err == nil {
			t.Fatal("non-empty RequiredFailed must block terminal gate")
		}
		if !strings.Contains(err.Error(), "blocking dual-flag rollout") {
			t.Fatalf("expected dual-flag rollout message, got: %v", err)
		}
		if !strings.Contains(err.Error(), "fm02.json") {
			t.Fatalf("expected failed fixture name in err, got: %v", err)
		}
	})
}
