package cs

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEscalationLifecycleAndDLQOwnerScope(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	now := time.Date(2026, 5, 16, 1, 2, 3, 0, time.UTC)
	service.SetNow(func() time.Time { return now })

	scopeA := OwnerScope{DomainID: DefaultDomainID, OwnerScope: "user", OwnerID: "u1"}
	scopeB := OwnerScope{DomainID: DefaultDomainID, OwnerScope: "user", OwnerID: "u2"}
	_, err := service.CreateSubscription(context.Background(), CreateSubscriptionInput{
		Scope: scopeA,
		Name:  "bad",
		URL:   "https://example.invalid/webhook",
	})
	if err != nil {
		t.Fatalf("CreateSubscription = %v", err)
	}
	rec, err := service.CreateEscalation(context.Background(), CreateEscalationInput{
		Scope:   scopeA,
		Subject: "need human",
		Summary: "customer asks for a human",
	})
	if err != nil {
		t.Fatalf("CreateEscalation = %v", err)
	}
	if rec.Status != EscalationQueued {
		t.Fatalf("status = %s, want queued", rec.Status)
	}

	msgs, err := store.ClaimDueOutbox(context.Background(), now, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ClaimDueOutbox = %d, %v", len(msgs), err)
	}
	_, err = store.MarkOutboxFailed(context.Background(), scopeA, msgs[0].ID, "bad request", msgs[0].MaxAttempts, now, now)
	if err != nil {
		t.Fatalf("MarkOutboxFailed = %v", err)
	}
	dlqA, err := service.ListDLQ(context.Background(), scopeA, 10)
	if err != nil || len(dlqA) != 1 {
		t.Fatalf("ListDLQ(scopeA) = %d, %v", len(dlqA), err)
	}
	dlqB, err := service.ListDLQ(context.Background(), scopeB, 10)
	if err != nil || len(dlqB) != 0 {
		t.Fatalf("ListDLQ(scopeB) = %d, %v", len(dlqB), err)
	}
}

func TestDispatch4xxMovesToDLQAndMarksEscalationFailed(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	now := time.Date(2026, 5, 16, 1, 2, 3, 0, time.UTC)
	service.SetNow(func() time.Time { return now })
	service.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})})

	scope := OwnerScope{OwnerID: "u1"}
	_, err := service.CreateSubscription(context.Background(), CreateSubscriptionInput{Scope: scope, URL: "https://example.com/webhook"})
	if err != nil {
		t.Fatalf("CreateSubscription = %v", err)
	}
	escalation, err := service.CreateEscalation(context.Background(), CreateEscalationInput{Scope: scope, Subject: "s", Summary: "summary"})
	if err != nil {
		t.Fatalf("CreateEscalation = %v", err)
	}
	deliveries, err := service.DispatchDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DispatchDue = %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != OutboxFailed {
		t.Fatalf("deliveries = %+v, want failed DLQ", deliveries)
	}
	got, err := store.GetEscalation(context.Background(), scope, escalation.ID)
	if err != nil {
		t.Fatalf("GetEscalation = %v", err)
	}
	if got.Status != EscalationFailed {
		t.Fatalf("escalation status = %s, want failed", got.Status)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCancelEscalationLifecycle(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	scope := OwnerScope{OwnerID: "u1"}
	_, err := service.CreateSubscription(context.Background(), CreateSubscriptionInput{Scope: scope, URL: "https://example.com/webhook"})
	if err != nil {
		t.Fatalf("CreateSubscription = %v", err)
	}
	rec, err := service.CreateEscalation(context.Background(), CreateEscalationInput{Scope: scope, Subject: "s", Summary: "summary"})
	if err != nil {
		t.Fatalf("CreateEscalation = %v", err)
	}
	canceled, err := service.CancelEscalation(context.Background(), scope, rec.ID)
	if err != nil {
		t.Fatalf("CancelEscalation = %v", err)
	}
	if canceled.Status != EscalationCanceled {
		t.Fatalf("status = %s, want canceled", canceled.Status)
	}
	msgs, err := store.ClaimDueOutbox(context.Background(), time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("ClaimDueOutbox = %v", err)
	}
	for _, msg := range msgs {
		if msg.EscalationID == rec.ID && msg.EventType == EventEscalationOpened {
			t.Fatalf("queued opened webhook was not canceled: %+v", msg)
		}
	}
	if _, err := service.CancelEscalation(context.Background(), scope, rec.ID); err == nil {
		t.Fatal("second cancel succeeded, want invalid transition")
	}
}

func TestSessionStateFollowsEscalationLifecycle(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	scope := OwnerScope{OwnerID: "u1"}
	session, err := service.CreateSession(context.Background(), CreateSessionInput{Scope: scope, ExternalUserRef: "buyer-1"})
	if err != nil {
		t.Fatalf("CreateSession = %v", err)
	}
	if session.State != SessionStateAIHandling {
		t.Fatalf("session state = %s, want ai_handling", session.State)
	}
	escalation, err := service.CreateEscalation(context.Background(), CreateEscalationInput{
		Scope:     scope,
		SessionID: session.ID,
		Subject:   "need human",
		Summary:   "refund exception",
	})
	if err != nil {
		t.Fatalf("CreateEscalation = %v", err)
	}
	snapshot, err := service.GetSessionState(context.Background(), scope, session.ID)
	if err != nil {
		t.Fatalf("GetSessionState = %v", err)
	}
	if snapshot.Session.State != SessionStateEscalatePending || snapshot.Escalation == nil || snapshot.Escalation.ID != escalation.ID {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	canceled, err := service.CancelSessionEscalation(context.Background(), scope, session.ID)
	if err != nil {
		t.Fatalf("CancelSessionEscalation = %v", err)
	}
	if canceled.Session.State != SessionStateAIHandling || canceled.Escalation == nil || canceled.Escalation.Status != EscalationCanceled {
		t.Fatalf("canceled snapshot = %+v", canceled)
	}
}
