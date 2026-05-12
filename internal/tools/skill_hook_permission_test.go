package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chef-guo/agents-hive/internal/errs"
)

type recordingShellExecutor struct {
	calls []string
}

func (e *recordingShellExecutor) Execute(command string) (string, string, error) {
	e.calls = append(e.calls, command)
	return "ok", "", nil
}

type staticSafeExecChecker struct {
	policy string
}

func (c staticSafeExecChecker) MatchPolicy(string) string {
	return c.policy
}

type recordingApprovalBridge struct {
	approved    bool
	err         error
	called      bool
	toolName    string
	description string
	details     map[string]string
}

func (b *recordingApprovalBridge) RequestApproval(_ context.Context, toolName, description string, details map[string]string) (bool, error) {
	b.called = true
	b.toolName = toolName
	b.description = description
	b.details = details
	return b.approved, b.err
}

func withSkillShellPermissionGlobals(t *testing.T, checker SafeExecChecker, bridge ApprovalBridge) {
	t.Helper()
	oldSafeExec := globalSafeExec
	oldApprovalBridge := globalApprovalBridge
	globalSafeExec = checker
	globalApprovalBridge = bridge
	t.Cleanup(func() {
		globalSafeExec = oldSafeExec
		globalApprovalBridge = oldApprovalBridge
	})
}

func TestApprovalGuardedShellExecutor_AskApprovedExecutesCommand(t *testing.T) {
	inner := &recordingShellExecutor{}
	bridge := &recordingApprovalBridge{approved: true}
	withSkillShellPermissionGlobals(t, staticSafeExecChecker{policy: "ask"}, bridge)

	exec := newApprovalGuardedShellExecutor(context.Background(), inner)
	stdout, _, err := exec.Execute("touch /tmp/hook")
	if err != nil {
		t.Fatalf("approved ask should execute command: %v", err)
	}
	if stdout != "ok" {
		t.Fatalf("stdout = %q, want ok", stdout)
	}
	if len(inner.calls) != 1 || inner.calls[0] != "touch /tmp/hook" {
		t.Fatalf("inner calls = %#v", inner.calls)
	}
	if !bridge.called {
		t.Fatal("ask policy should request approval")
	}
	if bridge.toolName != "bash" {
		t.Fatalf("approval toolName = %q, want bash", bridge.toolName)
	}
	if bridge.details["command"] != "touch /tmp/hook" || bridge.details["source"] != "skill" {
		t.Fatalf("approval details = %#v", bridge.details)
	}
}

func TestApprovalGuardedShellExecutor_ChecksUnwrappedHookCommand(t *testing.T) {
	inner := &recordingShellExecutor{}
	bridge := &recordingApprovalBridge{approved: true}
	checker := &substringSafeExecChecker{
		rules: map[string]string{
			"rm -rf /tmp/hook": "ask",
		},
		defaultPolicy: "allow",
	}
	withSkillShellPermissionGlobals(t, checker, bridge)

	exec := newApprovalGuardedShellExecutor(context.Background(), inner)
	command := `cd "/skills/demo" && rm -rf /tmp/hook`
	if _, _, err := exec.Execute(command); err != nil {
		t.Fatalf("approved unwrapped hook should execute: %v", err)
	}
	if !bridge.called {
		t.Fatal("unwrapped hook command should request approval")
	}
	if bridge.details["command"] != "rm -rf /tmp/hook" {
		t.Fatalf("approval command = %q, want unwrapped hook command", bridge.details["command"])
	}
	if bridge.details["execution_command"] != command {
		t.Fatalf("approval execution_command = %q, want %q", bridge.details["execution_command"], command)
	}
	if len(inner.calls) != 1 || inner.calls[0] != command {
		t.Fatalf("inner calls = %#v", inner.calls)
	}
}

func TestApprovalGuardedShellExecutor_UnwrappedDenyWinsOverWrappedAllow(t *testing.T) {
	inner := &recordingShellExecutor{}
	checker := &substringSafeExecChecker{
		rules: map[string]string{
			"rm -rf /tmp/hook": "deny",
		},
		defaultPolicy: "allow",
	}
	withSkillShellPermissionGlobals(t, checker, nil)

	exec := newApprovalGuardedShellExecutor(context.Background(), inner)
	_, _, err := exec.Execute(`cd "/skills/demo" && rm -rf /tmp/hook`)
	if err == nil {
		t.Fatal("unwrapped deny should fail")
	}
	if !errs.IsCode(err, errs.CodeExecDenied) {
		t.Fatalf("error code = %v, want CodeExecDenied", err)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("denied command should not execute, calls=%#v", inner.calls)
	}
}

