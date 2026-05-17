package kb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

func TestIngestMarkdownCreatesActiveDocumentAndNodes(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	service := NewService(store, WithSummaryGenerator(&fakeSummary{}), WithTokenCounter(fakeCounter{}))

	doc, err := service.IngestMarkdown(context.Background(), testScope(now, "ns-1"), IngestMarkdownInput{
		NamespaceID: "ns-1",
		Title:       "Refund Policy",
		Version:     "v1",
		Content:     "# Refund Policy\nIntro\n## 7-day Return\nConditions",
	})
	require.NoError(t, err)
	assert.Equal(t, DocumentActive, doc.Status)
	assert.NotEmpty(t, doc.ContentHash)

	nodes, err := store.GetStructure(context.Background(), testScope(now, "ns-1"), doc.ID)
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.Equal(t, "0000", nodes[0].ID)
	assert.Empty(t, nodes[0].Text)
}

func TestIngestMarkdownDuplicateReturnsExistingDocument(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	service := NewService(store, WithTokenCounter(fakeCounter{}))
	input := IngestMarkdownInput{
		NamespaceID: "ns-1",
		Title:       "Refund Policy",
		Version:     "v1",
		Content:     "# Refund Policy\nIntro",
	}
	first, err := service.IngestMarkdown(context.Background(), testScope(now, "ns-1"), input)
	require.NoError(t, err)
	second, err := service.IngestMarkdown(context.Background(), testScope(now, "ns-1"), input)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
}

func TestIngestMarkdownNormalizesLineEndingsForIdentityAndLineRanges(t *testing.T) {
	now := time.Now()
	base := "# T\n\n## A\nbody\n## B\nchild"
	crOnly := "# T\r\r## A\rbody\r## B\rchild"

	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	service := NewService(store, WithTokenCounter(fakeCounter{}))

	first, err := service.IngestMarkdown(context.Background(), testScope(now, "ns-1"), IngestMarkdownInput{
		NamespaceID: "ns-1",
		Title:       "LF",
		Version:     "v1",
		Content:     base,
	})
	require.NoError(t, err)
	second, err := service.IngestMarkdown(context.Background(), testScope(now, "ns-1"), IngestMarkdownInput{
		NamespaceID: "ns-1",
		Title:       "CR",
		Version:     "v1",
		Content:     crOnly,
	})
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, first.ContentHash, second.ContentHash)

	nodes, err := store.GetStructure(context.Background(), testScope(now, "ns-1"), first.ID)
	require.NoError(t, err)
	require.Len(t, nodes, 3)
	assert.Equal(t, 1, nodes[0].StartLine)
	assert.Equal(t, 2, nodes[0].EndLine)
	assert.Equal(t, 3, nodes[1].StartLine)
	assert.Equal(t, 4, nodes[1].EndLine)
	assert.Equal(t, 5, nodes[2].StartLine)
	assert.Equal(t, 6, nodes[2].EndLine)
}

func TestIngestMarkdownFailsBeforeActiveDocumentOnSummaryError(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	namespace := seedNamespace(t, store, now, "ns-1")
	namespace.SummaryTokenThreshold = 1
	require.NoError(t, store.SaveNamespace(context.Background(), namespace))
	recorder := &recordingQualityRecorder{}
	service := NewService(store, WithSummaryGenerator(&fakeSummary{err: errors.New("summary failed")}), WithTokenCounter(fakeCounter{}), WithQualityRecorder(recorder))

	_, err := service.IngestMarkdown(context.Background(), testScope(now, "ns-1"), IngestMarkdownInput{
		NamespaceID: "ns-1",
		Title:       "Refund Policy",
		Version:     "v1",
		Content:     "# Refund Policy\nIntro",
	})
	require.ErrorContains(t, err, "summary failed")
	docs, listErr := store.ListDocuments(context.Background(), testScope(now, "ns-1"), DocumentQuery{})
	require.NoError(t, listErr)
	assert.Empty(t, docs)
	require.Len(t, recorder.events, 1)
	assert.Equal(t, "kb.summary", recorder.events[0].Attributes["operation"])
	assert.Equal(t, "ns-1", recorder.events[0].Attributes["namespace_id"])
	assert.Equal(t, agentquality.FailureKBSummary, recorder.events[0].FailureType)
}

func TestIngestMarkdownRejectsEmptyNoHeadingAndUnboundNamespace(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	service := NewService(store)

	_, err := service.IngestMarkdown(context.Background(), testScope(now, "ns-1"), IngestMarkdownInput{
		NamespaceID: "ns-1",
		Title:       "T",
		Version:     "v1",
		Content:     "",
	})
	require.ErrorIs(t, err, ErrEmptyDocument)

	_, err = service.IngestMarkdown(context.Background(), testScope(now, "ns-1"), IngestMarkdownInput{
		NamespaceID: "ns-1",
		Title:       "T",
		Version:     "v1",
		Content:     "plain text",
	})
	require.ErrorIs(t, err, ErrNoHeading)

	_, err = service.IngestMarkdown(context.Background(), testScope(now, "ns-1"), IngestMarkdownInput{
		NamespaceID: "ns-2",
		Title:       "T",
		Version:     "v1",
		Content:     "# T\ntext",
	})
	require.ErrorIs(t, err, ErrNamespaceNotBound)
}

