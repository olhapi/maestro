package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateWorkspacePathBranches(t *testing.T) {
	root := t.TempDir()
	missingWorkspace := filepath.Join(root, "workspace")

	got, err := validateWorkspacePath(missingWorkspace, root)
	if err != nil {
		t.Fatalf("validateWorkspacePath missing workspace: %v", err)
	}
	if got != filepath.Clean(missingWorkspace) {
		t.Fatalf("validateWorkspacePath missing workspace = %q, want %q", got, missingWorkspace)
	}

	if _, err := validateWorkspacePath(filepath.Join(root, "..", "outside"), root); err == nil {
		t.Fatal("expected path escape to fail")
	}

	if runtime.GOOS == "windows" {
		t.Skip("symlink branch is not reliable on Windows")
	}

	outside := t.TempDir()
	linkPath := filepath.Join(root, "linked-workspace")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := validateWorkspacePath(linkPath, root); err == nil {
		t.Fatal("expected symlink escape to fail")
	}
}

