package security

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRedactJSONRemovesNestedSecrets(t *testing.T) {
	raw := []byte(`{
		"api_key":"key-123",
		"nested":{"Authorization":"Bearer abc","safe":"keep"},
		"items":[{"client-secret":"secret-1"},{"message":"token=abc123"}],
		"json_string":"{\"password\":\"pw\",\"name\":\"bob\"}"
	}`)

	got, err := RedactJSON(raw)
	if err != nil {
		t.Fatalf("RedactJSON() error = %v", err)
	}

	text := string(got)
	for _, leaked := range []string{"key-123", "Bearer abc", "secret-1", "token=abc123", `"pw"`} {
		if strings.Contains(text, leaked) {
			t.Fatalf("redacted JSON leaked %q: %s", leaked, text)
		}
	}
	for _, want := range []string{RedactedValue, `"safe":"keep"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("redacted JSON missing %q: %s", want, text)
		}
	}

	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("Unmarshal(redacted JSON) error = %v", err)
	}
	jsonString, ok := decoded["json_string"].(string)
	if !ok {
		t.Fatalf("json_string type = %T, want string", decoded["json_string"])
	}
	if !strings.Contains(jsonString, `"name":"bob"`) {
		t.Fatalf("json_string lost safe value: %q", jsonString)
	}
}

func TestRedactSecretsKeepsInvalidJSONStringButRedactsInlineSecret(t *testing.T) {
	got, err := RedactSecrets("request failed: access_token=abc123")
	if err != nil {
		t.Fatalf("RedactSecrets() error = %v", err)
	}
	text, ok := got.(string)
	if !ok {
		t.Fatalf("RedactSecrets() type = %T, want string", got)
	}
	if strings.Contains(text, "abc123") || !strings.Contains(text, RedactedValue) {
		t.Fatalf("inline secret was not redacted: %q", text)
	}
}

func TestRedactSecretsCoversRunQualityCredentialShapes(t *testing.T) {
	raw := map[string]any{
		"raw_credentials": map[string]any{"token": "raw-token-secret"},
		"credentials":     []any{map[string]any{"password": "raw-password-secret"}},
		"headers": map[string]any{
			"x-api-key": "raw-header-secret",
		},
		"message": "failed with client_secret=raw-inline-secret",
	}

	got, err := RedactSecrets(raw)
	if err != nil {
		t.Fatalf("RedactSecrets() error = %v", err)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal(redacted) error = %v", err)
	}
	text := string(b)
	for _, leaked := range []string{"raw-token-secret", "raw-password-secret", "raw-header-secret", "raw-inline-secret"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("redacted payload leaked %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, RedactedValue) {
		t.Fatalf("redacted payload missing marker: %s", text)
	}
}

func TestRedactJSONReturnsErrorForMalformedJSON(t *testing.T) {
	if _, err := RedactJSON(json.RawMessage(`{"api_key":`)); err == nil {
		t.Fatal("RedactJSON() error = nil, want malformed JSON error")
	}
}

func TestRunQualityFoundationGuardRejectsParallelRunPackage(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	script := filepath.Join(repoRoot, "scripts", "check_run_quality_foundation_guard.sh")

	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "internal", "run"), 0o755); err != nil {
		t.Fatalf("MkdirAll(internal/run) error = %v", err)
	}

	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(), "REPO_ROOT="+tmp)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("guard unexpectedly passed, output: %s", out)
	}
	if !strings.Contains(string(out), "internal/run") {
		t.Fatalf("guard output = %q, want internal/run rejection", out)
	}
}

func TestRunQualityFoundationGuardRejectsCheckboxParallelRunPlan(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	script := filepath.Join(repoRoot, "scripts", "check_run_quality_foundation_guard.sh")

	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "docs", "计划与路线"), 0o755); err != nil {
		t.Fatalf("MkdirAll(docs) error = %v", err)
	}
	planPath := filepath.Join(tmp, "docs", "计划与路线", "plan.md")
	if err := os.WriteFile(planPath, []byte("- [ ] Create: `internal/run`\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(plan) error = %v", err)
	}

	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(), "REPO_ROOT="+tmp)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("guard unexpectedly passed, output: %s", out)
	}
	if !strings.Contains(string(out), "internal/run") {
		t.Fatalf("guard output = %q, want internal/run plan rejection", out)
	}
}
