package kb

import (
	"context"
	"errors"
)

func (s *Service) DocMeta(ctx context.Context, scope Scope, input DocMetaInput) (*DocMetaResult, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	scope.NamespaceNarrowing = input.NamespaceID
	if err := ValidateScope(scope); err != nil {
		if errors.Is(err, ErrNoKBBinding) {
			return &DocMetaResult{NoKBBound: true}, nil
		}
		if errors.Is(err, ErrNamespaceNotBound) {
			return &DocMetaResult{Documents: []DocMetaDocument{}}, nil
		}
		return nil, err
	}
	docs, err := s.store.ListDocuments(ctx, scope, DocumentQuery{Limit: input.Limit, Query: input.Query})
	if err != nil {
		return nil, err
	}
	result := &DocMetaResult{Documents: make([]DocMetaDocument, 0, len(docs))}
	for _, doc := range docs {
		nodeCount, lineCount, pageCount := s.documentStructureStats(ctx, scope, doc)
		result.Documents = append(result.Documents, DocMetaDocument{
			ID:          doc.ID,
			NamespaceID: doc.NamespaceID,
			Title:       doc.Title,
			Version:     doc.Version,
			Status:      doc.Status,
			Description: doc.Description,
			SourceURI:   doc.SourceURI,
			PageCount:   pageCount,
			LineCount:   lineCount,
			NodeCount:   nodeCount,
			EffectiveAt: doc.EffectiveAt,
			ExpiresAt:   doc.ExpiresAt,
		})
	}
	return result, nil
}

func (s *Service) documentStructureStats(ctx context.Context, scope Scope, doc Document) (int, int, int) {
	if s == nil || s.store == nil || doc.ID == "" {
		return 0, 0, 0
	}
	nodes, err := s.store.GetStructure(ctx, scope, doc.ID)
	if err != nil {
		return 0, 0, 0
	}
	maxLine := 0
	maxPage := 0
	for _, node := range nodes {
		if node.EndLine > maxLine {
			maxLine = node.EndLine
		}
		if node.EndPage > maxPage {
			maxPage = node.EndPage
		}
	}
	return len(nodes), maxLine, maxPage
}
