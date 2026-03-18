package kanban

import "testing"

func TestPermissionProfilePlanThenFullAccessParsingAndNormalization(t *testing.T) {
	if got := NormalizePermissionProfile("plan_then_full_access"); got != PermissionProfilePlanThenFullAccess {
		t.Fatalf("expected normalized plan-first profile, got %q", got)
	}
	if got := NormalizePermissionProfile("PLAN-THEN-FULL-ACCESS"); got != PermissionProfilePlanThenFullAccess {
		t.Fatalf("expected case-insensitive normalization, got %q", got)
	}

	profile, err := ParsePermissionProfile("plan-then-full-access")
	if err != nil {
		t.Fatalf("ParsePermissionProfile: %v", err)
	}
	if profile != PermissionProfilePlanThenFullAccess {
		t.Fatalf("expected parsed plan-first profile, got %q", profile)
	}
}

func TestParsePermissionProfileRejectsUnknownProfile(t *testing.T) {
	if _, err := ParsePermissionProfile("admin-mode"); err == nil {
		t.Fatal("expected invalid permission profile error")
	}
}

func TestNormalizeCollaborationModeOverrideSupportsKnownValues(t *testing.T) {
	if got := NormalizeCollaborationModeOverride("plan"); got != CollaborationModeOverridePlan {
		t.Fatalf("expected plan override, got %q", got)
	}
	if got := NormalizeCollaborationModeOverride("DEFAULT"); got != CollaborationModeOverrideDefault {
		t.Fatalf("expected default override, got %q", got)
	}
	if got := NormalizeCollaborationModeOverride("unknown"); got != CollaborationModeOverrideNone {
		t.Fatalf("expected unknown override to normalize to none, got %q", got)
	}
}
