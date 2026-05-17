package kb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeCounter struct{}

func (fakeCounter) CountTokens(text string) int {
	if text == "" {
		return 0
	}
	return 1
}

type fakeSummary struct {
	calls int
	err   error
}

func (f *fakeSummary) Summarize(ctx context.Context, text string, model string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.calls++
	return "summary:" + text, nil
}

func testScope(now time.Time, namespaces ...string) Scope {
	return Scope{
		DomainID:     "domain-1",
		OwnerScope:   OwnerScopeTenant,
		OwnerID:      "tenant-1",
		NamespaceIDs: namespaces,
		Now:          now,
	}
}

func testEvidenceScope(now time.Time) EvidenceScope {
	return EvidenceScope{
		SessionID:  "session-1",
		TurnID:     "turn-1",
		TraceID:    "trace-1",
		ToolCallID: "tool-1",
		DomainID:   "domain-1",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		Now:        now,
	}
}

func seedNamespace(t *testing.T, store *MemoryStore, now time.Time, id string) Namespace {
	t.Helper()
	namespace := Namespace{
		ID:                    id,
		Name:                  id,
		DomainID:              "domain-1",
		OwnerScope:            OwnerScopeTenant,
		OwnerID:               "tenant-1",
		IndexStrategy:         "tree",
		SummaryTokenThreshold: 100,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	require.NoError(t, store.SaveNamespace(context.Background(), namespace))
	return namespace
}

func seedDocument(t *testing.T, store *MemoryStore, now time.Time, namespaceID string) Document {
	t.Helper()
	doc := Document{
		ID:          "doc-" + namespaceID,
		NamespaceID: namespaceID,
		DomainID:    "domain-1",
		OwnerScope:  OwnerScopeTenant,
		OwnerID:     "tenant-1",
		Title:       "Refund Policy",
		Description: "Policy description",
		ContentHash: "hash-" + namespaceID,
		Version:     "v1",
		Status:      DocumentActive,
		EffectiveAt: now.Add(-time.Hour),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	parent := "0000"
	nodes := []TreeNode{
		{
			ID:          "0000",
			DocumentID:  doc.ID,
			NamespaceID: namespaceID,
			DomainID:    "domain-1",
			OwnerScope:  OwnerScopeTenant,
			OwnerID:     "tenant-1",
			NodePath:    "1",
			Title:       "Refund Policy",
			Level:       1,
			Text:        "# Refund Policy\nIntro",
			Summary:     "intro",
			StartLine:   1,
			EndLine:     2,
			StartPage:   1,
			EndPage:     2,
			ContentHash: "node-hash-1",
			CreatedAt:   now,
		},
		{
			ID:           "0001",
			DocumentID:   doc.ID,
			NamespaceID:  namespaceID,
			DomainID:     "domain-1",
			OwnerScope:   OwnerScopeTenant,
			OwnerID:      "tenant-1",
			ParentNodeID: &parent,
			NodePath:     "1.1",
			Title:        "7-day Return",
			Level:        2,
			Text:         "## 7-day Return\nConditions",
			Summary:      "conditions",
			StartLine:    3,
			EndLine:      4,
			StartPage:    5,
			EndPage:      6,
			ContentHash:  "node-hash-2",
			CreatedAt:    now,
		},
	}
	require.NoError(t, store.SaveDocument(context.Background(), doc, nodes))
	return doc
}

func seedBinding(t *testing.T, store *MemoryStore, now time.Time, id, namespaceID string, typ BindingType, target string) {
	t.Helper()
	require.NoError(t, store.SaveBinding(context.Background(), Binding{
		ID:            id,
		DomainID:      "domain-1",
		OwnerScope:    OwnerScopeTenant,
		OwnerID:       "tenant-1",
		NamespaceID:   namespaceID,
		BindingType:   typ,
		BindingTarget: target,
		Enabled:       true,
		EffectiveAt:   now.Add(-time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}))
}
