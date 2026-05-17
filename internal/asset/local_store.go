package asset

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type LocalStore struct {
	basePath string
}

func NewLocalStore(basePath string) (*LocalStore, error) {
	base := strings.TrimSpace(basePath)
	if base == "" {
		return nil, fmt.Errorf("%w: local base_path is required", ErrInvalidUploadOpts)
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &LocalStore{basePath: abs}, nil
}

func (s *LocalStore) Put(ctx context.Context, key string, data []byte, meta ObjectMeta) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return s.writeMeta(path, meta)
}

func (s *LocalStore) Get(ctx context.Context, key string) ([]byte, ObjectMeta, error) {
	select {
	case <-ctx.Done():
		return nil, ObjectMeta{}, ctx.Err()
	default:
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return nil, ObjectMeta{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ObjectMeta{}, ErrNotFound
		}
		return nil, ObjectMeta{}, err
	}
	meta, err := s.readMeta(path)
	if err != nil {
		return nil, ObjectMeta{}, err
	}
	return data, meta, nil
}

func (s *LocalStore) Head(ctx context.Context, key string) (ObjectMeta, error) {
	select {
	case <-ctx.Done():
		return ObjectMeta{}, ctx.Err()
	default:
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return ObjectMeta{}, err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return ObjectMeta{}, ErrNotFound
		}
		return ObjectMeta{}, err
	}
	return s.readMeta(path)
}

func (s *LocalStore) Delete(ctx context.Context, key string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(metaPath(path)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *LocalStore) Exists(ctx context.Context, key string) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}
	path, err := s.pathForKey(key)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *LocalStore) SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	if _, err := s.Head(ctx, key); err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return fmt.Sprintf("local://asset/%s?expires=%d", key, time.Now().Add(ttl).Unix()), nil
}

func (s *LocalStore) ListKeys(ctx context.Context, prefix string) ([]string, error) {
	if s == nil || s.basePath == "" {
		return nil, ErrStoreUnavailable
	}
	cleanPrefix := strings.Trim(strings.TrimSpace(prefix), "/")
	if cleanPrefix != "" {
		cleanPrefix += "/"
	}
	var keys []string
	err := filepath.WalkDir(s.basePath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".meta.json") {
			return nil
		}
		rel, err := filepath.Rel(s.basePath, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if cleanPrefix != "" && !strings.HasPrefix(key, cleanPrefix) {
			return nil
		}
		if err := ValidateObjectKey(key); err != nil {
			return nil
		}
		keys = append(keys, key)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func (s *LocalStore) pathForKey(key string) (string, error) {
	if s == nil || s.basePath == "" {
		return "", ErrStoreUnavailable
	}
	if err := ValidateObjectKey(key); err != nil {
		return "", err
	}
	target := filepath.Join(s.basePath, filepath.FromSlash(key))
	rel, err := filepath.Rel(s.basePath, target)
	if err != nil {
		return "", err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", ErrInvalidObjectKey
	}
	return target, nil
}

func (s *LocalStore) writeMeta(path string, meta ObjectMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath(path), data, 0o644)
}

func (s *LocalStore) readMeta(path string) (ObjectMeta, error) {
	data, err := os.ReadFile(metaPath(path))
	if err != nil {
		if os.IsNotExist(err) {
			info, statErr := os.Stat(path)
			if statErr != nil {
				if os.IsNotExist(statErr) {
					return ObjectMeta{}, ErrNotFound
				}
				return ObjectMeta{}, statErr
			}
			return ObjectMeta{Size: info.Size()}, nil
		}
		return ObjectMeta{}, err
	}
	var meta ObjectMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return ObjectMeta{}, err
	}
	return meta, nil
}

func metaPath(path string) string {
	return path + ".meta.json"
}
