package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chef-guo/agents-hive/internal/asset"
	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/kb"
	"go.uber.org/zap"
)

func TestAssetAccessResolverAllowsKBDocumentImageOnlyWithManagementOrBinding(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	kbStore := kb.NewMemoryStore()
	namespace := kb.Namespace{
		ID:            "ns-1",
		Name:          "FAQ",
		DomainID:      "support",
		OwnerScope:    kb.OwnerScopeUser,
		OwnerID:       "user-1",
		IndexStrategy: "markdown_tree",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := kbStore.SaveNamespace(ctx, namespace); err != nil {
		t.Fatalf("SaveNamespace() error = %v", err)
	}
	kbService := kb.NewService(kbStore, kb.WithAssetUploader(&bootstrapFakeKBUploader{}))
	doc, err := kbService.IngestMarkdownWithAssets(ctx, kb.Scope{
		DomainID:     "support",
		OwnerScope:   kb.OwnerScopeUser,
		OwnerID:      "user-1",
		NamespaceIDs: []string{"ns-1"},
		Now:          now,
	}, kb.IngestMarkdownWithAssetsInput{
		IngestMarkdownInput: kb.IngestMarkdownInput{
			NamespaceID: "ns-1",
			Title:       "FAQ",
			Version:     "v1",
			Content:     "# FAQ\n![diagram](diagram.png)",
		},
		Assets: map[string]kb.MarkdownAsset{
			"diagram.png": {Filename: "diagram.png", MimeType: "image/png", Data: []byte("png")},
		},
	})
	if err != nil {
		t.Fatalf("IngestMarkdownWithAssets() error = %v", err)
	}
	assets, err := kbService.ListNodeAssets(ctx, kb.ManagementScope{
		DomainID:   "support",
		OwnerScope: kb.OwnerScopeUser,
		OwnerID:    "user-1",
		Now:        now,
	}, doc.ID, nil)
	if err != nil || len(assets) != 1 {
		t.Fatalf("ListNodeAssets() assets=%#v err=%v", assets, err)
	}
	rec := &asset.AssetRecord{
		ID:         "asset-1",
		Key:        "kb/user/user-1/ns-1/" + doc.ID + "/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png",
		Namespace:  "kb/user/user-1/ns-1/" + doc.ID,
		OwnerScope: "user",
		OwnerID:    "user-1",
		Tags: map[string]string{
			"source_kind":     "kb_document_image",
			"domain_id":       "support",
			"kb_namespace_id": "ns-1",
			"kb_document_id":  doc.ID,
		},
	}
	resolver := initAssetAccessResolver(zap.NewNop(), kbService)
	rc := asset.ResolveContext{
		OwnerScope: "user",
		OwnerID:    "user-1",
		UserID:     "user-1",
		DomainID:   "support",
		Purpose:    "kb_section_text",
	}

	if err := resolver.CanResolveAsset(ctx, rec, rc); !errors.Is(err, asset.ErrAccessDenied) {
		t.Fatalf("unbound KB asset resolve error = %v, want access denied", err)
	}

	management := rc
	management.Purpose = "kb_management"
	if err := resolver.CanResolveAsset(ctx, rec, management); err != nil {
		t.Fatalf("management KB asset resolve error = %v", err)
	}

	if _, err := kbService.CreateBinding(ctx, kb.ManagementScope{
		DomainID:   "support",
		OwnerScope: kb.OwnerScopeUser,
		OwnerID:    "user-1",
		Now:        now,
	}, kb.CreateBindingInput{
		NamespaceID:   "ns-1",
		DomainID:      "support",
		BindingType:   kb.BindingTypeUser,
		BindingTarget: "user-1",
	}); err != nil {
		t.Fatalf("CreateBinding() error = %v", err)
	}
	if err := resolver.CanResolveAsset(ctx, rec, rc); err != nil {
		t.Fatalf("bound KB asset resolve error = %v", err)
	}

	wrongOwner := management
	wrongOwner.OwnerID = "attacker"
	if err := resolver.CanResolveAsset(ctx, rec, wrongOwner); !errors.Is(err, asset.ErrAccessDenied) {
		t.Fatalf("wrong owner KB management resolve error = %v, want access denied", err)
	}
}

func TestNewObjectStoreRejectsIncompleteMinIOConfig(t *testing.T) {
	cfg := config.NormalizeAssetConfig(config.AssetConfig{
		Provider: "minio",
		MinIO: config.AssetS3Config{
			Endpoint: "127.0.0.1:9000",
			Bucket:   "",
		},
	})
	cfg.MinIO.Bucket = ""

	_, err := newObjectStore(context.Background(), cfg)
	if !errors.Is(err, asset.ErrInvalidUploadOpts) {
		t.Fatalf("newObjectStore() error = %v, want ErrInvalidUploadOpts", err)
	}
}

func TestNewObjectStoreRejectsUnknownProvider(t *testing.T) {
	_, err := newObjectStore(context.Background(), config.AssetConfig{Provider: "unknown"})
	if !errors.Is(err, asset.ErrStoreUnavailable) {
		t.Fatalf("newObjectStore() error = %v, want ErrStoreUnavailable", err)
	}
}

func TestAssetAccessResolverRequiresSessionForAgentArtifacts(t *testing.T) {
	resolver := initAssetAccessResolver(zap.NewNop(), nil)
	rec := &asset.AssetRecord{
		ID:         "artifact-1",
		OwnerScope: "user",
		OwnerID:    "user-1",
		Tags: map[string]string{
			"source_kind": "agent_artifact",
			"session_id":  "session-1",
		},
	}
	rc := asset.ResolveContext{
		OwnerScope: "user",
		OwnerID:    "user-1",
		UserID:     "user-1",
		Purpose:    "agent_artifact",
	}

	if err := resolver.CanResolveAsset(context.Background(), rec, rc); !errors.Is(err, asset.ErrAccessDenied) {
		t.Fatalf("agent artifact without session error = %v, want access denied", err)
	}

	rc.SessionID = "other-session"
	if err := resolver.CanResolveAsset(context.Background(), rec, rc); !errors.Is(err, asset.ErrAccessDenied) {
		t.Fatalf("agent artifact wrong session error = %v, want access denied", err)
	}

	rc.SessionID = "session-1"
	if err := resolver.CanResolveAsset(context.Background(), rec, rc); err != nil {
		t.Fatalf("agent artifact matching session error = %v", err)
	}
}

func TestAssetAccessResolverRequiresSessionForChatAttachments(t *testing.T) {
	resolver := initAssetAccessResolver(zap.NewNop(), nil)
	rec := &asset.AssetRecord{
		ID:         "attachment-1",
		OwnerScope: "user",
		OwnerID:    "user-1",
		Tags: map[string]string{
			"source_kind": "chat_attachment",
			"session_id":  "session-1",
		},
	}
	rc := asset.ResolveContext{
		OwnerScope: "user",
		OwnerID:    "user-1",
		UserID:     "user-1",
		Purpose:    "chat_attachment",
	}

	if err := resolver.CanResolveAsset(context.Background(), rec, rc); !errors.Is(err, asset.ErrAccessDenied) {
		t.Fatalf("chat attachment without session error = %v, want access denied", err)
	}

	rc.SessionID = "other-session"
	if err := resolver.CanResolveAsset(context.Background(), rec, rc); !errors.Is(err, asset.ErrAccessDenied) {
		t.Fatalf("chat attachment wrong session error = %v, want access denied", err)
	}

	rc.SessionID = "session-1"
	if err := resolver.CanResolveAsset(context.Background(), rec, rc); err != nil {
		t.Fatalf("chat attachment matching session error = %v", err)
	}
}

type bootstrapFakeKBUploader struct{}

func (bootstrapFakeKBUploader) Upload(ctx context.Context, data []byte, opts kb.AssetUploadOptions) (string, string, error) {
	return "asset://" + opts.Namespace + "/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png", "hash", nil
}
