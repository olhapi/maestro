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
