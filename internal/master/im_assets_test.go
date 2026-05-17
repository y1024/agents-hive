package master

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/chef-guo/agents-hive/internal/asset"
	"github.com/chef-guo/agents-hive/internal/auth"
	"go.uber.org/zap"
)

func TestPersistIMAttachmentsUploadsToAssetService(t *testing.T) {
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "u1"})
	local, err := asset.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore() error = %v", err)
	}
	svc, err := asset.NewService(local, newMasterAssetMetaStore())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	m := &Master{assetService: svc, logger: zap.NewNop()}
	data := []byte("im attachment")
	attachments := []FileAttachment{{
		Filename: "file.txt",
		MimeType: "text/plain",
		Data:     base64.StdEncoding.EncodeToString(data),
	}}
	got := m.persistIMAttachments(ctx, "session-1", "om-1", attachments)
	if len(got) != 1 || got[0].AssetURI == "" || got[0].ContentHash == "" || got[0].Size != int64(len(data)) {
		t.Fatalf("attachments = %+v", got)
	}
	downloaded, rec, err := svc.Download(context.Background(), asset.AssetURI(got[0].AssetURI))
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if string(downloaded) != string(data) || rec.Tags["source_kind"] != "chat_attachment" || rec.Tags["channel_message_id"] != "om-1" {
		t.Fatalf("downloaded=%q rec=%+v", downloaded, rec)
	}
}
