package master

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/asset"
	"github.com/chef-guo/agents-hive/internal/auth"
)

func (m *Master) persistIMAttachments(ctx context.Context, sessionID, channelMessageID string, attachments []FileAttachment) []FileAttachment {
	if m == nil || m.assetService == nil || len(attachments) == 0 {
		return attachments
	}
	ownerID := strings.TrimSpace(auth.UserIDFrom(ctx))
	if ownerID == "" {
		ownerID = "im"
	}
	namespace := "im/user/" + ownerID + "/session/" + sessionID
	for i := range attachments {
		if attachments[i].AssetURI != "" || strings.TrimSpace(attachments[i].Data) == "" {
			continue
		}
		raw, err := DecodeAttachmentData(attachments[i].Data)
		if err != nil {
			m.logger.Warn("IM 附件 base64 解码失败",
				zap.String("session_id", sessionID),
				zap.String("filename", attachments[i].Filename),
				zap.Error(err))
			continue
		}
		sum := sha256.Sum256(raw)
		contentHash := hex.EncodeToString(sum[:])
		uri, err := m.assetService.Upload(ctx, raw, asset.UploadOpts{
			Namespace:  namespace,
			Filename:   attachments[i].Filename,
			MimeType:   attachments[i].MimeType,
			OwnerScope: "user",
			OwnerID:    ownerID,
			Tags: map[string]string{
				"source_kind":        "chat_attachment",
				"session_id":         sessionID,
				"message_id":         channelMessageID,
				"platform":           "im",
				"channel_message_id": channelMessageID,
			},
		})
		if err != nil {
			m.logger.Warn("IM 附件持久化失败",
				zap.String("session_id", sessionID),
				zap.String("filename", attachments[i].Filename),
				zap.Error(err))
			continue
		}
		attachments[i].AssetURI = uri.String()
		attachments[i].ContentHash = contentHash
		attachments[i].Size = int64(len(raw))
	}
	return attachments
}

func DecodeAttachmentData(raw string) ([]byte, error) {
	if before, after, ok := strings.Cut(raw, ","); ok && strings.Contains(before, ";base64") {
		raw = after
	}
	return base64.StdEncoding.DecodeString(raw)
}
