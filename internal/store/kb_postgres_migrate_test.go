package store

import (
	"strings"
	"testing"
)

func TestPostgresMigrateIncludesKBTables(t *testing.T) {
	sql := strings.Join(strings.Fields(pgMigrateKBTables), " ")
	required := []string{
		"CREATE TABLE IF NOT EXISTS kb_namespaces",
		"id TEXT PRIMARY KEY",
		"index_strategy TEXT NOT NULL DEFAULT 'markdown_tree'",
		"CREATE TABLE IF NOT EXISTS kb_documents",
		"namespace_id TEXT NOT NULL REFERENCES kb_namespaces(id) ON DELETE CASCADE",
		"status TEXT NOT NULL DEFAULT 'draft'",
		"CREATE TABLE IF NOT EXISTS kb_tree_nodes",
		"start_page INTEGER NOT NULL DEFAULT 0",
		"end_page INTEGER NOT NULL DEFAULT 0",
		"PRIMARY KEY (document_id, id)",
		"ALTER TABLE kb_tree_nodes ADD COLUMN IF NOT EXISTS start_page",
		"ALTER TABLE kb_tree_nodes ADD COLUMN IF NOT EXISTS end_page",
		"CREATE TABLE IF NOT EXISTS kb_bindings",
		"binding_type TEXT NOT NULL",
		"enabled BOOLEAN NOT NULL DEFAULT TRUE",
		"CREATE TABLE IF NOT EXISTS kb_evidence_events",
		"ALTER TABLE kb_evidence_events ADD COLUMN IF NOT EXISTS start_page",
		"ALTER TABLE kb_evidence_events ADD COLUMN IF NOT EXISTS end_page",
		"evidence_token TEXT NOT NULL DEFAULT ''",
		"verified BOOLEAN NOT NULL DEFAULT FALSE",
		"CREATE TABLE IF NOT EXISTS kb_node_assets",
		"line INTEGER NOT NULL DEFAULT 0",
		"page INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE kb_node_assets ADD COLUMN IF NOT EXISTS line",
		"ALTER TABLE kb_node_assets ADD COLUMN IF NOT EXISTS page",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_namespaces_owner_domain_name",
		"CREATE INDEX IF NOT EXISTS idx_kb_documents_scope_status",
		"CREATE INDEX IF NOT EXISTS idx_kb_tree_nodes_scope",
		"CREATE INDEX IF NOT EXISTS idx_kb_tree_nodes_document_pages",
		"CREATE INDEX IF NOT EXISTS idx_kb_bindings_resolve",
		"CREATE INDEX IF NOT EXISTS idx_kb_evidence_events_session_turn",
		"CREATE INDEX IF NOT EXISTS idx_kb_node_assets_page",
	}
	for _, needle := range required {
		if !strings.Contains(sql, needle) {
			t.Fatalf("pgMigrateKBTables missing %q", needle)
		}
	}
}
