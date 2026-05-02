package qualityworkbench

import (
	"context"
	"fmt"
	"strings"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

type ReplayCandidateStore interface {
	GetCandidate(ctx context.Context, id string) (*agentquality.CandidateRecord, bool, error)
	ListCandidates(ctx context.Context, filter agentquality.CandidateFilter) ([]agentquality.CandidateRecord, int, error)
}

type ReplayRunner struct {
	Store          ReplayCandidateStore
	EvalRunner     agentquality.EvalRunner
	GateThresholds agentquality.GateThresholds
}

func (r ReplayRunner) Run(ctx context.Context, job ReplayJob) (ReplayJobResult, error) {
	if r.Store == nil {
		return ReplayJobResult{}, fmt.Errorf("candidate store not configured")
	}
	cases, err := r.loadCases(ctx, job)
	if err != nil {
		return ReplayJobResult{}, err
	}
	if len(cases) == 0 {
		return ReplayJobResult{}, fmt.Errorf("replay job %s has no cases", job.ID)
	}
	runner := r.EvalRunner
	if runner == nil {
		gateInput := agentquality.GateInput{Cases: cases}
		result := replayResultFromGate(cases, gateInput.Results)
		thresholds := r.GateThresholds
		if thresholds == (agentquality.GateThresholds{}) {
			thresholds = agentquality.DefaultGateThresholds()
		}
		metrics := agentquality.ComputeGateMetrics(gateInput)
		if err := agentquality.EvaluateGate(metrics, thresholds); err != nil {
			result.Reasons = append(result.Reasons, "gate failed: "+err.Error())
			return result, fmt.Errorf("eval runner not configured: %w", err)
		}
		return result, fmt.Errorf("eval runner not configured")
	}
	gateInput, err := runner.Run(cases)
	if err != nil {
		return ReplayJobResult{}, err
	}
	gateInput.Cases = cases
	result := replayResultFromGate(cases, gateInput.Results)
	thresholds := r.GateThresholds
	if thresholds == (agentquality.GateThresholds{}) {
		thresholds = agentquality.DefaultGateThresholds()
	}
	metrics := agentquality.ComputeGateMetrics(gateInput)
	if err := agentquality.EvaluateGate(metrics, thresholds); err != nil {
		result.Reasons = append(result.Reasons, "gate failed: "+err.Error())
		return result, err
	}
	result.Reasons = append(result.Reasons, "gate passed")
	return result, nil
}

func (r ReplayRunner) loadCases(ctx context.Context, job ReplayJob) ([]agentquality.LoadedCase, error) {
	switch job.Kind {
	case ReplayJobKindCandidate:
		out := make([]agentquality.LoadedCase, 0, len(job.TargetIDs))
		for _, id := range job.TargetIDs {
			rec, ok, err := r.Store.GetCandidate(ctx, strings.TrimSpace(id))
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("candidate %s not found", id)
			}
			lc, err := loadedCaseFromCandidate(*rec)
			if err != nil {
				return nil, err
			}
			out = append(out, lc)
		}
		return out, nil
	case ReplayJobKindCluster:
		out := make([]agentquality.LoadedCase, 0)
		for _, clusterID := range job.TargetIDs {
			candidates, _, err := r.Store.ListCandidates(ctx, agentquality.CandidateFilter{Limit: 100})
			if err != nil {
				return nil, err
			}
			for _, rec := range candidates {
				if rec.ClusterID != strings.TrimSpace(clusterID) {
					continue
				}
				lc, err := loadedCaseFromCandidate(rec)
				if err != nil {
					return nil, err
				}
				out = append(out, lc)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported replay kind %q", job.Kind)
	}
}

func loadedCaseFromCandidate(rec agentquality.CandidateRecord) (agentquality.LoadedCase, error) {
	var c agentquality.Case
	if rec.GoldenCase != nil {
		c = *rec.GoldenCase
	}
	if c.ID == "" {
		c = agentquality.Case{
			ID:             rec.ID,
			Name:           firstNonEmpty(rec.Case.Name, rec.ID),
			Route:          firstNonEmpty(rec.Case.Route, rec.Route),
			Input:          firstNonEmpty(rec.Case.Input, rec.Input),
			ExpectedTools:  append([]string(nil), rec.Case.ExpectedTools...),
			AllowedTools:   append([]string(nil), rec.Case.AllowedTools...),
			ExpectedSkills: append([]string(nil), rec.Case.ExpectedSkills...),
			ExpectedAgents: append([]string(nil), rec.Case.ExpectedAgents...),
			Scenario:       rec.Case.Scenario,
			ExpectedStatus: firstStatus(rec.Case.ExpectedStatus, agentquality.StatusPass),
			FailureType:    rec.Case.FailureType,
			Risk:           firstNonEmpty(rec.Case.Risk, rec.Risk, "safe"),
			Required:       true,
			Notes:          rec.Case.Notes,
		}
	}
	if err := agentquality.ValidateCase(c); err != nil {
		return agentquality.LoadedCase{}, err
	}
	return agentquality.LoadedCase{Path: "candidate:" + rec.ID, Case: c}, nil
}

func replayResultFromGate(cases []agentquality.LoadedCase, results []agentquality.Result) ReplayJobResult {
	byID := make(map[string]agentquality.Result, len(results))
	for _, result := range results {
		byID[result.CaseID] = result
	}
	out := ReplayJobResult{Total: len(cases)}
	for _, lc := range cases {
		out.CaseIDs = append(out.CaseIDs, lc.Case.ID)
		result, ok := byID[lc.Case.ID]
		if !ok {
			out.Unknown++
			out.Reasons = append(out.Reasons, lc.Case.ID+" has no result")
			continue
		}
		if result.Passed {
			out.Passed++
			continue
		}
		out.Failed++
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = "failed"
		}
		out.Reasons = append(out.Reasons, lc.Case.ID+": "+reason)
	}
	return out
}

func firstStatus(values ...agentquality.FinalStatus) agentquality.FinalStatus {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
