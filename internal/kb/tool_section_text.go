package kb

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

func (s *Service) SectionText(ctx context.Context, scope Scope, evidenceScope EvidenceScope, input SectionTextInput) (*SectionTextResult, error) {
	start := time.Now()
	var result *SectionTextResult
	var err error
	defer func() {
		s.recordSectionTextQuality(evidenceScope, input, result, err, time.Since(start))
	}()
	if s == nil || s.store == nil {
		err = ErrInvalidInput
		return nil, err
	}
	if input.DocumentID == "" || (len(input.NodeIDs) == 0 && len(input.PageRanges) == 0) {
		err = ErrInvalidInput
		return nil, err
	}
	if len(input.NodeIDs) > 0 && len(input.PageRanges) > 0 {
		err = ErrInvalidInput
		return nil, err
	}
	if len(input.NodeIDs)+len(input.PageRanges) > s.maxNodeIDs {
		err = ErrOutputTooLarge
		return nil, err
	}
	scope.NamespaceNarrowing = input.NamespaceID
	if err = ValidateScope(scope); err != nil {
		return nil, err
	}
	if err = ValidateEvidenceScope(evidenceScope); err != nil {
		return nil, err
	}
	var nodes []TreeNode
	var doc *Document
	var ranges []PageRange
	if len(input.NodeIDs) > 0 {
		nodes, doc, err = s.store.GetSectionText(ctx, scope, input.DocumentID, input.NodeIDs)
	} else {
		ranges, err = ParsePageRanges(input.PageRanges)
		if err != nil {
			err = ErrInvalidInput
			return nil, err
		}
		if len(ranges) > s.maxNodeIDs {
			err = ErrOutputTooLarge
			return nil, err
		}
		nodes, doc, err = s.store.GetSectionTextByPageRanges(ctx, scope, input.DocumentID, ranges)
	}
	if err != nil {
		return nil, err
	}
	if len(nodes) > s.maxNodeIDs {
		err = ErrOutputTooLarge
		return nil, err
	}
	if len(ranges) > 0 {
		nodes = sliceNodesToPageRanges(nodes, ranges)
	}
	totalBytes := 0
	for _, node := range nodes {
		totalBytes += len([]byte(node.Text))
	}
	if totalBytes > s.maxSectionBytes {
		err = ErrOutputTooLarge
		return nil, err
	}
	result = &SectionTextResult{
		DocumentID:  doc.ID,
		NamespaceID: doc.NamespaceID,
		Sections:    make([]SectionText, 0, len(nodes)),
		Evidence:    make([]EvidenceRef, 0, len(nodes)),
	}
	for _, node := range nodes {
		ref, recordErr := s.RecordEvidence(ctx, evidenceScope, *doc, node, node.Title)
		if recordErr != nil {
			err = recordErr
			return nil, err
		}
		result.Sections = append(result.Sections, SectionText{
			NodeID:        node.ID,
			NodePath:      node.NodePath,
			Title:         node.Title,
			Text:          node.Text,
			EvidenceToken: ref.Token,
			StartLine:     node.StartLine,
			EndLine:       node.EndLine,
			StartPage:     node.StartPage,
			EndPage:       node.EndPage,
		})
		result.Evidence = append(result.Evidence, ref)
	}
	nodeIDs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		nodeIDs = append(nodeIDs, node.ID)
	}
	assets, err := s.store.ListNodeAssets(ctx, ManagementScope{
		DomainID:   scope.DomainID,
		OwnerScope: scope.OwnerScope,
		OwnerID:    scope.OwnerID,
		Now:        scope.Now,
	}, doc.ID, nodeIDs)
	if err != nil {
		return nil, err
	}
	for _, asset := range assets {
		if len(ranges) > 0 && !assetInPageRanges(asset, ranges) {
			continue
		}
		result.AssetRefs = append(result.AssetRefs, AssetRef{
			AssetURI:    asset.AssetURI,
			NodeID:      asset.NodeID,
			Line:        asset.Line,
			Page:        asset.Page,
			AltText:     asset.AltText,
			Caption:     asset.Caption,
			ContentHash: asset.ContentHash,
			MimeType:    asset.MimeType,
		})
	}
	return result, nil
}

func ParsePageRanges(raw []string) ([]PageRange, error) {
	out := make([]PageRange, 0, len(raw))
	seen := make(map[PageRange]bool)
	for _, item := range raw {
		for _, part := range strings.Split(item, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			startText, endText, ok := strings.Cut(part, "-")
			start, err := strconv.Atoi(strings.TrimSpace(startText))
			if err != nil || start <= 0 {
				return nil, fmt.Errorf("invalid page range %q", part)
			}
			end := start
			if ok {
				end, err = strconv.Atoi(strings.TrimSpace(endText))
				if err != nil || end <= 0 || start > end {
					return nil, fmt.Errorf("invalid page range %q", part)
				}
			}
			r := PageRange{Start: start, End: end}
			if seen[r] {
				continue
			}
			seen[r] = true
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil, ErrInvalidInput
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start == out[j].Start {
			return out[i].End < out[j].End
		}
		return out[i].Start < out[j].Start
	})
	return out, nil
}

