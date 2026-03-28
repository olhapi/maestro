package kanban

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/appserver"
)

func setupTestStore(t *testing.T) *Store {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	t.Cleanup(func() {
		store.Close()
	})

	return store
}

func issueListContainsIdentifier(issues []Issue, identifier string) bool {
	for _, issue := range issues {
		if issue.Identifier == identifier {
			return true
		}
	}
	return false
}

func issueSummaryListContainsIdentifier(issues []IssueSummary, identifier string) bool {
	for _, issue := range issues {
		if issue.Identifier == identifier {
			return true
		}
	}
	return false
}

func samplePNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}
}

func runGitForStoreTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitStoreTestEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output))
}

func gitStoreTestEnv() []string {
	env := os.Environ()
	filtered := env[:0]
	for _, value := range env {
		if !strings.HasPrefix(value, "GIT_") {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func initGitRepoForStoreTest(t *testing.T, repoPath string) {
	t.Helper()
	runGitForStoreTest(t, repoPath, "init")
	runGitForStoreTest(t, repoPath, "config", "user.email", "maestro-tests@example.com")
	runGitForStoreTest(t, repoPath, "config", "user.name", "Maestro Tests")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("repo\n"), 0o644); err != nil {
		t.Fatalf("WriteFile README.md: %v", err)
	}
	runGitForStoreTest(t, repoPath, "add", "README.md")
	runGitForStoreTest(t, repoPath, "commit", "-m", "test init")
	runGitForStoreTest(t, repoPath, "branch", "-M", "main")
}

func TestDefaultDBPathUsesHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := DefaultDBPath()
	want := filepath.Join(home, ".maestro", "maestro.db")
	if got != want {
		t.Fatalf("DefaultDBPath() = %q, want %q", got, want)
	}
}

func TestResolveDBPathUsesDefaultWhenEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := ResolveDBPath("")
	want := filepath.Join(home, ".maestro", "maestro.db")
	if got != want {
		t.Fatalf("ResolveDBPath(\"\") = %q, want %q", got, want)
	}
}

func TestResolveDBPathPreservesExplicitPath(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom.db")
	if got := ResolveDBPath(want); got != want {
		t.Fatalf("ResolveDBPath(%q) = %q", want, got)
	}
}

func TestResolveDBPathExpandsEnvAndHomePaths(t *testing.T) {
	homeDir := t.TempDir()
	dbDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("MAESTRO_DB_DIR", dbDir)

	if got := ResolveDBPath("$MAESTRO_DB_DIR/maestro.db"); got != filepath.Join(dbDir, "maestro.db") {
		t.Fatalf("expected env-expanded db path, got %s", got)
	}
	if got := ResolveDBPath("~/maestro.db"); got != filepath.Join(homeDir, "maestro.db") {
		t.Fatalf("expected home-expanded db path, got %s", got)
	}
}

func TestHasUnresolvedExpandedEnvPath(t *testing.T) {
	tests := []struct {
		name     string
		rawPath  string
		resolved string
		want     bool
	}{
		{
			name:     "reject unresolved env segment",
			rawPath:  "$HOME/.maestro/$TEAM/maestro.db",
			resolved: "/Users/test/.maestro/$TEAM/maestro.db",
			want:     true,
		},
		{
			name:     "allow literal dollar sign inside filename",
			rawPath:  "$HOME/.maestro/price$5/maestro.db",
			resolved: "/Users/test/.maestro/price$5/maestro.db",
			want:     false,
		},
		{
			name:     "allow expanded env path",
			rawPath:  "$HOME/.maestro/maestro.db",
			resolved: "/Users/test/.maestro/maestro.db",
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasUnresolvedExpandedEnvPath(tc.rawPath, tc.resolved); got != tc.want {
				t.Fatalf("HasUnresolvedExpandedEnvPath(%q, %q) = %v, want %v", tc.rawPath, tc.resolved, got, tc.want)
			}
		})
	}
}

func TestNewStoreRejectsUnresolvedEnvironmentDatabasePaths(t *testing.T) {
	t.Setenv("MAESTRO_DB_DIR", "")

	_, err := NewStore("$MAESTRO_DB_DIR/maestro.db")
	if err == nil || !strings.Contains(err.Error(), "unresolved environment variable") {
		t.Fatalf("expected unresolved environment variable error, got %v", err)
	}
}

func TestIssuePlanApprovalLifecyclePersistsAndPromotes(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Plan approval lifecycle", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateIssuePermissionProfile(issue.ID, PermissionProfilePlanThenFullAccess); err != nil {
		t.Fatalf("UpdateIssuePermissionProfile: %v", err)
	}

	requestedAt := time.Date(2026, 3, 18, 9, 30, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Investigate, then implement.", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}

	pending, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue pending: %v", err)
	}
	if !pending.PlanApprovalPending || pending.PendingPlanMarkdown != "Investigate, then implement." {
		t.Fatalf("expected pending plan approval state, got %+v", pending)
	}
	if pending.PendingPlanRequestedAt == nil || !pending.PendingPlanRequestedAt.Equal(requestedAt) {
		t.Fatalf("unexpected requested_at: %+v", pending.PendingPlanRequestedAt)
	}

	revisionRequestedAt := requestedAt.Add(5 * time.Minute)
	if err := store.SetIssuePendingPlanRevision(issue.ID, "Tighten the rollout steps.", revisionRequestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision: %v", err)
	}

	pending, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue revision pending: %v", err)
	}
	if pending.PendingPlanRevisionMarkdown != "Tighten the rollout steps." {
		t.Fatalf("expected pending plan revision markdown, got %+v", pending)
	}
	if pending.PendingPlanRevisionRequestedAt == nil || !pending.PendingPlanRevisionRequestedAt.Equal(revisionRequestedAt) {
		t.Fatalf("unexpected revision requested_at: %+v", pending.PendingPlanRevisionRequestedAt)
	}

	if err := store.ApproveIssuePlan(issue.ID); err != nil {
		t.Fatalf("ApproveIssuePlan: %v", err)
	}

	approved, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue approved: %v", err)
	}
	if approved.PermissionProfile != PermissionProfileFullAccess {
		t.Fatalf("expected full-access after approval, got %q", approved.PermissionProfile)
	}
	if approved.CollaborationModeOverride != CollaborationModeOverrideDefault {
		t.Fatalf("expected default collaboration override after approval, got %q", approved.CollaborationModeOverride)
	}
	if approved.PlanApprovalPending || approved.PendingPlanMarkdown != "" || approved.PendingPlanRequestedAt != nil {
		t.Fatalf("expected cleared pending approval state, got %+v", approved)
	}
	if approved.PendingPlanRevisionMarkdown != "" || approved.PendingPlanRevisionRequestedAt != nil {
		t.Fatalf("expected cleared pending revision state, got %+v", approved)
	}
}

func TestApproveIssuePlanWithNotePersistsApprovalEventAndCommand(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Plan approval with note", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	approvedAt := time.Date(2026, 3, 18, 11, 45, 0, 0, time.UTC)
	command, err := store.ApproveIssuePlanWithNote(issue, approvedAt, "Ship the guarded rollout.", "")
	if err != nil {
		t.Fatalf("ApproveIssuePlanWithNote: %v", err)
	}
	if command == nil {
		t.Fatal("expected follow-up command to be created")
	}
	if command.Command != "Ship the guarded rollout." {
		t.Fatalf("unexpected follow-up command: %+v", command)
	}
	if command.Status != IssueAgentCommandPending {
		t.Fatalf("expected pending follow-up command, got %+v", command)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.PermissionProfile != PermissionProfileFullAccess {
		t.Fatalf("expected issue to promote to full access, got %+v", updated)
	}
	if updated.CollaborationModeOverride != CollaborationModeOverrideDefault {
		t.Fatalf("expected default collaboration override after approval, got %+v", updated)
	}
	if updated.PlanApprovalPending {
		t.Fatalf("expected plan approval to clear after approval, got %+v", updated)
	}

	events, err := store.ListIssueRuntimeEvents(issue.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	var approved *RuntimeEvent
	for i := range events {
		if events[i].Kind == "plan_approved" {
			approved = &events[i]
			break
		}
	}
	if approved == nil {
		t.Fatalf("expected plan_approved runtime event, got %#v", events)
	}
	if got := approved.Payload["approved_at"]; got != approvedAt.Format(time.RFC3339) {
		t.Fatalf("unexpected approved_at payload: %#v", got)
	}
}

func TestClearIssuePendingPlanApprovalClearsStateAndAppendsChange(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Clear plan approval", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	requestedAt := time.Date(2026, 3, 18, 9, 45, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApproval(issue.ID, "Review the rollout plan.", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval: %v", err)
	}
	if err := store.ClearIssuePendingPlanApproval(issue.ID, "manual_retry"); err != nil {
		t.Fatalf("ClearIssuePendingPlanApproval: %v", err)
	}

	cleared, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue cleared: %v", err)
	}
	if cleared.PlanApprovalPending || cleared.PendingPlanMarkdown != "" || cleared.PendingPlanRequestedAt != nil {
		t.Fatalf("expected pending approval state to clear, got %+v", cleared)
	}
}

func TestIssuePlanRevisionLifecyclePersistsAndClears(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Plan revision lifecycle", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	requestedAt := time.Date(2026, 3, 18, 10, 15, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanRevision(issue.ID, "Trim the rollout and keep the rollback explicit.", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision: %v", err)
	}

	pending, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue pending revision: %v", err)
	}
	if pending.PendingPlanRevisionMarkdown != "Trim the rollout and keep the rollback explicit." {
		t.Fatalf("unexpected pending revision markdown, got %+v", pending)
	}
	if pending.PendingPlanRevisionRequestedAt == nil || !pending.PendingPlanRevisionRequestedAt.Equal(requestedAt) {
		t.Fatalf("unexpected pending revision requested_at, got %+v", pending.PendingPlanRevisionRequestedAt)
	}

	if err := store.ClearIssuePendingPlanRevision(issue.ID, "manual_retry"); err != nil {
		t.Fatalf("ClearIssuePendingPlanRevision: %v", err)
	}

	cleared, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue cleared revision: %v", err)
	}
	if cleared.PendingPlanRevisionMarkdown != "" || cleared.PendingPlanRevisionRequestedAt != nil {
		t.Fatalf("expected pending revision state to clear, got %+v", cleared)
	}
}

func TestAppendRuntimeEventOnlyPersistsStandaloneEvent(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Standalone runtime event", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	requestedAt := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	clearedAt := requestedAt.Add(2 * time.Minute)
	if err := store.AppendRuntimeEventOnly("plan_revision_cleared", map[string]interface{}{
		"issue_id":     issue.ID,
		"identifier":   issue.Identifier,
		"title":        issue.Title,
		"phase":        string(issue.WorkflowPhase),
		"attempt":      2,
		"markdown":     "Trim the rollout and keep the rollback explicit.",
		"reason":       "turn_started",
		"requested_at": requestedAt.Format(time.RFC3339),
		"cleared_at":   clearedAt.Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("AppendRuntimeEventOnly: %v", err)
	}

	events, err := store.ListIssueRuntimeEvents(issue.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one runtime event, got %#v", events)
	}
	if events[0].Kind != "plan_revision_cleared" {
		t.Fatalf("unexpected runtime event kind: %+v", events[0])
	}
	if got := events[0].Payload["markdown"]; got != "Trim the rollout and keep the rollback explicit." {
		t.Fatalf("unexpected markdown payload: %#v", got)
	}
	if got := events[0].Payload["reason"]; got != "turn_started" {
		t.Fatalf("unexpected reason payload: %#v", got)
	}
	if got := events[0].Payload["requested_at"]; got != requestedAt.Format(time.RFC3339) {
		t.Fatalf("unexpected requested_at payload: %#v", got)
	}
	if got := events[0].Payload["cleared_at"]; got != clearedAt.Format(time.RFC3339) {
		t.Fatalf("unexpected cleared_at payload: %#v", got)
	}
}

func TestUpdateProjectPermissionProfileClearsInheritedPendingPlanApproval(t *testing.T) {
	store := setupTestStore(t)
	project, err := store.CreateProject("Demo", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Inherited plan approval", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.UpdateProjectPermissionProfile(project.ID, PermissionProfilePlanThenFullAccess); err != nil {
		t.Fatalf("UpdateProjectPermissionProfile plan-first: %v", err)
	}
	requestedAt := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"collaboration_mode_override": CollaborationModeOverridePlan,
		"plan_approval_pending":       true,
		"pending_plan_markdown":       "Approve me",
		"pending_plan_requested_at":   &requestedAt,
	}); err != nil {
		t.Fatalf("UpdateIssue pending plan state: %v", err)
	}

	if err := store.UpdateProjectPermissionProfile(project.ID, PermissionProfileFullAccess); err != nil {
		t.Fatalf("UpdateProjectPermissionProfile full-access: %v", err)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.PlanApprovalPending || updated.PendingPlanMarkdown != "" || updated.PendingPlanRequestedAt != nil {
		t.Fatalf("expected inherited pending approval cleared, got %+v", updated)
	}
	if updated.CollaborationModeOverride != CollaborationModeOverrideNone {
		t.Fatalf("expected collaboration override cleared, got %q", updated.CollaborationModeOverride)
	}
}

func TestIssuePlanApprovalHelpersValidateAndClearOverrideState(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Plan helper validation", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if err := store.SetIssuePendingPlanApproval("", "plan", time.Now().UTC()); err == nil {
		t.Fatal("expected validation error for missing issue id")
	}
	if err := store.SetIssuePendingPlanApproval(issue.ID, "   ", time.Now().UTC()); err == nil {
		t.Fatal("expected validation error for empty markdown")
	}
	if err := store.SetIssuePendingPlanApproval("missing", "plan", time.Now().UTC()); !IsNotFound(err) {
		t.Fatalf("expected missing issue error, got %v", err)
	}
	if err := store.ApproveIssuePlan(""); err == nil {
		t.Fatal("expected validation error for missing issue id")
	}
	if err := store.ApproveIssuePlan("missing"); !IsNotFound(err) {
		t.Fatalf("expected missing issue error, got %v", err)
	}

	requestedAt := time.Date(2026, 3, 18, 13, 0, 0, 0, time.UTC)
	if err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"collaboration_mode_override": CollaborationModeOverridePlan,
		"plan_approval_pending":       true,
		"pending_plan_markdown":       "Draft plan",
		"pending_plan_requested_at":   &requestedAt,
	}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	if err := store.UpdateIssuePermissionProfile(issue.ID, PermissionProfileDefault); err != nil {
		t.Fatalf("UpdateIssuePermissionProfile: %v", err)
	}
	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.CollaborationModeOverride != CollaborationModeOverrideNone {
		t.Fatalf("expected collaboration override cleared, got %q", updated.CollaborationModeOverride)
	}
	if updated.PlanApprovalPending || updated.PendingPlanMarkdown != "" || updated.PendingPlanRequestedAt != nil {
		t.Fatalf("expected pending plan state cleared, got %+v", updated)
	}

	if err := store.UpdateIssuePermissionProfile("missing", PermissionProfileFullAccess); !IsNotFound(err) {
		t.Fatalf("expected missing issue error, got %v", err)
	}
	if err := store.UpdateProjectPermissionProfile("missing", PermissionProfileFullAccess); !IsNotFound(err) {
		t.Fatalf("expected missing project error, got %v", err)
	}
}

func TestUpdateProjectStateNormalizesAndPersists(t *testing.T) {
	store := setupTestStore(t)
	project, err := store.CreateProject("Demo", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := store.UpdateProjectState(project.ID, ProjectState("RUNNING")); err != nil {
		t.Fatalf("UpdateProjectState: %v", err)
	}
	updated, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if updated.State != ProjectStateRunning {
		t.Fatalf("expected running state, got %q", updated.State)
	}

	if err := store.UpdateProjectState("proj-missing", ProjectStateRunning); !IsNotFound(err) {
		t.Fatalf("expected missing project error, got %v", err)
	}
}

func TestIssueAssetRootUsesDBLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got, want := IssueAssetRoot(""), filepath.Join(home, ".maestro", "assets", "issues"); got != want {
		t.Fatalf("IssueAssetRoot(\"\") = %q, want %q", got, want)
	}

	explicit := filepath.Join(t.TempDir(), "nested", "maestro.db")
	if got, want := IssueAssetRoot(explicit), filepath.Join(filepath.Dir(explicit), "assets", "issues"); got != want {
		t.Fatalf("IssueAssetRoot(%q) = %q, want %q", explicit, got, want)
	}
}

func TestProjectStateAndIssueTypeHelpers(t *testing.T) {
	if !ProjectStateRunning.IsValid() {
		t.Fatal("expected running project state to be valid")
	}
	if ProjectState("paused").IsValid() {
		t.Fatal("expected unknown project state to be invalid")
	}
	if got := DefaultIssueType(); got != IssueTypeStandard {
		t.Fatalf("DefaultIssueType() = %q", got)
	}
	if !(Issue{IssueType: IssueTypeRecurring}).IsRecurring() {
		t.Fatal("expected recurring issue to report recurring")
	}
	if (Issue{IssueType: IssueTypeStandard}).IsRecurring() {
		t.Fatal("expected standard issue to report non-recurring")
	}
}

