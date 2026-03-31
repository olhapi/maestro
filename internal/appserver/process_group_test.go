//go:build !windows

package appserver

import (
	"testing"
)

func TestProcessGroupHelperBranches(t *testing.T) {
	if err := CleanupLingeringAppServerProcess(0); err != nil {
		t.Fatalf("CleanupLingeringAppServerProcess zero pid: %v", err)
	}
	if err := CleanupLingeringAppServerProcess(-1); err != nil {
		t.Fatalf("CleanupLingeringAppServerProcess negative pid: %v", err)
	}
	if err := CleanupLingeringAppServerProcess(999999); err != nil {
		t.Fatalf("CleanupLingeringAppServerProcess missing pid: %v", err)
	}

	if managedProcessExists(0) || managedProcessGroupExists(0) {
		t.Fatal("expected zero pid to report no process or process group")
	}
	if !waitForManagedProcessGroupExit(0, 0) {
		t.Fatal("expected zero pid to count as exited")
	}
	if err := terminateManagedProcessTree(0, 0, 0); err != nil {
		t.Fatalf("terminateManagedProcessTree zero pid: %v", err)
	}
	if err := terminateManagedProcessTree(-1, 0, 0); err != nil {
		t.Fatalf("terminateManagedProcessTree negative pid: %v", err)
	}
	configureManagedProcess(nil)
}
