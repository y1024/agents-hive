package kb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStoreRejectsEmptyScope(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.ListDocuments(context.Background(), Scope{
		DomainID:     "",
		OwnerScope:   OwnerScopeTenant,
		OwnerID:      "tenant-1",
		NamespaceIDs: []string{"ns-1"},
		Now:          time.Now(),
	}, DocumentQuery{})
	require.ErrorIs(t, err, ErrInvalidScope)
}

func TestValidateScopeNarrowingMustBeBound(t *testing.T) {
	err := ValidateScope(Scope{
		DomainID:           "domain-1",
		OwnerScope:         OwnerScopeTenant,
		OwnerID:            "tenant-1",
		NamespaceIDs:       []string{"ns-1"},
		NamespaceNarrowing: "ns-2",
	})
	require.ErrorIs(t, err, ErrNamespaceNotBound)
}

func TestMemoryStoreListDocumentsFiltersScopeStatusAndTime(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	active := seedDocument(t, store, now, "ns-1")

	archived := active
	archived.ID = "archived"
	archived.ContentHash = "archived-hash"
	archived.Status = DocumentArchived
	require.NoError(t, store.SaveDocument(context.Background(), archived, nil))

	expiredAt := now.Add(-time.Minute)
	expired := active
	expired.ID = "expired"
	expired.ContentHash = "expired-hash"
	expired.ExpiresAt = &expiredAt
	require.NoError(t, store.SaveDocument(context.Background(), expired, nil))

	foreignOwner := active
	foreignOwner.ID = "foreign-owner"
	foreignOwner.ContentHash = "foreign-hash"
	foreignOwner.OwnerID = "tenant-2"
	require.NoError(t, store.SaveDocument(context.Background(), foreignOwner, nil))

	docs, err := store.ListDocuments(context.Background(), testScope(now, "ns-1"), DocumentQuery{})
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, active.ID, docs[0].ID)
}

func TestMemoryStoreStructureStripsText(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedNamespace(t, store, now, "ns-1")
	doc := seedDocument(t, store, now, "ns-1")

	nodes, err := store.GetStructure(context.Background(), testScope(now, "ns-1"), doc.ID)
	require.NoError(t, err)
	require.NotEmpty(t, nodes)
	for _, node := range nodes {
		assert.Empty(t, node.Text)
	}
}

func TestDocMetaNoBindingReturnsRecoverableEmptyResult(t *testing.T) {
	service := NewService(NewMemoryStore())
	result, err := service.DocMeta(context.Background(), Scope{
		DomainID:   "domain-1",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		Now:        time.Now(),
	}, DocMetaInput{})
	require.NoError(t, err)
	assert.True(t, result.NoKBBound)
}

func TestRecoverableErrors(t *testing.T) {
	assert.True(t, IsRecoverable(ErrNoKBBinding))
	assert.False(t, IsRecoverable(errors.New("boom")))
}
