package kb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocMetaHonorsBoundNamespacesAndNarrowing(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	seedNamespace(t, store, now, "ns-2")
	seedDocument(t, store, now, "ns-1")
	seedDocument(t, store, now, "ns-2")
	service := NewService(store)

	result, err := service.DocMeta(context.Background(), testScope(now, "ns-1", "ns-2"), DocMetaInput{})
	require.NoError(t, err)
	require.Len(t, result.Documents, 2)

	result, err = service.DocMeta(context.Background(), testScope(now, "ns-1", "ns-2"), DocMetaInput{NamespaceID: "ns-1"})
	require.NoError(t, err)
	require.Len(t, result.Documents, 1)
	assert.Equal(t, "ns-1", result.Documents[0].NamespaceID)
	assert.Equal(t, 2, result.Documents[0].NodeCount)
	assert.Equal(t, 4, result.Documents[0].LineCount)
	assert.Equal(t, 6, result.Documents[0].PageCount)

	result, err = service.DocMeta(context.Background(), testScope(now, "ns-1"), DocMetaInput{NamespaceID: "ns-2"})
	require.NoError(t, err)
	assert.Empty(t, result.Documents)
}

func TestDocStructureRequiresBoundDocumentAndOmitsText(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	seedNamespace(t, store, now, "ns-2")
	doc := seedDocument(t, store, now, "ns-1")
	foreignDoc := seedDocument(t, store, now, "ns-2")
	service := NewService(store)

	result, err := service.DocStructure(context.Background(), testScope(now, "ns-1"), DocStructureInput{DocumentID: doc.ID})
	require.NoError(t, err)
	require.Len(t, result.Nodes, 1)
	assert.Equal(t, "Refund Policy", result.Nodes[0].Title)
	require.Len(t, result.Nodes[0].Children, 1)
	assert.Equal(t, "7-day Return", result.Nodes[0].Children[0].Title)
	assert.Equal(t, 1, result.Nodes[0].StartPage)
	assert.Equal(t, 6, result.Nodes[0].EndPage)

	_, err = service.DocStructure(context.Background(), testScope(now, "ns-1"), DocStructureInput{DocumentID: foreignDoc.ID})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSectionTextReturnsEvidenceAndRejectsUnauthorizedNodes(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	seedNamespace(t, store, now, "ns-2")
	doc := seedDocument(t, store, now, "ns-1")
	foreignDoc := seedDocument(t, store, now, "ns-2")
	service := NewService(store)

	result, err := service.SectionText(context.Background(), testScope(now, "ns-1"), testEvidenceScope(now), SectionTextInput{
		DocumentID: doc.ID,
		NodeIDs:    []string{"0001"},
	})
	require.NoError(t, err)
	require.Len(t, result.Sections, 1)
	assert.Contains(t, result.Sections[0].Text, "Conditions")
	assert.NotEmpty(t, result.Sections[0].EvidenceToken)
	require.Len(t, result.Evidence, 1)
	assert.Equal(t, result.Sections[0].EvidenceToken, result.Evidence[0].Token)

	_, err = service.SectionText(context.Background(), testScope(now, "ns-1"), testEvidenceScope(now), SectionTextInput{
		DocumentID: foreignDoc.ID,
		NodeIDs:    []string{"0000"},
	})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSectionTextSupportsPageRanges(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	doc := seedDocument(t, store, now, "ns-1")
	require.NoError(t, store.SaveNodeAssets(context.Background(), []NodeAsset{{
		ID:          "asset-1",
		DocumentID:  doc.ID,
		NamespaceID: doc.NamespaceID,
		DomainID:    doc.DomainID,
		OwnerScope:  doc.OwnerScope,
		OwnerID:     doc.OwnerID,
		NodeID:      "0001",
		Line:        10,
		Page:        5,
		AssetURI:    "asset://kb/ns-1/doc-1/page-5.png",
		MimeType:    "image/png",
		AltText:     "page 5 chart",
		CreatedAt:   now,
	}}))
	service := NewService(store)

	result, err := service.SectionText(context.Background(), testScope(now, "ns-1"), testEvidenceScope(now), SectionTextInput{
		DocumentID: doc.ID,
		PageRanges: []string{
			"5-6",
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Sections, 1)
	assert.Equal(t, "0001", result.Sections[0].NodeID)
	assert.Equal(t, 5, result.Sections[0].StartPage)
	assert.Equal(t, 6, result.Sections[0].EndPage)
	assert.NotEmpty(t, result.Sections[0].EvidenceToken)
	require.Len(t, result.AssetRefs, 1)
	assert.Equal(t, "0001", result.AssetRefs[0].NodeID)
	assert.Equal(t, 5, result.AssetRefs[0].Page)
	assert.Equal(t, "asset://kb/ns-1/doc-1/page-5.png", result.AssetRefs[0].AssetURI)
}

func TestSectionTextPageRangesReturnTightAnchoredText(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-tight")
	doc := Document{
		ID:          "doc-tight",
		NamespaceID: "ns-tight",
		DomainID:    "domain-1",
		OwnerScope:  OwnerScopeTenant,
		OwnerID:     "tenant-1",
		Title:       "Anchored PDF",
		ContentHash: "hash-tight",
		Version:     "v1",
		Status:      DocumentActive,
		EffectiveAt: now.Add(-time.Hour),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	require.NoError(t, store.SaveDocument(context.Background(), doc, []TreeNode{{
		ID:          "0000",
		DocumentID:  doc.ID,
		NamespaceID: doc.NamespaceID,
		DomainID:    doc.DomainID,
		OwnerScope:  doc.OwnerScope,
		OwnerID:     doc.OwnerID,
		NodePath:    "1",
		Title:       "Long Section",
		Text:        "# Long Section\n<physical_index_4>\npage four\n<physical_index_5>\npage five\n<physical_index_6>\npage six",
		StartLine:   1,
		EndLine:     7,
		StartPage:   4,
		EndPage:     6,
		ContentHash: "node-tight",
		CreatedAt:   now,
	}}))
	service := NewService(store)

	result, err := service.SectionText(context.Background(), testScope(now, "ns-tight"), testEvidenceScope(now), SectionTextInput{
		DocumentID: doc.ID,
		PageRanges: []string{
			"5",
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Sections, 1)
	assert.Equal(t, "<physical_index_5>\npage five", result.Sections[0].Text)
	assert.Equal(t, 4, result.Sections[0].StartLine)
	assert.Equal(t, 5, result.Sections[0].EndLine)
	assert.Equal(t, 5, result.Sections[0].StartPage)
	assert.Equal(t, 5, result.Sections[0].EndPage)
}

func TestSectionTextPageRangesCannotBypassNodeLimit(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	doc := seedDocument(t, store, now, "ns-1")
	service := NewService(store, WithSectionLimits(1, 64*1024))

	_, err := service.SectionText(context.Background(), testScope(now, "ns-1"), testEvidenceScope(now), SectionTextInput{
		DocumentID: doc.ID,
		PageRanges: []string{
			"1-6",
		},
	})
	require.ErrorIs(t, err, ErrOutputTooLarge)
}

func TestSectionTextPageRangesCannotBypassLimitWithCommaList(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	doc := seedDocument(t, store, now, "ns-1")
	service := NewService(store, WithSectionLimits(1, 64*1024))

	_, err := service.SectionText(context.Background(), testScope(now, "ns-1"), testEvidenceScope(now), SectionTextInput{
		DocumentID: doc.ID,
		PageRanges: []string{
			"5,6",
		},
	})
	require.ErrorIs(t, err, ErrOutputTooLarge)
}

func TestSectionTextPageRangesFilterPageScopedAssets(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-tight")
	doc := Document{
		ID:          "doc-assets",
		NamespaceID: "ns-tight",
		DomainID:    "domain-1",
		OwnerScope:  OwnerScopeTenant,
		OwnerID:     "tenant-1",
		Title:       "Anchored Assets",
		ContentHash: "hash-assets",
		Version:     "v1",
		Status:      DocumentActive,
		EffectiveAt: now.Add(-time.Hour),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	require.NoError(t, store.SaveDocument(context.Background(), doc, []TreeNode{{
		ID:          "0000",
		DocumentID:  doc.ID,
		NamespaceID: doc.NamespaceID,
		DomainID:    doc.DomainID,
		OwnerScope:  doc.OwnerScope,
		OwnerID:     doc.OwnerID,
		NodePath:    "1",
		Title:       "Long Section",
		Text:        "# Long Section\n<physical_index_5>\n![p5](asset://kb/p5.png)\n<physical_index_6>\n![p6](asset://kb/p6.png)",
		StartLine:   1,
		EndLine:     5,
		StartPage:   5,
		EndPage:     6,
		ContentHash: "node-assets",
		CreatedAt:   now,
	}}))
	require.NoError(t, store.SaveNodeAssets(context.Background(), []NodeAsset{
		{
			ID:          "asset-p5",
			DocumentID:  doc.ID,
			NamespaceID: doc.NamespaceID,
			DomainID:    doc.DomainID,
			OwnerScope:  doc.OwnerScope,
			OwnerID:     doc.OwnerID,
			NodeID:      "0000",
			Page:        5,
			AssetURI:    "asset://kb/p5.png",
			MimeType:    "image/png",
			CreatedAt:   now,
		},
		{
			ID:          "asset-p6",
			DocumentID:  doc.ID,
			NamespaceID: doc.NamespaceID,
			DomainID:    doc.DomainID,
			OwnerScope:  doc.OwnerScope,
			OwnerID:     doc.OwnerID,
			NodeID:      "0000",
			Page:        6,
			AssetURI:    "asset://kb/p6.png",
			MimeType:    "image/png",
			CreatedAt:   now,
		},
		{
			ID:          "asset-legacy",
			DocumentID:  doc.ID,
			NamespaceID: doc.NamespaceID,
			DomainID:    doc.DomainID,
			OwnerScope:  doc.OwnerScope,
			OwnerID:     doc.OwnerID,
			NodeID:      "0000",
			AssetURI:    "asset://kb/legacy.png",
			MimeType:    "image/png",
			CreatedAt:   now,
		},
	}))
	service := NewService(store)

	result, err := service.SectionText(context.Background(), testScope(now, "ns-tight"), testEvidenceScope(now), SectionTextInput{
		DocumentID: doc.ID,
		PageRanges: []string{
			"5",
		},
	})
	require.NoError(t, err)
	require.Len(t, result.AssetRefs, 1)
	assert.Equal(t, "asset://kb/p5.png", result.AssetRefs[0].AssetURI)
}

func TestSectionTextRejectsMixedNodeIDsAndPageRanges(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	doc := seedDocument(t, store, now, "ns-1")
	service := NewService(store)

	_, err := service.SectionText(context.Background(), testScope(now, "ns-1"), testEvidenceScope(now), SectionTextInput{
		DocumentID: doc.ID,
		NodeIDs:    []string{"0001"},
		PageRanges: []string{"5"},
	})
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestParsePageRanges(t *testing.T) {
	ranges, err := ParsePageRanges([]string{"5-7, 12", "7-8"})
	require.NoError(t, err)
	assert.Equal(t, []PageRange{{Start: 5, End: 7}, {Start: 7, End: 8}, {Start: 12, End: 12}}, ranges)

	_, err = ParsePageRanges([]string{"8-3"})
	require.Error(t, err)
}

func TestSectionTextLimitsNodeCountAndBytes(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	doc := seedDocument(t, store, now, "ns-1")
	service := NewService(store, WithSectionLimits(1, 4))

	_, err := service.SectionText(context.Background(), testScope(now, "ns-1"), testEvidenceScope(now), SectionTextInput{
		DocumentID: doc.ID,
		NodeIDs:    []string{"0000", "0001"},
	})
	require.ErrorIs(t, err, ErrOutputTooLarge)

	_, err = service.SectionText(context.Background(), testScope(now, "ns-1"), testEvidenceScope(now), SectionTextInput{
		DocumentID: doc.ID,
		NodeIDs:    []string{"0000"},
	})
	require.ErrorIs(t, err, ErrOutputTooLarge)
}

func TestResolveBindingBuildsScopeWithoutToolArgAuthorization(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	seedBinding(t, store, now, "b-agent", "ns-1", BindingTypeAgent, "agent-1")
	service := NewService(store)
	namespaces, err := service.ResolveBinding(context.Background(), BindingResolveInput{
		DomainID:   "domain-1",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		AgentID:    "agent-1",
		Now:        now,
	})
	require.NoError(t, err)
	scope := ScopeFromBindingInput(BindingResolveInput{
		DomainID:   "domain-1",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		Now:        now,
	}, namespaces, "ns-2")
	require.ErrorIs(t, ValidateScope(scope), ErrNamespaceNotBound)
}

func TestResolveBindingDoesNotFallbackAcrossDomains(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-generic")
	seedBinding(t, store, now, "b-generic", "ns-generic", BindingTypeSession, "session-1")
	service := NewService(store)

	_, err := service.ResolveBinding(context.Background(), BindingResolveInput{
		DomainID:   "support",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		SessionID:  "session-1",
		Now:        now,
	})

	require.ErrorIs(t, err, ErrNoKBBinding)
}
