package kb

import (
	"strings"
	"time"
)

type TokenCounter interface {
	CountTokens(text string) int
}

type EstimateTokenCounter struct{}

func (EstimateTokenCounter) CountTokens(text string) int {
	runes := len([]rune(text))
	if runes == 0 {
		return 0
	}
	count := runes / 2
	if count == 0 {
		return 1
	}
	return count
}

type ThinTreeOptions struct {
	Enabled        bool
	TokenThreshold int
	TokenCounter   TokenCounter
}

func ThinTree(nodes []TreeNode, opts ThinTreeOptions) []TreeNode {
	copied := cloneNodes(nodes)
	if !opts.Enabled || opts.TokenThreshold <= 0 || len(copied) == 0 {
		return RenumberTreeNodes(copied)
	}
	children := childrenByParent(copied)
	remove := make(map[string]bool)
	for i := len(copied) - 1; i >= 0; i-- {
		node := &copied[i]
		nodeChildren := children[node.ID]
		if len(nodeChildren) == 0 || node.TokenCount >= opts.TokenThreshold {
			continue
		}
		parts := []string{node.Text}
		endLine := node.EndLine
		startPage := node.StartPage
		endPage := node.EndPage
		for _, childIndex := range nodeChildren {
			child := copied[childIndex]
			if remove[child.ID] {
				continue
			}
			if child.Text != "" {
				parts = append(parts, child.Text)
			}
			if child.EndLine > endLine {
				endLine = child.EndLine
			}
			if startPage == 0 || child.StartPage > 0 && child.StartPage < startPage {
				startPage = child.StartPage
			}
			if child.EndPage > endPage {
				endPage = child.EndPage
			}
			markSubtreeRemoved(copied, child.ID, children, remove)
		}
		node.Text = strings.TrimSpace(strings.Join(parts, "\n\n"))
		node.EndLine = endLine
		node.StartPage = startPage
		node.EndPage = endPage
		counter := opts.TokenCounter
		if counter == nil {
			counter = EstimateTokenCounter{}
		}
		node.TokenCount = counter.CountTokens(node.Text)
		node.ContentHash = hashText(node.Text)
	}
	thinned := make([]TreeNode, 0, len(copied)-len(remove))
	for _, node := range copied {
		if !remove[node.ID] {
			thinned = append(thinned, node)
		}
	}
	return RenumberTreeNodes(thinned)
}

func markSubtreeRemoved(nodes []TreeNode, nodeID string, children map[string][]int, remove map[string]bool) {
	remove[nodeID] = true
	for _, childIndex := range children[nodeID] {
		markSubtreeRemoved(nodes, nodes[childIndex].ID, children, remove)
	}
}

func RenumberTreeNodes(nodes []TreeNode) []TreeNode {
	out := cloneNodes(nodes)
	oldToNew := make(map[string]string, len(out))
	for i := range out {
		oldToNew[out[i].ID] = formatNodeID(i)
	}
	for i := range out {
		oldParent := out[i].ParentNodeID
		out[i].ID = formatNodeID(i)
		if oldParent != nil {
			if newParent, ok := oldToNew[*oldParent]; ok {
				parent := newParent
				out[i].ParentNodeID = &parent
			} else {
				out[i].ParentNodeID = nil
			}
		}
		if out[i].ContentHash == "" {
			out[i].ContentHash = hashText(out[i].Text)
		}
		if out[i].CreatedAt.IsZero() {
			out[i].CreatedAt = time.Now()
		}
	}
	return out
}

func cloneNodes(nodes []TreeNode) []TreeNode {
	out := make([]TreeNode, len(nodes))
	for i, node := range nodes {
		out[i] = node
		if node.ParentNodeID != nil {
			parent := *node.ParentNodeID
			out[i].ParentNodeID = &parent
		}
	}
	return out
}

func childrenByParent(nodes []TreeNode) map[string][]int {
	children := make(map[string][]int)
	for i, node := range nodes {
		if node.ParentNodeID == nil {
			continue
		}
		children[*node.ParentNodeID] = append(children[*node.ParentNodeID], i)
	}
	return children
}
