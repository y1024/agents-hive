package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunOutputsCostLatencySummary(t *testing.T) {
	var out bytes.Buffer
	if err := run(nil, &out); err != nil {
		t.Fatalf("run delegation eval: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Delegation evaluation summary", "cases:", "direct_cost_total:", "selected_cost_total:", "direct_latency_ms_total:", "selected_latency_ms_total:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRunLoadsDelegationCasesFromInputFile(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "delegation-cases.json")
	if err := os.WriteFile(input, []byte(`{
		"cases": [
			{"name":"cheap-delegate","request":{"task_type":"review","max_depth":2,"direct_cost":10,"delegated_cost":3,"direct_latency_ms":1000,"delegated_latency_ms":500}},
			{"name":"chat-direct","request":{"task_type":"chat","max_depth":2,"direct_cost":1,"delegated_cost":2,"direct_latency_ms":100,"delegated_latency_ms":200}}
		]
	}`), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	var out bytes.Buffer
	if err := run([]string{"-input", input}, &out); err != nil {
		t.Fatalf("run delegation eval: %v", err)
	}
	got := out.String()
	for _, want := range []string{"cases: 2", "direct: 1", "delegated: 1", "direct_cost_total: 11.00", "selected_cost_total: 4.00"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}