func TestNewStoreConfiguresSQLitePragmas(t *testing.T) {
	store := setupTestStore(t)

	checkString := func(name, query, want string) {
		t.Helper()
		var got string
		if err := store.db.QueryRow(query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.ToLower(got) != strings.ToLower(want) {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	checkInt := func(name, query string, want int) {
		t.Helper()
		var got int
		if err := store.db.QueryRow(query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Fatalf("%s = %d, want %d", name, got, want)
		}
	}

	checkString("journal_mode", `PRAGMA journal_mode`, "wal")
	checkInt("busy_timeout", `PRAGMA busy_timeout`, 10000)
	checkInt("foreign_keys", `PRAGMA foreign_keys`, 1)
	checkInt("synchronous", `PRAGMA synchronous`, 1)

	stats := store.db.Stats()
	if stats.MaxOpenConnections != sqliteMaxOpenConns {
		t.Fatalf("MaxOpenConnections = %d, want %d", stats.MaxOpenConnections, sqliteMaxOpenConns)
	}

	var table string
	if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'issue_assets'`).Scan(&table); err != nil {
		t.Fatalf("expected issue_assets migration to run: %v", err)
	}
	if table != "issue_assets" {
		t.Fatalf("unexpected issue_assets table result %q", table)
	}
}

func TestNewStoreClosesDBOnMigrationFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "broken.db")
	expected := errors.New("boom")
	var capturedDB *sql.DB

	store, err := newStoreWithMigrator(dbPath, func(store *Store) error {
		capturedDB = store.db
		return expected
	})
	if store != nil {
		t.Fatalf("expected nil store on migration failure, got %#v", store)
	}
	if !errors.Is(err, expected) {
		t.Fatalf("expected wrapped migration error %v, got %v", expected, err)
	}
	if capturedDB == nil {
		t.Fatal("expected migration hook to observe the opened database")
	}
	if pingErr := capturedDB.Ping(); pingErr == nil || !strings.Contains(pingErr.Error(), "closed") {
		t.Fatalf("expected closed database after migration failure, got %v", pingErr)
	}
}

func TestIssueAssetLifecyclePersistsMetadataAndCleansFiles(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Attach assets", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	asset, err := store.CreateIssueAsset(issue.ID, "screen.png", bytes.NewReader(samplePNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueAsset: %v", err)
	}
	if asset.ContentType != "image/png" {
		t.Fatalf("expected image/png, got %q", asset.ContentType)
	}
	if asset.ByteSize <= 0 {
		t.Fatalf("expected non-zero byte size, got %d", asset.ByteSize)
	}

	assets, err := store.ListIssueAssets(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueAssets: %v", err)
	}
	if len(assets) != 1 || assets[0].ID != asset.ID {
		t.Fatalf("unexpected issue assets: %#v", assets)
	}

	detail, err := store.GetIssueDetailByIdentifier(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssueDetailByIdentifier: %v", err)
	}
	if len(detail.Assets) != 1 || detail.Assets[0].ID != asset.ID {
		t.Fatalf("expected issue detail assets, got %#v", detail.Assets)
	}

	_, path, err := store.GetIssueAssetContent(issue.ID, asset.ID)
	if err != nil {
		t.Fatalf("GetIssueAssetContent: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected asset file at %s: %v", path, err)
	}

	if err := store.DeleteIssueAsset(issue.ID, asset.ID); err != nil {
		t.Fatalf("DeleteIssueAsset: %v", err)
	}
	if _, err := store.GetIssueAsset(issue.ID, asset.ID); !IsNotFound(err) {
		t.Fatalf("expected deleted asset to be missing, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected asset file cleanup, got err=%v", err)
	}
}

func TestIssueAssetsAcceptArbitraryContentAndRejectOversize(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Validate uploads", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	asset, err := store.CreateIssueAsset(issue.ID, "notes.txt", strings.NewReader("plain text asset"))
	if err != nil {
		t.Fatalf("CreateIssueAsset text: %v", err)
	}
	if asset.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("expected detected text content type, got %q", asset.ContentType)
	}

	oversized := bytes.NewReader(append(samplePNGBytes(), bytes.Repeat([]byte{0}, int(MaxIssueAssetBytes))...))
	if _, err := store.CreateIssueAsset(issue.ID, "too-large.png", oversized); !IsValidation(err) {
		t.Fatalf("expected oversize validation error, got %v", err)
	}
}

func TestIssueAssetHelpersNormalizeAndGuardPaths(t *testing.T) {
	store := setupTestStore(t)

	if got := normalizeIssueAssetFilename(""); got != "asset" {
		t.Fatalf("normalizeIssueAssetFilename empty = %q", got)
	}
	if got := normalizeIssueAssetFilename("nested/mock"); got != "mock" {
		t.Fatalf("normalizeIssueAssetFilename nested = %q", got)
	}

	validPath, err := store.issueAssetPath("issue-1/asset.txt")
	if err != nil {
		t.Fatalf("issueAssetPath valid: %v", err)
	}
	if want := filepath.Join(store.IssueAssetRoot(), "issue-1", "asset.txt"); validPath != want {
		t.Fatalf("issueAssetPath valid = %q, want %q", validPath, want)
	}

	if _, err := store.issueAssetPath("../escape.txt"); !IsValidation(err) {
		t.Fatalf("expected invalid stored asset path validation error, got %v", err)
	}
}

func TestIssueAssetContentMissingFileReturnsNotFoundAndCleanupHelpersAreSafe(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Missing asset file", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	asset, err := store.CreateIssueAsset(issue.ID, "missing.png", bytes.NewReader(samplePNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueAsset: %v", err)
	}
	_, path, err := store.GetIssueAssetContent(issue.ID, asset.ID)
	if err != nil {
		t.Fatalf("GetIssueAssetContent: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove issue asset: %v", err)
	}

	if _, _, err := store.GetIssueAssetContent(issue.ID, asset.ID); !IsNotFound(err) {
		t.Fatalf("expected missing issue asset to report not found, got %v", err)
	}

	removeIssueAssetFile("")
	removeIssueAssetFile(path)

	dir := filepath.Join(t.TempDir(), "empty-dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll empty dir: %v", err)
	}
	removeIfEmpty(dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected empty dir removal, got %v", err)
	}

	nonEmptyDir := filepath.Join(t.TempDir(), "non-empty-dir")
	if err := os.MkdirAll(nonEmptyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll non-empty dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nonEmptyDir, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile keep.txt: %v", err)
	}
	removeIfEmpty(nonEmptyDir)
	if _, err := os.Stat(nonEmptyDir); err != nil {
		t.Fatalf("expected non-empty dir to remain, got %v", err)
	}
}

func TestWriteIssueAssetTempFileAcceptsValidFilesAndRejectsEmptyInput(t *testing.T) {
	root := filepath.Join(t.TempDir(), "issue-assets")

	contentType, byteSize, path, err := writeIssueAssetTempFile(root, bytes.NewReader(samplePNGBytes()), "screen.png")
	if err != nil {
		t.Fatalf("writeIssueAssetTempFile valid: %v", err)
	}
	if contentType != "image/png" {
		t.Fatalf("expected image/png, got %q", contentType)
	}
	if byteSize != int64(len(samplePNGBytes())) {
		t.Fatalf("expected %d bytes, got %d", len(samplePNGBytes()), byteSize)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected temp file to exist: %v", err)
	}
	removeIssueAssetFile(path)

	if _, _, _, err := writeIssueAssetTempFile(root, bytes.NewReader(nil), "empty.txt"); !IsValidation(err) {
		t.Fatalf("expected empty asset validation error, got %v", err)
	}
}

func TestDeleteIssueRemovesAttachedAssets(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Clean assets", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	asset, err := store.CreateIssueAsset(issue.ID, "clean.png", bytes.NewReader(samplePNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueAsset: %v", err)
	}
	_, path, err := store.GetIssueAssetContent(issue.ID, asset.ID)
	if err != nil {
		t.Fatalf("GetIssueAssetContent: %v", err)
	}

	if err := store.DeleteIssue(issue.ID); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected issue asset to be removed, got %v", err)
	}
}

func TestCreateIssueAgentCommandWithRuntimeEventRollsBackOnEventFailure(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Follow-up", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	_, err = store.CreateIssueAgentCommandWithRuntimeEvent(
		issue.ID,
		"Retry after failure.",
		IssueAgentCommandPending,
		"manual_command_submitted",
		map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      string(issue.WorkflowPhase),
			"bad":        func() {},
		},
	)
	if err == nil {
		t.Fatal("expected event payload error")
	}

	commands, err := store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueAgentCommands: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("expected rollback to remove command, got %+v", commands)
	}
}

func TestIssueAgentCommandLifecycle(t *testing.T) {
	store := setupTestStore(t)

	blocker, err := store.CreateIssue("", "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	issue, err := store.CreateIssue("", "", "Follow-up", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue issue: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState issue: %v", err)
	}
	if _, err := store.SetIssueBlockers(issue.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}

	unresolved, err := store.UnresolvedBlockersForIssue(issue.ID)
	if err != nil {
		t.Fatalf("UnresolvedBlockersForIssue: %v", err)
	}
	if len(unresolved) != 1 || unresolved[0] != blocker.Identifier {
		t.Fatalf("expected unresolved blocker %q, got %#v", blocker.Identifier, unresolved)
	}

	submitted, err := store.CreateIssueAgentCommandWithRuntimeEvent(
		issue.ID,
		"Resume implementation after unblock.",
		IssueAgentCommandPending,
		"manual_command_submitted",
		map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      string(issue.WorkflowPhase),
		},
	)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommandWithRuntimeEvent: %v", err)
	}
	waiting, err := store.CreateIssueAgentCommand(issue.ID, "Run the final check.", IssueAgentCommandWaitingForUnblock)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand waiting: %v", err)
	}
	if err := store.UpdateIssueAgentCommandStatus(submitted.ID, IssueAgentCommandWaitingForUnblock); err != nil {
		t.Fatalf("UpdateIssueAgentCommandStatus: %v", err)
	}

	pending, err := store.ListPendingIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListPendingIssueAgentCommands while blocked: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending commands while blocked, got %#v", pending)
	}

	if err := store.UpdateIssueState(blocker.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState blocker done: %v", err)
	}
	if err := store.ActivateIssueAgentCommandsIfDispatchable(issue.ID); err != nil {
		t.Fatalf("ActivateIssueAgentCommandsIfDispatchable: %v", err)
	}

	pending, err = store.ListPendingIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListPendingIssueAgentCommands after unblock: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected two pending commands after unblock, got %#v", pending)
	}
	if pending[0].ID != submitted.ID || pending[1].ID != waiting.ID {
		t.Fatalf("expected oldest-first pending ordering, got %#v", pending)
	}

	beforeDeliveredChange, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq before delivered change: %v", err)
	}
	if err := store.MarkIssueAgentCommandsDelivered(issue.ID, []string{submitted.ID, waiting.ID}, "same_thread", "thread-live", 2); err != nil {
		t.Fatalf("MarkIssueAgentCommandsDelivered: %v", err)
	}
	afterDeliveredChange, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq after delivered change: %v", err)
	}
	if afterDeliveredChange <= beforeDeliveredChange {
		t.Fatalf("expected delivered change event to advance seq: before=%d after=%d", beforeDeliveredChange, afterDeliveredChange)
	}

	commands, err := store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueAgentCommands: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("expected two commands, got %#v", commands)
	}
	for _, command := range commands {
		if command.Status != IssueAgentCommandDelivered {
			t.Fatalf("expected delivered status, got %+v", command)
		}
		if command.DeliveryMode != "same_thread" || command.DeliveryThreadID != "thread-live" || command.DeliveryAttempt != 2 {
			t.Fatalf("unexpected delivery metadata: %+v", command)
		}
		if command.DeliveredAt == nil || command.DeliveredAt.IsZero() {
			t.Fatalf("expected delivered timestamp, got %+v", command)
		}
	}

	events, err := store.ListRuntimeEvents(0, 20)
	if err != nil {
		t.Fatalf("ListRuntimeEvents: %v", err)
	}
	foundSubmission := false
	for _, event := range events {
		switch event.Kind {
		case "manual_command_submitted":
			if event.Payload["command_id"] != submitted.ID || event.Payload["command"] != submitted.Command {
				t.Fatalf("unexpected submitted payload: %+v", event)
			}
			foundSubmission = true
		}
	}
	if !foundSubmission {
		t.Fatal("expected manual_command_submitted runtime event")
	}
}

func TestIssueAgentCommandUpdateAndDelete(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Mutable command issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	command, err := store.CreateIssueAgentCommand(issue.ID, "Merge the branch to master.", IssueAgentCommandPending)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand: %v", err)
	}

	beforeUpdateChange, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq before update: %v", err)
	}
	updated, err := store.UpdateIssueAgentCommand(issue.ID, command.ID, "Merge the branch after fixing the tests.")
	if err != nil {
		t.Fatalf("UpdateIssueAgentCommand: %v", err)
	}
	if updated.ID != command.ID || updated.Command != "Merge the branch after fixing the tests." {
		t.Fatalf("unexpected updated command: %+v", updated)
	}
	if updated.Status != IssueAgentCommandPending {
		t.Fatalf("expected pending status to remain unchanged, got %+v", updated)
	}

	afterUpdateChange, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq after update: %v", err)
	}
	if afterUpdateChange <= beforeUpdateChange {
		t.Fatalf("expected update change event to advance seq: before=%d after=%d", beforeUpdateChange, afterUpdateChange)
	}

	commands, err := store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueAgentCommands after update: %v", err)
	}
	if len(commands) != 1 || commands[0].Command != "Merge the branch after fixing the tests." {
		t.Fatalf("unexpected command list after update: %#v", commands)
	}

	beforeDeleteChange, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq before delete: %v", err)
	}
	if err := store.DeleteIssueAgentCommand(issue.ID, command.ID); err != nil {
		t.Fatalf("DeleteIssueAgentCommand: %v", err)
	}
	afterDeleteChange, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq after delete: %v", err)
	}
	if afterDeleteChange <= beforeDeleteChange {
		t.Fatalf("expected delete change event to advance seq: before=%d after=%d", beforeDeleteChange, afterDeleteChange)
	}

	commands, err = store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueAgentCommands after delete: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("expected command to be deleted, got %#v", commands)
	}
}

func TestMarkIssueAgentCommandsDeliveredIfUnchangedRejectsEditedCommands(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Edited command delivery issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	command, err := store.CreateIssueAgentCommand(issue.ID, "Ship the approved plan.", IssueAgentCommandPending)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand: %v", err)
	}
	if _, err := store.UpdateIssueAgentCommand(issue.ID, command.ID, "Ship the approved plan after rerunning tests."); err != nil {
		t.Fatalf("UpdateIssueAgentCommand: %v", err)
	}

	err = store.MarkIssueAgentCommandsDeliveredIfUnchanged(issue.ID, []IssueAgentCommand{*command}, "next_run", "thread-edited", 1)
	if err == nil {
		t.Fatal("expected stale delivery guard to reject edited command")
	}
	if !strings.Contains(err.Error(), "changed before delivery") {
		t.Fatalf("expected stale delivery error, got %v", err)
	}

	commands, err := store.ListIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueAgentCommands: %v", err)
	}
	if len(commands) != 1 {
		t.Fatalf("expected one command, got %#v", commands)
	}
	if commands[0].Status != IssueAgentCommandPending {
		t.Fatalf("expected command to remain pending, got %+v", commands[0])
	}
	if commands[0].Command != "Ship the approved plan after rerunning tests." {
		t.Fatalf("expected revised command text to remain stored, got %+v", commands[0])
	}
	if commands[0].DeliveredAt != nil {
		t.Fatalf("expected delivered timestamp to remain unset, got %+v", commands[0])
	}
}

func TestIssueAgentCommandSteerPrioritizesCommandsAfterUnblock(t *testing.T) {
	store := setupTestStore(t)

	blocker, err := store.CreateIssue("", "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	issue, err := store.CreateIssue("", "", "Steerable command issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue issue: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocker: %v", err)
	}
	if err := store.UpdateIssueState(issue.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState issue: %v", err)
	}
	if _, err := store.SetIssueBlockers(issue.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}

	first, err := store.CreateIssueAgentCommand(issue.ID, "Keep this queued follow-up behind the steered one.", IssueAgentCommandWaitingForUnblock)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand first: %v", err)
	}
	second, err := store.CreateIssueAgentCommand(issue.ID, "Bring this follow-up to the front once unblocked.", IssueAgentCommandWaitingForUnblock)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand second: %v", err)
	}

	beforeSteerChange, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq before steer: %v", err)
	}
	steered, err := store.SteerIssueAgentCommand(issue.ID, second.ID)
	if err != nil {
		t.Fatalf("SteerIssueAgentCommand: %v", err)
	}
	if steered.SteeredAt == nil || steered.SteeredAt.IsZero() {
		t.Fatalf("expected steered timestamp, got %+v", steered)
	}

	afterSteerChange, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq after steer: %v", err)
	}
	if afterSteerChange <= beforeSteerChange {
		t.Fatalf("expected steer change event to advance seq: before=%d after=%d", beforeSteerChange, afterSteerChange)
	}

	if err := store.UpdateIssueState(blocker.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState blocker done: %v", err)
	}
	if err := store.ActivateIssueAgentCommandsIfDispatchable(issue.ID); err != nil {
		t.Fatalf("ActivateIssueAgentCommandsIfDispatchable: %v", err)
	}

	pending, err := store.ListPendingIssueAgentCommands(issue.ID)
	if err != nil {
		t.Fatalf("ListPendingIssueAgentCommands: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected two pending commands after unblock, got %#v", pending)
	}
	if pending[0].ID != second.ID || pending[1].ID != first.ID {
		t.Fatalf("expected steered command first after unblock, got %#v", pending)
	}
	if pending[0].SteeredAt == nil || pending[1].SteeredAt != nil {
		t.Fatalf("unexpected steered metadata in pending commands: %#v", pending)
	}
}

func TestIssueAgentCommandMutationsRejectDeliveredCommands(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Delivered command issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	command, err := store.CreateIssueAgentCommand(issue.ID, "Ship the branch.", IssueAgentCommandPending)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand: %v", err)
	}
	if err := store.MarkIssueAgentCommandsDelivered(issue.ID, []string{command.ID}, "same_thread", "thread-live", 1); err != nil {
		t.Fatalf("MarkIssueAgentCommandsDelivered: %v", err)
	}

	if _, err := store.UpdateIssueAgentCommand(issue.ID, command.ID, "Ship the branch and verify release notes."); !IsNotFound(err) {
		t.Fatalf("expected delivered command update to be not found, got %v", err)
	}
	if err := store.DeleteIssueAgentCommand(issue.ID, command.ID); !IsNotFound(err) {
		t.Fatalf("expected delivered command delete to be not found, got %v", err)
	}
	if _, err := store.SteerIssueAgentCommand(issue.ID, command.ID); !IsNotFound(err) {
		t.Fatalf("expected delivered command steer to be not found, got %v", err)
	}
}

func TestStateValidation(t *testing.T) {
	tests := []struct {
		state    State
		expected bool
	}{
		{StateBacklog, true},
		{StateReady, true},
		{StateInProgress, true},
		{StateInReview, true},
		{StateDone, true},
		{StateCancelled, true},
		{State("invalid"), false},
		{State(""), false},
	}

	for _, tt := range tests {
		if got := tt.state.IsValid(); got != tt.expected {
			t.Errorf("State(%q).IsValid() = %v, expected %v", tt.state, got, tt.expected)
		}
	}
}

func TestActiveStates(t *testing.T) {
	states := ActiveStates()
	if len(states) != 3 {
		t.Errorf("Expected 3 active states, got %d", len(states))
	}
}

func TestTerminalStates(t *testing.T) {
	states := TerminalStates()
	if len(states) != 2 {
		t.Errorf("Expected 2 terminal states, got %d", len(states))
	}
}

// Project tests

func TestCreateProject(t *testing.T) {
	store := setupTestStore(t)

	project, err := store.CreateProject("Test Project", "A test project", "", "")
	if err != nil {
		t.Fatalf("Failed to create project: %v", err)
	}

	if project.Name != "Test Project" {
		t.Errorf("Expected name 'Test Project', got %s", project.Name)
	}
	if project.Description != "A test project" {
		t.Errorf("Expected description 'A test project', got %s", project.Description)
	}
	if project.ID == "" {
		t.Error("Expected non-empty ID")
	}
	if project.State != ProjectStateStopped {
		t.Fatalf("expected default project state stopped, got %q", project.State)
	}
}

func TestGetProject(t *testing.T) {
	store := setupTestStore(t)

	created, err := store.CreateProject("Test", "Desc", "", "")
	if err != nil {
		t.Fatalf("Failed to create project: %v", err)
	}

	project, err := store.GetProject(created.ID)
	if err != nil {
		t.Fatalf("Failed to get project: %v", err)
	}

	if project.Name != "Test" {
		t.Errorf("Expected name 'Test', got %s", project.Name)
	}
	if project.State != ProjectStateStopped {
		t.Fatalf("expected persisted project state stopped, got %q", project.State)
	}
}

func TestListProjects(t *testing.T) {
	store := setupTestStore(t)

	_, _ = store.CreateProject("Project A", "", "", "")
	_, _ = store.CreateProject("Project B", "", "", "")

	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("Failed to list projects: %v", err)
	}

	if len(projects) != 2 {
		t.Errorf("Expected 2 projects, got %d", len(projects))
	}
	for _, project := range projects {
		if project.State != ProjectStateStopped {
			t.Fatalf("expected listed project state stopped, got %q", project.State)
		}
	}
}

func TestDeleteProject(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("To Delete", "", "", "")
	epic, err := store.CreateEpic(project.ID, "Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, epic.ID, "Issue in project", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	workspacePath := filepath.Join(t.TempDir(), issue.Identifier)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatalf("Failed to delete project: %v", err)
	}

	_, err = store.GetProject(project.ID)
	if err == nil {
		t.Error("Expected error getting deleted project")
	}
	if _, err := store.GetIssue(issue.ID); err == nil {
		t.Error("Expected project issue to be deleted")
	}
	if _, err := store.GetEpic(epic.ID); err == nil {
		t.Error("Expected project epic to be deleted")
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("expected workspace path to be removed, got err=%v", err)
	}
}

func TestDeleteProjectReturnsNotFound(t *testing.T) {
	store := setupTestStore(t)

	err := store.DeleteProject("missing-project")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected not found error, got %v", err)
	}
}

// Epic tests

func TestCreateEpic(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")

	epic, err := store.CreateEpic(project.ID, "Epic 1", "Epic description")
	if err != nil {
		t.Fatalf("Failed to create epic: %v", err)
	}

	if epic.Name != "Epic 1" {
		t.Errorf("Expected name 'Epic 1', got %s", epic.Name)
	}
	if epic.ProjectID != project.ID {
		t.Error("Epic project ID mismatch")
	}
}

func TestListEpics(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	_, _ = store.CreateEpic(project.ID, "Epic 1", "")
	_, _ = store.CreateEpic(project.ID, "Epic 2", "")

	epics, err := store.ListEpics(project.ID)
	if err != nil {
		t.Fatalf("Failed to list epics: %v", err)
	}

	if len(epics) != 2 {
		t.Errorf("Expected 2 epics, got %d", len(epics))
	}
}

// Issue tests

func TestCreateIssue(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("A B C", "", "", "")
	labels := []string{"bug", "urgent"}

	issue, err := store.CreateIssue(project.ID, "", "Fix login bug", "Description here", 1, labels)
	if err != nil {
		t.Fatalf("Failed to create issue: %v", err)
	}

	if issue.Title != "Fix login bug" {
		t.Errorf("Expected title 'Fix login bug', got %s", issue.Title)
	}
	if issue.State != StateBacklog {
		t.Errorf("Expected initial state 'backlog', got %s", issue.State)
	}
	if issue.Identifier != "ABC-1" {
		t.Fatalf("Expected identifier ABC-1, got %s", issue.Identifier)
	}
	if len(issue.Labels) != 2 {
		t.Errorf("Expected 2 labels, got %d", len(issue.Labels))
	}

	nextIssue, err := store.CreateIssue(project.ID, "", "Fix auth bug", "Second issue", 1, nil)
	if err != nil {
		t.Fatalf("Failed to create second issue: %v", err)
	}
	if nextIssue.Identifier != "ABC-2" {
		t.Fatalf("Expected identifier ABC-2, got %s", nextIssue.Identifier)
	}
}

func TestCreateIssueWithAgentMetadata(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("MyApp", "", "", "")
	issue, err := store.CreateIssueWithOptions(project.ID, "", "Review homepage", "Copy refresh", 1, []string{"marketing"}, IssueCreateOptions{
		AgentName:   "marketing",
		AgentPrompt: "Review the homepage copy for clarity and conversion.",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions: %v", err)
	}
	if issue.AgentName != "marketing" || issue.AgentPrompt != "Review the homepage copy for clarity and conversion." {
		t.Fatalf("expected agent metadata to persist, got %#v", issue)
	}
}

func TestNewStoreBackfillsLegacyIssueTypesAndCreatesRecurrenceTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	for _, stmt := range []string{
		`CREATE TABLE store_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE issues (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			epic_id TEXT,
			identifier TEXT UNIQUE NOT NULL,
			title TEXT NOT NULL,
			description TEXT,
			state TEXT NOT NULL DEFAULT 'backlog',
			workflow_phase TEXT,
			priority INTEGER DEFAULT 0,
			branch_name TEXT,
			pr_number INTEGER,
			pr_url TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			started_at DATETIME,
			completed_at DATETIME
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("Exec legacy schema: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO issues (id, identifier, title, description, state, priority, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"iss-legacy", "ISS-1", "Legacy issue", "migrated", StateBacklog, 2, now, now,
	); err != nil {
		t.Fatalf("insert legacy issue: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.GetIssue("iss-legacy")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.IssueType != IssueTypeStandard {
		t.Fatalf("expected standard issue type after backfill, got %s", issue.IssueType)
	}

	var backfill string
	if err := store.db.QueryRow(`SELECT value FROM store_metadata WHERE key = 'issue_type_backfill_v1'`).Scan(&backfill); err != nil {
		t.Fatalf("query issue_type_backfill_v1: %v", err)
	}
	if backfill != "done" {
		t.Fatalf("expected issue_type_backfill_v1=done, got %q", backfill)
	}

	var prNumberColumnCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('issues') WHERE name = 'pr_number'`).Scan(&prNumberColumnCount); err != nil {
		t.Fatalf("query pragma_table_info: %v", err)
	}
	if prNumberColumnCount != 0 {
		t.Fatalf("expected pr_number column to be removed, got count %d", prNumberColumnCount)
	}

	var recurrenceTableCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'issue_recurrences'`).Scan(&recurrenceTableCount); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if recurrenceTableCount != 1 {
		t.Fatalf("expected issue_recurrences table to exist, got count=%d", recurrenceTableCount)
	}
}

func TestNewStoreNormalizesLegacyIssueAssociationsDuringPRNumberMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-associations.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	for _, stmt := range []string{
		`CREATE TABLE store_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			state TEXT NOT NULL DEFAULT 'stopped',
			repo_path TEXT NOT NULL DEFAULT '',
			workflow_path TEXT NOT NULL DEFAULT '',
			provider_kind TEXT NOT NULL DEFAULT 'kanban',
			provider_project_ref TEXT NOT NULL DEFAULT '',
			provider_config_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE epics (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			name TEXT NOT NULL,
			description TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE issues (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			epic_id TEXT,
			identifier TEXT UNIQUE NOT NULL,
			title TEXT NOT NULL,
			description TEXT,
			state TEXT NOT NULL DEFAULT 'backlog',
			workflow_phase TEXT,
			priority INTEGER DEFAULT 0,
			branch_name TEXT,
			pr_number INTEGER,
			pr_url TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			started_at DATETIME,
			completed_at DATETIME
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("Exec legacy schema: %v", err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"project-1", "Platform", now, now,
	); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO epics (id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"epic-1", "project-1", "Automation", now, now,
	); err != nil {
		t.Fatalf("insert epic: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO issues (id, project_id, epic_id, identifier, title, description, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"iss-orphan-epic", "project-1", "epic-missing", "ISS-1", "Clear bad epic", "", StateBacklog, now, now,
	); err != nil {
		t.Fatalf("insert issue with orphaned epic: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO issues (id, project_id, epic_id, identifier, title, description, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"iss-derived-project", "missing-project", "epic-1", "ISS-2", "Recover project from epic", "", StateBacklog, now, now,
	); err != nil {
		t.Fatalf("insert issue with stale project: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	orphanedEpicIssue, err := store.GetIssue("iss-orphan-epic")
	if err != nil {
		t.Fatalf("GetIssue orphaned epic: %v", err)
	}
	if orphanedEpicIssue.ProjectID != "project-1" {
		t.Fatalf("expected project-1 to be preserved, got %q", orphanedEpicIssue.ProjectID)
	}
	if orphanedEpicIssue.EpicID != "" {
		t.Fatalf("expected orphaned epic reference to be cleared, got %q", orphanedEpicIssue.EpicID)
	}

	derivedProjectIssue, err := store.GetIssue("iss-derived-project")
	if err != nil {
		t.Fatalf("GetIssue derived project: %v", err)
	}
	if derivedProjectIssue.ProjectID != "project-1" {
		t.Fatalf("expected project_id to be recovered from the epic, got %q", derivedProjectIssue.ProjectID)
	}
	if derivedProjectIssue.EpicID != "epic-1" {
		t.Fatalf("expected valid epic_id to be preserved, got %q", derivedProjectIssue.EpicID)
	}

	var normalization string
	if err := store.db.QueryRow(`SELECT value FROM store_metadata WHERE key = 'issue_foreign_key_normalization_v1'`).Scan(&normalization); err != nil {
		t.Fatalf("query issue_foreign_key_normalization_v1: %v", err)
	}
	if normalization != "done" {
		t.Fatalf("expected issue_foreign_key_normalization_v1=done, got %q", normalization)
	}
}

func TestNewStoreRepairsIssueAssociationsAfterInterruptedPRNumberMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "interrupted-pr-number-migration.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	now := time.Date(2026, 3, 13, 12, 30, 0, 0, time.UTC)
	for _, stmt := range []string{
		`CREATE TABLE store_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			state TEXT NOT NULL DEFAULT 'stopped',
			repo_path TEXT NOT NULL DEFAULT '',
			workflow_path TEXT NOT NULL DEFAULT '',
			provider_kind TEXT NOT NULL DEFAULT 'kanban',
			provider_project_ref TEXT NOT NULL DEFAULT '',
			provider_config_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE epics (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			name TEXT NOT NULL,
			description TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		`CREATE TABLE issues (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			epic_id TEXT,
			identifier TEXT UNIQUE NOT NULL,
			issue_type TEXT NOT NULL DEFAULT 'standard',
			provider_kind TEXT NOT NULL DEFAULT 'kanban',
			provider_issue_ref TEXT NOT NULL DEFAULT '',
			provider_shadow INTEGER NOT NULL DEFAULT 0,
			title TEXT NOT NULL,
			description TEXT,
			state TEXT NOT NULL DEFAULT 'backlog',
			workflow_phase TEXT NOT NULL DEFAULT 'implementation',
			priority INTEGER DEFAULT 0,
			branch_name TEXT,
			pr_url TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			total_tokens_spent INTEGER NOT NULL DEFAULT 0,
			started_at DATETIME,
			completed_at DATETIME,
			last_synced_at DATETIME,
			FOREIGN KEY (project_id) REFERENCES projects(id),
			FOREIGN KEY (epic_id) REFERENCES epics(id)
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("Exec interrupted schema: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO store_metadata (key, value) VALUES (?, ?)`, "issue_pr_number_drop_v1", "done"); err != nil {
		t.Fatalf("insert stale migration marker: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"project-1", "Platform", now, now,
	); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO issues (id, project_id, epic_id, identifier, title, description, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"iss-interrupted", "project-1", "epic-missing", "ISS-1", "Interrupted migration", "", StateBacklog, now, now,
	); err != nil {
		t.Fatalf("insert issue with orphaned epic: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close interrupted db: %v", err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	issue, err := store.GetIssue("iss-interrupted")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.ProjectID != "project-1" {
		t.Fatalf("expected project_id to remain intact, got %q", issue.ProjectID)
	}
	if issue.EpicID != "" {
		t.Fatalf("expected stale epic_id to be cleared after recovery, got %q", issue.EpicID)
	}

	var normalization string
	if err := store.db.QueryRow(`SELECT value FROM store_metadata WHERE key = 'issue_foreign_key_normalization_v1'`).Scan(&normalization); err != nil {
		t.Fatalf("query issue_foreign_key_normalization_v1: %v", err)
	}
	if normalization != "done" {
		t.Fatalf("expected issue_foreign_key_normalization_v1=done, got %q", normalization)
	}
}

func TestCreateRecurringIssueWithOptions(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssueWithOptions("", "", "Check GitHub ready-to-work", "Create Maestro issues from labeled GitHub issues.", 1, []string{"automation"}, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "  */30   * * * *  ",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions: %v", err)
	}

	if issue.IssueType != IssueTypeRecurring {
		t.Fatalf("expected recurring type, got %s", issue.IssueType)
	}
	if issue.Cron != "*/30 * * * *" {
		t.Fatalf("expected normalized cron, got %q", issue.Cron)
	}
	if !issue.Enabled {
		t.Fatal("expected recurring issue to default enabled")
	}
	if issue.NextRunAt == nil || !issue.NextRunAt.After(issue.CreatedAt) {
		t.Fatalf("expected future next_run_at, got %+v", issue.NextRunAt)
	}

	recurrence, err := store.GetIssueRecurrence(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueRecurrence: %v", err)
	}
	if recurrence == nil {
		t.Fatal("expected recurrence row")
	}
	if recurrence.Cron != "*/30 * * * *" || !recurrence.Enabled {
		t.Fatalf("unexpected recurrence payload: %+v", recurrence)
	}
}

func TestCreateIssueWithOptionsRejectsInvalidIssueType(t *testing.T) {
	store := setupTestStore(t)

	_, err := store.CreateIssueWithOptions("", "", "Invalid", "", 0, nil, IssueCreateOptions{
		IssueType: IssueType("recurirng"),
		Cron:      "*/15 * * * *",
	})
	if err == nil {
		t.Fatal("expected invalid issue type error")
	}
	if !IsValidation(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestGetIssueByIdentifier(t *testing.T) {
	store := setupTestStore(t)

	created, _ := store.CreateIssue("", "", "Test Issue", "", 0, nil)

	issue, err := store.GetIssueByIdentifier(created.Identifier)
	if err != nil {
		t.Fatalf("Failed to get issue by identifier: %v", err)
	}

	if issue.ID != created.ID {
		t.Error("Issue ID mismatch")
	}
}

func TestGetIssueReturnsRelationQueryErrors(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Strict issue read", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := store.db.Exec(`ALTER TABLE issue_labels RENAME TO issue_labels_missing`); err != nil {
		t.Fatalf("rename issue_labels: %v", err)
	}

	_, err = store.GetIssue(issue.ID)
	if err == nil || !strings.Contains(err.Error(), "issue_labels") {
		t.Fatalf("expected relation query error mentioning issue_labels, got %v", err)
	}
}

func TestGetIssueDetailByIdentifierUsesExactLookup(t *testing.T) {
	store := setupTestStore(t)

	target, err := store.CreateIssue("", "", "Target", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue target: %v", err)
	}
	for i := 0; i < 50; i++ {
		if _, err := store.CreateIssue("", "", fmt.Sprintf("Distractor %02d", i), "references "+target.Identifier, 0, nil); err != nil {
			t.Fatalf("CreateIssue distractor %d: %v", i, err)
		}
	}

	detail, err := store.GetIssueDetailByIdentifier(target.Identifier)
	if err != nil {
		t.Fatalf("GetIssueDetailByIdentifier: %v", err)
	}
	if detail.ID != target.ID || detail.Identifier != target.Identifier {
		t.Fatalf("expected exact issue detail for %s, got %#v", target.Identifier, detail)
	}
}

func TestGetIssueDetailByIdentifierIncludesRelatedMetadata(t *testing.T) {
	store := setupTestStore(t)

	project, err := store.CreateProject("Project", "Project description", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Epic", "Epic description")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	blocker, err := store.CreateIssue(project.ID, epic.ID, "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, epic.ID, "Target", "", 0, []string{"ops"})
	if err != nil {
		t.Fatalf("CreateIssue target: %v", err)
	}
	if _, err := store.SetIssueBlockers(issue.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}
	if _, err := store.CreateWorkspace(issue.ID, filepath.Join(t.TempDir(), "workspace")); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	asset, err := store.CreateIssueAsset(issue.ID, "detail.png", bytes.NewReader(samplePNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueAsset: %v", err)
	}

	detail, err := store.GetIssueDetailByIdentifier(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssueDetailByIdentifier: %v", err)
	}
	if detail.ProjectName != project.Name || detail.ProjectDescription != project.Description {
		t.Fatalf("unexpected project metadata: %#v", detail)
	}
	if detail.EpicName != epic.Name || detail.EpicDescription != epic.Description {
		t.Fatalf("unexpected epic metadata: %#v", detail)
	}
	if detail.WorkspacePath == "" || detail.WorkspaceRunCount != 0 {
		t.Fatalf("unexpected workspace metadata: %#v", detail)
	}
	if !detail.IsBlocked {
		t.Fatalf("expected issue detail to report unresolved blockers: %#v", detail)
	}
	if len(detail.Assets) != 1 || detail.Assets[0].ID != asset.ID {
		t.Fatalf("unexpected issue assets: %#v", detail.Assets)
	}
}

func TestGetIssueDetailByIdentifierSkipsMissingRelatedRecords(t *testing.T) {
	store := setupTestStore(t)

	project, err := store.CreateProject("Project", "Project description", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Epic", "Epic description")
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, epic.ID, "Target", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	ctx := context.Background()
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer func() {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
			t.Fatalf("restore foreign keys: %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Fatalf("close conn: %v", err)
		}
	}()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE issues SET project_id = ?, epic_id = ? WHERE id = ?`, "missing-project", "missing-epic", issue.ID); err != nil {
		t.Fatalf("UPDATE issues: %v", err)
	}

	detail, err := store.GetIssueDetailByIdentifier(issue.Identifier)
	if err != nil {
		t.Fatalf("GetIssueDetailByIdentifier: %v", err)
	}
	if detail.ProjectName != "" || detail.ProjectDescription != "" {
		t.Fatalf("expected missing project metadata to be ignored, got %#v", detail)
	}
	if detail.EpicName != "" || detail.EpicDescription != "" {
		t.Fatalf("expected missing epic metadata to be ignored, got %#v", detail)
	}
}

func TestLoadIssuesByIDsDeduplicatesAndHydratesRelations(t *testing.T) {
	store := setupTestStore(t)

	blocker, err := store.CreateIssue("", "", "Blocker", "", 0, []string{"blocker"})
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	recurring, err := store.CreateIssueWithOptions("", "", "Recurring", "", 0, []string{"docs"}, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/15 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions recurring: %v", err)
	}
	if _, err := store.SetIssueBlockers(recurring.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}

	issues, err := store.loadIssuesByIDs([]string{"", recurring.ID, recurring.ID, "   ", blocker.ID})
	if err != nil {
		t.Fatalf("loadIssuesByIDs: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected deduplicated issue list, got %#v", issues)
	}
	if issues[0].ID != recurring.ID || issues[1].ID != blocker.ID {
		t.Fatalf("expected stable issue ordering, got %#v", issues)
	}
	if !issues[0].Enabled || issues[0].Cron != "*/15 * * * *" {
		t.Fatalf("expected recurrence metadata on first issue, got %#v", issues[0])
	}
	if len(issues[0].Labels) != 1 || issues[0].Labels[0] != "docs" {
		t.Fatalf("expected recurring issue labels, got %#v", issues[0].Labels)
	}
	if len(issues[0].BlockedBy) != 1 || issues[0].BlockedBy[0] != blocker.Identifier {
		t.Fatalf("expected recurring issue blockers, got %#v", issues[0].BlockedBy)
	}
	if len(issues[1].Labels) != 1 || issues[1].Labels[0] != "blocker" {
		t.Fatalf("expected blocker labels, got %#v", issues[1].Labels)
	}
}

func TestLoadIssuesByIDsReturnsNotFoundForMissingIssue(t *testing.T) {
	store := setupTestStore(t)

	if _, err := store.loadIssuesByIDs([]string{"missing-id"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestLoadIssuesByIDsReturnsEmptyForBlankInputs(t *testing.T) {
	store := setupTestStore(t)

	issues, err := store.loadIssuesByIDs([]string{"", "   "})
	if err != nil {
		t.Fatalf("loadIssuesByIDs: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues for blank inputs, got %#v", issues)
	}
}

func TestAddIssueTokenSpendIncrementsWithoutTouchingUpdatedAt(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Token spend", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	before, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue before increment: %v", err)
	}

	if err := store.AddIssueTokenSpend(issue.ID, 12); err != nil {
		t.Fatalf("AddIssueTokenSpend first increment: %v", err)
	}
	if err := store.AddIssueTokenSpend(issue.ID, 5); err != nil {
		t.Fatalf("AddIssueTokenSpend second increment: %v", err)
	}

	after, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after increment: %v", err)
	}
	if after.TotalTokensSpent != 17 {
		t.Fatalf("TotalTokensSpent = %d, want 17", after.TotalTokensSpent)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("UpdatedAt changed from %s to %s", before.UpdatedAt, after.UpdatedAt)
	}
}

func TestRecomputeIssueTokenSpendUsesFinalizedRuntimeTotals(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Token repair", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.AddIssueTokenSpend(issue.ID, 999); err != nil {
		t.Fatalf("AddIssueTokenSpend: %v", err)
	}
	events := []struct {
		kind   string
		fields map[string]interface{}
	}{
		{
			kind: "run_completed",
			fields: map[string]interface{}{
				"issue_id":     issue.ID,
				"identifier":   issue.Identifier,
				"thread_id":    "thread-a",
				"total_tokens": 12,
			},
		},
		{
			kind: "retry_paused",
			fields: map[string]interface{}{
				"issue_id":     issue.ID,
				"identifier":   issue.Identifier,
				"thread_id":    "thread-a",
				"error":        "plan_approval_pending",
				"total_tokens": 20,
			},
		},
		{
			kind: "run_failed",
			fields: map[string]interface{}{
				"issue_id":     issue.ID,
				"identifier":   issue.Identifier,
				"thread_id":    "thread-b",
				"total_tokens": 5,
			},
		},
		{
			kind: "run_unsuccessful",
			fields: map[string]interface{}{
				"issue_id":     issue.ID,
				"identifier":   issue.Identifier,
				"total_tokens": 7,
			},
		},
		{
			kind: "retry_paused",
			fields: map[string]interface{}{
				"issue_id":     issue.ID,
				"identifier":   issue.Identifier,
				"error":        "retry_limit_reached",
				"total_tokens": 100,
			},
		},
		{
			kind: "run_started",
			fields: map[string]interface{}{
				"issue_id":     issue.ID,
				"identifier":   issue.Identifier,
				"thread_id":    "thread-c",
				"total_tokens": 200,
			},
		},
	}
	for _, event := range events {
		if err := store.AppendRuntimeEvent(event.kind, event.fields); err != nil {
			t.Fatalf("AppendRuntimeEvent %s: %v", event.kind, err)
		}
	}

	total, err := store.RecomputeIssueTokenSpend(issue.ID)
	if err != nil {
		t.Fatalf("RecomputeIssueTokenSpend: %v", err)
	}
	if total != 32 {
		t.Fatalf("RecomputeIssueTokenSpend = %d, want 32", total)
	}

	reloaded, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if reloaded.TotalTokensSpent != 32 {
		t.Fatalf("TotalTokensSpent = %d, want 32", reloaded.TotalTokensSpent)
	}

	if _, err := store.RecomputeIssueTokenSpend("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows for missing issue, got %v", err)
	}
}

func TestRecomputeProjectAndAllIssueTokenSpend(t *testing.T) {
	store := setupTestStore(t)

	project, err := store.CreateProject("Platform", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	inProjectA, err := store.CreateIssue(project.ID, "", "Project issue A", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue inProjectA: %v", err)
	}
	inProjectB, err := store.CreateIssue(project.ID, "", "Project issue B", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue inProjectB: %v", err)
	}
	outsideProject, err := store.CreateIssue("", "", "Standalone issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue outsideProject: %v", err)
	}
	for _, event := range []struct {
		issueID     string
		identifier  string
		threadID    string
		totalTokens int
	}{
		{inProjectA.ID, inProjectA.Identifier, "thread-a", 9},
		{inProjectB.ID, inProjectB.Identifier, "thread-b", 4},
		{outsideProject.ID, outsideProject.Identifier, "thread-c", 11},
	} {
		if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
			"issue_id":     event.issueID,
			"identifier":   event.identifier,
			"thread_id":    event.threadID,
			"total_tokens": event.totalTokens,
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent run_completed for %s: %v", event.identifier, err)
		}
	}

	recomputed, err := store.RecomputeProjectIssueTokenSpend(project.ID)
	if err != nil {
		t.Fatalf("RecomputeProjectIssueTokenSpend: %v", err)
	}
	if recomputed != 2 {
		t.Fatalf("RecomputeProjectIssueTokenSpend count = %d, want 2", recomputed)
	}

	projectIssue, err := store.GetIssue(inProjectA.ID)
	if err != nil {
		t.Fatalf("GetIssue inProjectA: %v", err)
	}
	if projectIssue.TotalTokensSpent != 9 {
		t.Fatalf("project issue token total = %d, want 9", projectIssue.TotalTokensSpent)
	}
	standaloneBefore, err := store.GetIssue(outsideProject.ID)
	if err != nil {
		t.Fatalf("GetIssue outsideProject before global repair: %v", err)
	}
	if standaloneBefore.TotalTokensSpent != 0 {
		t.Fatalf("standalone issue token total before global repair = %d, want 0", standaloneBefore.TotalTokensSpent)
	}

	recomputed, err = store.RecomputeAllIssueTokenSpend()
	if err != nil {
		t.Fatalf("RecomputeAllIssueTokenSpend: %v", err)
	}
	if recomputed != 3 {
		t.Fatalf("RecomputeAllIssueTokenSpend count = %d, want 3", recomputed)
	}

	standaloneAfter, err := store.GetIssue(outsideProject.ID)
	if err != nil {
		t.Fatalf("GetIssue outsideProject after global repair: %v", err)
	}
	if standaloneAfter.TotalTokensSpent != 11 {
		t.Fatalf("standalone issue token total after global repair = %d, want 11", standaloneAfter.TotalTokensSpent)
	}
}

func TestRuntimeEventThreadIDHandlesInvalidPayloads(t *testing.T) {
	if got := runtimeEventThreadID(""); got != "" {
		t.Fatalf("runtimeEventThreadID empty payload = %q, want empty", got)
	}
	if got := runtimeEventThreadID("{not-json"); got != "" {
		t.Fatalf("runtimeEventThreadID invalid payload = %q, want empty", got)
	}
	if got := runtimeEventThreadID(`{"thread_id":"  thread-123  "}`); got != "thread-123" {
		t.Fatalf("runtimeEventThreadID valid payload = %q, want thread-123", got)
	}
}

func TestListIssues(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	_, _ = store.CreateIssue(project.ID, "", "Issue 1", "", 0, nil)
	_, _ = store.CreateIssue(project.ID, "", "Issue 2", "", 0, nil)
	_, _ = store.CreateIssue("", "", "Issue 3 (no project)", "", 0, nil)

	// List all
	issues, err := store.ListIssues(nil)
	if err != nil {
		t.Fatalf("Failed to list issues: %v", err)
	}
	if len(issues) != 3 {
		t.Errorf("Expected 3 issues, got %d", len(issues))
	}

	// Filter by project
	projectIssues, err := store.ListIssues(map[string]interface{}{"project_id": project.ID})
	if err != nil {
		t.Fatalf("Failed to list project issues: %v", err)
	}
	if len(projectIssues) != 2 {
		t.Errorf("Expected 2 project issues, got %d", len(projectIssues))
	}
}

func TestCountIssuesByState(t *testing.T) {
	store := setupTestStore(t)

	projectA, err := store.CreateProject("Project A", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject A: %v", err)
	}
	projectB, err := store.CreateProject("Project B", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject B: %v", err)
	}

	backlog, err := store.CreateIssue(projectA.ID, "", "Backlog", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue backlog: %v", err)
	}
	ready, err := store.CreateIssue(projectA.ID, "", "Ready", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue ready: %v", err)
	}
	done, err := store.CreateIssue(projectB.ID, "", "Done", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}

	if err := store.UpdateIssueState(ready.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState ready: %v", err)
	}
	if err := store.UpdateIssueState(done.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState done: %v", err)
	}

	counts, err := store.CountIssuesByState("")
	if err != nil {
		t.Fatalf("CountIssuesByState all: %v", err)
	}
	if counts[StateBacklog] != 1 || counts[StateReady] != 1 || counts[StateDone] != 1 {
		t.Fatalf("unexpected global counts: %#v", counts)
	}

	projectCounts, err := store.CountIssuesByState(projectA.ID)
	if err != nil {
		t.Fatalf("CountIssuesByState project: %v", err)
	}
	if projectCounts[StateBacklog] != 1 || projectCounts[StateReady] != 1 || projectCounts[StateDone] != 0 {
		t.Fatalf("unexpected project counts: %#v", projectCounts)
	}

	backlogIssue, err := store.GetIssue(backlog.ID)
	if err != nil {
		t.Fatalf("GetIssue backlog: %v", err)
	}
	if backlogIssue.State != StateBacklog {
		t.Fatalf("expected backlog issue to remain backlog, got %s", backlogIssue.State)
	}
}

func TestListIssuesAndSummariesSupportIssueTypeFilter(t *testing.T) {
	store := setupTestStore(t)

	recurring, err := store.CreateIssueWithOptions("", "", "Recurring", "", 0, nil, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "0 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions recurring: %v", err)
	}
	standard, err := store.CreateIssue("", "", "Standard", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue standard: %v", err)
	}

	issues, err := store.ListIssues(map[string]interface{}{"issue_type": "recurring"})
	if err != nil {
		t.Fatalf("ListIssues recurring: %v", err)
	}
	if len(issues) != 1 || issues[0].Identifier != recurring.Identifier {
		t.Fatalf("expected only recurring issue, got %#v", issues)
	}

	summaries, total, err := store.ListIssueSummaries(IssueQuery{IssueType: "recurring", Limit: 20})
	if err != nil {
		t.Fatalf("ListIssueSummaries recurring: %v", err)
	}
	if total != 1 || len(summaries) != 1 || summaries[0].Identifier != recurring.Identifier {
		t.Fatalf("expected recurring summary only, got total=%d items=%#v", total, summaries)
	}
	if !issueSummaryListContainsIdentifier(summaries, recurring.Identifier) || issueSummaryListContainsIdentifier(summaries, standard.Identifier) {
		t.Fatalf("unexpected recurring summary filter result: %#v", summaries)
	}
}

func TestUpdateIssueState(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)

	// Move to ready
	if err := store.UpdateIssueState(issue.ID, StateReady); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}

	updated, _ := store.GetIssue(issue.ID)
	if updated.State != StateReady {
		t.Errorf("Expected state 'ready', got %s", updated.State)
	}
	if updated.WorkflowPhase != WorkflowPhaseImplementation {
		t.Fatalf("expected implementation phase for ready issue, got %s", updated.WorkflowPhase)
	}

	// Move to in_progress - should set started_at
	if err := store.UpdateIssueState(issue.ID, StateInProgress); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}

	updated, _ = store.GetIssue(issue.ID)
	if updated.StartedAt == nil {
		t.Error("Expected started_at to be set")
	}
	if updated.WorkflowPhase != WorkflowPhaseImplementation {
		t.Fatalf("expected implementation phase for in_progress issue, got %s", updated.WorkflowPhase)
	}

	if err := store.UpdateIssueState(issue.ID, StateInReview); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}
	updated, _ = store.GetIssue(issue.ID)
	if updated.WorkflowPhase != WorkflowPhaseImplementation {
		t.Fatalf("expected manual in_review move to stay in implementation phase, got %s", updated.WorkflowPhase)
	}

	// Move to done - should set completed_at
	if err := store.UpdateIssueState(issue.ID, StateDone); err != nil {
		t.Fatalf("Failed to update state: %v", err)
	}

	updated, _ = store.GetIssue(issue.ID)
	if updated.CompletedAt == nil {
		t.Error("Expected completed_at to be set")
	}
	if updated.WorkflowPhase != WorkflowPhaseComplete {
		t.Fatalf("expected complete phase for done issue, got %s", updated.WorkflowPhase)
	}
}

func TestUpdateIssueStateRejectsInvalidState(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	err := store.UpdateIssueState(issue.ID, State("invalid"))
	if err == nil {
		t.Fatal("expected invalid state error")
	}
	if !IsValidation(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestUpdateIssueStateRejectsBlockedInProgress(t *testing.T) {
	store := setupTestStore(t)

	blockerB, _ := store.CreateIssue("", "", "Blocker B", "", 0, nil)
	blockerA, _ := store.CreateIssue("", "", "Blocker A", "", 0, nil)
	blocked, _ := store.CreateIssue("", "", "Blocked", "", 0, nil)

	if err := store.UpdateIssueState(blockerA.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState blockerA: %v", err)
	}
	if err := store.UpdateIssueState(blockerB.ID, StateInReview); err != nil {
		t.Fatalf("UpdateIssueState blockerB: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{blockerA.Identifier, blockerB.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}

	err := store.UpdateIssueState(blocked.ID, StateInProgress)
	if err == nil {
		t.Fatal("expected blocked transition error")
	}
	if !IsBlockedTransition(err) {
		t.Fatalf("expected blocked transition error, got %v", err)
	}
	if !IsValidation(err) {
		t.Fatalf("expected validation classification, got %v", err)
	}
	blockers := []string{blockerA.Identifier, blockerB.Identifier}
	sort.Strings(blockers)
	want := "cannot move issue to in_progress: blocked by " + strings.Join(blockers, ", ") + ". Move those blockers to done or cancelled, or remove them from blocked_by first"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected blocker message %q, got %q", want, err.Error())
	}

	reloaded, err := store.GetIssue(blocked.ID)
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}
	if reloaded.State != StateBacklog {
		t.Fatalf("expected blocked issue to stay in backlog, got %s", reloaded.State)
	}
	if reloaded.StartedAt != nil {
		t.Fatal("expected blocked issue to remain unstarted")
	}

	if err := store.UpdateIssueState(blocked.ID, StateReady); err != nil {
		t.Fatalf("expected non-in_progress transition to succeed, got %v", err)
	}
}

func TestUpdateIssueStateAllowsTerminalBlockers(t *testing.T) {
	store := setupTestStore(t)

	doneBlocker, _ := store.CreateIssue("", "", "Done blocker", "", 0, nil)
	cancelledBlocker, _ := store.CreateIssue("", "", "Cancelled blocker", "", 0, nil)
	blocked, _ := store.CreateIssue("", "", "Blocked", "", 0, nil)

	if err := store.UpdateIssueState(doneBlocker.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState doneBlocker: %v", err)
	}
	if err := store.UpdateIssueState(cancelledBlocker.ID, StateCancelled); err != nil {
		t.Fatalf("UpdateIssueState cancelledBlocker: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{doneBlocker.Identifier, cancelledBlocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}

	if err := store.UpdateIssueState(blocked.ID, StateInProgress); err != nil {
		t.Fatalf("expected terminal blockers to allow in_progress, got %v", err)
	}
}

func TestUpdateIssue(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Original Title", "", 0, nil)

	updates := map[string]interface{}{
		"title":        "Updated Title",
		"description":  "New description",
		"priority":     5,
		"labels":       []string{"new-label"},
		"agent_name":   "marketing",
		"agent_prompt": "Tighten the messaging before implementation.",
	}

	if err := store.UpdateIssue(issue.ID, updates); err != nil {
		t.Fatalf("Failed to update issue: %v", err)
	}

	updated, _ := store.GetIssue(issue.ID)
	if updated.Title != "Updated Title" {
		t.Errorf("Expected title 'Updated Title', got %s", updated.Title)
	}
	if updated.Priority != 5 {
		t.Errorf("Expected priority 5, got %d", updated.Priority)
	}
	if len(updated.Labels) != 1 || updated.Labels[0] != "new-label" {
		t.Errorf("Expected labels ['new-label'], got %v", updated.Labels)
	}
	if updated.AgentName != "marketing" || updated.AgentPrompt != "Tighten the messaging before implementation." {
		t.Fatalf("expected agent metadata to persist, got %#v", updated)
	}
}

func TestUpdateIssueRecurringFields(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Original Title", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"issue_type": IssueTypeRecurring,
		"cron":       "*/15 * * * *",
		"enabled":    false,
	}); err != nil {
		t.Fatalf("UpdateIssue recurring: %v", err)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue recurring: %v", err)
	}
	if updated.IssueType != IssueTypeRecurring {
		t.Fatalf("expected recurring issue type, got %s", updated.IssueType)
	}
	if updated.Cron != "*/15 * * * *" {
		t.Fatalf("expected cron to persist, got %q", updated.Cron)
	}
	if updated.Enabled {
		t.Fatal("expected recurring issue to be disabled")
	}

	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"issue_type": IssueTypeStandard}); err != nil {
		t.Fatalf("UpdateIssue standard: %v", err)
	}

	reverted, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue reverted: %v", err)
	}
	if reverted.IssueType != IssueTypeStandard {
		t.Fatalf("expected standard issue type, got %s", reverted.IssueType)
	}
	if reverted.Cron != "" || reverted.NextRunAt != nil || reverted.LastEnqueuedAt != nil || reverted.PendingRerun {
		t.Fatalf("expected recurrence fields cleared, got %+v", reverted)
	}
	recurrence, err := store.GetIssueRecurrence(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueRecurrence reverted: %v", err)
	}
	if recurrence != nil {
		t.Fatalf("expected recurrence row to be removed, got %+v", recurrence)
	}
}

func TestUpdateIssueRecurringConversionDefaultsEnabled(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssue("", "", "Recurring conversion", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"issue_type": "recurring",
		"cron":       "*/15 * * * *",
	}); err != nil {
		t.Fatalf("UpdateIssue recurring conversion: %v", err)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.IssueType != IssueTypeRecurring {
		t.Fatalf("expected recurring type, got %s", updated.IssueType)
	}
	if !updated.Enabled {
		t.Fatal("expected converted recurring issue to default enabled")
	}
	if updated.NextRunAt == nil {
		t.Fatal("expected converted recurring issue to compute next_run_at")
	}
}

func TestUpdateIssueRejectsInvalidIssueTypeWithoutDroppingRecurrence(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssueWithOptions("", "", "Recurring", "", 0, nil, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/15 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions: %v", err)
	}

	err = store.UpdateIssue(issue.ID, map[string]interface{}{"issue_type": "recurirng"})
	if err == nil {
		t.Fatal("expected invalid issue type error")
	}
	if !IsValidation(err) {
		t.Fatalf("expected validation error, got %v", err)
	}

	updated, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.IssueType != IssueTypeRecurring || updated.Cron != "*/15 * * * *" {
		t.Fatalf("expected recurring issue to remain unchanged, got %+v", updated)
	}
}

func TestUpdateIssueStateCancelledDisablesRecurring(t *testing.T) {
	store := setupTestStore(t)

	issue, err := store.CreateIssueWithOptions("", "", "Recurring", "", 0, nil, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "0 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions: %v", err)
	}

	if err := store.UpdateIssueState(issue.ID, StateCancelled); err != nil {
		t.Fatalf("UpdateIssueState: %v", err)
	}

	recurrence, err := store.GetIssueRecurrence(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueRecurrence: %v", err)
	}
	if recurrence == nil {
		t.Fatal("expected recurrence row to remain")
	}
	if recurrence.Enabled {
		t.Fatalf("expected cancelled recurring issue to be disabled, got %+v", recurrence)
	}
}

func TestRecurringStoreQueriesAndRearmLifecycle(t *testing.T) {
	store := setupTestStore(t)

	dueIssue, err := store.CreateIssueWithOptions("", "", "Due recurring", "", 0, nil, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/10 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions due: %v", err)
	}
	pendingIssue, err := store.CreateIssueWithOptions("", "", "Pending recurring", "", 0, nil, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/20 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions pending: %v", err)
	}
	secondPendingIssue, err := store.CreateIssueWithOptions("", "", "Pending recurring 2", "", 0, nil, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/25 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions second pending: %v", err)
	}
	cancelledIssue, err := store.CreateIssueWithOptions("", "", "Cancelled recurring", "", 0, nil, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/30 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions cancelled: %v", err)
	}
	secondDueIssue, err := store.CreateIssueWithOptions("", "", "Due recurring 2", "", 0, nil, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/5 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions second due: %v", err)
	}
	standardIssue, err := store.CreateIssue("", "", "Standard", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue standard: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	past := now.Add(-2 * time.Minute)
	olderPast := now.Add(-4 * time.Minute)
	future := now.Add(45 * time.Minute)
	earlierFuture := now.Add(15 * time.Minute)
	if _, err := store.db.Exec(`UPDATE issue_recurrences SET next_run_at = ? WHERE issue_id = ?`, past, dueIssue.ID); err != nil {
		t.Fatalf("update due recurrence: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE issue_recurrences SET next_run_at = ? WHERE issue_id = ?`, olderPast, secondDueIssue.ID); err != nil {
		t.Fatalf("update second due recurrence: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE issue_recurrences SET next_run_at = ?, pending_rerun = 1 WHERE issue_id = ?`, future, pendingIssue.ID); err != nil {
		t.Fatalf("update pending recurrence: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE issue_recurrences SET next_run_at = ?, pending_rerun = 1 WHERE issue_id = ?`, earlierFuture, secondPendingIssue.ID); err != nil {
		t.Fatalf("update second pending recurrence: %v", err)
	}
	if err := store.UpdateIssueState(cancelledIssue.ID, StateCancelled); err != nil {
		t.Fatalf("UpdateIssueState cancelled recurring: %v", err)
	}

	due, err := store.ListDueRecurringIssues(now, "", 20)
	if err != nil {
		t.Fatalf("ListDueRecurringIssues: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("expected two due recurring issues, got %#v", due)
	}
	if due[0].Identifier != secondDueIssue.Identifier || due[1].Identifier != dueIssue.Identifier {
		t.Fatalf("expected due issues ordered by next_run_at, got %#v", due)
	}
	if issueListContainsIdentifier(due, standardIssue.Identifier) {
		t.Fatalf("did not expect standard issue in due recurring list: %#v", due)
	}

	pending, err := store.ListPendingRecurringIssues("", 20)
	if err != nil {
		t.Fatalf("ListPendingRecurringIssues: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected two pending recurring issues, got %#v", pending)
	}
	if pending[0].Identifier != pendingIssue.Identifier || pending[1].Identifier != secondPendingIssue.Identifier {
		t.Fatalf("expected pending issues ordered by updated_at, got %#v", pending)
	}

	nextDue, err := store.NextRecurringDueAt("")
	if err != nil {
		t.Fatalf("NextRecurringDueAt: %v", err)
	}
	if nextDue == nil || !nextDue.UTC().Equal(olderPast) {
		t.Fatalf("expected next recurring due at %s, got %+v", olderPast.Format(time.RFC3339), nextDue)
	}

	if err := store.UpdateIssueState(dueIssue.ID, StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState in_progress: %v", err)
	}
	if err := store.UpdateIssueState(dueIssue.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState done: %v", err)
	}
	if err := store.MarkRecurringPendingRerun(dueIssue.ID, true); err != nil {
		t.Fatalf("MarkRecurringPendingRerun: %v", err)
	}
	enqueuedAt := now.Add(5 * time.Minute)
	nextRunAt := now.Add(35 * time.Minute)
	if err := store.RearmRecurringIssue(dueIssue.ID, enqueuedAt, &nextRunAt); err != nil {
		t.Fatalf("RearmRecurringIssue: %v", err)
	}

	rearmed, err := store.GetIssue(dueIssue.ID)
	if err != nil {
		t.Fatalf("GetIssue rearmed: %v", err)
	}
	if rearmed.State != StateReady || rearmed.WorkflowPhase != WorkflowPhaseImplementation {
		t.Fatalf("expected ready/implementation after rearm, got state=%s phase=%s", rearmed.State, rearmed.WorkflowPhase)
	}
	if rearmed.StartedAt != nil || rearmed.CompletedAt != nil {
		t.Fatalf("expected lifecycle timestamps cleared after rearm, got %+v", rearmed)
	}
	if rearmed.LastEnqueuedAt == nil || !rearmed.LastEnqueuedAt.UTC().Equal(enqueuedAt.UTC()) {
		t.Fatalf("expected last_enqueued_at %s, got %+v", enqueuedAt.Format(time.RFC3339), rearmed.LastEnqueuedAt)
	}
	if rearmed.NextRunAt == nil || !rearmed.NextRunAt.UTC().Equal(nextRunAt.UTC()) {
		t.Fatalf("expected next_run_at %s, got %+v", nextRunAt.Format(time.RFC3339), rearmed.NextRunAt)
	}
	if rearmed.PendingRerun {
		t.Fatalf("expected pending rerun to be cleared after rearm, got %+v", rearmed)
	}
}

func TestRecurringQueriesRespectRepoFilterAndDefaultLimit(t *testing.T) {
	store := setupTestStore(t)

	repoA, err := store.CreateProject("Repo A", "", "/repo/a", "")
	if err != nil {
		t.Fatalf("CreateProject repoA: %v", err)
	}
	repoB, err := store.CreateProject("Repo B", "", "/repo/b", "")
	if err != nil {
		t.Fatalf("CreateProject repoB: %v", err)
	}
	blocker, err := store.CreateIssue(repoA.ID, "", "Blocker", "", 0, []string{"blocker"})
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	dueA, err := store.CreateIssueWithOptions(repoA.ID, "", "Due A", "", 0, []string{"ops"}, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/10 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions dueA: %v", err)
	}
	dueB, err := store.CreateIssueWithOptions(repoB.ID, "", "Due B", "", 0, []string{"infra"}, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/12 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions dueB: %v", err)
	}
	pendingA, err := store.CreateIssueWithOptions(repoA.ID, "", "Pending A", "", 0, []string{"docs"}, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/15 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions pendingA: %v", err)
	}
	pendingB, err := store.CreateIssueWithOptions(repoB.ID, "", "Pending B", "", 0, []string{"ops"}, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/20 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions pendingB: %v", err)
	}
	if _, err := store.SetIssueBlockers(dueA.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers dueA: %v", err)
	}
	if _, err := store.SetIssueBlockers(pendingA.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers pendingA: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.Exec(`UPDATE issue_recurrences SET next_run_at = ? WHERE issue_id = ?`, now.Add(-1*time.Minute), dueA.ID); err != nil {
		t.Fatalf("update dueA recurrence: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE issue_recurrences SET next_run_at = ? WHERE issue_id = ?`, now.Add(-2*time.Minute), dueB.ID); err != nil {
		t.Fatalf("update dueB recurrence: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE issue_recurrences SET next_run_at = ?, pending_rerun = 1 WHERE issue_id = ?`, now.Add(30*time.Minute), pendingA.ID); err != nil {
		t.Fatalf("update pendingA recurrence: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE issue_recurrences SET next_run_at = ?, pending_rerun = 1 WHERE issue_id = ?`, now.Add(45*time.Minute), pendingB.ID); err != nil {
		t.Fatalf("update pendingB recurrence: %v", err)
	}

	due, err := store.ListDueRecurringIssues(now, "  /repo/a  ", 0)
	if err != nil {
		t.Fatalf("ListDueRecurringIssues: %v", err)
	}
	if len(due) != 1 || due[0].ID != dueA.ID {
		t.Fatalf("expected only repoA due issue, got %#v", due)
	}
	if due[0].Cron != "*/10 * * * *" || !due[0].Enabled {
		t.Fatalf("expected hydrated recurrence metadata, got %#v", due[0])
	}
	if len(due[0].Labels) != 1 || due[0].Labels[0] != "ops" {
		t.Fatalf("expected hydrated labels, got %#v", due[0].Labels)
	}
	if len(due[0].BlockedBy) != 1 || due[0].BlockedBy[0] != blocker.Identifier {
		t.Fatalf("expected hydrated blockers, got %#v", due[0].BlockedBy)
	}

	pending, err := store.ListPendingRecurringIssues(" /repo/a ", 0)
	if err != nil {
		t.Fatalf("ListPendingRecurringIssues: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != pendingA.ID {
		t.Fatalf("expected only repoA pending issue, got %#v", pending)
	}
	if pending[0].Cron != "*/15 * * * *" || !pending[0].PendingRerun {
		t.Fatalf("expected hydrated pending recurrence metadata, got %#v", pending[0])
	}
	if len(pending[0].Labels) != 1 || pending[0].Labels[0] != "docs" {
		t.Fatalf("expected hydrated labels, got %#v", pending[0].Labels)
	}
	if len(pending[0].BlockedBy) != 1 || pending[0].BlockedBy[0] != blocker.Identifier {
		t.Fatalf("expected hydrated blockers, got %#v", pending[0].BlockedBy)
	}
}

func TestIssueRecurrenceMapHandlesEmptyAndBlankInputs(t *testing.T) {
	store := setupTestStore(t)

	recurrenceMap, err := store.issueRecurrenceMap(nil)
	if err != nil {
		t.Fatalf("issueRecurrenceMap nil: %v", err)
	}
	if len(recurrenceMap) != 0 {
		t.Fatalf("expected no recurrences for nil input, got %#v", recurrenceMap)
	}

	issue, err := store.CreateIssueWithOptions("", "", "Recurring", "", 0, nil, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/15 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := store.db.Exec(`UPDATE issue_recurrences SET last_enqueued_at = ?, pending_rerun = 1 WHERE issue_id = ?`, now, issue.ID); err != nil {
		t.Fatalf("update recurrence: %v", err)
	}

	recurrenceMap, err = store.issueRecurrenceMap([]string{"", "   ", issue.ID})
	if err != nil {
		t.Fatalf("issueRecurrenceMap populated: %v", err)
	}
	recurrence, ok := recurrenceMap[issue.ID]
	if !ok {
		t.Fatalf("expected recurrence entry for %s, got %#v", issue.ID, recurrenceMap)
	}
	if recurrence.Cron != "*/15 * * * *" || !recurrence.Enabled || !recurrence.PendingRerun {
		t.Fatalf("unexpected recurrence payload: %#v", recurrence)
	}
	if recurrence.LastEnqueuedAt == nil || !recurrence.LastEnqueuedAt.UTC().Equal(now) {
		t.Fatalf("expected last enqueued time %s, got %+v", now.Format(time.RFC3339), recurrence.LastEnqueuedAt)
	}

	recurrenceMap, err = store.issueRecurrenceMap([]string{"", "   "})
	if err != nil {
		t.Fatalf("issueRecurrenceMap blank-only: %v", err)
	}
	if len(recurrenceMap) != 0 {
		t.Fatalf("expected no recurrences for blank-only input, got %#v", recurrenceMap)
	}
}

func TestUpdateIssueResolvesEpicProjectConsistency(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	epic, _ := store.CreateEpic(project.ID, "Epic", "")
	issue, _ := store.CreateIssue("", "", "Original Title", "", 0, nil)

	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"epic_id": epic.ID}); err != nil {
		t.Fatalf("UpdateIssue failed: %v", err)
	}

	updated, _ := store.GetIssue(issue.ID)
	if updated.EpicID != epic.ID {
		t.Fatalf("expected epic %s, got %s", epic.ID, updated.EpicID)
	}
	if updated.ProjectID != project.ID {
		t.Fatalf("expected project %s, got %s", project.ID, updated.ProjectID)
	}
}

func TestUpdateIssueRejectsMismatchedProjectAndEpic(t *testing.T) {
	store := setupTestStore(t)

	projectA, _ := store.CreateProject("Project A", "", "", "")
	projectB, _ := store.CreateProject("Project B", "", "", "")
	epic, _ := store.CreateEpic(projectA.ID, "Epic", "")
	issue, _ := store.CreateIssue(projectB.ID, "", "Original Title", "", 0, nil)

	err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"project_id": projectB.ID,
		"epic_id":    epic.ID,
	})
	if err == nil {
		t.Fatal("expected mismatched project/epic validation error")
	}
	if !IsValidation(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestUpdateIssueClearingProjectAlsoClearsEpic(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	epic, _ := store.CreateEpic(project.ID, "Epic", "")
	issue, _ := store.CreateIssue(project.ID, epic.ID, "Original Title", "", 0, nil)

	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"project_id": ""}); err != nil {
		t.Fatalf("UpdateIssue failed: %v", err)
	}

	updated, _ := store.GetIssue(issue.ID)
	if updated.ProjectID != "" || updated.EpicID != "" {
		t.Fatalf("expected cleared project/epic, got project=%q epic=%q", updated.ProjectID, updated.EpicID)
	}
}

func TestDeleteIssue(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "To Delete", "", 0, nil)
	workspacePath := filepath.Join(t.TempDir(), issue.Identifier)
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if err := store.DeleteIssue(issue.ID); err != nil {
		t.Fatalf("Failed to delete issue: %v", err)
	}

	_, err := store.GetIssue(issue.ID)
	if err == nil {
		t.Error("Expected error getting deleted issue")
	}
	if _, err := store.GetWorkspace(issue.ID); err == nil {
		t.Error("Expected workspace record to be deleted")
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("expected workspace path to be removed, got err=%v", err)
	}
}

func TestDeleteIssueRemovesReadOnlyWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only directory permissions behave differently on Windows")
	}

	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "To Delete", "", 0, nil)
	workspacePath := filepath.Join(t.TempDir(), issue.Identifier)
	makeReadOnlyWorkspaceTree(t, workspacePath)
	if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if err := store.DeleteIssue(issue.ID); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}

	if _, err := store.GetWorkspace(issue.ID); err == nil {
		t.Fatal("expected workspace record to be deleted")
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("expected workspace path to be removed, got err=%v", err)
	}
}

func TestDeleteIssueReturnsNotFound(t *testing.T) {
	store := setupTestStore(t)

	err := store.DeleteIssue("missing-issue")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestDeleteIssueRemovesIncomingBlockersAndReactivatesCommands(t *testing.T) {
	store := setupTestStore(t)

	blocker, err := store.CreateIssue("", "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	blocked, err := store.CreateIssue("", "", "Blocked", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}
	if err := store.UpdateIssueState(blocked.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocked: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers: %v", err)
	}

	unresolved, err := store.UnresolvedBlockersForIssue(blocked.ID)
	if err != nil {
		t.Fatalf("UnresolvedBlockersForIssue before delete: %v", err)
	}
	if len(unresolved) != 1 || unresolved[0] != blocker.Identifier {
		t.Fatalf("expected unresolved blocker %q before delete, got %#v", blocker.Identifier, unresolved)
	}

	firstCommand, err := store.CreateIssueAgentCommandWithRuntimeEvent(
		blocked.ID,
		"Resume implementation after unblock.",
		IssueAgentCommandPending,
		"manual_command_submitted",
		map[string]interface{}{
			"issue_id":   blocked.ID,
			"identifier": blocked.Identifier,
			"phase":      string(blocked.WorkflowPhase),
		},
	)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommandWithRuntimeEvent: %v", err)
	}
	if err := store.UpdateIssueAgentCommandStatus(firstCommand.ID, IssueAgentCommandWaitingForUnblock); err != nil {
		t.Fatalf("UpdateIssueAgentCommandStatus first command: %v", err)
	}
	time.Sleep(time.Millisecond)
	secondCommand, err := store.CreateIssueAgentCommand(blocked.ID, "Run the final check.", IssueAgentCommandWaitingForUnblock)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand second command: %v", err)
	}

	pending, err := store.ListPendingIssueAgentCommands(blocked.ID)
	if err != nil {
		t.Fatalf("ListPendingIssueAgentCommands before delete: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending commands while blocked, got %#v", pending)
	}

	if err := store.DeleteIssue(blocker.ID); err != nil {
		t.Fatalf("DeleteIssue blocker: %v", err)
	}

	updated, err := store.GetIssue(blocked.ID)
	if err != nil {
		t.Fatalf("GetIssue blocked after delete: %v", err)
	}
	if len(updated.BlockedBy) != 0 {
		t.Fatalf("expected blocked issue to have no blockers after delete, got %#v", updated.BlockedBy)
	}

	detail, err := store.GetIssueDetailByIdentifier(blocked.Identifier)
	if err != nil {
		t.Fatalf("GetIssueDetailByIdentifier blocked after delete: %v", err)
	}
	if detail.IsBlocked {
		t.Fatalf("expected issue detail to report unblocked after delete, got %#v", detail)
	}

	unresolved, err = store.UnresolvedBlockersForIssue(blocked.ID)
	if err != nil {
		t.Fatalf("UnresolvedBlockersForIssue after delete: %v", err)
	}
	if len(unresolved) != 0 {
		t.Fatalf("expected no unresolved blockers after delete, got %#v", unresolved)
	}

	pending, err = store.ListPendingIssueAgentCommands(blocked.ID)
	if err != nil {
		t.Fatalf("ListPendingIssueAgentCommands after delete: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected two pending commands after delete, got %#v", pending)
	}
	if pending[0].ID != firstCommand.ID || pending[1].ID != secondCommand.ID {
		t.Fatalf("expected pending commands in creation order, got %#v", pending)
	}
}

func TestIssueBlockers(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	issue1, _ := store.CreateIssue(project.ID, "", "Issue 1", "", 0, nil)
	issue2, _ := store.CreateIssue(project.ID, "", "Issue 2", "", 0, nil)

	if _, err := store.SetIssueBlockers(issue1.ID, []string{issue2.Identifier}); err != nil {
		t.Fatalf("Failed to set blockers: %v", err)
	}

	updated, _ := store.GetIssue(issue1.ID)
	if len(updated.BlockedBy) != 1 || updated.BlockedBy[0] != issue2.Identifier {
		t.Errorf("Expected blocker %s, got %v", issue2.Identifier, updated.BlockedBy)
	}
}

func TestSetIssueBlockersNormalizesDuplicates(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	issue1, _ := store.CreateIssue(project.ID, "", "Issue 1", "", 0, nil)
	issue2, _ := store.CreateIssue(project.ID, "", "Issue 2", "", 0, nil)

	blockers, err := store.SetIssueBlockers(issue1.ID, []string{issue2.Identifier, " ", issue2.Identifier})
	if err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}
	if len(blockers) != 1 || blockers[0] != issue2.Identifier {
		t.Fatalf("expected normalized blocker set [%s], got %v", issue2.Identifier, blockers)
	}
}

func TestSetIssueBlockersRejectsCrossProjectAndRollsBack(t *testing.T) {
	store := setupTestStore(t)

	projectA, _ := store.CreateProject("Project A", "", "", "")
	projectB, _ := store.CreateProject("Project B", "", "", "")
	issueA, _ := store.CreateIssue(projectA.ID, "", "Issue A", "", 0, nil)
	issueB, _ := store.CreateIssue(projectB.ID, "", "Issue B", "", 0, nil)

	if _, err := store.SetIssueBlockers(issueA.ID, []string{issueB.Identifier}); err == nil {
		t.Fatal("expected cross-project blocker validation error")
	}

	updated, _ := store.GetIssue(issueA.ID)
	if len(updated.BlockedBy) != 0 {
		t.Fatalf("expected blocker set to remain empty, got %v", updated.BlockedBy)
	}
}

func TestUpdateIssueWithInvalidBlockerRollsBackIssueFields(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Project", "", "", "")
	issue, _ := store.CreateIssue(project.ID, "", "Original", "", 0, nil)

	err := store.UpdateIssue(issue.ID, map[string]interface{}{
		"title":       "Changed",
		"description": "Changed description",
		"blocked_by":  []string{"MISSING-1"},
	})
	if err == nil {
		t.Fatal("expected invalid blocker update to fail")
	}

	updated, _ := store.GetIssue(issue.ID)
	if updated.Title != "Original" {
		t.Fatalf("expected title rollback, got %q", updated.Title)
	}
	if updated.Description != "" {
		t.Fatalf("expected description rollback, got %q", updated.Description)
	}
	if len(updated.BlockedBy) != 0 {
		t.Fatalf("expected blockers rollback, got %v", updated.BlockedBy)
	}
}

// Workspace tests

func TestCreateWorkspace(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)

	workspace, err := store.CreateWorkspace(issue.ID, "/tmp/workspace")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}

	if workspace.IssueID != issue.ID {
		t.Error("Workspace issue ID mismatch")
	}
	if workspace.RunCount != 0 {
		t.Errorf("Expected run count 0, got %d", workspace.RunCount)
	}
}

func TestUpdateWorkspaceRun(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	_, _ = store.CreateWorkspace(issue.ID, "/tmp/workspace")

	if err := store.UpdateWorkspaceRun(issue.ID); err != nil {
		t.Fatalf("Failed to update workspace run: %v", err)
	}

	workspace, _ := store.GetWorkspace(issue.ID)
	if workspace.RunCount != 1 {
		t.Errorf("Expected run count 1, got %d", workspace.RunCount)
	}
	if workspace.LastRunAt == nil {
		t.Error("Expected last_run_at to be set")
	}
}

func TestUpdateWorkspacePath(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	_, _ = store.CreateWorkspace(issue.ID, "/tmp/workspace")

	updated, err := store.UpdateWorkspacePath(issue.ID, "/tmp/workspace-renamed")
	if err != nil {
		t.Fatalf("Failed to update workspace path: %v", err)
	}
	if updated.Path != "/tmp/workspace-renamed" {
		t.Fatalf("expected updated path, got %q", updated.Path)
	}

	loaded, err := store.GetWorkspace(issue.ID)
	if err != nil {
		t.Fatalf("GetWorkspace failed: %v", err)
	}
	if loaded.Path != "/tmp/workspace-renamed" {
		t.Fatalf("expected persisted path, got %q", loaded.Path)
	}
}

func TestDeleteWorkspace(t *testing.T) {
	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	_, _ = store.CreateWorkspace(issue.ID, workspacePath)

	if err := store.DeleteWorkspace(issue.ID); err != nil {
		t.Fatalf("Failed to delete workspace: %v", err)
	}

	_, err := store.GetWorkspace(issue.ID)
	if err == nil {
		t.Error("Expected error getting deleted workspace")
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("expected workspace path to be removed, got err=%v", err)
	}
}

func TestDeleteWorkspaceRemovesReadOnlyTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only directory permissions behave differently on Windows")
	}

	store := setupTestStore(t)

	issue, _ := store.CreateIssue("", "", "Test", "", 0, nil)
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	makeReadOnlyWorkspaceTree(t, workspacePath)
	if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if err := store.DeleteWorkspace(issue.ID); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}

	if _, err := store.GetWorkspace(issue.ID); err == nil {
		t.Fatal("expected workspace record to be deleted")
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("expected workspace path to be removed, got err=%v", err)
	}
}

func TestDeleteWorkspaceRemovesGitWorktree(t *testing.T) {
	store := setupTestStore(t)

	repoPath := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("MkdirAll repo: %v", err)
	}
	initGitRepoForStoreTest(t, repoPath)

	issue, err := store.CreateIssue("", "", "Git worktree", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	runGitForStoreTest(t, repoPath, "worktree", "add", "-b", "codex/test-worktree", workspacePath, "HEAD")
	if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	commonDir, err := gitCommonDirForWorkspace(workspacePath)
	if err != nil {
		t.Fatalf("gitCommonDirForWorkspace: %v", err)
	}
	if filepath.Base(commonDir) != ".git" {
		t.Fatalf("expected common dir ending in .git, got %q", commonDir)
	}

	if err := store.DeleteWorkspace(issue.ID); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("expected worktree path to be removed, got err=%v", err)
	}
	if output := runGitForStoreTest(t, repoPath, "worktree", "list", "--porcelain"); strings.Contains(output, workspacePath) {
		t.Fatalf("expected git worktree list to exclude %q, got %q", workspacePath, output)
	}
}

func makeReadOnlyWorkspaceTree(t *testing.T, root string) {
	t.Helper()

	moduleDir := filepath.Join(root, ".maestro", "tmp", "go-mod", "gopkg.in", "yaml.v3@v3.0.1")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("MkdirAll module dir: %v", err)
	}

	testFile := filepath.Join(moduleDir, "decode_test.go")
	if err := os.WriteFile(testFile, []byte("package yaml\n"), 0o644); err != nil {
		t.Fatalf("WriteFile decode_test.go: %v", err)
	}

	t.Cleanup(func() {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info == nil {
				return nil
			}
			if info.IsDir() {
				_ = os.Chmod(path, 0o700)
				return nil
			}
			if info.Mode()&os.ModeSymlink == 0 {
				_ = os.Chmod(path, 0o600)
			}
			return nil
		})
	})

	if err := os.Chmod(testFile, 0o444); err != nil {
		t.Fatalf("Chmod decode_test.go: %v", err)
	}
	if err := os.Chmod(moduleDir, 0o555); err != nil {
		t.Fatalf("Chmod module dir: %v", err)
	}
}

