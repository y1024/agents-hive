package kb

import (
	"context"
	"time"
)

type contextKey string

const (
	scopeContextKey         contextKey = "kb.scope"
	evidenceScopeContextKey contextKey = "kb.evidence_scope"
	bindingInputContextKey  contextKey = "kb.binding_input"
)

func ContextWithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeContextKey, scope)
}

func ScopeFromContext(ctx context.Context) (Scope, bool) {
	scope, ok := ctx.Value(scopeContextKey).(Scope)
	return scope, ok
}

func ContextWithEvidenceScope(ctx context.Context, scope EvidenceScope) context.Context {
	return context.WithValue(ctx, evidenceScopeContextKey, scope)
}

func EvidenceScopeFromContext(ctx context.Context) (EvidenceScope, bool) {
	scope, ok := ctx.Value(evidenceScopeContextKey).(EvidenceScope)
	return scope, ok
}

func ContextWithBindingResolveInput(ctx context.Context, input BindingResolveInput) context.Context {
	return context.WithValue(ctx, bindingInputContextKey, input)
}

func BindingResolveInputFromContext(ctx context.Context) (BindingResolveInput, bool) {
	input, ok := ctx.Value(bindingInputContextKey).(BindingResolveInput)
	return input, ok
}

func ScopeFromBindingInput(input BindingResolveInput, namespaceIDs []string, narrowing string) Scope {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	return Scope{
		DomainID:           input.DomainID,
		OwnerScope:         input.OwnerScope,
		OwnerID:            input.OwnerID,
		NamespaceIDs:       uniqueNonEmpty(namespaceIDs),
		NamespaceNarrowing: narrowing,
		Now:                now,
	}
}
