package asset

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

type memoryMetaStore struct {
	mu      sync.Mutex
	byID    map[string]*AssetRecord
	byKey   map[string][]string
	byOwner map[string]string
}

func newMemoryMetaStore() *memoryMetaStore {
	return &memoryMetaStore{
		byID:    map[string]*AssetRecord{},
		byKey:   map[string][]string{},
		byOwner: map[string]string{},
	}
}

func (s *memoryMetaStore) Save(_ context.Context, rec *AssetRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	key := ownerHashKey(rec.Namespace, rec.ContentHash, rec.OwnerScope, rec.OwnerID)
	if existingID, ok := s.byOwner[key]; ok {
		existing := s.byID[existingID]
		rec.ID = existing.ID
		rec.CreatedAt = existing.CreatedAt
		existing.Filename = rec.Filename
		existing.MimeType = rec.MimeType
		existing.Tags = cloneTags(rec.Tags)
		return nil
	}
	cp := *rec
	cp.Tags = cloneTags(rec.Tags)
	s.byID[rec.ID] = &cp
	s.byKey[rec.Key] = append(s.byKey[rec.Key], rec.ID)
	s.byOwner[key] = rec.ID
	return nil
}

func (s *memoryMetaStore) GetByKey(_ context.Context, key string) (*AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := s.byKey[key]
	if len(ids) == 0 {
		return nil, ErrNotFound
	}
	return cloneRecord(s.byID[ids[0]]), nil
}

func (s *memoryMetaStore) GetByKeyForOwner(_ context.Context, key, ownerScope, ownerID string) (*AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.byKey[key] {
		rec := s.byID[id]
		if rec.OwnerScope == ownerScope && rec.OwnerID == ownerID {
			return cloneRecord(rec), nil
		}
	}
	return nil, ErrNotFound
}

func (s *memoryMetaStore) GetByHash(_ context.Context, namespace, contentHash string) (*AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.byID {
		if rec.Namespace == namespace && rec.ContentHash == contentHash {
			return cloneRecord(rec), nil
		}
	}
	return nil, ErrNotFound
}

func (s *memoryMetaStore) GetByHashForOwner(_ context.Context, namespace, contentHash, ownerScope, ownerID string) (*AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byOwner[ownerHashKey(namespace, contentHash, ownerScope, ownerID)]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneRecord(s.byID[id]), nil
}

func (s *memoryMetaStore) ListByNamespace(_ context.Context, namespace string) ([]AssetRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []AssetRecord
	for _, rec := range s.byID {
		if rec.Namespace == namespace {
			out = append(out, *cloneRecord(rec))
		}
	}
	return out, nil
}

func (s *memoryMetaStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.byID[id]
	if rec == nil {
		return ErrNotFound
	}
	delete(s.byID, id)
	delete(s.byOwner, ownerHashKey(rec.Namespace, rec.ContentHash, rec.OwnerScope, rec.OwnerID))
	return nil
}

func ownerHashKey(namespace, hash, scope, owner string) string {
	return namespace + "\x00" + hash + "\x00" + scope + "\x00" + owner
}

func cloneRecord(rec *AssetRecord) *AssetRecord {
	if rec == nil {
		return nil
	}
	cp := *rec
	cp.Tags = cloneTags(rec.Tags)
	return &cp
}