func TestIngestMarkdownRejectsImageReferencesWithoutAssetIngest(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	service := NewService(store)

	_, err := service.IngestMarkdown(context.Background(), testScope(now, "ns-1"), IngestMarkdownInput{
		NamespaceID: "ns-1",
		Title:       "T",
		Version:     "v1",
		Content:     "# T\n![diagram](./images/a.png)",
	})
	require.ErrorIs(t, err, ErrUnsupportedAsset)

	_, err = service.IngestMarkdown(context.Background(), testScope(now, "ns-1"), IngestMarkdownInput{
		NamespaceID: "ns-1",
		Title:       "T",
		Version:     "v1",
		Content:     "# T\n```markdown\n![diagram](./images/a.png)\n```\ntext",
	})
	require.NoError(t, err)
}

func TestIngestMarkdownWithAssetsUploadsAndRecordsNodeAsset(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	uploader := &fakeAssetUploader{}
	service := NewService(store, WithAssetUploader(uploader))

	doc, err := service.IngestMarkdownWithAssets(context.Background(), testScope(now, "ns-1"), IngestMarkdownWithAssetsInput{
		IngestMarkdownInput: IngestMarkdownInput{
			NamespaceID: "ns-1",
			Title:       "T",
			Version:     "v1",
			Content:     "# T\n![diagram](./images/a.png)\ntext",
		},
		Assets: map[string]MarkdownAsset{
			"a.png": {Filename: "a.png", MimeType: "image/png", Data: []byte("png-data")},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, uploader.lastOpts.Tags["source_kind"])

	assets, err := store.ListNodeAssets(context.Background(), ManagementScope{
		DomainID:   "domain-1",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		Now:        now,
	}, doc.ID, nil)
	require.NoError(t, err)
	require.Len(t, assets, 1)
	assert.Equal(t, "asset://kb/tenant/tenant-1/ns-1/"+doc.ID+"/hash.png", assets[0].AssetURI)
	assert.Equal(t, 2, assets[0].Line)
	assert.Zero(t, assets[0].Page)

	nodes, _, err := store.GetStructureForManagement(context.Background(), ManagementScope{
		DomainID:   "domain-1",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		Now:        now,
	}, doc.ID, true)
	require.NoError(t, err)
	require.Contains(t, nodes[0].Text, "asset://")
}

func TestIngestMarkdownWithAssetsRecordsPageForAnchoredAsset(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	service := NewService(store, WithAssetUploader(&fakeAssetUploader{}))

	doc, err := service.IngestMarkdownWithAssets(context.Background(), testScope(now, "ns-1"), IngestMarkdownWithAssetsInput{
		IngestMarkdownInput: IngestMarkdownInput{
			NamespaceID: "ns-1",
			Title:       "T",
			Version:     "v1",
			Content:     "# T\n<physical_index_5>\n![diagram](./images/a.png)\ntext",
		},
		Assets: map[string]MarkdownAsset{
			"a.png": {Filename: "a.png", MimeType: "image/png", Data: []byte("png-data")},
		},
	})
	require.NoError(t, err)

	assets, err := store.ListNodeAssets(context.Background(), ManagementScope{
		DomainID:   "domain-1",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		Now:        now,
	}, doc.ID, nil)
	require.NoError(t, err)
	require.Len(t, assets, 1)
	assert.Equal(t, 3, assets[0].Line)
	assert.Equal(t, 5, assets[0].Page)
}

func TestIngestMarkdownWithAssetsNormalizesLineEndingsForAssetLineBinding(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	service := NewService(store, WithAssetUploader(&fakeAssetUploader{}))

	doc, err := service.IngestMarkdownWithAssets(context.Background(), testScope(now, "ns-1"), IngestMarkdownWithAssetsInput{
		IngestMarkdownInput: IngestMarkdownInput{
			NamespaceID: "ns-1",
			Title:       "T",
			Version:     "v1",
			Content:     "# T\r<physical_index_5>\r![diagram](./images/a.png)\rtext",
		},
		Assets: map[string]MarkdownAsset{
			"a.png": {Filename: "a.png", MimeType: "image/png", Data: []byte("png-data")},
		},
	})
	require.NoError(t, err)

	assets, err := store.ListNodeAssets(context.Background(), ManagementScope{
		DomainID:   "domain-1",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		Now:        now,
	}, doc.ID, nil)
	require.NoError(t, err)
	require.Len(t, assets, 1)
	assert.Equal(t, 3, assets[0].Line)
	assert.Equal(t, 5, assets[0].Page)
}

func TestIngestMarkdownWithAssetsIncludesAssetBytesInDocumentIdentity(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	service := NewService(store, WithAssetUploader(&fakeAssetUploader{}))
	input := IngestMarkdownInput{
		NamespaceID: "ns-1",
		Title:       "T",
		Version:     "v1",
		Content:     "# T\n![diagram](./images/a.png)\ntext",
	}

	first, err := service.IngestMarkdownWithAssets(context.Background(), testScope(now, "ns-1"), IngestMarkdownWithAssetsInput{
		IngestMarkdownInput: input,
		Assets: map[string]MarkdownAsset{
			"a.png": {Filename: "a.png", MimeType: "image/png", Data: []byte("png-data-v1")},
		},
	})
	require.NoError(t, err)
	second, err := service.IngestMarkdownWithAssets(context.Background(), testScope(now, "ns-1"), IngestMarkdownWithAssetsInput{
		IngestMarkdownInput: input,
		Assets: map[string]MarkdownAsset{
			"a.png": {Filename: "a.png", MimeType: "image/png", Data: []byte("png-data-v2")},
		},
	})
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID)
}

func TestIngestMarkdownWithAssetsClearsStaleNodeAssetsOnReingest(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	service := NewService(store, WithAssetUploader(&fakeAssetUploader{}))
	input := IngestMarkdownInput{
		NamespaceID: "ns-1",
		Title:       "T",
		Version:     "v1",
		Content:     "# T\n![diagram](./images/a.png)\ntext",
	}
	doc, err := service.IngestMarkdownWithAssets(context.Background(), testScope(now, "ns-1"), IngestMarkdownWithAssetsInput{
		IngestMarkdownInput: input,
		Assets: map[string]MarkdownAsset{
			"a.png": {Filename: "a.png", MimeType: "image/png", Data: []byte("png-data")},
		},
	})
	require.NoError(t, err)
	require.NoError(t, store.SaveNodeAssets(context.Background(), []NodeAsset{{
		ID:          "stale",
		DocumentID:  doc.ID,
		NamespaceID: doc.NamespaceID,
		DomainID:    doc.DomainID,
		OwnerScope:  doc.OwnerScope,
		OwnerID:     doc.OwnerID,
		NodeID:      "0000",
		AssetURI:    "asset://kb/stale.png",
		MimeType:    "image/png",
		CreatedAt:   now,
	}}))

	_, err = service.IngestMarkdownWithAssets(context.Background(), testScope(now, "ns-1"), IngestMarkdownWithAssetsInput{
		IngestMarkdownInput: input,
		Assets: map[string]MarkdownAsset{
			"a.png": {Filename: "a.png", MimeType: "image/png", Data: []byte("png-data")},
		},
	})
	require.NoError(t, err)
	assets, err := store.ListNodeAssets(context.Background(), ManagementScope{
		DomainID:   "domain-1",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		Now:        now,
	}, doc.ID, nil)
	require.NoError(t, err)
	require.Len(t, assets, 1)
	assert.NotEqual(t, "asset://kb/stale.png", assets[0].AssetURI)
}

func TestAssignAssetsToNodesPrefersDeepestMatchingNode(t *testing.T) {
	now := time.Now()
	childParent := "root"
	doc := Document{
		ID:          "doc-1",
		NamespaceID: "ns-1",
		DomainID:    "domain-1",
		OwnerScope:  OwnerScopeTenant,
		OwnerID:     "tenant-1",
	}
	nodes := []TreeNode{{
		ID:        "root",
		StartLine: 1,
		EndLine:   10,
		Level:     1,
	}, {
		ID:           "child",
		ParentNodeID: &childParent,
		StartLine:    4,
		EndLine:      5,
		Level:        2,
	}}

	assets := assignAssetsToNodes(doc, nodes, []rewrittenAssetRef{{
		URI:         "asset://kb/img.png",
		ContentHash: "hash",
		MimeType:    "image/png",
		AltText:     "diagram",
		Line:        4,
		Page:        2,
	}})

	require.Len(t, assets, 1)
	assert.Equal(t, "child", assets[0].NodeID)
	assert.Equal(t, 4, assets[0].Line)
	assert.Equal(t, 2, assets[0].Page)
	assert.WithinDuration(t, now, assets[0].CreatedAt, time.Second)
}

func TestIngestMarkdownWithAssetsRejectsMissingImage(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	service := NewService(store, WithAssetUploader(&fakeAssetUploader{}))

	_, err := service.IngestMarkdownWithAssets(context.Background(), testScope(now, "ns-1"), IngestMarkdownWithAssetsInput{
		IngestMarkdownInput: IngestMarkdownInput{
			NamespaceID: "ns-1",
			Title:       "T",
			Version:     "v1",
			Content:     "# T\n![diagram](./missing.png)",
		},
	})
	require.ErrorIs(t, err, ErrUnsupportedAsset)
}

type fakeAssetUploader struct {
	lastOpts AssetUploadOptions
}

func (f *fakeAssetUploader) Upload(ctx context.Context, data []byte, opts AssetUploadOptions) (string, string, error) {
	f.lastOpts = opts
	return "asset://" + opts.Namespace + "/hash.png", "hash", nil
}
