package kb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeTreeShortCircuitsSmallNodes(t *testing.T) {
	gen := &fakeSummary{}
	nodes := []TreeNode{{ID: "0000", Title: "A", Text: "short", TokenCount: 1}}
	out, err := SummarizeTree(context.Background(), nodes, SummarizeTreeOptions{
		TokenThreshold: 10,
		Generator:      gen,
	})
	require.NoError(t, err)
	assert.Equal(t, "short", out[0].Summary)
	assert.Equal(t, 0, gen.calls)
}

func TestSummarizeTreeBranchUsesPrefixSummary(t *testing.T) {
	parent := "0000"
	nodes := []TreeNode{
		{ID: "0000", Title: "A", Text: "long parent", TokenCount: 10},
		{ID: "0001", ParentNodeID: &parent, Title: "B", Text: "long child", TokenCount: 10},
	}
	out, err := SummarizeTree(context.Background(), nodes, SummarizeTreeOptions{
		TokenThreshold: 2,
		Generator:      &fakeSummary{},
	})
	require.NoError(t, err)
	assert.Equal(t, "summary:long parent", out[0].PrefixSummary)
	assert.Empty(t, out[0].Summary)
	assert.Equal(t, "summary:long child", out[1].Summary)
	assert.Empty(t, out[1].PrefixSummary)
}
