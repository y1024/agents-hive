package kb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThinTreeDisabledRenumbersWithoutMerging(t *testing.T) {
	parent := "0007"
	nodes := []TreeNode{
		{ID: "0007", Title: "A", Text: "parent", TokenCount: 1, ContentHash: "old"},
		{ID: "0009", ParentNodeID: &parent, Title: "B", Text: "child", TokenCount: 1, ContentHash: "child"},
	}
	out := ThinTree(nodes, ThinTreeOptions{Enabled: false, TokenThreshold: 10, TokenCounter: fakeCounter{}})
	require.Len(t, out, 2)
	assert.Equal(t, "0000", out[0].ID)
	assert.Equal(t, "0001", out[1].ID)
	require.NotNil(t, out[1].ParentNodeID)
	assert.Equal(t, "0000", *out[1].ParentNodeID)
	assert.Equal(t, "child", out[1].Text)
}

func TestThinTreeMergesSmallParentChildrenAndRenumbers(t *testing.T) {
	parent := "0000"
	nodes := []TreeNode{
		{ID: "0000", Title: "A", Text: "parent", TokenCount: 1, ContentHash: "old", StartLine: 1, EndLine: 2},
		{ID: "0001", ParentNodeID: &parent, Title: "B", Text: "child", TokenCount: 1, ContentHash: "child", StartLine: 3, EndLine: 5},
	}
	out := ThinTree(nodes, ThinTreeOptions{Enabled: true, TokenThreshold: 10, TokenCounter: fakeCounter{}})
	require.Len(t, out, 1)
	assert.Equal(t, "0000", out[0].ID)
	assert.Contains(t, out[0].Text, "parent")
	assert.Contains(t, out[0].Text, "child")
	assert.Equal(t, 1, out[0].StartLine)
	assert.Equal(t, 5, out[0].EndLine)
	assert.NotEqual(t, "old", out[0].ContentHash)
}

func TestThinTreePreservesChildPageAnchorsWhenParentHasNone(t *testing.T) {
	parent := "0000"
	nodes := []TreeNode{
		{ID: "0000", Title: "A", Text: "parent", TokenCount: 1, StartLine: 1, EndLine: 1},
		{ID: "0001", ParentNodeID: &parent, Title: "B", Text: "<physical_index_5>\nchild", TokenCount: 1, StartLine: 2, EndLine: 3, StartPage: 5, EndPage: 5},
	}
	out := ThinTree(nodes, ThinTreeOptions{Enabled: true, TokenThreshold: 10, TokenCounter: fakeCounter{}})
	require.Len(t, out, 1)
	assert.Equal(t, 5, out[0].StartPage)
	assert.Equal(t, 5, out[0].EndPage)
}
