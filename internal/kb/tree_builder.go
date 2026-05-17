package kb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var headingRE = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
var pageAnchorREs = []*regexp.Regexp{
	regexp.MustCompile(`(?i)<(?:physical|page|start)_index_(\d+)>`),
	regexp.MustCompile(`(?i)<page[_ -]?(\d+)>`),
	regexp.MustCompile(`(?i)^\s*<!--\s*page\s*[:=]\s*(\d+)\s*-->\s*$`),
	regexp.MustCompile(`(?i)^\s*\[\[page\s*[:=]\s*(\d+)\]\]\s*$`),
}

type BuildTreeOptions struct {
	DocumentID   string
	NamespaceID  string
	DomainID     string
	OwnerScope   OwnerScope
	OwnerID      string
	TokenCounter TokenCounter
}

type draftNode struct {
	title     string
	level     int
	startLine int
	endLine   int
	textStart int
	textEnd   int
	parent    *int
	ordinal   int
	nodePath  string
}

func BuildMarkdownTree(markdown string, opts BuildTreeOptions) ([]TreeNode, error) {
	if strings.TrimSpace(markdown) == "" {
		return nil, ErrEmptyDocument
	}
	lines := strings.Split(markdown, "\n")
	headings := extractMarkdownHeadings(lines)
	if len(headings) == 0 {
		return nil, ErrNoHeading
	}
	nodes := make([]draftNode, 0, len(headings)+1)
	firstHeadingLine := headings[0].line
	if strings.TrimSpace(strings.Join(lines[:firstHeadingLine-1], "\n")) != "" {
		nodes = append(nodes, draftNode{
			title:     "Preamble",
			level:     0,
			startLine: 1,
			textStart: 0,
			textEnd:   firstHeadingLine - 1,
		})
	}
	stack := make([]int, 0, 6)
	ordinals := make([]int, 7)
	for i, heading := range headings {
		if strings.TrimSpace(heading.title) == "" {
			return nil, ErrEmptyHeading
		}
		for len(stack) > 0 && nodes[stack[len(stack)-1]].level >= heading.level {
			stack = stack[:len(stack)-1]
		}
		var parent *int
		if len(stack) > 0 {
			parentIndex := stack[len(stack)-1]
			parent = &parentIndex
		}
		ordinals[heading.level]++
		for level := heading.level + 1; level < len(ordinals); level++ {
			ordinals[level] = 0
		}
		pathParts := make([]string, 0, heading.level)
		for level := 1; level <= heading.level; level++ {
			if ordinals[level] > 0 {
				pathParts = append(pathParts, fmt.Sprintf("%d", ordinals[level]))
			}
		}
		textEnd := len(lines)
		if i+1 < len(headings) {
			textEnd = headings[i+1].line - 1
		}
		nodes = append(nodes, draftNode{
			title:     heading.title,
			level:     heading.level,
			startLine: heading.line,
			textStart: heading.line - 1,
			textEnd:   textEnd,
			parent:    parent,
			nodePath:  strings.Join(pathParts, "."),
		})
		stack = append(stack, len(nodes)-1)
	}
	if len(nodes) > 0 && nodes[0].title == "Preamble" {
		nodes[0].nodePath = "0"
	}
	return materializeDraftNodes(lines, nodes, opts), nil
}

type markdownHeading struct {
	line  int
	level int
	title string
}

func extractMarkdownHeadings(lines []string) []markdownHeading {
	headings := make([]markdownHeading, 0)
	inFence := false
	fenceMarker := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inFence {
				inFence = true
				fenceMarker = marker
			} else if marker == fenceMarker {
				inFence = false
				fenceMarker = ""
			}
			continue
		}
		if inFence {
			continue
		}
		match := headingRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		headings = append(headings, markdownHeading{
			line:  i + 1,
			level: len(match[1]),
			title: strings.TrimSpace(match[2]),
		})
	}
	return headings
}

func materializeDraftNodes(lines []string, drafts []draftNode, opts BuildTreeOptions) []TreeNode {
	now := time.Now()
	nodes := make([]TreeNode, len(drafts))
	for i, draft := range drafts {
		textLines := append([]string(nil), lines[draft.textStart:draft.textEnd]...)
		text := strings.TrimRight(strings.Join(textLines, "\n"), "\n")
		tokenCount := 0
		if opts.TokenCounter != nil {
			tokenCount = opts.TokenCounter.CountTokens(text)
		} else {
			tokenCount = EstimateTokenCounter{}.CountTokens(text)
		}
		var parentID *string
		if draft.parent != nil {
			id := formatNodeID(*draft.parent)
			parentID = &id
		}
		nodes[i] = TreeNode{
			ID:           formatNodeID(i),
			DocumentID:   opts.DocumentID,
			NamespaceID:  opts.NamespaceID,
			DomainID:     opts.DomainID,
			OwnerScope:   opts.OwnerScope,
			OwnerID:      opts.OwnerID,
			ParentNodeID: parentID,
			NodePath:     draft.nodePath,
			Title:        draft.title,
			Level:        draft.level,
			Text:         text,
			TokenCount:   tokenCount,
			StartLine:    draft.startLine,
			EndLine:      draft.textEnd,
			StartPage:    firstPageAnchor(textLines),
			EndPage:      lastPageAnchor(textLines),
			ContentHash:  hashText(text),
			CreatedAt:    now,
		}
		if nodes[i].StartPage == 0 && i > 0 {
			nodes[i].StartPage = nodes[i-1].EndPage
		}
		if nodes[i].EndPage == 0 {
			nodes[i].EndPage = nodes[i].StartPage
		}
	}
	return nodes
}

func firstPageAnchor(lines []string) int {
	for _, line := range lines {
		if page := pageAnchorFromLine(line); page > 0 {
			return page
		}
	}
	return 0
}

func lastPageAnchor(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if page := pageAnchorFromLine(lines[i]); page > 0 {
			return page
		}
	}
	return 0
}

func pageAnchorFromLine(line string) int {
	for _, re := range pageAnchorREs {
		match := re.FindStringSubmatch(line)
		if len(match) < 2 {
			continue
		}
		page, err := strconv.Atoi(match[1])
		if err == nil && page > 0 {
			return page
		}
	}
	return 0
}

func formatNodeID(index int) string {
	return fmt.Sprintf("%04d", index)
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
