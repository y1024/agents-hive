package kb

import (
	"context"
	"strings"
	"time"
)

type CreateNamespaceInput struct {
	Name                   string `json:"name"`
	DomainID               string `json:"domain_id"`
	IndexStrategy          string `json:"index_strategy,omitempty"`
	ThinningEnabled        bool   `json:"thinning_enabled,omitempty"`
	ThinningTokenThreshold int    `json:"thinning_token_threshold,omitempty"`
	SummaryTokenThreshold  int    `json:"summary_token_threshold,omitempty"`
	SummaryModel           string `json:"summary_model,omitempty"`
}

type ListNamespacesInput struct {
	DomainID string
	Query    string
	Limit    int
}

type ListDocumentsInput struct {
	NamespaceID string
	Query       string
	Status      DocumentStatus
	Limit       int
}

type CreateBindingInput struct {
	NamespaceID   string
	DomainID      string
	BindingType   BindingType
	BindingTarget string
	EffectiveAt   time.Time
	ExpiresAt     *time.Time
	CreatedBy     string
}

type UpdateBindingInput struct {
	Enabled       *bool
	BindingTarget *string
	EffectiveAt   *time.Time
	ExpiresAt     **time.Time
}

type EffectiveBinding struct {
	Binding     Binding
	NamespaceID string
}

func (s *Service) SaveNodeAssets(ctx context.Context, assets []NodeAsset) error {
	if s == nil || s.store == nil {
		return ErrInvalidInput
	}
	return s.store.SaveNodeAssets(ctx, assets)
}

func (s *Service) ListNodeAssets(ctx context.Context, scope ManagementScope, documentID string, nodeIDs []string) ([]NodeAsset, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	return s.store.ListNodeAssets(ctx, scope, strings.TrimSpace(documentID), nodeIDs)
}

func (s *Service) CreateNamespace(ctx context.Context, scope ManagementScope, input CreateNamespaceInput) (*Namespace, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	if err := ValidateManagementScope(scope); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(input.Name)
	domainID := strings.TrimSpace(input.DomainID)
	if name == "" || domainID == "" || domainID != scope.DomainID {
		return nil, ErrInvalidInput
	}
	now := scope.Now
	if now.IsZero() {
		now = time.Now()
	}
	namespace := Namespace{
		ID:                     "kbns_" + hashText(string(scope.OwnerScope) + "\x00" + scope.OwnerID + "\x00" + domainID + "\x00" + name)[:16],
		Name:                   name,
		DomainID:               domainID,
		OwnerScope:             scope.OwnerScope,
		OwnerID:                scope.OwnerID,
		IndexStrategy:          strings.TrimSpace(input.IndexStrategy),
		ThinningEnabled:        input.ThinningEnabled,
		ThinningTokenThreshold: input.ThinningTokenThreshold,
		SummaryTokenThreshold:  input.SummaryTokenThreshold,
		SummaryModel:           strings.TrimSpace(input.SummaryModel),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	if namespace.IndexStrategy == "" {
		namespace.IndexStrategy = "markdown_tree"
	}
	if err := s.store.SaveNamespace(ctx, namespace); err != nil {
		return nil, err
	}
	return &namespace, nil
}

func (s *Service) ListNamespaces(ctx context.Context, scope ManagementScope, input ListNamespacesInput) ([]Namespace, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	if err := ValidateManagementScope(scope); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.DomainID) != "" && strings.TrimSpace(input.DomainID) != scope.DomainID {
		return nil, ErrInvalidInput
	}
	return s.store.ListNamespaces(ctx, NamespaceQuery{
		DomainID:   scope.DomainID,
		OwnerScope: scope.OwnerScope,
		OwnerID:    scope.OwnerID,
		Query:      input.Query,
		Limit:      input.Limit,
	})
}

func (s *Service) ListDocumentsForManagement(ctx context.Context, scope ManagementScope, input ListDocumentsInput) ([]Document, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	if err := ValidateManagementScope(scope); err != nil {
		return nil, err
	}
	if input.NamespaceID != "" {
		namespace, err := s.store.GetNamespace(ctx, input.NamespaceID)
		if err != nil {
			return nil, err
		}
		if namespace.DomainID != scope.DomainID || namespace.OwnerScope != scope.OwnerScope || namespace.OwnerID != scope.OwnerID {
			return nil, ErrNotFound
		}
	}
	return s.store.ListDocumentsForManagement(ctx, scope, input.NamespaceID, DocumentQuery{
		Query:  input.Query,
		Limit:  input.Limit,
		Status: input.Status,
	})
}

func (s *Service) DocumentTreeForManagement(ctx context.Context, scope ManagementScope, documentID string, includeText bool) ([]TreeNode, *Document, error) {
	if s == nil || s.store == nil {
		return nil, nil, ErrInvalidInput
	}
	return s.store.GetStructureForManagement(ctx, scope, strings.TrimSpace(documentID), includeText)
}

