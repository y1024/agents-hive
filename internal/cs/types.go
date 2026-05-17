package cs

import (
	"encoding/json"
	"time"
)

const (
	DefaultDomainID   = "customer_service"
	DefaultOwnerScope = "user"
	DefaultOwnerID    = "local"
)

type EscalationStatus string

const (
	EscalationOpen     EscalationStatus = "open"
	EscalationQueued   EscalationStatus = "queued"
	EscalationSent     EscalationStatus = "sent"
	EscalationFailed   EscalationStatus = "failed"
	EscalationCanceled EscalationStatus = "canceled"
)

type OutboxStatus string

const (
	OutboxQueued OutboxStatus = "queued"
	OutboxSent   OutboxStatus = "sent"
	OutboxFailed OutboxStatus = "failed"
)

type SessionState string

const (
	SessionStateInitial         SessionState = "initial"
	SessionStateAIHandling      SessionState = "ai_handling"
	SessionStateEscalatePending SessionState = "escalate_pending"
	SessionStateHumanHandling   SessionState = "human_handling"
	SessionStateResolved        SessionState = "resolved"
)

type OwnerScope struct {
	DomainID   string `json:"domain_id"`
	OwnerScope string `json:"owner_scope"`
	OwnerID    string `json:"owner_id"`
}

func (s OwnerScope) Normalized() OwnerScope {
	if s.DomainID == "" {
		s.DomainID = DefaultDomainID
	}
	if s.OwnerScope == "" {
		s.OwnerScope = DefaultOwnerScope
	}
	if s.OwnerID == "" {
		s.OwnerID = DefaultOwnerID
	}
	return s
}

type Session struct {
	ID              string          `json:"id"`
	Scope           OwnerScope      `json:"scope"`
	State           SessionState    `json:"state"`
	ExternalUserRef string          `json:"external_user_ref,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	ResolvedAt      *time.Time      `json:"resolved_at,omitempty"`
}

type Escalation struct {
	ID         string           `json:"id"`
	Scope      OwnerScope       `json:"scope"`
	SessionID  string           `json:"session_id,omitempty"`
	Subject    string           `json:"subject"`
	Summary    string           `json:"summary"`
	Priority   string           `json:"priority,omitempty"`
	Status     EscalationStatus `json:"status"`
	Metadata   json.RawMessage  `json:"metadata,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
	CanceledAt *time.Time       `json:"canceled_at,omitempty"`
}

type WebhookSubscription struct {
	ID        string          `json:"id"`
	Scope     OwnerScope      `json:"scope"`
	Name      string          `json:"name"`
	URL       string          `json:"url"`
	Secret    string          `json:"secret,omitempty"`
	Events    []string        `json:"events"`
	Enabled   bool            `json:"enabled"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type OutboxMessage struct {
	ID             string          `json:"id"`
	Scope          OwnerScope      `json:"scope"`
	EscalationID   string          `json:"escalation_id"`
	SubscriptionID string          `json:"subscription_id"`
	EventType      string          `json:"event_type"`
	Payload        json.RawMessage `json:"payload"`
	Status         OutboxStatus    `json:"status"`
	Attempts       int             `json:"attempts"`
	MaxAttempts    int             `json:"max_attempts"`
	NextAttemptAt  time.Time       `json:"next_attempt_at"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type CreateEscalationInput struct {
	Scope     OwnerScope      `json:"scope"`
	SessionID string          `json:"session_id,omitempty"`
	Subject   string          `json:"subject"`
	Summary   string          `json:"summary"`
	Priority  string          `json:"priority,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type CreateSessionInput struct {
	Scope           OwnerScope      `json:"scope"`
	ExternalUserRef string          `json:"external_user_ref,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
}

type SessionStateSnapshot struct {
	Session    Session     `json:"session"`
	Escalation *Escalation `json:"escalation,omitempty"`
}

type CreateSubscriptionInput struct {
	Scope    OwnerScope      `json:"scope"`
	Name     string          `json:"name"`
	URL      string          `json:"url"`
	Secret   string          `json:"secret,omitempty"`
	Events   []string        `json:"events,omitempty"`
	Enabled  *bool           `json:"enabled,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}
