package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/asset"
	"github.com/chef-guo/agents-hive/internal/config"
	"github.com/chef-guo/agents-hive/internal/kb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func initAssetService(cfg *config.Config, pgPool *pgxpool.Pool, logger *zap.Logger) *asset.AssetService {
	if cfg == nil {
		return nil
	}
	if pgPool == nil {
		logger.Warn("asset service disabled: PG metadata store unavailable")
		return nil
	}

	assetCfg := config.NormalizeAssetConfig(cfg.Asset)
	objStore, err := newObjectStore(context.Background(), assetCfg)
	if err != nil {
		logger.Error("asset object store init failed", zap.Error(err), zap.String("provider", assetCfg.Provider))
		return nil
	}
	svc, err := asset.NewService(objStore, asset.NewPGMetaStore(pgPool))
	if err != nil {
		logger.Error("asset service init failed", zap.Error(err))
		return nil
	}

	logger.Info("asset service enabled", zap.String("provider", assetCfg.Provider))
	return svc
}

func newObjectStore(ctx context.Context, assetCfg config.AssetConfig) (asset.ObjectStore, error) {
	switch assetCfg.Provider {
	case "local":
		return asset.NewLocalStore(strings.TrimSpace(assetCfg.Local.BasePath))
	case "minio":
		return asset.NewMinIOStore(ctx, asset.S3Config{
			Endpoint:  assetCfg.MinIO.Endpoint,
			AccessKey: assetCfg.MinIO.AccessKey,
			SecretKey: assetCfg.MinIO.SecretKey,
			Bucket:    assetCfg.MinIO.Bucket,
			Region:    assetCfg.MinIO.Region,
			UseSSL:    assetCfg.MinIO.UseSSL,
		})
	case "s3":
		return asset.NewS3Store(ctx, asset.S3Config{
			Endpoint:  assetCfg.S3.Endpoint,
			AccessKey: assetCfg.S3.AccessKey,
			SecretKey: assetCfg.S3.SecretKey,
			Bucket:    assetCfg.S3.Bucket,
			Region:    assetCfg.S3.Region,
			UseSSL:    assetCfg.S3.UseSSL,
		})
	default:
		return nil, asset.ErrStoreUnavailable
	}
}

func initAssetAccessResolver(logger *zap.Logger, kbService *kb.Service) asset.AccessResolver {
	return &assetAccessResolver{
		logger:    logger,
		kbService: kbService,
	}
}

type assetAccessResolver struct {
	logger    *zap.Logger
	kbService *kb.Service
}

func (r *assetAccessResolver) CanResolveAsset(ctx context.Context, rec *asset.AssetRecord, rc asset.ResolveContext) error {
	if rec == nil {
		return asset.ErrNotFound
	}
	if rec.Tags["source_kind"] == "kb_document_image" {
		return r.canResolveKBAsset(ctx, rec, rc)
	}
	if rec.Tags["source_kind"] == "agent_artifact" || rec.Tags["source_kind"] == "chat_attachment" {
		sessionID := strings.TrimSpace(rc.SessionID)
		assetSessionID := strings.TrimSpace(rec.Tags["session_id"])
		if sessionID == "" || assetSessionID == "" || assetSessionID != sessionID {
			return asset.ErrAccessDenied
		}
	}
	if rc.OwnerScope != "user" || rc.OwnerID == "" || rc.UserID == "" || rc.OwnerID != rc.UserID {
		return asset.ErrAccessDenied
	}
	if rec.OwnerScope != "user" || rec.OwnerID != rc.OwnerID {
		return asset.ErrAccessDenied
	}
	return nil
}

func (r *assetAccessResolver) canResolveKBAsset(ctx context.Context, rec *asset.AssetRecord, rc asset.ResolveContext) error {
	if r == nil || r.kbService == nil {
		if r != nil && r.logger != nil {
			r.logger.Warn("KB 资产访问 resolver 未接入 KB 服务，拒绝解析",
				zap.String("asset_id", rec.ID),
				zap.String("namespace", rec.Namespace))
		}
		return asset.ErrAccessDenied
	}
	if rc.OwnerScope != rec.OwnerScope || rc.OwnerID != rec.OwnerID || rc.UserID == "" {
		return asset.ErrAccessDenied
	}
	domainID := strings.TrimSpace(rec.Tags["domain_id"])
	namespaceID := strings.TrimSpace(rec.Tags["kb_namespace_id"])
	documentID := strings.TrimSpace(rec.Tags["kb_document_id"])
	if domainID == "" || namespaceID == "" || documentID == "" {
		return asset.ErrAccessDenied
	}
	if rc.DomainID != "" && rc.DomainID != domainID {
		return asset.ErrAccessDenied
	}
	assetURI, err := asset.AssetURIFromObjectKey(rec.Key)
	if err != nil {
		return err
	}
	now := time.Now()
	assets, err := r.kbService.ListNodeAssets(ctx, kb.ManagementScope{
		DomainID:   domainID,
		OwnerScope: kb.OwnerScope(rec.OwnerScope),
		OwnerID:    rec.OwnerID,
		Now:        now,
	}, documentID, nil)
	if err != nil {
		return asset.ErrAccessDenied
	}
	found := false
	for _, item := range assets {
		if item.NamespaceID == namespaceID && item.AssetURI == assetURI.String() {
			found = true
			break
		}
	}
	if !found {
		return asset.ErrAccessDenied
	}
	if rc.Purpose == "kb_management" && rc.OwnerScope == "user" && rc.OwnerID == rc.UserID {
		return nil
	}
	effective, err := r.kbService.EffectiveBindings(ctx, kb.BindingResolveInput{
		DomainID:          domainID,
		OwnerScope:        kb.OwnerScope(rec.OwnerScope),
		OwnerID:           rec.OwnerID,
		UserID:            rc.UserID,
		TenantID:          strings.TrimSpace(rc.Extra["tenant_id"]),
		AgentID:           strings.TrimSpace(rc.Extra["agent_id"]),
		SessionTemplateID: strings.TrimSpace(rc.Extra["session_template_id"]),
		SessionID:         rc.SessionID,
		Now:               now,
	})
	if err != nil {
		if r.logger != nil {
			r.logger.Debug("KB 资产绑定校验失败",
				zap.String("asset_id", rec.ID),
				zap.String("namespace_id", namespaceID),
				zap.Error(err))
		}
		return asset.ErrAccessDenied
	}
	for _, binding := range effective {
		if binding.NamespaceID == namespaceID {
			return nil
		}
	}
	return asset.ErrAccessDenied
}

type kbAssetUploader struct {
	service *asset.AssetService
}

func newKBAssetUploader(service *asset.AssetService) kb.AssetUploader {
	if service == nil {
		return nil
	}
	return &kbAssetUploader{service: service}
}

func (u *kbAssetUploader) Upload(ctx context.Context, data []byte, opts kb.AssetUploadOptions) (string, string, error) {
	if u == nil || u.service == nil {
		return "", "", asset.ErrStoreUnavailable
	}
	sum := sha256.Sum256(data)
	contentHash := hex.EncodeToString(sum[:])
	uri, err := u.service.Upload(ctx, data, asset.UploadOpts{
		Namespace:  opts.Namespace,
		Filename:   opts.Filename,
		MimeType:   opts.MimeType,
		OwnerScope: opts.OwnerScope,
		OwnerID:    opts.OwnerID,
		Tags:       opts.Tags,
	})
	if err != nil {
		return "", "", err
	}
	return uri.String(), contentHash, nil
}
