package kanban

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/appserver"
)

const testProviderKind = "stub"

func TestProviderProjectHelpersAndCounts(t *testing.T) {
	repoDir := t.TempDir()
	workflowPath := filepath.Join(repoDir, "WORKFLOW.md")
	if err := os.WriteFile(workflowPath, []byte("---\ntracker:\n  kind: kanban\n---\n"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	counts := IssueStateCounts{}
	for _, state := range []State{StateBacklog, StateReady, StateInProgress, StateInReview, StateDone, StateCancelled} {
		counts.Add(state)
	}
	if counts.Total() != 6 {
		t.Fatalf("expected total 6, got %d", counts.Total())
	}
	if counts.Active() != 3 {
		t.Fatalf("expected active 3, got %d", counts.Active())
	}

	if got := DefaultCapabilities(testProviderKind); !got.Epics || !got.IssueDelete {
		t.Fatalf("expected default capabilities for stub to match local, got %#v", got)
	}
	if got := DefaultCapabilities(" custom "); !got.Epics || !got.IssueDelete {
		t.Fatalf("expected default capabilities for custom provider, got %#v", got)
	}

	if got := normalizeProviderKind(""); got != ProviderKindKanban {
		t.Fatalf("expected default provider kind kanban, got %q", got)
	}
	if got := normalizeProviderKind(" STUB "); got != testProviderKind {
		t.Fatalf("expected normalized stub kind, got %q", got)
	}
	if got := normalizeProviderKind("Asana"); got != "asana" {
		t.Fatalf("expected custom provider kind lower-cased, got %q", got)
	}

	originalConfig := map[string]interface{}{"active_states": []interface{}{"todo", "doing", " "}, "terminal_states": []string{"done", "canceled"}}
	cloned := cloneProviderConfig(originalConfig)
	cloned["extra"] = true
	if _, ok := originalConfig["extra"]; ok {
		t.Fatal("expected cloneProviderConfig to return a copy")
	}
	if got := cloneProviderConfig(nil); len(got) != 0 {
		t.Fatalf("expected empty config clone for nil input, got %#v", got)
	}

	if got := decodeProviderConfig(""); len(got) != 0 {
		t.Fatalf("expected empty decoded config for blank input, got %#v", got)
	}
	if got := decodeProviderConfig("{"); len(got) != 0 {
		t.Fatalf("expected empty decoded config for invalid JSON, got %#v", got)
	}
	decoded := decodeProviderConfig(`{"active_states":["todo","doing"]}`)
	if !reflect.DeepEqual(decoded["active_states"], []interface{}{"todo", "doing"}) {
		t.Fatalf("unexpected decoded config: %#v", decoded)
	}
	if got := encodeProviderConfig(nil); got != "{}" {
		t.Fatalf("expected empty encoded config, got %q", got)
	}
	if got := encodeProviderConfig(map[string]interface{}{"k": "v"}); got != `{"k":"v"}` {
		t.Fatalf("unexpected encoded config: %q", got)
	}

	if got := interfaceSliceToStrings([]string{" a ", "", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("unexpected []string conversion: %#v", got)
	}
	if got := interfaceSliceToStrings([]interface{}{" x ", 42, ""}); !reflect.DeepEqual(got, []string{"x", "42"}) {
		t.Fatalf("unexpected []interface{} conversion: %#v", got)
	}
	if got := interfaceSliceToStrings("nope"); got != nil {
		t.Fatalf("expected nil conversion for scalar input, got %#v", got)
	}

	store := setupTestStore(t)
	project, err := store.CreateProjectWithProvider("Stub Project", "desc", repoDir, "", testProviderKind, "STUB-PROJ", originalConfig)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider failed: %v", err)
	}
	if project.ProviderKind != testProviderKind || project.ProviderProjectRef != "STUB-PROJ" {
		t.Fatalf("unexpected provider project fields: %#v", project)
	}
	if project.State != ProjectStateStopped {
		t.Fatalf("expected provider project state stopped, got %#v", project.State)
	}
	if !project.OrchestrationReady || !project.DispatchReady {
		t.Fatalf("expected provider project to be runnable, got %#v", project)
	}
	if !reflect.DeepEqual(projectDefaultActiveStates(*project), []string{"todo", "doing"}) {
		t.Fatalf("unexpected provider active states: %#v", projectDefaultActiveStates(*project))
	}
	if !reflect.DeepEqual(projectDefaultTerminalStates(*project), []string{"done", "canceled"}) {
		t.Fatalf("unexpected provider terminal states: %#v", projectDefaultTerminalStates(*project))
	}

	if err := store.UpdateProjectWithProvider(project.ID, "Custom Project", "desc2", repoDir, "", "Asana", "ASA-1", map[string]interface{}{"terminal_states": []interface{}{"closed"}}); err != nil {
		t.Fatalf("UpdateProjectWithProvider failed: %v", err)
	}
	reloaded, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if reloaded.ProviderKind != "asana" || reloaded.ProviderProjectRef != "ASA-1" {
		t.Fatalf("unexpected updated provider project: %#v", reloaded)
	}
	if !reflect.DeepEqual(projectDefaultTerminalStates(*reloaded), []string{"closed"}) {
		t.Fatalf("unexpected updated terminal states: %#v", projectDefaultTerminalStates(*reloaded))
	}

	if err := store.UpdateProjectWithProvider("missing", "Missing", "", repoDir, "", testProviderKind, "MISS", nil); err == nil {
		t.Fatal("expected missing project update to fail")
	}
	if err := invalidPhaseError(WorkflowPhase("bogus")); !IsValidation(err) {
		t.Fatalf("expected invalidPhaseError to be validation-classified, got %v", err)
	}
}

func TestProviderIssueLifecycle(t *testing.T) {
	store := setupTestStore(t)
	project, err := store.CreateProjectWithProvider("Provider Project", "", "", "", testProviderKind, "STUB-PROJ", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider failed: %v", err)
	}

	if _, err := store.UpsertProviderIssue("", nil); err == nil {
		t.Fatal("expected nil provider issue to fail")
	}
	if _, err := store.UpsertProviderIssue("", &Issue{ProviderKind: testProviderKind, ProviderIssueRef: "STUB-0"}); err == nil {
		t.Fatal("expected empty projectID to fail")
	}
	if _, err := store.UpsertProviderIssue(project.ID, &Issue{ProviderKind: ProviderKindKanban}); err == nil {
		t.Fatal("expected missing provider issue ref to fail")
	}

	incoming := &Issue{
		Identifier:       "EXT-1",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-1",
		Title:            "Imported one",
		Description:      "desc",
		State:            StateInReview,
		Priority:         2,
		Labels:           []string{"sync", "provider"},
		BlockedBy:        []string{"EXT-2"},
	}
	created, err := store.UpsertProviderIssue(project.ID, incoming)
	if err != nil {
		t.Fatalf("UpsertProviderIssue create failed: %v", err)
	}
	if !created.ProviderShadow || created.ProviderKind != testProviderKind || created.ProviderIssueRef != "ext-1" {
		t.Fatalf("unexpected created provider issue: %#v", created)
	}
	if created.LastSyncedAt == nil {
		t.Fatal("expected provider issue last_synced_at to be set")
	}

	lookedUp, err := store.GetIssueByProviderRef(" stub ", " ext-1 ")
	if err != nil {
		t.Fatalf("GetIssueByProviderRef failed: %v", err)
	}
	if lookedUp.ID != created.ID {
		t.Fatalf("expected provider ref lookup to return created issue, got %#v", lookedUp)
	}

	syncedAt := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)
	if err := store.UpdateProviderIssueState(created.ID, StateDone, WorkflowPhaseComplete, &syncedAt); err != nil {
		t.Fatalf("UpdateProviderIssueState failed: %v", err)
	}
	updatedState, err := store.GetIssue(created.ID)
	if err != nil {
		t.Fatalf("GetIssue after provider state update failed: %v", err)
	}
	if updatedState.State != StateDone || updatedState.WorkflowPhase != WorkflowPhaseComplete {
		t.Fatalf("unexpected provider state update result: %#v", updatedState)
	}
	if updatedState.LastSyncedAt == nil || !updatedState.LastSyncedAt.Equal(syncedAt) {
		t.Fatalf("expected provider syncedAt to round-trip, got %#v", updatedState.LastSyncedAt)
	}

	second := &Issue{
		Identifier:       "EXT-2",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-2",
		Title:            "Imported two",
		State:            StateBacklog,
	}
	secondCreated, err := store.UpsertProviderIssue(project.ID, second)
	if err != nil {
		t.Fatalf("UpsertProviderIssue second create failed: %v", err)
	}

	updateIncoming := &Issue{
		Identifier:       "EXT-1",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-1",
		Title:            "Imported one updated",
		Description:      "updated",
		State:            StateCancelled,
		Priority:         1,
		Labels:           []string{"updated"},
		BlockedBy:        []string{second.Identifier},
		UpdatedAt:        time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC),
		LastSyncedAt:     &syncedAt,
	}
	updated, err := store.UpsertProviderIssue(project.ID, updateIncoming)
	if err != nil {
		t.Fatalf("UpsertProviderIssue update failed: %v", err)
	}
	if updated.ID != created.ID || updated.Title != "Imported one updated" || updated.Priority != 1 {
		t.Fatalf("unexpected provider issue update result: %#v", updated)
	}
	if !reflect.DeepEqual(updated.Labels, []string{"updated"}) {
		t.Fatalf("expected labels to be replaced, got %#v", updated.Labels)
	}
	if !reflect.DeepEqual(updated.BlockedBy, []string{second.Identifier}) {
		t.Fatalf("expected blockers to be replaced, got %#v", updated.BlockedBy)
	}

	filtered, err := store.ListIssues(map[string]interface{}{"provider_kind": testProviderKind})
	if err != nil {
		t.Fatalf("ListIssues provider filter failed: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 provider-filtered issues, got %d", len(filtered))
	}

	if err := store.DeleteProviderIssuesExcept(project.ID, testProviderKind, []string{"ext-1", " "}); err != nil {
		t.Fatalf("DeleteProviderIssuesExcept failed: %v", err)
	}
	if _, err := store.GetIssue(secondCreated.ID); err == nil {
		t.Fatal("expected stale provider issue to be deleted")
	} else if !IsNotFound(err) {
		t.Fatalf("expected deleted provider issue to be not found, got %v", err)
	}
	if _, err := store.GetIssueByProviderRef(testProviderKind, "ext-2"); err != sql.ErrNoRows {
		t.Fatalf("expected deleted provider ref lookup to return sql.ErrNoRows, got %v", err)
	}

	if err := store.UpdateProviderIssueState(created.ID, State(""), WorkflowPhase(""), nil); err == nil {
		t.Fatal("expected blank provider state update to fail")
	}
	if err := store.UpdateProviderIssueState(created.ID, StateReady, WorkflowPhase("invalid"), nil); err != nil {
		t.Fatalf("expected invalid provider phase to fall back, got %v", err)
	}
	afterFallback, err := store.GetIssue(created.ID)
	if err != nil {
		t.Fatalf("GetIssue after fallback update failed: %v", err)
	}
	if afterFallback.WorkflowPhase != WorkflowPhaseComplete {
		t.Fatalf("expected fallback provider phase to preserve current phase, got %#v", afterFallback)
	}
}

