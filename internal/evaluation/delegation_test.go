package evaluation

import "testing"

func TestDecideDelegationUsesTaskTypeDepthAndCost(t *testing.T) {
	tests := []struct {
		name string
		req  DelegationRequest
		want DecisionAction
	}{
		{
			name: "simple chat stays direct",
			req:  DelegationRequest{TaskType: TaskChat, Depth: 0, DirectCost: 1, DelegatedCost: 2, DirectLatencyMS: 100, DelegatedLatencyMS: 150},
			want: ActionDirect,
		},
		{
			name: "review delegates when cheaper and depth available",
			req:  DelegationRequest{TaskType: TaskReview, Depth: 0, MaxDepth: 2, DirectCost: 10, DelegatedCost: 3, DirectLatencyMS: 1000, DelegatedLatencyMS: 600},
			want: ActionDelegate,
		},
		{
			name: "depth limit forces direct",
			req:  DelegationRequest{TaskType: TaskReview, Depth: 2, MaxDepth: 2, DirectCost: 10, DelegatedCost: 3, DirectLatencyMS: 1000, DelegatedLatencyMS: 600},
			want: ActionDirect,
		},
		{
			name: "expensive delegation stays direct",
			req:  DelegationRequest{TaskType: TaskImplementation, Depth: 0, MaxDepth: 2, DirectCost: 3, DelegatedCost: 9, DirectLatencyMS: 100, DelegatedLatencyMS: 80},
			want: ActionDirect,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecideDelegation(tt.req)
			if got.Action != tt.want {
				t.Fatalf("action = %s, want %s; reason=%s", got.Action, tt.want, got.Reason)
			}
		})
	}
}

func TestSummarizeDelegationComparison(t *testing.T) {
	summary := SummarizeDelegationComparison([]EvaluationCase{
		{Name: "a", Request: DelegationRequest{TaskType: TaskReview, MaxDepth: 2, DirectCost: 10, DelegatedCost: 5, DirectLatencyMS: 100, DelegatedLatencyMS: 50}},
		{Name: "b", Request: DelegationRequest{TaskType: TaskChat, MaxDepth: 2, DirectCost: 1, DelegatedCost: 2, DirectLatencyMS: 10, DelegatedLatencyMS: 20}},
	})

	if summary.Cases != 2 {
		t.Fatalf("cases = %d, want 2", summary.Cases)
	}
	if summary.Delegated != 1 || summary.Direct != 1 {
		t.Fatalf("delegated/direct = %d/%d, want 1/1", summary.Delegated, summary.Direct)
	}
	if summary.DirectCostTotal != 11 || summary.SelectedCostTotal != 6 {
		t.Fatalf("cost totals = direct %.2f selected %.2f, want 11 and 6", summary.DirectCostTotal, summary.SelectedCostTotal)
	}
}
