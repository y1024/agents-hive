package kb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

func (s *Service) ResolveBinding(ctx context.Context, input BindingResolveInput) ([]string, error) {
	if s == nil || s.resolver == nil {
		return nil, ErrInvalidInput
	}
	return s.resolver.Resolve(ctx, input)
}

func (s *Service) IngestMarkdown(ctx context.Context, scope Scope, input IngestMarkdownInput) (*Document, error) {
	return s.ingestMarkdown(ctx, scope, input, "")
}

func (s *Service) ingestMarkdown(ctx context.Context, scope Scope, input IngestMarkdownInput, forcedDocumentID string) (*Document, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	if input.NamespaceID == "" || input.Title == "" || input.Version == "" {
		return nil, ErrInvalidInput
	}
	if strings.TrimSpace(input.Content) == "" {
		return nil, ErrEmptyDocument
	}
	if containsMarkdownAssetReference(input.Content) {
		return nil, ErrUnsupportedAsset
	}
	if !containsNamespace(scope, input.NamespaceID) {
		return nil, ErrNamespaceNotBound
	}
	namespace, err := s.store.GetNamespace(ctx, input.NamespaceID)
	if err != nil {
		return nil, err
	}
	if namespace.DomainID != scope.DomainID || namespace.OwnerScope != scope.OwnerScope || namespace.OwnerID != scope.OwnerID {
		return nil, ErrNotFound
	}
	now := scope.Now
	if now.IsZero() {
		now = time.Now()
	}
	contentHash := hashDocument(input.Content)
	documentID := StableDocumentID(input.NamespaceID, contentHash)
	if strings.TrimSpace(forcedDocumentID) != "" {
		documentID = strings.TrimSpace(forcedDocumentID)
	}
	doc := Document{
		ID:          documentID,
		NamespaceID: input.NamespaceID,
		DomainID:    scope.DomainID,
		OwnerScope:  scope.OwnerScope,
		OwnerID:     scope.OwnerID,
		SourceURI:   input.SourceURI,
		Title:       input.Title,
		ContentHash: contentHash,
		Version:     input.Version,
		Status:      DocumentActive,
		EffectiveAt: input.EffectiveAt,
		ExpiresAt:   input.ExpiresAt,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if doc.EffectiveAt.IsZero() {
		doc.EffectiveAt = now
	}
	nodes, err := BuildMarkdownTree(input.Content, BuildTreeOptions{
		DocumentID:   doc.ID,
		NamespaceID:  doc.NamespaceID,
		DomainID:     doc.DomainID,
		OwnerScope:   doc.OwnerScope,
		OwnerID:      doc.OwnerID,
		TokenCounter: s.tokenCounter,
	})
	if err != nil {
		return nil, err
	}
	nodes = ThinTree(nodes, ThinTreeOptions{
		Enabled:        namespace.ThinningEnabled,
		TokenThreshold: namespace.ThinningTokenThreshold,
		TokenCounter:   s.tokenCounter,
	})
	for i := range nodes {
		nodes[i].DocumentID = doc.ID
		nodes[i].NamespaceID = doc.NamespaceID
		nodes[i].DomainID = doc.DomainID
		nodes[i].OwnerScope = doc.OwnerScope
		nodes[i].OwnerID = doc.OwnerID
	}
	nodes, err = SummarizeTree(ctx, nodes, SummarizeTreeOptions{
		TokenThreshold: namespace.SummaryTokenThreshold,
		Model:          namespace.SummaryModel,
		Generator:      s.summaryGenerator,
		TokenCounter:   s.tokenCounter,
	})
	if err != nil {
		s.recordSummaryQuality(scope, *namespace, doc, err)
		return nil, err
	}
	if err := s.store.SaveDocument(ctx, doc, nodes); err != nil {
		if err == ErrDuplicateDocument {
			existing, findErr := s.findDuplicateDocument(ctx, scope, input.NamespaceID, contentHash, input.Version)
			if findErr == nil {
				return existing, nil
			}
		}
		return nil, err
	}
	return &doc, nil
}

func (s *Service) recordSummaryQuality(scope Scope, namespace Namespace, doc Document, err error) {
	if s == nil || s.qualityRecorder == nil || err == nil {
		return
	}
	s.qualityRecorder.RecordKBQualityEvent("", agentquality.NewKBQualityEvent(agentquality.KBEventInput{
		Name:       agentquality.EventKBRetrieval,
		DomainID:   scope.DomainID,
		OwnerScope: agentquality.OwnerScope(scope.OwnerScope),
		OwnerID:    scope.OwnerID,
		ToolName:   "kb.summary",
		Status:     agentquality.StatusFail,
		Failure:    agentquality.FailureKBSummary,
		Reason:     agentquality.KBFailureSummaryGenerator,
		Error:      err.Error(),
		Attributes: map[string]any{
			"operation":               "kb.summary",
			"namespace_id":            namespace.ID,
			"doc_id":                  doc.ID,
			"summary_model":           namespace.SummaryModel,
			"summary_token_threshold": namespace.SummaryTokenThreshold,
		},
	}))
}

func (s *Service) findDuplicateDocument(ctx context.Context, scope Scope, namespaceID, contentHash, version string) (*Document, error) {
	scope.NamespaceNarrowing = namespaceID
	docs, err := s.store.ListDocuments(ctx, scope, DocumentQuery{})
	if err != nil {
		return nil, err
	}
	for _, doc := range docs {
		if doc.ContentHash == contentHash && doc.Version == version {
			return &doc, nil
		}
	}
	return nil, ErrDuplicateDocument
}

func hashDocument(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func containsNamespace(scope Scope, namespaceID string) bool {
	for _, id := range effectiveNamespaceIDs(scope) {
		if id == namespaceID {
			return true
		}
	}
	return false
}

func containsMarkdownAssetReference(content string) bool {
	inFence := false
	fenceMarker := ""
	for _, line := range strings.Split(content, "\n") {
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
		for offset := 0; offset < len(line); {
			start := strings.Index(line[offset:], "![")
			if start < 0 {
				break
			}
			start += offset
			closingAlt := strings.Index(line[start+2:], "](")
			if closingAlt < 0 {
				break
			}
			uriStart := start + 2 + closingAlt + len("](")
			uriEnd := strings.Index(line[uriStart:], ")")
			if uriEnd < 0 {
				break
			}
			target := strings.TrimSpace(line[uriStart : uriStart+uriEnd])
			if target != "" && !strings.HasPrefix(target, "asset://") {
				return true
			}
			offset = uriStart + uriEnd + 1
		}
	}
	return false
}
