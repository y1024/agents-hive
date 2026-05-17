package kb

import (
	"context"
	"slices"
	"time"
)

type Store interface {
	SaveNamespace(ctx context.Context, namespace Namespace) error
	GetNamespace(ctx context.Context, namespaceID string) (*Namespace, error)
	ListNamespaces(ctx context.Context, query NamespaceQuery) ([]Namespace, error)
	SaveDocument(ctx context.Context, document Document, nodes []TreeNode) error
	UpdateDocumentStatus(ctx context.Context, scope ManagementScope, documentID string, status DocumentStatus) error
	GetDocument(ctx context.Context, scope Scope, documentID string) (*Document, error)
	GetDocumentForManagement(ctx context.Context, scope ManagementScope, documentID string) (*Document, error)
	ListDocuments(ctx context.Context, scope Scope, query DocumentQuery) ([]Document, error)
	ListDocumentsForManagement(ctx context.Context, scope ManagementScope, namespaceID string, query DocumentQuery) ([]Document, error)
	GetStructure(ctx context.Context, scope Scope, documentID string) ([]TreeNode, error)
	GetStructureForManagement(ctx context.Context, scope ManagementScope, documentID string, includeText bool) ([]TreeNode, *Document, error)
	GetSectionText(ctx context.Context, scope Scope, documentID string, nodeIDs []string) ([]TreeNode, *Document, error)
	GetSectionTextByPageRanges(ctx context.Context, scope Scope, documentID string, ranges []PageRange) ([]TreeNode, *Document, error)
	SaveBinding(ctx context.Context, binding Binding) error
	ListBindings(ctx context.Context, input BindingResolveInput) ([]Binding, error)
	ListBindingsForManagement(ctx context.Context, query BindingQuery) ([]Binding, error)
	SaveEvidenceEvent(ctx context.Context, event EvidenceEvent) error
	ListEvidenceEvents(ctx context.Context, scope EvidenceScope) ([]EvidenceEvent, error)
	SaveNodeAssets(ctx context.Context, assets []NodeAsset) error
	DeleteNodeAssetsForDocument(ctx context.Context, scope ManagementScope, documentID string) error
	ListNodeAssets(ctx context.Context, scope ManagementScope, documentID string, nodeIDs []string) ([]NodeAsset, error)
}

type PageRange struct {
	Start int
	End   int
}

func ValidateScope(scope Scope) error {
	if scope.DomainID == "" || scope.OwnerScope == "" || scope.OwnerID == "" {
		return ErrInvalidScope
	}
	if len(scope.NamespaceIDs) == 0 {
		return ErrNoKBBinding
	}
	if scope.NamespaceNarrowing != "" && !slices.Contains(scope.NamespaceIDs, scope.NamespaceNarrowing) {
		return ErrNamespaceNotBound
	}
	return nil
}

func ValidateManagementScope(scope ManagementScope) error {
	if scope.DomainID == "" || scope.OwnerScope == "" || scope.OwnerID == "" {
		return ErrInvalidScope
	}
	return nil
}

func normalizeScope(scope Scope) Scope {
	if scope.Now.IsZero() {
		scope.Now = time.Now()
	}
	scope.NamespaceIDs = uniqueNonEmpty(scope.NamespaceIDs)
	return scope
}

func normalizeManagementScope(scope ManagementScope) ManagementScope {
	if scope.Now.IsZero() {
		scope.Now = time.Now()
	}
	return scope
}

func effectiveNamespaceIDs(scope Scope) []string {
	if scope.NamespaceNarrowing != "" {
		return []string{scope.NamespaceNarrowing}
	}
	return uniqueNonEmpty(scope.NamespaceIDs)
}

func documentVisible(scope Scope, doc Document) bool {
	scope = normalizeScope(scope)
	if doc.OwnerScope != scope.OwnerScope || doc.OwnerID != scope.OwnerID || doc.DomainID != scope.DomainID {
		return false
	}
	if !slices.Contains(effectiveNamespaceIDs(scope), doc.NamespaceID) {
		return false
	}
	if doc.Status != DocumentActive {
		return false
	}
	if doc.EffectiveAt.After(scope.Now) {
		return false
	}
	if doc.ExpiresAt != nil && !doc.ExpiresAt.After(scope.Now) {
		return false
	}
	return true
}

func documentVisibleForManagement(scope ManagementScope, doc Document) bool {
	scope = normalizeManagementScope(scope)
	return doc.OwnerScope == scope.OwnerScope && doc.OwnerID == scope.OwnerID && doc.DomainID == scope.DomainID
}

func stripNodeText(nodes []TreeNode) []TreeNode {
	out := make([]TreeNode, len(nodes))
	copy(out, nodes)
	for i := range out {
		out[i].Text = ""
	}
	return out
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
