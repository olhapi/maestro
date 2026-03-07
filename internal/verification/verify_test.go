package verification

import (
	"path/filepath"
	"testing"
)

func TestRunVerification(t *testing.T) {
	tmp := t.TempDir()
	db := filepath.Join(tmp, "db", "symphony.db")
	res := Run(tmp, db)
	if !res.OK {
		t.Fatalf("expected ok result, got %+v", res)
	}
	if res.Checks["workflow"] != "ok" {
		t.Fatalf("workflow check failed: %+v", res)
	}
	if res.Checks["db_open"] != "ok" {
		t.Fatalf("db check failed: %+v", res)
	}
}
