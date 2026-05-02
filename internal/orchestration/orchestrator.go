package orchestration

import (
	"context"
	"sort"
	"sync"
)

type Mode string

const (
	Sequential  Mode = "sequential"
	Parallel    Mode = "parallel"
	FanoutFanin Mode = "fanout_fanin"
)

type Status string

const (
	StatusSuccess Status = "success"
	StatusPartial Status = "partial"
	StatusFailed  Status = "failed"
)

type SharedContext struct {
	Values map[string]string
}

type TaskContext struct {
	Values map[string]string
}

type TaskOutput struct {
	Value string
	Meta  map[string]string
}

type TaskFunc func(context.Context, TaskContext) (TaskOutput, error)

type Task struct {
	ID  string
	Run TaskFunc
}

type FaninFunc func([]TaskResult) (TaskOutput, error)

type Plan struct {
	Mode        Mode
	BaseContext SharedContext
	Tasks       []Task
	Fanin       FaninFunc
}

type TaskResult struct {
	TaskID string
	Output TaskOutput
	Error  string
}

type Result struct {
	Status  Status
	Results []TaskResult
	Fanin   *TaskOutput
}

type Orchestrator struct {
	isolator ContextIsolator
}

type ContextIsolator struct{}

func NewOrchestrator() *Orchestrator {
	return &Orchestrator{isolator: ContextIsolator{}}
}

func (o *Orchestrator) Run(ctx context.Context, plan Plan) Result {
	switch plan.Mode {
	case Parallel, FanoutFanin:
		return o.runParallel(ctx, plan)
	default:
		return o.runSequential(ctx, plan)
	}
}

func (o *Orchestrator) runSequential(ctx context.Context, plan Plan) Result {
	results := make([]TaskResult, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		result := runTask(ctx, task, o.isolator.Isolate(plan.BaseContext))
		results = append(results, result)
		if result.Error != "" {
			break
		}
	}
	return finalizeResult(plan, results)
}

func (o *Orchestrator) runParallel(ctx context.Context, plan Plan) Result {
	results := make([]TaskResult, len(plan.Tasks))
	var wg sync.WaitGroup
	for i, task := range plan.Tasks {
		wg.Add(1)
		go func(index int, task Task) {
			defer wg.Done()
			results[index] = runTask(ctx, task, o.isolator.Isolate(plan.BaseContext))
		}(i, task)
	}
	wg.Wait()
	return finalizeResult(plan, results)
}

func runTask(ctx context.Context, task Task, taskCtx TaskContext) TaskResult {
	result := TaskResult{TaskID: task.ID}
	if task.Run == nil {
		result.Error = "task runner is nil"
		return result
	}
	output, err := task.Run(ctx, taskCtx)
	result.Output = output
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func finalizeResult(plan Plan, results []TaskResult) Result {
	out := Result{
		Status:  classify(results, len(plan.Tasks)),
		Results: results,
	}
	if plan.Mode == FanoutFanin && plan.Fanin != nil {
		fanin, err := plan.Fanin(results)
		out.Fanin = &fanin
		if err != nil && out.Status == StatusSuccess {
			out.Status = StatusPartial
		}
	}
	return out
}

func classify(results []TaskResult, planned int) Status {
	if planned == 0 {
		return StatusSuccess
	}
	failures := 0
	for _, result := range results {
		if result.Error != "" {
			failures++
		}
	}
	switch {
	case failures == 0 && len(results) == planned:
		return StatusSuccess
	case failures == len(results):
		return StatusFailed
	default:
		return StatusPartial
	}
}

func (ContextIsolator) Isolate(shared SharedContext) TaskContext {
	values := make(map[string]string, len(shared.Values))
	for k, v := range shared.Values {
		values[k] = v
	}
	return TaskContext{Values: values}
}

type TraceEdge struct {
	ParentTraceID string
	ChildTraceID  string
	AgentID       string
	AgentType     string
}

type AgentTreeNode struct {
	TraceID   string
	AgentID   string
	AgentType string
	Children  []*AgentTreeNode
}

func BuildAgentTree(edges []TraceEdge) []*AgentTreeNode {
	nodes := make(map[string]*AgentTreeNode)
	childSet := make(map[string]bool)

	nodeFor := func(traceID string) *AgentTreeNode {
		if nodes[traceID] == nil {
			nodes[traceID] = &AgentTreeNode{TraceID: traceID}
		}
		return nodes[traceID]
	}

	for _, edge := range edges {
		if edge.ParentTraceID == "" || edge.ChildTraceID == "" {
			continue
		}
		parent := nodeFor(edge.ParentTraceID)
		child := nodeFor(edge.ChildTraceID)
		child.AgentID = edge.AgentID
		child.AgentType = edge.AgentType
		parent.Children = append(parent.Children, child)
		childSet[edge.ChildTraceID] = true
	}

	roots := make([]*AgentTreeNode, 0)
	for traceID, node := range nodes {
		if !childSet[traceID] {
			sortTree(node)
			roots = append(roots, node)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].TraceID < roots[j].TraceID
	})
	return roots
}

func sortTree(node *AgentTreeNode) {
	sort.Slice(node.Children, func(i, j int) bool {
		return node.Children[i].TraceID < node.Children[j].TraceID
	})
	for _, child := range node.Children {
		sortTree(child)
	}
}
