package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
	"github.com/chef-guo/agents-hive/internal/qualityworkbench"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("quality-batch-eval", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	casesDir := fs.String("cases-dir", "", "golden cases directory")
	format := fs.String("format", "markdown", "output format: markdown or json")
	batchID := fs.String("batch-id", "local-batch", "batch id")
	if err := fs.Parse(args); err != nil {
		return err
	}

	store := qualityworkbench.NewMemoryBatchEvalRunStore(func() time.Time { return time.Now().UTC() })
	run, err := store.Start(qualityworkbench.BatchEvalStart{
		BatchID:    *batchID,
		Kind:       qualityworkbench.BatchEvalKindManual,
		CasesDir:   *casesDir,
		EvalRunner: agentquality.StaticEvalRunner{},
	})
	if err != nil {
		return err
	}

	switch *format {
	case "json":
		payload := map[string]any{
			"id":      run.ID,
			"batch":   run.BatchID,
			"status":  run.Status,
			"total":   run.Summary.Total,
			"passed":  run.Summary.Passed,
			"failed":  run.Summary.Failed,
			"unknown": run.Summary.Unknown,
			"reasons": run.Summary.Reasons,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	case "markdown":
		return writeMarkdownSummary(stdout, run)
	default:
		return fmt.Errorf("unsupported format %q", *format)
	}
}

func writeMarkdownSummary(w io.Writer, run qualityworkbench.BatchEvalRun) error {
	_, err := fmt.Fprintf(w, "# Quality Batch Eval\n\n")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "- Batch: %s\n- Status: %s\n- Total: %d\n- Passed: %d\n- Failed: %d\n- Unknown: %d\n\n",
		run.BatchID,
		run.Status,
		run.Summary.Total,
		run.Summary.Passed,
		run.Summary.Failed,
		run.Summary.Unknown,
	)
	if err != nil {
		return err
	}
	if len(run.Summary.Reasons) == 0 {
		_, err = io.WriteString(w, "## Reasons\n\n- none\n")
		return err
	}
	_, err = io.WriteString(w, "## Reasons\n\n")
	if err != nil {
		return err
	}
	for _, reason := range run.Summary.Reasons {
		if _, err := fmt.Fprintf(w, "- %s\n", reason); err != nil {
			return err
		}
	}
	return nil
}