func TestApprovalGuardedShellExecutor_AskDeniedSkipsCommand(t *testing.T) {
	inner := &recordingShellExecutor{}
	bridge := &recordingApprovalBridge{approved: false}
	withSkillShellPermissionGlobals(t, staticSafeExecChecker{policy: "ask"}, bridge)

	exec := newApprovalGuardedShellExecutor(context.Background(), inner)
	_, _, err := exec.Execute("touch /tmp/hook")
	if err == nil {
		t.Fatal("denied ask should fail")
	}
	if !errs.IsCode(err, errs.CodePermissionDenied) {
		t.Fatalf("error code = %v, want CodePermissionDenied", err)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error should be terminal permission text, got %q", err.Error())
	}
	if len(inner.calls) != 0 {
		t.Fatalf("denied command should not execute, calls=%#v", inner.calls)
	}
}

func TestApprovalGuardedShellExecutor_DenySkipsCommand(t *testing.T) {
	inner := &recordingShellExecutor{}
	withSkillShellPermissionGlobals(t, staticSafeExecChecker{policy: "deny"}, nil)

	exec := newApprovalGuardedShellExecutor(context.Background(), inner)
	_, _, err := exec.Execute("rm -rf /tmp/hook")
	if err == nil {
		t.Fatal("deny policy should fail")
	}
	if !errs.IsCode(err, errs.CodeExecDenied) {
		t.Fatalf("error code = %v, want CodeExecDenied", err)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("denied command should not execute, calls=%#v", inner.calls)
	}
}

func TestApprovalGuardedShellExecutor_AskWithoutBridgeFailsClosed(t *testing.T) {
	inner := &recordingShellExecutor{}
	withSkillShellPermissionGlobals(t, staticSafeExecChecker{policy: "ask"}, nil)

	exec := newApprovalGuardedShellExecutor(context.Background(), inner)
	_, _, err := exec.Execute("curl https://example.com")
	if err == nil {
		t.Fatal("ask without approval bridge should fail")
	}
	if !errs.IsCode(err, errs.CodePermissionDenied) {
		t.Fatalf("error code = %v, want CodePermissionDenied", err)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("command should not execute without approval bridge, calls=%#v", inner.calls)
	}
}

func TestApprovalGuardedShellExecutor_ApprovalErrorSkipsCommand(t *testing.T) {
	inner := &recordingShellExecutor{}
	bridge := &recordingApprovalBridge{err: errors.New("approval unavailable")}
	withSkillShellPermissionGlobals(t, staticSafeExecChecker{policy: "ask"}, bridge)

	exec := newApprovalGuardedShellExecutor(context.Background(), inner)
	_, _, err := exec.Execute("curl https://example.com")
	if err == nil {
		t.Fatal("approval error should fail")
	}
	if !errs.IsCode(err, errs.CodeExecApprovalTimeout) {
		t.Fatalf("error code = %v, want CodeExecApprovalTimeout", err)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("command should not execute after approval error, calls=%#v", inner.calls)
	}
}

func TestApprovalGuardedShellExecutor_AllowDoesNotAsk(t *testing.T) {
	inner := &recordingShellExecutor{}
	bridge := &recordingApprovalBridge{approved: false}
	withSkillShellPermissionGlobals(t, staticSafeExecChecker{policy: "allow"}, bridge)

	exec := newApprovalGuardedShellExecutor(context.Background(), inner)
	if _, _, err := exec.Execute("echo ok"); err != nil {
		t.Fatalf("allow policy should execute: %v", err)
	}
	if bridge.called {
		t.Fatal("allow policy should not request approval")
	}
	if len(inner.calls) != 1 {
		t.Fatalf("inner calls = %#v", inner.calls)
	}
}

func TestApprovalGuardedShellExecutor_NoPolicyCheckerPreservesExistingBehavior(t *testing.T) {
	inner := &recordingShellExecutor{}
	bridge := &recordingApprovalBridge{approved: false}
	withSkillShellPermissionGlobals(t, nil, bridge)

	exec := newApprovalGuardedShellExecutor(context.Background(), inner)
	if _, _, err := exec.Execute("echo legacy"); err != nil {
		t.Fatalf("nil policy checker should pass through: %v", err)
	}
	if bridge.called {
		t.Fatal("nil policy checker should not request approval")
	}
	if len(inner.calls) != 1 {
		t.Fatalf("inner calls = %#v", inner.calls)
	}
}

type substringSafeExecChecker struct {
	rules         map[string]string
	defaultPolicy string
}

func (c *substringSafeExecChecker) MatchPolicy(command string) string {
	for pattern, policy := range c.rules {
		if strings.Contains(command, pattern) {
			return policy
		}
	}
	if c.defaultPolicy != "" {
		return c.defaultPolicy
	}
	return "allow"
}
