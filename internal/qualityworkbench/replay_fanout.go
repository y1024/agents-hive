package qualityworkbench

type ReplayClusterFanoutPlan struct {
	Total            int        `json:"total"`
	Limit            int        `json:"limit"`
	SelectedIDs      []string   `json:"selected_ids"`
	Truncated        bool       `json:"truncated"`
	Remaining        int        `json:"remaining"`
	RemainingBatches [][]string `json:"remaining_batches"`
}

func PlanReplayClusterFanout(targetIDs []string, limit int) ReplayClusterFanoutPlan {
	total := len(targetIDs)
	if limit <= 0 || limit > total {
		limit = total
	}
	plan := ReplayClusterFanoutPlan{
		Total:       total,
		Limit:       limit,
		SelectedIDs: append([]string(nil), targetIDs[:limit]...),
	}
	if total <= limit {
		return plan
	}
	plan.Truncated = true
	plan.Remaining = total - limit
	for start := limit; start < total; start += limit {
		end := start + limit
		if end > total {
			end = total
		}
		plan.RemainingBatches = append(plan.RemainingBatches, append([]string(nil), targetIDs[start:end]...))
	}
	return plan
}
