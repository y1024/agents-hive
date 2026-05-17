package kb

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyEvidenceRefsOnlyAcceptsCurrentTurnLedger(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	doc := seedDocument(t, store, now, "ns-1")
	service := NewService(store)
	scope := testEvidenceScope(now)

	nodes, _, err := store.GetSectionText(context.Background(), testScope(now, "ns-1"), doc.ID, []string{"0000"})
	require.NoError(t, err)
	ref, err := service.RecordEvidence(context.Background(), scope, doc, nodes[0], "Refund Policy")
	require.NoError(t, err)

	refs, err := service.CurrentTurnEvidence(context.Background(), scope)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, ref.Token, refs[0].Token)
	assert.True(t, refs[0].Verified)
	assert.Equal(t, nodes[0].StartPage, refs[0].StartPage)
	assert.Equal(t, nodes[0].EndPage, refs[0].EndPage)

	verified, violations, err := service.VerifyEvidenceRefs(context.Background(), scope, []EvidenceRef{{Token: ref.Token}})
	require.NoError(t, err)
	require.Len(t, verified, 1)
	assert.True(t, verified[0].Verified)
	assert.Equal(t, nodes[0].StartPage, verified[0].StartPage)
	assert.Equal(t, nodes[0].EndPage, verified[0].EndPage)
	assert.Empty(t, violations)

	wrongTurn := scope
	wrongTurn.TurnID = "turn-2"
	verified, violations, err = service.VerifyEvidenceRefs(context.Background(), wrongTurn, []EvidenceRef{{Token: ref.Token}})
	require.NoError(t, err)
	assert.Empty(t, verified)
	require.Len(t, violations, 1)
	assert.Equal(t, "not_in_current_turn_ledger", violations[0].Reason)
}

func TestVerifyEvidenceRefsRejectsMetadataMismatch(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	doc := seedDocument(t, store, now, "ns-1")
	service := NewService(store)
	scope := testEvidenceScope(now)
	nodes, _, err := store.GetSectionText(context.Background(), testScope(now, "ns-1"), doc.ID, []string{"0000"})
	require.NoError(t, err)
	ref, err := service.RecordEvidence(context.Background(), scope, doc, nodes[0], "Refund Policy")
	require.NoError(t, err)

	verified, violations, err := service.VerifyEvidenceRefs(context.Background(), scope, []EvidenceRef{{
		Token:      ref.Token,
		DocumentID: "other-doc",
		NodeID:     ref.NodeID,
	}})
	require.NoError(t, err)
	assert.Empty(t, verified)
	require.Len(t, violations, 1)
	assert.Equal(t, "metadata_mismatch", violations[0].Reason)
}

func TestEvidenceScopeRequiresSessionTurnTraceAndScope(t *testing.T) {
	err := ValidateEvidenceScope(EvidenceScope{SessionID: "s", TurnID: "t", TraceID: "tr"})
	require.ErrorIs(t, err, ErrInvalidScope)
}

func TestEvidenceRefJSONUsesStableSnakeCaseContract(t *testing.T) {
	raw, err := json.Marshal(EvidenceRef{
		Token:           "kbref-token",
		NamespaceID:     "ns-1",
		DocumentID:      "doc-1",
		DocumentVersion: "v1",
		NodeID:          "0001",
		NodePath:        "1.2",
		StartPage:       5,
		EndPage:         7,
		CitationText:    "Refund",
		Verified:        true,
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"token":"kbref-token",
		"namespace_id":"ns-1",
		"doc_id":"doc-1",
		"document_version":"v1",
		"node_id":"0001",
		"node_path":"1.2",
		"start_page":5,
		"end_page":7,
		"citation_text":"Refund",
		"verified":true
	}`, string(raw))
}
