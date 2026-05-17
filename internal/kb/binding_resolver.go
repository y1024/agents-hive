package kb

import (
	"context"
	"time"
)

type BindingResolver interface {
	Resolve(ctx context.Context, input BindingResolveInput) ([]string, error)
}

type StoreBindingResolver struct {
	store Store
}

func NewBindingResolver(store Store) *StoreBindingResolver {
	return &StoreBindingResolver{store: store}
}

func (r *StoreBindingResolver) Resolve(ctx context.Context, input BindingResolveInput) ([]string, error) {
	if r == nil || r.store == nil {
		return nil, ErrInvalidInput
	}
	if input.DomainID == "" || input.OwnerScope == "" || input.OwnerID == "" {
		return nil, ErrInvalidScope
	}
	if input.Now.IsZero() {
		input.Now = time.Now()
	}
	bindings, err := r.store.ListBindings(ctx, input)
	if err != nil {
		return nil, err
	}
	namespaceIDs := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Active(input.Now) {
			namespaceIDs = append(namespaceIDs, binding.NamespaceID)
		}
	}
	namespaceIDs = uniqueNonEmpty(namespaceIDs)
	if len(namespaceIDs) == 0 {
		return nil, ErrNoKBBinding
	}
	return namespaceIDs, nil
}