func nodeOverlapsAnyPageRange(node TreeNode, ranges []PageRange) bool {
	if node.StartPage <= 0 || node.EndPage <= 0 {
		return false
	}
	for _, r := range ranges {
		if node.StartPage <= r.End && node.EndPage >= r.Start {
			return true
		}
	}
	return false
}

func sliceNodesToPageRanges(nodes []TreeNode, ranges []PageRange) []TreeNode {
	if len(ranges) == 0 {
		return nodes
	}
	out := make([]TreeNode, len(nodes))
	for i, node := range nodes {
		out[i] = sliceNodeTextToPageRanges(node, ranges)
	}
	return out
}

func sliceNodeTextToPageRanges(node TreeNode, ranges []PageRange) TreeNode {
	if node.Text == "" || len(ranges) == 0 {
		return node
	}
	lines := strings.Split(node.Text, "\n")
	pageByLine := make([]int, len(lines))
	currentPage := 0
	hasAnchor := false
	for i, line := range lines {
		if page := pageAnchorFromLine(line); page > 0 {
			currentPage = page
			hasAnchor = true
		}
		pageByLine[i] = currentPage
	}
	if !hasAnchor {
		return node
	}
	selected := make([]string, 0, len(lines))
	firstOffset := 0
	lastOffset := 0
	for i, line := range lines {
		if !pageInRanges(pageByLine[i], ranges) {
			continue
		}
		if len(selected) == 0 {
			firstOffset = i
		}
		lastOffset = i
		selected = append(selected, line)
	}
	if len(selected) == 0 {
		return node
	}
	node.Text = strings.TrimRight(strings.Join(selected, "\n"), "\n")
	node.StartLine += firstOffset
	node.EndLine = node.StartLine + (lastOffset - firstOffset)
	node.StartPage = firstSelectedPage(pageByLine, ranges, node.StartPage)
	node.EndPage = lastSelectedPage(pageByLine, ranges, node.EndPage)
	return node
}

func pageInRanges(page int, ranges []PageRange) bool {
	if page <= 0 {
		return false
	}
	for _, r := range ranges {
		if page >= r.Start && page <= r.End {
			return true
		}
	}
	return false
}

func firstSelectedPage(pages []int, ranges []PageRange, fallback int) int {
	for _, page := range pages {
		if pageInRanges(page, ranges) {
			return page
		}
	}
	return fallback
}

func lastSelectedPage(pages []int, ranges []PageRange, fallback int) int {
	for i := len(pages) - 1; i >= 0; i-- {
		if pageInRanges(pages[i], ranges) {
			return pages[i]
		}
	}
	return fallback
}

func assetInPageRanges(asset NodeAsset, ranges []PageRange) bool {
	if len(ranges) == 0 {
		return true
	}
	return asset.Page > 0 && pageInRanges(asset.Page, ranges)
}

func (s *Service) recordSectionTextQuality(scope EvidenceScope, input SectionTextInput, result *SectionTextResult, err error, latency time.Duration) {
	if s == nil || s.qualityRecorder == nil {
		return
	}
	status := agentquality.StatusPass
	failure := agentquality.FailureNone
	reason := ""
	errText := ""
	if err != nil {
		status = agentquality.StatusFail
		failure = agentquality.FailureKBRetrieval
		reason = agentquality.KBFailureSectionText
		errText = err.Error()
	}
	attrs := map[string]any{
		"operation":      "kb.section.text",
		"doc_id":         input.DocumentID,
		"namespace_id":   input.NamespaceID,
		"requested":      len(input.NodeIDs),
		"node_ids":       append([]string(nil), input.NodeIDs...),
		"page_ranges":    append([]string(nil), input.PageRanges...),
		"latency_ms":     latency.Milliseconds(),
		"owner_scope_kb": string(scope.OwnerScope),
		"owner_id_kb":    scope.OwnerID,
	}
	if result != nil {
		attrs["returned_sections"] = len(result.Sections)
		attrs["evidence_count"] = len(result.Evidence)
		attrs["doc_id"] = result.DocumentID
		attrs["namespace_id"] = result.NamespaceID
		returnedNodeIDs := make([]string, 0, len(result.Sections))
		for _, section := range result.Sections {
			returnedNodeIDs = append(returnedNodeIDs, section.NodeID)
		}
		attrs["returned_node_ids"] = returnedNodeIDs
	}
	s.qualityRecorder.RecordKBQualityEvent(scope.SessionID, agentquality.NewKBQualityEvent(agentquality.KBEventInput{
		Name:       agentquality.EventKBRetrieval,
		SessionID:  scope.SessionID,
		TurnID:     scope.TurnID,
		TraceID:    scope.TraceID,
		SpanID:     scope.ToolCallID,
		ToolCallID: scope.ToolCallID,
		DomainID:   scope.DomainID,
		OwnerScope: agentquality.OwnerScope(scope.OwnerScope),
		OwnerID:    scope.OwnerID,
		ToolName:   "kb.section.text",
		Status:     status,
		Failure:    failure,
		Reason:     reason,
		Error:      errText,
		Attributes: attrs,
	}))
}
