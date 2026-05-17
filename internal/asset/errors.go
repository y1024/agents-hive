package asset

import "errors"

var (
	ErrInvalidAssetURI   = errors.New("asset: invalid asset URI")
	ErrInvalidObjectKey  = errors.New("asset: invalid object key")
	ErrInvalidNamespace  = errors.New("asset: invalid namespace")
	ErrInvalidUploadOpts = errors.New("asset: invalid upload options")
	ErrNotFound          = errors.New("asset: not found")
	ErrAccessDenied      = errors.New("asset: access denied")
	ErrStoreUnavailable  = errors.New("asset: store unavailable")
)
