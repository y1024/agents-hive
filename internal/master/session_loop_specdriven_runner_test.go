package master

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/llm"
	"github.com/chef-guo/agents-hive/internal/specdriven"
	"github.com/chef-guo/agents-hive/internal/specdriven/ingress"
	"github.com/chef-guo/agents-hive/internal/specdriven/intake"
	"github.com/chef-guo/agents-hive/internal/specdriven/planner"
)

// fakeSpecRunner 是 Sprint 3.3.d 测试用 runner。
// 不调 LLM，不读 store；按构造参数直接回结果，用于驱动 applySpecDrivenIntake 的
// success / schema-invalid / timeout / over-budget 四路蓝军矩阵。
//
// 为什么不复用 ingress.MinimalRunner：MinimalRunner 硬写 ErrPlannerSchemaInvalid，
// 不能 parametrize success path——测试要锁住 PathSpec/PathDual 必须能注入非 nil Context。
type fakeSpecRunner struct {
	ctx   *specdriven.Context
	stats ingress.RunStats
	err   error
	calls int // 蓝军反锁：断言真被调了，防止 applySpecDrivenIntake 绕过 runner
}

func (f *fakeSpecRunner) Run(_ context.Context, _ string, _ string) (*specdriven.Context, ingress.RunStats, error) {
	f.calls++
	return f.ctx, f.stats, f.err
}

// newIntakeTestMasterWithRunner 构造可注入 runner 的 Master。
// 与 newSpecDrivenTestMaster 的区别：本函数允许 mode 与 runner 双轴参数化，
// 让 dual / spec × success / err 四象限测试共用同一个 builder。
func newIntakeTestMasterWithRunner(t *testing.T, mode intake.Mode, runner ingress.Runner) *Master {
	t.Helper()
	return &Master{
		config: Config{
			SpecDriven: config.SpecDrivenConfig{
				Mode: string(mode),
				Continuation: config.SpecContinuationConfig{
					Default: config.DefaultSpecContinuationDefault,
				},
				Planner: config.SpecPlannerConfig{
					TokenBudget: config.DefaultSpecPlannerTokenBudget,
				},
			},
		},
		logger:     zaptest.NewLogger(t),
		specRunner: runner,
	}
}

// --------------------------------------------------------------------
// Sprint 3.3.d TG10.5/10.6 核心断言：PathSpec / PathDual 真可达
// --------------------------------------------------------------------

// TestApplySpecDrivenIntake_SpecMode_RunnerSuccess 锁死 3.3.d 主路径：
// mode=spec + runner 返回非 nil Context → PathSpec，specCtx 挂到 session 上。
//
// 蓝军反锁（见 assertions）：
//   - Path 必 PathSpec（不是之前 stub 的 PathLegacy）
//   - runner.calls == 1（证明真调了 runner，不是绕过）
//   - session.LoadSpecCtx() != nil 且 ChangeID == 注入值（证明 plumbing 穿透）
//   - Revision == 1（RealRunner spine 契约）
//
// R-mutation（防假绿）：若把 ResolvedSpecCtx 从 ResolveInput 去掉 → PathSpec 返 Legacy 红。
func TestApplySpecDrivenIntake_SpecMode_RunnerSuccess(t *testing.T) {
	fake := &fakeSpecRunner{
		ctx: &specdriven.Context{
			ChangeID:       "add-user-login",
			CurrentTaskKey: "1.1",
			Revision:       1,
		},
		stats: ingress.RunStats{
			Usage:          llm.Usage{PromptTokens: 120, CompletionTokens: 80, TotalTokens: 200},
			BudgetExceeded: false,
		},
	}
	m := newIntakeTestMasterWithRunner(t, intake.ModeSpec, fake)
	session := newSpecDrivenTestSession("sess-spec-real")

	path := m.applySpecDrivenIntake(session, "实现用户登录")

	// 4 断言（颗粒度齐发）
	assert.Equal(t, intake.PathSpec, path, "mode=spec + runner success → 必须走 PathSpec")
	assert.Equal(t, 1, fake.calls, "runner 必须真被调一次——0=绕过、>1=重复 spine")
	ctx := session.LoadSpecCtx()
	require.NotNil(t, ctx, "specCtx 必须挂 session，之前 plumbing bug = 永远 nil")
	assert.Equal(t, "add-user-login", ctx.ChangeID, "ChangeID 必须从 runner 穿透")
	assert.Equal(t, 1, ctx.Revision, "Revision=1 是 RealRunner 新建语义契约")
	assert.Equal(t, "1.1", ctx.CurrentTaskKey)
}

// TestApplySpecDrivenIntake_DualMode_RunnerSuccess 锁死 dual 路径：
// mode=dual + runner success → PathDual（响应取 legacy，但 specCtx 仍挂用于后续 diff log）。
func TestApplySpecDrivenIntake_DualMode_RunnerSuccess(t *testing.T) {
	fake := &fakeSpecRunner{
		ctx: &specdriven.Context{
			ChangeID:       "refactor-auth",
			CurrentTaskKey: "2.3",
			Revision:       1,
		},
		stats: ingress.RunStats{
			Usage:          llm.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
			BudgetExceeded: false,
		},
	}
	m := newIntakeTestMasterWithRunner(t, intake.ModeDual, fake)
	session := newSpecDrivenTestSession("sess-dual-real")

	path := m.applySpecDrivenIntake(session, "重构 auth 中间件")

	assert.Equal(t, intake.PathDual, path, "mode=dual + runner success → PathDual（非 Legacy）")
	assert.Equal(t, 1, fake.calls)
	ctx := session.LoadSpecCtx()
	require.NotNil(t, ctx, "dual 也必须挂 specCtx 供 diff log 使用")
	assert.Equal(t, "refactor-auth", ctx.ChangeID)
	assert.Equal(t, 1, ctx.Revision)
}

