package speccheck

import "testing"

func TestRun(t *testing.T) {
	r := Run("../../")
	if !r.OK {
		t.Fatalf("expected spec check OK, got %+v", r)
	}
	if r.Checks["orchestrator"] != "ok" {
		t.Fatalf("orchestrator check missing")
	}
}
