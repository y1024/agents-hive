package agentquality

import (
	"context"
	"fmt"
)

type CandidateFilter struct {
	Status CandidateStatus
	Route  string
	Limit  int
	Offset int
}

type CandidateStore interface {
	UpsertCandidate(ctx context.Context, rec CandidateRecord) (*CandidateRecord, error)
	ListCandidates(ctx context.Context, filter CandidateFilter) ([]CandidateRecord, int, error)
	GetCandidate(ctx context.Context, id string) (*CandidateRecord, bool, error)
	UpdateCandidateStatus(ctx context.Context, id string, status CandidateStatus, reviewer, note, promotedCaseID string) error
}

func ValidateCandidateStatus(status CandidateStatus) error {
	switch status {
	case CandidateNew, CandidateReviewing, CandidateApproved, CandidateRejected, CandidatePromoted, CandidatePromotedVerified, CandidatePromotedRegressed:
		return nil
	default:
		return fmt.Errorf("invalid candidate status %q", status)
	}
}

func ValidateCandidateTransition(from, to CandidateStatus) error {
	if err := ValidateCandidateStatus(to); err != nil {
		return err
	}
	if from == "" {
		from = CandidateNew
	}
	if err := ValidateCandidateStatus(from); err != nil {
		return err
	}
	if from == to {
		return nil
	}

	switch from {
	case CandidateNew:
		switch to {
		case CandidateReviewing, CandidateApproved, CandidateRejected:
			return nil
		}
	case CandidateReviewing:
		switch to {
		case CandidateNew, CandidateApproved, CandidateRejected:
			return nil
		}
	case CandidateApproved:
		switch to {
		case CandidateReviewing, CandidateRejected, CandidatePromoted:
			return nil
		}
	case CandidateRejected:
		return fmt.Errorf("candidate transition %s -> %s is not allowed", from, to)
	case CandidatePromoted:
		switch to {
		case CandidatePromotedVerified, CandidatePromotedRegressed:
			return nil
		}
		return fmt.Errorf("candidate transition %s -> %s is not allowed", from, to)
	case CandidatePromotedVerified, CandidatePromotedRegressed:
		return fmt.Errorf("candidate transition %s -> %s is not allowed", from, to)
	}
	return fmt.Errorf("candidate transition %s -> %s is not allowed", from, to)
}
