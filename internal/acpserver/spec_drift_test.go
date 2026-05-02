package acpserver

import (
	"errors"
	"reflect"
	"testing"
)

func TestACPValidatedMethodFixtureMatchesSupportedMethods(t *testing.T) {
	fixture := ACPMethodFixture{
		Name: "acp-validated-inbound-methods-2026-04-30",
		Methods: []string{
			"initialize",
			"session/new",
			"session/prompt",
			"session/cancel",
			"session/request_permission",
		},
	}

	result := CheckACPMethodDrift(fixture)
	if result.HasDrift() {
		t.Fatalf("ACP method fixture drifted: missing=%v extra=%v invalid=%v duplicate=%v", result.Missing, result.Extra, result.Invalid, result.Duplicate)
	}
}

func TestCheckACPMethodDriftReportsMissingExtraInvalidAndDuplicateMethods(t *testing.T) {
	result := CheckACPMethodDrift(ACPMethodFixture{
		Name: "broken-fixture",
		Methods: []string{
			"initialize",
			"session/new",
			"session/new",
			"session/cancel",
			"session/request_permission",
			"",
			"session/unknown",
		},
	})

	if !result.HasDrift() {
		t.Fatal("CheckACPMethodDrift reported no drift for a broken fixture")
	}
	if !reflect.DeepEqual(result.Missing, []string{"session/prompt"}) {
		t.Fatalf("missing mismatch: got %v", result.Missing)
	}
	if !reflect.DeepEqual(result.Extra, []string{"session/unknown"}) {
		t.Fatalf("extra mismatch: got %v", result.Extra)
	}
	if !reflect.DeepEqual(result.Invalid, []string{""}) {
		t.Fatalf("invalid mismatch: got %v", result.Invalid)
	}
	if !reflect.DeepEqual(result.Duplicate, []string{"session/new"}) {
		t.Fatalf("duplicate mismatch: got %v", result.Duplicate)
	}
	if err := result.Err(); err == nil {
		t.Fatal("Err returned nil for drift result")
	} else if !errors.Is(err, ErrACPMethodFixtureDrift) {
		t.Fatalf("Err mismatch: got %v", err)
	}
}
