package store

import (
	"strings"
	"testing"
)

func TestPostgresDefaultPermissionRulesUseMinimalIMPolicy(t *testing.T) {
	for _, required := range []string{
		`"tool_name":"send_im_message","action":"allow"`,
		`"tool_name":"feishu_api","pattern":"create_approval","action":"ask"`,
		`"tool_name":"feishu_api","action":"allow"`,
		`"tool_name":"memory","pattern":"delete","action":"ask"`,
		`"tool_name":"taskboard","pattern":"delete","action":"ask"`,
	} {
		if !strings.Contains(pgDefaultPermissionRulesJSON, required) {
			t.Fatalf("pgDefaultPermissionRulesJSON missing %s in %s", required, pgDefaultPermissionRulesJSON)
		}
	}
	for _, forbidden := range []string{
		`"tool_name":"send_im_message","action":"ask"`,
		`"tool_name":"feishu_api","action":"ask"`,
	} {
		if strings.Contains(pgDefaultPermissionRulesJSON, forbidden) {
			t.Fatalf("pgDefaultPermissionRulesJSON contains legacy blanket ask %s in %s", forbidden, pgDefaultPermissionRulesJSON)
		}
	}
	if !strings.Contains(pgSeedDefaultConfigs, pgDefaultPermissionRulesJSON) {
		t.Fatal("pgSeedDefaultConfigs must seed the current default permission rules")
	}
	if !strings.Contains(pgFixDefaultPermissionRules, pgLegacyPermissionRulesJSON) || !strings.Contains(pgFixDefaultPermissionRules, pgDefaultPermissionRulesJSON) {
		t.Fatal("pgFixDefaultPermissionRules must migrate exactly from legacy default rules to current defaults")
	}
}
