package asset

import (
	"context"
	"time"
)

// ObjectStore 定义对象体存储能力。key 是内部对象 key，不是公开 URL。
type ObjectStore interface {
	Put(ctx context.Context, key string, data []byte, meta ObjectMeta) error
	Get(ctx context.Context, key string) ([]byte, ObjectMeta, error)
	Head(ctx context.Context, key string) (ObjectMeta, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

type ObjectLister interface {
	ListKeys(ctx context.Context, prefix string) ([]string, error)
}

type ObjectMeta struct {
	ContentHash string            `json:"content_hash"`
	MimeType    string            `json:"mime_type"`
	Size        int64             `json:"size"`
	Tags        map[string]string `json:"tags,omitempty"`
}

type AssetMetaStore interface {
	Save(ctx context.Context, rec *AssetRecord) error
	GetByKey(ctx context.Context, key string) (*AssetRecord, error)
	GetByKeyForOwner(ctx context.Context, key, ownerScope, ownerID string) (*AssetRecord, error)
	GetByHash(ctx context.Context, namespace, contentHash string) (*AssetRecord, error)
	GetByHashForOwner(ctx context.Context, namespace, contentHash, ownerScope, ownerID string) (*AssetRecord, error)
	ListByNamespace(ctx context.Context, namespace string) ([]AssetRecord, error)
	Delete(ctx context.Context, id string) error
}
