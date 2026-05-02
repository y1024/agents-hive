package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/chef-guo/agents-hive/internal/evaluation"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type delegationEvalInputFile struct {
	Cases []evaluation.EvaluationCase `json:"cases"`
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("delegation-eval", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	inputPath := fs.String("input", "", "json delegation eval input file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cases := defaultDelegationCases()
	if *inputPath != "" {
		loaded, err := loadDelegationCases(*inputPath)
		if err != nil {
			return err
		}
		cases = loaded
	}
	summary := evaluation.SummarizeDelegationComparison(cases)

	_, err := fmt.Fprintf(out, `Delegation evaluation summary
cases: %d
direct: %d
delegated: %d
direct_cost_total: %.2f
selected_cost_total: %.2f
direct_latency_ms_total: %d
selected_latency_ms_total: %d
`, summary.Cases, summary.Direct, summary.Delegated, summary.DirectCostTotal, summary.SelectedCostTotal, summary.DirectLatencyMSTotal, summary.SelectedLatencyMSTotal)
	return err
}

func defaultDelegationCases() []evaluation.EvaluationCase {
	return []evaluation.EvaluationCase{
		{
			Name: "review",
			Request: evaluation.DelegationRequest{
				TaskType:           evaluation.TaskReview,
				MaxDepth:           2,
				DirectCost:         10,
				DelegatedCost:      4,
				DirectLatencyMS:    1200,
				DelegatedLatencyMS: 800,
			},
		},
		{
			Name: "implementation",
			Request: evaluation.DelegationRequest{
				TaskType:           evaluation.TaskImplementation,
				MaxDepth:           2,
				DirectCost:         18,
				DelegatedCost:      11,
				DirectLatencyMS:    2400,
				DelegatedLatencyMS: 1700,
			},
		},
		{
			Name: "chat",
			Request: evaluation.DelegationRequest{
				TaskType:           evaluation.TaskChat,
				MaxDepth:           2,
				DirectCost:         1,
				DelegatedCost:      2,
				DirectLatencyMS:    120,
				DelegatedLatencyMS: 240,
			},
		},
	}
}

func loadDelegationCases(path string) ([]evaluation.EvaluationCase, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var input delegationEvalInputFile
	if err := json.Unmarshal(b, &input); err != nil {
		return nil, fmt.Errorf("invalid -input json: %w", err)
	}
	if len(input.Cases) == 0 {
		return nil, fmt.Errorf("input cases are required")
	}
	return input.Cases, nil
}
