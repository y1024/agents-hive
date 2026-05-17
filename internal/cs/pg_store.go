package cs

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

func (s *PGStore) CreateSession(ctx context.Context, rec Session) (Session, error) {
	rec.Scope = rec.Scope.Normalized()
	if rec.State == "" {
		rec.State = SessionStateAIHandling
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO cs_sessions
		(id, domain_id, owner_scope, owner_id, state, external_user_ref, metadata, created_at, updated_at, resolved_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		rec.ID, rec.Scope.DomainID, rec.Scope.OwnerScope, rec.Scope.OwnerID, rec.State, rec.ExternalUserRef, normalizeJSON(rec.Metadata), rec.CreatedAt, rec.UpdatedAt, rec.ResolvedAt)
	return rec, err
}

func (s *PGStore) GetSession(ctx context.Context, scope OwnerScope, id string) (Session, error) {
	scope = scope.Normalized()
	rows, err := s.pool.Query(ctx, `SELECT id, domain_id, owner_scope, owner_id, state, external_user_ref, metadata, created_at, updated_at, resolved_at
		FROM cs_sessions WHERE id=$1 AND domain_id=$2 AND owner_scope=$3 AND owner_id=$4`,
		id, scope.DomainID, scope.OwnerScope, scope.OwnerID)
	if err != nil {
		return Session{}, err
	}
	defer rows.Close()
	rec, err := pgx.CollectOneRow(rows, scanSession)
	if err == pgx.ErrNoRows {
		return Session{}, ErrNotFound
	}
	return rec, err
}

func (s *PGStore) UpdateSessionState(ctx context.Context, scope OwnerScope, id string, state SessionState, now time.Time) (Session, error) {
	current, err := s.GetSession(ctx, scope, id)
	if err != nil {
		return Session{}, err
	}
	if err := ValidateSessionTransition(current.State, state); err != nil {
		return Session{}, err
	}
	scope = scope.Normalized()
	rows, err := s.pool.Query(ctx, `UPDATE cs_sessions SET state=$5, updated_at=$6, resolved_at=CASE WHEN $5='resolved' THEN $6 ELSE resolved_at END
		WHERE id=$1 AND domain_id=$2 AND owner_scope=$3 AND owner_id=$4
		RETURNING id, domain_id, owner_scope, owner_id, state, external_user_ref, metadata, created_at, updated_at, resolved_at`,
		id, scope.DomainID, scope.OwnerScope, scope.OwnerID, state, now)
	if err != nil {
		return Session{}, err
	}
	defer rows.Close()
	rec, err := pgx.CollectOneRow(rows, scanSession)
	if err == pgx.ErrNoRows {
		return Session{}, ErrNotFound
	}
	return rec, err
}

func (s *PGStore) CreateEscalation(ctx context.Context, rec Escalation) (Escalation, error) {
	rec.Scope = rec.Scope.Normalized()
	_, err := s.pool.Exec(ctx, `INSERT INTO cs_escalations
		(id, domain_id, owner_scope, owner_id, session_id, subject, summary, priority, status, metadata, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		rec.ID, rec.Scope.DomainID, rec.Scope.OwnerScope, rec.Scope.OwnerID, rec.SessionID, rec.Subject, rec.Summary, rec.Priority, rec.Status, normalizeJSON(rec.Metadata), rec.CreatedAt, rec.UpdatedAt)
	return rec, err
}

func (s *PGStore) LatestEscalationForSession(ctx context.Context, scope OwnerScope, sessionID string) (Escalation, error) {
	scope = scope.Normalized()
	rows, err := s.pool.Query(ctx, `SELECT id, domain_id, owner_scope, owner_id, session_id, subject, summary, priority, status, metadata, created_at, updated_at, canceled_at
		FROM cs_escalations WHERE domain_id=$1 AND owner_scope=$2 AND owner_id=$3 AND session_id=$4
		ORDER BY created_at DESC LIMIT 1`,
		scope.DomainID, scope.OwnerScope, scope.OwnerID, sessionID)
	if err != nil {
		return Escalation{}, err
	}
	defer rows.Close()
	rec, err := pgx.CollectOneRow(rows, scanEscalation)
	if err == pgx.ErrNoRows {
		return Escalation{}, ErrNotFound
	}
	return rec, err
}

func (s *PGStore) GetEscalation(ctx context.Context, scope OwnerScope, id string) (Escalation, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, domain_id, owner_scope, owner_id, session_id, subject, summary, priority, status, metadata, created_at, updated_at, canceled_at
		FROM cs_escalations WHERE id=$1 AND domain_id=$2 AND owner_scope=$3 AND owner_id=$4`,
		id, scope.Normalized().DomainID, scope.Normalized().OwnerScope, scope.Normalized().OwnerID)
	if err != nil {
		return Escalation{}, err
	}
	defer rows.Close()
	rec, err := pgx.CollectOneRow(rows, scanEscalation)
	if err == pgx.ErrNoRows {
		return Escalation{}, ErrNotFound
	}
	return rec, err
}

func (s *PGStore) UpdateEscalationStatus(ctx context.Context, scope OwnerScope, id string, status EscalationStatus, now time.Time) (Escalation, error) {
	current, err := s.GetEscalation(ctx, scope, id)
	if err != nil {
		return Escalation{}, err
	}
	if err := ValidateEscalationTransition(current.Status, status); err != nil {
		return Escalation{}, err
	}
	rows, err := s.pool.Query(ctx, `UPDATE cs_escalations SET status=$5, updated_at=$6, canceled_at=CASE WHEN $5='canceled' THEN $6 ELSE canceled_at END
		WHERE id=$1 AND domain_id=$2 AND owner_scope=$3 AND owner_id=$4
		RETURNING id, domain_id, owner_scope, owner_id, session_id, subject, summary, priority, status, metadata, created_at, updated_at, canceled_at`,
		id, scope.Normalized().DomainID, scope.Normalized().OwnerScope, scope.Normalized().OwnerID, status, now)
	if err != nil {
		return Escalation{}, err
	}
	defer rows.Close()
	rec, err := pgx.CollectOneRow(rows, scanEscalation)
	if err == pgx.ErrNoRows {
		return Escalation{}, ErrNotFound
	}
	return rec, err
}

func (s *PGStore) CreateSubscription(ctx context.Context, rec WebhookSubscription) (WebhookSubscription, error) {
	rec.Scope = rec.Scope.Normalized()
	events, _ := json.Marshal(rec.Events)
	_, err := s.pool.Exec(ctx, `INSERT INTO cs_webhook_subscriptions
		(id, domain_id, owner_scope, owner_id, name, url, secret, events, enabled, metadata, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		rec.ID, rec.Scope.DomainID, rec.Scope.OwnerScope, rec.Scope.OwnerID, rec.Name, rec.URL, rec.Secret, events, rec.Enabled, normalizeJSON(rec.Metadata), rec.CreatedAt, rec.UpdatedAt)
	return rec, err
}

func (s *PGStore) ListSubscriptions(ctx context.Context, scope OwnerScope) ([]WebhookSubscription, error) {
	scope = scope.Normalized()
	rows, err := s.pool.Query(ctx, `SELECT id, domain_id, owner_scope, owner_id, name, url, secret, events, enabled, metadata, created_at, updated_at
		FROM cs_webhook_subscriptions WHERE domain_id=$1 AND owner_scope=$2 AND owner_id=$3 ORDER BY created_at DESC`,
		scope.DomainID, scope.OwnerScope, scope.OwnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, scanSubscription)
}

func (s *PGStore) GetSubscription(ctx context.Context, scope OwnerScope, id string) (WebhookSubscription, error) {
	scope = scope.Normalized()
	rows, err := s.pool.Query(ctx, `SELECT id, domain_id, owner_scope, owner_id, name, url, secret, events, enabled, metadata, created_at, updated_at
		FROM cs_webhook_subscriptions WHERE id=$1 AND domain_id=$2 AND owner_scope=$3 AND owner_id=$4`,
		id, scope.DomainID, scope.OwnerScope, scope.OwnerID)
	if err != nil {
		return WebhookSubscription{}, err
	}
	defer rows.Close()
	rec, err := pgx.CollectOneRow(rows, scanSubscription)
	if err == pgx.ErrNoRows {
		return WebhookSubscription{}, ErrNotFound
	}
	return rec, err
}

func (s *PGStore) EnqueueOutbox(ctx context.Context, msg OutboxMessage) (OutboxMessage, error) {
	msg.Scope = msg.Scope.Normalized()
	_, err := s.pool.Exec(ctx, `INSERT INTO cs_webhook_outbox
		(id, domain_id, owner_scope, owner_id, escalation_id, subscription_id, event_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		msg.ID, msg.Scope.DomainID, msg.Scope.OwnerScope, msg.Scope.OwnerID, msg.EscalationID, msg.SubscriptionID, msg.EventType, msg.Payload, msg.Status, msg.Attempts, msg.MaxAttempts, msg.NextAttemptAt, msg.LastError, msg.CreatedAt, msg.UpdatedAt)
	return msg, err
}

func (s *PGStore) DeleteQueuedOutboxForEscalation(ctx context.Context, scope OwnerScope, escalationID string, eventTypes []string) (int, error) {
	scope = scope.Normalized()
	args := []any{scope.DomainID, scope.OwnerScope, scope.OwnerID, escalationID}
	eventFilter := ""
	events := make([]string, 0, len(eventTypes))
	for _, eventType := range eventTypes {
		eventType = strings.TrimSpace(eventType)
		if eventType != "" {
			events = append(events, eventType)
		}
	}
	if len(events) > 0 {
		args = append(args, events)
		eventFilter = " AND event_type = ANY($5)"
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM cs_webhook_outbox
		WHERE domain_id=$1 AND owner_scope=$2 AND owner_id=$3 AND escalation_id=$4 AND status='queued'`+eventFilter,
		args...)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *PGStore) ListDLQ(ctx context.Context, scope OwnerScope, limit int) ([]OutboxMessage, error) {
	scope = scope.Normalized()
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id, domain_id, owner_scope, owner_id, escalation_id, subscription_id, event_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at
		FROM cs_webhook_outbox WHERE domain_id=$1 AND owner_scope=$2 AND owner_id=$3 AND status='failed' ORDER BY updated_at DESC LIMIT $4`,
		scope.DomainID, scope.OwnerScope, scope.OwnerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, scanOutbox)
}

func (s *PGStore) ClaimDueOutbox(ctx context.Context, now time.Time, limit int) ([]OutboxMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id, domain_id, owner_scope, owner_id, escalation_id, subscription_id, event_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at
		FROM cs_webhook_outbox WHERE status='queued' AND next_attempt_at <= $1 ORDER BY created_at ASC LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, scanOutbox)
}

func (s *PGStore) MarkOutboxSent(ctx context.Context, scope OwnerScope, id string, now time.Time) (OutboxMessage, error) {
	return s.updateOutbox(ctx, scope, id, OutboxSent, "", -1, now, now)
}

func (s *PGStore) MarkOutboxFailed(ctx context.Context, scope OwnerScope, id, errText string, attempts int, nextAttemptAt time.Time, now time.Time) (OutboxMessage, error) {
	status := OutboxQueued
	var maxAttempts int
	_ = s.pool.QueryRow(ctx, `SELECT max_attempts FROM cs_webhook_outbox WHERE id=$1`, id).Scan(&maxAttempts)
	if attempts >= maxAttempts {
		status = OutboxFailed
	}
	return s.updateOutbox(ctx, scope, id, status, errText, attempts, nextAttemptAt, now)
}

func (s *PGStore) RetryOutbox(ctx context.Context, scope OwnerScope, id string, now time.Time) (OutboxMessage, error) {
	return s.updateOutbox(ctx, scope, id, OutboxQueued, "", -1, now, now)
}

func (s *PGStore) updateOutbox(ctx context.Context, scope OwnerScope, id string, status OutboxStatus, lastError string, attempts int, nextAttemptAt time.Time, now time.Time) (OutboxMessage, error) {
	scope = scope.Normalized()
	rows, err := s.pool.Query(ctx, `UPDATE cs_webhook_outbox SET status=$5, last_error=$6, attempts=CASE WHEN $7 >= 0 THEN $7 ELSE attempts END, next_attempt_at=$8, updated_at=$9
		WHERE id=$1 AND domain_id=$2 AND owner_scope=$3 AND owner_id=$4
		RETURNING id, domain_id, owner_scope, owner_id, escalation_id, subscription_id, event_type, payload, status, attempts, max_attempts, next_attempt_at, last_error, created_at, updated_at`,
		id, scope.DomainID, scope.OwnerScope, scope.OwnerID, status, lastError, attempts, nextAttemptAt, now)
	if err != nil {
		return OutboxMessage{}, err
	}
	defer rows.Close()
	rec, err := pgx.CollectOneRow(rows, scanOutbox)
	if err == pgx.ErrNoRows {
		return OutboxMessage{}, ErrNotFound
	}
	return rec, err
}

func scanSession(row pgx.CollectableRow) (Session, error) {
	var rec Session
	err := row.Scan(&rec.ID, &rec.Scope.DomainID, &rec.Scope.OwnerScope, &rec.Scope.OwnerID, &rec.State, &rec.ExternalUserRef, &rec.Metadata, &rec.CreatedAt, &rec.UpdatedAt, &rec.ResolvedAt)
	return rec, err
}

func scanEscalation(row pgx.CollectableRow) (Escalation, error) {
	var rec Escalation
	err := row.Scan(&rec.ID, &rec.Scope.DomainID, &rec.Scope.OwnerScope, &rec.Scope.OwnerID, &rec.SessionID, &rec.Subject, &rec.Summary, &rec.Priority, &rec.Status, &rec.Metadata, &rec.CreatedAt, &rec.UpdatedAt, &rec.CanceledAt)
	return rec, err
}

func scanSubscription(row pgx.CollectableRow) (WebhookSubscription, error) {
	var rec WebhookSubscription
	var events []byte
	if err := row.Scan(&rec.ID, &rec.Scope.DomainID, &rec.Scope.OwnerScope, &rec.Scope.OwnerID, &rec.Name, &rec.URL, &rec.Secret, &events, &rec.Enabled, &rec.Metadata, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
		return rec, err
	}
	_ = json.Unmarshal(events, &rec.Events)
	return rec, nil
}

func scanOutbox(row pgx.CollectableRow) (OutboxMessage, error) {
	var rec OutboxMessage
	err := row.Scan(&rec.ID, &rec.Scope.DomainID, &rec.Scope.OwnerScope, &rec.Scope.OwnerID, &rec.EscalationID, &rec.SubscriptionID, &rec.EventType, &rec.Payload, &rec.Status, &rec.Attempts, &rec.MaxAttempts, &rec.NextAttemptAt, &rec.LastError, &rec.CreatedAt, &rec.UpdatedAt)
	if strings.TrimSpace(string(rec.Payload)) == "" {
		rec.Payload = json.RawMessage(`{}`)
	}
	return rec, err
}
