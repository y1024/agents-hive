package kb

import (
	"context"
)

type SummaryGenerator interface {
	Summarize(ctx context.Context, text string, model string) (string, error)
}

type SummarizeTreeOptions struct {
	TokenThreshold int
	Model          string
	Generator      SummaryGenerator
	TokenCounter   TokenCounter
}

func SummarizeTree(ctx context.Context, nodes []TreeNode, opts SummarizeTreeOptions) ([]TreeNode, error) {
	out := cloneNodes(nodes)
	counter := opts.TokenCounter
	if counter == nil {
		counter = EstimateTokenCounter{}
	}
	children := childrenByParent(out)
	for i := range out {
		if out[i].TokenCount == 0 {
			out[i].TokenCount = counter.CountTokens(out[i].Text)
		}
		target := out[i].Text
		summary := target
		if opts.TokenThreshold > 0 && out[i].TokenCount >= opts.TokenThreshold {
			if opts.Generator == nil {
				return nil, ErrInvalidInput
			}
			generated, err := opts.Generator.Summarize(ctx, target, opts.Model)
			if err != nil {
				return nil, err
			}
			summary = generated
		}
		if len(children[out[i].ID]) > 0 {
			out[i].PrefixSummary = summary
			out[i].Summary = ""
		} else {
			out[i].Summary = summary
			out[i].PrefixSummary = ""
		}
	}
	return out, nil
}