// --------------------------------------------------------------------
// Sprint 3.3.d 蓝军矩阵：错误路径仍稳定降级
// --------------------------------------------------------------------

// TestApplySpecDrivenIntake_SpecMode_SchemaInvalid_Downshifts
// 蓝军：runner 返 ErrPlannerSchemaInvalid → 即使 mode=spec 也必须 downshift 到 Legacy。
// 断言 fallback metric 分类走 schema_invalid（不是 unknown）。
func TestApplySpecDrivenIntake_SpecMode_SchemaInvalid_Downshifts(t *testing.T) {
	fake := &fakeSpecRunner{
		ctx: nil,
		err: planner.ErrPlannerSchemaInvalid,
	}
	m := newIntakeTestMasterWithRunner(t, intake.ModeSpec, fake)
	session := newSpecDrivenTestSession("sess-schema-fail")

	path := m.applySpecDrivenIntake(session, "bad llm output scenario")

	assert.Equal(t, intake.PathLegacy, path, "schema 失败必须 fail-closed 到 legacy")
	assert.Nil(t, session.LoadSpecCtx(), "downshift 必须 StoreSpecCtx(nil) 清零")
	assert.Equal(t, 1, fake.calls)
	// 正向反锁：classifyPlannerErr 把 ErrPlannerSchemaInvalid 路由到 schema_invalid reason
	assert.Equal(t, specdriven.FallbackReasonSchemaInvalid, m.classifyPlannerErr(fake.err))
}

// TestApplySpecDrivenIntake_SpecMode_TimeoutDownshift_ReasonRoutes
// 蓝军：runner 返 context.DeadlineExceeded → Downshift reason 是 planner_timeout，
// 不是 planner_schema。这是 3.3.d 新加的 reason 分流断言——之前是 hardcoded schema。
func TestApplySpecDrivenIntake_SpecMode_TimeoutDownshift_ReasonRoutes(t *testing.T) {
	fake := &fakeSpecRunner{
		ctx: nil,
		err: context.DeadlineExceeded,
	}
	m := newIntakeTestMasterWithRunner(t, intake.ModeSpec, fake)
	session := newSpecDrivenTestSession("sess-timeout")

	path := m.applySpecDrivenIntake(session, "llm 超时场景")

	assert.Equal(t, intake.PathLegacy, path)
	assert.Nil(t, session.LoadSpecCtx())
	// 反向反锁：classifyPlannerErr 把 DeadlineExceeded 路由到 llm_timeout
	assert.Equal(t, specdriven.FallbackReasonLLMTimeout, m.classifyPlannerErr(fake.err))
}

// TestApplySpecDrivenIntake_DualMode_RealLLMSchemaDrift 是 Sprint 3.3.d 要求的
// dual-mode schema drift 场景：runner 调了真 LLM，花了 tokens，但 decode 失败。
//
// 断言：
//   - Path == PathLegacy（drift 必降级，即使 mode=dual）
//   - runner 被调（calls==1）
//   - specCtx 被清零
//   - classify 正确路由到 schema_invalid
//
// 这是 task 10.5 "dual 模式 diff" 的反例锁——drift 时不能走 PathDual，否则
// 一个坏 plan 会 silently 被挂 session 干扰下游。
func TestApplySpecDrivenIntake_DualMode_RealLLMSchemaDrift(t *testing.T) {
	fake := &fakeSpecRunner{
		ctx: nil,
		stats: ingress.RunStats{
			// 模拟：LLM 真调了，tokens 花了，但 decode 失败
			Usage: llm.Usage{PromptTokens: 300, CompletionTokens: 150, TotalTokens: 450},
		},
		err: planner.ErrPlannerSchemaInvalid,
	}
	m := newIntakeTestMasterWithRunner(t, intake.ModeDual, fake)
	session := newSpecDrivenTestSession("sess-dual-drift")

	path := m.applySpecDrivenIntake(session, "实现新功能")

	assert.Equal(t, intake.PathLegacy, path, "dual + schema drift 必须 fail-closed，不能 PathDual")
	assert.Nil(t, session.LoadSpecCtx(), "drift 不能留污染 specCtx")
	assert.Equal(t, 1, fake.calls, "runner 必调——否则 token_cost metric 不会 emit 是纸老虎")
	assert.Equal(t, specdriven.FallbackReasonSchemaInvalid, m.classifyPlannerErr(fake.err))
}

// TestApplySpecDrivenIntake_UnknownErr_RoutesToUnknownReason
// 蓝军补刀：未知 sentinel err → classifyPlannerErr → FallbackReasonUnknown。
// 保护"unknown 暴涨 = 分类漏 sentinel"的观察点不被静默吞没。
func TestApplySpecDrivenIntake_UnknownErr_RoutesToUnknownReason(t *testing.T) {
	customErr := errors.New("llm provider 5xx")
	fake := &fakeSpecRunner{err: customErr}
	m := newIntakeTestMasterWithRunner(t, intake.ModeSpec, fake)
	session := newSpecDrivenTestSession("sess-unknown")

	path := m.applySpecDrivenIntake(session, "任意请求")

	assert.Equal(t, intake.PathLegacy, path, "未知 err 也必须降级")
	assert.Equal(t, specdriven.FallbackReasonUnknown, m.classifyPlannerErr(customErr),
		"未知 err 必须路由到 unknown——被默认归类 schema 会掩盖分布漂移")
}

// 编译期反锁：fakeSpecRunner 必须实现 ingress.Runner（契约 drift 立即爆出）
var _ ingress.Runner = (*fakeSpecRunner)(nil)
