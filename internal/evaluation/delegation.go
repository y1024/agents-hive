package evaluation

type TaskType string

const (
	TaskChat           TaskType = "chat"
	TaskReview         TaskType = "review"
	TaskImplementation TaskType = "implementation"
	TaskResearch       TaskType = "research"
)

type DecisionAction string

const (
	ActionDirect   DecisionAction = "direct"
	ActionDelegate DecisionAction = "delegate"
)

type DelegationRequest struct {
	TaskType           TaskType `json:"task_type"`
	Depth              int      `json:"depth"`
	MaxDepth           int      `json:"max_depth"`
	DirectCost         float64  `json:"direct_cost"`
	DelegatedCost      float64  `json:"delegated_cost"`
	DirectLatencyMS    int      `json:"direct_latency_ms"`
	DelegatedLatencyMS int      `json:"delegated_latency_ms"`
}

type DelegationDecision struct {
	Action DecisionAction
	Reason string
}

type EvaluationCase struct {
	Name    string            `json:"name"`
	Request DelegationRequest `json:"request"`
}

type EvaluationSummary struct {
	Cases                  int
	Direct                 int
	Delegated              int
	DirectCostTotal        float64
	SelectedCostTotal      float64
	DirectLatencyMSTotal   int
	SelectedLatencyMSTotal int
}

func DecideDelegation(req DelegationRequest) DelegationDecision {
	if req.MaxDepth > 0 && req.Depth >= req.MaxDepth {
		return DelegationDecision{Action: ActionDirect, Reason: "depth limit reached"}
	}
	if req.TaskType == TaskChat {
		return DelegationDecision{Action: ActionDirect, Reason: "chat tasks are cheaper direct"}
	}
	if req.DelegatedCost <= 0 {
		return DelegationDecision{Action: ActionDirect, Reason: "delegated cost unknown"}
	}
	if req.DelegatedCost > req.DirectCost {
		return DelegationDecision{Action: ActionDirect, Reason: "delegation cost is higher"}
	}
	if req.DelegatedLatencyMS > 0 && req.DirectLatencyMS > 0 && req.DelegatedLatencyMS > req.DirectLatencyMS*2 {
		return DelegationDecision{Action: ActionDirect, Reason: "delegation latency penalty too high"}
	}
	return DelegationDecision{Action: ActionDelegate, Reason: "specialized delegation is cheaper within limits"}
}

func SummarizeDelegationComparison(cases []EvaluationCase) EvaluationSummary {
	var summary EvaluationSummary
	for _, c := range cases {
		decision := DecideDelegation(c.Request)
		summary.Cases++
		summary.DirectCostTotal += c.Request.DirectCost
		summary.DirectLatencyMSTotal += c.Request.DirectLatencyMS
		if decision.Action == ActionDelegate {
			summary.Delegated++
			summary.SelectedCostTotal += c.Request.DelegatedCost
			summary.SelectedLatencyMSTotal += c.Request.DelegatedLatencyMS
		} else {
			summary.Direct++
			summary.SelectedCostTotal += c.Request.DirectCost
			summary.SelectedLatencyMSTotal += c.Request.DirectLatencyMS
		}
	}
	return summary
}
