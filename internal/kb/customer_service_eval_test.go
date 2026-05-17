package kb

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCustomerServiceEvalBindingUsesOnlyConfiguredNamespaces(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedCustomerServiceNamespace(t, store, now, "refund_policy", "tenant-1")
	seedCustomerServiceNamespace(t, store, now, "product_faq", "tenant-1")
	seedCustomerServiceNamespace(t, store, now, "internal_only", "tenant-1")
	seedCustomerServiceBinding(t, store, now, "b-refund", "refund_policy", "tenant-1", BindingTypeDomain, "customer_service")
	seedCustomerServiceBinding(t, store, now, "b-faq", "product_faq", "tenant-1", BindingTypeAgent, "support-agent")
	seedCustomerServiceBinding(t, store, now, "b-internal", "internal_only", "tenant-1", BindingTypeAgent, "other-agent")

	got, err := NewBindingResolver(store).Resolve(context.Background(), BindingResolveInput{
		DomainID:   "customer_service",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		AgentID:    "support-agent",
		TenantID:   "tenant-1",
		UserID:     "buyer-1",
		Now:        now,
	})
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	want := map[string]bool{"refund_policy": true, "product_faq": true}
	if len(got) != len(want) {
		t.Fatalf("namespaces = %v, want refund_policy/product_faq only", got)
	}
	for _, namespaceID := range got {
		if !want[namespaceID] {
			t.Fatalf("unexpected namespace %q in customer_service binding result: %v", namespaceID, got)
		}
	}
}

func TestCustomerServiceEvalRejectsUnboundExpiredAndCrossOwnerNamespaces(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore()
	seedCustomerServiceNamespace(t, store, now, "expired_policy", "tenant-1")
	expiredAt := now.Add(-time.Minute)
	if err := store.SaveBinding(context.Background(), Binding{
		ID:            "b-expired",
		DomainID:      "customer_service",
		OwnerScope:    OwnerScopeTenant,
		OwnerID:       "tenant-1",
		NamespaceID:   "expired_policy",
		BindingType:   BindingTypeDomain,
		BindingTarget: "customer_service",
		Enabled:       true,
		EffectiveAt:   now.Add(-time.Hour),
		ExpiresAt:     &expiredAt,
	}); err != nil {
		t.Fatalf("SaveBinding = %v", err)
	}
	seedCustomerServiceNamespace(t, store, now, "cross_owner", "tenant-2")
	if err := store.SaveBinding(context.Background(), Binding{
		ID:            "b-cross",
		DomainID:      "customer_service",
		OwnerScope:    OwnerScopeTenant,
		OwnerID:       "tenant-2",
		NamespaceID:   "cross_owner",
		BindingType:   BindingTypeDomain,
		BindingTarget: "customer_service",
		Enabled:       true,
		EffectiveAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveBinding = %v", err)
	}

	_, err := NewBindingResolver(store).Resolve(context.Background(), BindingResolveInput{
		DomainID:   "customer_service",
		OwnerScope: OwnerScopeTenant,
		OwnerID:    "tenant-1",
		TenantID:   "tenant-1",
		UserID:     "buyer-1",
		Now:        now,
	})
	if !errors.Is(err, ErrNoKBBinding) {
		t.Fatalf("Resolve err = %v, want ErrNoKBBinding", err)
	}
}

func seedCustomerServiceNamespace(t *testing.T, store *MemoryStore, now time.Time, id, ownerID string) {
	t.Helper()
	if err := store.SaveNamespace(context.Background(), Namespace{
		ID:            id,
		Name:          id,
		DomainID:      "customer_service",
		OwnerScope:    OwnerScopeTenant,
		OwnerID:       ownerID,
		IndexStrategy: "tree",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveNamespace(%s) = %v", id, err)
	}
}

func seedCustomerServiceBinding(t *testing.T, store *MemoryStore, now time.Time, id, namespaceID, ownerID string, typ BindingType, target string) {
	t.Helper()
	if err := store.SaveBinding(context.Background(), Binding{
		ID:            id,
		DomainID:      "customer_service",
		OwnerScope:    OwnerScopeTenant,
		OwnerID:       ownerID,
		NamespaceID:   namespaceID,
		BindingType:   typ,
		BindingTarget: target,
		Enabled:       true,
		EffectiveAt:   now.Add(-time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveBinding(%s) = %v", id, err)
	}
}
