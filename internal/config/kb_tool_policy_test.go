package config

import "testing"

func TestDefaultToolPolicyConfigIncludesKBGroupInMasterDirect(t *testing.T) {
	cfg := defaultToolPolicyConfig()

	var foundGroup bool
	for _, group := range cfg.Groups {
		if group.Name != "kb" {
			continue
		}
		foundGroup = true
		for _, want := range []string{"kb.doc.meta", "kb.doc.structure", "kb.section.text"} {
			if !containsToolPolicyString(group.Tools, want) {
				t.Fatalf("kb group tools = %+v, missing %s", group.Tools, want)
			}
		}
	}
	if !foundGroup {
		t.Fatalf("default tool policy groups = %+v, missing kb", cfg.Groups)
	}

	var foundProfile bool
	for _, profile := range cfg.Profiles {
		if profile.Name != "master_direct" {
			continue
		}
		foundProfile = true
		if !containsToolPolicyString(profile.Tools, "group:kb") {
			t.Fatalf("master_direct tools = %+v, want group:kb", profile.Tools)
		}
	}
	if !foundProfile {
		t.Fatalf("default tool policy profiles = %+v, missing master_direct", cfg.Profiles)
	}
}

func containsToolPolicyString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