func TestGenerateIdentifierHelper(t *testing.T) {
	store := setupTestStore(t)

	project, err := store.CreateProject("A B C", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	first, err := store.generateIdentifier(project.ID)
	if err != nil {
		t.Fatalf("generateIdentifier first failed: %v", err)
	}
	second, err := store.generateIdentifier(project.ID)
	if err != nil {
		t.Fatalf("generateIdentifier second failed: %v", err)
	}
	if first != "ABC-1" || second != "ABC-2" {
		t.Fatalf("expected sequential project prefix identifiers, got %q and %q", first, second)
	}
}

func TestIdentifierPrefixFromProjectName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "A B C", want: "ABC"},
		{name: "Go", want: "GO"},
		{name: "  \t  ", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := identifierPrefixFromProjectName(tc.name); got != tc.want {
				t.Fatalf("expected prefix %q, got %q", tc.want, got)
			}
		})
	}
}

func TestGenerateIdentifier(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("MyApp", "", "", "")

	issue1, _ := store.CreateIssue(project.ID, "", "First", "", 0, nil)
	issue2, _ := store.CreateIssue(project.ID, "", "Second", "", 0, nil)

	// Identifiers should be unique
	if issue1.Identifier == issue2.Identifier {
		t.Error("Expected unique identifiers")
	}

	// Identifier should have a prefix
	if len(issue1.Identifier) < 5 {
		t.Errorf("Identifier too short: %s", issue1.Identifier)
	}
}

