package cs

import "fmt"

func CanTransitionSession(from, to SessionState) bool {
	if from == to {
		return true
	}
	switch from {
	case "", SessionStateInitial:
		return to == SessionStateAIHandling
	case SessionStateAIHandling:
		return to == SessionStateEscalatePending || to == SessionStateResolved
	case SessionStateEscalatePending:
		return to == SessionStateAIHandling || to == SessionStateHumanHandling || to == SessionStateResolved
	case SessionStateHumanHandling:
		return to == SessionStateResolved
	case SessionStateResolved:
		return false
	default:
		return false
	}
}

func ValidateSessionTransition(from, to SessionState) error {
	if CanTransitionSession(from, to) {
		return nil
	}
	return fmt.Errorf("invalid customer service session transition %q -> %q", from, to)
}

func CanTransitionEscalation(from, to EscalationStatus) bool {
	if from == to && from != EscalationSent && from != EscalationCanceled {
		return true
	}
	switch from {
	case EscalationOpen:
		return to == EscalationQueued || to == EscalationCanceled
	case EscalationQueued:
		return to == EscalationSent || to == EscalationFailed || to == EscalationCanceled
	case EscalationFailed:
		return to == EscalationQueued || to == EscalationCanceled
	case EscalationSent, EscalationCanceled:
		return false
	default:
		return false
	}
}

func ValidateEscalationTransition(from, to EscalationStatus) error {
	if CanTransitionEscalation(from, to) {
		return nil
	}
	return fmt.Errorf("invalid escalation transition %q -> %q", from, to)
}
