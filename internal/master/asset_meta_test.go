package master

import (
	"context"
	"sync"
	"time"

	"github.com/chef-guo/agents-hive/internal/asset"
	"github.com/google/uuid"
)

type masterAssetMetaStore struct {
	mu      sync.Mutex
	byID    map[string]*asset.AssetRecord
	byKey   map[string][]string
	byOwner map[string]string
}

func newMasterAssetMetaStore() *masterAssetMetaStore {
	return &masterAssetMetaStore{
		byID:    map[string]*asset.AssetRecord{},
		byKey:   map[string][]string{},
		byOwner: map[string]string{},
	}
}

func (s *masterAssetMetaStore) Save(_ context.Context, rec *asset.AssetRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	key := masterAssetOwnerHashKey(rec.Namespace, rec.ContentHash, rec.OwnerScope, rec.OwnerID)
	if existingID, ok := s.byOwner[key]; ok {
		existing := s.byID[existingID]
		rec.ID = existing.ID
		rec.CreatedAt = existing.CreatedAt
		existing.Filename = rec.Filename
		existing.MimeType = rec.MimeType
		existing.Tags = cloneAssetTags(rec.Tags)
		return nil
	}
	cp := *rec
	cp.Tags = cloneAssetTags(rec.Tags)
	s.byID[rec.ID] = &cp
	s.byKey[rec.Key] = append(s.byKey[rec.Key], rec.ID)
	s.byOwner[key] = rec.ID
	return nil
}

func (s *masterAssetMetaStore) GetByKey(_ context.Context, key string) (*asset.AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := s.byKey[key]
	if len(ids) == 0 {
		return nil, asset.ErrNotFound
	}
	return cloneAssetRecord(s.byID[ids[0]]), nil
}

func (s *masterAssetMetaStore) GetByKeyForOwner(_ context.Context, key, ownerScope, ownerID string) (*asset.AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.byKey[key] {
		rec := s.byID[id]
		if rec.OwnerScope == ownerScope && rec.OwnerID == ownerID {
			return cloneAssetRecord(rec), nil
		}
	}
	return nil, asset.ErrNotFound
}

func (s *masterAssetMetaStore) GetByHash(_ context.Context, namespace, contentHash string) (*asset.AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.byID {
		if rec.Namespace == namespace && rec.ContentHash == contentHash {
			return cloneAssetRecord(rec), nil
		}
	}
	return nil, asset.ErrNotFound
}

func (s *masterAssetMetaStore) GetByHashForOwner(_ context.Context, namespace, contentHash, ownerScope, ownerID string) (*asset.AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byOwner[masterAssetOwnerHashKey(namespace, contentHash, ownerScope, ownerID)]
	if !ok {
		return nil, asset.ErrNotFound
	}
	return cloneAssetRecord(s.byID[id]), nil
}

func (s *masterAssetMetaStore) ListByNamespace(_ context.Context, namespace string) ([]asset.AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []asset.AssetRecord
	for _, rec := range s.byID {
		if rec.Namespace == namespace {
			out = append(out, *cloneAssetRecord(rec))
		}
	}
	return out, nil
}

func (s *masterAssetMetaStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.byID[id]
	if rec == nil {
		return asset.ErrNotFound
	}
	delete(s.byID, id)
	delete(s.byOwner, masterAssetOwnerHashKey(rec.Namespace, rec.ContentHash, rec.OwnerScope, rec.OwnerID))
	return nil
}

func masterAssetOwnerHashKey(namespace, hash, scope, owner string) string {
	return namespace + "\x00" + hash + "\x00" + scope + "\x00" + owner
}

func cloneAssetRecord(rec *asset.AssetRecord) *asset.AssetRecord {
	if rec == nil {
		return nil
	}
	cp := *rec
	cp.Tags = cloneAssetTags(rec.Tags)
	return &cp
}

func cloneAssetTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}
	return out
}
