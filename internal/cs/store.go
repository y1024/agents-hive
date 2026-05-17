package cs

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("customer service record not found")

type Store interface {
	CreateSession(ctx context.Context, rec Session) (Session, error)
	GetSession(ctx context.Context, scope OwnerScope, id string) (Session, error)
	UpdateSessionState(ctx context.Context, scope OwnerScope, id string, state SessionState, now time.Time) (Session, error)
	CreateEscalation(ctx context.Context, rec Escalation) (Escalation, error)
	LatestEscalationForSession(ctx context.Context, scope OwnerScope, sessionID string) (Escalation, error)
	GetEscalation(ctx context.Context, scope OwnerScope, id string) (Escalation, error)
	UpdateEscalationStatus(ctx context.Context, scope OwnerScope, id string, status EscalationStatus, now time.Time) (Escalation, error)
	CreateSubscription(ctx context.Context, rec WebhookSubscription) (WebhookSubscription, error)
	ListSubscriptions(ctx context.Context, scope OwnerScope) ([]WebhookSubscription, error)
	GetSubscription(ctx context.Context, scope OwnerScope, id string) (WebhookSubscription, error)
	EnqueueOutbox(ctx context.Context, msg OutboxMessage) (OutboxMessage, error)
	DeleteQueuedOutboxForEscalation(ctx context.Context, scope OwnerScope, escalationID string, eventTypes []string) (int, error)
	ListDLQ(ctx context.Context, scope OwnerScope, limit int) ([]OutboxMessage, error)
	ClaimDueOutbox(ctx context.Context, now time.Time, limit int) ([]OutboxMessage, error)
	MarkOutboxSent(ctx context.Context, scope OwnerScope, id string, now time.Time) (OutboxMessage, error)
	MarkOutboxFailed(ctx context.Context, scope OwnerScope, id, errText string, attempts int, nextAttemptAt time.Time, now time.Time) (OutboxMessage, error)
	RetryOutbox(ctx context.Context, scope OwnerScope, id string, now time.Time) (OutboxMessage, error)
}

type MemoryStore struct {
	mu            sync.Mutex
	sessions      map[string]Session
	escalations   map[string]Escalation
	subscriptions map[string]WebhookSubscription
	outbox        map[string]OutboxMessage
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions:      map[string]Session{},
		escalations:   map[string]Escalation{},
		subscriptions: map[string]WebhookSubscription{},
		outbox:        map[string]OutboxMessage{},
	}
}

func (s *MemoryStore) CreateSession(_ context.Context, rec Session) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.Scope = rec.Scope.Normalized()
	if rec.State == "" {
		rec.State = SessionStateAIHandling
	}
	s.sessions[rec.ID] = rec
	return rec, nil
}

func (s *MemoryStore) GetSession(_ context.Context, scope OwnerScope, id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	if !ok || !sameScope(rec.Scope, scope.Normalized()) {
		return Session{}, ErrNotFound
	}
	return rec, nil
}

func (s *MemoryStore) UpdateSessionState(_ context.Context, scope OwnerScope, id string, state SessionState, now time.Time) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	if !ok || !sameScope(rec.Scope, scope.Normalized()) {
		return Session{}, ErrNotFound
	}
	if err := ValidateSessionTransition(rec.State, state); err != nil {
		return Session{}, err
	}
	rec.State = state
	rec.UpdatedAt = now
	if state == SessionStateResolved {
		rec.ResolvedAt = &now
	}
	s.sessions[id] = rec
	return rec, nil
}

func (s *MemoryStore) CreateEscalation(_ context.Context, rec Escalation) (Escalation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.Scope = rec.Scope.Normalized()
	s.escalations[rec.ID] = rec
	return rec, nil
}

func (s *MemoryStore) LatestEscalationForSession(_ context.Context, scope OwnerScope, sessionID string) (Escalation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope = scope.Normalized()
	var latest *Escalation
	for _, rec := range s.escalations {
		if rec.SessionID != sessionID || !sameScope(rec.Scope, scope) {
			continue
		}
		copied := rec
		if latest == nil || copied.CreatedAt.After(latest.CreatedAt) {
			latest = &copied
		}
	}
	if latest == nil {
		return Escalation{}, ErrNotFound
	}
	return *latest, nil
}

func (s *MemoryStore) GetEscalation(_ context.Context, scope OwnerScope, id string) (Escalation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.escalations[id]
	if !ok || !sameScope(rec.Scope, scope.Normalized()) {
		return Escalation{}, ErrNotFound
	}
	return rec, nil
}