func TestIssuePrioritySorting(t *testing.T) {
	store := setupTestStore(t)

	// Create issues with different priorities
	_, _ = store.CreateIssue("", "", "Low Priority", "", 10, nil)
	_, _ = store.CreateIssue("", "", "High Priority", "", 1, nil)
	_, _ = store.CreateIssue("", "", "Unprioritized", "", 0, nil)
	_, _ = store.CreateIssue("", "", "Medium Priority", "", 5, nil)

	issues, _ := store.ListIssues(nil)

	// Positive priorities should be sorted ascending before unprioritized (0).
	if len(issues) < 4 {
		t.Fatalf("expected 4 issues, got %d", len(issues))
	}
	if issues[0].Priority != 1 || issues[1].Priority != 5 || issues[2].Priority != 10 || issues[3].Priority != 0 {
		t.Fatalf("unexpected priority order: got [%d %d %d %d]", issues[0].Priority, issues[1].Priority, issues[2].Priority, issues[3].Priority)
	}
}

func TestListIssueSummariesPrioritySortTreatsZeroAsUnprioritized(t *testing.T) {
	store := setupTestStore(t)
	_, _ = store.CreateIssue("", "", "No priority", "", 0, nil)
	_, _ = store.CreateIssue("", "", "P3", "", 3, nil)
	_, _ = store.CreateIssue("", "", "P1", "", 1, nil)

	items, _, err := store.ListIssueSummaries(IssueQuery{
		Sort:  "priority_asc",
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListIssueSummaries failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 issues, got %d", len(items))
	}
	if items[0].Priority != 1 || items[1].Priority != 3 || items[2].Priority != 0 {
		t.Fatalf("unexpected priority_asc order: got [%d %d %d]", items[0].Priority, items[1].Priority, items[2].Priority)
	}
}

func TestListIssueSummariesSupportsBlockedAndProjectNameFilters(t *testing.T) {
	store := setupTestStore(t)

	project, _ := store.CreateProject("Platform", "", "", "")
	other, _ := store.CreateProject("Other", "", "", "")
	activeBlocker, _ := store.CreateIssue(project.ID, "", "Active blocker", "", 0, nil)
	resolvedBlocker, _ := store.CreateIssue(project.ID, "", "Resolved blocker", "", 0, nil)
	blocked, _ := store.CreateIssue(project.ID, "", "Blocked subject", "", 0, nil)
	resolved, _ := store.CreateIssue(project.ID, "", "Resolved subject", "", 0, nil)
	_, _ = store.CreateIssue(other.ID, "", "Elsewhere", "", 0, nil)
	if err := store.UpdateIssueState(activeBlocker.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState activeBlocker: %v", err)
	}
	if err := store.UpdateIssueState(resolvedBlocker.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState resolvedBlocker: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{activeBlocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}
	if _, err := store.SetIssueBlockers(resolved.ID, []string{resolvedBlocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}

	blockedOnly := true
	items, total, err := store.ListIssueSummaries(IssueQuery{
		ProjectName: "platform",
		Search:      "subject",
		Blocked:     &blockedOnly,
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("ListIssueSummaries failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected one blocked platform issue, got total=%d items=%d", total, len(items))
	}
	if items[0].Identifier != blocked.Identifier {
		t.Fatalf("expected blocked identifier %s, got %s", blocked.Identifier, items[0].Identifier)
	}
	if !items[0].IsBlocked {
		t.Fatalf("expected %s to be marked blocked", blocked.Identifier)
	}
	if len(items[0].BlockedBy) != 1 || items[0].BlockedBy[0] != activeBlocker.Identifier {
		t.Fatalf("expected %s blockers [%s], got %v", blocked.Identifier, activeBlocker.Identifier, items[0].BlockedBy)
	}

	blockedOnly = false
	items, total, err = store.ListIssueSummaries(IssueQuery{
		ProjectName: "platform",
		Search:      "subject",
		Blocked:     &blockedOnly,
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("ListIssueSummaries failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected one unblocked platform issue with resolved blockers, got total=%d items=%d", total, len(items))
	}
	if items[0].Identifier != resolved.Identifier {
		t.Fatalf("expected resolved identifier %s, got %s", resolved.Identifier, items[0].Identifier)
	}
	if items[0].IsBlocked {
		t.Fatalf("expected %s not to be marked blocked once its blocker is done", resolved.Identifier)
	}
	if len(items[0].BlockedBy) != 1 || items[0].BlockedBy[0] != resolvedBlocker.Identifier {
		t.Fatalf("expected %s blockers [%s], got %v", resolved.Identifier, resolvedBlocker.Identifier, items[0].BlockedBy)
	}
}

func TestConcurrentAccess(t *testing.T) {
	store := setupTestStore(t)

	done := make(chan bool)

	// Concurrent creates - use mutex or sequential since identifier generation isn't thread-safe
	for i := 0; i < 10; i++ {
		go func(n int) {
			// Create issues sequentially within each goroutine to avoid race
			_, err := store.CreateIssue("", "", "Concurrent", "", 0, nil)
			if err != nil {
				// UNIQUE constraint on identifier is expected under concurrency
				// Try again
				_, err = store.CreateIssue("", "", "Concurrent", "", 0, nil)
			}
			if err != nil {
				t.Errorf("Concurrent create failed: %v", err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have at least some issues created
	issues, _ := store.ListIssues(nil)
	if len(issues) < 1 {
		t.Errorf("Expected at least 1 issue, got %d", len(issues))
	}
}

func TestCreateProjectNormalizesPathsAndReadiness(t *testing.T) {
	store := setupTestStore(t)
	repoDir := t.TempDir()
	workflowPath := filepath.Join(repoDir, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte("---\ntracker:\n  kind: kanban\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	project, err := store.CreateProject("Repo Project", "desc", repoDir, "")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if project.RepoPath != repoDir {
		t.Fatalf("expected repo path %q, got %q", repoDir, project.RepoPath)
	}
	if project.WorkflowPath != workflowPath {
		t.Fatalf("expected workflow path %q, got %q", workflowPath, project.WorkflowPath)
	}
	if !project.OrchestrationReady {
		t.Fatal("expected project to be orchestration ready")
	}
	if project.State != ProjectStateStopped {
		t.Fatalf("expected stopped project state, got %q", project.State)
	}
}

func TestExistingProjectsMigrateToStoppedState(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			repo_path TEXT NOT NULL DEFAULT '',
			workflow_path TEXT NOT NULL DEFAULT '',
			provider_kind TEXT NOT NULL DEFAULT 'kanban',
			provider_project_ref TEXT NOT NULL DEFAULT '',
			provider_config_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`); err != nil {
		t.Fatalf("create legacy projects table: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO projects (id, name, description, repo_path, workflow_path, provider_kind, provider_project_ref, provider_config_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"proj-legacy", "Legacy", "", "", "", ProviderKindKanban, "", "{}", now, now,
	); err != nil {
		t.Fatalf("insert legacy project: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.GetProject("proj-legacy")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if project.State != ProjectStateStopped {
		t.Fatalf("expected migrated project state stopped, got %q", project.State)
	}
}

func TestLatestChangeSeqAdvancesOnMutations(t *testing.T) {
	store := setupTestStore(t)
	before, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq failed: %v", err)
	}

	if _, err := store.CreateProject("Tracked", "", "", ""); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	after, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq failed: %v", err)
	}
	if after <= before {
		t.Fatalf("expected change seq to increase, before=%d after=%d", before, after)
	}
}

func TestNewStoreUsesSmallSQLitePool(t *testing.T) {
	store := setupTestStore(t)
	if got := store.db.Stats().MaxOpenConnections; got != sqliteMaxOpenConns {
		t.Fatalf("expected sqlite max open connections to be %d, got %d", sqliteMaxOpenConns, got)
	}
}

func TestStoreAllowsConcurrentReadsWhileWriting(t *testing.T) {
	store := setupTestStore(t)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup

	for writer := 0; writer < 4; writer++ {
		writer := writer
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 12; i++ {
				if _, err := store.CreateIssue("", "", "Concurrent issue", "", writer, nil); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				if err := store.AppendRuntimeEvent("tick", map[string]interface{}{
					"issue_id":     "",
					"identifier":   "",
					"title":        "Concurrent issue",
					"attempt":      i,
					"total_tokens": writer + i,
				}); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}()
	}

	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				if _, _, err := store.ListIssueSummaries(IssueQuery{Sort: "updated_desc", Limit: 25}); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				if _, err := store.LatestChangeSeq(); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("expected concurrent read/write access without sqlite errors, got %v", err)
		}
	}
}

func TestStoreIdentityStable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "identity.db")

	store1, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	identity1 := store1.Identity()
	if identity1.StoreID == "" {
		t.Fatal("expected non-empty store id")
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	store2, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store2.Close()

	identity2 := store2.Identity()
	if identity1.StoreID != identity2.StoreID {
		t.Fatalf("expected stable store id, got %q then %q", identity1.StoreID, identity2.StoreID)
	}
	absDBPath, _ := filepath.Abs(dbPath)
	if identity2.DBPath != absDBPath {
		t.Fatalf("expected db path %q, got %q", absDBPath, identity2.DBPath)
	}
}

func TestListIssueRuntimeEventsFiltersAndOrdersExecutionEvents(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Runtime issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	for _, kind := range []string{"run_started", "tick", "workspace_bootstrap_created", "workspace_bootstrap_recovery", "workspace_bootstrap_failed", "run_failed", "retry_scheduled", "manual_retry_requested"} {
		if err := store.AppendRuntimeEvent(kind, map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s) failed: %v", kind, err)
		}
	}

	events, err := store.ListIssueRuntimeEvents(issue.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents failed: %v", err)
	}
	if len(events) != 7 {
		t.Fatalf("expected 7 execution events, got %d", len(events))
	}
	if events[0].Kind != "run_started" || events[len(events)-1].Kind != "manual_retry_requested" {
		t.Fatalf("expected oldest-to-newest execution events, got %#v", events)
	}
	for _, kind := range []string{"workspace_bootstrap_created", "workspace_bootstrap_recovery", "workspace_bootstrap_failed"} {
		found := false
		for _, event := range events {
			if event.Kind == kind {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s event in filtered runtime events, got %#v", kind, events)
		}
	}
	for _, event := range events {
		if event.Kind == "tick" {
			t.Fatalf("unexpected non-execution event returned: %#v", event)
		}
		if event.IssueID != issue.ID {
			t.Fatalf("unexpected issue id: %#v", event)
		}
	}
}

func TestRuntimeSeriesCountsFailuresAndUsesPerThreadTokenDeltas(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Runtime series issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Hour)
	bucket0 := now.Add(-2 * time.Hour)
	threadID := "thread-runtime-series"
	insertNullErrorEvent := func(ts time.Time, kind string, totalTokens int) {
		t.Helper()
		payload := map[string]interface{}{
			"issue_id":     issue.ID,
			"identifier":   issue.Identifier,
			"attempt":      1,
			"thread_id":    threadID,
			"total_tokens": totalTokens,
			"ts":           ts.UTC().Format(time.RFC3339),
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal runtime payload: %v", err)
		}
		if _, err := store.db.Exec(`
			INSERT INTO runtime_events (kind, issue_id, identifier, title, attempt, delay_type, input_tokens, output_tokens, total_tokens, error, event_ts, payload_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
			kind,
			issue.ID,
			issue.Identifier,
			issue.Title,
			1,
			"",
			0,
			0,
			totalTokens,
			ts,
			string(body),
		); err != nil {
			t.Fatalf("insert runtime event %s: %v", kind, err)
		}
	}
	appendEvent := func(ts time.Time, kind string, fields map[string]interface{}) {
		t.Helper()
		payload := map[string]interface{}{
			"issue_id":     issue.ID,
			"identifier":   issue.Identifier,
			"attempt":      1,
			"thread_id":    threadID,
			"total_tokens": 0,
			"ts":           ts.UTC().Format(time.RFC3339),
		}
		for key, value := range fields {
			payload[key] = value
		}
		if err := store.AppendRuntimeEvent(kind, payload); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s) failed: %v", kind, err)
		}
	}

	insertNullErrorEvent(bucket0.Add(-10*time.Minute), "run_completed", 8)
	appendEvent(bucket0.Add(5*time.Minute), "run_started", nil)
	appendEvent(bucket0.Add(10*time.Minute), "run_interrupted", map[string]interface{}{
		"error":        "run_interrupted",
		"total_tokens": 8,
	})
	appendEvent(bucket0.Add(20*time.Minute), "retry_scheduled", nil)
	insertNullErrorEvent(bucket0.Add(30*time.Minute), "run_failed", 8)
	appendEvent(bucket0.Add(75*time.Minute), "run_completed", map[string]interface{}{
		"total_tokens": 14,
	})

	series, err := store.RuntimeSeries(3)
	if err != nil {
		t.Fatalf("RuntimeSeries failed: %v", err)
	}
	if len(series) != 3 {
		t.Fatalf("expected 3 series buckets, got %d", len(series))
	}

	if series[0].Bucket != bucket0.Format("15:04") {
		t.Fatalf("expected first bucket %s, got %s", bucket0.Format("15:04"), series[0].Bucket)
	}
	if series[0].RunsStarted != 1 || series[0].RunsCompleted != 0 || series[0].RunsFailed != 2 || series[0].Retries != 1 || series[0].Tokens != 0 {
		t.Fatalf("unexpected first bucket aggregation: %#v", series[0])
	}
	if series[1].Bucket != bucket0.Add(time.Hour).Format("15:04") {
		t.Fatalf("expected second bucket %s, got %s", bucket0.Add(time.Hour).Format("15:04"), series[1].Bucket)
	}
	if series[1].RunsStarted != 0 || series[1].RunsCompleted != 1 || series[1].RunsFailed != 0 || series[1].Retries != 0 || series[1].Tokens != 6 {
		t.Fatalf("unexpected second bucket aggregation: %#v", series[1])
	}
	if series[2].Tokens != 0 || series[2].RunsStarted != 0 || series[2].RunsCompleted != 0 || series[2].RunsFailed != 0 || series[2].Retries != 0 {
		t.Fatalf("expected empty trailing bucket, got %#v", series[2])
	}
}

func TestRuntimeEventReadersTolerateMalformedPayloadJSON(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Runtime issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}

	now := time.Now().UTC()
	if _, err := store.db.Exec(`
		INSERT INTO runtime_events (kind, issue_id, identifier, title, attempt, delay_type, input_tokens, output_tokens, total_tokens, error, event_ts, payload_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"run_started",
		issue.ID,
		issue.Identifier,
		issue.Title,
		1,
		"",
		0,
		0,
		0,
		"",
		now,
		"{",
	); err != nil {
		t.Fatalf("insert malformed runtime event: %v", err)
	}

	events, err := store.ListRuntimeEvents(0, 10)
	if err != nil {
		t.Fatalf("ListRuntimeEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 runtime event, got %d", len(events))
	}
	if events[0].Payload != nil {
		t.Fatalf("expected malformed payload to be ignored, got %#v", events[0].Payload)
	}

	issueEvents, err := store.ListIssueRuntimeEvents(issue.ID, 10)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents failed: %v", err)
	}
	if len(issueEvents) != 1 {
		t.Fatalf("expected 1 issue runtime event, got %d", len(issueEvents))
	}
	if issueEvents[0].Payload != nil {
		t.Fatalf("expected malformed issue payload to be ignored, got %#v", issueEvents[0].Payload)
	}
}

func TestIssueExecutionSessionSnapshotRoundTrip(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Session issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := ExecutionSessionSnapshot{
		IssueID:        issue.ID,
		Identifier:     issue.Identifier,
		Phase:          "implementation",
		Attempt:        2,
		RunKind:        "run_failed",
		Error:          "approval_required",
		ResumeEligible: true,
		StopReason:     "graceful_shutdown",
		UpdatedAt:      now,
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-1-turn-1",
			ThreadID:        "thread-1",
			TurnID:          "turn-1",
			LastEvent:       "turn.started",
			LastTimestamp:   now,
			LastMessage:     "Waiting for approval",
			TotalTokens:     42,
			TurnsStarted:    1,
			History: []appserver.Event{
				{Type: "turn.started", Message: "Start"},
				{Type: "turn.approval_required", Message: "Waiting for approval"},
			},
		},
	}

	if err := store.UpsertIssueExecutionSession(snapshot); err != nil {
		t.Fatalf("UpsertIssueExecutionSession failed: %v", err)
	}

	loaded, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession failed: %v", err)
	}
	if loaded.Attempt != 2 || loaded.RunKind != "run_failed" || loaded.Error != "approval_required" {
		t.Fatalf("unexpected snapshot metadata: %+v", loaded)
	}
	if !loaded.ResumeEligible || loaded.StopReason != "graceful_shutdown" {
		t.Fatalf("expected resume metadata, got %+v", loaded)
	}
	if loaded.AppSession.SessionID != "thread-1-turn-1" || len(loaded.AppSession.History) != 0 {
		t.Fatalf("unexpected session payload: %+v", loaded.AppSession)
	}

	snapshot.Attempt = 3
	snapshot.RunKind = "run_completed"
	snapshot.Error = ""
	snapshot.ResumeEligible = false
	snapshot.StopReason = ""
	snapshot.AppSession.LastEvent = "turn.completed"
	if err := store.UpsertIssueExecutionSession(snapshot); err != nil {
		t.Fatalf("UpsertIssueExecutionSession update failed: %v", err)
	}
	loaded, err = store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession after update failed: %v", err)
	}
	if loaded.Attempt != 3 || loaded.RunKind != "run_completed" || loaded.AppSession.LastEvent != "turn.completed" {
		t.Fatalf("unexpected updated snapshot: %+v", loaded)
	}
	if loaded.ResumeEligible || loaded.StopReason != "" {
		t.Fatalf("expected cleared resume metadata, got %+v", loaded)
	}
}

func TestIssueExecutionSessionMigrationAddsResumeColumns(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE issue_execution_sessions (
			issue_id TEXT PRIMARY KEY,
			identifier TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL DEFAULT '',
			attempt INTEGER NOT NULL DEFAULT 0,
			run_kind TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			session_json TEXT NOT NULL DEFAULT '{}'
		)`); err != nil {
		t.Fatalf("create legacy table failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db failed: %v", err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	issue, err := store.CreateIssue("", "", "Migrated session", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
		IssueID:        issue.ID,
		Identifier:     issue.Identifier,
		Phase:          "implementation",
		Attempt:        1,
		RunKind:        "run_started",
		ResumeEligible: true,
		StopReason:     "graceful_shutdown",
		UpdatedAt:      time.Now().UTC(),
		AppSession:     appserver.Session{IssueID: issue.ID, IssueIdentifier: issue.Identifier, ThreadID: "thread-migrated"},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession failed: %v", err)
	}

	loaded, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession failed: %v", err)
	}
	if !loaded.ResumeEligible || loaded.StopReason != "graceful_shutdown" {
		t.Fatalf("expected migrated resume metadata, got %+v", loaded)
	}
}

func TestIssueExecutionSessionUpsertDoesNotEmitChangeEvents(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Quiet session issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	before, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq failed: %v", err)
	}

	if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
		IssueID:    issue.ID,
		Identifier: issue.Identifier,
		Phase:      "implementation",
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  time.Now().UTC(),
		AppSession: appserver.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "thread-quiet-turn-quiet",
			LastEvent:       "item.started",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession failed: %v", err)
	}

	after, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq failed: %v", err)
	}
	if after != before {
		t.Fatalf("expected execution session upsert not to emit change event, before=%d after=%d", before, after)
	}
}

func TestListRecentExecutionSessionsOrdersAndDecodesPayloads(t *testing.T) {
	store := setupTestStore(t)
	oldIssue, err := store.CreateIssue("", "", "Older session", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue old failed: %v", err)
	}
	newIssue, err := store.CreateIssue("", "", "Newer session", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue new failed: %v", err)
	}
	base := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	for _, snapshot := range []ExecutionSessionSnapshot{
		{
			IssueID:    oldIssue.ID,
			Identifier: oldIssue.Identifier,
			Phase:      "implementation",
			Attempt:    1,
			RunKind:    "run_failed",
			Error:      "approval_required",
			UpdatedAt:  base.Add(-2 * time.Hour),
			AppSession: appserver.Session{
				IssueID:         oldIssue.ID,
				IssueIdentifier: oldIssue.Identifier,
				SessionID:       "thread-old-turn-old",
				LastEvent:       "turn.approval_required",
				LastTimestamp:   base.Add(-2 * time.Hour),
				LastMessage:     "Waiting for approval",
				History:         []appserver.Event{{Type: "turn.approval_required", Message: "Waiting for approval"}},
			},
		},
		{
			IssueID:    newIssue.ID,
			Identifier: newIssue.Identifier,
			Phase:      "review",
			Attempt:    2,
			RunKind:    "run_completed",
			UpdatedAt:  base,
			AppSession: appserver.Session{
				IssueID:         newIssue.ID,
				IssueIdentifier: newIssue.Identifier,
				SessionID:       "thread-new-turn-new",
				LastEvent:       "turn.completed",
				LastTimestamp:   base,
				LastMessage:     "Finished review",
				History:         []appserver.Event{{Type: "turn.completed", Message: "Finished review"}},
			},
		},
	} {
		if err := store.UpsertIssueExecutionSession(snapshot); err != nil {
			t.Fatalf("UpsertIssueExecutionSession(%s) failed: %v", snapshot.Identifier, err)
		}
	}

	snapshots, err := store.ListRecentExecutionSessions(base.Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatalf("ListRecentExecutionSessions failed: %v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snapshots))
	}
	if snapshots[0].IssueID != newIssue.ID || snapshots[1].IssueID != oldIssue.ID {
		t.Fatalf("expected newest-first ordering, got %#v", snapshots)
	}
	if snapshots[0].AppSession.LastMessage != "Finished review" || len(snapshots[0].AppSession.History) != 0 {
		t.Fatalf("expected decoded app session payload, got %+v", snapshots[0].AppSession)
	}

	filtered, err := store.ListRecentExecutionSessions(base.Add(-90*time.Minute), 10)
	if err != nil {
		t.Fatalf("ListRecentExecutionSessions filtered failed: %v", err)
	}
	if len(filtered) != 1 || filtered[0].IssueID != newIssue.ID {
		t.Fatalf("expected recent filter to keep only newest snapshot, got %#v", filtered)
	}
}

func TestRunMaintenancePrunesExpiredRowsButKeepsActiveIssueData(t *testing.T) {
	store := setupTestStore(t)
	oldIssue, err := store.CreateIssue("", "", "Old issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue old: %v", err)
	}
	activeIssue, err := store.CreateIssue("", "", "Active issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue active: %v", err)
	}

	oldTS := time.Now().UTC().AddDate(0, 0, -(runtimeEventRetentionDays + 2))
	activityTS := time.Now().UTC().AddDate(0, 0, -(issueActivityRetentionDays + 2))
	sessionTS := time.Now().UTC().AddDate(0, 0, -(completedSessionRetentionDays + 2))

	for _, issue := range []Issue{*oldIssue, *activeIssue} {
		if _, err := store.db.Exec(`INSERT INTO runtime_events (kind, issue_id, identifier, event_ts, payload_json) VALUES (?, ?, ?, ?, '{}')`,
			"run_failed", issue.ID, issue.Identifier, oldTS,
		); err != nil {
			t.Fatalf("insert runtime event: %v", err)
		}
		if _, err := store.db.Exec(`INSERT INTO issue_activity_entries (issue_id, identifier, logical_id, attempt, kind, title, summary, created_at, updated_at, raw_payload_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '{}')`,
			issue.ID, issue.Identifier, issue.ID+"-entry", 1, "status", "Turn Failed", "failed", activityTS, activityTS,
		); err != nil {
			t.Fatalf("insert activity entry: %v", err)
		}
		if _, err := store.db.Exec(`INSERT INTO issue_activity_updates (issue_id, entry_id, event_type, event_ts, payload_json) VALUES (?, ?, ?, ?, '{}')`,
			issue.ID, issue.ID+"-entry", "turn.failed", activityTS,
		); err != nil {
			t.Fatalf("insert activity update: %v", err)
		}
		if _, err := store.db.Exec(`INSERT INTO issue_execution_sessions (issue_id, identifier, phase, attempt, run_kind, error, resume_eligible, stop_reason, updated_at, session_json) VALUES (?, ?, '', 1, 'run_failed', '', 0, '', ?, '{}')`,
			issue.ID, issue.Identifier, sessionTS,
		); err != nil {
			t.Fatalf("insert execution session: %v", err)
		}
	}
	if _, err := store.db.Exec(`INSERT INTO change_events (entity_type, entity_id, action, event_ts, payload_json) VALUES ('runtime_event', ?, 'run_failed', ?, '{}')`,
		oldIssue.ID, oldTS.AddDate(0, 0, -(changeEventRetentionDays))); err != nil {
		t.Fatalf("insert change event: %v", err)
	}

	result, err := store.RunMaintenance([]string{activeIssue.ID})
	if err != nil {
		t.Fatalf("RunMaintenance: %v", err)
	}
	if result.CheckpointResult == "" || result.CheckpointAt.IsZero() {
		t.Fatalf("expected checkpoint metadata, got %+v", result)
	}

	for _, tc := range []struct {
		query string
		args  []interface{}
		want  int
	}{
		{query: `SELECT COUNT(*) FROM runtime_events WHERE issue_id = ?`, args: []interface{}{oldIssue.ID}, want: 0},
		{query: `SELECT COUNT(*) FROM runtime_events WHERE issue_id = ?`, args: []interface{}{activeIssue.ID}, want: 1},
		{query: `SELECT COUNT(*) FROM issue_activity_entries WHERE issue_id = ?`, args: []interface{}{oldIssue.ID}, want: 0},
		{query: `SELECT COUNT(*) FROM issue_activity_entries WHERE issue_id = ?`, args: []interface{}{activeIssue.ID}, want: 1},
		{query: `SELECT COUNT(*) FROM issue_activity_updates WHERE issue_id = ?`, args: []interface{}{oldIssue.ID}, want: 0},
		{query: `SELECT COUNT(*) FROM issue_execution_sessions WHERE issue_id = ?`, args: []interface{}{oldIssue.ID}, want: 0},
		{query: `SELECT COUNT(*) FROM change_events WHERE entity_id = ? AND action = ?`, args: []interface{}{oldIssue.ID, "run_failed"}, want: 0},
	} {
		var got int
		if err := store.db.QueryRow(tc.query, tc.args...).Scan(&got); err != nil {
			t.Fatalf("query %q: %v", tc.query, err)
		}
		if got != tc.want {
			t.Fatalf("query %q = %d, want %d", tc.query, got, tc.want)
		}
	}
}

func TestRunMaintenancePrunesExpiredRunStartedStateForUnprotectedIssues(t *testing.T) {
	store := setupTestStore(t)
	oldIssue, err := store.CreateIssue("", "", "Old run started issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue old: %v", err)
	}
	protectedIssue, err := store.CreateIssue("", "", "Protected run started issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue protected: %v", err)
	}

	oldTS := time.Now().UTC().AddDate(0, 0, -(runtimeEventRetentionDays + 2))
	sessionTS := time.Now().UTC().AddDate(0, 0, -(completedSessionRetentionDays + 2))

	for _, issue := range []Issue{*oldIssue, *protectedIssue} {
		if _, err := store.db.Exec(`INSERT INTO runtime_events (kind, issue_id, identifier, event_ts, payload_json) VALUES (?, ?, ?, ?, '{}')`,
			"run_started", issue.ID, issue.Identifier, oldTS,
		); err != nil {
			t.Fatalf("insert runtime event: %v", err)
		}
		if _, err := store.db.Exec(`INSERT INTO issue_execution_sessions (issue_id, identifier, phase, attempt, run_kind, error, resume_eligible, stop_reason, updated_at, session_json) VALUES (?, ?, '', 1, 'run_started', '', 0, '', ?, '{}')`,
			issue.ID, issue.Identifier, sessionTS,
		); err != nil {
			t.Fatalf("insert execution session: %v", err)
		}
	}

	if _, err := store.RunMaintenance([]string{protectedIssue.ID}); err != nil {
		t.Fatalf("RunMaintenance: %v", err)
	}

	for _, tc := range []struct {
		query string
		args  []interface{}
		want  int
	}{
		{query: `SELECT COUNT(*) FROM runtime_events WHERE issue_id = ?`, args: []interface{}{oldIssue.ID}, want: 0},
		{query: `SELECT COUNT(*) FROM issue_execution_sessions WHERE issue_id = ?`, args: []interface{}{oldIssue.ID}, want: 0},
		{query: `SELECT COUNT(*) FROM runtime_events WHERE issue_id = ?`, args: []interface{}{protectedIssue.ID}, want: 1},
		{query: `SELECT COUNT(*) FROM issue_execution_sessions WHERE issue_id = ?`, args: []interface{}{protectedIssue.ID}, want: 1},
	} {
		var got int
		if err := store.db.QueryRow(tc.query, tc.args...).Scan(&got); err != nil {
			t.Fatalf("query %q: %v", tc.query, err)
		}
		if got != tc.want {
			t.Fatalf("query %q = %d, want %d", tc.query, got, tc.want)
		}
	}
}

func TestDBStatsReturnsPageMetadata(t *testing.T) {
	store := setupTestStore(t)

	stats, err := store.DBStats()
	if err != nil {
		t.Fatalf("DBStats: %v", err)
	}
	if stats.PageCount <= 0 || stats.PageSize <= 0 {
		t.Fatalf("expected positive page metadata, got %+v", stats)
	}
	if stats.FreelistCount < 0 {
		t.Fatalf("expected non-negative freelist metadata, got %+v", stats)
	}
}

func TestStoreAccessorsAndAdditionalCRUDPaths(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "extra.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if store.DBPath() == "" {
		t.Fatal("expected DBPath to be populated")
	}
	if store.StoreID() == "" {
		t.Fatal("expected StoreID to be populated")
	}

	repoPath := t.TempDir()
	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte("---\ntracker:\n  kind: kanban\n---\n"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	project, err := store.CreateProject("Project", "", repoPath, workflowPath)
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	updatedRepo := t.TempDir()
	updatedWorkflow := filepath.Join(updatedRepo, "ALT_WORKFLOW.md")
	if err := os.WriteFile(updatedWorkflow, []byte("---\ntracker:\n  kind: kanban\n---\n"), 0o644); err != nil {
		t.Fatalf("write updated workflow: %v", err)
	}
	if err := store.UpdateProject(project.ID, "Project Updated", "desc", updatedRepo, updatedWorkflow); err != nil {
		t.Fatalf("UpdateProject failed: %v", err)
	}
	project, err = store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject after update failed: %v", err)
	}
	if project.Name != "Project Updated" || project.RepoPath != updatedRepo || project.WorkflowPath != updatedWorkflow {
		t.Fatalf("unexpected updated project: %+v", project)
	}
	if project.PermissionProfile != PermissionProfileDefault {
		t.Fatalf("expected default permission profile, got %q", project.PermissionProfile)
	}
	if err := store.UpdateProjectPermissionProfile(project.ID, PermissionProfileFullAccess); err != nil {
		t.Fatalf("UpdateProjectPermissionProfile failed: %v", err)
	}
	project, err = store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject after permission update failed: %v", err)
	}
	if project.PermissionProfile != PermissionProfileFullAccess {
		t.Fatalf("expected full-access permission profile, got %q", project.PermissionProfile)
	}

	issue, err := store.CreateIssue(project.ID, "", "Issue permissions", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if issue.PermissionProfile != PermissionProfileDefault {
		t.Fatalf("expected default issue permission profile, got %q", issue.PermissionProfile)
	}
	if err := store.UpdateIssuePermissionProfile(issue.ID, PermissionProfileFullAccess); err != nil {
		t.Fatalf("UpdateIssuePermissionProfile failed: %v", err)
	}
	issue, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after permission update failed: %v", err)
	}
	if issue.PermissionProfile != PermissionProfileFullAccess {
		t.Fatalf("expected full-access issue permission profile, got %q", issue.PermissionProfile)
	}

	epic, err := store.CreateEpic(project.ID, "Epic", "desc")
	if err != nil {
		t.Fatalf("CreateEpic failed: %v", err)
	}
	if err := store.UpdateEpic(epic.ID, project.ID, "Epic Updated", "updated"); err != nil {
		t.Fatalf("UpdateEpic failed: %v", err)
	}
	epic, err = store.GetEpic(epic.ID)
	if err != nil {
		t.Fatalf("GetEpic after update failed: %v", err)
	}
	if epic.Name != "Epic Updated" {
		t.Fatalf("unexpected updated epic: %+v", epic)
	}

	issue, err = store.CreateIssue(project.ID, epic.ID, "Tracked", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue failed: %v", err)
	}
	if issue.PermissionProfile != PermissionProfileDefault {
		t.Fatalf("expected default issue permission profile, got %q", issue.PermissionProfile)
	}
	if err := store.UpdateIssuePermissionProfile(issue.ID, PermissionProfileFullAccess); err != nil {
		t.Fatalf("UpdateIssuePermissionProfile failed: %v", err)
	}
	issue, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after permission update failed: %v", err)
	}
	if issue.PermissionProfile != PermissionProfileFullAccess {
		t.Fatalf("expected full-access issue permission profile, got %q", issue.PermissionProfile)
	}
	providerIssue, err := store.UpsertProviderIssue(project.ID, &Issue{
		Identifier:       "EXT-1",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-1",
		Title:            "Imported issue",
		State:            StateBacklog,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue failed: %v", err)
	}
	if providerIssue.PermissionProfile != PermissionProfileDefault {
		t.Fatalf("expected imported issue permission profile to remain default, got %q", providerIssue.PermissionProfile)
	}
	if err := store.UpdateIssueWorkflowPhase(issue.ID, WorkflowPhaseReview); err != nil {
		t.Fatalf("UpdateIssueWorkflowPhase failed: %v", err)
	}
	issue, err = store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after phase update failed: %v", err)
	}
	if issue.WorkflowPhase != WorkflowPhaseReview {
		t.Fatalf("expected review phase, got %s", issue.WorkflowPhase)
	}

	for _, kind := range []string{"run_started", "tick", "manual_retry_requested"} {
		if err := store.AppendRuntimeEvent(kind, map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"attempt":    1,
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent(%s) failed: %v", kind, err)
		}
	}
	events, err := store.ListRuntimeEvents(0, 10)
	if err != nil {
		t.Fatalf("ListRuntimeEvents failed: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("expected runtime events, got %d", len(events))
	}
	if events[len(events)-1].Kind != "manual_retry_requested" {
		t.Fatalf("unexpected runtime event ordering: %#v", events)
	}

	if err := store.DeleteEpic(epic.ID); err != nil {
		t.Fatalf("DeleteEpic failed: %v", err)
	}
	if _, err := store.GetEpic(epic.ID); err == nil {
		t.Fatal("expected deleted epic lookup to fail")
	}
}

