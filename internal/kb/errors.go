package kb

import "errors"

var (
	ErrInvalidScope      = errors.New("kb: invalid scope")
	ErrNoKBBinding       = errors.New("kb: no kb bound")
	ErrNamespaceNotBound = errors.New("kb: namespace not bound")
	ErrNotFound          = errors.New("kb: not found")
	ErrInvalidInput      = errors.New("kb: invalid input")
	ErrEmptyDocument     = errors.New("kb: empty document")
	ErrUnsupportedAsset  = errors.New("kb: document asset references require asset ingest")
	ErrNoHeading         = errors.New("kb: markdown has no heading")
	ErrEmptyHeading      = errors.New("kb: empty heading")
	ErrDuplicateDocument = errors.New("kb: duplicate document")
	ErrOutputTooLarge    = errors.New("kb: section text output too large")
)

func IsRecoverable(err error) bool {
	return errors.Is(err, ErrNoKBBinding) ||
		errors.Is(err, ErrNamespaceNotBound) ||
		errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrInvalidInput) ||
		errors.Is(err, ErrOutputTooLarge) ||
		errors.Is(err, ErrUnsupportedAsset)
}