func (s *MemoryStore) UpdateEscalationStatus(_ context.Context, scope OwnerScope, id string, status EscalationStatus, now time.Time) (Escalation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.escalations[id]
	if !ok || !sameScope(rec.Scope, scope.Normalized()) {
		return Escalation{}, ErrNotFound
	}
	if err := ValidateEscalationTransition(rec.Status, status); err != nil {
		return Escalation{}, err
	}
	rec.Status = status
	rec.UpdatedAt = now
	if status == EscalationCanceled {
		rec.CanceledAt = &now
	}
	s.escalations[id] = rec
	return rec, nil
}

func (s *MemoryStore) CreateSubscription(_ context.Context, rec WebhookSubscription) (WebhookSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.Scope = rec.Scope.Normalized()
	s.subscriptions[rec.ID] = rec
	return rec, nil
}

func (s *MemoryStore) ListSubscriptions(_ context.Context, scope OwnerScope) ([]WebhookSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope = scope.Normalized()
	out := make([]WebhookSubscription, 0)
	for _, rec := range s.subscriptions {
		if sameScope(rec.Scope, scope) {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) GetSubscription(_ context.Context, scope OwnerScope, id string) (WebhookSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.subscriptions[id]
	if !ok || !sameScope(rec.Scope, scope.Normalized()) {
		return WebhookSubscription{}, ErrNotFound
	}
	return rec, nil
}

func (s *MemoryStore) EnqueueOutbox(_ context.Context, msg OutboxMessage) (OutboxMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg.Scope = msg.Scope.Normalized()
	s.outbox[msg.ID] = msg
	return msg, nil
}

func (s *MemoryStore) DeleteQueuedOutboxForEscalation(_ context.Context, scope OwnerScope, escalationID string, eventTypes []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope = scope.Normalized()
	allowedEvents := map[string]bool{}
	for _, eventType := range eventTypes {
		eventType = strings.TrimSpace(eventType)
		if eventType != "" {
			allowedEvents[eventType] = true
		}
	}
	deleted := 0
	for id, rec := range s.outbox {
		if rec.Status != OutboxQueued || rec.EscalationID != escalationID || !sameScope(rec.Scope, scope) {
			continue
		}
		if len(allowedEvents) > 0 && !allowedEvents[rec.EventType] {
			continue
		}
		delete(s.outbox, id)
		deleted++
	}
	return deleted, nil
}

func (s *MemoryStore) ListDLQ(_ context.Context, scope OwnerScope, limit int) ([]OutboxMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope = scope.Normalized()
	out := make([]OutboxMessage, 0)
	for _, rec := range s.outbox {
		if rec.Status == OutboxFailed && sameScope(rec.Scope, scope) {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return capLimit(out, limit), nil
}

func (s *MemoryStore) ClaimDueOutbox(_ context.Context, now time.Time, limit int) ([]OutboxMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OutboxMessage, 0)
	for _, rec := range s.outbox {
		if rec.Status == OutboxQueued && !rec.NextAttemptAt.After(now) {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return capLimit(out, limit), nil
}

func (s *MemoryStore) MarkOutboxSent(_ context.Context, scope OwnerScope, id string, now time.Time) (OutboxMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.outbox[id]
	if !ok || !sameScope(rec.Scope, scope.Normalized()) {
		return OutboxMessage{}, ErrNotFound
	}
	rec.Status = OutboxSent
	rec.UpdatedAt = now
	rec.LastError = ""
	s.outbox[id] = rec
	return rec, nil
}

func (s *MemoryStore) MarkOutboxFailed(_ context.Context, scope OwnerScope, id, errText string, attempts int, nextAttemptAt time.Time, now time.Time) (OutboxMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.outbox[id]
	if !ok || !sameScope(rec.Scope, scope.Normalized()) {
		return OutboxMessage{}, ErrNotFound
	}
	rec.Attempts = attempts
	rec.NextAttemptAt = nextAttemptAt
	rec.LastError = errText
	rec.UpdatedAt = now
	if attempts >= rec.MaxAttempts {
		rec.Status = OutboxFailed
	} else {
		rec.Status = OutboxQueued
	}
	s.outbox[id] = rec
	return rec, nil
}

func (s *MemoryStore) RetryOutbox(_ context.Context, scope OwnerScope, id string, now time.Time) (OutboxMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.outbox[id]
	if !ok || !sameScope(rec.Scope, scope.Normalized()) {
		return OutboxMessage{}, ErrNotFound
	}
	rec.Status = OutboxQueued
	rec.NextAttemptAt = now
	rec.LastError = ""
	rec.UpdatedAt = now
	s.outbox[id] = rec
	return rec, nil
}

func sameScope(a, b OwnerScope) bool {
	a = a.Normalized()
	b = b.Normalized()
	return a.DomainID == b.DomainID && a.OwnerScope == b.OwnerScope && a.OwnerID == b.OwnerID
}

func normalizeJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return json.RawMessage(`{}`)
	}
	return raw
}

func capLimit[T any](items []T, limit int) []T {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}