func (s *Service) ArchiveDocument(ctx context.Context, scope ManagementScope, documentID string) error {
	if s == nil || s.store == nil {
		return ErrInvalidInput
	}
	return s.store.UpdateDocumentStatus(ctx, scope, strings.TrimSpace(documentID), DocumentArchived)
}

func (s *Service) CreateBinding(ctx context.Context, scope ManagementScope, input CreateBindingInput) (*Binding, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	if err := ValidateManagementScope(scope); err != nil {
		return nil, err
	}
	if err := validateBindingInput(input.NamespaceID, input.DomainID, input.BindingType, input.BindingTarget); err != nil {
		return nil, err
	}
	if input.DomainID != scope.DomainID {
		return nil, ErrInvalidInput
	}
	namespace, err := s.store.GetNamespace(ctx, input.NamespaceID)
	if err != nil {
		return nil, err
	}
	if namespace.DomainID != scope.DomainID || namespace.OwnerScope != scope.OwnerScope || namespace.OwnerID != scope.OwnerID {
		return nil, ErrNotFound
	}
	now := scope.Now
	if now.IsZero() {
		now = time.Now()
	}
	effectiveAt := input.EffectiveAt
	if effectiveAt.IsZero() {
		effectiveAt = now
	}
	binding := Binding{
		ID:            "kbb_" + hashText(string(scope.OwnerScope) + "\x00" + scope.OwnerID + "\x00" + input.DomainID + "\x00" + input.NamespaceID + "\x00" + string(input.BindingType) + "\x00" + input.BindingTarget)[:20],
		DomainID:      input.DomainID,
		OwnerScope:    scope.OwnerScope,
		OwnerID:       scope.OwnerID,
		NamespaceID:   input.NamespaceID,
		BindingType:   input.BindingType,
		BindingTarget: input.BindingTarget,
		Enabled:       true,
		EffectiveAt:   effectiveAt,
		ExpiresAt:     input.ExpiresAt,
		CreatedBy:     strings.TrimSpace(input.CreatedBy),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.store.SaveBinding(ctx, binding); err != nil {
		return nil, err
	}
	return &binding, nil
}

func (s *Service) ListBindingsForManagement(ctx context.Context, scope ManagementScope, query BindingQuery) ([]Binding, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	if err := ValidateManagementScope(scope); err != nil {
		return nil, err
	}
	query.DomainID = scope.DomainID
	query.OwnerScope = scope.OwnerScope
	query.OwnerID = scope.OwnerID
	return s.store.ListBindingsForManagement(ctx, query)
}

func (s *Service) UpdateBinding(ctx context.Context, scope ManagementScope, bindingID string, input UpdateBindingInput) (*Binding, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	bindings, err := s.store.ListBindingsForManagement(ctx, BindingQuery{
		DomainID:   scope.DomainID,
		OwnerScope: scope.OwnerScope,
		OwnerID:    scope.OwnerID,
	})
	if err != nil {
		return nil, err
	}
	var binding *Binding
	for i := range bindings {
		if bindings[i].ID == strings.TrimSpace(bindingID) {
			cp := bindings[i]
			binding = &cp
			break
		}
	}
	if binding == nil {
		return nil, ErrNotFound
	}
	if input.Enabled != nil {
		binding.Enabled = *input.Enabled
	}
	if input.BindingTarget != nil {
		target := strings.TrimSpace(*input.BindingTarget)
		if target == "" {
			return nil, ErrInvalidInput
		}
		binding.BindingTarget = target
	}
	if input.EffectiveAt != nil {
		binding.EffectiveAt = *input.EffectiveAt
	}
	if input.ExpiresAt != nil {
		binding.ExpiresAt = *input.ExpiresAt
	}
	binding.UpdatedAt = time.Now()
	if err := s.store.SaveBinding(ctx, *binding); err != nil {
		return nil, err
	}
	return binding, nil
}

func (s *Service) DisableBinding(ctx context.Context, scope ManagementScope, bindingID string) (*Binding, error) {
	enabled := false
	return s.UpdateBinding(ctx, scope, bindingID, UpdateBindingInput{Enabled: &enabled})
}

func (s *Service) EffectiveBindings(ctx context.Context, input BindingResolveInput) ([]EffectiveBinding, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	bindings, err := s.store.ListBindings(ctx, input)
	if err != nil {
		return nil, err
	}
	out := make([]EffectiveBinding, 0, len(bindings))
	for _, binding := range bindings {
		if !binding.Active(input.Now) {
			continue
		}
		out = append(out, EffectiveBinding{Binding: binding, NamespaceID: binding.NamespaceID})
	}
	return out, nil
}

func validateBindingInput(namespaceID, domainID string, bindingType BindingType, bindingTarget string) error {
	if strings.TrimSpace(namespaceID) == "" || strings.TrimSpace(domainID) == "" || strings.TrimSpace(bindingTarget) == "" {
		return ErrInvalidInput
	}
	switch bindingType {
	case BindingTypeAgent, BindingTypeDomain, BindingTypeSessionTemplate, BindingTypeSession, BindingTypeTenant, BindingTypeUser, BindingTypeSystem:
		return nil
	default:
		return ErrInvalidInput
	}
}
