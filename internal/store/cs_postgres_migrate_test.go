package store

import (
	"strings"
	"testing"
)

func TestPostgresMigrateIncludesCustomerServiceTables(t *testing.T) {
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS cs_sessions",
		"CONSTRAINT cs_sessions_state_check",
		"CREATE TABLE IF NOT EXISTS cs_escalations",
		"CREATE TABLE IF NOT EXISTS cs_webhook_subscriptions",
		"CREATE TABLE IF NOT EXISTS cs_webhook_outbox",
	} {
		if !strings.Contains(pgInitSQL, want) {
			t.Fatalf("pgInitSQL missing %q", want)
		}
	}
}
