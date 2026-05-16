package agentquality

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type LoadedCase struct {
	Path string
	Case Case
}

func LoadCases(dir string) ([]LoadedCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []LoadedCase
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == "sample_gate_summary.json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var c Case
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, LoadedCase{Path: path, Case: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// LoadCasesByDomain 从目录加载 case 并按 domain_id 过滤。
// 如果 domainID 为空，返回所有 case。
func LoadCasesByDomain(dir string, domainID string) ([]LoadedCase, error) {
	all, err := LoadCases(dir)
	if err != nil {
		return nil, err
	}
	if domainID == "" {
		return all, nil
	}
	var filtered []LoadedCase
	for _, lc := range all {
		if lc.Case.DomainID == domainID {
			filtered = append(filtered, lc)
		}
	}
	return filtered, nil
}

// LoadCasesBySuiteID 从目录加载 case 并按 suite_id (存储在 Tags 中) 过滤。
func LoadCasesBySuiteID(dir string, suiteID string) ([]LoadedCase, error) {
	all, err := LoadCases(dir)
	if err != nil {
		return nil, err
	}
	if suiteID == "" {
		return all, nil
	}
	var filtered []LoadedCase
	for _, lc := range all {
		for _, tag := range lc.Case.Tags {
			if tag == "suite:"+suiteID {
				filtered = append(filtered, lc)
				break
			}
		}
	}
	return filtered, nil
}

func ValidateCase(c Case) error {
	if c.ID == "" {
		return fmt.Errorf("id missing")
	}
	if c.Name == "" {
		return fmt.Errorf("%s: name missing", c.ID)
	}
	if c.Route == "" {
		return fmt.Errorf("%s: route missing", c.ID)
	}
	if c.Input == "" {
		return fmt.Errorf("%s: input missing", c.ID)
	}
	switch c.ExpectedStatus {
	case StatusPass, StatusFail, StatusBlocked, StatusNeedsUser:
	default:
		return fmt.Errorf("%s: invalid expected_status %q", c.ID, c.ExpectedStatus)
	}
	if len(c.ExpectedTools) > 0 && len(c.AllowedTools) > 0 {
		return fmt.Errorf("%s: expected_tools and allowed_tools are mutually exclusive", c.ID)
	}
	if c.Risk != "" {
		switch c.Risk {
		case "safe", "dangerous":
		default:
			return fmt.Errorf("%s: invalid risk %q", c.ID, c.Risk)
		}
	}
	if c.Risk == "safe" && c.ExpectedStatus == StatusNeedsUser {
		return fmt.Errorf("%s: safe case must not require user approval", c.ID)
	}
	return nil
}