func TestUpsertProviderIssuePrunesStaleShadowData(t *testing.T) {
	store := setupTestStore(t)
	project, err := store.CreateProjectWithProvider("Provider Project", "", "", "", testProviderKind, "STUB-PROJ", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider failed: %v", err)
	}

	keep, err := store.UpsertProviderIssue(project.ID, &Issue{
		Identifier:       "EXT-KEEP",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-keep",
		Title:            "Keep me",
		State:            StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue keep failed: %v", err)
	}
	stale, err := store.UpsertProviderIssue(project.ID, &Issue{
		Identifier:       "EXT-STALE",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-stale",
		Title:            "Stale issue",
		State:            StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue stale failed: %v", err)
	}

	workspacePath := filepath.Join(t.TempDir(), "workspace")
	if _, err := store.CreateWorkspace(stale.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace stale failed: %v", err)
	}
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	asset, err := store.CreateIssueAsset(stale.ID, "preview.png", bytes.NewReader([]byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}))
	if err != nil {
		t.Fatalf("CreateIssueAsset stale failed: %v", err)
	}
	commentDir := t.TempDir()
	commentAttachmentPath := filepath.Join(commentDir, "stale.txt")
	if err := os.WriteFile(commentAttachmentPath, []byte("stale comment"), 0o644); err != nil {
		t.Fatalf("WriteFile stale attachment: %v", err)
	}
	commentBody := "Stale issue comment"
	comment, err := store.CreateIssueComment(stale.ID, IssueCommentInput{
		Body: &commentBody,
		Attachments: []IssueCommentAttachmentInput{{
			Path: commentAttachmentPath,
		}},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment stale failed: %v", err)
	}
	if _, err := store.CreateIssueAgentCommand(stale.ID, "Stale follow-up", IssueAgentCommandPending); err != nil {
		t.Fatalf("CreateIssueAgentCommand stale failed: %v", err)
	}
	requestedAt := time.Date(2026, 3, 18, 12, 15, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApprovalWithContext(stale, "Stale plan", requestedAt, 2, "thread-stale", "turn-stale"); err != nil {
		t.Fatalf("SetIssuePendingPlanApprovalWithContext stale failed: %v", err)
	}

	syncedAt := time.Date(2026, 3, 18, 12, 30, 0, 0, time.UTC)
	updatedKeep, err := store.UpsertProviderIssue(project.ID, &Issue{
		Identifier:       keep.Identifier,
		ProviderKind:     testProviderKind,
		ProviderIssueRef: keep.ProviderIssueRef,
		Title:            "Keep me updated",
		Description:      "refreshed",
		State:            StateDone,
		Labels:           []string{"synced"},
		UpdatedAt:        time.Date(2026, 3, 18, 11, 0, 0, 0, time.UTC),
		LastSyncedAt:     &syncedAt,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue keep update failed: %v", err)
	}
	if updatedKeep.ID != keep.ID {
		t.Fatalf("expected keep issue to be updated in place, got %#v", updatedKeep)
	}

	if err := store.DeleteProviderIssuesExcept(project.ID, testProviderKind, []string{keep.ProviderIssueRef}); err != nil {
		t.Fatalf("DeleteProviderIssuesExcept stale cleanup failed: %v", err)
	}
	if _, err := store.GetIssue(stale.ID); !IsNotFound(err) {
		t.Fatalf("expected stale provider issue to be deleted, got %v", err)
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale workspace to be removed, got %v", err)
	}
	if _, err := store.GetIssueAsset(stale.ID, asset.ID); !IsNotFound(err) {
		t.Fatalf("expected stale asset to be deleted, got %v", err)
	}
	if _, _, err := store.GetIssueCommentAttachmentContent(stale.ID, comment.ID, comment.Attachments[0].ID); !IsNotFound(err) {
		t.Fatalf("expected stale comment attachment to be deleted, got %v", err)
	}
	if commands, err := store.ListIssueAgentCommands(stale.ID); err != nil || len(commands) != 0 {
		t.Fatalf("expected stale issue commands to be deleted, got %#v err=%v", commands, err)
	}
}

func TestUpsertProviderIssueNormalizesWorkflowPhaseAndUpdatedAt(t *testing.T) {
	store := setupTestStore(t)
	project, err := store.CreateProjectWithProvider("Provider Project", "", "", "", testProviderKind, "STUB-PROJ", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider failed: %v", err)
	}

	doneIssue, err := store.UpsertProviderIssue(project.ID, &Issue{
		Identifier:       "EXT-DONE",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-done",
		Title:            "Imported done",
		State:            StateDone,
		WorkflowPhase:    WorkflowPhaseDone,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue done failed: %v", err)
	}
	if doneIssue.WorkflowPhase != WorkflowPhaseDone {
		t.Fatalf("expected explicit done phase to be preserved, got %#v", doneIssue)
	}

	reviewIssue, err := store.UpsertProviderIssue(project.ID, &Issue{
		Identifier:       "EXT-REVIEW",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-review",
		Title:            "Imported review",
		State:            StateInReview,
		WorkflowPhase:    WorkflowPhaseReview,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue review failed: %v", err)
	}
	if reviewIssue.WorkflowPhase != WorkflowPhaseReview {
		t.Fatalf("expected explicit review phase to be preserved, got %#v", reviewIssue)
	}

	derivedIssue, err := store.UpsertProviderIssue(project.ID, &Issue{
		Identifier:       "EXT-DERIVED",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-derived",
		Title:            "Imported derived",
		State:            StateCancelled,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue derived failed: %v", err)
	}
	if derivedIssue.WorkflowPhase != WorkflowPhaseComplete {
		t.Fatalf("expected cancelled provider issue to derive complete phase, got %#v", derivedIssue)
	}

	refreshedReview, err := store.UpsertProviderIssue(project.ID, &Issue{
		Identifier:       "EXT-REVIEW",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-review",
		Title:            "Imported review refreshed",
		State:            StateInReview,
		WorkflowPhase:    WorkflowPhaseReview,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue refreshed review failed: %v", err)
	}
	if refreshedReview.WorkflowPhase != WorkflowPhaseReview {
		t.Fatalf("expected refreshed provider issue to retain review phase, got %#v", refreshedReview)
	}
	if refreshedReview.UpdatedAt.IsZero() {
		t.Fatalf("expected zero UpdatedAt payload to be normalized on update, got %#v", refreshedReview)
	}
}

func TestReconcileProviderIssuesBatchesUpdatesPreservesLocalFieldsAndPrunesStaleData(t *testing.T) {
	store := setupTestStore(t)
	project, err := store.CreateProjectWithProvider("Provider Project", "", "", "", testProviderKind, "STUB-PROJ", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider failed: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState failed: %v", err)
	}

	keep, err := store.UpsertProviderIssue(project.ID, &Issue{
		Identifier:       "EXT-KEEP",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-keep",
		Title:            "Old title",
		State:            StateBacklog,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue keep failed: %v", err)
	}
	if err := store.UpdateIssue(keep.ID, map[string]interface{}{
		"agent_name":   "codex",
		"agent_prompt": "preserve",
		"branch_name":  "codex/EXT-KEEP",
		"pr_url":       "https://example.com/pr/1",
	}); err != nil {
		t.Fatalf("UpdateIssue keep failed: %v", err)
	}

	stale, err := store.UpsertProviderIssue(project.ID, &Issue{
		Identifier:       "EXT-STALE",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-stale",
		Title:            "Stale issue",
		State:            StateReady,
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue stale failed: %v", err)
	}
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	if _, err := store.CreateWorkspace(stale.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace stale failed: %v", err)
	}
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	if _, err := store.CreateIssueAsset(stale.ID, "preview.png", bytes.NewReader([]byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	})); err != nil {
		t.Fatalf("CreateIssueAsset stale failed: %v", err)
	}
	plannedAt := time.Date(2026, 3, 18, 12, 15, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApprovalWithContext(stale, "Stale plan", plannedAt, 2, "thread-stale", "turn-stale"); err != nil {
		t.Fatalf("SetIssuePendingPlanApprovalWithContext stale failed: %v", err)
	}
	if err := store.SetIssuePendingPlanRevision(stale.ID, "Stale revision", plannedAt.Add(time.Minute)); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision stale failed: %v", err)
	}
	if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
		IssueID:    stale.ID,
		Identifier: stale.Identifier,
		Phase:      string(WorkflowPhaseImplementation),
		Attempt:    1,
		RunKind:    "run_started",
		UpdatedAt:  plannedAt,
		AppSession: appserver.Session{
			IssueID:         stale.ID,
			IssueIdentifier: stale.Identifier,
			SessionID:       "session-stale",
			ThreadID:        "thread-stale",
			TurnID:          "turn-stale",
			LastEvent:       "turn.started",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession stale failed: %v", err)
	}
	if err := store.ApplyIssueActivityEvent(stale.ID, stale.Identifier, 1, agentruntime.ActivityEvent{
		Type:     "turn.started",
		ThreadID: "thread-stale",
		TurnID:   "turn-stale",
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent started stale failed: %v", err)
	}
	if err := store.ApplyIssueActivityEvent(stale.ID, stale.Identifier, 1, agentruntime.ActivityEvent{
		Type:     "turn.completed",
		ThreadID: "thread-stale",
		TurnID:   "turn-stale",
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent completed stale failed: %v", err)
	}
	commentDir := t.TempDir()
	commentAttachmentPath := filepath.Join(commentDir, "stale.txt")
	if err := os.WriteFile(commentAttachmentPath, []byte("stale comment"), 0o644); err != nil {
		t.Fatalf("WriteFile stale attachment: %v", err)
	}
	commentBody := "Stale issue comment"
	comment, err := store.CreateIssueComment(stale.ID, IssueCommentInput{
		Body: &commentBody,
		Attachments: []IssueCommentAttachmentInput{{
			Path: commentAttachmentPath,
		}},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment stale failed: %v", err)
	}
	replyBody := "Stale issue reply"
	if _, err := store.CreateIssueComment(stale.ID, IssueCommentInput{
		Body:            &replyBody,
		ParentCommentID: comment.ID,
	}); err != nil {
		t.Fatalf("CreateIssueComment reply stale failed: %v", err)
	}
	if _, err := store.CreateIssueAgentCommand(stale.ID, "Stale follow-up", IssueAgentCommandPending); err != nil {
		t.Fatalf("CreateIssueAgentCommand stale failed: %v", err)
	}
	blockedIssue, err := store.CreateIssue(project.ID, "", "Blocked by stale", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocked failed: %v", err)
	}
	if err := store.UpdateIssueState(blockedIssue.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocked failed: %v", err)
	}
	if _, err := store.SetIssueBlockers(blockedIssue.ID, []string{stale.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers blocked failed: %v", err)
	}
	blockedCommand, err := store.CreateIssueAgentCommand(blockedIssue.ID, "Resume after stale deletion.", IssueAgentCommandWaitingForUnblock)
	if err != nil {
		t.Fatalf("CreateIssueAgentCommand blocked failed: %v", err)
	}
	nextRunAt := plannedAt.Add(2 * time.Hour)
	if _, err := store.db.Exec(`
		INSERT INTO issue_recurrences (issue_id, cron, enabled, next_run_at, last_enqueued_at, pending_rerun, created_at, updated_at)
		VALUES (?, ?, 1, ?, ?, 0, ?, ?)`,
		stale.ID,
		"0 * * * *",
		nextRunAt,
		plannedAt,
		plannedAt,
		plannedAt,
	); err != nil {
		t.Fatalf("insert stale recurrence: %v", err)
	}

	lastSyncedAt := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	if err := store.ReconcileProviderIssues(project.ID, testProviderKind, []Issue{
		{
			Identifier:       "EXT-KEEP",
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "ext-keep",
			Title:            "Refreshed title",
			Description:      "Refreshed description",
			State:            StateDone,
			Priority:         1,
			Labels:           []string{"synced"},
			UpdatedAt:        time.Date(2026, 3, 18, 11, 0, 0, 0, time.UTC),
			LastSyncedAt:     &lastSyncedAt,
		},
		{
			Identifier:       "EXT-NEW",
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "ext-new",
			Title:            "New issue",
			State:            StateReady,
			Labels:           []string{"new"},
		},
	}); err != nil {
		t.Fatalf("ReconcileProviderIssues failed: %v", err)
	}

	updatedKeep, err := store.GetIssue(keep.ID)
	if err != nil {
		t.Fatalf("GetIssue keep failed: %v", err)
	}
	if updatedKeep.Title != "Refreshed title" || updatedKeep.Description != "Refreshed description" || updatedKeep.State != StateDone || updatedKeep.Priority != 1 {
		t.Fatalf("expected provider fields to refresh, got %#v", updatedKeep)
	}
	if updatedKeep.AgentName != "codex" || updatedKeep.AgentPrompt != "preserve" || updatedKeep.BranchName != "codex/EXT-KEEP" || updatedKeep.PRURL != "https://example.com/pr/1" {
		t.Fatalf("expected local fields to be preserved, got %#v", updatedKeep)
	}
	if !reflect.DeepEqual(updatedKeep.Labels, []string{"synced"}) {
		t.Fatalf("expected updated labels, got %#v", updatedKeep.Labels)
	}
	if updatedKeep.LastSyncedAt == nil || !updatedKeep.LastSyncedAt.Equal(lastSyncedAt) {
		t.Fatalf("expected last_synced_at to persist, got %#v", updatedKeep.LastSyncedAt)
	}

	if _, err := store.GetIssue(stale.ID); !IsNotFound(err) {
		t.Fatalf("expected stale issue to be removed, got %v", err)
	}
	if _, err := store.GetWorkspace(stale.ID); err == nil {
		t.Fatal("expected stale workspace to be removed")
	}
	if _, err := store.GetIssueComment(stale.ID, comment.ID); !IsNotFound(err) {
		t.Fatalf("expected stale comments to be removed, got %v", err)
	}
	if recurrence, err := store.GetIssueRecurrence(stale.ID); err != nil || recurrence != nil {
		t.Fatalf("expected stale recurrence to be removed, got %#v err=%v", recurrence, err)
	}
	if session, err := store.GetIssueExecutionSession(stale.ID); !errors.Is(err, sql.ErrNoRows) || session != nil {
		t.Fatalf("expected stale execution session to be removed, got %#v err=%v", session, err)
	}
	if activities, err := store.ListIssueActivityEntries(stale.ID); err != nil || len(activities) != 0 {
		t.Fatalf("expected stale activity entries to be removed, got %#v err=%v", activities, err)
	}
	if commands, err := store.ListIssueAgentCommands(stale.ID); err != nil || len(commands) != 0 {
		t.Fatalf("expected stale commands to be removed, got %#v err=%v", commands, err)
	}
	updatedBlocked, err := store.GetIssue(blockedIssue.ID)
	if err != nil {
		t.Fatalf("GetIssue blocked after reconcile failed: %v", err)
	}
	if len(updatedBlocked.BlockedBy) != 0 {
		t.Fatalf("expected blocked issue to be unblocked, got %#v", updatedBlocked.BlockedBy)
	}
	pending, err := store.ListPendingIssueAgentCommands(blockedIssue.ID)
	if err != nil {
		t.Fatalf("ListPendingIssueAgentCommands blocked after reconcile failed: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != blockedCommand.ID {
		t.Fatalf("expected blocked command to be reactivated, got %#v", pending)
	}
	if _, err := store.GetIssueByProviderRef(testProviderKind, "ext-new"); err != nil {
		t.Fatalf("expected new provider issue to be inserted, got %v", err)
	}
}

func TestDispatchIssueStateQueriesReflectProjectAndBlockerStatus(t *testing.T) {
	store := setupTestStore(t)
	project, err := store.CreateProject("Dispatch Project", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState failed: %v", err)
	}
	blocker, err := store.CreateIssue(project.ID, "", "Active blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker failed: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocker failed: %v", err)
	}
	blocked, err := store.CreateIssue(project.ID, "", "Blocked issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocked failed: %v", err)
	}
	if _, err := store.SetIssueBlockers(blocked.ID, []string{blocker.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers failed: %v", err)
	}
	if err := store.UpdateIssueState(blocked.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocked failed: %v", err)
	}

	freeIssue, err := store.CreateIssue("", "", "Free issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue free failed: %v", err)
	}
	if err := store.UpdateIssueState(freeIssue.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState free failed: %v", err)
	}

	dispatchIssues, err := store.ListDispatchIssues([]string{string(StateReady)})
	if err != nil {
		t.Fatalf("ListDispatchIssues failed: %v", err)
	}
	if len(dispatchIssues) != 3 {
		t.Fatalf("expected 3 ready issues, got %d", len(dispatchIssues))
	}

	foundBlocked := false
	foundFree := false
	for _, item := range dispatchIssues {
		switch item.Identifier {
		case blocked.Identifier:
			foundBlocked = true
			if !item.DispatchState.ProjectExists || item.DispatchState.ProjectState != ProjectStateRunning || !item.DispatchState.HasUnresolvedBlockers {
				t.Fatalf("unexpected blocked dispatch state: %#v", item.DispatchState)
			}
		case freeIssue.Identifier:
			foundFree = true
			if item.DispatchState.ProjectExists || item.DispatchState.ProjectState != ProjectStateStopped || item.DispatchState.HasUnresolvedBlockers {
				t.Fatalf("unexpected free issue dispatch state: %#v", item.DispatchState)
			}
		}
	}
	if !foundBlocked || !foundFree {
		t.Fatalf("expected blocked and free issues in dispatch list, got %#v", dispatchIssues)
	}

	state, err := store.GetIssueDispatchState(blocked.ID)
	if err != nil {
		t.Fatalf("GetIssueDispatchState blocked failed: %v", err)
	}
	if !state.ProjectExists || state.ProjectState != ProjectStateRunning || !state.HasUnresolvedBlockers {
		t.Fatalf("unexpected blocked issue dispatch state: %#v", state)
	}

	freeState, err := store.GetIssueDispatchState(freeIssue.ID)
	if err != nil {
		t.Fatalf("GetIssueDispatchState free failed: %v", err)
	}
	if freeState.ProjectExists || freeState.ProjectState != ProjectStateStopped || freeState.HasUnresolvedBlockers {
		t.Fatalf("unexpected free issue dispatch state: %#v", freeState)
	}
}

func TestDispatchIssueQueryHydratesOptionalFieldsAndRecurrence(t *testing.T) {
	store := setupTestStore(t)
	project, err := store.CreateProject("Dispatch Hydration Project", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if err := store.UpdateProjectState(project.ID, ProjectStateRunning); err != nil {
		t.Fatalf("UpdateProjectState failed: %v", err)
	}
	epic, err := store.CreateEpic(project.ID, "Dispatch Hydration Epic", "")
	if err != nil {
		t.Fatalf("CreateEpic failed: %v", err)
	}
	blocker, err := store.CreateIssue(project.ID, "", "Dispatch blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocker failed: %v", err)
	}
	if err := store.UpdateIssueState(blocker.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocker failed: %v", err)
	}

	providerIssue, err := store.UpsertProviderIssue(project.ID, &Issue{
		Identifier:       "EXT-PROVIDER",
		ProviderKind:     testProviderKind,
		ProviderIssueRef: "ext-provider",
		Title:            "Provider issue",
		Description:      "provider desc",
		State:            StateReady,
		Priority:         2,
		Labels:           []string{"alpha", "beta"},
		BlockedBy:        []string{blocker.Identifier},
	})
	if err != nil {
		t.Fatalf("UpsertProviderIssue failed: %v", err)
	}
	if err := store.UpdateIssue(providerIssue.ID, map[string]interface{}{
		"branch_name": "feature/provider-dispatch",
		"pr_url":      "https://example.com/pr/provider",
	}); err != nil {
		t.Fatalf("UpdateIssue provider metadata failed: %v", err)
	}

	recurringIssue, err := store.CreateIssueWithOptions(project.ID, epic.ID, "Recurring issue", "recurring desc", 3, []string{"recurring"}, IssueCreateOptions{
		IssueType: IssueTypeRecurring,
		Cron:      "*/30 * * * *",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions recurring failed: %v", err)
	}
	if err := store.UpdateIssue(recurringIssue.ID, map[string]interface{}{
		"branch_name": "feature/recurring-dispatch",
		"pr_url":      "https://example.com/pr/recurring",
	}); err != nil {
		t.Fatalf("UpdateIssue recurring metadata failed: %v", err)
	}
	requestedAt := time.Date(2026, 3, 18, 13, 0, 0, 0, time.UTC)
	if err := store.SetIssuePendingPlanApproval(recurringIssue.ID, "Approve the recurring dispatch issue", requestedAt); err != nil {
		t.Fatalf("SetIssuePendingPlanApproval failed: %v", err)
	}
	if err := store.SetIssuePendingPlanRevision(recurringIssue.ID, "Revise the recurring dispatch issue", requestedAt.Add(10*time.Minute)); err != nil {
		t.Fatalf("SetIssuePendingPlanRevision failed: %v", err)
	}
	if err := store.UpdateIssueState(recurringIssue.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState recurring ready failed: %v", err)
	}
	if err := store.UpdateIssueState(recurringIssue.ID, StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState recurring in_progress failed: %v", err)
	}
	if err := store.UpdateIssueState(recurringIssue.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState recurring done failed: %v", err)
	}
	lastSyncedAt := requestedAt.Add(20 * time.Minute)
	nextRunAt := requestedAt.Add(1 * time.Hour)
	if _, err := store.db.Exec(`UPDATE issues SET last_synced_at = ? WHERE id = ?`, lastSyncedAt, recurringIssue.ID); err != nil {
		t.Fatalf("UPDATE issues last_synced_at failed: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE issue_recurrences SET next_run_at = ?, last_enqueued_at = ?, pending_rerun = 1 WHERE issue_id = ?`, nextRunAt, requestedAt, recurringIssue.ID); err != nil {
		t.Fatalf("UPDATE issue_recurrences failed: %v", err)
	}

	dispatchIssues, err := store.ListDispatchIssues([]string{string(StateReady), string(StateDone)})
	if err != nil {
		t.Fatalf("ListDispatchIssues failed: %v", err)
	}

	var providerFound, recurringFound bool
	for _, item := range dispatchIssues {
		switch item.Identifier {
		case providerIssue.Identifier:
			providerFound = true
			if item.ProjectID != project.ID || !item.DispatchState.ProjectExists || item.DispatchState.ProjectState != ProjectStateRunning || !item.DispatchState.HasUnresolvedBlockers {
				t.Fatalf("unexpected provider dispatch state: %#v", item)
			}
			if item.ProviderKind != testProviderKind || item.ProviderIssueRef != "ext-provider" || !item.ProviderShadow {
				t.Fatalf("unexpected provider metadata: %#v", item)
			}
			if !reflect.DeepEqual(item.Labels, []string{"alpha", "beta"}) || !reflect.DeepEqual(item.BlockedBy, []string{blocker.Identifier}) {
				t.Fatalf("expected provider labels and blockers to hydrate, got %#v", item)
			}
			if item.BranchName != "feature/provider-dispatch" || item.PRURL != "https://example.com/pr/provider" {
				t.Fatalf("unexpected provider branch metadata: %#v", item)
			}
		case recurringIssue.Identifier:
			recurringFound = true
			if item.ProjectID != project.ID || item.EpicID != epic.ID {
				t.Fatalf("unexpected recurring dispatch associations: %#v", item)
			}
			if item.State != StateDone || item.WorkflowPhase != WorkflowPhaseComplete {
				t.Fatalf("unexpected recurring dispatch state: %#v", item)
			}
			if item.BranchName != "feature/recurring-dispatch" || item.PRURL != "https://example.com/pr/recurring" {
				t.Fatalf("unexpected recurring branch metadata: %#v", item)
			}
			if item.LastSyncedAt == nil || !item.LastSyncedAt.Equal(lastSyncedAt) {
				t.Fatalf("expected last_synced_at to round-trip, got %#v", item.LastSyncedAt)
			}
			if item.PendingPlanMarkdown == "" || item.PendingPlanRevisionMarkdown == "" || item.PendingPlanRequestedAt == nil || item.PendingPlanRevisionRequestedAt == nil {
				t.Fatalf("expected pending plan metadata to hydrate, got %#v", item)
			}
			if item.Cron != "*/30 * * * *" || !item.Enabled || item.NextRunAt == nil || !item.NextRunAt.Equal(nextRunAt) || item.LastEnqueuedAt == nil || !item.LastEnqueuedAt.Equal(requestedAt) || !item.PendingRerun {
				t.Fatalf("expected recurrence overlay to hydrate, got %#v", item)
			}
		}
	}
	if !providerFound || !recurringFound {
		t.Fatalf("expected provider and recurring issues to be returned, got %#v", dispatchIssues)
	}
}

func TestReconcileProviderIssuesFailureBranches(t *testing.T) {
	const providerKind = "asana"

	t.Run("validation and query failure", func(t *testing.T) {
		store := setupTestStore(t)
		project, err := store.CreateProjectWithProvider("Provider Coverage", "", "", "", providerKind, "ASA-PROJ", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider failed: %v", err)
		}

		if err := store.ReconcileProviderIssues("", providerKind, nil); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected blank project id to fail validation, got %v", err)
		}
		if err := store.ReconcileProviderIssues(project.ID, ProviderKindKanban, nil); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected kanban provider kind to fail validation, got %v", err)
		}
		if err := store.ReconcileProviderIssues(project.ID, providerKind, []Issue{{ProviderKind: "", ProviderIssueRef: "ext-1", Title: "missing kind"}}); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected missing provider kind on incoming issue to fail validation, got %v", err)
		}

		if err := store.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}
		faulty := openFaultyMigratedSQLiteStoreAt(t, filepath.Join(t.TempDir(), "reconcile-query.db"), "select id, project_id, provider_issue_ref, provider_shadow")
		if err := faulty.ReconcileProviderIssues(project.ID, providerKind, nil); err == nil {
			t.Fatal("expected ReconcileProviderIssues to fail when issue query is injected")
		}
	})

	t.Run("update branch failure", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "reconcile-update.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProjectWithProvider("Provider Update Coverage", "", "", "", providerKind, "ASA-UPD", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider failed: %v", err)
		}
		if _, err := base.UpsertProviderIssue(project.ID, &Issue{
			Identifier:       "EXT-1",
			ProviderKind:     providerKind,
			ProviderIssueRef: "ext-1",
			Title:            "Existing provider issue",
			State:            StateBacklog,
		}); err != nil {
			t.Fatalf("UpsertProviderIssue existing failed: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		faulty := openFaultyMigratedSQLiteStoreAt(t, dbPath, "insert into change_events")
		if err := faulty.ReconcileProviderIssues(project.ID, providerKind, []Issue{
			{
				Identifier:       "EXT-1",
				ProviderKind:     providerKind,
				ProviderIssueRef: "ext-1",
				Title:            "Updated provider issue",
				Description:      "updated",
				State:            StateReady,
				Labels:           []string{"synced"},
			},
		}); err == nil {
			t.Fatal("expected update branch to fail when change-events write is injected")
		}
	})

	t.Run("create branch failure", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "reconcile-create.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProjectWithProvider("Provider Create Coverage", "", "", "", providerKind, "ASA-NEW", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider failed: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		faulty := openFaultyMigratedSQLiteStoreAt(t, dbPath, "insert into change_events")
		if err := faulty.ReconcileProviderIssues(project.ID, providerKind, []Issue{
			{
				Identifier:       "EXT-NEW",
				ProviderKind:     providerKind,
				ProviderIssueRef: "ext-new",
				Title:            "New provider issue",
				State:            StateReady,
				Labels:           []string{"fresh"},
			},
		}); err == nil {
			t.Fatal("expected create branch to fail when change-events write is injected")
		}
	})

	t.Run("delete branch failure", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "reconcile-delete.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProjectWithProvider("Provider Delete Coverage", "", "", "", providerKind, "ASA-DEL", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider failed: %v", err)
		}
		if _, err := base.UpsertProviderIssue(project.ID, &Issue{
			Identifier:       "EXT-STALE",
			ProviderKind:     providerKind,
			ProviderIssueRef: "ext-stale",
			Title:            "Stale provider issue",
			State:            StateReady,
		}); err != nil {
			t.Fatalf("UpsertProviderIssue stale failed: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		faulty := openFaultyMigratedSQLiteStoreAt(t, dbPath, "insert into change_events")
		if err := faulty.ReconcileProviderIssues(project.ID, providerKind, nil); err == nil {
			t.Fatal("expected delete branch to fail when change-events write is injected")
		}
	})
}

func TestUpsertProviderIssueFailureBranches(t *testing.T) {
	t.Run("lookup query failure", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "provider-query.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProjectWithProvider("Provider query project", "", "", "", testProviderKind, "QUERY", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		faulty := openFaultySQLiteStoreAt(t, dbPath, "select id from issues where provider_kind = ? and provider_issue_ref = ?")
		if err := faulty.configureConnection(); err != nil {
			t.Fatalf("configureConnection faulty store: %v", err)
		}
		if _, err := faulty.UpsertProviderIssue(project.ID, &Issue{
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "query-1",
			Title:            "Query failure",
			State:            StateReady,
		}); err == nil {
			t.Fatal("expected UpsertProviderIssue to fail when the provider lookup query is injected")
		}
	})

	t.Run("create path rollback", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "provider-create.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProjectWithProvider("Provider create project", "", "", "", testProviderKind, "CREATE", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		faulty := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
		if err := faulty.configureConnection(); err != nil {
			t.Fatalf("configureConnection faulty store: %v", err)
		}
		_, err = faulty.UpsertProviderIssue(project.ID, &Issue{
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "create-1",
			Title:            "Create failure",
			State:            StateReady,
			BlockedBy:        []string{"BLOCKER"},
		})
		if err == nil {
			t.Fatal("expected UpsertProviderIssue to fail on create rollback")
		}
		issues, err := faulty.ListIssues(map[string]interface{}{"project_id": project.ID, "provider_kind": testProviderKind})
		if err != nil {
			t.Fatalf("ListIssues after create failure: %v", err)
		}
		if len(issues) != 0 {
			t.Fatalf("expected create rollback to leave no provider issues, got %#v", issues)
		}
	})

	t.Run("update path rollback", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "provider-update.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProjectWithProvider("Provider update project", "", "", "", testProviderKind, "UPDATE", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider: %v", err)
		}
		existing, err := base.UpsertProviderIssue(project.ID, &Issue{
			Identifier:       "EX-1",
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "update-1",
			Title:            "Original title",
			State:            StateBacklog,
		})
		if err != nil {
			t.Fatalf("UpsertProviderIssue existing: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		faulty := openFaultySQLiteStoreAt(t, dbPath, "delete from issue_blockers")
		if err := faulty.configureConnection(); err != nil {
			t.Fatalf("configureConnection faulty store: %v", err)
		}
		updated, err := faulty.UpsertProviderIssue(project.ID, &Issue{
			Identifier:       "EX-1",
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "update-1",
			Title:            "Updated title",
			State:            StateReady,
			BlockedBy:        []string{"BLOCKER"},
		})
		if err == nil {
			t.Fatal("expected UpsertProviderIssue update to fail")
		}
		if updated != nil {
			t.Fatalf("expected update failure to return nil issue, got %#v", updated)
		}
		loaded, err := faulty.GetIssue(existing.ID)
		if err != nil {
			t.Fatalf("GetIssue after update failure: %v", err)
		}
		if loaded.Title != existing.Title || loaded.State != existing.State {
			t.Fatalf("expected provider issue rollback to preserve original row, got %#v", loaded)
		}
	})

	t.Run("update change-events rollback", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "provider-update-change-events.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProjectWithProvider("Provider update change-events project", "", "", "", testProviderKind, "UPDATE-CHANGE", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider: %v", err)
		}
		existing, err := base.UpsertProviderIssue(project.ID, &Issue{
			Identifier:       "EX-2",
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "update-2",
			Title:            "Original title",
			State:            StateBacklog,
		})
		if err != nil {
			t.Fatalf("UpsertProviderIssue existing: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		faulty := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
		if err := faulty.configureConnection(); err != nil {
			t.Fatalf("configureConnection faulty store: %v", err)
		}
		updated, err := faulty.UpsertProviderIssue(project.ID, &Issue{
			Identifier:       "EX-2",
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "update-2",
			Title:            "Updated title",
			State:            StateReady,
			BlockedBy:        []string{"BLOCKER"},
		})
		if err == nil {
			t.Fatal("expected UpsertProviderIssue update to fail on change-events append")
		}
		if updated != nil {
			t.Fatalf("expected update failure to return nil issue, got %#v", updated)
		}
		loaded, err := faulty.GetIssue(existing.ID)
		if err != nil {
			t.Fatalf("GetIssue after change-events failure: %v", err)
		}
		if loaded.Title != existing.Title || loaded.State != existing.State {
			t.Fatalf("expected provider issue rollback to preserve original row, got %#v", loaded)
		}
	})

	t.Run("stale cleanup failure", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "provider-cleanup.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProjectWithProvider("Provider cleanup project", "", "", "", testProviderKind, "CLEANUP", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider: %v", err)
		}
		keep, err := base.UpsertProviderIssue(project.ID, &Issue{
			Identifier:       "EX-KEEP",
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "cleanup-keep",
			Title:            "Keep issue",
			State:            StateBacklog,
		})
		if err != nil {
			t.Fatalf("UpsertProviderIssue keep: %v", err)
		}
		stale, err := base.UpsertProviderIssue(project.ID, &Issue{
			Identifier:       "EX-STALE",
			ProviderKind:     testProviderKind,
			ProviderIssueRef: "cleanup-stale",
			Title:            "Stale issue",
			State:            StateReady,
		})
		if err != nil {
			t.Fatalf("UpsertProviderIssue stale: %v", err)
		}
		workspacePath := filepath.Join(t.TempDir(), "workspace")
		if _, err := base.CreateWorkspace(stale.ID, workspacePath); err != nil {
			t.Fatalf("CreateWorkspace stale: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		faulty := openFaultySQLiteStoreAt(t, dbPath, "delete from workspaces")
		if err := faulty.configureConnection(); err != nil {
			t.Fatalf("configureConnection faulty store: %v", err)
		}
		if err := faulty.DeleteProviderIssuesExcept(project.ID, testProviderKind, []string{keep.ProviderIssueRef}); err == nil {
			t.Fatal("expected DeleteProviderIssuesExcept to fail when workspace deletion is injected")
		}
		if _, err := faulty.GetIssue(stale.ID); err != nil {
			t.Fatalf("expected stale issue to survive rollback, got %v", err)
		}
		if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
			t.Fatalf("expected stale workspace to remain after cleanup failure, got %v", err)
		}
	})
}
