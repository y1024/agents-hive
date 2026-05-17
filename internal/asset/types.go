package asset

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const uriScheme = "asset://"

var (
	contentHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	extPattern         = regexp.MustCompile(`^[A-Za-z0-9]{1,16}$`)
)

type AssetRecord struct {
	ID          string            `json:"id"`
	Key         string            `json:"key"`
	Namespace   string            `json:"namespace"`
	ContentHash string            `json:"content_hash"`
	MimeType    string            `json:"mime_type"`
	Filename    string            `json:"filename"`
	Size        int64             `json:"size"`
	OwnerScope  string            `json:"owner_scope"`
	OwnerID     string            `json:"owner_id"`
	Tags        map[string]string `json:"tags,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

type AssetURI string

type UploadOpts struct {
	Namespace  string
	Filename   string
	MimeType   string
	OwnerScope string
	OwnerID    string
	Tags       map[string]string
}

func NewAssetURI(namespace, contentHash, ext string) AssetURI {
	key := objectKey(namespace, contentHash, ext)
	if key == "" {
		return ""
	}
	return AssetURI(uriScheme + key)
}

func (u AssetURI) String() string {
	return string(u)
}

func (u AssetURI) ToObjectKey() (string, error) {
	raw := strings.TrimSpace(string(u))
	if !strings.HasPrefix(raw, uriScheme) {
		return "", ErrInvalidAssetURI
	}
	key := strings.TrimPrefix(raw, uriScheme)
	if err := ValidateObjectKey(key); err != nil {
		return "", err
	}
	return key, nil
}

func AssetURIFromObjectKey(key string) (AssetURI, error) {
	if err := ValidateObjectKey(key); err != nil {
		return "", err
	}
	return AssetURI(uriScheme + key), nil
}

func ValidateNamespace(namespace string) error {
	ns := strings.TrimSpace(namespace)
	if ns == "" || strings.HasPrefix(ns, "/") || strings.Contains(ns, "\\") {
		return ErrInvalidNamespace
	}
	clean := path.Clean(ns)
	if clean == "." || clean != ns || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || strings.Contains(clean, "../") {
		return ErrInvalidNamespace
	}
	return nil
}

func ValidateObjectKey(key string) error {
	k := strings.TrimSpace(key)
	if k == "" || strings.HasPrefix(k, "/") || strings.Contains(k, "\\") {
		return ErrInvalidObjectKey
	}
	clean := path.Clean(k)
	if clean == "." || clean != k || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || strings.Contains(clean, "../") {
		return ErrInvalidObjectKey
	}
	_, filename := path.Split(k)
	parts := strings.Split(filename, ".")
	if len(parts) != 2 || !contentHashPattern.MatchString(parts[0]) || !extPattern.MatchString(parts[1]) {
		return ErrInvalidObjectKey
	}
	return nil
}

func FileExtForAsset(filename, mimeType string) string {
	if ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), "."); extPattern.MatchString(ext) {
		return ext
	}
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "application/pdf":
		return "pdf"
	case "text/plain":
		return "txt"
	case "text/markdown":
		return "md"
	default:
		return "bin"
	}
}

func objectKey(namespace, contentHash, ext string) string {
	ns := strings.TrimSpace(namespace)
	hash := strings.ToLower(strings.TrimSpace(contentHash))
	cleanExt := strings.Trim(strings.ToLower(strings.TrimSpace(ext)), ".")
	if ValidateNamespace(ns) != nil || !contentHashPattern.MatchString(hash) || !extPattern.MatchString(cleanExt) {
		return ""
	}
	return fmt.Sprintf("%s/%s.%s", ns, hash, cleanExt)
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}
	return out
}
