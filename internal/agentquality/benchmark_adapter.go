package agentquality

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// BenchmarkItem 是外部标准 benchmark 的单条测试项。
type BenchmarkItem struct {
	ID             string            `json:"id" yaml:"id"`
	Name           string            `json:"name" yaml:"name"`
	Input          string            `json:"input" yaml:"input"`
	ExpectedOutput string            `json:"expected_output,omitempty" yaml:"expected_output,omitempty"`
	Category       string            `json:"category,omitempty" yaml:"category,omitempty"`
	Source         string            `json:"source,omitempty" yaml:"source,omitempty"`
	Version        string            `json:"version,omitempty" yaml:"version,omitempty"`
	License        string            `json:"license,omitempty" yaml:"license,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// BenchmarkSuite 是外部标准 benchmark 的完整测试集。
type BenchmarkSuite struct {
	Name    string          `json:"name" yaml:"name"`
	Version string          `json:"version" yaml:"version"`
	Source  string          `json:"source" yaml:"source"`
	License string          `json:"license" yaml:"license"`
	Items   []BenchmarkItem `json:"items" yaml:"items"`
}

// BenchmarkAdapter 将外部 benchmark 转换为内部 Case 格式。
type BenchmarkAdapter interface {
	ConvertToCase(item BenchmarkItem) (Case, error)
}

// GenericBenchmarkAdapter 是通用的 benchmark 适配器。
// 将外部 benchmark item 转换为内部 Case，保留 source/version/license 元信息。
type GenericBenchmarkAdapter struct {
	Route      string
	SuiteName  string
	SuiteType  string // "external_benchmark"
	DefaultTag string // 默认 tag，例如 "suite:external_benchmark"
}

func (a GenericBenchmarkAdapter) ConvertToCase(item BenchmarkItem) (Case, error) {
	if item.ID == "" {
		return Case{}, fmt.Errorf("benchmark item missing id")
	}
	if item.Input == "" {
		return Case{}, fmt.Errorf("benchmark item %s missing input", item.ID)
	}

	route := a.Route
	if route == "" {
		route = "chat"
	}

	tags := []string{}
	if a.DefaultTag != "" {
		tags = append(tags, a.DefaultTag)
	}
	if a.SuiteType != "" {
		tags = append(tags, "suite_type:"+a.SuiteType)
	}
	if a.SuiteName != "" {
		tags = append(tags, "suite:"+a.SuiteName)
	}
	if item.Category != "" {
		tags = append(tags, "category:"+item.Category)
	}

	name := item.Name
	if name == "" {
		name = "External benchmark " + item.ID
	}

	notes := buildBenchmarkNotes(item, a.SuiteName)

	c := Case{
		ID:               item.ID,
		Name:             name,
		Route:            route,
		Input:            item.Input,
		ExpectedAnswer:   item.ExpectedOutput,
		ExpectedStatus:   StatusPass,
		Required:         false, // 外部 benchmark 不作为 required gate
		Tags:             tags,
		Notes:            notes,
		SourceKind:       "external_benchmark",
		SourceName:       item.Source,
		CreatedFrom:      "benchmark_adapter",
		State:            "external",
		EvidenceLevelMin: string(EvidenceRealRunner),
	}

	return c, nil
}

func buildBenchmarkNotes(item BenchmarkItem, suiteName string) string {
	var parts []string
	parts = append(parts, "外部标准 benchmark，用于横向对比和外部公信力，不作为域上线的主要 gate。")
	if suiteName != "" {
		parts = append(parts, fmt.Sprintf("Suite: %s", suiteName))
	}
	if item.Source != "" {
		parts = append(parts, fmt.Sprintf("Source: %s", item.Source))
	}
	if item.Version != "" {
		parts = append(parts, fmt.Sprintf("Version: %s", item.Version))
	}
	if item.License != "" {
		parts = append(parts, fmt.Sprintf("License: %s", item.License))
	}
	return strings.Join(parts, " | ")
}

// LoadBenchmarkSuite 从 JSON 或 YAML 文件加载 benchmark suite。
func LoadBenchmarkSuite(path string) (BenchmarkSuite, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return BenchmarkSuite{}, err
	}

	var suite BenchmarkSuite

	// 尝试 JSON
	if err := json.Unmarshal(b, &suite); err == nil {
		return suite, validateBenchmarkSuite(suite)
	}

	// 尝试 YAML
	if err := yaml.Unmarshal(b, &suite); err != nil {
		return BenchmarkSuite{}, fmt.Errorf("failed to parse as JSON or YAML: %w", err)
	}

	return suite, validateBenchmarkSuite(suite)
}

func validateBenchmarkSuite(suite BenchmarkSuite) error {
	if suite.Name == "" {
		return fmt.Errorf("benchmark suite missing name")
	}
	if len(suite.Items) == 0 {
		return fmt.Errorf("benchmark suite %s has no items", suite.Name)
	}
	seen := make(map[string]bool, len(suite.Items))
	for i, item := range suite.Items {
		if item.ID == "" {
			return fmt.Errorf("benchmark suite %s item[%d] missing id", suite.Name, i)
		}
		if seen[item.ID] {
			return fmt.Errorf("benchmark suite %s has duplicate item id %s", suite.Name, item.ID)
		}
		seen[item.ID] = true
		if item.Input == "" {
			return fmt.Errorf("benchmark suite %s item %s missing input", suite.Name, item.ID)
		}
	}
	return nil
}
