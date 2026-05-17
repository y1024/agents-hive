package kb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMarkdownTreeSkipsCodeBlockHeadings(t *testing.T) {
	md := "# A\ntext\n```go\n# not heading\n```\n~~~\n## also not heading\n~~~\n## B\nbody\n"
	nodes, err := BuildMarkdownTree(md, BuildTreeOptions{})
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.Equal(t, "A", nodes[0].Title)
	assert.Equal(t, "B", nodes[1].Title)
	assert.Equal(t, "0000", nodes[0].ID)
	assert.Equal(t, "0001", nodes[1].ID)
	require.NotNil(t, nodes[1].ParentNodeID)
	assert.Equal(t, "0000", *nodes[1].ParentNodeID)
}

func TestBuildMarkdownTreePreambleAndLineRanges(t *testing.T) {
	md := "intro\nmore intro\n# A\nbody\n## B\nchild\n"
	nodes, err := BuildMarkdownTree(md, BuildTreeOptions{})
	require.NoError(t, err)
	require.Len(t, nodes, 3)
	assert.Equal(t, "Preamble", nodes[0].Title)
	assert.Equal(t, "0000", nodes[0].ID)
	assert.Equal(t, "0", nodes[0].NodePath)
	assert.Equal(t, 1, nodes[0].StartLine)
	assert.Equal(t, 2, nodes[0].EndLine)
	assert.Equal(t, "# A\nbody", nodes[1].Text)
	assert.Equal(t, 3, nodes[1].StartLine)
	assert.Equal(t, 4, nodes[1].EndLine)
	assert.Equal(t, "1.1", nodes[2].NodePath)
}

func TestBuildMarkdownTreeExtractsPageAnchors(t *testing.T) {
	md := "# A\n<physical_index_3>\nbody\n## B\n<!-- page: 5 -->\nchild\n# C\n[[page=8]]\nthird\n"
	nodes, err := BuildMarkdownTree(md, BuildTreeOptions{})
	require.NoError(t, err)
	require.Len(t, nodes, 3)
	assert.Equal(t, 3, nodes[0].StartPage)
	assert.Equal(t, 3, nodes[0].EndPage)
	assert.Equal(t, 5, nodes[1].StartPage)
	assert.Equal(t, 5, nodes[1].EndPage)
	assert.Equal(t, 8, nodes[2].StartPage)
	assert.Equal(t, 8, nodes[2].EndPage)
}

func TestBuildMarkdownTreeDuplicateTitlesAndLevelJump(t *testing.T) {
	md := "# A\none\n### A\ntwo\n# A\nthree\n"
	nodes, err := BuildMarkdownTree(md, BuildTreeOptions{})
	require.NoError(t, err)
	require.Len(t, nodes, 3)
	assert.Equal(t, "A", nodes[0].Title)
	assert.Equal(t, "A", nodes[1].Title)
	assert.Equal(t, "A", nodes[2].Title)
	assert.Equal(t, "1", nodes[0].NodePath)
	assert.Equal(t, "1.1", nodes[1].NodePath)
	assert.Equal(t, "2", nodes[2].NodePath)
}

func TestBuildMarkdownTreeRejectsEmptyAndNoHeading(t *testing.T) {
	_, err := BuildMarkdownTree("", BuildTreeOptions{})
	require.ErrorIs(t, err, ErrEmptyDocument)
	_, err = BuildMarkdownTree("plain text", BuildTreeOptions{})
	require.ErrorIs(t, err, ErrNoHeading)
}
