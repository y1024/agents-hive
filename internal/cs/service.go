package cs

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	EventEscalationOpened   = "escalation.opened"
	EventEscalationCanceled = "escalation.canceled"
	DefaultMaxAttempts      = 3
)

type Service struct {
	store      Store
	httpClient *http.Client
	now        func() time.Time
}

func NewService(store Store) *Service {
	return &Service{
		store:      store,
		httpClient: http.DefaultClient,
		now:        time.Now,
	}
}

func (s *Service) SetHTTPClient(client *http.Client) {
	if client != nil {
		s.httpClient = client
	}
}

func (s *Service) SetNow(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

func (s *Service) CreateSession(ctx context.Context, input CreateSessionInput) (Session, error) {
	if s == nil || s.store == nil {
		return Session{}, errors.New("customer service store not configured")
	}
	input.Scope = input.Scope.Normalized()
	now := s.now().UTC()
	rec := Session{
		ID:              "css_" + uuid.NewString(),
		Scope:           input.Scope,
		State:           SessionStateAIHandling,
		ExternalUserRef: strings.TrimSpace(input.ExternalUserRef),
		Metadata:        normalizeJSON(input.Metadata),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	return s.store.CreateSession(ctx, rec)
}

func (s *Service) GetSessionState(ctx context.Context, scope OwnerScope, id string) (SessionStateSnapshot, error) {
	if s == nil || s.store == nil {
		return SessionStateSnapshot{}, errors.New("customer service store not configured")
	}
	scope = scope.Normalized()
	session, err := s.store.GetSession(ctx, scope, strings.TrimSpace(id))
	if err != nil {
		return SessionStateSnapshot{}, err
	}
	snapshot := SessionStateSnapshot{Session: session}
	if escalation, err := s.store.LatestEscalationForSession(ctx, scope, session.ID); err == nil {
		snapshot.Escalation = &escalation
	} else if err != ErrNotFound {
		return SessionStateSnapshot{}, err
	}
	return snapshot, nil
}

func (s *Service) CancelSessionEscalation(ctx context.Context, scope OwnerScope, sessionID string) (SessionStateSnapshot, error) {
	if s == nil || s.store == nil {
		return SessionStateSnapshot{}, errors.New("customer service store not configured")
	}
	scope = scope.Normalized()
	session, err := s.store.GetSession(ctx, scope, strings.TrimSpace(sessionID))
	if err != nil {
		return SessionStateSnapshot{}, err
	}
	escalation, err := s.store.LatestEscalationForSession(ctx, scope, session.ID)
	if err != nil {
		return SessionStateSnapshot{}, err
	}
	canceled, err := s.CancelEscalation(ctx, scope, escalation.ID)
	if err != nil {
		return SessionStateSnapshot{}, err
	}
	now := s.now().UTC()
	if CanTransitionSession(session.State, SessionStateAIHandling) {
		session, err = s.store.UpdateSessionState(ctx, scope, session.ID, SessionStateAIHandling, now)
		if err != nil {
			return SessionStateSnapshot{}, err
		}
	}
	return SessionStateSnapshot{Session: session, Escalation: &canceled}, nil
}

func (s *Service) CreateEscalation(ctx context.Context, input CreateEscalationInput) (Escalation, error) {
	if s == nil || s.store == nil {
		return Escalation{}, errors.New("customer service store not configured")
	}
	input.Scope = input.Scope.Normalized()
	if strings.TrimSpace(input.Subject) == "" {
		return Escalation{}, errors.New("subject is required")
	}
	if strings.TrimSpace(input.Summary) == "" {
		return Escalation{}, errors.New("summary is required")
	}
	now := s.now().UTC()
	rec := Escalation{
		ID:        "cse_" + uuid.NewString(),
		Scope:     input.Scope,
		SessionID: strings.TrimSpace(input.SessionID),
		Subject:   strings.TrimSpace(input.Subject),
		Summary:   strings.TrimSpace(input.Summary),
		Priority:  strings.TrimSpace(input.Priority),
		Status:    EscalationOpen,
		Metadata:  normalizeJSON(input.Metadata),
		CreatedAt: now,
		UpdatedAt: now,
	}
	rec, err := s.store.CreateEscalation(ctx, rec)
	if err != nil {
		return Escalation{}, err
	}
	if rec.SessionID != "" {
		if _, err := s.store.GetSession(ctx, input.Scope, rec.SessionID); err == nil {
			if _, updateErr := s.store.UpdateSessionState(ctx, input.Scope, rec.SessionID, SessionStateEscalatePending, now); updateErr != nil {
				return Escalation{}, updateErr
			}
		} else if err != ErrNotFound {
			return Escalation{}, err
		}
	}
	subs, err := s.store.ListSubscriptions(ctx, input.Scope)
	if err != nil {
		return Escalation{}, err
	}
	queued := false
	for _, sub := range subs {
		if !sub.Enabled || !subWantsEvent(sub, EventEscalationOpened) {
			continue
		}
		payload, err := escalationPayload(EventEscalationOpened, rec, now)
		if err != nil {
			return Escalation{}, err
		}
		_, err = s.store.EnqueueOutbox(ctx, OutboxMessage{
			ID:             "csob_" + uuid.NewString(),
			Scope:          input.Scope,
			EscalationID:   rec.ID,
			SubscriptionID: sub.ID,
			EventType:      EventEscalationOpened,
			Payload:        payload,
			Status:         OutboxQueued,
			MaxAttempts:    DefaultMaxAttempts,
			NextAttemptAt:  now,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
		if err != nil {
			return Escalation{}, err
		}
		queued = true
	}
	if queued {
		return s.store.UpdateEscalationStatus(ctx, input.Scope, rec.ID, EscalationQueued, now)
	}
	return rec, nil
}

func (s *Service) CancelEscalation(ctx context.Context, scope OwnerScope, id string) (Escalation, error) {
	if s == nil || s.store == nil {
		return Escalation{}, errors.New("customer service store not configured")
	}
	scope = scope.Normalized()
	now := s.now().UTC()
	rec, err := s.store.UpdateEscalationStatus(ctx, scope, strings.TrimSpace(id), EscalationCanceled, now)
	if err != nil {
		return Escalation{}, err
	}
	if _, err := s.store.DeleteQueuedOutboxForEscalation(ctx, scope, rec.ID, []string{EventEscalationOpened}); err != nil {
		return Escalation{}, err
	}
	subs, err := s.store.ListSubscriptions(ctx, scope)
	if err != nil {
		return Escalation{}, err
	}
	for _, sub := range subs {
		if !sub.Enabled || !subWantsEvent(sub, EventEscalationCanceled) {
			continue
		}
		payload, err := escalationPayload(EventEscalationCanceled, rec, now)
		if err != nil {
			return Escalation{}, err
		}
		if _, err := s.store.EnqueueOutbox(ctx, OutboxMessage{
			ID:             "csob_" + uuid.NewString(),
			Scope:          scope,
			EscalationID:   rec.ID,
			SubscriptionID: sub.ID,
			EventType:      EventEscalationCanceled,
			Payload:        payload,
			Status:         OutboxQueued,
			MaxAttempts:    DefaultMaxAttempts,
			NextAttemptAt:  now,
			CreatedAt:      now,
			UpdatedAt:      now,
		}); err != nil {
			return Escalation{}, err
		}
	}
	return rec, nil
}

func (s *Service) CreateSubscription(ctx context.Context, input CreateSubscriptionInput) (WebhookSubscription, error) {
	if s == nil || s.store == nil {
		return WebhookSubscription{}, errors.New("customer service store not configured")
	}
	input.Scope = input.Scope.Normalized()
	input.URL = strings.TrimSpace(input.URL)
	u, err := url.ParseRequestURI(input.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return WebhookSubscription{}, errors.New("valid http(s) webhook url is required")
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	events := normalizeEvents(input.Events)
	now := s.now().UTC()
	rec := WebhookSubscription{
		ID:        "cswh_" + uuid.NewString(),
		Scope:     input.Scope,
		Name:      strings.TrimSpace(input.Name),
		URL:       input.URL,
		Secret:    strings.TrimSpace(input.Secret),
		Events:    events,
		Enabled:   enabled,
		Metadata:  normalizeJSON(input.Metadata),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if rec.Name == "" {
		rec.Name = "webhook"
	}
	return s.store.CreateSubscription(ctx, rec)
}

func (s *Service) ListSubscriptions(ctx context.Context, scope OwnerScope) ([]WebhookSubscription, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("customer service store not configured")
	}
	return s.store.ListSubscriptions(ctx, scope.Normalized())
}

func (s *Service) ListDLQ(ctx context.Context, scope OwnerScope, limit int) ([]OutboxMessage, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("customer service store not configured")
	}
	return s.store.ListDLQ(ctx, scope.Normalized(), limit)
}

func (s *Service) RetryDLQ(ctx context.Context, scope OwnerScope, id string) (OutboxMessage, error) {
	if s == nil || s.store == nil {
		return OutboxMessage{}, errors.New("customer service store not configured")
	}
	return s.store.RetryOutbox(ctx, scope.Normalized(), strings.TrimSpace(id), s.now().UTC())
}

func (s *Service) DispatchDue(ctx context.Context, limit int) ([]OutboxMessage, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("customer service store not configured")
	}
	if limit <= 0 {
		limit = 20
	}
	now := s.now().UTC()
	msgs, err := s.store.ClaimDueOutbox(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	out := make([]OutboxMessage, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, s.dispatchOne(ctx, msg))
	}
	return out, nil
}

func (s *Service) dispatchOne(ctx context.Context, msg OutboxMessage) OutboxMessage {
	now := s.now().UTC()
	sub, err := s.store.GetSubscription(ctx, msg.Scope, msg.SubscriptionID)
	if err != nil {
		updated, _ := s.store.MarkOutboxFailed(ctx, msg.Scope, msg.ID, err.Error(), msg.MaxAttempts, now, now)
		return updated
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(msg.Payload))
	if err != nil {
		updated, _ := s.store.MarkOutboxFailed(ctx, msg.Scope, msg.ID, err.Error(), msg.MaxAttempts, now, now)
		return updated
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agents-Hive-Event", msg.EventType)
	req.Header.Set("X-Agents-Hive-Delivery", msg.ID)
	if sub.Secret != "" {
		req.Header.Set("X-Agents-Hive-Signature", signPayload(sub.Secret, msg.Payload))
	}
	resp, err := s.httpClient.Do(req)
	if err == nil && resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		updated, _ := s.store.MarkOutboxSent(ctx, msg.Scope, msg.ID, now)
		_ = s.markEscalationAfterDelivery(ctx, updated, EscalationSent)
		return updated
	}
	errText := ""
	attempts := msg.Attempts + 1
	if err != nil {
		errText = err.Error()
	} else {
		errText = fmt.Sprintf("webhook returned status %d", resp.StatusCode)
	}
	if resp != nil && resp.StatusCode >= 400 && resp.StatusCode < 500 {
		attempts = msg.MaxAttempts
	}
	next := now.Add(time.Duration(attempts) * time.Minute)
	updated, _ := s.store.MarkOutboxFailed(ctx, msg.Scope, msg.ID, errText, attempts, next, now)
	if updated.Status == OutboxFailed {
		_ = s.markEscalationAfterDelivery(ctx, updated, EscalationFailed)
	}
	return updated
}

func (s *Service) markEscalationAfterDelivery(ctx context.Context, msg OutboxMessage, status EscalationStatus) error {
	rec, err := s.store.GetEscalation(ctx, msg.Scope, msg.EscalationID)
	if err != nil || !CanTransitionEscalation(rec.Status, status) {
		return err
	}
	now := s.now().UTC()
	rec, err = s.store.UpdateEscalationStatus(ctx, msg.Scope, msg.EscalationID, status, now)
	if err != nil {
		return err
	}
	if rec.SessionID != "" {
		targetState := SessionStateHumanHandling
		if status == EscalationFailed {
			targetState = SessionStateAIHandling
		}
		if _, sessionErr := s.store.GetSession(ctx, msg.Scope, rec.SessionID); sessionErr == nil {
			_, _ = s.store.UpdateSessionState(ctx, msg.Scope, rec.SessionID, targetState, now)
		}
	}
	return err
}

func subWantsEvent(sub WebhookSubscription, event string) bool {
	if len(sub.Events) == 0 {
		return true
	}
	for _, item := range sub.Events {
		if item == "*" || item == event {
			return true
		}
	}
	return false
}

func normalizeEvents(events []string) []string {
	if len(events) == 0 {
		return []string{EventEscalationOpened, EventEscalationCanceled}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(events))
	for _, event := range events {
		event = strings.TrimSpace(event)
		if event == "" || seen[event] {
			continue
		}
		seen[event] = true
		out = append(out, event)
	}
	if len(out) == 0 {
		return []string{EventEscalationOpened, EventEscalationCanceled}
	}
	return out
}

func escalationPayload(event string, rec Escalation, now time.Time) (json.RawMessage, error) {
	data, err := json.Marshal(map[string]any{
		"event":      event,
		"created_at": now,
		"escalation": rec,
	})
	return data, err
}

func signPayload(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
