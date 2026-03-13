package kanban

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

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

	if got := DefaultCapabilities(ProviderKindLinear); got.Epics {
		t.Fatalf("expected linear provider epics=false, got %#v", got)
	}
	if got := DefaultCapabilities(" custom "); !got.Epics || !got.IssueDelete {
		t.Fatalf("expected default capabilities for custom provider, got %#v", got)
	}

	if got := normalizeProviderKind(""); got != ProviderKindKanban {
		t.Fatalf("expected default provider kind kanban, got %q", got)
	}
	if got := normalizeProviderKind(" LINEAR "); got != ProviderKindLinear {
		t.Fatalf("expected normalized linear kind, got %q", got)
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
	project, err := store.CreateProjectWithProvider("Linear Project", "desc", repoDir, "", ProviderKindLinear, "LIN-PROJ", originalConfig)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider failed: %v", err)
	}
	if project.ProviderKind != ProviderKindLinear || project.ProviderProjectRef != "LIN-PROJ" {
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

	if err := store.UpdateProjectWithProvider("missing", "Missing", "", repoDir, "", ProviderKindLinear, "MISS", nil); err == nil {
		t.Fatal("expected missing project update to fail")
	}
	if err := invalidPhaseError(WorkflowPhase("bogus")); !IsValidation(err) {
		t.Fatalf("expected invalidPhaseError to be validation-classified, got %v", err)
	}
}

func TestProviderIssueLifecycle(t *testing.T) {
	store := setupTestStore(t)
	project, err := store.CreateProjectWithProvider("Provider Project", "", "", "", ProviderKindLinear, "LIN-PROJ", nil)
	if err != nil {
		t.Fatalf("CreateProjectWithProvider failed: %v", err)
	}

	if _, err := store.UpsertProviderIssue("", nil); err == nil {
		t.Fatal("expected nil provider issue to fail")
	}
	if _, err := store.UpsertProviderIssue("", &Issue{ProviderKind: ProviderKindLinear, ProviderIssueRef: "LIN-0"}); err == nil {
		t.Fatal("expected empty projectID to fail")
	}
	if _, err := store.UpsertProviderIssue(project.ID, &Issue{ProviderKind: ProviderKindKanban}); err == nil {
		t.Fatal("expected missing provider issue ref to fail")
	}

	incoming := &Issue{
		Identifier:       "LIN-1",
		ProviderKind:     ProviderKindLinear,
		ProviderIssueRef: "LIN-1",
		Title:            "Imported one",
		Description:      "desc",
		State:            StateInReview,
		Priority:         2,
		Labels:           []string{"sync", "provider"},
		BlockedBy:        []string{"LIN-2"},
	}
	created, err := store.UpsertProviderIssue(project.ID, incoming)
	if err != nil {
		t.Fatalf("UpsertProviderIssue create failed: %v", err)
	}
	if !created.ProviderShadow || created.ProviderKind != ProviderKindLinear || created.ProviderIssueRef != "LIN-1" {
		t.Fatalf("unexpected created provider issue: %#v", created)
	}
	if created.LastSyncedAt == nil {
		t.Fatal("expected provider issue last_synced_at to be set")
	}

	lookedUp, err := store.GetIssueByProviderRef(" linear ", " LIN-1 ")
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
		Identifier:       "LIN-2",
		ProviderKind:     ProviderKindLinear,
		ProviderIssueRef: "LIN-2",
		Title:            "Imported two",
		State:            StateBacklog,
	}
	secondCreated, err := store.UpsertProviderIssue(project.ID, second)
	if err != nil {
		t.Fatalf("UpsertProviderIssue second create failed: %v", err)
	}

	updateIncoming := &Issue{
		Identifier:       "LIN-1",
		ProviderKind:     ProviderKindLinear,
		ProviderIssueRef: "LIN-1",
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

	filtered, err := store.ListIssues(map[string]interface{}{"provider_kind": ProviderKindLinear})
	if err != nil {
		t.Fatalf("ListIssues provider filter failed: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 provider-filtered issues, got %d", len(filtered))
	}

	if err := store.DeleteProviderIssuesExcept(project.ID, ProviderKindLinear, []string{"LIN-1", " "}); err != nil {
		t.Fatalf("DeleteProviderIssuesExcept failed: %v", err)
	}
	if _, err := store.GetIssue(secondCreated.ID); err == nil {
		t.Fatal("expected stale provider issue to be deleted")
	} else if !IsNotFound(err) {
		t.Fatalf("expected deleted provider issue to be not found, got %v", err)
	}
	if _, err := store.GetIssueByProviderRef(ProviderKindLinear, "LIN-2"); err != sql.ErrNoRows {
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
