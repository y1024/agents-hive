package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

// BenchmarkRunResult 是 benchmark 运行的输出结果。
type BenchmarkRunResult struct {
	SuiteName  string                `json:"suite_name"`
	SuiteType  string                `json:"suite_type"`
	Version    string                `json:"version"`
	Source     string                `json:"source"`
	License    string                `json:"license"`
	RunAt      time.Time             `json:"run_at"`
	TotalItems int                   `json:"total_items"`
	Converted  int                   `json:"converted"`
	Errors     []string              `json:"errors,omitempty"`
	Cases      []agentquality.Case   `json:"cases"`
	Rollout    bool                  `json:"authorize_rollout"`
	Note       string                `json:"note"`
}

func main() {
	runCmd := flag.NewFlagSet("run", flag.ExitOnError)
	suitePath := runCmd.String("suite", "", "Path to benchmark suite file (JSON or YAML)")
	outputPath := runCmd.String("output", "", "Path to write results (JSON)")
	route := runCmd.String("route", "chat", "Route to assign to converted cases")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		if err := runCmd.Parse(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if *suitePath == "" {
			fmt.Fprintf(os.Stderr, "Error: --suite is required\n")
			runCmd.Usage()
			os.Exit(1)
		}
		if err := runBenchmark(*suitePath, *outputPath, *route); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`agentquality-benchmark - Run external benchmark suites

Usage:
  agentquality-benchmark run --suite <path> [--output <path>] [--route <route>]

Commands:
  run     Load and convert a benchmark suite to internal cases

Flags:
  --suite   Path to benchmark suite file (JSON or YAML) [required]
  --output  Path to write results JSON (default: stdout)
  --route   Route to assign to converted cases (default: "chat")

Note:
  External benchmarks are for external credibility and horizontal comparison.
  They do NOT trigger domain rollout by default.`)
}

func runBenchmark(suitePath, outputPath, route string) error {
	suite, err := agentquality.LoadBenchmarkSuite(suitePath)
	if err != nil {
		return fmt.Errorf("load suite: %w", err)
	}

	adapter := agentquality.GenericBenchmarkAdapter{
		Route:      route,
		SuiteName:  suite.Name,
		SuiteType:  "external_benchmark",
		DefaultTag: "external_benchmark",
	}

	result := BenchmarkRunResult{
		SuiteName:  suite.Name,
		SuiteType:  "external_benchmark",
		Version:    suite.Version,
		Source:     suite.Source,
		License:    suite.License,
		RunAt:      time.Now().UTC(),
		TotalItems: len(suite.Items),
		Cases:      make([]agentquality.Case, 0, len(suite.Items)),
		Rollout:    false,
		Note:       "External benchmark results do NOT authorize domain rollout. For external credibility only.",
	}

	for _, item := range suite.Items {
		// 继承 suite 级别的 source/version/license
		if item.Source == "" {
			item.Source = suite.Source
		}
		if item.Version == "" {
			item.Version = suite.Version
		}
		if item.License == "" {
			item.License = suite.License
		}

		c, err := adapter.ConvertToCase(item)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("item %s: %v", item.ID, err))
			continue
		}
		result.Cases = append(result.Cases, c)
		result.Converted++
	}

	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	if outputPath == "" {
		fmt.Println(string(b))
		return nil
	}

	if err := os.WriteFile(outputPath, b, 0644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Benchmark run complete: %d/%d items converted, suite_type=external_benchmark, authorize_rollout=false\n",
		result.Converted, result.TotalItems)
	return nil
}