func TestProviderIssueLookupHelpers(t *testing.T) {
	store := setupTestStore(t)

	project, err := store.CreateProjectWithProvider(
		"Provider Project",
		"",
		"",
		"",
		testProviderKind,
		"stub-proj",
		map[string]interface{}{"project_slug": "stub-proj"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	issueA, err := store.UpsertProviderIssue(project.ID, &Issue{
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-1",
		Identifier:       "EXT-1",
		Title:            "First provider issue",
		State:            StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue issueA: %v", err)
	}
	issueB, err := store.UpsertProviderIssue(project.ID, &Issue{
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-2",
		Identifier:       "EXT-2",
		Title:            "   ",
		State:            StateDone,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue issueB: %v", err)
	}

	hasProviderIssues, err := store.HasProviderIssues(project.ID, " stub ")
	if err != nil {
		t.Fatalf("HasProviderIssues existing project: %v", err)
	}
	if !hasProviderIssues {
		t.Fatal("expected provider issues to be detected for normalized provider kind")
	}
	hasProviderIssues, err = store.HasProviderIssues("missing-project", testProviderKind)
	if err != nil {
		t.Fatalf("HasProviderIssues missing project: %v", err)
	}
	if hasProviderIssues {
		t.Fatal("expected missing project to report no provider issues")
	}

	titlesByID, titlesByIdentifier, err := store.LookupIssueTitles(
		[]string{issueA.ID, issueA.ID, " ", issueB.ID},
		[]string{"EXT-1", "EXT-1", "", "EXT-2"},
	)
	if err != nil {
		t.Fatalf("LookupIssueTitles: %v", err)
	}
	if len(titlesByID) != 1 || titlesByID[issueA.ID] != "First provider issue" {
		t.Fatalf("expected only non-empty titles by id, got %#v", titlesByID)
	}
	if len(titlesByIdentifier) != 1 || titlesByIdentifier["EXT-1"] != "First provider issue" {
		t.Fatalf("expected only non-empty titles by identifier, got %#v", titlesByIdentifier)
	}

	emptyByID, emptyByIdentifier, err := store.LookupIssueTitles(nil, []string{"", " "})
	if err != nil {
		t.Fatalf("LookupIssueTitles empty inputs: %v", err)
	}
	if len(emptyByID) != 0 || len(emptyByIdentifier) != 0 {
		t.Fatalf("expected empty lookup result for empty inputs, got ids=%#v identifiers=%#v", emptyByID, emptyByIdentifier)
	}
}

func TestReconcileProviderIssuesRemovesDeletedBlockersAndReactivatesCommands(t *testing.T) {
	store := setupTestStore(t)

	project, err := store.CreateProjectWithProvider(
		"Provider Project",
		"",
		"",
		"",
		testProviderKind,
		"stub-proj",
		map[string]interface{}{"project_slug": "stub-proj"},
	)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState: %v", err)
	}

	if err := store.ReconcileProviderIssues(project.ID, testProviderKind, []Issue{
		{
			Identifier:       "EXT-1",
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "ext-1",
			Title:            "Blocker",
			State:            StateReady,
		},
		{
			Identifier:       "EXT-2",
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "ext-2",
			Title:            "Blocked",
			BlockedBy:        []string{"EXT-1"},
			State:            StateReady,
		},
	}); err != nil {
		t.Fatalf("ReconcileProviderIssues initial sync: %v", err)
	}

	blocker, err := store.GetIssueByIdentifier("EXT-1")
	if err != nil {
		t.Fatalf("GetIssueByIdentifier blocker: %v", err)
	}
	blocked, err := store.GetIssueByIdentifier("EXT-2")
	if err != nil {
		t.Fatalf("GetIssueByIdentifier blocked: %v", err)
	}

	firstCommand, err := store.CreateIssueAgentCommandWithRuntimeEvent(
		blocked.ID,
		"Resume implementation after unblock.",
		IssueAgentCommandPending,
		"manual_command_submitted",
		map[string]interface{}{
			"issue_id":   blocked.ID,
			"identifier": blocked.Identifier,
			"phase":      string(blocked.WorkflowPhase),
		},
	)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommandWithRuntimeEvent: %v", err)
	}
	if err := store.UpdateIssueAgentCommandStatus(firstCommand.ID, IssueAgentCommandWaitingForUnblock); err != nil {
		t.Fatalf("UpdateIssueAgentCommandStatus first command: %v", err)
	}
	time.Sleep(time.Millisecond)
	secondCommand, err := store.CreateIssueAgentCommand(blocked.ID, "Run the final check.", IssueAgentCommandWaitingForUnblock)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand second command: %v", err)
	}

	if err := store.ReconcileProviderIssues(project.ID, testProviderKind, []Issue{
		{
			Identifier:       "EXT-2",
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "ext-2",
			Title:            "Blocked",
			BlockedBy:        []string{blocker.Identifier},
			State:            StateReady,
		},
	}); err != nil {
		t.Fatalf("ReconcileProviderIssues second sync: %v", err)
	}

	if _, err := store.GetIssueByIdentifier("EXT-1"); !IsNotFound(err) {
		t.Fatalf("expected blocker issue to be deleted, got %v", err)
	}

	updated, err := store.GetIssue(blocked.ID)
	if err != nil {
		t.Fatalf("GetIssue blocked after reconcile delete: %v", err)
	}
	if len(updated.BlockedBy) != 0 {
		t.Fatalf("expected blocked issue to have no blockers after reconcile delete, got %#v", updated.BlockedBy)
	}

	detail, err := store.GetIssueDetailByIdentifier(blocked.Identifier)
	if err != nil {
		t.Fatalf("GetIssueDetailByIdentifier blocked after reconcile delete: %v", err)
	}
	if detail.IsBlocked {
		t.Fatalf("expected issue detail to report unblocked after reconcile delete, got %#v", detail)
	}

	pending, err := store.ListPendingIssueAgentCommands(blocked.ID)
	if err != nil {
		t.Fatalf("ListPendingIssueAgentCommands after reconcile delete: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected two pending commands after reconcile delete, got %#v", pending)
	}
	if pending[0].ID != firstCommand.ID || pending[1].ID != secondCommand.ID {
		t.Fatalf("expected pending commands in creation order, got %#v", pending)
	}
}

func TestCreateProjectPersistsLegacyWorkflowFullAccessProfile(t *testing.T) {
	store := setupTestStore(t)

	repoPath := t.TempDir()
	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte(`---
tracker:
  kind: kanban
codex:
  thread_sandbox: danger-full-access
  turn_sandbox_policy:
    type: dangerFullAccess
---
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	project, err := store.CreateProject("Project", "", repoPath, workflowPath)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if project.PermissionProfile != PermissionProfileFullAccess {
		t.Fatalf("expected persisted full-access permission profile, got %q", project.PermissionProfile)
	}

	var stored string
	if err := store.db.QueryRow(`SELECT permission_profile FROM projects WHERE id = ?`, project.ID).Scan(&stored); err != nil {
		t.Fatalf("query permission_profile: %v", err)
	}
	if stored != string(PermissionProfileFullAccess) {
		t.Fatalf("expected persisted full-access permission profile, got %q", stored)
	}
}

func TestNewStoreBackfillsLegacyWorkflowFullAccessProfile(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy.db")
	repoPath := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("MkdirAll repo: %v", err)
	}
	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte(`---
tracker:
  kind: kanban
codex:
  thread_sandbox: danger-full-access
  turn_sandbox_policy:
    type: dangerFullAccess
---
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			state TEXT NOT NULL DEFAULT 'stopped',
			permission_profile TEXT NOT NULL DEFAULT 'default',
			repo_path TEXT NOT NULL DEFAULT '',
			workflow_path TEXT NOT NULL DEFAULT '',
			provider_kind TEXT NOT NULL DEFAULT 'kanban',
			provider_project_ref TEXT NOT NULL DEFAULT '',
			provider_config_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
	); err != nil {
		t.Fatalf("create projects table: %v", err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`
		INSERT INTO projects (id, name, description, state, permission_profile, repo_path, workflow_path, provider_kind, provider_project_ref, provider_config_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"proj-legacy", "Legacy", "", ProjectStateStopped, PermissionProfileDefault, repoPath, workflowPath, ProviderKindKanban, "", "{}", now, now,
	); err != nil {
		t.Fatalf("insert legacy project: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var stored string
	if err := store.db.QueryRow(`SELECT permission_profile FROM projects WHERE id = ?`, "proj-legacy").Scan(&stored); err != nil {
		t.Fatalf("query permission_profile: %v", err)
	}
	if stored != string(PermissionProfileFullAccess) {
		t.Fatalf("expected startup backfill to persist full-access, got %q", stored)
	}
}

func TestGetProjectDoesNotBackfillLegacyProfileDuringRead(t *testing.T) {
	store := setupTestStore(t)

	repoPath := t.TempDir()
	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte(`---
tracker:
  kind: kanban
codex:
  thread_sandbox: danger-full-access
  turn_sandbox_policy:
    type: dangerFullAccess
---
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	now := time.Now().UTC()
	if _, err := store.db.Exec(`
		INSERT INTO projects (id, name, description, state, permission_profile, repo_path, workflow_path, provider_kind, provider_project_ref, provider_config_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"proj-manual", "Manual", "", ProjectStateStopped, PermissionProfileDefault, repoPath, workflowPath, ProviderKindKanban, "", "{}", now, now,
	); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	before, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq before read: %v", err)
	}
	project, err := store.GetProject("proj-manual")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if project.PermissionProfile != PermissionProfileDefault {
		t.Fatalf("expected read path to preserve stored profile, got %q", project.PermissionProfile)
	}
	after, err := store.LatestChangeSeq()
	if err != nil {
		t.Fatalf("LatestChangeSeq after read: %v", err)
	}
	if after != before {
		t.Fatalf("expected project reads to avoid mutating change state, before=%d after=%d", before, after)
	}
}

func TestGetProjectSkipsLegacyBackfillWithoutRepoPath(t *testing.T) {
	store := setupTestStore(t)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	tmpDir := t.TempDir()
	workflowPath := filepath.Join(tmpDir, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte(`---
tracker:
  kind: kanban
codex:
  thread_sandbox: danger-full-access
  turn_sandbox_policy:
    type: dangerFullAccess
---
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	project, err := store.CreateProject("Project", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	project, err = store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if project.PermissionProfile != PermissionProfileDefault {
		t.Fatalf("expected unbound project to remain on default permission profile, got %q", project.PermissionProfile)
	}
}

func TestCreateProjectIgnoresLegacyWorkflowParseErrorsDuringPermissionDetection(t *testing.T) {
	store := setupTestStore(t)

	repoPath := t.TempDir()
	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte(`---
codex:
  thread_sandbox: [danger-full-access
---
`), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	project, err := store.CreateProject("Project", "", repoPath, workflowPath)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if project.PermissionProfile != PermissionProfileDefault {
		t.Fatalf("expected malformed legacy workflow to leave permission profile unchanged, got %q", project.PermissionProfile)
	}
}

func TestHelperUtilities(t *testing.T) {
	if min(2, 5) != 2 || min(9, 3) != 3 {
		t.Fatal("expected min helper to pick the smaller value")
	}
	if asInt(4) != 4 || asInt(int64(5)) != 5 || asInt(float64(3)) != 3 {
		t.Fatal("expected asInt helper to decode common numeric forms")
	}
}
