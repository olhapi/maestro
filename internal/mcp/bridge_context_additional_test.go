package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMCPBridgeJSONObjectAndWorkspaceDiscoveryBranches(t *testing.T) {
	t.Run("json object conversion", func(t *testing.T) {
		if got, err := jsonObjectFromAny(nil); err != nil || len(got) != 0 {
			t.Fatalf("expected nil input to return an empty object, got %#v %v", got, err)
		}

		source := map[string]interface{}{
			"nested": map[string]interface{}{
				"items": []interface{}{
					map[string]interface{}{"ok": true},
					"value",
				},
			},
		}
		got, err := jsonObjectFromAny(source)
		if err != nil {
			t.Fatalf("jsonObjectFromAny failed: %v", err)
		}
		if !reflect.DeepEqual(got, source) {
			t.Fatalf("unexpected json object conversion:\n got %#v\nwant %#v", got, source)
		}

		if _, err := jsonObjectFromAny(map[string]interface{}{"bad": make(chan int)}); err == nil {
			t.Fatal("expected jsonObjectFromAny to reject unsupported values")
		}
	})

	t.Run("workspace discovery and cache", func(t *testing.T) {
		store := testStore(t, "")
		workspace := filepath.Join(t.TempDir(), "workspace")
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			t.Fatalf("mkdir workspace: %v", err)
		}
		project, err := store.CreateProject("Bridge project", "", workspace, filepath.Join(workspace, "WORKFLOW.md"))
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		issue, err := store.CreateIssue(project.ID, "", "Bridge issue", "", 1, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if _, err := store.CreateWorkspace(issue.ID, workspace); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}

		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Chdir(cwd)
		})

		if err := os.Chdir(workspace); err != nil {
			t.Fatalf("Chdir workspace: %v", err)
		}

		discovered, err := discoverBridgeIssueContext(store.DBPath())
		if err != nil {
			t.Fatalf("discoverBridgeIssueContext failed: %v", err)
		}
		if discovered == nil {
			t.Fatal("expected issue context for matching workspace")
		}
		if discovered.IssueID != issue.ID || discovered.IssueIdentifier != issue.Identifier || discovered.ProjectID != project.ID || discovered.WorkspacePath != filepath.Clean(workspace) {
			t.Fatalf("unexpected discovered issue context: %#v", discovered)
		}

		bridge := &stdioBridge{dbPath: store.DBPath()}
		first, ok := bridge.issueContextForCurrentWorkspace()
		if !ok || first == nil {
			t.Fatal("expected issueContextForCurrentWorkspace to discover the current workspace")
		}
		second, ok := bridge.issueContextForCurrentWorkspace()
		if !ok || second == nil {
			t.Fatal("expected issueContextForCurrentWorkspace cache to remain available")
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("expected cached workspace context to stay stable: %#v %#v", first, second)
		}

		otherDir := filepath.Join(t.TempDir(), "other")
		if err := os.MkdirAll(otherDir, 0o755); err != nil {
			t.Fatalf("mkdir other dir: %v", err)
		}
		if err := os.Chdir(otherDir); err != nil {
			t.Fatalf("Chdir other dir: %v", err)
		}
		if noMatch, err := discoverBridgeIssueContext(store.DBPath()); err != nil || noMatch != nil {
			t.Fatalf("expected no match for a different workspace, got %#v %v", noMatch, err)
		}

		var nilBridge *stdioBridge
		if ctx, ok := nilBridge.issueContextForCurrentWorkspace(); ok || ctx != nil {
			t.Fatalf("expected nil bridge to return no issue context, got %#v %v", ctx, ok)
		}
	})
}
