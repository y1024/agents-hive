package kb

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var markdownImagePattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

func (s *Service) IngestMarkdownWithAssets(ctx context.Context, scope Scope, input IngestMarkdownWithAssetsInput) (*Document, error) {
	if s == nil || s.assetUploader == nil {
		return nil, ErrUnsupportedAsset
	}
	documentID := StableDocumentID(input.NamespaceID, hashDocument(input.Content+"\x00assets\x00"+hashMarkdownAssets(input.Assets)))
	content, refs, err := s.rewriteMarkdownAssets(ctx, scope, input, documentID)
	if err != nil {
		return nil, err
	}
	input.Content = content
	doc, err := s.ingestMarkdown(ctx, scope, input.IngestMarkdownInput, documentID)
	if err != nil {
		return nil, err
	}
	nodes, _, err := s.store.GetStructureForManagement(ctx, ManagementScope{
		DomainID:   doc.DomainID,
		OwnerScope: doc.OwnerScope,
		OwnerID:    doc.OwnerID,
		Now:        scope.Now,
	}, doc.ID, true)
	if err != nil {
		return nil, err
	}
	relations := assignAssetsToNodes(*doc, nodes, refs)
	if err := s.store.DeleteNodeAssetsForDocument(ctx, ManagementScope{
		DomainID:   doc.DomainID,
		OwnerScope: doc.OwnerScope,
		OwnerID:    doc.OwnerID,
		Now:        scope.Now,
	}, doc.ID); err != nil {
		return nil, err
	}
	if len(relations) > 0 {
		if err := s.store.SaveNodeAssets(ctx, relations); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

func hashMarkdownAssets(assets map[string]MarkdownAsset) string {
	if len(assets) == 0 {
		return ""
	}
	keys := make([]string, 0, len(assets))
	for key := range assets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		asset := assets[key]
		sum := sha256.Sum256(asset.Data)
		_, _ = h.Write([]byte(strings.TrimSpace(key)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(strings.TrimSpace(asset.Filename)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(strings.TrimSpace(asset.MimeType)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(hex.EncodeToString(sum[:])))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

type rewrittenAssetRef struct {
	URI         string
	ContentHash string
	MimeType    string
	AltText     string
	Caption     string
	Line        int
	Page        int
}

func (s *Service) rewriteMarkdownAssets(ctx context.Context, scope Scope, input IngestMarkdownWithAssetsInput, documentID string) (string, []rewrittenAssetRef, error) {
	lines := strings.Split(input.Content, "\n")
	refs := make([]rewrittenAssetRef, 0)
	inFence := false
	fenceMarker := ""
	assetNamespace := fmt.Sprintf("kb/%s/%s/%s/%s", scope.OwnerScope, scope.OwnerID, input.NamespaceID, documentID)
	currentPage := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inFence {
				inFence = true
				fenceMarker = marker
			} else if marker == fenceMarker {
				inFence = false
				fenceMarker = ""
			}
			continue
		}
		if inFence {
			continue
		}
		if page := pageAnchorFromLine(line); page > 0 {
			currentPage = page
		}
		matches := markdownImagePattern.FindAllStringSubmatchIndex(line, -1)
		if len(matches) == 0 {
			continue
		}
		var b strings.Builder
		last := 0
		for _, match := range matches {
			alt := line[match[2]:match[3]]
			target := strings.TrimSpace(line[match[4]:match[5]])
			if strings.HasPrefix(target, "asset://") {
				continue
			}
			data, filename, mimeType, err := resolveMarkdownImageData(target, input.Assets)
			if err != nil {
				return "", nil, err
			}
			sum := sha256.Sum256(data)
			contentHash := hex.EncodeToString(sum[:])
			uri, _, err := s.assetUploader.Upload(ctx, data, AssetUploadOptions{
				Namespace:  assetNamespace,
				Filename:   filename,
				MimeType:   mimeType,
				OwnerScope: string(scope.OwnerScope),
				OwnerID:    scope.OwnerID,
				Tags: map[string]string{
					"source_kind":     "kb_document_image",
					"kb_namespace_id": input.NamespaceID,
					"kb_document_id":  documentID,
					"domain_id":       scope.DomainID,
				},
			})
			if err != nil {
				return "", nil, err
			}
			b.WriteString(line[last:match[0]])
			b.WriteString("![")
			b.WriteString(alt)
			b.WriteString("](")
			b.WriteString(uri)
			b.WriteString(")")
			last = match[1]
			refs = append(refs, rewrittenAssetRef{
				URI:         uri,
				ContentHash: contentHash,
				MimeType:    mimeType,
				AltText:     alt,
				Line:        i + 1,
				Page:        currentPage,
			})
		}
		if last > 0 {
			b.WriteString(line[last:])
			lines[i] = b.String()
		}
	}
	return strings.Join(lines, "\n"), refs, nil
}

func resolveMarkdownImageData(target string, assets map[string]MarkdownAsset) ([]byte, string, string, error) {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return nil, "", "", ErrUnsupportedAsset
	}
	if strings.HasPrefix(target, "data:") {
		header, raw, ok := strings.Cut(target, ",")
		if !ok || !strings.Contains(header, ";base64") {
			return nil, "", "", ErrUnsupportedAsset
		}
		mimeType := strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:")
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, "", "", ErrUnsupportedAsset
		}
		exts, _ := mime.ExtensionsByType(mimeType)
		ext := ".bin"
		if len(exts) > 0 {
			ext = exts[0]
		}
		return data, "inline" + ext, mimeType, nil
	}
	asset, ok := assets[target]
	if !ok {
		asset, ok = assets[strings.TrimPrefix(target, "./")]
	}
	if !ok {
		asset, ok = assets[filepath.Base(target)]
	}
	if !ok || len(asset.Data) == 0 {
		return nil, "", "", ErrUnsupportedAsset
	}
	filename := strings.TrimSpace(asset.Filename)
	if filename == "" {
		filename = filepath.Base(target)
	}
	mimeType := strings.TrimSpace(asset.MimeType)
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(filename))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return asset.Data, filename, mimeType, nil
}

func assignAssetsToNodes(doc Document, nodes []TreeNode, refs []rewrittenAssetRef) []NodeAsset {
	out := make([]NodeAsset, 0, len(refs))
	for i, ref := range refs {
		nodeID := ""
		for _, node := range nodes {
			if ref.Line >= node.StartLine && (node.EndLine == 0 || ref.Line <= node.EndLine) {
				nodeID = node.ID
			}
		}
		out = append(out, NodeAsset{
			ID:          hashText(doc.ID + "\x00" + ref.URI + "\x00" + fmt.Sprintf("%d", i)),
			DomainID:    doc.DomainID,
			NamespaceID: doc.NamespaceID,
			DocumentID:  doc.ID,
			NodeID:      nodeID,
			Line:        ref.Line,
			Page:        ref.Page,
			OwnerScope:  doc.OwnerScope,
			OwnerID:     doc.OwnerID,
			AssetURI:    ref.URI,
			ContentHash: ref.ContentHash,
			MimeType:    ref.MimeType,
			AltText:     ref.AltText,
			Caption:     ref.Caption,
			CreatedAt:   time.Now(),
		})
	}
	return out
}
