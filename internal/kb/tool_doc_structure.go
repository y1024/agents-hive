package kb

import "context"

func (s *Service) DocStructure(ctx context.Context, scope Scope, input DocStructureInput) (*DocStructureResult, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	if input.DocumentID == "" {
		return nil, ErrInvalidInput
	}
	scope.NamespaceNarrowing = input.NamespaceID
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	doc, err := s.store.GetDocument(ctx, scope, input.DocumentID)
	if err != nil {
		return nil, err
	}
	nodes, err := s.store.GetStructure(ctx, scope, input.DocumentID)
	if err != nil {
		return nil, err
	}
	return &DocStructureResult{
		DocumentID:  doc.ID,
		NamespaceID: doc.NamespaceID,
		Nodes:       buildStructure(nodes),
	}, nil
}

func buildStructure(nodes []TreeNode) []StructureNode {
	itemsByID := make(map[string]StructureNode, len(nodes))
	childrenByID := make(map[string][]string, len(nodes))
	rootIDs := make([]string, 0)
	for _, node := range nodes {
		itemsByID[node.ID] = StructureNode{
			ID:            node.ID,
			ParentNodeID:  node.ParentNodeID,
			NodePath:      node.NodePath,
			Title:         node.Title,
			Level:         node.Level,
			TokenCount:    node.TokenCount,
			Summary:       node.Summary,
			PrefixSummary: node.PrefixSummary,
			StartLine:     node.StartLine,
			EndLine:       node.EndLine,
			StartPage:     node.StartPage,
			EndPage:       node.EndPage,
		}
		if node.ParentNodeID == nil {
			rootIDs = append(rootIDs, node.ID)
			continue
		}
		if _, ok := itemsByID[*node.ParentNodeID]; !ok {
			rootIDs = append(rootIDs, node.ID)
			continue
		}
		childrenByID[*node.ParentNodeID] = append(childrenByID[*node.ParentNodeID], node.ID)
	}
	var build func(id string) StructureNode
	build = func(id string) StructureNode {
		item := itemsByID[id]
		for _, childID := range childrenByID[id] {
			child := build(childID)
			item.Children = append(item.Children, child)
			if child.StartPage > 0 && (item.StartPage == 0 || child.StartPage < item.StartPage) {
				item.StartPage = child.StartPage
			}
			if child.EndPage > item.EndPage {
				item.EndPage = child.EndPage
			}
		}
		return item
	}
	roots := make([]StructureNode, 0, len(rootIDs))
	for _, id := range rootIDs {
		roots = append(roots, build(id))
	}
	return roots
}
