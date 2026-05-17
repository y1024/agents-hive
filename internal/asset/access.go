package asset

import (
	"context"
	"fmt"
)

type ResolveContext struct {
	OwnerScope string            `json:"owner_scope,omitempty"`
	OwnerID    string            `json:"owner_id,omitempty"`
	UserID     string            `json:"user_id,omitempty"`
	SessionID  string            `json:"session_id,omitempty"`
	DomainID   string            `json:"domain_id,omitempty"`
	Purpose    string            `json:"purpose,omitempty"`
	Extra      map[string]string `json:"extra,omitempty"`
}

type AccessResolver interface {
	CanResolveAsset(ctx context.Context, rec *AssetRecord, rc ResolveContext) error
}

type AccessResolverFunc func(ctx context.Context, rec *AssetRecord, rc ResolveContext) error

func (f AccessResolverFunc) CanResolveAsset(ctx context.Context, rec *AssetRecord, rc ResolveContext) error {
	if f == nil {
		return ErrAccessDenied
	}
	return f(ctx, rec, rc)
}

type AllowAllResolver struct{}

func (AllowAllResolver) CanResolveAsset(context.Context, *AssetRecord, ResolveContext) error {
	return nil
}

func checkOwner(rec *AssetRecord, rc ResolveContext) error {
	if rec == nil {
		return ErrNotFound
	}
	if rec.OwnerScope == "" && rec.OwnerID == "" {
		return nil
	}
	if rec.OwnerScope != rc.OwnerScope || rec.OwnerID != rc.OwnerID {
		return fmt.Errorf("%w: owner mismatch", ErrAccessDenied)
	}
	return nil
}
