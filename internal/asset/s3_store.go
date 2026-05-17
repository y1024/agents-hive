package asset

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	UseSSL    bool
}

type S3Store struct {
	client *minio.Client
	bucket string
}

func NewS3Store(ctx context.Context, cfg S3Config) (*S3Store, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("%w: bucket is required", ErrInvalidUploadOpts)
	}
	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(strings.TrimSpace(cfg.AccessKey), strings.TrimSpace(cfg.SecretKey), ""),
		Secure: cfg.UseSSL,
		Region: strings.TrimSpace(cfg.Region),
	}
	if endpoint == "" {
		opts.Creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{},
			&credentials.IAM{},
		})
		endpoint = awsEndpointForRegion(opts.Region)
	}
	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, err
	}
	store := &S3Store{client: client, bucket: bucket}
	if err := store.ensureBucket(ctx, opts.Region); err != nil {
		return nil, err
	}
	return store, nil
}

func NewMinIOStore(ctx context.Context, cfg S3Config) (*S3Store, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("%w: minio endpoint is required", ErrInvalidUploadOpts)
	}
	return NewS3Store(ctx, cfg)
}

func (s *S3Store) Put(ctx context.Context, key string, data []byte, meta ObjectMeta) error {
	if s == nil || s.client == nil {
		return ErrStoreUnavailable
	}
	if err := ValidateObjectKey(key); err != nil {
		return err
	}
	userMeta := map[string]string{
		"content-hash": meta.ContentHash,
	}
	for k, v := range meta.Tags {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			userMeta["tag-"+sanitizeMetaKey(k)] = v
		}
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType:  strings.TrimSpace(meta.MimeType),
		UserMetadata: userMeta,
	})
	return err
}

func (s *S3Store) Get(ctx context.Context, key string) ([]byte, ObjectMeta, error) {
	if s == nil || s.client == nil {
		return nil, ObjectMeta{}, ErrStoreUnavailable
	}
	if err := ValidateObjectKey(key); err != nil {
		return nil, ObjectMeta{}, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectMeta{}, mapS3Error(err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, ObjectMeta{}, mapS3Error(err)
	}
	meta, err := s.Head(ctx, key)
	if err != nil {
		return nil, ObjectMeta{}, err
	}
	return data, meta, nil
}

func (s *S3Store) Head(ctx context.Context, key string) (ObjectMeta, error) {
	if s == nil || s.client == nil {
		return ObjectMeta{}, ErrStoreUnavailable
	}
	if err := ValidateObjectKey(key); err != nil {
		return ObjectMeta{}, err
	}
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectMeta{}, mapS3Error(err)
	}
	tags := make(map[string]string)
	for k, value := range info.UserMetadata {
		if !strings.HasPrefix(strings.ToLower(k), "tag-") || value == "" {
			continue
		}
		tags[strings.TrimPrefix(strings.ToLower(k), "tag-")] = value
	}
	return ObjectMeta{
		ContentHash: firstHeader(info.UserMetadata, "Content-Hash"),
		MimeType:    info.ContentType,
		Size:        info.Size,
		Tags:        tags,
	}, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	if s == nil || s.client == nil {
		return ErrStoreUnavailable
	}
	if err := ValidateObjectKey(key); err != nil {
		return err
	}
	return mapS3Error(s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}))
}

func (s *S3Store) Exists(ctx context.Context, key string) (bool, error) {
	if _, err := s.Head(ctx, key); err != nil {
		if err == ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *S3Store) SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if s == nil || s.client == nil {
		return "", ErrStoreUnavailable
	}
	if err := ValidateObjectKey(key); err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if _, err := s.Head(ctx, key); err != nil {
		return "", err
	}
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, ttl, url.Values{})
	if err != nil {
		return "", mapS3Error(err)
	}
	return u.String(), nil
}

func (s *S3Store) ListKeys(ctx context.Context, prefix string) ([]string, error) {
	if s == nil || s.client == nil {
		return nil, ErrStoreUnavailable
	}
	cleanPrefix := strings.Trim(strings.TrimSpace(prefix), "/")
	if cleanPrefix != "" {
		cleanPrefix += "/"
	}
	var keys []string
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    cleanPrefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, mapS3Error(obj.Err)
		}
		if obj.Key == "" {
			continue
		}
		if err := ValidateObjectKey(obj.Key); err != nil {
			return nil, err
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}

func (s *S3Store) ensureBucket(ctx context.Context, region string) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return mapS3Error(err)
	}
	if exists {
		return nil
	}
	return mapS3Error(s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: region}))
}

func awsEndpointForRegion(region string) string {
	if strings.TrimSpace(region) == "" {
		region = "us-east-1"
	}
	return "s3." + region + ".amazonaws.com"
}

func sanitizeMetaKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "_", "-")
	key = strings.ReplaceAll(key, " ", "-")
	return key
}

func firstHeader(headers map[string]string, key string) string {
	for k, value := range headers {
		if strings.EqualFold(k, key) {
			return value
		}
	}
	return ""
}

func mapS3Error(err error) error {
	if err == nil {
		return nil
	}
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "NoSuchKey", "NoSuchBucket", "NotFound":
		return ErrNotFound
	default:
		return err
	}
}
