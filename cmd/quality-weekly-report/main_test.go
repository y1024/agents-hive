package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunEmptyDataOutputsSixMarkdownSections(t *testing.T) {
	var stdout bytes.Buffer

	err := run([]string{"-fixture", "empty"}, &stdout)

	require.NoError(t, err)
	out := stdout.String()
	assert.Contains(t, out, "# Quality Workbench Weekly Report")
	assert.Contains(t, out, "## Summary")
	assert.Contains(t, out, "## Open Clusters")
	assert.Contains(t, out, "## Candidate Changes")
	assert.Contains(t, out, "## Eval Runs")
	assert.Contains(t, out, "## Regressions")
	assert.Contains(t, out, "## Next Actions")
}

func TestRunLoadsReportInputFile(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "report-input.json")
	require.NoError(t, os.WriteFile(input, []byte(`{
		"since": "2026-04-20",
		"until": "2026-04-27",
		"clusters": [
			{"id":"cl_tool","size":3,"open_count":2,"failure_type":"tool","last_seen":"2026-04-25T00:00:00Z"}
		],
		"candidates": [
			{"id":"cand-1","status":"promoted_regressed","failure_type":"tool","verify_result":"failed"}
		],
		"eval_runs": [
			{"id":"eval-1","batch_id":"batch-1","status":"failed","summary":{"passed":1,"failed":1,"unknown":0}}
		]
	}`), 0o644))

	var stdout bytes.Buffer
	err := run([]string{"-input", input}, &stdout)

	require.NoError(t, err)
	out := stdout.String()
	assert.Contains(t, out, "Window: 2026-04-20 to 2026-04-27")
	assert.Contains(t, out, "- Open clusters: 1")
	assert.Contains(t, out, "- Candidates: 1")
	assert.Contains(t, out, "- Failed eval runs: 1")
	assert.Contains(t, out, "- Regressed records: 1")
	assert.Contains(t, out, "- cl_tool: 2 open, 3 total, failure=tool")
	assert.Contains(t, out, "- cand-1: status=promoted_regressed")
}

func TestRunWeekFlagSetsSevenDayWindow(t *testing.T) {
	var stdout bytes.Buffer

	err := run([]string{"--week", "2026-04-20"}, &stdout)

	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "Window: 2026-04-20 to 2026-04-27")
}

func TestRunWeekFlagOverridesInputWindow(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "report-input.json")
	require.NoError(t, os.WriteFile(input, []byte(`{
		"since": "2026-04-01",
		"until": "2026-04-08"
	}`), 0o644))
	var stdout bytes.Buffer

	err := run([]string{"-input", input, "--week", "2026-04-20"}, &stdout)

	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "Window: 2026-04-20 to 2026-04-27")
}
