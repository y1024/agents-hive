package asset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestServiceUploadDedupeIsOwnerScoped(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	meta := newMemoryMetaStore()
	svc, err := NewService(store, meta)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	data := []byte("same image")
	opts := UploadOpts{
		Namespace:  "im/user/session-1",
		Filename:   "photo.png",
		MimeType:   "image/png",
		OwnerScope: "user",
		OwnerID:    "u1",
		Tags:       map[string]string{"source_kind": "chat_attachment"},
	}
	uri1, err := svc.Upload(ctx, data, opts)
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	uri2, err := svc.Upload(ctx, data, opts)
	if err != nil {
		t.Fatalf("second Upload() error = %v", err)
	}
	if uri1 != uri2 {
		t.Fatalf("same owner upload URI mismatch: %s != %s", uri1, uri2)
	}

	opts.OwnerID = "u2"
	uri3, err := svc.Upload(ctx, data, opts)
	if err != nil {
		t.Fatalf("different owner Upload() error = %v", err)
	}
	if uri3 != uri1 {
		t.Fatalf("object URI should remain content-addressed, got %s want %s", uri3, uri1)
	}
	records, err := meta.ListByNamespace(ctx, opts.Namespace)
	if err != nil {
		t.Fatalf("ListByNamespace() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records len = %d, want 2 owner-scoped metadata rows", len(records))
	}
}

func TestServiceResolveRequiresOwnerAndResolver(t *testing.T) {
	ctx := context.Background()
	local, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	store := &captureSignedURLStore{ObjectStore: local}
	svc, err := NewService(store, newMemoryMetaStore())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	uri, err := svc.Upload(ctx, []byte("artifact"), UploadOpts{
		Namespace:  "agent/user/u1/session-1",
		Filename:   "report.txt",
		MimeType:   "text/plain",
		OwnerScope: "user",
		OwnerID:    "u1",
		Tags:       map[string]string{"source_kind": "agent_artifact"},
	})
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	_, _, err = svc.ResolveAsset(ctx, uri, ResolveContext{OwnerScope: "user", OwnerID: "u2"}, AllowAllResolver{}, time.Minute)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveAsset() wrong owner error = %v, want ErrNotFound", err)
	}

	blocked := AccessResolverFunc(func(context.Context, *AssetRecord, ResolveContext) error {
		return ErrAccessDenied
	})
	_, _, err = svc.ResolveAsset(ctx, uri, ResolveContext{OwnerScope: "user", OwnerID: "u1"}, blocked, time.Minute)
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ResolveAsset() resolver error = %v, want ErrAccessDenied", err)
	}

	wantTTL := 2 * time.Minute
	url, rec, err := svc.ResolveAsset(ctx, uri, ResolveContext{OwnerScope: "user", OwnerID: "u1", Purpose: "agent_artifact"}, AllowAllResolver{}, wantTTL)
	if err != nil {
		t.Fatalf("ResolveAsset() error = %v", err)
	}
	if !strings.HasPrefix(url, "local://asset/") || rec.OwnerID != "u1" || rec.Tags["source_kind"] != "agent_artifact" {
		t.Fatalf("ResolveAsset() url=%q rec=%#v", url, rec)
	}
	if store.lastTTL != wantTTL {
		t.Fatalf("SignedURL ttl = %v, want %v", store.lastTTL, wantTTL)
	}
}

func TestUploadRejectsInvalidInputs(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	svc, err := NewService(store, newMemoryMetaStore())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	_, err = svc.Upload(context.Background(), []byte("x"), UploadOpts{
		Namespace:  "../bad",
		OwnerScope: "user",
		OwnerID:    "u1",
	})
	if !errors.Is(err, ErrInvalidNamespace) {
		t.Fatalf("Upload() invalid namespace error = %v, want ErrInvalidNamespace", err)
	}
	_, err = svc.Upload(context.Background(), []byte("x"), UploadOpts{
		Namespace: "ok/ns",
	})
	if !errors.Is(err, ErrInvalidUploadOpts) {
		t.Fatalf("Upload() missing owner error = %v, want ErrInvalidUploadOpts", err)
	}
}

func TestServiceUploadRejectsOversizedAsset(t *testing.T) {
	oldMaxUploadBytes := maxUploadBytes
	maxUploadBytes = 8
	t.Cleanup(func() { maxUploadBytes = oldMaxUploadBytes })

	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	svc, err := NewService(store, newMemoryMetaStore())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	_, err = svc.Upload(context.Background(), make([]byte, maxUploadBytes+1), UploadOpts{
		Namespace:  "oversized/test",
		OwnerScope: "user",
		OwnerID:    "u1",
	})
	if !errors.Is(err, ErrInvalidUploadOpts) {
		t.Fatalf("Upload() oversized error = %v, want ErrInvalidUploadOpts", err)
	}
	keys, listErr := store.ListKeys(context.Background(), "")
	if listErr != nil {
		t.Fatalf("ListKeys() error = %v", listErr)
	}
	if len(keys) != 0 {
		t.Fatalf("oversized upload should not write object, got keys=%v", keys)
	}
}

