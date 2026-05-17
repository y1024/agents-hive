package agentquality

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBenchmarkAdapterConvertsItemsToCases(t *testing.T) {
	adapter := GenericBenchmarkAdapter{
		Route:      "chat",
		SuiteName:  "test-suite",
		SuiteType:  "external_benchmark",
		DefaultTag: "external",
	}

	item := BenchmarkItem{
		ID:             "bench_001",
		Name:           "Test benchmark item",
		Input:          "What is 2+2?",
		ExpectedOutput: "4",
		Category:       "math",
		Source:         "test-source",
		Version:        "1.0",
		License:        "MIT",
	}

	c, err := adapter.ConvertToCase(item)
	require.NoError(t, err)

	assert.Equal(t, "bench_001", c.ID)
	assert.Equal(t, "Test benchmark item", c.Name)
	assert.Equal(t, "chat", c.Route)
	assert.Equal(t, "What is 2+2?", c.Input)
	assert.Equal(t, "4", c.ExpectedAnswer)
	assert.Equal(t, StatusPass, c.ExpectedStatus)
	assert.False(t, c.Required, "external benchmarks should not be required")
	assert.Equal(t, "external_benchmark", c.SourceKind)
	assert.Equal(t, "test-source", c.SourceName)
	assert.Equal(t, "benchmark_adapter", c.CreatedFrom)
	assert.Equal(t, "external", c.State)

	// 验证 tags
	assert.Contains(t, c.Tags, "external")
	assert.Contains(t, c.Tags, "suite_type:external_benchmark")
	assert.Contains(t, c.Tags, "suite:test-suite")
	assert.Contains(t, c.Tags, "category:math")

	// 验证 notes 包含元信息
	assert.Contains(t, c.Notes, "外部标准 benchmark")
	assert.Contains(t, c.Notes, "Source: test-source")
	assert.Contains(t, c.Notes, "Version: 1.0")
	assert.Contains(t, c.Notes, "License: MIT")
}

func TestBenchmarkAdapterValidation(t *testing.T) {
	adapter := GenericBenchmarkAdapter{Route: "chat"}

	t.Run("missing id", func(t *testing.T) {
		item := BenchmarkItem{Input: "test"}
		_, err := adapter.ConvertToCase(item)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing id")
	})

	t.Run("missing input", func(t *testing.T) {
		item := BenchmarkItem{ID: "test_001"}
		_, err := adapter.ConvertToCase(item)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing input")
	})
}

func TestBenchmarkRunDoesNotAuthorizeRollout(t *testing.T) {
	adapter := GenericBenchmarkAdapter{
		Route:      "chat",
		SuiteName:  "external-suite",
		SuiteType:  "external_benchmark",
		DefaultTag: "external",
	}

	item := BenchmarkItem{
		ID:             "ext_001",
		Name:           "External test",
		Input:          "test input",
		ExpectedOutput: "test output",
		Source:         "external-source",
	}

	c, err := adapter.ConvertToCase(item)
	require.NoError(t, err)

	// 验证外部 benchmark 不会触发上线
	assert.False(t, c.Required, "external benchmark must not be required")
	assert.Equal(t, "external_benchmark", c.SourceKind)
	assert.Contains(t, c.Tags, "suite_type:external_benchmark")
	assert.Contains(t, c.Notes, "不作为域上线的主要 gate")
}

func TestBenchmarkSuiteRecordsSourceAndVersion(t *testing.T) {
	adapter := GenericBenchmarkAdapter{
		Route:     "chat",
		SuiteName: "versioned-suite",
		SuiteType: "external_benchmark",
	}

	item := BenchmarkItem{
		ID:      "ver_001",
		Name:    "Versioned item",
		Input:   "test",
		Source:  "benchmark-org/repo",
		Version: "v2.1.0",
		License: "Apache-2.0",
	}

	c, err := adapter.ConvertToCase(item)
	require.NoError(t, err)

	// 验证 source/version/license 被保留
	assert.Equal(t, "benchmark-org/repo", c.SourceName)
	assert.Contains(t, c.Notes, "Source: benchmark-org/repo")
	assert.Contains(t, c.Notes, "Version: v2.1.0")
	assert.Contains(t, c.Notes, "License: Apache-2.0")
}

