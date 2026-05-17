package master

import (
	"encoding/json"
	"strings"

	"github.com/chef-guo/agents-hive/internal/llm"
)

func attachmentMetadata(attachments []FileAttachment) map[string]string {
	manifest := attachmentManifest(attachments)
	if len(manifest) == 0 {
		return nil
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil
	}
	return map[string]string{"attachments": string(raw)}
}

func attachmentManifest(attachments []FileAttachment) []FileAttachment {
	out := make([]FileAttachment, 0, len(attachments))
	for _, att := range attachments {
		if strings.TrimSpace(att.Filename) == "" && strings.TrimSpace(att.AssetURI) == "" {
			continue
		}
		out = append(out, FileAttachment{
			Filename:    att.Filename,
			MimeType:    att.MimeType,
			Size:        att.Size,
			AssetURI:    att.AssetURI,
			ContentHash: att.ContentHash,
		})
	}
	return out
}

func hasAttachmentManifest(metadata map[string]string) bool {
	if metadata == nil {
		return false
	}
	return strings.TrimSpace(metadata["attachments"]) != ""
}

func attachmentsFromMetadata(meta map[string]any) []FileAttachment {
	if meta == nil {
		return nil
	}
	raw, ok := meta["attachments"]
	if !ok {
		return nil
	}
	var attachments []FileAttachment
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		if err := json.Unmarshal([]byte(v), &attachments); err != nil {
			return nil
		}
	case []any:
		data, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		if err := json.Unmarshal(data, &attachments); err != nil {
			return nil
		}
	default:
		return nil
	}
	return attachmentManifest(attachments)
}

func AttachmentsFromMetadataForAPI(meta map[string]any) []FileAttachment {
	return attachmentsFromMetadata(meta)
}

func AttachmentMetadataForTest(attachments []FileAttachment) map[string]string {
	return attachmentMetadata(attachments)
}

func restoreContentFromMetadata(text string, meta map[string]any) llm.Content {
	attachments := attachmentsFromMetadata(meta)
	if len(attachments) == 0 {
		return llm.NewTextContent(text)
	}
	parts := []llm.ContentPart{llm.TextPart(text)}
	for _, att := range attachments {
		parts = append(parts, llm.TextPart(attachmentReferenceText(att)))
	}
	return llm.NewMultiContent(parts...)
}

func attachmentReferenceText(att FileAttachment) string {
	label := strings.TrimSpace(att.Filename)
	if label == "" {
		label = "附件"
	}
	uri := strings.TrimSpace(att.AssetURI)
	if uri == "" {
		return "[附件: " + label + "]"
	}
	return "[附件引用: " + label + "] " + uri
}
