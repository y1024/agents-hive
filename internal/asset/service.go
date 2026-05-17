package asset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type AssetService struct {
	objects ObjectStore
	meta    AssetMetaStore
}

const DefaultMaxUploadBytes int64 = 100 * 1024 * 1024

var maxUploadBytes = DefaultMaxUploadBytes

type GCOptions struct {
	Prefix string
	DryRun bool
}

type GCResult struct {
	ScannedKeys []string `json:"scanned_keys,omitempty"`
	OrphanKeys  []string `json:"orphan_keys,omitempty"`
	DeletedKeys []string `json:"deleted_keys,omitempty"`
}

func NewService(objects ObjectStore, meta AssetMetaStore) (*AssetService, error) {
	if objects == nil {
		return nil, fmt.Errorf("%w: object store is nil", ErrStoreUnavailable)
	}
	if meta == nil {
		return nil, fmt.Errorf("%w: metadata store is nil", ErrStoreUnavailable)
	}
	return &AssetService{objects: objects, meta: meta}, nil
}

func (s *AssetService) Upload(ctx context.Context, data []byte, opts UploadOpts) (AssetURI, error) {
	if s == nil {
		return "", ErrStoreUnavailable
	}
	opts.Namespace = strings.TrimSpace(opts.Namespace)
	opts.OwnerScope = strings.TrimSpace(opts.OwnerScope)
	opts.OwnerID = strings.TrimSpace(opts.OwnerID)
	if err := ValidateNamespace(opts.Namespace); err != nil {
		return "", err
	}
	if opts.OwnerScope == "" || opts.OwnerID == "" {
		return "", fmt.Errorf("%w: owner_scope and owner_id are required", ErrInvalidUploadOpts)
	}
	if int64(len(data)) > maxUploadBytes {
		return "", fmt.Errorf("%w: asset size %d exceeds %d bytes", ErrInvalidUploadOpts, len(data), maxUploadBytes)
	}

	sum := sha256.Sum256(data)
	contentHash := hex.EncodeToString(sum[:])
	if rec, err := s.meta.GetByHashForOwner(ctx, opts.Namespace, contentHash, opts.OwnerScope, opts.OwnerID); err == nil && rec != nil {
		return AssetURIFromObjectKey(rec.Key)
	} else if err != nil && err != ErrNotFound {
		return "", err
	}

	ext := FileExtForAsset(opts.Filename, opts.MimeType)
	key := objectKey(opts.Namespace, contentHash, ext)
	if key == "" {
		return "", ErrInvalidObjectKey
	}
	existedBeforePut, err := s.objects.Exists(ctx, key)
	if err != nil {
		return "", err
	}
	meta := ObjectMeta{
		ContentHash: contentHash,
		MimeType:    strings.TrimSpace(opts.MimeType),
		Size:        int64(len(data)),
		Tags:        cloneTags(opts.Tags),
	}
	if err := s.objects.Put(ctx, key, data, meta); err != nil {
		return "", err
	}
	rec := &AssetRecord{
		Key:         key,
		Namespace:   opts.Namespace,
		ContentHash: contentHash,
		MimeType:    strings.TrimSpace(opts.MimeType),
		Filename:    strings.TrimSpace(opts.Filename),
		Size:        int64(len(data)),
		OwnerScope:  opts.OwnerScope,
		OwnerID:     opts.OwnerID,
		Tags:        cloneTags(opts.Tags),
	}
	if err := s.meta.Save(ctx, rec); err != nil {
		if !existedBeforePut {
			_ = s.objects.Delete(ctx, key)
		}
		return "", err
	}
	return AssetURIFromObjectKey(key)
}

func (s *AssetService) Download(ctx context.Context, uri AssetURI) ([]byte, *AssetRecord, error) {
	if s == nil {
		return nil, nil, ErrStoreUnavailable
	}
	key, err := uri.ToObjectKey()
	if err != nil {
		return nil, nil, err
	}
	rec, err := s.meta.GetByKey(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	data, _, err := s.objects.Get(ctx, rec.Key)
	if err != nil {
		return nil, nil, err
	}
	return data, rec, nil
}

// GetSignedURL 是内部低层 helper。HTTP/API 调用必须使用 ResolveAsset。
func (s *AssetService) GetSignedURL(ctx context.Context, uri AssetURI, ttl time.Duration) (string, error) {
	if s == nil {
		return "", ErrStoreUnavailable
	}
	key, err := uri.ToObjectKey()
	if err != nil {
		return "", err
	}
	return s.objects.SignedURL(ctx, key, ttl)
}

func (s *AssetService) ResolveAsset(ctx context.Context, uri AssetURI, rc ResolveContext, resolver AccessResolver, ttl time.Duration) (string, *AssetRecord, error) {
	if s == nil {
		return "", nil, ErrStoreUnavailable
	}
	key, err := uri.ToObjectKey()
	if err != nil {
		return "", nil, err
	}
	rec, err := s.meta.GetByKeyForOwner(ctx, key, rc.OwnerScope, rc.OwnerID)
	if err != nil {
		return "", nil, err
	}
	if err := checkOwner(rec, rc); err != nil {
		return "", nil, err
	}
	if resolver == nil {
		return "", nil, fmt.Errorf("%w: access resolver is required", ErrAccessDenied)
	}
	if err := resolver.CanResolveAsset(ctx, rec, rc); err != nil {
		return "", nil, err
	}
	url, err := s.objects.SignedURL(ctx, rec.Key, ttl)
	if err != nil {
		return "", nil, err
	}
	return url, rec, nil
}

func (s *AssetService) GCOrphanObjects(ctx context.Context, opts GCOptions) (GCResult, error) {
	var result GCResult
	if s == nil {
		return result, ErrStoreUnavailable
	}
	lister, ok := s.objects.(ObjectLister)
	if !ok {
		return result, fmt.Errorf("%w: object store does not support key listing", ErrStoreUnavailable)
	}
	prefix := strings.TrimSpace(opts.Prefix)
	if prefix != "" {
		if err := ValidateNamespace(prefix); err != nil {
			return result, err
		}
	}
	keys, err := lister.ListKeys(ctx, prefix)
	if err != nil {
		return result, err
	}
	result.ScannedKeys = append(result.ScannedKeys, keys...)
	for _, key := range keys {
		if _, err := s.meta.GetByKey(ctx, key); err != nil {
			if err != ErrNotFound {
				return result, err
			}
			result.OrphanKeys = append(result.OrphanKeys, key)
			if !opts.DryRun {
				if err := s.objects.Delete(ctx, key); err != nil {
					return result, err
				}
				result.DeletedKeys = append(result.DeletedKeys, key)
			}
		}
	}
	return result, nil
}
