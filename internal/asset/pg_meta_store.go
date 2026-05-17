package asset

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGMetaStore struct {
	pool *pgxpool.Pool
}

func NewPGMetaStore(pool *pgxpool.Pool) *PGMetaStore {
	return &PGMetaStore{pool: pool}
}

func (s *PGMetaStore) Save(ctx context.Context, rec *AssetRecord) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	if rec == nil {
		return ErrInvalidUploadOpts
	}
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	tags, err := json.Marshal(nonNilTags(rec.Tags))
	if err != nil {
		return err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO assets (
			id, key, namespace, content_hash, mime_type, filename, size_bytes,
			owner_scope, owner_id, tags, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10::jsonb, $11
		)
		ON CONFLICT (namespace, content_hash, owner_scope, owner_id) DO UPDATE SET
			filename = EXCLUDED.filename,
			mime_type = EXCLUDED.mime_type,
			tags = COALESCE(assets.tags, '{}'::jsonb) || EXCLUDED.tags
		RETURNING id, created_at`,
		rec.ID, rec.Key, rec.Namespace, rec.ContentHash, rec.MimeType, rec.Filename, rec.Size,
		rec.OwnerScope, rec.OwnerID, string(tags), rec.CreatedAt,
	)
	return row.Scan(&rec.ID, &rec.CreatedAt)
}

func (s *PGMetaStore) GetByKey(ctx context.Context, key string) (*AssetRecord, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id, key, namespace, content_hash, mime_type, filename, size_bytes,
		       owner_scope, owner_id, tags, created_at
		FROM assets
		WHERE key = $1`, key)
	return scanAssetRecord(row)
}

func (s *PGMetaStore) GetByKeyForOwner(ctx context.Context, key, ownerScope, ownerID string) (*AssetRecord, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id, key, namespace, content_hash, mime_type, filename, size_bytes,
		       owner_scope, owner_id, tags, created_at
		FROM assets
		WHERE key = $1 AND owner_scope = $2 AND owner_id = $3`, key, ownerScope, ownerID)
	return scanAssetRecord(row)
}

func (s *PGMetaStore) GetByHash(ctx context.Context, namespace, contentHash string) (*AssetRecord, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id, key, namespace, content_hash, mime_type, filename, size_bytes,
		       owner_scope, owner_id, tags, created_at
		FROM assets
		WHERE namespace = $1 AND content_hash = $2
		ORDER BY created_at ASC
		LIMIT 1`, namespace, contentHash)
	return scanAssetRecord(row)
}

func (s *PGMetaStore) GetByHashForOwner(ctx context.Context, namespace, contentHash, ownerScope, ownerID string) (*AssetRecord, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id, key, namespace, content_hash, mime_type, filename, size_bytes,
		       owner_scope, owner_id, tags, created_at
		FROM assets
		WHERE namespace = $1 AND content_hash = $2 AND owner_scope = $3 AND owner_id = $4
		ORDER BY created_at ASC
		LIMIT 1`, namespace, contentHash, ownerScope, ownerID)
	return scanAssetRecord(row)
}

func (s *PGMetaStore) ListByNamespace(ctx context.Context, namespace string) ([]AssetRecord, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, key, namespace, content_hash, mime_type, filename, size_bytes,
		       owner_scope, owner_id, tags, created_at
		FROM assets
		WHERE namespace = $1
		ORDER BY created_at DESC`, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AssetRecord
	for rows.Next() {
		rec, err := scanAssetRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

func (s *PGMetaStore) Delete(ctx context.Context, id string) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	cmd, err := s.pool.Exec(ctx, `DELETE FROM assets WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type assetScanner interface {
	Scan(dest ...any) error
}

type assetRowScanner interface {
	Scan(dest ...any) error
}

func scanAssetRecord(row assetScanner) (*AssetRecord, error) {
	var rec AssetRecord
	var tags []byte
	if err := row.Scan(
		&rec.ID, &rec.Key, &rec.Namespace, &rec.ContentHash, &rec.MimeType, &rec.Filename, &rec.Size,
		&rec.OwnerScope, &rec.OwnerID, &tags, &rec.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(tags) > 0 {
		if err := json.Unmarshal(tags, &rec.Tags); err != nil {
			return nil, err
		}
	}
	return &rec, nil
}

func nonNilTags(tags map[string]string) map[string]string {
	if tags == nil {
		return map[string]string{}
	}
	return tags
}
