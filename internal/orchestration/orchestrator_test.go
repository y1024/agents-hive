package orchestration

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestOrchestratorSequentialStopsAfterFailureWithPartialResults(t *testing.T) {
	orch := NewOrchestrator()
	result := orch.Run(context.Background(), Plan{
		Mode: Sequential,
		Tasks: []Task{
			{ID: "a", Run: okTask("A")},
			{ID: "b", Run: failTask("boom")},
			{ID: "c", Run: okTask("C")},
		},
	})

	if result.Status != StatusPartial {
		t.Fatalf("status = %s, want partial", result.Status)
	}
	if len(result.Results) != 2 {
		t.Fatalf("result count = %d, want 2", len(result.Results))
	}
	if result.Results[1].Error == "" {
		t.Fatal("failed task should include error text")
	}
}

func TestOrchestratorParallelIsolatesContextAndReturnsPartialSuccess(t *testing.T) {
	orch := NewOrchestrator()
	result := orch.Run(context.Background(), Plan{
		Mode: Parallel,
		BaseContext: SharedContext{
			Values: map[string]string{"shared": "base"},
		},
		Tasks: []Task{
			{ID: "a", Run: func(_ context.Context, c TaskContext) (TaskOutput, error) {
				c.Values["shared"] = "mutated"
				return TaskOutput{Value: c.Values["shared"]}, nil
			}},
			{ID: "b", Run: func(_ context.Context, c TaskContext) (TaskOutput, error) {
				return TaskOutput{Value: c.Values["shared"]}, nil
			}},
			{ID: "c", Run: failTask("boom")},
		},
	})

	if result.Status != StatusPartial {
		t.Fatalf("status = %s, want partial", result.Status)
	}
	if result.Results[1].Output.Value != "base" {
		t.Fatalf("context leaked across tasks: task b saw %q", result.Results[1].Output.Value)
	}
}

func TestOrchestratorFanoutFaninAggregatesSuccessfulOutputs(t *testing.T) {
	orch := NewOrchestrator()
	result := orch.Run(context.Background(), Plan{
		Mode: FanoutFanin,
		Tasks: []Task{
			{ID: "a", Run: okTask("A")},
			{ID: "b", Run: failTask("boom")},
			{ID: "c", Run: okTask("C")},
		},
		Fanin: func(results []TaskResult) (TaskOutput, error) {
			var merged string
			for _, r := range results {
				if r.Error == "" {
					merged += r.Output.Value
				}
			}
			return TaskOutput{Value: merged}, nil
		},
	})

	if result.Status != StatusPartial {
		t.Fatalf("status = %s, want partial", result.Status)
	}
	if result.Fanin == nil || result.Fanin.Value != "AC" {
		t.Fatalf("fan-in output = %#v, want AC", result.Fanin)
	}
}

func TestBuildAgentTreeFromTraceEdges(t *testing.T) {
	tree := BuildAgentTree([]TraceEdge{
		{ParentTraceID: "root", ChildTraceID: "child-a", AgentID: "a", AgentType: "worker"},
		{ParentTraceID: "root", ChildTraceID: "child-b", AgentID: "b", AgentType: "worker"},
		{ParentTraceID: "child-a", ChildTraceID: "leaf", AgentID: "leaf", AgentType: "reviewer"},
	})

	if len(tree) != 1 {
		t.Fatalf("root count = %d, want 1", len(tree))
	}
	if tree[0].TraceID != "root" {
		t.Fatalf("root trace = %q, want root", tree[0].TraceID)
	}
	gotChildren := []string{tree[0].Children[0].TraceID, tree[0].Children[1].TraceID}
	wantChildren := []string{"child-a", "child-b"}
	if !reflect.DeepEqual(gotChildren, wantChildren) {
		t.Fatalf("children = %#v, want %#v", gotChildren, wantChildren)
	}
	if tree[0].Children[0].Children[0].TraceID != "leaf" {
		t.Fatalf("leaf trace = %q, want leaf", tree[0].Children[0].Children[0].TraceID)
	}
}

func okTask(value string) TaskFunc {
	return func(context.Context, TaskContext) (TaskOutput, error) {
		return TaskOutput{Value: value}, nil
	}
}

func failTask(message string) TaskFunc {
	return func(context.Context, TaskContext) (TaskOutput, error) {
		return TaskOutput{}, errors.New(message)
	}
}
