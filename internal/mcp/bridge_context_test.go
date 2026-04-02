package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBridgeIssueContextDiscoveryAndCaching(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	store := testStore(t, dbPath)

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	project, err := store.CreateProject("Bridge discovery project", "", workspace, filepath.Join(workspace, "WORKFLOW.md"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	blankIssue, err := store.CreateIssue(project.ID, "", "Blank workspace issue", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue blank: %v", err)
	}
	blankWorkspace := filepath.Join(t.TempDir(), "blank-workspace")
	if _, err := store.CreateWorkspace(blankIssue.ID, blankWorkspace); err != nil {
		t.Fatalf("CreateWorkspace blank: %v", err)
	}
	if _, err := store.UpdateWorkspacePath(blankIssue.ID, ""); err != nil {
		t.Fatalf("UpdateWorkspacePath blank: %v", err)
	}

	matchIssue, err := store.CreateIssue(project.ID, "", "Match workspace issue", "", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue match: %v", err)
	}
	if _, err := store.CreateWorkspace(matchIssue.ID, workspace); err != nil {
		t.Fatalf("CreateWorkspace match: %v", err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldwd)
	}()
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir workspace: %v", err)
	}

	bridge := &stdioBridge{dbPath: dbPath}
	ctx, ok := bridge.issueContextForCurrentWorkspace()
	if !ok || ctx == nil {
		t.Fatal("expected issue context discovery to succeed")
	}
	if ctx.IssueID != matchIssue.ID || ctx.IssueIdentifier != matchIssue.Identifier {
		t.Fatalf("unexpected discovered issue context: %+v", ctx)
	}
	if ctx.WorkspacePath != filepath.Clean(workspace) {
		t.Fatalf("unexpected discovered workspace path: %+v", ctx)
	}

	otherWorkspace := filepath.Join(t.TempDir(), "other-workspace")
	if err := os.MkdirAll(otherWorkspace, 0o755); err != nil {
		t.Fatalf("mkdir other workspace: %v", err)
	}
	if err := os.Chdir(otherWorkspace); err != nil {
		t.Fatalf("Chdir other workspace: %v", err)
	}

	cached, ok := bridge.issueContextForCurrentWorkspace()
	if !ok || cached == nil {
		t.Fatal("expected cached issue context to be reused")
	}
	if !reflect.DeepEqual(cached, ctx) {
		t.Fatalf("expected cached issue context to match first discovery: got %+v want %+v", cached, ctx)
	}

	var nilBridge *stdioBridge
	if got, ok := nilBridge.issueContextForCurrentWorkspace(); ok || got != nil {
		t.Fatalf("expected nil bridge to return no issue context, got %+v ok=%t", got, ok)
	}

	if got, err := discoverBridgeIssueContext(dbPath); err != nil {
		t.Fatalf("discoverBridgeIssueContext cached cwd should succeed, got %v", err)
	} else if got != nil {
		t.Fatalf("expected direct discovery to respect the current cwd, got %+v", got)
	}

	mismatchWorkspace := filepath.Join(t.TempDir(), "mismatch-workspace")
	if err := os.MkdirAll(mismatchWorkspace, 0o755); err != nil {
		t.Fatalf("mkdir mismatch workspace: %v", err)
	}
	if err := os.Chdir(mismatchWorkspace); err != nil {
		t.Fatalf("Chdir mismatch workspace: %v", err)
	}
	if got, err := discoverBridgeIssueContext(dbPath); err != nil {
		t.Fatalf("discoverBridgeIssueContext mismatch cwd: %v", err)
	} else if got != nil {
		t.Fatalf("expected no discovery match for mismatched cwd, got %+v", got)
	}
}

func TestBridgeJSONObjectFromAnyBranches(t *testing.T) {
	if got, err := jsonObjectFromAny(nil); err != nil {
		t.Fatalf("jsonObjectFromAny nil: %v", err)
	} else if len(got) != 0 {
		t.Fatalf("expected nil input to become empty map, got %#v", got)
	}

	if got, err := jsonObjectFromAny(map[string]interface{}{"nested": map[string]interface{}{"k": "v"}}); err != nil {
		t.Fatalf("jsonObjectFromAny map: %v", err)
	} else if !reflect.DeepEqual(got, map[string]any{"nested": map[string]any{"k": "v"}}) {
		t.Fatalf("unexpected map conversion: %#v", got)
	}

	if _, err := jsonObjectFromAny(make(chan int)); err == nil {
		t.Fatal("expected marshal failure for unsupported value")
	}
}