func TestLoadBenchmarkSuiteJSON(t *testing.T) {
	tmpDir := t.TempDir()
	suitePath := filepath.Join(tmpDir, "test-suite.json")

	suiteJSON := `{
  "name": "test-benchmark",
  "version": "1.0",
  "source": "test-org",
  "license": "MIT",
  "items": [
    {
      "id": "item_001",
      "name": "Test item 1",
      "input": "What is AI?",
      "expected_output": "Artificial Intelligence",
      "category": "general"
    },
    {
      "id": "item_002",
      "name": "Test item 2",
      "input": "What is ML?",
      "expected_output": "Machine Learning",
      "category": "general"
    }
  ]
}`

	err := os.WriteFile(suitePath, []byte(suiteJSON), 0644)
	require.NoError(t, err)

	suite, err := LoadBenchmarkSuite(suitePath)
	require.NoError(t, err)

	assert.Equal(t, "test-benchmark", suite.Name)
	assert.Equal(t, "1.0", suite.Version)
	assert.Equal(t, "test-org", suite.Source)
	assert.Equal(t, "MIT", suite.License)
	assert.Len(t, suite.Items, 2)

	assert.Equal(t, "item_001", suite.Items[0].ID)
	assert.Equal(t, "Test item 1", suite.Items[0].Name)
	assert.Equal(t, "What is AI?", suite.Items[0].Input)
	assert.Equal(t, "Artificial Intelligence", suite.Items[0].ExpectedOutput)
}

func TestLoadBenchmarkSuiteYAML(t *testing.T) {
	tmpDir := t.TempDir()
	suitePath := filepath.Join(tmpDir, "test-suite.yaml")

	suiteYAML := `name: yaml-benchmark
version: "2.0"
source: yaml-org
license: Apache-2.0
items:
  - id: yaml_001
    name: YAML test 1
    input: "Test input 1"
    expected_output: "Test output 1"
    category: yaml-test
  - id: yaml_002
    name: YAML test 2
    input: "Test input 2"
    expected_output: "Test output 2"
`

	err := os.WriteFile(suitePath, []byte(suiteYAML), 0644)
	require.NoError(t, err)

	suite, err := LoadBenchmarkSuite(suitePath)
	require.NoError(t, err)

	assert.Equal(t, "yaml-benchmark", suite.Name)
	assert.Equal(t, "2.0", suite.Version)
	assert.Equal(t, "yaml-org", suite.Source)
	assert.Equal(t, "Apache-2.0", suite.License)
	assert.Len(t, suite.Items, 2)

	assert.Equal(t, "yaml_001", suite.Items[0].ID)
	assert.Equal(t, "YAML test 1", suite.Items[0].Name)
}

func TestLoadBenchmarkSuiteValidation(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("missing name", func(t *testing.T) {
		path := filepath.Join(tmpDir, "no-name.json")
		content := `{"version": "1.0", "items": [{"id": "test", "input": "test"}]}`
		err := os.WriteFile(path, []byte(content), 0644)
		require.NoError(t, err)

		_, err = LoadBenchmarkSuite(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing name")
	})

	t.Run("no items", func(t *testing.T) {
		path := filepath.Join(tmpDir, "no-items.json")
		content := `{"name": "test", "version": "1.0", "items": []}`
		err := os.WriteFile(path, []byte(content), 0644)
		require.NoError(t, err)

		_, err = LoadBenchmarkSuite(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "has no items")
	})

	t.Run("duplicate item id", func(t *testing.T) {
		path := filepath.Join(tmpDir, "dup-id.json")
		content := `{
  "name": "test",
  "items": [
    {"id": "dup", "input": "test1"},
    {"id": "dup", "input": "test2"}
  ]
}`
		err := os.WriteFile(path, []byte(content), 0644)
		require.NoError(t, err)

		_, err = LoadBenchmarkSuite(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate item id")
	})

	t.Run("item missing input", func(t *testing.T) {
		path := filepath.Join(tmpDir, "no-input.json")
		content := `{
  "name": "test",
  "items": [{"id": "test_001", "name": "Test"}]
}`
		err := os.WriteFile(path, []byte(content), 0644)
		require.NoError(t, err)

		_, err = LoadBenchmarkSuite(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing input")
	})
}