func TestServiceUploadDeletesObjectWhenMetaSaveFails(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	meta := &failingSaveMetaStore{memoryMetaStore: newMemoryMetaStore(), err: fmt.Errorf("pg down")}
	svc, err := NewService(store, meta)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	_, err = svc.Upload(ctx, []byte("cleanup me"), UploadOpts{
		Namespace:  "cleanup/test",
		Filename:   "note.txt",
		MimeType:   "text/plain",
		OwnerScope: "user",
		OwnerID:    "u1",
	})
	if !errors.Is(err, meta.err) {
		t.Fatalf("Upload() error = %v, want %v", err, meta.err)
	}
	keys, listErr := store.ListKeys(ctx, "")
	if listErr != nil {
		t.Fatalf("ListKeys() error = %v", listErr)
	}
	if len(keys) != 0 {
		t.Fatalf("metadata save failure should delete written object, got keys=%v", keys)
	}
}

func TestServiceUploadDoesNotDeleteExistingObjectWhenMetaSaveFails(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	svc, err := NewService(store, newMemoryMetaStore())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	data := []byte("shared object")
	uri, err := svc.Upload(ctx, data, UploadOpts{
		Namespace:  "cleanup/shared",
		Filename:   "note.txt",
		MimeType:   "text/plain",
		OwnerScope: "user",
		OwnerID:    "u1",
	})
	if err != nil {
		t.Fatalf("Upload(existing) error = %v", err)
	}
	meta := &failingSaveMetaStore{memoryMetaStore: newMemoryMetaStore(), err: fmt.Errorf("pg down")}
	failingSvc, err := NewService(store, meta)
	if err != nil {
		t.Fatalf("NewService(failing) error = %v", err)
	}
	_, err = failingSvc.Upload(ctx, data, UploadOpts{
		Namespace:  "cleanup/shared",
		Filename:   "note.txt",
		MimeType:   "text/plain",
		OwnerScope: "user",
		OwnerID:    "u2",
	})
	if !errors.Is(err, meta.err) {
		t.Fatalf("Upload(failing) error = %v, want %v", err, meta.err)
	}
	key, err := uri.ToObjectKey()
	if err != nil {
		t.Fatalf("ToObjectKey() error = %v", err)
	}
	if ok, err := store.Exists(ctx, key); err != nil || !ok {
		t.Fatalf("existing shared object should remain, exists=%v err=%v", ok, err)
	}
}

func TestServiceGCOrphanObjects(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	meta := newMemoryMetaStore()
	svc, err := NewService(store, meta)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	keptURI, err := svc.Upload(ctx, []byte("kept"), UploadOpts{
		Namespace:  "gc/test",
		Filename:   "kept.txt",
		MimeType:   "text/plain",
		OwnerScope: "user",
		OwnerID:    "u1",
	})
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	orphanKey := objectKey("gc/test", strings.Repeat("a", 64), "txt")
	if err := store.Put(ctx, orphanKey, []byte("orphan"), ObjectMeta{MimeType: "text/plain"}); err != nil {
		t.Fatalf("Put(orphan) error = %v", err)
	}

	dryRun, err := svc.GCOrphanObjects(ctx, GCOptions{Prefix: "gc/test", DryRun: true})
	if err != nil {
		t.Fatalf("GCOrphanObjects(dry-run) error = %v", err)
	}
	if len(dryRun.OrphanKeys) != 1 || dryRun.OrphanKeys[0] != orphanKey || len(dryRun.DeletedKeys) != 0 {
		t.Fatalf("dry-run result = %#v, want one orphan and no deleted keys", dryRun)
	}
	if ok, err := store.Exists(ctx, orphanKey); err != nil || !ok {
		t.Fatalf("dry-run should not delete orphan, exists=%v err=%v", ok, err)
	}

	result, err := svc.GCOrphanObjects(ctx, GCOptions{Prefix: "gc/test"})
	if err != nil {
		t.Fatalf("GCOrphanObjects() error = %v", err)
	}
	if len(result.DeletedKeys) != 1 || result.DeletedKeys[0] != orphanKey {
		t.Fatalf("GC result = %#v, want orphan deleted", result)
	}
	if ok, err := store.Exists(ctx, orphanKey); err != nil || ok {
		t.Fatalf("orphan should be deleted, exists=%v err=%v", ok, err)
	}
	keptKey, err := keptURI.ToObjectKey()
	if err != nil {
		t.Fatalf("kept uri key error = %v", err)
	}
	if ok, err := store.Exists(ctx, keptKey); err != nil || !ok {
		t.Fatalf("metadata-backed object should be kept, exists=%v err=%v", ok, err)
	}
}

type failingSaveMetaStore struct {
	*memoryMetaStore
	err error
}

func (s *failingSaveMetaStore) Save(context.Context, *AssetRecord) error {
	return s.err
}
