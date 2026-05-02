package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

func TestRunEmptyInputGeneratesMarkdownSummary(t *testing.T) {
	var stdout bytes.Buffer

	err := run([]string{"-format", "markdown"}, &stdout)

	require.NoError(t, err)
	out := stdout.String()
	assert.Contains(t, out, "# Quality Batch Eval")
	assert.Contains(t, out, "Total: 0")
	assert.Contains(t, out, "no candidates")
}

func TestRunCasesDirGeneratesJSONSummary(t *testing.T) {
	dir := t.TempDir()
	writeCase(t, dir, agentquality.Case{
		ID:             "case-1",
		Name:           "case 1",
		Route:          "web",
		Input:          "hello",
		ExpectedStatus: agentquality.StatusPass,
		Required:       true,
	})
	var stdout bytes.Buffer

	err := run([]string{"-cases-dir", dir, "-format", "json"}, &stdout)

	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &payload))
	assert.Equal(t, float64(1), payload["total"])
	assert.Equal(t, "succeeded", payload["status"])
}

func writeCase(t *testing.T, dir string, c agentquality.Case) {
	t.Helper()
	b, err := json.Marshal(c)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, c.ID+".json"), b, 0o600))
}
