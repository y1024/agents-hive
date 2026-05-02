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

type weeklyReportInputFile struct {
	Since      string                          `json:"since"`
	Until      string                          `json:"until"`
	Clusters   []qualityworkbench.Cluster      `json:"clusters"`
	Candidates []agentquality.CandidateRecord  `json:"candidates"`
	EvalRuns   []qualityworkbench.BatchEvalRun `json:"eval_runs"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("quality-weekly-report", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fixture := fs.String("fixture", "empty", "fixture data source: empty")
	inputPath := fs.String("input", "", "json report input file")
	weekText := fs.String("week", "", "report week start date, yyyy-mm-dd")
	sinceText := fs.String("since", "", "report window start date, yyyy-mm-dd")
	untilText := fs.String("until", "", "report window end date, yyyy-mm-dd")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = fixture

	until := time.Now().UTC()
	since := until.AddDate(0, 0, -7)
	var err error
	if *weekText != "" {
		since, err = time.Parse("2006-01-02", *weekText)
		if err != nil {
			return fmt.Errorf("invalid -week: %w", err)
		}
		until = since.AddDate(0, 0, 7)
	}
	if *sinceText != "" {
		since, err = time.Parse("2006-01-02", *sinceText)
		if err != nil {
			return fmt.Errorf("invalid -since: %w", err)
		}
	}
	if *untilText != "" {
		until, err = time.Parse("2006-01-02", *untilText)
		if err != nil {
			return fmt.Errorf("invalid -until: %w", err)
		}
	}

	input := qualityworkbench.WeeklyReportInput{Since: since, Until: until}
	if *inputPath != "" {
		loaded, err := loadWeeklyReportInput(*inputPath)
		if err != nil {
			return err
		}
		input = loaded
		if *weekText != "" {
			input.Since = since
			input.Until = until
		}
		if !since.IsZero() && *sinceText != "" {
			input.Since = since
		}
		if !until.IsZero() && *untilText != "" {
			input.Until = until
		}
	}

	report := qualityworkbench.GenerateWeeklyReport(input)
	_, err = io.WriteString(stdout, report.Markdown)
	return err
}

func loadWeeklyReportInput(path string) (qualityworkbench.WeeklyReportInput, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return qualityworkbench.WeeklyReportInput{}, err
	}
	var raw weeklyReportInputFile
	if err := json.Unmarshal(b, &raw); err != nil {
		return qualityworkbench.WeeklyReportInput{}, fmt.Errorf("invalid -input json: %w", err)
	}
	since, err := parseReportTime(raw.Since)
	if err != nil {
		return qualityworkbench.WeeklyReportInput{}, fmt.Errorf("invalid input since: %w", err)
	}
	until, err := parseReportTime(raw.Until)
	if err != nil {
		return qualityworkbench.WeeklyReportInput{}, fmt.Errorf("invalid input until: %w", err)
	}
	return qualityworkbench.WeeklyReportInput{
		Since:      since,
		Until:      until,
		Clusters:   raw.Clusters,
		Candidates: raw.Candidates,
		EvalRuns:   raw.EvalRuns,
	}, nil
}

func parseReportTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, raw)
}
