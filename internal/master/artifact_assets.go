package master

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/asset"
	"github.com/chef-guo/agents-hive/internal/auth"
)

var (
	closedArtifactTagRe = regexp.MustCompile(`(?s)<artifact\b([^>]*)>(.*?)</artifact>`)
	artifactAttrRe      = regexp.MustCompile(`(?:^|\s)(type|title|language)=["']([^"']*)["']`)
)

type AssistantArtifactManifest struct {
	URI         string `json:"uri"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Language    string `json:"language,omitempty"`
	MimeType    string `json:"mime_type"`
	Size        int64  `json:"size"`
	ContentHash string `json:"content_hash"`
}

func (m *Master) persistAssistantArtifacts(ctx context.Context, session *SessionState, userID, content, createdAt string) string {
	if m == nil || m.assetService == nil || session == nil || !strings.Contains(content, "<artifact") {
		return ""
	}
	ownerID := strings.TrimSpace(userID)
	if ownerID == "" {
		ownerID = artifactOwnerID(ctx, session)
	}

	matches := closedArtifactTagRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return ""
	}
	out := make([]AssistantArtifactManifest, 0, len(matches))
	namespace := "agent/user/" + ownerID + "/session/" + session.ID
	for i, match := range matches {
		attrs := parseArtifactAttrs(match[1])
		body := strings.Trim(match[2], "\n\r\t ")
		if body == "" {
			continue
		}
		artifactType := normalizeArtifactType(attrs["type"])
		mimeType := artifactMimeType(artifactType, attrs["language"])
		filename := artifactFilename(attrs["title"], artifactType, attrs["language"], i+1)
		sum := sha256.Sum256([]byte(body))
		contentHash := hex.EncodeToString(sum[:])
		uri, err := m.assetService.Upload(ctx, []byte(body), asset.UploadOpts{
			Namespace:  namespace,
			Filename:   filename,
			MimeType:   mimeType,
			OwnerScope: "user",
			OwnerID:    ownerID,
			Tags: map[string]string{
				"source_kind": "agent_artifact",
				"session_id":  session.ID,
				"message_ts":  createdAt,
			},
		})
		if err != nil {
			m.logger.Warn("assistant artifact 持久化失败",
				zap.String("session_id", session.ID),
				zap.String("artifact_type", artifactType),
				zap.Error(err))
			continue
		}
		title := strings.TrimSpace(attrs["title"])
		if title == "" {
			title = "文档"
		}
		out = append(out, AssistantArtifactManifest{
			URI:         uri.String(),
			Title:       title,
			Type:        artifactType,
			Language:    strings.TrimSpace(attrs["language"]),
			MimeType:    mimeType,
			Size:        int64(len(body)),
			ContentHash: contentHash,
		})
	}
	if len(out) == 0 {
		return ""
	}
	return marshalAssistantArtifacts(out)
}

func marshalAssistantArtifacts(artifacts []AssistantArtifactManifest) string {
	data, err := json.Marshal(artifacts)
	if err != nil {
		return ""
	}
	return string(data)
}

func parseArtifactAttrs(raw string) map[string]string {
	out := map[string]string{}
	for _, match := range artifactAttrRe.FindAllStringSubmatch(raw, -1) {
		if len(match) == 3 {
			out[match[1]] = match[2]
		}
	}
	return out
}

func normalizeArtifactType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "html", "code", "ppt":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "markdown"
	}
}

func artifactMimeType(artifactType, language string) string {
	switch artifactType {
	case "html":
		return "text/html; charset=utf-8"
	case "code":
		switch strings.ToLower(strings.TrimSpace(language)) {
		case "json":
			return "application/json"
		case "csv":
			return "text/csv; charset=utf-8"
		default:
			return "text/plain; charset=utf-8"
		}
	default:
		return "text/markdown; charset=utf-8"
	}
}

func artifactFilename(title, artifactType, language string, index int) string {
	base := strings.TrimSpace(title)
	if base == "" {
		base = "artifact"
	}
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '_'
		}
	}, base)
	base = strings.Trim(base, "_.")
	if base == "" {
		base = "artifact"
	}
	ext := "md"
	switch artifactType {
	case "html":
		ext = "html"
	case "code":
		ext = strings.Trim(strings.ToLower(strings.TrimSpace(language)), ".")
		if ext == "" || len(ext) > 16 {
			ext = "txt"
		}
	}
	return base + "-" + hex.EncodeToString([]byte{byte(index)}) + "." + ext
}

func artifactOwnerID(ctx context.Context, session *SessionState) string {
	if userID := auth.UserIDFrom(ctx); userID != "" {
		return userID
	}
	if session != nil {
		if ownerID := strings.TrimSpace(session.UserID); ownerID != "" {
			return ownerID
		}
	}
	return "local"
}
