package master

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chef-guo/agents-hive/internal/asset"
	"go.uber.org/zap"
)

func TestPersistAssistantArtifactsStoresManifest(t *testing.T) {
	ctx := context.Background()
	local, err := asset.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	meta := newMasterAssetMetaStore()
	svc, err := asset.NewService(local, meta)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	m := &Master{assetService: svc, logger: zap.NewNop()}
	session := &SessionState{ID: "session-1", UserID: "u1"}

	raw := `前置 <artifact type="code" title="main" language="go">
package main
</artifact> 后置`
	manifestJSON := m.persistAssistantArtifacts(ctx, session, "u1", raw, "2026-05-16T00:00:00Z")
	if manifestJSON == "" {
		t.Fatal("persistAssistantArtifacts() returned empty manifest")
	}
	var manifest []AssistantArtifactManifest
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		t.Fatalf("manifest json invalid: %v", err)
	}
	if len(manifest) != 1 || manifest[0].URI == "" || manifest[0].Type != "code" || manifest[0].Language != "go" {
		t.Fatalf("manifest = %+v", manifest)
	}
	data, rec, err := svc.Download(ctx, asset.AssetURI(manifest[0].URI))
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if string(data) != "package main" || rec.Tags["source_kind"] != "agent_artifact" || rec.Tags["session_id"] != "session-1" {
		t.Fatalf("data=%q rec=%+v", data, rec)
	}
}

func TestPersistAssistantArtifactsUsesLocalOwnerWithoutAuth(t *testing.T) {
	local, err := asset.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	svc, err := asset.NewService(local, newMasterAssetMetaStore())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	m := &Master{assetService: svc, logger: zap.NewNop()}
	got := m.persistAssistantArtifacts(context.Background(), &SessionState{ID: "session-1"}, "", `<artifact title="x">body</artifact>`, "")
	if got == "" {
		t.Fatal("manifest is empty, want local-owner artifact persisted")
	}
	var manifest []AssistantArtifactManifest
	if err := json.Unmarshal([]byte(got), &manifest); err != nil {
		t.Fatalf("manifest json invalid: %v", err)
	}
	if len(manifest) != 1 {
		t.Fatalf("manifest = %+v, want one artifact", manifest)
	}
	_, rec, err := svc.Download(context.Background(), asset.AssetURI(manifest[0].URI))
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if rec.OwnerScope != "user" || rec.OwnerID != "local" {
		t.Fatalf("owner = %s/%s, want user/local", rec.OwnerScope, rec.OwnerID)
	}
}
