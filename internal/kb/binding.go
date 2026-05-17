package kb

import "time"

func (b Binding) Active(now time.Time) bool {
	if now.IsZero() {
		now = time.Now()
	}
	if !b.Enabled {
		return false
	}
	if b.EffectiveAt.After(now) {
		return false
	}
	if b.ExpiresAt != nil && !b.ExpiresAt.After(now) {
		return false
	}
	return true
}

func bindingTargets(input BindingResolveInput) map[BindingType]string {
	targets := make(map[BindingType]string)
	if input.AgentID != "" {
		targets[BindingTypeAgent] = input.AgentID
	}
	if input.DomainID != "" {
		targets[BindingTypeDomain] = input.DomainID
	}
	if input.SessionTemplateID != "" {
		targets[BindingTypeSessionTemplate] = input.SessionTemplateID
	}
	if input.SessionID != "" {
		targets[BindingTypeSession] = input.SessionID
	}
	if input.TenantID != "" {
		targets[BindingTypeTenant] = input.TenantID
	}
	if input.UserID != "" {
		targets[BindingTypeUser] = input.UserID
	}
	if input.OwnerScope == OwnerScopeSystem {
		targets[BindingTypeSystem] = "system"
	}
	return targets
}
