package master

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/skills"
	"github.com/chef-guo/agents-hive/internal/store"
	"github.com/chef-guo/agents-hive/internal/subagent"
)

func TestAgentQualityRunAdapterUsesProductionProcessMessagePath(t *testing.T) {
	logger := zaptest.NewLogger(t)
	m := NewMaster(Config{}, config.HITLConfig{Enabled: false}, subagent.NewRegistry(logger), skills.NewRegistry(logger), store.NewMemoryStore(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	defer m.Stop()
	go func() {
		_ = m.SessionLoop(ctx)
	}()

	adapter := NewAgentQualityRunAdapter(m)
	runCtx, cancelRun := context.WithTimeout(context.Background(), time.Second)
	defer cancelRun()
	out, err := adapter.RunCase(runCtx, agentquality.AgentRunCaseInput{
		Case: agentquality.Case{
			ID:             "real-path-case",
			Input:          "hello",
			ExpectedStatus: agentquality.StatusPass,
		},
		RunID:     "trace-agent-quality-test",
		SessionID: "eval-real-path-case",
		OwnerID:   "quality-admin",
	})

	if err == nil {
		t.Fatalf("RunCase error = nil, want real master path failure with unconfigured LLM")
	}
	if out.TraceID != "trace-agent-quality-test" {
		t.Fatalf("TraceID = %q", out.TraceID)
	}
	if out.FinalStatus != agentquality.StatusFail {
		t.Fatalf("FinalStatus = %s, want fail", out.FinalStatus)
	}
	if out.ReplayRef != "session:eval-real-path-case" {
		t.Fatalf("ReplayRef = %q", out.ReplayRef)
	}
	if got := m.sessionMgr.GetSession("eval-real-path-case"); got == nil {
		t.Fatal("adapter did not create eval session")
	}
	if len(out.Events) == 0 || out.Events[len(out.Events)-1].Name != agentquality.EventAgentTurn {
		t.Fatalf("missing agent turn event: %+v", out.Events)
	}
}

func TestAgentQualityRunAdapterSandboxBlocksUndeclaredSideEffectBeforeProductionPath(t *testing.T) {
	logger := zaptest.NewLogger(t)
	m := NewMaster(Config{}, config.HITLConfig{Enabled: false}, subagent.NewRegistry(logger), skills.NewRegistry(logger), store.NewMemoryStore(), logger)
	adapter := NewAgentQualityRunAdapter(m)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := adapter.RunCase(ctx, agentquality.AgentRunCaseInput{
		Case: agentquality.Case{
			ID:             "sandbox-side-effect-case",
			Input:          "给郭松发一条飞书消息",
			ExpectedTools:  []string{"feishu_api"},
			ExpectedStatus: agentquality.StatusBlocked,
		},
		RunID:           "trace-sandbox-side-effect",
		SessionID:       "eval-sandbox-side-effect-case",
		SandboxExternal: true,
	})

	if err != nil {
		t.Fatalf("RunCase error = %v", err)
	}
	if out.FinalStatus != agentquality.StatusBlocked {
		t.Fatalf("FinalStatus = %s, want blocked", out.FinalStatus)
	}
	if out.ReplayRef != "sandbox:blocked" {
		t.Fatalf("ReplayRef = %q, want sandbox:blocked", out.ReplayRef)
	}
	if got := m.sessionMgr.GetSession("eval-sandbox-side-effect-case"); got != nil {
		t.Fatal("sandbox preflight must not create eval session or call production ProcessMessage path")
	}
	if len(out.Events) == 0 || out.Events[0].Name != agentquality.EventToolDecision {
		t.Fatalf("missing sandbox tool decision event: %+v", out.Events)
	}
	if out.Events[0].FinalStatus != agentquality.StatusBlocked {
		t.Fatalf("event FinalStatus = %s, want blocked", out.Events[0].FinalStatus)
	}
}

func TestAgentQualityRunAdapterSandboxBlocksExternalWriteIntentWithoutToolDeclaration(t *testing.T) {
	logger := zaptest.NewLogger(t)
	m := NewMaster(Config{}, config.HITLConfig{Enabled: false}, subagent.NewRegistry(logger), skills.NewRegistry(logger), store.NewMemoryStore(), logger)
	adapter := NewAgentQualityRunAdapter(m)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := adapter.RunCase(ctx, agentquality.AgentRunCaseInput{
		Case: agentquality.Case{
			ID:             "sandbox-external-intent-case",
			Input:          "给飞书用户郭松发一条消息",
			ExpectedStatus: agentquality.StatusBlocked,
		},
		RunID:           "trace-sandbox-external-intent",
		SessionID:       "eval-sandbox-external-intent-case",
		SandboxExternal: true,
	})

	if err != nil {
		t.Fatalf("RunCase error = %v", err)
	}
	if out.FinalStatus != agentquality.StatusBlocked {
		t.Fatalf("FinalStatus = %s, want blocked", out.FinalStatus)
	}
	if out.ReplayRef != "sandbox:blocked" {
		t.Fatalf("ReplayRef = %q, want sandbox:blocked", out.ReplayRef)
	}
	if got := m.sessionMgr.GetSession("eval-sandbox-external-intent-case"); got != nil {
		t.Fatal("sandbox external intent preflight must not create eval session or call production ProcessMessage path")
	}
}
