package kb

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
)

func ValidateEvidenceScope(scope EvidenceScope) error {
	if scope.SessionID == "" || scope.TurnID == "" || scope.TraceID == "" ||
		scope.DomainID == "" || scope.OwnerScope == "" || scope.OwnerID == "" {
		return ErrInvalidScope
	}
	return nil
}

func (s *Service) RecordEvidence(ctx context.Context, scope EvidenceScope, doc Document, node TreeNode, citationText string) (EvidenceRef, error) {
	if err := ValidateEvidenceScope(scope); err != nil {
		return EvidenceRef{}, err
	}
	token := buildEvidenceToken(scope, doc, node)
	now := scope.Now
	if now.IsZero() {
		now = time.Now()
	}
	event := EvidenceEvent{
		ID:              hashText(token + now.Format(time.RFC3339Nano)),
		SessionID:       scope.SessionID,
		TurnID:          scope.TurnID,
		TraceID:         scope.TraceID,
		DomainID:        scope.DomainID,
		NamespaceID:     doc.NamespaceID,
		DocumentID:      doc.ID,
		DocumentVersion: doc.Version,
		NodeID:          node.ID,
		NodePath:        node.NodePath,
		StartPage:       node.StartPage,
		EndPage:         node.EndPage,
		OwnerScope:      scope.OwnerScope,
		OwnerID:         scope.OwnerID,
		EvidenceToken:   token,
		CitationText:    citationText,
		Verified:        false,
		CreatedAt:       now,
	}
	if err := s.store.SaveEvidenceEvent(ctx, event); err != nil {
		return EvidenceRef{}, err
	}
	return EvidenceRef{
		Token:           token,
		NamespaceID:     doc.NamespaceID,
		DocumentID:      doc.ID,
		DocumentVersion: doc.Version,
		NodeID:          node.ID,
		NodePath:        node.NodePath,
		StartPage:       node.StartPage,
		EndPage:         node.EndPage,
		CitationText:    citationText,
	}, nil
}

func (s *Service) VerifyEvidenceRefs(ctx context.Context, scope EvidenceScope, refs []EvidenceRef) ([]EvidenceRef, []EvidenceViolation, error) {
	if err := ValidateEvidenceScope(scope); err != nil {
		return nil, nil, err
	}
	events, err := s.store.ListEvidenceEvents(ctx, scope)
	if err != nil {
		return nil, nil, err
	}
	ledger := make(map[string]EvidenceEvent, len(events))
	for _, event := range events {
		ledger[event.EvidenceToken] = event
	}
	verified := make([]EvidenceRef, 0, len(refs))
	violations := make([]EvidenceViolation, 0)
	for _, ref := range refs {
		event, ok := ledger[ref.Token]
		if !ok {
			violations = append(violations, EvidenceViolation{Token: ref.Token, Reason: "not_in_current_turn_ledger"})
			continue
		}
		if ref.DocumentID != "" && ref.DocumentID != event.DocumentID ||
			ref.DocumentVersion != "" && ref.DocumentVersion != event.DocumentVersion ||
			ref.NodeID != "" && ref.NodeID != event.NodeID ||
			ref.NamespaceID != "" && ref.NamespaceID != event.NamespaceID {
			violations = append(violations, EvidenceViolation{Token: ref.Token, Reason: "metadata_mismatch"})
			continue
		}
		ref.Verified = true
		ref.NamespaceID = event.NamespaceID
		ref.DocumentID = event.DocumentID
		ref.DocumentVersion = event.DocumentVersion
		ref.NodeID = event.NodeID
		ref.NodePath = event.NodePath
		ref.StartPage = event.StartPage
		ref.EndPage = event.EndPage
		ref.CitationText = event.CitationText
		verified = append(verified, ref)
	}
	return verified, violations, nil
}

func (s *Service) CurrentTurnEvidence(ctx context.Context, scope EvidenceScope) ([]EvidenceRef, error) {
	if s == nil || s.store == nil {
		return nil, ErrInvalidInput
	}
	if err := ValidateEvidenceScope(scope); err != nil {
		return nil, err
	}
	events, err := s.store.ListEvidenceEvents(ctx, scope)
	if err != nil {
		return nil, err
	}
	refs := make([]EvidenceRef, 0, len(events))
	for _, event := range events {
		refs = append(refs, EvidenceRef{
			Token:           event.EvidenceToken,
			NamespaceID:     event.NamespaceID,
			DocumentID:      event.DocumentID,
			DocumentVersion: event.DocumentVersion,
			NodeID:          event.NodeID,
			NodePath:        event.NodePath,
			StartPage:       event.StartPage,
			EndPage:         event.EndPage,
			CitationText:    event.CitationText,
			Verified:        true,
		})
	}
	return refs, nil
}

func buildEvidenceToken(scope EvidenceScope, doc Document, node TreeNode) string {
	payload := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s|%s",
		scope.SessionID,
		scope.TurnID,
		scope.TraceID,
		scope.DomainID,
		scope.OwnerScope,
		scope.OwnerID,
		doc.NamespaceID,
		doc.ID,
		doc.Version,
		node.ID,
	)
	sum := sha256.Sum256([]byte(payload))
	return "kbref_" + base64.RawURLEncoding.EncodeToString(sum[:])
}
