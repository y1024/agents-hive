package asset

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLocalStorePutGetSignedURL(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	key := "im/user/u1/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"
	meta := ObjectMeta{ContentHash: strings.Repeat("a", 64), MimeType: "image/png", Size: 3, Tags: map[string]string{"source_kind": "chat_attachment"}}
	if err := store.Put(ctx, key, []byte("png"), meta); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	got, gotMeta, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got) != "png" || gotMeta.ContentHash != meta.ContentHash || gotMeta.Tags["source_kind"] != "chat_attachment" {
		t.Fatalf("Get() = %q %#v", got, gotMeta)
	}
	signed, err := store.SignedURL(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("SignedURL() error = %v", err)
	}
	if !strings.HasPrefix(signed, "local://asset/") || !strings.Contains(signed, "expires=") || strings.Contains(signed, store.basePath) {
		t.Fatalf("SignedURL() = %q", signed)
	}
}

func TestLocalStoreRejectsTraversal(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	badKeys := []string{
		"../aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png",
		"ns/../../aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png",
		"/ns/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png",
		`ns\aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png`,
	}
	for _, key := range badKeys {
		if err := store.Put(context.Background(), key, []byte("x"), ObjectMeta{}); !errors.Is(err, ErrInvalidObjectKey) {
			t.Fatalf("Put(%q) error = %v, want ErrInvalidObjectKey", key, err)
		}
	}
}
