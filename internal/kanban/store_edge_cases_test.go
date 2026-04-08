package kanban

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/appserver"
)

type countingReader struct {
	remaining int64
}

func TestKanbanCoverageAdditionalRollbackAndDecodeBranches(t *testing.T) {
	newFaultyMigratedStore := func(t *testing.T, failPattern string) *Store {
		t.Helper()
		store := openFaultySQLiteStore(t, failPattern)
		if err := store.configureConnection(); err != nil {
			t.Fatalf("configureConnection: %v", err)
		}
		if err := store.migrate(); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		return store
	}

	t.Run("create issue with options failures", func(t *testing.T) {
		cases := []struct {
			name        string
			failPattern string
			create      func(*Store) error
		}{
			{
				name:        "identifier generation",
				failPattern: "insert into counters",
				create: func(store *Store) error {
					_, err := store.CreateIssue("", "", "Create failure", "", 0, nil)
					return err
				},
			},
			{
				name:        "issue row insert",
				failPattern: "insert into issues",
				create: func(store *Store) error {
					_, err := store.CreateIssue("", "", "Create failure", "", 0, nil)
					return err
				},
			},
			{
				name:        "recurrence insert",
				failPattern: "insert into issue_recurrences",
				create: func(store *Store) error {
					_, err := store.CreateIssueWithOptions("", "", "Recurring create failure", "", 0, nil, IssueCreateOptions{
						IssueType: IssueTypeRecurring,
						Cron:      "0 * * * *",
					})
					return err
				},
			},
			{
				name:        "change event append",
				failPattern: "insert into change_events",
				create: func(store *Store) error {
					_, err := store.CreateIssue("", "", "Change event create failure", "", 0, nil)
					return err
				},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				store := newFaultyMigratedStore(t, tc.failPattern)
				if err := tc.create(store); err == nil {
					t.Fatalf("expected create helper to fail when %s is injected", tc.failPattern)
				}
			})
		}
	})

	t.Run("delete issue cleanup failures", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "coverage.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProject("Delete matrix project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		blockedIssue, err := base.CreateIssue(project.ID, "", "Blocked issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocked: %v", err)
		}
		if err := base.UpdateIssueState(blockedIssue.ID, StateReady); err != nil {
			t.Fatalf("UpdateIssueState blocked: %v", err)
		}
		issue, err := base.CreateIssueWithOptions(project.ID, "", "Delete matrix target", "cleanup everything", 2, []string{"alpha"}, IssueCreateOptions{
			IssueType: IssueTypeRecurring,
			Cron:      "*/10 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions target: %v", err)
		}
		if err := base.UpdateIssue(issue.ID, map[string]interface{}{
			"agent_name":   "planner",
			"agent_prompt": "  refine the rollout  ",
		}); err != nil {
			t.Fatalf("UpdateIssue agent fields: %v", err)
		}
		workspacePath := filepath.Join(t.TempDir(), issue.Identifier)
		if err := os.MkdirAll(workspacePath, 0o755); err != nil {
			t.Fatalf("MkdirAll workspace: %v", err)
		}
		if _, err := base.CreateWorkspace(issue.ID, workspacePath); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		commentDir := t.TempDir()
		attachmentPath := filepath.Join(commentDir, "note.txt")
		if err := os.WriteFile(attachmentPath, []byte("comment attachment"), 0o644); err != nil {
			t.Fatalf("WriteFile attachment: %v", err)
		}
		commentBody := "Delete matrix comment"
		if _, err := base.CreateIssueComment(issue.ID, IssueCommentInput{
			Body: &commentBody,
			Attachments: []IssueCommentAttachmentInput{{
				Path: attachmentPath,
			}},
		}); err != nil {
			t.Fatalf("CreateIssueComment: %v", err)
		}
		if _, err := base.CreateIssueAsset(issue.ID, "preview.png", bytes.NewReader(samplePNGBytes())); err != nil {
			t.Fatalf("CreateIssueAsset: %v", err)
		}
		requestedAt := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
		if err := base.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
			IssueID:        issue.ID,
			Identifier:     issue.Identifier,
			Phase:          string(WorkflowPhaseImplementation),
			Attempt:        1,
			RunKind:        "run_started",
			ResumeEligible: true,
			UpdatedAt:      requestedAt,
			AppSession: agentruntime.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "session-delete",
				ThreadID:        "thread-delete",
			},
		}); err != nil {
			t.Fatalf("UpsertIssueExecutionSession: %v", err)
		}
		if err := base.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, agentruntime.ActivityEvent{
			Type:     "turn.started",
			ThreadID: "thread-delete",
			TurnID:   "turn-delete",
		}); err != nil {
			t.Fatalf("ApplyIssueActivityEvent started: %v", err)
		}
		if err := base.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, agentruntime.ActivityEvent{
			Type:     "turn.completed",
			ThreadID: "thread-delete",
			TurnID:   "turn-delete",
		}); err != nil {
			t.Fatalf("ApplyIssueActivityEvent completed: %v", err)
		}
		if err := base.SetIssuePendingPlanApproval(issue.ID, "Approve the delete matrix target", requestedAt); err != nil {
			t.Fatalf("SetIssuePendingPlanApproval: %v", err)
		}
		if _, err := base.CreateIssueAgentCommand(issue.ID, "Clean up after delete", IssueAgentCommandPending); err != nil {
			t.Fatalf("CreateIssueAgentCommand: %v", err)
		}
		if _, err := base.SetIssueBlockers(blockedIssue.ID, []string{issue.Identifier}); err != nil {
			t.Fatalf("SetIssueBlockers: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		cases := []struct {
			name        string
			failPattern string
		}{
			{name: "labels", failPattern: "delete from issue_labels"},
			{name: "blockers", failPattern: "delete from issue_blockers"},
			{name: "assets", failPattern: "delete from issue_assets"},
			{name: "comment attachments", failPattern: "delete from issue_comment_attachments"},
			{name: "comments", failPattern: "delete from issue_comments"},
			{name: "planning versions", failPattern: "delete from issue_plan_versions"},
			{name: "recurrences", failPattern: "delete from issue_recurrences"},
			{name: "execution sessions", failPattern: "delete from issue_execution_sessions"},
			{name: "activity updates", failPattern: "delete from issue_activity_updates"},
			{name: "activity entries", failPattern: "delete from issue_activity_entries"},
			{name: "agent commands", failPattern: "delete from issue_agent_commands"},
			{name: "workspaces", failPattern: "delete from workspaces"},
			{name: "issue row", failPattern: "delete from issues"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				store := openFaultySQLiteStoreAt(t, dbPath, tc.failPattern)
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				if err := store.DeleteIssue(issue.ID); err == nil {
					t.Fatalf("expected DeleteIssue to fail when %s is injected", tc.failPattern)
				}
				if _, err := store.GetIssue(issue.ID); err != nil {
					t.Fatalf("expected issue row to survive rollback, got %v", err)
				}
				if err := store.Close(); err != nil {
					t.Fatalf("Close faulty store: %v", err)
				}
			})
		}
	})

	t.Run("runtime event and session decoding branches", func(t *testing.T) {
		store := setupTestStore(t)
		issue, err := store.CreateIssue("", "", "Runtime decode issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := store.AppendRuntimeEvent("run_started", map[string]interface{}{
			"issue_id":   issue.ID,
			"identifier": issue.Identifier,
			"phase":      "implementation",
			"thread_id":  "thread-runtime",
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent: %v", err)
		}
		if _, err := store.db.Exec(`UPDATE runtime_events SET payload_json = ? WHERE issue_id = ?`, "{", issue.ID); err != nil {
			t.Fatalf("update runtime event payload: %v", err)
		}
		events, err := store.ListRuntimeEvents(0, 0)
		if err != nil {
			t.Fatalf("ListRuntimeEvents invalid payload: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected one runtime event, got %#v", events)
		}
		if events[0].Payload != nil {
			t.Fatalf("expected invalid payload to be dropped, got %#v", events[0].Payload)
		}

		sessionIssue, err := store.CreateIssue("", "", "Runtime session issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue session: %v", err)
		}
		if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
			IssueID:    sessionIssue.ID,
			Identifier: sessionIssue.Identifier,
			Phase:      string(WorkflowPhaseImplementation),
			Attempt:    1,
			RunKind:    "run_started",
			UpdatedAt:  time.Now().UTC(),
			AppSession: agentruntime.Session{
				IssueID:         sessionIssue.ID,
				IssueIdentifier: sessionIssue.Identifier,
				SessionID:       "session-runtime",
				ThreadID:        "thread-runtime",
			},
		}); err != nil {
			t.Fatalf("UpsertIssueExecutionSession: %v", err)
		}
		if _, err := store.db.Exec(`UPDATE issue_execution_sessions SET session_json = ? WHERE issue_id = ?`, "{", sessionIssue.ID); err != nil {
			t.Fatalf("update session json: %v", err)
		}
		if _, err := store.ListRecentExecutionSessions(time.Time{}, 0); err == nil {
			t.Fatal("expected invalid execution session JSON to fail")
		}
	})
}

func TestKanbanCoverageValidationAndMutationBranches(t *testing.T) {
	t.Run("validation branches", func(t *testing.T) {
		store := setupTestStore(t)
		if err := store.DeleteWorkspace("missing-workspace"); err != nil {
			t.Fatalf("DeleteWorkspace missing: %v", err)
		}
		if _, err := store.CreateEpic("missing-project", "Missing epic", ""); !IsNotFound(err) {
			t.Fatalf("expected missing project epic creation to fail, got %v", err)
		}
		if err := store.UpdateProviderIssueState("missing-issue", StateReady, WorkflowPhaseImplementation, nil); !IsNotFound(err) {
			t.Fatalf("expected missing provider issue update to fail, got %v", err)
		}
		if _, err := store.ApproveIssuePlanWithNote(nil, time.Now().UTC(), "Ship it", ""); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected nil approval issue to fail validation, got %v", err)
		}
		if err := store.ClearIssuePendingPlanApproval("", "manual_retry"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected blank approval clear id to fail validation, got %v", err)
		}
		if err := store.ClearIssuePendingPlanRevision("", "manual_retry"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected blank revision clear id to fail validation, got %v", err)
		}
		if err := store.SetIssuePendingPlanApprovalWithContext(nil, "Draft rollout", time.Now().UTC(), 1, "thread", "turn"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected nil approval context issue to fail validation, got %v", err)
		}
		if err := store.SetIssuePendingPlanRevision("", "Revise the rollout", time.Now().UTC()); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected blank revision issue to fail validation, got %v", err)
		}
	})

	t.Run("query failures", func(t *testing.T) {
		cases := []struct {
			name        string
			failPattern string
			call        func(*Store) error
		}{
			{
				name:        "project token recompute",
				failPattern: "select id from issues where project_id",
				call: func(store *Store) error {
					_, err := store.RecomputeProjectIssueTokenSpend("proj-missing")
					return err
				},
			},
			{
				name:        "all token recompute",
				failPattern: "select id from issues",
				call: func(store *Store) error {
					_, err := store.RecomputeAllIssueTokenSpend()
					return err
				},
			},
			{
				name:        "provider issue existence",
				failPattern: "select exists",
				call: func(store *Store) error {
					_, err := store.HasProviderIssues("proj-missing", testProviderKind)
					return err
				},
			},
			{
				name:        "next recurring due",
				failPattern: "select r.next_run_at",
				call: func(store *Store) error {
					_, err := store.NextRecurringDueAt("")
					return err
				},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				store := openFaultyMigratedSQLiteStoreAt(t, filepath.Join(t.TempDir(), "query.db"), tc.failPattern)
				if err := tc.call(store); err == nil {
					t.Fatalf("expected %s to fail when %s is injected", tc.name, tc.failPattern)
				}
			})
		}
	})

	t.Run("mutation failures", func(t *testing.T) {
		prepareFaultyDB := func(t *testing.T, failPattern string, setup func(*Store)) *Store {
			t.Helper()
			dbPath := filepath.Join(t.TempDir(), "coverage.db")
			base := openSQLiteStoreAt(t, dbPath)
			if setup != nil {
				setup(base)
			}
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}
			return openFaultyMigratedSQLiteStoreAt(t, dbPath, failPattern)
		}

		t.Run("delete workspace append change", func(t *testing.T) {
			var issue *Issue
			store := prepareFaultyDB(t, "insert into change_events", func(base *Store) {
				project, err := base.CreateProject("Workspace delete coverage", "", "", "")
				if err != nil {
					t.Fatalf("CreateProject: %v", err)
				}
				issue, err = base.CreateIssue(project.ID, "", "Workspace delete coverage issue", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				workspacePath := filepath.Join(t.TempDir(), "workspace")
				if err := os.MkdirAll(workspacePath, 0o755); err != nil {
					t.Fatalf("MkdirAll workspace: %v", err)
				}
				if _, err := base.CreateWorkspace(issue.ID, workspacePath); err != nil {
					t.Fatalf("CreateWorkspace: %v", err)
				}
			})
			if err := store.DeleteWorkspace(issue.ID); err == nil {
				t.Fatal("expected DeleteWorkspace to fail when change-events write is injected")
			}
		})

		t.Run("update workspace path exec", func(t *testing.T) {
			var issue *Issue
			store := prepareFaultyDB(t, "update workspaces set path =", func(base *Store) {
				project, err := base.CreateProject("Workspace path coverage", "", "", "")
				if err != nil {
					t.Fatalf("CreateProject: %v", err)
				}
				issue, err = base.CreateIssue(project.ID, "", "Workspace path coverage issue", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				workspacePath := filepath.Join(t.TempDir(), "workspace")
				if err := os.MkdirAll(workspacePath, 0o755); err != nil {
					t.Fatalf("MkdirAll workspace: %v", err)
				}
				if _, err := base.CreateWorkspace(issue.ID, workspacePath); err != nil {
					t.Fatalf("CreateWorkspace: %v", err)
				}
			})
			if _, err := store.UpdateWorkspacePath(issue.ID, filepath.Join(t.TempDir(), "renamed")); err == nil {
				t.Fatal("expected UpdateWorkspacePath to fail when workspace update is injected")
			}
		})

		t.Run("update workspace run append change", func(t *testing.T) {
			var issue *Issue
			store := prepareFaultyDB(t, "insert into change_events", func(base *Store) {
				project, err := base.CreateProject("Workspace run coverage", "", "", "")
				if err != nil {
					t.Fatalf("CreateProject: %v", err)
				}
				issue, err = base.CreateIssue(project.ID, "", "Workspace run coverage issue", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				workspacePath := filepath.Join(t.TempDir(), "workspace")
				if err := os.MkdirAll(workspacePath, 0o755); err != nil {
					t.Fatalf("MkdirAll workspace: %v", err)
				}
				if _, err := base.CreateWorkspace(issue.ID, workspacePath); err != nil {
					t.Fatalf("CreateWorkspace: %v", err)
				}
			})
			if err := store.UpdateWorkspaceRun(issue.ID); err == nil {
				t.Fatal("expected UpdateWorkspaceRun to fail when change-events write is injected")
			}
		})

		t.Run("create epic append change", func(t *testing.T) {
			var project *Project
			store := prepareFaultyDB(t, "insert into change_events", func(base *Store) {
				var err error
				project, err = base.CreateProject("Epic create coverage", "", "", "")
				if err != nil {
					t.Fatalf("CreateProject: %v", err)
				}
			})
			if _, err := store.CreateEpic(project.ID, "Coverage epic", ""); err == nil {
				t.Fatal("expected CreateEpic to fail when change-events write is injected")
			}
		})

		t.Run("delete project issue cleanup", func(t *testing.T) {
			var project *Project
			store := prepareFaultyDB(t, "insert into change_events", func(base *Store) {
				var err error
				project, err = base.CreateProject("Project delete issue coverage", "", "", "")
				if err != nil {
					t.Fatalf("CreateProject: %v", err)
				}
				if _, err = base.CreateIssue(project.ID, "", "Project delete issue coverage issue", "", 0, nil); err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
			})
			if err := store.DeleteProject(project.ID); err == nil {
				t.Fatal("expected DeleteProject to fail when issue cleanup change-events write is injected")
			}
		})

		t.Run("delete project epic cleanup", func(t *testing.T) {
			var project *Project
			store := prepareFaultyDB(t, "insert into change_events", func(base *Store) {
				var err error
				project, err = base.CreateProject("Project delete epic coverage", "", "", "")
				if err != nil {
					t.Fatalf("CreateProject: %v", err)
				}
				if _, err = base.CreateEpic(project.ID, "Project delete epic coverage epic", ""); err != nil {
					t.Fatalf("CreateEpic: %v", err)
				}
			})
			if err := store.DeleteProject(project.ID); err == nil {
				t.Fatal("expected DeleteProject to fail when epic cleanup change-events write is injected")
			}
		})

		t.Run("delete epic update failure", func(t *testing.T) {
			var epic *Epic
			store := prepareFaultyDB(t, "update issues set epic_id = null", func(base *Store) {
				project, err := base.CreateProject("Epic update failure coverage", "", "", "")
				if err != nil {
					t.Fatalf("CreateProject: %v", err)
				}
				epic, err = base.CreateEpic(project.ID, "Epic update failure coverage epic", "")
				if err != nil {
					t.Fatalf("CreateEpic: %v", err)
				}
			})
			if err := store.DeleteEpic(epic.ID); err == nil {
				t.Fatal("expected DeleteEpic to fail when issue detach update is injected")
			}
		})

		t.Run("delete epic delete failure", func(t *testing.T) {
			var epic *Epic
			store := prepareFaultyDB(t, "delete from epics where id =", func(base *Store) {
				project, err := base.CreateProject("Epic delete failure coverage", "", "", "")
				if err != nil {
					t.Fatalf("CreateProject: %v", err)
				}
				epic, err = base.CreateEpic(project.ID, "Epic delete failure coverage epic", "")
				if err != nil {
					t.Fatalf("CreateEpic: %v", err)
				}
			})
			if err := store.DeleteEpic(epic.ID); err == nil {
				t.Fatal("expected DeleteEpic to fail when epic delete is injected")
			}
		})
	})
}

func TestKanbanCoverageRemainingBranches(t *testing.T) {
	newFaultyStoreAt := func(t *testing.T, dbPath, failPattern string) *Store {
		t.Helper()
		store := openFaultySQLiteStoreAt(t, dbPath, failPattern)
		if err := store.configureConnection(); err != nil {
			t.Fatalf("configureConnection: %v", err)
		}
		return store
	}

	t.Run("workspace delete query and exec failures", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "workspace.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProject("Workspace coverage project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		issue, err := base.CreateIssue(project.ID, "", "Workspace coverage issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		workspacePath := filepath.Join(t.TempDir(), "workspace")
		if err := os.MkdirAll(workspacePath, 0o755); err != nil {
			t.Fatalf("MkdirAll workspace: %v", err)
		}
		if _, err := base.CreateWorkspace(issue.ID, workspacePath); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		queryFail := newFaultyStoreAt(t, dbPath, "select issue_id, path, created_at")
		if err := queryFail.DeleteWorkspace(issue.ID); err == nil {
			t.Fatal("expected DeleteWorkspace to fail when workspace lookup is injected")
		}
		if _, err := os.Stat(workspacePath); err != nil {
			t.Fatalf("expected workspace path to remain after lookup failure, got %v", err)
		}

		deleteFail := newFaultyStoreAt(t, dbPath, "delete from workspaces")
		if err := deleteFail.DeleteWorkspace(issue.ID); err == nil {
			t.Fatal("expected DeleteWorkspace to fail when workspace delete is injected")
		}
		if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
			t.Fatalf("expected workspace tree to be removed before delete failure, got %v", err)
		}
		workspace, err := deleteFail.GetWorkspace(issue.ID)
		if err != nil {
			t.Fatalf("GetWorkspace after delete failure: %v", err)
		}
		if workspace.Path != workspacePath {
			t.Fatalf("expected workspace row to survive delete failure, got %#v", workspace)
		}
	})

	t.Run("issue asset mkdir failure", func(t *testing.T) {
		store := setupTestStore(t)
		issue, err := store.CreateIssue("", "", "Asset mkdir issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		assetRoot := store.IssueAssetRoot()
		issueDir := filepath.Join(assetRoot, issue.ID)
		if err := os.MkdirAll(assetRoot, 0o755); err != nil {
			t.Fatalf("MkdirAll asset root: %v", err)
		}
		if err := os.WriteFile(issueDir, []byte("block"), 0o644); err != nil {
			t.Fatalf("WriteFile issue dir blocker: %v", err)
		}
		if _, err := store.CreateIssueAsset(issue.ID, "preview.png", bytes.NewReader(samplePNGBytes())); err == nil {
			t.Fatal("expected CreateIssueAsset to fail when the issue directory cannot be created")
		}
		if assets, err := store.ListIssueAssets(issue.ID); err != nil {
			t.Fatalf("ListIssueAssets after mkdir failure: %v", err)
		} else if len(assets) != 0 {
			t.Fatalf("expected failed asset creation to leave no rows, got %#v", assets)
		}
	})

	t.Run("blocker persistence failure branches", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "blockers.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Blocked issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocked: %v", err)
		}
		blockerA, err := base.CreateIssue("", "", "Blocker A", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocker A: %v", err)
		}
		blockerB, err := base.CreateIssue("", "", "Blocker B", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocker B: %v", err)
		}
		if _, err := base.SetIssueBlockers(issue.ID, []string{blockerA.Identifier}); err != nil {
			t.Fatalf("SetIssueBlockers initial: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		cases := []struct {
			name        string
			failPattern string
		}{
			{name: "issue_blockers delete", failPattern: "delete from issue_blockers"},
			{name: "change_events append", failPattern: "insert into change_events ("},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				store := newFaultyStoreAt(t, dbPath, tc.failPattern)
				if _, err := store.SetIssueBlockers(issue.ID, []string{blockerB.Identifier}); err == nil {
					t.Fatalf("expected SetIssueBlockers to fail when %s is injected", tc.failPattern)
				}
				blockers, err := store.UnresolvedBlockersForIssue(issue.ID)
				if err != nil {
					t.Fatalf("UnresolvedBlockersForIssue: %v", err)
				}
				if len(blockers) != 1 || blockers[0] != blockerA.Identifier {
					t.Fatalf("expected original blocker to survive rollback, got %#v", blockers)
				}
			})
		}
	})

	t.Run("recurrence filter and query branches", func(t *testing.T) {
		store := setupTestStore(t)
		repoPath := filepath.Join(t.TempDir(), "repo")
		workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
		if err := os.MkdirAll(repoPath, 0o755); err != nil {
			t.Fatalf("MkdirAll repo path: %v", err)
		}
		if err := os.WriteFile(workflowPath, []byte("---\ntracker:\n  kind: kanban\n---\n"), 0o644); err != nil {
			t.Fatalf("WriteFile workflow: %v", err)
		}
		project, err := store.CreateProject("Recurring repo project", "", repoPath, workflowPath)
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		issue, err := store.CreateIssueWithOptions(project.ID, "", "Recurring issue", "", 0, nil, IssueCreateOptions{
			IssueType: IssueTypeRecurring,
			Cron:      "0 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions recurring: %v", err)
		}
		if issue.NextRunAt == nil {
			t.Fatal("expected recurring issue to have a next run")
		}
		nextDue, err := store.NextRecurringDueAt(project.RepoPath)
		if err != nil {
			t.Fatalf("NextRecurringDueAt repoPath: %v", err)
		}
		if nextDue == nil || !nextDue.Equal(issue.NextRunAt.UTC()) {
			t.Fatalf("expected next recurring due %v, got %#v", issue.NextRunAt, nextDue)
		}

		recurrenceDBPath := filepath.Join(t.TempDir(), "recurrence.db")
		baseForFailure := openSQLiteStoreAt(t, recurrenceDBPath)
		issueForFailure, err := baseForFailure.CreateIssueWithOptions("", "", "Recurring failure issue", "", 0, nil, IssueCreateOptions{
			IssueType: IssueTypeRecurring,
			Cron:      "0 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions recurrence failure: %v", err)
		}
		if issueForFailure.NextRunAt == nil {
			t.Fatal("expected recurring failure issue to have a next run")
		}
		if err := baseForFailure.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		queryFail := newFaultyStoreAt(t, recurrenceDBPath, "inner join issue_recurrences r on r.issue_id = i.id")
		if _, err := queryFail.ListDueRecurringIssues(time.Now(), "", 10); err == nil {
			t.Fatal("expected ListDueRecurringIssues to fail when the recurring issue lookup is injected")
		}
	})

	t.Run("issue asset deletion query failure", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "assets.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Asset delete coverage", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		asset, err := base.CreateIssueAsset(issue.ID, "preview.png", bytes.NewReader(samplePNGBytes()))
		if err != nil {
			t.Fatalf("CreateIssueAsset: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		store := newFaultyStoreAt(t, dbPath, "select storage_path from issue_assets")
		tx, err := store.db.Begin()
		if err != nil {
			t.Fatalf("Begin asset tx: %v", err)
		}
		if _, err := store.deleteIssueAssetsTx(tx, issue.ID); err == nil {
			_ = tx.Rollback()
			t.Fatal("expected deleteIssueAssetsTx to fail when the storage-path query is injected")
		}
		if err := tx.Rollback(); err != nil {
			t.Fatalf("Rollback asset tx: %v", err)
		}
		if _, err := store.GetIssueAsset(issue.ID, asset.ID); err != nil {
			t.Fatalf("expected asset row to survive query failure, got %v", err)
		}
	})
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 'a'
	}
	r.remaining -= int64(n)
	return n, nil
}

func TestKanbanHelperCoverageBranches(t *testing.T) {
	t.Run("models", func(t *testing.T) {
		if got := NormalizePermissionProfile(""); got != PermissionProfileDefault {
			t.Fatalf("NormalizePermissionProfile blank = %q, want default", got)
		}
		if got := NormalizePermissionProfile("full_access"); got != PermissionProfileFullAccess {
			t.Fatalf("NormalizePermissionProfile full_access = %q, want full-access", got)
		}
		if got := NormalizePermissionProfile("plan_then_full_access"); got != PermissionProfilePlanThenFullAccess {
			t.Fatalf("NormalizePermissionProfile plan_then_full_access = %q, want plan-then-full-access", got)
		}

		for _, raw := range []string{"", "default", "full_access", "plan-then-full-access"} {
			if profile, err := ParsePermissionProfile(raw); err != nil || profile == "" {
				t.Fatalf("ParsePermissionProfile(%q) = %q, %v", raw, profile, err)
			}
		}
		if _, err := ParsePermissionProfile("admin-mode"); err == nil {
			t.Fatal("expected unknown permission profile to fail")
		}

		if got := DefaultWorkflowPhaseForState(StateDone); got != WorkflowPhaseComplete {
			t.Fatalf("DefaultWorkflowPhaseForState(done) = %q, want complete", got)
		}
		if got := DefaultWorkflowPhaseForState(StateBacklog); got != WorkflowPhaseImplementation {
			t.Fatalf("DefaultWorkflowPhaseForState(backlog) = %q, want implementation", got)
		}

		if got := NormalizeIssueType(""); got != IssueTypeStandard {
			t.Fatalf("NormalizeIssueType blank = %q, want standard", got)
		}
		if got := NormalizeIssueType("RECURRENT"); got != IssueTypeStandard {
			t.Fatalf("NormalizeIssueType unknown = %q, want standard", got)
		}
		if got := NormalizeIssueType("recurring"); got != IssueTypeRecurring {
			t.Fatalf("NormalizeIssueType recurring = %q, want recurring", got)
		}

		if got := NormalizeCollaborationModeOverride(""); got != CollaborationModeOverrideNone {
			t.Fatalf("NormalizeCollaborationModeOverride blank = %q, want none", got)
		}
		if got := NormalizeCollaborationModeOverride("plan"); got != CollaborationModeOverridePlan {
			t.Fatalf("NormalizeCollaborationModeOverride plan = %q, want plan", got)
		}
		if got := NormalizeCollaborationModeOverride("default"); got != CollaborationModeOverrideDefault {
			t.Fatalf("NormalizeCollaborationModeOverride default = %q, want default", got)
		}

		if !StateReady.IsValid() || !ProjectStateRunning.IsValid() || !WorkflowPhaseReview.IsValid() || !IssueTypeRecurring.IsValid() {
			t.Fatal("expected known enum values to be valid")
		}
		if State("unknown").IsValid() || ProjectState("unknown").IsValid() || WorkflowPhase("unknown").IsValid() || IssueType("unknown").IsValid() {
			t.Fatal("expected unknown enum values to be invalid")
		}
	})

	t.Run("activity helpers", func(t *testing.T) {
		if got := existingCommandFromDetail(""); got != "" {
			t.Fatalf("existingCommandFromDetail blank = %q, want empty", got)
		}
		if got := existingCommandFromDetail("$ git status\ncwd: /repo"); got != "git status" {
			t.Fatalf("existingCommandFromDetail command = %q, want git status", got)
		}
		if got := existingCommandFromDetail("plain detail"); got != "" {
			t.Fatalf("existingCommandFromDetail plain = %q, want empty", got)
		}
		if got := existingCommandCWD("cwd: /repo\n$ git status"); got != "/repo" {
			t.Fatalf("existingCommandCWD = %q, want /repo", got)
		}
		if got := agentText(nil); got != "" {
			t.Fatalf("agentText(nil) = %q, want empty", got)
		}
		if got := agentText(map[string]interface{}{"text": "  hello  "}); got != "hello" {
			t.Fatalf("agentText = %q, want hello", got)
		}

		if got := secondaryItemSummary("plan", map[string]interface{}{"text": "  keep it tight  "}); got != "keep it tight" {
			t.Fatalf("secondaryItemSummary(plan) = %q", got)
		}
		if got := secondaryItemSummary("reasoning", map[string]interface{}{"summary": []interface{}{"first", "second"}}); got != "first\nsecond" {
			t.Fatalf("secondaryItemSummary(reasoning) = %q", got)
		}
		if got := secondaryItemSummary("fileChange", map[string]interface{}{"changes": []interface{}{"a", "b"}}); got != "2 file change(s)." {
			t.Fatalf("secondaryItemSummary(fileChange) = %q", got)
		}
		if got := secondaryItemSummary("mcpToolCall", map[string]interface{}{"tool": "search", "status": "running"}); got != "search running" {
			t.Fatalf("secondaryItemSummary(tool call) = %q", got)
		}
		if got := secondaryItemSummary("webSearch", map[string]interface{}{"query": "   "}); got != "Web search executed." {
			t.Fatalf("secondaryItemSummary(webSearch) = %q", got)
		}
		if got := secondaryItemSummary("imageView", map[string]interface{}{"path": "  "}); got != "Image viewed." {
			t.Fatalf("secondaryItemSummary(imageView) = %q", got)
		}
		if got := secondaryItemSummary("enteredReviewMode", map[string]interface{}{"review": "  "}); got != "EnteredReviewMode" {
			t.Fatalf("secondaryItemSummary(review mode fallback) = %q", got)
		}
		if got := secondaryItemSummary("imageGeneration", map[string]interface{}{"result": "  preview.png  "}); got != "preview.png" {
			t.Fatalf("secondaryItemSummary(imageGeneration) = %q", got)
		}
		if got := secondaryItemSummary("contextCompaction", nil); got != "Context compacted." {
			t.Fatalf("secondaryItemSummary(contextCompaction) = %q", got)
		}
		if got := secondaryItemSummary("custom_event", nil); got != "Custom Event" {
			t.Fatalf("secondaryItemSummary(default) = %q", got)
		}

		if got := secondaryItemDetail(nil); got != "" {
			t.Fatalf("secondaryItemDetail(nil) = %q, want empty", got)
		}
		if got := secondaryItemDetail(map[string]interface{}{"kind": "tool", "nested": map[string]interface{}{"value": 1}}); !strings.Contains(got, "\"kind\": \"tool\"") {
			t.Fatalf("secondaryItemDetail = %q", got)
		}
		if got := approvalDetail(nil); got != "" {
			t.Fatalf("approvalDetail(nil) = %q, want empty", got)
		}
		if got := approvalDetail(map[string]interface{}{"params": map[string]interface{}{"value": "yes"}}); !strings.Contains(got, "\"value\": \"yes\"") {
			t.Fatalf("approvalDetail = %q", got)
		}
		if got := planApprovalDetail(nil); got != "" {
			t.Fatalf("planApprovalDetail(nil) = %q, want empty", got)
		}
		if got := planApprovalDetail(map[string]interface{}{"markdown": "  revise  "}); !strings.Contains(got, "\"markdown\": \"  revise  \"") {
			t.Fatalf("planApprovalDetail = %q", got)
		}
		if got := approvalResponseDetail(nil); got != "" {
			t.Fatalf("approvalResponseDetail(nil) = %q, want empty", got)
		}
		if got := approvalResponseDetail(map[string]interface{}{"decision": "accept"}); !strings.Contains(got, "\"decision\": \"accept\"") {
			t.Fatalf("approvalResponseDetail = %q", got)
		}

		if got := elicitationResponseSummary("accept"); got != "Operator accepted the elicitation." {
			t.Fatalf("elicitationResponseSummary(accept) = %q", got)
		}
		if got := elicitationResponseSummary("decline"); got != "Operator declined the elicitation." {
			t.Fatalf("elicitationResponseSummary(decline) = %q", got)
		}
		if got := elicitationResponseSummary("cancel"); got != "Operator cancelled the elicitation." {
			t.Fatalf("elicitationResponseSummary(cancel) = %q", got)
		}
		if got := elicitationResponseSummary("maybe"); got != "Operator resolved the elicitation." {
			t.Fatalf("elicitationResponseSummary(default) = %q", got)
		}
		if got := elicitationResponseTone("accept"); got != "success" {
			t.Fatalf("elicitationResponseTone(accept) = %q", got)
		}
		if got := elicitationResponseTone("decline"); got != "error" {
			t.Fatalf("elicitationResponseTone(decline) = %q", got)
		}

		if got := defaultActivitySummary("turn.started"); got != "Turn execution started." {
			t.Fatalf("defaultActivitySummary(started) = %q", got)
		}
		if got := defaultActivitySummary("turn.completed"); got != "Turn execution completed." {
			t.Fatalf("defaultActivitySummary(completed) = %q", got)
		}
		if got := defaultActivitySummary("turn.failed"); got != "Turn execution failed." {
			t.Fatalf("defaultActivitySummary(failed) = %q", got)
		}
		if got := defaultActivitySummary("turn.cancelled"); got != "Turn execution was cancelled." {
			t.Fatalf("defaultActivitySummary(cancelled) = %q", got)
		}
		if got := defaultActivitySummary("custom_event"); got != "Custom Event" {
			t.Fatalf("defaultActivitySummary(default) = %q", got)
		}
	})

	t.Run("recurrence and runtime keys", func(t *testing.T) {
		trueValue := true
		if got, ok := boolFromValue(true); !ok || !got {
			t.Fatalf("boolFromValue(true) = (%v, %v)", got, ok)
		}
		if got, ok := boolFromValue(&trueValue); !ok || !got {
			t.Fatalf("boolFromValue(*bool) = (%v, %v)", got, ok)
		}
		if got, ok := boolFromValue(nil); ok || got {
			t.Fatalf("boolFromValue(nil) = (%v, %v)", got, ok)
		}
		if got, ok := boolFromValue("true"); ok || got {
			t.Fatalf("boolFromValue(string) = (%v, %v)", got, ok)
		}

		if got := runtimeSeriesTokenEvent("run_completed", ""); !got {
			t.Fatal("expected completed run to count as token event")
		}
		if got := runtimeSeriesTokenEvent("retry_paused", "plan_approval_pending"); !got {
			t.Fatal("expected plan approval retry pause to count as token event")
		}
		if got := runtimeSeriesTokenEvent("retry_paused", "other"); got {
			t.Fatal("expected unrelated retry pause to be ignored")
		}
		if got := runtimeSeriesTokenEvent("turn.started", ""); got {
			t.Fatal("expected unrelated event to be ignored")
		}

		ts := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		if got := runtimeSeriesEventKey("issue-1", "ISS-1", 2, `{"thread_id":"thread-1"}`, "run_completed", ts); got != "thread:thread-1" {
			t.Fatalf("runtimeSeriesEventKey thread = %q", got)
		}
		if got := runtimeSeriesEventKey("issue-1", "ISS-1", 2, `{}`, "run_completed", ts); got != "issue:issue-1#attempt:2" {
			t.Fatalf("runtimeSeriesEventKey issue = %q", got)
		}
		if got := runtimeSeriesEventKey("", "ISS-1", 2, `{}`, "run_completed", ts); got != "identifier:ISS-1#attempt:2" {
			t.Fatalf("runtimeSeriesEventKey identifier = %q", got)
		}
		if got := runtimeSeriesEventKey("", "", 2, `{}`, "run_started", ts); !strings.HasPrefix(got, "event:run_started#attempt:2#ts:") {
			t.Fatalf("runtimeSeriesEventKey fallback = %q", got)
		}

		if got := asString("ready"); got != "ready" {
			t.Fatalf("asString = %q", got)
		}
		if got := asString(123); got != "" {
			t.Fatalf("asString non-string = %q", got)
		}
		if got := asInt(17); got != 17 {
			t.Fatalf("asInt(int) = %d", got)
		}
		if got := asInt(int32(18)); got != 18 {
			t.Fatalf("asInt(int32) = %d", got)
		}
		if got := asInt(int64(19)); got != 19 {
			t.Fatalf("asInt(int64) = %d", got)
		}
		if got := asInt(float64(20)); got != 20 {
			t.Fatalf("asInt(float64) = %d", got)
		}
		if got := asInt("20"); got != 0 {
			t.Fatalf("asInt(non-numeric) = %d", got)
		}
	})

	t.Run("formatting helpers", func(t *testing.T) {
		if got := cloneActivityMap(nil); got != nil {
			t.Fatalf("cloneActivityMap(nil) = %#v, want nil", got)
		}
		original := map[string]interface{}{"kind": "tool", "count": 2}
		cloned := cloneActivityMap(original)
		if len(cloned) != len(original) || cloned["kind"] != "tool" || cloned["count"] != 2 {
			t.Fatalf("cloneActivityMap copy = %#v, want %#v", cloned, original)
		}
		cloned["kind"] = "updated"
		if original["kind"] != "tool" {
			t.Fatalf("cloneActivityMap should not alias input, got %#v", original)
		}

		detail := "$ git status\ncwd: /repo\n\nchanged files\n\nexit code: 2"
		if got := existingCommandOutput(detail); got != "changed files" {
			t.Fatalf("existingCommandOutput = %q, want changed files", got)
		}
		if got := existingCommandOutput("$ git status\ncwd: /repo\n\nexit code: 2"); got != "" {
			t.Fatalf("existingCommandOutput empty body = %q, want empty", got)
		}
		if got := commandCompletionStatus(nil); got != "completed" {
			t.Fatalf("commandCompletionStatus(nil) = %q", got)
		}
		zero := 0
		if got := commandCompletionStatus(&zero); got != "completed" {
			t.Fatalf("commandCompletionStatus(0) = %q", got)
		}
		nonzero := 3
		if got := commandCompletionStatus(&nonzero); got != "failed" {
			t.Fatalf("commandCompletionStatus(nonzero) = %q", got)
		}
		if title, tone := commandCompletionTitleAndTone(&nonzero, "completed"); title != "Command failed (exit 3)" || tone != "error" {
			t.Fatalf("commandCompletionTitleAndTone(exit) = (%q, %q)", title, tone)
		}
		if title, tone := commandCompletionTitleAndTone(nil, "failed"); title != "Command failed" || tone != "error" {
			t.Fatalf("commandCompletionTitleAndTone(failed) = (%q, %q)", title, tone)
		}
		if title, tone := commandCompletionTitleAndTone(nil, "completed"); title != "Command completed" || tone != "success" {
			t.Fatalf("commandCompletionTitleAndTone(default) = (%q, %q)", title, tone)
		}

		if got := firstMeaningfulLine("\n   \nfirst line\nsecond line"); got != "first line" {
			t.Fatalf("firstMeaningfulLine = %q", got)
		}
		if got := humanizeActivityLabel("item.commandExecution.output_delta"); got != "Item CommandExecution Output Delta" {
			t.Fatalf("humanizeActivityLabel = %q", got)
		}
		if got := humanizeActivityLabel(""); got != "Activity" {
			t.Fatalf("humanizeActivityLabel blank = %q", got)
		}
		if got := firstString(map[string]interface{}{"count": 1, "text": " hello "}, "missing", "text"); got != " hello " {
			t.Fatalf("firstString = %q", got)
		}
		if got := firstString(nil, "text"); got != "" {
			t.Fatalf("firstString(nil) = %q", got)
		}
		if got := stringSlice([]interface{}{"alpha", 1, "beta", nil}); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
			t.Fatalf("stringSlice = %#v", got)
		}
		if got := stringSlice("not-a-slice"); got != nil {
			t.Fatalf("stringSlice(non-slice) = %#v, want nil", got)
		}
		if got := inputRequestSummary(nil); got != "The agent requested user input." {
			t.Fatalf("inputRequestSummary(nil) = %q", got)
		}
		if got := inputRequestSummary(map[string]interface{}{"params": map[string]interface{}{"questions": []interface{}{map[string]interface{}{"question": "   "}, map[string]interface{}{"question": "Need approval?"}}}}); got != "Need approval?" {
			t.Fatalf("inputRequestSummary(question) = %q", got)
		}
		if got := inputRequestDetail(map[string]interface{}{"params": map[string]interface{}{"questions": []interface{}{map[string]interface{}{"question": "Need approval?"}}}}); !strings.Contains(got, "Need approval?") {
			t.Fatalf("inputRequestDetail = %q", got)
		}
		if got := inputRequestDetail(map[string]interface{}{"params": map[string]interface{}{"questions": []interface{}{map[string]interface{}{"question": make(chan int)}}}}); got != "" {
			t.Fatalf("inputRequestDetail error branch = %q", got)
		}
		if got := planApprovalMarkdown(nil); got != "" {
			t.Fatalf("planApprovalMarkdown(nil) = %q", got)
		}
		if got := planApprovalMarkdown(map[string]interface{}{"markdown": "  keep this  "}); got != "keep this" {
			t.Fatalf("planApprovalMarkdown = %q", got)
		}
		if got := approvalDetail(map[string]interface{}{"params": map[string]interface{}{"answer": "yes"}}); !strings.Contains(got, "\"answer\": \"yes\"") {
			t.Fatalf("approvalDetail = %q", got)
		}
		if got := approvalDetail(map[string]interface{}{"params": map[string]interface{}{"answer": make(chan int)}}); got != "" {
			t.Fatalf("approvalDetail error branch = %q", got)
		}
		if got := appendActivityText("", "  line one  "); got != "  line one  " {
			t.Fatalf("appendActivityText blank current = %q", got)
		}
		if got := appendActivityText("prefix", ""); got != "prefix" {
			t.Fatalf("appendActivityText empty delta = %q", got)
		}
		if got := cleanActivityTextPreserveWhitespace("\x1b[31mline\r\n\t\x00two"); got != "line\n\ttwo" {
			t.Fatalf("cleanActivityTextPreserveWhitespace = %q", got)
		}
		if got := activityEntryExpandable("", "summary"); got {
			t.Fatal("activityEntryExpandable should be false for empty detail")
		}
		if got := activityEntryExpandable("line1\nline2", "summary"); !got {
			t.Fatal("activityEntryExpandable should be true for multiline detail")
		}
		if got := activityEntryExpandable("detailed explanation that is longer than summary plus padding", "short"); !got {
			t.Fatal("activityEntryExpandable should be true for long detail")
		}
	})

	t.Run("activity and attachment helpers", func(t *testing.T) {
		if got := trimToUTF8Boundary("a€b", 3); got != "a" {
			t.Fatalf("trimToUTF8Boundary = %q, want a", got)
		}
		if got := trimToTrailingUTF8Boundary("a€b", 3); got != "b" {
			t.Fatalf("trimToTrailingUTF8Boundary = %q, want b", got)
		}
		if got := truncateActivityText("abcdef", 1); got != "\n" {
			t.Fatalf("truncateActivityText short budget = %q, want newline marker", got)
		}
		if got := truncateActivityTail("abcdef", 1); got != "." {
			t.Fatalf("truncateActivityTail short budget = %q, want marker prefix", got)
		}

		value := map[string]interface{}{
			"plain":  "hello",
			"nested": []interface{}{"world", map[string]interface{}{"deep": "value"}},
		}
		truncated := truncateActivityValue(value).(map[string]interface{})
		if truncated["plain"] != "hello" {
			t.Fatalf("truncateActivityValue plain = %#v", truncated["plain"])
		}
		nested := truncated["nested"].([]interface{})
		if nested[0] != "world" {
			t.Fatalf("truncateActivityValue nested slice = %#v", nested)
		}
		if deep := nested[1].(map[string]interface{}); deep["deep"] != "value" {
			t.Fatalf("truncateActivityValue nested map = %#v", deep)
		}
		if got := truncateActivityValue(nil); got != nil {
			t.Fatalf("truncateActivityValue(nil) = %#v, want nil", got)
		}
		if got := truncateActivityValue(7); got != 7 {
			t.Fatalf("truncateActivityValue scalar = %#v, want 7", got)
		}

		activityCases := []struct {
			name string
			ev   agentruntime.ActivityEvent
			want string
			ok   bool
		}{
			{name: "missing item id", ev: agentruntime.ActivityEvent{Type: "item.started", ThreadID: "thread-1", TurnID: "turn-1"}, ok: false},
			{name: "agent item started", ev: agentruntime.ActivityEvent{Type: "item.started", ThreadID: "thread-1", TurnID: "turn-1", ItemID: "item-1"}, want: "attempt:1:item:thread-1:turn-1:item-1", ok: true},
			{name: "missing turn id", ev: agentruntime.ActivityEvent{Type: "turn.started", ThreadID: "thread-1"}, ok: false},
			{name: "turn status", ev: agentruntime.ActivityEvent{Type: "turn.completed", ThreadID: "thread-1", TurnID: "turn-2"}, want: "attempt:1:status:thread-1:turn-2:turn.completed", ok: true},
			{name: "approval request", ev: agentruntime.ActivityEvent{Type: "item.commandExecution.requestApproval", ThreadID: "thread-1", TurnID: "turn-3", RequestID: "req-1"}, want: "attempt:1:status:thread-1:turn-3:req-1", ok: true},
			{name: "approval resolved", ev: agentruntime.ActivityEvent{Type: "item.commandExecution.approvalResolved", ThreadID: "thread-1", TurnID: "turn-3", RequestID: "req-1"}, want: "attempt:1:status:thread-1:turn-3:req-1", ok: true},
			{name: "plan session", ev: agentruntime.ActivityEvent{Type: "plan.sessionStarted", ThreadID: "thread-1", TurnID: "turn-4", Raw: map[string]interface{}{"session_id": "plan-1"}}, want: "attempt:1:planning:plan-1:session-start", ok: true},
			{name: "plan version", ev: agentruntime.ActivityEvent{Type: "plan.versionPublished", ThreadID: "thread-1", TurnID: "turn-4", Raw: map[string]interface{}{"session_id": "plan-1", "version_number": "2"}}, want: "attempt:1:planning:plan-1:version:2", ok: true},
			{name: "revision requested", ev: agentruntime.ActivityEvent{Type: "plan.revisionRequested", ThreadID: "thread-1", TurnID: "turn-4", Raw: map[string]interface{}{"session_id": "plan-1", "requested_at": "2026-03-18T12:00:00Z"}}, want: "attempt:1:planning:plan-1:revision-requested:2026-03-18T12:00:00Z", ok: true},
			{name: "revision applied", ev: agentruntime.ActivityEvent{Type: "plan.revisionApplied", ThreadID: "thread-1", TurnID: "turn-4", Raw: map[string]interface{}{"session_id": "plan-1", "cleared_at": "2026-03-18T12:10:00Z"}}, want: "attempt:1:planning:plan-1:revision-applied:2026-03-18T12:10:00Z", ok: true},
			{name: "plan approved", ev: agentruntime.ActivityEvent{Type: "plan.approved", ThreadID: "thread-1", TurnID: "turn-4", Raw: map[string]interface{}{"session_id": "plan-1"}}, want: "attempt:1:planning:plan-1:approved", ok: true},
			{name: "plan abandoned", ev: agentruntime.ActivityEvent{Type: "plan.abandoned", ThreadID: "thread-1", TurnID: "turn-4", Raw: map[string]interface{}{"session_id": "plan-1"}}, want: "attempt:1:planning:plan-1:abandoned", ok: true},
		}
		for _, tc := range activityCases {
			got, ok := issueActivityLogicalID(1, tc.ev)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("%s: issueActivityLogicalID() = (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.ok)
			}
		}

		store := setupTestStore(t)
		agentIssue, err := store.CreateIssue("", "", "Agent activity", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue agent: %v", err)
		}
		commandIssue, err := store.CreateIssue("", "", "Command activity", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue command: %v", err)
		}
		statusIssue, err := store.CreateIssue("", "", "Status activity", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue status: %v", err)
		}

		apply := func(issue *Issue, attempt int, event agentruntime.ActivityEvent) {
			t.Helper()
			if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, attempt, event); err != nil {
				t.Fatalf("ApplyIssueActivityEvent(%s, %s): %v", issue.Identifier, event.Type, err)
			}
		}

		finalAnswer := map[string]interface{}{
			"text": "  hello  ",
			"meta": map[string]interface{}{"nested": "value"},
		}
		apply(agentIssue, 1, agentruntime.ActivityEvent{
			Type:      "item.started",
			ThreadID:  "thread-agent",
			TurnID:    "turn-agent",
			ItemID:    "agent-1",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Item:      finalAnswer,
			Raw:       map[string]interface{}{"params": map[string]interface{}{"markdown": "  plan  "}},
		})
		apply(agentIssue, 1, agentruntime.ActivityEvent{
			Type:      "item.agentMessage.delta",
			ThreadID:  "thread-agent",
			TurnID:    "turn-agent",
			ItemID:    "agent-1",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Delta:     " world",
		})
		apply(agentIssue, 1, agentruntime.ActivityEvent{
			Type:      "item.completed",
			ThreadID:  "thread-agent",
			TurnID:    "turn-agent",
			ItemID:    "agent-1",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Item:      finalAnswer,
		})
		apply(agentIssue, 1, agentruntime.ActivityEvent{
			Type:     "item.plan.delta",
			ThreadID: "thread-agent",
			TurnID:   "turn-agent",
			ItemID:   "plan-1",
			ItemType: "plan",
			Delta:    " first line",
		})
		apply(agentIssue, 1, agentruntime.ActivityEvent{
			Type:     "item.started",
			ThreadID: "thread-agent",
			TurnID:   "turn-agent",
			ItemID:   "search-1",
			ItemType: "webSearch",
			Item:     map[string]interface{}{"query": " docs "},
		})
		apply(agentIssue, 1, agentruntime.ActivityEvent{
			Type:     "item.completed",
			ThreadID: "thread-agent",
			TurnID:   "turn-agent",
			ItemID:   "search-1",
			ItemType: "webSearch",
			Item:     map[string]interface{}{"query": " docs "},
		})

		exitCode := 7
		apply(commandIssue, 1, agentruntime.ActivityEvent{
			Type:     "item.started",
			ThreadID: "thread-command",
			TurnID:   "turn-command",
			ItemID:   "cmd-1",
			ItemType: "commandExecution",
			Command:  "git status",
			CWD:      "/repo",
		})
		apply(commandIssue, 1, agentruntime.ActivityEvent{
			Type:     "item.commandExecution.outputDelta",
			ThreadID: "thread-command",
			TurnID:   "turn-command",
			ItemID:   "cmd-1",
			Delta:    "line one\n",
		})
		apply(commandIssue, 1, agentruntime.ActivityEvent{
			Type:     "item.commandExecution.terminalInteraction",
			ThreadID: "thread-command",
			TurnID:   "turn-command",
			ItemID:   "cmd-1",
			Stdin:    "ls -la",
		})
		apply(commandIssue, 1, agentruntime.ActivityEvent{
			Type:             "item.completed",
			ThreadID:         "thread-command",
			TurnID:           "turn-command",
			ItemID:           "cmd-1",
			ItemType:         "commandExecution",
			Command:          "git status",
			CWD:              "/repo",
			AggregatedOutput: "line one\nline two",
			ExitCode:         &exitCode,
		})
		apply(commandIssue, 1, agentruntime.ActivityEvent{
			Type:      "item.commandExecution.requestApproval",
			ThreadID:  "thread-command",
			TurnID:    "turn-command",
			RequestID: "req-approval",
			Command:   "rm -rf /",
			Reason:    "Need approval",
			Raw:       map[string]interface{}{"params": map[string]interface{}{"command": "rm -rf /"}},
		})
		apply(commandIssue, 1, agentruntime.ActivityEvent{
			Type:      "item.commandExecution.approvalResolved",
			ThreadID:  "thread-command",
			TurnID:    "turn-command",
			RequestID: "req-approval",
			Status:    "approved",
			Raw:       map[string]interface{}{"decision": "approved"},
		})

		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:     "turn.started",
			ThreadID: "thread-status",
			TurnID:   "turn-1",
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:     "turn.completed",
			ThreadID: "thread-status",
			TurnID:   "turn-2",
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:     "turn.failed",
			ThreadID: "thread-status",
			TurnID:   "turn-3",
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:     "turn.cancelled",
			ThreadID: "thread-status",
			TurnID:   "turn-4",
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:      "item.tool.requestUserInput",
			ThreadID:  "thread-status",
			TurnID:    "turn-status",
			RequestID: "input-1",
			Raw: map[string]interface{}{
				"params": map[string]interface{}{
					"questions": []interface{}{map[string]interface{}{"question": "Need approval?"}},
				},
			},
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:      "item.tool.userInputSubmitted",
			ThreadID:  "thread-status",
			TurnID:    "turn-status",
			RequestID: "input-1",
			Raw: map[string]interface{}{
				"answers": map[string]interface{}{"answer": []interface{}{"Yes"}},
			},
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:      "mcpServer.elicitation.request",
			ThreadID:  "thread-status",
			TurnID:    "turn-status",
			RequestID: "elic-1",
			Reason:    "Please confirm",
			Raw: map[string]interface{}{
				"params": map[string]interface{}{
					"questions": []interface{}{map[string]interface{}{"question": "Proceed?"}},
				},
			},
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:      "mcpServer.elicitation.resolved",
			ThreadID:  "thread-status",
			TurnID:    "turn-status",
			RequestID: "elic-1",
			Status:    "accept",
			Raw:       map[string]interface{}{"decision": "accept"},
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:     "plan.sessionStarted",
			ThreadID: "thread-status",
			TurnID:   "turn-status",
			Raw: map[string]interface{}{
				"session_id": "plan-1",
				"markdown":   "Draft plan",
			},
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:     "plan.versionPublished",
			ThreadID: "thread-status",
			TurnID:   "turn-status",
			Raw: map[string]interface{}{
				"session_id":     "plan-1",
				"version_number": "2",
				"markdown":       "Ready",
			},
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:     "plan.revisionRequested",
			ThreadID: "thread-status",
			TurnID:   "turn-status",
			Raw: map[string]interface{}{
				"session_id":    "plan-1",
				"revision_note": "Needs tweaks",
			},
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:     "plan.revisionApplied",
			ThreadID: "thread-status",
			TurnID:   "turn-status",
			Raw: map[string]interface{}{
				"session_id":    "plan-1",
				"revision_note": "Queued",
			},
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:     "plan.approved",
			ThreadID: "thread-status",
			TurnID:   "turn-status",
			Raw:      map[string]interface{}{"session_id": "plan-1"},
		})
		apply(statusIssue, 1, agentruntime.ActivityEvent{
			Type:     "plan.abandoned",
			ThreadID: "thread-status",
			TurnID:   "turn-status",
			Raw: map[string]interface{}{
				"session_id": "plan-1",
				"reason":     "Cancelled",
			},
		})

		hasTitle := func(entries []IssueActivityEntry, want string) bool {
			for _, entry := range entries {
				if entry.Title == want {
					return true
				}
			}
			return false
		}

		if entries, err := store.ListIssueActivityEntries(agentIssue.ID); err != nil || len(entries) == 0 || !hasTitle(entries, "Final answer") || !hasTitle(entries, "WebSearch") {
			t.Fatalf("expected agent activity entries, got entries=%#v err=%v", entries, err)
		}
		if entries, err := store.ListIssueActivityEntries(commandIssue.ID); err != nil || len(entries) == 0 || !hasTitle(entries, "Command failed (exit 7)") || !hasTitle(entries, "Approval resolved") {
			t.Fatalf("expected command activity entries, got entries=%#v err=%v", entries, err)
		}
		if entries, err := store.ListIssueActivityEntries(statusIssue.ID); err != nil || len(entries) == 0 || !hasTitle(entries, "User input submitted") || !hasTitle(entries, "Plan approved") || !hasTitle(entries, "Elicitation resolved") {
			t.Fatalf("expected status activity entries, got entries=%#v err=%v", entries, err)
		}
	})
}

func TestKanbanCoverageSummariesRuntimeAndLifecycleBranches(t *testing.T) {
	store := setupTestStore(t)

	customProject, err := store.CreateProjectWithProvider("Custom summary project", "Custom summary project", "", "", ProviderKindKanban, "", map[string]interface{}{
		"active_states":   []string{string(StateReady), string(StateInProgress), string(StateInReview)},
		"terminal_states": []string{string(StateDone), string(StateCancelled)},
	})
	if err != nil {
		t.Fatalf("CreateProjectWithProvider custom: %v", err)
	}
	defaultProject, err := store.CreateProject("Default summary project", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject default: %v", err)
	}

	customEpic, err := store.CreateEpic(customProject.ID, "Custom epic", "Custom epic description")
	if err != nil {
		t.Fatalf("CreateEpic custom: %v", err)
	}
	emptyEpic, err := store.CreateEpic(customProject.ID, "Empty epic", "Empty epic description")
	if err != nil {
		t.Fatalf("CreateEpic empty: %v", err)
	}
	defaultEpic, err := store.CreateEpic(defaultProject.ID, "Default epic", "Default epic description")
	if err != nil {
		t.Fatalf("CreateEpic default: %v", err)
	}

	readyIssue, err := store.CreateIssue(customProject.ID, customEpic.ID, "needle blocker", "ready issue", 1, nil)
	if err != nil {
		t.Fatalf("CreateIssue ready: %v", err)
	}
	blockedIssue, err := store.CreateIssue(customProject.ID, customEpic.ID, "blocked needle", "blocked issue", 2, nil)
	if err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}
	recurringEnabled := false
	recurringIssue, err := store.CreateIssueWithOptions(customProject.ID, customEpic.ID, "recurring needle", "recurring issue", 3, []string{"delta"}, IssueCreateOptions{
		IssueType:   IssueTypeRecurring,
		Cron:        "*/5 * * * *",
		Enabled:     &recurringEnabled,
		AgentName:   " planner ",
		AgentPrompt: " outline release ",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions recurring: %v", err)
	}
	defaultIssue, err := store.CreateIssue(defaultProject.ID, defaultEpic.ID, "default issue", "default issue", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue default: %v", err)
	}

	if err := store.UpdateIssueState(readyIssue.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState ready: %v", err)
	}
	if err := store.UpdateIssueState(blockedIssue.ID, StateReady); err != nil {
		t.Fatalf("UpdateIssueState blocked: %v", err)
	}
	if err := store.UpdateIssueState(recurringIssue.ID, StateInProgress); err != nil {
		t.Fatalf("UpdateIssueState recurring: %v", err)
	}
	if err := store.UpdateIssueState(defaultIssue.ID, StateDone); err != nil {
		t.Fatalf("UpdateIssueState default: %v", err)
	}

	if _, err := store.SetIssueBlockers(blockedIssue.ID, []string{readyIssue.Identifier, readyIssue.Identifier}); err != nil {
		t.Fatalf("SetIssueBlockers blocked: %v", err)
	}
	if err := store.UpdateIssue(blockedIssue.ID, map[string]interface{}{
		"labels": []string{"alpha", "beta"},
	}); err != nil {
		t.Fatalf("UpdateIssue labels: %v", err)
	}
	if err := store.UpdateIssue(readyIssue.ID, map[string]interface{}{
		"labels": []string{"gamma"},
	}); err != nil {
		t.Fatalf("UpdateIssue ready labels: %v", err)
	}

	workspacePath := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePath, "note.txt"), []byte("workspace"), 0o644); err != nil {
		t.Fatalf("WriteFile workspace note: %v", err)
	}
	if _, err := store.CreateWorkspace(blockedIssue.ID, workspacePath); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := store.UpdateWorkspaceRun(blockedIssue.ID); err != nil {
		t.Fatalf("UpdateWorkspaceRun: %v", err)
	}
	renamedWorkspacePath := filepath.Join(t.TempDir(), "workspace-renamed")
	if err := os.MkdirAll(renamedWorkspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll renamed workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(renamedWorkspacePath, "note.txt"), []byte("renamed workspace"), 0o644); err != nil {
		t.Fatalf("WriteFile renamed workspace note: %v", err)
	}
	if _, err := store.UpdateWorkspacePath(blockedIssue.ID, renamedWorkspacePath); err != nil {
		t.Fatalf("UpdateWorkspacePath: %v", err)
	}

	asset, err := store.CreateIssueAsset(blockedIssue.ID, "preview", bytes.NewReader(samplePNGBytes()))
	if err != nil {
		t.Fatalf("CreateIssueAsset: %v", err)
	}
	assetContent, assetPath, err := store.GetIssueAssetContent(blockedIssue.ID, asset.ID)
	if err != nil {
		t.Fatalf("GetIssueAssetContent: %v", err)
	}
	if assetContent.ID != asset.ID {
		t.Fatalf("expected asset content to resolve the created asset, got %#v", assetContent)
	}

	commentAttachmentPath := filepath.Join(t.TempDir(), "comment.txt")
	if err := os.WriteFile(commentAttachmentPath, []byte("comment attachment"), 0o644); err != nil {
		t.Fatalf("WriteFile comment attachment: %v", err)
	}
	commentBody := "Review the blocked issue"
	comment, err := store.CreateIssueComment(blockedIssue.ID, IssueCommentInput{
		Body: &commentBody,
		Attachments: []IssueCommentAttachmentInput{{
			Path: commentAttachmentPath,
		}},
	})
	if err != nil {
		t.Fatalf("CreateIssueComment: %v", err)
	}
	commentAttachment, commentAttachmentPathStored, err := store.GetIssueCommentAttachmentContent(blockedIssue.ID, comment.ID, comment.Attachments[0].ID)
	if err != nil {
		t.Fatalf("GetIssueCommentAttachmentContent: %v", err)
	}
	if commentAttachment.ID != comment.Attachments[0].ID {
		t.Fatalf("expected comment attachment lookup to resolve the created attachment, got %#v", commentAttachment)
	}
	comments, err := store.ListIssueComments(blockedIssue.ID)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(comments) != 1 || len(comments[0].Attachments) != 1 {
		t.Fatalf("unexpected comment list: %#v", comments)
	}

	updatedAttachmentPath := filepath.Join(t.TempDir(), "comment-updated.txt")
	if err := os.WriteFile(updatedAttachmentPath, []byte("updated attachment"), 0o644); err != nil {
		t.Fatalf("WriteFile updated comment attachment: %v", err)
	}
	updatedBody := "Updated review note"
	updatedComment, err := store.UpdateIssueComment(blockedIssue.ID, comment.ID, IssueCommentInput{
		Body: &updatedBody,
		Attachments: []IssueCommentAttachmentInput{{
			Path: updatedAttachmentPath,
		}},
		RemoveAttachmentIDs: []string{comment.Attachments[0].ID},
	})
	if err != nil {
		t.Fatalf("UpdateIssueComment: %v", err)
	}
	if len(updatedComment.Attachments) != 1 || updatedComment.Body != updatedBody {
		t.Fatalf("unexpected updated comment: %#v", updatedComment)
	}
	if _, err := os.Stat(commentAttachmentPathStored); !os.IsNotExist(err) {
		t.Fatalf("expected removed comment attachment to be cleaned up, got %v", err)
	}
	updatedAttachment, updatedAttachmentPathStored, err := store.GetIssueCommentAttachmentContent(blockedIssue.ID, updatedComment.ID, updatedComment.Attachments[0].ID)
	if err != nil {
		t.Fatalf("GetIssueCommentAttachmentContent updated: %v", err)
	}
	if updatedAttachment.ID != updatedComment.Attachments[0].ID {
		t.Fatalf("expected updated comment attachment lookup to resolve the created attachment, got %#v", updatedAttachment)
	}

	if err := store.DeleteIssueComment(blockedIssue.ID, comment.ID); err != nil {
		t.Fatalf("DeleteIssueComment: %v", err)
	}
	if _, err := store.GetIssueComment(blockedIssue.ID, comment.ID); !IsNotFound(err) {
		t.Fatalf("expected deleted comment to be removed, got %v", err)
	}
	if _, err := os.Stat(updatedAttachmentPathStored); !os.IsNotExist(err) {
		t.Fatalf("expected deleted comment attachment to be cleaned up, got %v", err)
	}

	doneCommentAttachment, err := store.CreateIssueComment(blockedIssue.ID, IssueCommentInput{
		Body: &commentBody,
	})
	if err != nil {
		t.Fatalf("CreateIssueComment body-only: %v", err)
	}
	if err := store.DeleteIssueComment(blockedIssue.ID, doneCommentAttachment.ID); err != nil {
		t.Fatalf("DeleteIssueComment body-only: %v", err)
	}

	if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
		IssueID:        blockedIssue.ID,
		Identifier:     blockedIssue.Identifier,
		Phase:          string(WorkflowPhaseImplementation),
		Attempt:        2,
		RunKind:        "run_started",
		ResumeEligible: true,
		UpdatedAt:      time.Time{},
		AppSession: agentruntime.Session{
			IssueID:         blockedIssue.ID,
			IssueIdentifier: blockedIssue.Identifier,
			SessionID:       "session-blocked",
			ThreadID:        "thread-blocked",
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
		"issue_id":     readyIssue.ID,
		"identifier":   readyIssue.Identifier,
		"phase":        "implementation",
		"thread_id":    "thread-ready",
		"total_tokens": 11,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent ready: %v", err)
	}
	if err := store.AppendRuntimeEventOnly("run_completed", map[string]interface{}{
		"issue_id":     readyIssue.ID,
		"identifier":   readyIssue.Identifier,
		"phase":        "implementation",
		"thread_id":    "thread-ready",
		"total_tokens": 18,
	}); err != nil {
		t.Fatalf("AppendRuntimeEventOnly ready: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
		"issue_id":     blockedIssue.ID,
		"identifier":   blockedIssue.Identifier,
		"phase":        "implementation",
		"total_tokens": 7,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent blocked: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
		"issue_id":     recurringIssue.ID,
		"identifier":   recurringIssue.Identifier,
		"phase":        "implementation",
		"thread_id":    "thread-recurring",
		"total_tokens": 5,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent recurring: %v", err)
	}
	if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
		"issue_id":     defaultIssue.ID,
		"identifier":   defaultIssue.Identifier,
		"phase":        "implementation",
		"total_tokens": 9,
	}); err != nil {
		t.Fatalf("AppendRuntimeEvent default: %v", err)
	}

	if _, err := store.db.Exec(`UPDATE runtime_events SET payload_json = ? WHERE issue_id = ?`, "{", blockedIssue.ID); err != nil {
		t.Fatalf("update runtime event payload: %v", err)
	}
	events, err := store.ListRuntimeEvents(0, 0)
	if err != nil {
		t.Fatalf("ListRuntimeEvents default limit: %v", err)
	}
	foundInvalidPayload := false
	for _, event := range events {
		if event.IssueID == blockedIssue.ID && event.Payload == nil {
			foundInvalidPayload = true
			break
		}
	}
	if !foundInvalidPayload {
		t.Fatalf("expected invalid runtime event payload to be dropped, got %#v", events)
	}
	if limitedEvents, err := store.ListRuntimeEvents(0, 501); err != nil {
		t.Fatalf("ListRuntimeEvents capped limit: %v", err)
	} else if len(limitedEvents) == 0 {
		t.Fatal("expected capped runtime event query to return rows")
	}

	issueEvents, err := store.ListIssueRuntimeEvents(blockedIssue.ID, 0)
	if err != nil {
		t.Fatalf("ListIssueRuntimeEvents default limit: %v", err)
	}
	if len(issueEvents) == 0 || issueEvents[0].Payload != nil {
		t.Fatalf("expected invalid issue runtime payload to be dropped, got %#v", issueEvents)
	}
	if limitedIssueEvents, err := store.ListIssueRuntimeEvents(blockedIssue.ID, 201); err != nil {
		t.Fatalf("ListIssueRuntimeEvents capped limit: %v", err)
	} else if len(limitedIssueEvents) == 0 {
		t.Fatal("expected capped issue runtime query to return rows")
	}

	if count, err := store.RecomputeProjectIssueTokenSpend(customProject.ID); err != nil {
		t.Fatalf("RecomputeProjectIssueTokenSpend: %v", err)
	} else if count != 3 {
		t.Fatalf("RecomputeProjectIssueTokenSpend count = %d, want 3", count)
	}
	if count, err := store.RecomputeAllIssueTokenSpend(); err != nil {
		t.Fatalf("RecomputeAllIssueTokenSpend: %v", err)
	} else if count != 4 {
		t.Fatalf("RecomputeAllIssueTokenSpend count = %d, want 4", count)
	}
	if current, err := store.GetIssue(readyIssue.ID); err != nil {
		t.Fatalf("GetIssue ready after recompute: %v", err)
	} else if current.TotalTokensSpent != 18 {
		t.Fatalf("ready issue total tokens = %d, want 18", current.TotalTokensSpent)
	}
	if current, err := store.GetIssue(blockedIssue.ID); err != nil {
		t.Fatalf("GetIssue blocked after recompute: %v", err)
	} else if current.TotalTokensSpent != 7 {
		t.Fatalf("blocked issue total tokens = %d, want 7", current.TotalTokensSpent)
	}
	if current, err := store.GetIssue(recurringIssue.ID); err != nil {
		t.Fatalf("GetIssue recurring after recompute: %v", err)
	} else if current.TotalTokensSpent != 5 {
		t.Fatalf("recurring issue total tokens = %d, want 5", current.TotalTokensSpent)
	}
	if current, err := store.GetIssue(defaultIssue.ID); err != nil {
		t.Fatalf("GetIssue default after recompute: %v", err)
	} else if current.TotalTokensSpent != 9 {
		t.Fatalf("default issue total tokens = %d, want 9", current.TotalTokensSpent)
	}

	labels, blockers, unresolved, err := store.issueRelations(nil)
	if err != nil {
		t.Fatalf("issueRelations empty: %v", err)
	}
	if len(labels) != 0 || len(blockers) != 0 || len(unresolved) != 0 {
		t.Fatalf("expected empty issue relations, got labels=%#v blockers=%#v unresolved=%#v", labels, blockers, unresolved)
	}
	labels, blockers, unresolved, err = store.issueRelations([]string{readyIssue.ID, blockedIssue.ID, recurringIssue.ID})
	if err != nil {
		t.Fatalf("issueRelations populated: %v", err)
	}
	if got := len(labels[blockedIssue.ID]); got != 2 {
		t.Fatalf("expected two labels on blocked issue, got %d", got)
	}
	if got := len(blockers[blockedIssue.ID]); got != 1 || blockers[blockedIssue.ID][0] != readyIssue.Identifier {
		t.Fatalf("expected one blocker on blocked issue, got %#v", blockers[blockedIssue.ID])
	}
	if !unresolved[blockedIssue.ID] {
		t.Fatalf("expected blocked issue to be unresolved, got %#v", unresolved)
	}
	if blocked, err := store.isIssueBlocked(blockedIssue.ID); err != nil || !blocked {
		t.Fatalf("expected blocked issue to be blocked, got blocked=%v err=%v", blocked, err)
	}
	if blocked, err := store.isIssueBlocked(readyIssue.ID); err != nil || blocked {
		t.Fatalf("expected ready issue to be unblocked, got blocked=%v err=%v", blocked, err)
	}

	sortCases := []IssueQuery{
		{ProjectID: customProject.ID, Limit: 10},
		{ProjectID: customProject.ID, Sort: "created_asc", Limit: 10},
		{ProjectID: customProject.ID, Sort: "priority_asc", Limit: 10},
		{ProjectID: customProject.ID, Sort: "identifier_asc", Limit: 10},
		{ProjectID: customProject.ID, Sort: "state_asc", Limit: 10},
	}
	for _, query := range sortCases {
		items, total, err := store.ListIssueSummaries(query)
		if err != nil {
			t.Fatalf("ListIssueSummaries sort %q: %v", query.Sort, err)
		}
		if total != 3 || len(items) != 3 {
			t.Fatalf("expected three custom project issues for sort %q, got total=%d items=%d", query.Sort, total, len(items))
		}
	}

	blockedOnly := true
	if items, total, err := store.ListIssueSummaries(IssueQuery{
		ProjectName: customProject.Name,
		EpicID:      customEpic.ID,
		State:       string(StateReady),
		Search:      "needle",
		Blocked:     &blockedOnly,
		Sort:        "identifier_asc",
		Limit:       10,
	}); err != nil {
		t.Fatalf("ListIssueSummaries project name blocked: %v", err)
	} else if total != 1 || len(items) != 1 || items[0].Identifier != blockedIssue.Identifier {
		t.Fatalf("unexpected blocked issue summary query result: total=%d items=%#v", total, items)
	}
	blockedOnly = false
	if items, total, err := store.ListIssueSummaries(IssueQuery{
		ProjectID: customProject.ID,
		IssueType: string(IssueTypeRecurring),
		Search:    "needle",
		Blocked:   &blockedOnly,
		Sort:      "created_asc",
		Limit:     0,
		Offset:    -5,
	}); err != nil {
		t.Fatalf("ListIssueSummaries recurring: %v", err)
	} else if total != 1 || len(items) != 1 || items[0].Identifier != recurringIssue.Identifier {
		t.Fatalf("unexpected recurring issue summary query result: total=%d items=%#v", total, items)
	}

	projectSummaries, err := store.ListProjectSummaries()
	if err != nil {
		t.Fatalf("ListProjectSummaries: %v", err)
	}
	if len(projectSummaries) != 2 {
		t.Fatalf("expected two project summaries, got %#v", projectSummaries)
	}
	if projectSummaries[0].Project.ID == customProject.ID {
		if projectSummaries[0].Counts.Ready != 2 || projectSummaries[0].TotalCount != 2 || projectSummaries[0].ActiveCount != 2 || projectSummaries[0].TerminalCount != 0 {
			t.Fatalf("unexpected custom project summary: %#v", projectSummaries[0])
		}
		if projectSummaries[0].TotalTokensSpent != 30 {
			t.Fatalf("unexpected custom project token total: %#v", projectSummaries[0])
		}
	} else if projectSummaries[1].Project.ID == customProject.ID {
		if projectSummaries[1].Counts.Ready != 2 || projectSummaries[1].TotalCount != 2 || projectSummaries[1].ActiveCount != 2 || projectSummaries[1].TerminalCount != 0 {
			t.Fatalf("unexpected custom project summary: %#v", projectSummaries[1])
		}
		if projectSummaries[1].TotalTokensSpent != 30 {
			t.Fatalf("unexpected custom project token total: %#v", projectSummaries[1])
		}
	} else {
		t.Fatalf("expected custom project summary to be present, got %#v", projectSummaries)
	}

	epicSummaries, err := store.ListEpicSummaries(customProject.ID)
	if err != nil {
		t.Fatalf("ListEpicSummaries custom project: %v", err)
	}
	if len(epicSummaries) != 2 {
		t.Fatalf("expected two custom epic summaries, got %#v", epicSummaries)
	}
	if epicSummaries[0].Epic.ID == customEpic.ID {
		if epicSummaries[0].Counts.Ready != 2 || epicSummaries[0].TotalCount != 2 || epicSummaries[0].ActiveCount != 2 || epicSummaries[0].TerminalCount != 0 {
			t.Fatalf("unexpected custom epic summary: %#v", epicSummaries[0])
		}
	} else if epicSummaries[1].Epic.ID == customEpic.ID {
		if epicSummaries[1].Counts.Ready != 2 || epicSummaries[1].TotalCount != 2 || epicSummaries[1].ActiveCount != 2 || epicSummaries[1].TerminalCount != 0 {
			t.Fatalf("unexpected custom epic summary: %#v", epicSummaries[1])
		}
	} else {
		t.Fatalf("expected custom epic summary to be present, got %#v", epicSummaries)
	}
	if epicSummaries[0].Epic.ID == emptyEpic.ID {
		if epicSummaries[0].TotalCount != 0 || epicSummaries[0].ActiveCount != 0 || epicSummaries[0].TerminalCount != 0 {
			t.Fatalf("expected empty epic summary to stay empty, got %#v", epicSummaries[0])
		}
	} else if epicSummaries[1].Epic.ID == emptyEpic.ID {
		if epicSummaries[1].TotalCount != 0 || epicSummaries[1].ActiveCount != 0 || epicSummaries[1].TerminalCount != 0 {
			t.Fatalf("expected empty epic summary to stay empty, got %#v", epicSummaries[1])
		}
	} else {
		t.Fatalf("expected empty epic summary to be present, got %#v", epicSummaries)
	}

	if allEpicSummaries, err := store.ListEpicSummaries(""); err != nil {
		t.Fatalf("ListEpicSummaries all projects: %v", err)
	} else if len(allEpicSummaries) != 3 {
		t.Fatalf("expected three epic summaries across all projects, got %#v", allEpicSummaries)
	}

	detail, err := store.GetIssueDetailByIdentifier(blockedIssue.Identifier)
	if err != nil {
		t.Fatalf("GetIssueDetailByIdentifier: %v", err)
	}
	if detail.ProjectName != customProject.Name || detail.EpicName != customEpic.Name {
		t.Fatalf("unexpected issue detail project/epic names: %#v", detail)
	}
	if detail.WorkspacePath != renamedWorkspacePath || detail.WorkspaceRunCount != 1 {
		t.Fatalf("unexpected issue detail workspace fields: %#v", detail)
	}
	if !detail.IsBlocked || len(detail.Assets) != 1 {
		t.Fatalf("expected blocked issue detail to include blockers and assets: %#v", detail)
	}

	if err := store.DeleteIssueAsset(blockedIssue.ID, asset.ID); err != nil {
		t.Fatalf("DeleteIssueAsset: %v", err)
	}
	if _, err := store.GetIssueAsset(blockedIssue.ID, asset.ID); !IsNotFound(err) {
		t.Fatalf("expected deleted asset to be missing, got %v", err)
	}
	if _, err := os.Stat(assetPath); !os.IsNotExist(err) {
		t.Fatalf("expected asset file to be cleaned up, got %v", err)
	}

	if err := store.DeleteWorkspace(blockedIssue.ID); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	if _, err := store.GetWorkspace(blockedIssue.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected deleted workspace to be missing, got %v", err)
	}
	if _, err := os.Stat(renamedWorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("expected workspace directory to be cleaned up, got %v", err)
	}

	if err := store.ClearIssuePendingPlanRevision(blockedIssue.ID, "manual_retry"); err != nil {
		t.Fatalf("ClearIssuePendingPlanRevision no-op path: %v", err)
	}
}

func TestKanbanCommentAttachmentHelperCoverage(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Comment helper issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if got := normalizeIssueCommentAttachmentFilename(""); got != "attachment" {
		t.Fatalf("normalizeIssueCommentAttachmentFilename empty = %q", got)
	}
	if got := normalizeIssueCommentAttachmentFilename("nested/path.txt"); got != "path.txt" {
		t.Fatalf("normalizeIssueCommentAttachmentFilename nested = %q", got)
	}

	var limited limitedBuffer
	if n, err := limited.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("limitedBuffer.Write(limit=0) = (%d, %v)", n, err)
	}
	if len(limited.Bytes()) != 0 {
		t.Fatalf("limitedBuffer.Write(limit=0) stored bytes = %q", limited.Bytes())
	}
	limited.limit = 3
	if n, err := limited.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("limitedBuffer.Write(limit=3) = (%d, %v)", n, err)
	}
	if got := string(limited.Bytes()); got != "abc" {
		t.Fatalf("limitedBuffer.Write(limit=3) = %q", got)
	}

	if got := commentBodyValue(nil); got != "" {
		t.Fatalf("commentBodyValue(nil) = %q", got)
	}
	body := "  keep the spacing  "
	if got := commentBodyValue(&body); got != body {
		t.Fatalf("commentBodyValue(body) = %q", got)
	}

	author := normalizeLocalIssueCommentAuthor(IssueCommentAuthor{})
	if author.Type != "source" || author.Name != "System" {
		t.Fatalf("normalizeLocalIssueCommentAuthor blank = %#v", author)
	}
	author = normalizeLocalIssueCommentAuthor(IssueCommentAuthor{Name: "  Alice  ", Type: "  user  ", Email: "  alice@example.com  "})
	if author.Type != "user" || author.Name != "Alice" || author.Email != "alice@example.com" {
		t.Fatalf("normalizeLocalIssueCommentAuthor trimmed = %#v", author)
	}

	if !containsIssueCommentAttachmentID([]string{"a", "b"}, " b ") {
		t.Fatal("containsIssueCommentAttachmentID should match trimmed ids")
	}

	comments := nestIssueComments([]IssueComment{
		{ID: "parent", ParentCommentID: ""},
		{ID: "child", ParentCommentID: "parent"},
		{ID: "orphan", ParentCommentID: "missing"},
	})
	if len(comments) != 2 {
		t.Fatalf("nestIssueComments len = %d, want 2", len(comments))
	}
	if len(comments[0].Replies) != 1 || comments[0].Replies[0].ID != "child" {
		t.Fatalf("nestIssueComments parent replies = %#v", comments[0].Replies)
	}
	if comments[1].ID != "orphan" || comments[1].ParentCommentID != "" {
		t.Fatalf("nestIssueComments orphan handling = %#v", comments[1])
	}

	root := t.TempDir()
	contentType, byteSize, tempPath, err := copyIssueCommentAttachmentTemp(root, strings.NewReader("hello world"), "note.txt", "")
	if err != nil {
		t.Fatalf("copyIssueCommentAttachmentTemp inferred content type: %v", err)
	}
	defer func() { _ = os.Remove(tempPath) }()
	if byteSize != int64(len("hello world")) {
		t.Fatalf("copyIssueCommentAttachmentTemp byte size = %d", byteSize)
	}
	if !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("copyIssueCommentAttachmentTemp inferred content type = %q", contentType)
	}
	if _, err := os.Stat(tempPath); err != nil {
		t.Fatalf("copyIssueCommentAttachmentTemp temp file missing: %v", err)
	}

	_, _, explicitPath, err := copyIssueCommentAttachmentTemp(root, strings.NewReader("hello"), "note.bin", "application/custom")
	if err != nil {
		t.Fatalf("copyIssueCommentAttachmentTemp explicit content type: %v", err)
	}
	defer func() { _ = os.Remove(explicitPath) }()

	if _, _, _, err := copyIssueCommentAttachmentTemp(root, &countingReader{remaining: MaxIssueCommentAttachmentBytes + 1}, "oversize.txt", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected oversize attachment validation error, got %v", err)
	}

	validPath, err := store.issueCommentAttachmentPath(filepath.Join("issue-1", "comment-1", "note.txt"))
	if err != nil {
		t.Fatalf("issueCommentAttachmentPath valid: %v", err)
	}
	if !strings.HasSuffix(validPath, filepath.Join("issue-1", "comment-1", "note.txt")) {
		t.Fatalf("issueCommentAttachmentPath valid = %q", validPath)
	}
	if _, err := store.issueCommentAttachmentPath("../escape"); !errors.Is(err, ErrValidation) {
		t.Fatalf("issueCommentAttachmentPath invalid = %v", err)
	}

	parentBody := "Parent comment"
	parent, err := store.CreateIssueComment(issue.ID, IssueCommentInput{Body: &parentBody})
	if err != nil {
		t.Fatalf("CreateIssueComment parent: %v", err)
	}
	replyBody := "Reply comment"
	reply, err := store.CreateIssueComment(issue.ID, IssueCommentInput{Body: &replyBody, ParentCommentID: parent.ID})
	if err != nil {
		t.Fatalf("CreateIssueComment reply: %v", err)
	}

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	hasReplies, err := issueCommentHasRepliesTx(tx, parent.ID)
	if err != nil {
		t.Fatalf("issueCommentHasRepliesTx parent: %v", err)
	}
	if !hasReplies {
		t.Fatal("expected parent comment to report replies")
	}
	hasReplies, err = issueCommentHasRepliesTx(tx, reply.ID)
	if err != nil {
		t.Fatalf("issueCommentHasRepliesTx reply: %v", err)
	}
	if hasReplies {
		t.Fatal("expected reply comment to report no replies")
	}
}

func TestKanbanStoreCoverageBranches(t *testing.T) {
	t.Run("legacy planning session helper", func(t *testing.T) {
		store := setupTestStore(t)
		issue, err := store.CreateIssue("", "", "Planning helper", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		issue.PendingPlanMarkdown = "Initial plan"
		issue.PendingPlanRequestedAt = &now
		issue.PendingPlanRevisionMarkdown = "Revise the rollout"
		revisionRequestedAt := now.Add(10 * time.Minute)
		issue.PendingPlanRevisionRequestedAt = &revisionRequestedAt

		tx, err := store.db.Begin()
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		record, created, err := store.ensureLegacyIssuePlanSessionTx(tx, nil, now)
		if err != nil || created || record != nil {
			t.Fatalf("expected nil issue to be ignored, got record=%#v created=%v err=%v", record, created, err)
		}
		blankIssue := *issue
		blankIssue.PendingPlanMarkdown = ""
		record, created, err = store.ensureLegacyIssuePlanSessionTx(tx, &blankIssue, now)
		if err != nil || created || record != nil {
			t.Fatalf("expected blank pending plan to be ignored, got record=%#v created=%v err=%v", record, created, err)
		}
		record, created, err = store.ensureLegacyIssuePlanSessionTx(tx, issue, now.Add(time.Hour))
		if err != nil {
			t.Fatalf("ensureLegacyIssuePlanSessionTx create: %v", err)
		}
		if !created || record == nil {
			t.Fatalf("expected plan session to be created, got record=%#v created=%v", record, created)
		}
		if record.Status != IssuePlanningStatusRevisionRequested {
			t.Fatalf("expected revision requested status, got %#v", record)
		}
		if record.PendingRevisionRequestedAt == nil || !record.PendingRevisionRequestedAt.Equal(revisionRequestedAt.UTC()) {
			t.Fatalf("expected revision requested timestamp, got %#v", record.PendingRevisionRequestedAt)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit plan session tx: %v", err)
		}

		tx2, err := store.db.Begin()
		if err != nil {
			t.Fatalf("Begin existing: %v", err)
		}
		existing, created, err := store.ensureLegacyIssuePlanSessionTx(tx2, issue, now.Add(2*time.Hour))
		if err != nil {
			t.Fatalf("ensureLegacyIssuePlanSessionTx existing: %v", err)
		}
		if created || existing == nil || existing.ID != record.ID {
			t.Fatalf("expected existing open plan session to be reused, got record=%#v created=%v", existing, created)
		}
		if err := tx2.Rollback(); err != nil {
			t.Fatalf("rollback existing tx: %v", err)
		}
	})

	t.Run("workflow, dispatch, and issue list filters", func(t *testing.T) {
		store := setupTestStore(t)
		orphanIssue, err := store.CreateIssue("", "", "Orphan issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue orphan: %v", err)
		}
		project, err := store.CreateProject("Dispatch project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		epic, err := store.CreateEpic(project.ID, "Epic", "")
		if err != nil {
			t.Fatalf("CreateEpic: %v", err)
		}
		backlogIssue, err := store.CreateIssue(project.ID, "", "Backlog issue", "", 1, nil)
		if err != nil {
			t.Fatalf("CreateIssue backlog: %v", err)
		}
		if backlogIssue.State != StateBacklog {
			t.Fatalf("expected backlog issue to start in backlog, got %s", backlogIssue.State)
		}
		readyIssue, err := store.CreateIssue(project.ID, epic.ID, "Ready issue", "", 2, nil)
		if err != nil {
			t.Fatalf("CreateIssue ready: %v", err)
		}
		if err := store.UpdateIssueState(readyIssue.ID, StateReady); err != nil {
			t.Fatalf("UpdateIssueState ready: %v", err)
		}
		doneIssue, err := store.CreateIssue(project.ID, "", "Done issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue done: %v", err)
		}
		if err := store.UpdateIssueState(doneIssue.ID, StateDone); err != nil {
			t.Fatalf("UpdateIssueState done: %v", err)
		}
		nextDue, err := store.NextRecurringDueAt("")
		if err != nil {
			t.Fatalf("NextRecurringDueAt initial: %v", err)
		}
		if nextDue != nil {
			t.Fatalf("expected no next recurring issue due before scheduling, got %v", nextDue)
		}
		recurringIssue, err := store.CreateIssueWithOptions(project.ID, epic.ID, "Recurring issue", "", 3, nil, IssueCreateOptions{
			IssueType: IssueTypeRecurring,
			Cron:      "0 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions recurring: %v", err)
		}
		if err := store.SetIssuePendingPlanApproval(readyIssue.ID, "Approve this plan", time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("SetIssuePendingPlanApproval: %v", err)
		}

		dispatch, err := store.GetIssueDispatchState(orphanIssue.ID)
		if err != nil {
			t.Fatalf("GetIssueDispatchState no project: %v", err)
		}
		if dispatch.ProjectExists || dispatch.ProjectState != ProjectStateStopped || dispatch.HasUnresolvedBlockers {
			t.Fatalf("unexpected dispatch state without project: %#v", dispatch)
		}

		blocker, err := store.CreateIssue(project.ID, "", "Blocker", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocker: %v", err)
		}
		if _, err := store.SetIssueBlockers(readyIssue.ID, []string{blocker.Identifier}); err != nil {
			t.Fatalf("SetIssueBlockers: %v", err)
		}
		if err := store.UpdateProjectState(project.ID, ProjectStateRunning); err != nil {
			t.Fatalf("UpdateProjectState: %v", err)
		}

		dispatch, err = store.GetIssueDispatchState(readyIssue.ID)
		if err != nil {
			t.Fatalf("GetIssueDispatchState project: %v", err)
		}
		if !dispatch.ProjectExists || dispatch.ProjectState != ProjectStateRunning || !dispatch.HasUnresolvedBlockers {
			t.Fatalf("unexpected dispatch state with blocker: %#v", dispatch)
		}

		if _, err := store.GetIssueDispatchState("missing"); !IsNotFound(err) {
			t.Fatalf("expected missing issue dispatch lookup to fail, got %v", err)
		}

		issues, err := store.ListIssues(nil)
		if err != nil {
			t.Fatalf("ListIssues nil filter: %v", err)
		}
		if len(issues) < 5 {
			t.Fatalf("expected issues from helper setup, got %#v", issues)
		}
		if filtered, err := store.ListIssues(map[string]interface{}{"project_id": project.ID}); err != nil || len(filtered) != 5 {
			t.Fatalf("ListIssues(project_id) = %#v, err=%v", filtered, err)
		}
		if filtered, err := store.ListIssues(map[string]interface{}{"provider_kind": ProviderKindKanban}); err != nil || len(filtered) != 6 {
			t.Fatalf("ListIssues(provider_kind) = %#v, err=%v", filtered, err)
		}
		if filtered, err := store.ListIssues(map[string]interface{}{"issue_type": string(IssueTypeRecurring)}); err != nil || len(filtered) != 1 || filtered[0].Identifier != recurringIssue.Identifier {
			t.Fatalf("ListIssues(issue_type) = %#v, err=%v", filtered, err)
		}
		if filtered, err := store.ListIssues(map[string]interface{}{"epic_id": epic.ID}); err != nil || len(filtered) != 2 {
			t.Fatalf("ListIssues(epic_id) = %#v, err=%v", filtered, err)
		}
		if filtered, err := store.ListIssues(map[string]interface{}{"state": string(StateReady)}); err != nil || len(filtered) != 1 || filtered[0].Identifier != readyIssue.Identifier {
			t.Fatalf("ListIssues(state) = %#v, err=%v", filtered, err)
		}
		if filtered, err := store.ListIssues(map[string]interface{}{"states": []string{string(StateDone), string(StateReady)}}); err != nil || len(filtered) != 2 {
			t.Fatalf("ListIssues(states) = %#v, err=%v", filtered, err)
		}
		if filtered, err := store.ListIssues(map[string]interface{}{"plan_approval_pending": true}); err != nil || len(filtered) != 1 || filtered[0].Identifier != readyIssue.Identifier {
			t.Fatalf("ListIssues(plan_approval_pending) = %#v, err=%v", filtered, err)
		}

		if err := store.UpdateIssueWorkflowPhase(readyIssue.ID, WorkflowPhaseReview); err != nil {
			t.Fatalf("UpdateIssueWorkflowPhase valid: %v", err)
		}
		issue, err := store.GetIssue(readyIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue after valid phase update: %v", err)
		}
		if issue.WorkflowPhase != WorkflowPhaseReview {
			t.Fatalf("expected review phase, got %s", issue.WorkflowPhase)
		}
		if err := store.UpdateIssueWorkflowPhase(doneIssue.ID, WorkflowPhase("invalid")); err != nil {
			t.Fatalf("UpdateIssueWorkflowPhase invalid: %v", err)
		}
		issue, err = store.GetIssue(doneIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue after fallback phase update: %v", err)
		}
		if issue.WorkflowPhase != WorkflowPhaseComplete {
			t.Fatalf("expected fallback complete phase, got %s", issue.WorkflowPhase)
		}
		if err := store.UpdateIssueWorkflowPhase("missing", WorkflowPhaseReview); !IsNotFound(err) {
			t.Fatalf("expected missing issue phase update to fail, got %v", err)
		}

		if recurringIssue.NextRunAt == nil {
			t.Fatal("expected recurring issue to have an initial next run")
		}
		nextDue, err = store.NextRecurringDueAt(project.RepoPath)
		if err != nil {
			t.Fatalf("NextRecurringDueAt filtered: %v", err)
		}
		if nextDue == nil || !nextDue.Equal(recurringIssue.NextRunAt.UTC()) {
			t.Fatalf("expected next recurring due %v, got %#v", recurringIssue.NextRunAt, nextDue)
		}
		nextRunAt := time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)
		if err := store.RearmRecurringIssue(recurringIssue.ID, time.Date(2026, 3, 10, 14, 30, 0, 0, time.UTC), &nextRunAt); err != nil {
			t.Fatalf("RearmRecurringIssue: %v", err)
		}
		nextDue, err = store.NextRecurringDueAt(project.RepoPath)
		if err != nil {
			t.Fatalf("NextRecurringDueAt filtered: %v", err)
		}
		if nextDue == nil || !nextDue.Equal(nextRunAt.UTC()) {
			t.Fatalf("expected next recurring due %v, got %#v", nextRunAt, nextDue)
		}
	})

	t.Run("issue assets and commands", func(t *testing.T) {
		store := setupTestStore(t)
		issue, err := store.CreateIssue("", "", "Assets and commands", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}

		asset, err := store.CreateIssueAsset(issue.ID, "preview", bytes.NewReader(samplePNGBytes()))
		if err != nil {
			t.Fatalf("CreateIssueAsset inferred extension: %v", err)
		}
		if !strings.HasSuffix(asset.StoragePath, ".png") {
			t.Fatalf("expected guessed png extension, got %q", asset.StoragePath)
		}
		if _, err := store.CreateIssueAsset("", "preview.png", bytes.NewReader(samplePNGBytes())); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected missing issue validation error, got %v", err)
		}
		if _, err := store.CreateIssueAsset(issue.ID, "preview.png", nil); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected nil asset reader validation error, got %v", err)
		}
		if _, err := store.CreateIssueAsset("missing", "preview.png", bytes.NewReader(samplePNGBytes())); !IsNotFound(err) {
			t.Fatalf("expected missing issue asset lookup to fail, got %v", err)
		}

		command, err := store.CreateIssueAgentCommand(issue.ID, "Run the check.", IssueAgentCommandPending)
		if err != nil {
			t.Fatalf("CreateIssueAgentCommand: %v", err)
		}
		if err := store.UpdateIssueAgentCommandStatus("", IssueAgentCommandDelivered); err == nil {
			t.Fatal("expected missing id to fail")
		}
		if err := store.UpdateIssueAgentCommandStatus(command.ID, ""); err == nil {
			t.Fatal("expected missing status to fail")
		}
		if err := store.UpdateIssueAgentCommandStatus("missing", IssueAgentCommandDelivered); !IsNotFound(err) {
			t.Fatalf("expected missing command lookup to fail, got %v", err)
		}
		if err := store.UpdateIssueAgentCommandStatus(command.ID, IssueAgentCommandWaitingForUnblock); err != nil {
			t.Fatalf("UpdateIssueAgentCommandStatus success: %v", err)
		}

		command, err = store.CreateIssueAgentCommand(issue.ID, "Ship the fix.", IssueAgentCommandPending)
		if err != nil {
			t.Fatalf("CreateIssueAgentCommand second: %v", err)
		}
		if err := store.MarkIssueAgentCommandsDeliveredIfUnchanged("", nil, "", "", 0); err != nil {
			t.Fatalf("MarkIssueAgentCommandsDeliveredIfUnchanged empty should be no-op: %v", err)
		}
		if err := store.MarkIssueAgentCommandsDeliveredIfUnchanged(issue.ID, []IssueAgentCommand{*command}, "next_run", "thread-1", 1); err != nil {
			t.Fatalf("MarkIssueAgentCommandsDeliveredIfUnchanged success: %v", err)
		}
		commands, err := store.ListIssueAgentCommands(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueAgentCommands: %v", err)
		}
		if len(commands) != 2 {
			t.Fatalf("expected two commands, got %#v", commands)
		}
		delivered := false
		for _, item := range commands {
			if item.ID == command.ID {
				if item.Status != IssueAgentCommandDelivered || item.DeliveredAt == nil {
					t.Fatalf("expected delivered command, got %#v", item)
				}
				delivered = true
			}
		}
		if !delivered {
			t.Fatal("expected delivered command to be persisted")
		}
	})

	t.Run("maintenance and stats", func(t *testing.T) {
		store := setupTestStore(t)
		stats, err := store.DBStats()
		if err != nil {
			t.Fatalf("DBStats: %v", err)
		}
		if stats.PageCount <= 0 || stats.PageSize <= 0 {
			t.Fatalf("expected positive DB stats, got %#v", stats)
		}

		closedStore := setupTestStore(t)
		if err := closedStore.Close(); err != nil {
			t.Fatalf("Close store: %v", err)
		}
		if _, err := closedStore.DBStats(); err == nil {
			t.Fatal("expected DBStats on closed store to fail")
		}
		for _, tc := range []struct {
			name        string
			failPattern string
		}{
			{name: "page size", failPattern: "pragma page_size"},
			{name: "freelist count", failPattern: "pragma freelist_count"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				faulty := openFaultyMigratedSQLiteStoreAt(t, filepath.Join(t.TempDir(), tc.name+".db"), tc.failPattern)
				if _, err := faulty.DBStats(); err == nil {
					t.Fatalf("expected DBStats to fail when %s is injected", tc.failPattern)
				}
			})
		}
		if _, err := closedStore.RunMaintenance(nil); err == nil {
			t.Fatal("expected RunMaintenance on closed store to fail")
		}

		if err := store.AddIssueTokenSpend("missing", 5); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected missing token spend update to return sql.ErrNoRows, got %v", err)
		}
		issue, err := store.CreateIssue("", "", "Token issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := store.AddIssueTokenSpend(issue.ID, 0); err != nil {
			t.Fatalf("AddIssueTokenSpend zero delta: %v", err)
		}
		if err := store.AddIssueTokenSpend(issue.ID, 7); err != nil {
			t.Fatalf("AddIssueTokenSpend delta: %v", err)
		}
		updated, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue: %v", err)
		}
		if updated.TotalTokensSpent != 7 {
			t.Fatalf("expected token spend to increment, got %d", updated.TotalTokensSpent)
		}

		protectedIssue, err := store.CreateIssue("", "", "Protected maintenance issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue protected maintenance: %v", err)
		}
		staleIssue, err := store.CreateIssue("", "", "Stale maintenance issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue stale maintenance: %v", err)
		}
		cutoff := time.Now().UTC().AddDate(0, 0, -40)
		for _, item := range []*Issue{protectedIssue, staleIssue} {
			if _, err := store.db.Exec(`INSERT INTO runtime_events (kind, issue_id, identifier, event_ts, payload_json) VALUES (?, ?, ?, ?, '{}')`,
				"run_started", item.ID, item.Identifier, cutoff.Add(-time.Minute),
			); err != nil {
				t.Fatalf("insert runtime event: %v", err)
			}
			if err := store.appendChange("issue", item.ID, "updated", map[string]interface{}{"maintenance": true}); err != nil {
				t.Fatalf("append change event: %v", err)
			}
			if err := store.ApplyIssueActivityEvent(item.ID, item.Identifier, 1, agentruntime.ActivityEvent{
				Type:     "turn.started",
				ThreadID: "thread-" + item.Identifier,
				TurnID:   "turn-" + item.Identifier,
			}); err != nil {
				t.Fatalf("ApplyIssueActivityEvent maintenance: %v", err)
			}
			if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
				IssueID:    item.ID,
				Identifier: item.Identifier,
				Phase:      "implementation",
				Attempt:    1,
				RunKind:    "run_started",
				UpdatedAt:  time.Now().UTC(),
				AppSession: appserver.Session{
					IssueID:         item.ID,
					IssueIdentifier: item.Identifier,
					SessionID:       item.Identifier + "-session",
					ThreadID:        "thread-" + item.Identifier,
					TurnID:          "turn-" + item.Identifier,
					LastEvent:       "turn.started",
					LastMessage:     "running",
				},
			}); err != nil {
				t.Fatalf("UpsertIssueExecutionSession maintenance: %v", err)
			}
		}
		if _, err := store.db.Exec(`UPDATE runtime_events SET event_ts = ? WHERE issue_id IN (?, ?)`, cutoff, protectedIssue.ID, staleIssue.ID); err != nil {
			t.Fatalf("update runtime events cutoff: %v", err)
		}
		if _, err := store.db.Exec(`UPDATE change_events SET event_ts = ? WHERE entity_id IN (?, ?)`, cutoff, protectedIssue.ID, staleIssue.ID); err != nil {
			t.Fatalf("update change events cutoff: %v", err)
		}
		if _, err := store.db.Exec(`UPDATE issue_activity_updates SET event_ts = ? WHERE issue_id IN (?, ?)`, cutoff, protectedIssue.ID, staleIssue.ID); err != nil {
			t.Fatalf("update activity updates cutoff: %v", err)
		}
		if _, err := store.db.Exec(`UPDATE issue_activity_entries SET updated_at = ? WHERE issue_id IN (?, ?)`, cutoff, protectedIssue.ID, staleIssue.ID); err != nil {
			t.Fatalf("update activity entries cutoff: %v", err)
		}
		if _, err := store.db.Exec(`UPDATE issue_execution_sessions SET updated_at = ? WHERE issue_id IN (?, ?)`, cutoff, protectedIssue.ID, staleIssue.ID); err != nil {
			t.Fatalf("update execution sessions cutoff: %v", err)
		}

		result, err := store.RunMaintenance([]string{protectedIssue.ID})
		if err != nil {
			t.Fatalf("RunMaintenance success: %v", err)
		}
		if result.StartedAt.IsZero() || result.CheckpointAt.IsZero() || result.CheckpointResult == "" {
			t.Fatalf("expected maintenance result metadata, got %#v", result)
		}
		for _, tc := range []struct {
			name  string
			query string
			args  []interface{}
			want  int
		}{
			{name: "protected runtime", query: `SELECT COUNT(*) FROM runtime_events WHERE issue_id = ?`, args: []interface{}{protectedIssue.ID}, want: 1},
			{name: "stale runtime", query: `SELECT COUNT(*) FROM runtime_events WHERE issue_id = ?`, args: []interface{}{staleIssue.ID}, want: 0},
			{name: "protected change", query: `SELECT COUNT(*) FROM change_events WHERE entity_id = ?`, args: []interface{}{protectedIssue.ID}, want: 0},
			{name: "stale change", query: `SELECT COUNT(*) FROM change_events WHERE entity_id = ?`, args: []interface{}{staleIssue.ID}, want: 0},
			{name: "protected activity updates", query: `SELECT COUNT(*) FROM issue_activity_updates WHERE issue_id = ?`, args: []interface{}{protectedIssue.ID}, want: 1},
			{name: "stale activity updates", query: `SELECT COUNT(*) FROM issue_activity_updates WHERE issue_id = ?`, args: []interface{}{staleIssue.ID}, want: 0},
			{name: "protected activity entries", query: `SELECT COUNT(*) FROM issue_activity_entries WHERE issue_id = ?`, args: []interface{}{protectedIssue.ID}, want: 1},
			{name: "stale activity entries", query: `SELECT COUNT(*) FROM issue_activity_entries WHERE issue_id = ?`, args: []interface{}{staleIssue.ID}, want: 0},
			{name: "protected sessions", query: `SELECT COUNT(*) FROM issue_execution_sessions WHERE issue_id = ?`, args: []interface{}{protectedIssue.ID}, want: 1},
			{name: "stale sessions", query: `SELECT COUNT(*) FROM issue_execution_sessions WHERE issue_id = ?`, args: []interface{}{staleIssue.ID}, want: 0},
		} {
			var got int
			if err := store.db.QueryRow(tc.query, tc.args...).Scan(&got); err != nil {
				t.Fatalf("%s query: %v", tc.name, err)
			}
			if got != tc.want {
				t.Fatalf("%s = %d, want %d", tc.name, got, tc.want)
			}
		}
	})

	t.Run("transaction helpers and paths", func(t *testing.T) {
		store := setupTestStore(t)
		project, err := store.CreateProject("Helper project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject helper: %v", err)
		}
		if got, err := store.generateIdentifier(""); err != nil || got != "ISS-1" {
			t.Fatalf("generateIdentifier blank = %q, %v", got, err)
		}
		if got, err := store.generateIdentifier(project.ID); err != nil || got != "HELP-1" {
			t.Fatalf("generateIdentifier project = %q, %v", got, err)
		}
		if _, err := store.DBStats(); err != nil {
			t.Fatalf("DBStats helper: %v", err)
		}

		issue, err := store.CreateIssue(project.ID, "", "Helper issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue helper: %v", err)
		}
		blockerDone, err := store.CreateIssue("", "", "Resolved blocker", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocker done: %v", err)
		}
		if err := store.UpdateIssueState(blockerDone.ID, StateDone); err != nil {
			t.Fatalf("UpdateIssueState blocker done: %v", err)
		}
		blockerOpen, err := store.CreateIssue("", "", "Open blocker", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocker open: %v", err)
		}
		if err := store.UpdateIssueState(blockerOpen.ID, StateReady); err != nil {
			t.Fatalf("UpdateIssueState blocker open: %v", err)
		}

		tx, err := store.db.Begin()
		if err != nil {
			t.Fatalf("Begin label tx: %v", err)
		}
		if err := replaceIssueLabelsTx(tx, issue.ID, []string{"beta", "alpha", "beta"}); err != nil {
			_ = tx.Rollback()
			t.Fatalf("replaceIssueLabelsTx: %v", err)
		}
		if err := replaceIssueBlockersRawTx(tx, issue.ID, []string{blockerDone.Identifier, " ", blockerOpen.Identifier, blockerOpen.Identifier}); err != nil {
			_ = tx.Rollback()
			t.Fatalf("replaceIssueBlockersRawTx: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit label tx: %v", err)
		}

		loaded, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue helper: %v", err)
		}
		if len(loaded.Labels) != 2 || loaded.Labels[0] != "alpha" || loaded.Labels[1] != "beta" {
			t.Fatalf("unexpected labels after helper tx: %#v", loaded.Labels)
		}
		if len(loaded.BlockedBy) != 2 || loaded.BlockedBy[0] != blockerDone.Identifier || loaded.BlockedBy[1] != blockerOpen.Identifier {
			t.Fatalf("unexpected blockers after helper tx: %#v", loaded.BlockedBy)
		}
		if unresolved, err := store.unresolvedBlockersForIssue(issue.ID); err != nil || len(unresolved) != 1 || unresolved[0] != blockerOpen.Identifier {
			t.Fatalf("unexpected unresolved blockers: %#v err=%v", unresolved, err)
		}
		if err := store.UpdateIssueStateAndPhase(issue.ID, StateInProgress, WorkflowPhaseImplementation); !IsBlockedTransition(err) {
			t.Fatalf("expected blocked in-progress transition, got %v", err)
		}
		if err := store.UpdateIssueState(blockerOpen.ID, StateDone); err != nil {
			t.Fatalf("UpdateIssueState blocker open done: %v", err)
		}
		if err := store.UpdateIssueStateAndPhase(issue.ID, StateInProgress, WorkflowPhaseImplementation); err != nil {
			t.Fatalf("UpdateIssueStateAndPhase after unblock: %v", err)
		}
		loaded, err = store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue after unblock: %v", err)
		}
		if loaded.StartedAt == nil {
			t.Fatal("expected started_at after in-progress transition")
		}

		planIssue, err := store.CreateIssue("", "", "Plan issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue plan: %v", err)
		}
		requestedAt := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
		if err := store.SetIssuePendingPlanApproval(planIssue.ID, "Initial plan", requestedAt); err != nil {
			t.Fatalf("SetIssuePendingPlanApproval helper: %v", err)
		}
		if err := store.SetIssuePendingPlanRevision(planIssue.ID, "Revision note", requestedAt.Add(time.Minute)); err != nil {
			t.Fatalf("SetIssuePendingPlanRevision helper: %v", err)
		}
		tx, err = store.db.Begin()
		if err != nil {
			t.Fatalf("Begin planning tx: %v", err)
		}
		if err := store.deleteIssuePlanningTx(tx, planIssue.ID); err != nil {
			_ = tx.Rollback()
			t.Fatalf("deleteIssuePlanningTx: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit planning tx: %v", err)
		}
		if planning, err := store.GetIssuePlanning(planIssue); err != nil || planning != nil {
			t.Fatalf("expected planning rows to be removed, got %#v err=%v", planning, err)
		}

		assetIssue, err := store.CreateIssue("", "", "Asset issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue asset: %v", err)
		}
		assetOne, err := store.CreateIssueAsset(assetIssue.ID, "preview", bytes.NewReader(samplePNGBytes()))
		if err != nil {
			t.Fatalf("CreateIssueAsset first: %v", err)
		}
		assetTwo, err := store.CreateIssueAsset(assetIssue.ID, "diagram", bytes.NewReader(samplePNGBytes()))
		if err != nil {
			t.Fatalf("CreateIssueAsset second: %v", err)
		}
		if _, err := store.issueAssetPath("../escape"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected invalid issue asset path error, got %v", err)
		}
		tx, err = store.db.Begin()
		if err != nil {
			t.Fatalf("Begin asset tx: %v", err)
		}
		assetPaths, err := store.deleteIssueAssetsTx(tx, assetIssue.ID)
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("deleteIssueAssetsTx: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit asset tx: %v", err)
		}
		store.cleanupIssueAssetPaths(append(assetPaths, assetPaths...))
		if _, err := os.Stat(assetPaths[0]); !os.IsNotExist(err) {
			t.Fatalf("expected asset file cleanup, got %v", err)
		}
		if _, err := store.GetIssueAsset(assetIssue.ID, assetOne.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted asset lookup to fail, got %v", err)
		}
		if _, err := store.GetIssueAsset(assetIssue.ID, assetTwo.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted asset lookup to fail, got %v", err)
		}
		removeIssueAssetFile("")
		removeIfEmpty("")

		commentIssue, err := store.CreateIssue("", "", "Comment issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue comment: %v", err)
		}
		commentAttachmentPath := filepath.Join(t.TempDir(), "comment.txt")
		if err := os.WriteFile(commentAttachmentPath, []byte("comment attachment"), 0o644); err != nil {
			t.Fatalf("WriteFile comment attachment: %v", err)
		}
		commentBody := "Comment body"
		comment, err := store.CreateIssueComment(commentIssue.ID, IssueCommentInput{
			Body: &commentBody,
			Attachments: []IssueCommentAttachmentInput{{
				Path: commentAttachmentPath,
			}},
		})
		if err != nil {
			t.Fatalf("CreateIssueComment helper: %v", err)
		}
		reply, err := store.CreateIssueComment(commentIssue.ID, IssueCommentInput{
			Body:            &commentBody,
			ParentCommentID: comment.ID,
		})
		if err != nil {
			t.Fatalf("CreateIssueComment reply helper: %v", err)
		}
		if _, err := store.issueCommentAttachmentPath("../escape"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected invalid comment attachment path error, got %v", err)
		}
		tx, err = store.db.Begin()
		if err != nil {
			t.Fatalf("Begin comment tx: %v", err)
		}
		commentAttachments, err := store.deleteIssueCommentsTx(tx, commentIssue.ID)
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("deleteIssueCommentsTx: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit comment tx: %v", err)
		}
		store.cleanupIssueCommentAttachmentPaths(append(commentAttachments, commentAttachments...))
		if len(commentAttachments) == 0 {
			t.Fatal("expected comment attachments to be returned for cleanup")
		}
		if _, err := store.GetIssueComment(commentIssue.ID, comment.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted comment lookup to fail, got %v", err)
		}
		if _, err := store.GetIssueComment(commentIssue.ID, reply.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted reply lookup to fail, got %v", err)
		}
	})
}

func TestKanbanCoverageDispatchAndValidationBranches(t *testing.T) {
	store := setupTestStore(t)
	project, err := store.CreateProject("Dispatch Guards", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	issue, err := store.CreateIssue(project.ID, "", "Guard issue", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if dispatch, err := store.ListDispatchIssues([]string{" ", "\t"}); err != nil || len(dispatch) != 0 {
		t.Fatalf("expected blank states to short-circuit, got %#v err=%v", dispatch, err)
	}
	if dispatch, err := store.ListDispatchIssues([]string{string(StateDone)}); err != nil || len(dispatch) != 0 {
		t.Fatalf("expected empty dispatch query to return no rows, got %#v err=%v", dispatch, err)
	}
	if _, err := store.GetIssueDispatchState(""); !IsNotFound(err) {
		t.Fatalf("expected blank dispatch state lookup to be not found, got %v", err)
	}
	if unresolved, err := store.UnresolvedBlockersForIssue(issue.ID); err != nil || len(unresolved) != 0 {
		t.Fatalf("expected issue without blockers to return empty unresolved list, got %#v err=%v", unresolved, err)
	}
	if _, err := store.UnresolvedBlockersForIssue("missing"); !IsNotFound(err) {
		t.Fatalf("expected missing issue blockers lookup to be not found, got %v", err)
	}
	if err := store.ReconcileProviderIssues("", ProviderKindKanban, nil); err == nil {
		t.Fatal("expected blank project_id to fail reconciliation")
	}
	if err := store.ReconcileProviderIssues(project.ID, "", nil); err == nil {
		t.Fatal("expected blank provider_kind to fail reconciliation")
	}
	if err := store.ReconcileProviderIssues(project.ID, ProviderKindKanban, nil); err == nil {
		t.Fatal("expected kanban provider_kind to fail reconciliation")
	}

	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := replaceIssueLabelsTx(tx, issue.ID, nil); err != nil {
		t.Fatalf("replaceIssueLabelsTx empty: %v", err)
	}
	if err := replaceIssueBlockersRawTx(tx, issue.ID, nil); err != nil {
		t.Fatalf("replaceIssueBlockersRawTx empty: %v", err)
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("Rollback: %v", err)
	}
}

func TestKanbanCoverageEdgeBranches(t *testing.T) {
	t.Run("helpers and validation", func(t *testing.T) {
		if got := notFoundError("issue", ""); !IsNotFound(got) || !strings.Contains(got.Error(), "issue") {
			t.Fatalf("unexpected notFoundError output: %v", got)
		}
		if got := blockedInProgressError([]string{"ISS-1"}); !IsBlockedTransition(got) || !strings.Contains(got.Error(), "ISS-1") {
			t.Fatalf("unexpected blockedInProgressError single-blocker output: %v", got)
		}
		if got := blockedInProgressError([]string{"ISS-1", "ISS-2"}); !strings.Contains(got.Error(), "ISS-1, ISS-2") {
			t.Fatalf("unexpected blockedInProgressError multi-blocker output: %v", got)
		}

		if err := ValidateRecurringCron(""); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty cron validation error, got %v", err)
		}
		if err := ValidateRecurringCron("not a cron"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected invalid cron validation error, got %v", err)
		}
		from := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		next, err := NextRecurringRun("0 0 * * *", from, nil)
		if err != nil {
			t.Fatalf("NextRecurringRun valid cron: %v", err)
		}
		if next.IsZero() || next.Before(from.UTC()) {
			t.Fatalf("expected next recurring run after %s, got %v", from, next)
		}
		if _, err := NextRecurringRun("", from, time.UTC); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty next-run cron validation error, got %v", err)
		}
	})

	t.Run("command and session helpers", func(t *testing.T) {
		store := setupTestStore(t)
		issue, err := store.CreateIssue("", "", "Command issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}

		if _, err := store.CreateIssueAgentCommandWithRuntimeEvent("", "run the check", IssueAgentCommandPending, "manual_command_submitted", nil); err == nil {
			t.Fatal("expected missing issue id to fail")
		}
		if _, err := store.CreateIssueAgentCommand(issue.ID, "", IssueAgentCommandPending); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty command validation error, got %v", err)
		}

		command, err := store.CreateIssueAgentCommandWithRuntimeEvent(issue.ID, "Run the check.", IssueAgentCommandPending, "manual_command_submitted", nil)
		if err != nil {
			t.Fatalf("CreateIssueAgentCommandWithRuntimeEvent: %v", err)
		}
		if command.Command != "Run the check." {
			t.Fatalf("unexpected command payload: %#v", command)
		}
		if err := store.MarkIssueAgentCommandsDelivered("", nil, "", "", 0); err != nil {
			t.Fatalf("MarkIssueAgentCommandsDelivered blank no-op: %v", err)
		}
		if err := store.MarkIssueAgentCommandsDelivered(issue.ID, []string{"", command.ID, " "}, "next_run", "thread-1", 2); err != nil {
			t.Fatalf("MarkIssueAgentCommandsDelivered: %v", err)
		}

		delivered, err := store.ListIssueAgentCommands(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueAgentCommands: %v", err)
		}
		if len(delivered) != 1 || delivered[0].Status != IssueAgentCommandDelivered || delivered[0].DeliveredAt == nil {
			t.Fatalf("expected delivered command state, got %#v", delivered)
		}

		deliveryCheck, err := store.CreateIssueAgentCommand(issue.ID, "Review the diff.", IssueAgentCommandPending)
		if err != nil {
			t.Fatalf("CreateIssueAgentCommand deliveryCheck: %v", err)
		}
		if err := store.MarkIssueAgentCommandsDeliveredIfUnchanged(issue.ID, []IssueAgentCommand{{ID: deliveryCheck.ID, Command: "changed"}}, "manual", "thread-2", 1); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected changed command validation error, got %v", err)
		}
		if err := store.MarkIssueAgentCommandsDeliveredIfUnchanged(issue.ID, []IssueAgentCommand{{ID: deliveryCheck.ID, Command: "Review the diff."}}, "manual", "thread-2", 1); err != nil {
			t.Fatalf("MarkIssueAgentCommandsDeliveredIfUnchanged: %v", err)
		}

		editableCommand, err := store.CreateIssueAgentCommand(issue.ID, "Review the patch.", IssueAgentCommandWaitingForUnblock)
		if err != nil {
			t.Fatalf("CreateIssueAgentCommand editable: %v", err)
		}

		if _, err := store.UpdateIssueAgentCommand("", editableCommand.ID, "updated"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty issue validation error, got %v", err)
		}
		if _, err := store.UpdateIssueAgentCommand(issue.ID, "", "updated"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty command id validation error, got %v", err)
		}
		if _, err := store.UpdateIssueAgentCommand(issue.ID, editableCommand.ID, ""); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty update command validation error, got %v", err)
		}
		updatedCommand, err := store.UpdateIssueAgentCommand(issue.ID, editableCommand.ID, "updated review command")
		if err != nil {
			t.Fatalf("UpdateIssueAgentCommand: %v", err)
		}
		if updatedCommand.Command != "updated review command" {
			t.Fatalf("unexpected updated command: %#v", updatedCommand)
		}

		if _, err := store.SteerIssueAgentCommand("", editableCommand.ID); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty issue validation error, got %v", err)
		}
		if _, err := store.SteerIssueAgentCommand(issue.ID, ""); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty command id validation error, got %v", err)
		}
		steeredCommand, err := store.SteerIssueAgentCommand(issue.ID, editableCommand.ID)
		if err != nil {
			t.Fatalf("SteerIssueAgentCommand: %v", err)
		}
		if steeredCommand.SteeredAt == nil {
			t.Fatalf("expected steered command timestamp, got %#v", steeredCommand)
		}

		if err := store.DeleteIssueAgentCommand("", editableCommand.ID); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty issue validation error, got %v", err)
		}
		if err := store.DeleteIssueAgentCommand(issue.ID, ""); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty command id validation error, got %v", err)
		}
		if err := store.DeleteIssueAgentCommand(issue.ID, "missing"); !IsNotFound(err) {
			t.Fatalf("expected missing command not found error, got %v", err)
		}
		if err := store.DeleteIssueAgentCommand(issue.ID, editableCommand.ID); err != nil {
			t.Fatalf("DeleteIssueAgentCommand: %v", err)
		}

		if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{}); err == nil {
			t.Fatal("expected missing issue_id to fail")
		}
		if _, err := store.GetIssueExecutionSession(""); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected blank execution session lookup to fail with sql.ErrNoRows, got %v", err)
		}
		snapshot := ExecutionSessionSnapshot{
			IssueID:        issue.ID,
			Identifier:     issue.Identifier,
			Phase:          "implementation",
			Attempt:        1,
			RunKind:        "run_started",
			ResumeEligible: true,
			UpdatedAt:      time.Time{},
			AppSession: appserver.Session{
				IssueID:         issue.ID,
				IssueIdentifier: issue.Identifier,
				SessionID:       "thread-1-turn-1",
				ThreadID:        "thread-1",
				TurnID:          "turn-1",
				LastEvent:       "turn.started",
				LastMessage:     "Working",
				History: []appserver.Event{
					{Type: "turn.started", Message: "Working"},
				},
			},
		}
		if err := store.UpsertIssueExecutionSession(snapshot); err != nil {
			t.Fatalf("UpsertIssueExecutionSession: %v", err)
		}
		loaded, err := store.GetIssueExecutionSession(issue.ID)
		if err != nil {
			t.Fatalf("GetIssueExecutionSession: %v", err)
		}
		if loaded.UpdatedAt.IsZero() {
			t.Fatal("expected execution session update time to default")
		}
		if loaded.AppSession.SessionID != "thread-1-turn-1" || len(loaded.AppSession.History) != 0 {
			t.Fatalf("expected summarized app session payload, got %#v", loaded.AppSession)
		}
		recent, err := store.ListRecentExecutionSessions(time.Time{}, 0)
		if err != nil {
			t.Fatalf("ListRecentExecutionSessions: %v", err)
		}
		if len(recent) != 1 || recent[0].IssueID != issue.ID {
			t.Fatalf("expected one recent execution snapshot, got %#v", recent)
		}
	})

	t.Run("runtime event and planning fallbacks", func(t *testing.T) {
		store := setupTestStore(t)

		runtimeIssue, err := store.CreateIssue("", "", "Runtime fallback issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue runtime: %v", err)
		}
		seedTS := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Hour)
		inRangeTS := seedTS.Add(2 * time.Hour)
		if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
			"issue_id":     runtimeIssue.ID,
			"identifier":   runtimeIssue.Identifier,
			"attempt":      1,
			"total_tokens": 3,
			"thread_id":    "thread-runtime",
			"ts":           seedTS.Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent seed: %v", err)
		}
		if _, err := store.db.Exec(`
			INSERT INTO runtime_events (kind, issue_id, identifier, title, attempt, delay_type, total_tokens, error, event_ts, payload_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"run_completed", runtimeIssue.ID, runtimeIssue.Identifier, "", 1, "", 5, "", inRangeTS,
			`{"phase":"implementation","thread_id":"thread-runtime"}`,
		); err != nil {
			t.Fatalf("insert runtime completed: %v", err)
		}
		if _, err := store.db.Exec(`
			INSERT INTO runtime_events (kind, issue_id, identifier, title, attempt, delay_type, total_tokens, error, event_ts, payload_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"run_started", runtimeIssue.ID, runtimeIssue.Identifier, "", 1, "", 0, "", seedTS.Add(-15*time.Minute),
			"{invalid",
		); err != nil {
			t.Fatalf("insert invalid runtime payload: %v", err)
		}
		if err := store.AppendRuntimeEvent("retry_paused", map[string]interface{}{
			"issue_id":     runtimeIssue.ID,
			"identifier":   runtimeIssue.Identifier,
			"error":        "plan_approval_pending",
			"total_tokens": 7,
			"thread_id":    "thread-runtime",
			"ts":           inRangeTS.Add(10 * time.Minute).Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent retry_paused: %v", err)
		}
		if err := store.AppendRuntimeEvent("run_failed", map[string]interface{}{
			"issue_id":     runtimeIssue.ID,
			"identifier":   runtimeIssue.Identifier,
			"error":        "stall_timeout",
			"total_tokens": 8,
			"thread_id":    "thread-runtime",
			"ts":           inRangeTS.Add(20 * time.Minute).Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent run_failed: %v", err)
		}
		if err := store.AppendRuntimeEvent("retry_scheduled", map[string]interface{}{
			"issue_id":   runtimeIssue.ID,
			"identifier": runtimeIssue.Identifier,
			"delay_type": "failure",
			"ts":         inRangeTS.Add(30 * time.Minute).Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent retry_scheduled: %v", err)
		}

		events, err := store.ListRuntimeEvents(0, 0)
		if err != nil {
			t.Fatalf("ListRuntimeEvents: %v", err)
		}
		if len(events) != 6 {
			t.Fatalf("expected all runtime events to be returned in order, got %#v", events)
		}
		if events[0].Kind != "run_completed" || events[1].Kind != "run_completed" || events[2].Payload != nil || events[3].Kind != "retry_paused" || events[3].Phase != "" || events[4].Kind != "run_failed" || events[5].Kind != "retry_scheduled" {
			t.Fatalf("unexpected runtime event ordering or payload decoding, got %#v", events)
		}
		if events[1].Phase != "implementation" {
			t.Fatalf("expected runtime event phase to be decoded, got %#v", events[1])
		}
		issueEvents, err := store.ListIssueRuntimeEvents("", 10)
		if err != nil {
			t.Fatalf("ListIssueRuntimeEvents blank: %v", err)
		}
		if len(issueEvents) != 0 {
			t.Fatalf("expected blank issue id to return no events, got %#v", issueEvents)
		}
		issueEvents, err = store.ListIssueRuntimeEvents(runtimeIssue.ID, 300)
		if err != nil {
			t.Fatalf("ListIssueRuntimeEvents runtime: %v", err)
		}
		if len(issueEvents) != 6 {
			t.Fatalf("expected all issue runtime events to be returned, got %#v", issueEvents)
		}

		seriesIssue, err := store.CreateIssue("", "", "Runtime series issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue series: %v", err)
		}
		seriesSeedTS := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Hour)
		seriesInRangeTS := seriesSeedTS.Add(2 * time.Hour)
		if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
			"issue_id":     seriesIssue.ID,
			"identifier":   seriesIssue.Identifier,
			"attempt":      1,
			"total_tokens": 2,
			"thread_id":    "thread-series",
			"ts":           seriesSeedTS.Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent series seed: %v", err)
		}
		if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
			"issue_id":     seriesIssue.ID,
			"identifier":   seriesIssue.Identifier,
			"attempt":      1,
			"total_tokens": 5,
			"thread_id":    "thread-series",
			"ts":           seriesInRangeTS.Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent series completion: %v", err)
		}
		if err := store.AppendRuntimeEvent("retry_paused", map[string]interface{}{
			"issue_id":     seriesIssue.ID,
			"identifier":   seriesIssue.Identifier,
			"error":        "plan_approval_pending",
			"total_tokens": 7,
			"thread_id":    "thread-series",
			"ts":           seriesInRangeTS.Add(10 * time.Minute).Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent series retry_paused: %v", err)
		}
		if err := store.AppendRuntimeEvent("run_failed", map[string]interface{}{
			"issue_id":     seriesIssue.ID,
			"identifier":   seriesIssue.Identifier,
			"error":        "stall_timeout",
			"total_tokens": 8,
			"thread_id":    "thread-series",
			"ts":           seriesInRangeTS.Add(20 * time.Minute).Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent series failure: %v", err)
		}
		if err := store.AppendRuntimeEvent("retry_scheduled", map[string]interface{}{
			"issue_id":   seriesIssue.ID,
			"identifier": seriesIssue.Identifier,
			"delay_type": "failure",
			"ts":         seriesInRangeTS.Add(30 * time.Minute).Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent series retry_scheduled: %v", err)
		}

		series, err := store.RuntimeSeries(3)
		if err != nil {
			t.Fatalf("RuntimeSeries: %v", err)
		}
		totalStarted, totalCompleted, totalFailed, totalRetries, totalTokens := 0, 0, 0, 0, 0
		for _, point := range series {
			totalStarted += point.RunsStarted
			totalCompleted += point.RunsCompleted
			totalFailed += point.RunsFailed
			totalRetries += point.Retries
			totalTokens += point.Tokens
		}
		if totalStarted != 0 || totalCompleted != 1 || totalFailed != 1 || totalRetries != 1 || totalTokens != 6 {
			t.Fatalf("unexpected runtime series totals: started=%d completed=%d failed=%d retries=%d tokens=%d series=%#v", totalStarted, totalCompleted, totalFailed, totalRetries, totalTokens, series)
		}
		if seriesDefault, err := store.RuntimeSeries(0); err != nil || len(seriesDefault) != 24 {
			t.Fatalf("expected default runtime series window, got len=%d err=%v", len(seriesDefault), err)
		}

		execIssue, err := store.CreateIssue("", "", "Execution session fallback", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue exec: %v", err)
		}
		if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
			IssueID:    execIssue.ID,
			Identifier: execIssue.Identifier,
			Phase:      string(WorkflowPhaseImplementation),
			Attempt:    1,
			RunKind:    "run_started",
			UpdatedAt:  time.Time{},
			AppSession: appserver.Session{
				IssueID:         execIssue.ID,
				IssueIdentifier: execIssue.Identifier,
				SessionID:       "session-1",
				ThreadID:        "thread-1",
				LastMessage:     "Running",
				History: []appserver.Event{
					{Type: "turn.started", Message: "Running"},
				},
			},
		}); err != nil {
			t.Fatalf("UpsertIssueExecutionSession insert: %v", err)
		}
		loadedSession, err := store.GetIssueExecutionSession(execIssue.ID)
		if err != nil {
			t.Fatalf("GetIssueExecutionSession insert: %v", err)
		}
		if loadedSession.UpdatedAt.IsZero() || len(loadedSession.AppSession.History) != 0 {
			t.Fatalf("expected summarized execution session, got %#v", loadedSession)
		}
		if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
			IssueID:    execIssue.ID,
			Identifier: execIssue.Identifier,
			Phase:      string(WorkflowPhaseImplementation),
			Attempt:    2,
			RunKind:    "retry_paused",
			StopReason: "plan_approval_pending",
			UpdatedAt:  time.Now().UTC(),
			AppSession: appserver.Session{
				IssueID:         execIssue.ID,
				IssueIdentifier: execIssue.Identifier,
				SessionID:       "session-2",
				ThreadID:        "thread-2",
			},
		}); err != nil {
			t.Fatalf("UpsertIssueExecutionSession update: %v", err)
		}
		updatedSession, err := store.GetIssueExecutionSession(execIssue.ID)
		if err != nil {
			t.Fatalf("GetIssueExecutionSession update: %v", err)
		}
		if updatedSession.Attempt != 2 || updatedSession.AppSession.SessionID != "session-2" {
			t.Fatalf("expected execution session update path to replace the snapshot, got %#v", updatedSession)
		}

		planIssue, err := store.CreateIssue("", "", "Planning fallback issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue planning: %v", err)
		}
		if planning, err := store.GetIssuePlanning(nil); err != nil || planning != nil {
			t.Fatalf("expected nil issue to return nil planning, got planning=%#v err=%v", planning, err)
		}
		if planning, err := store.GetIssuePlanning(planIssue); err != nil || planning != nil {
			t.Fatalf("expected missing plan session to return nil, got planning=%#v err=%v", planning, err)
		}
		requestedAt := time.Date(2026, 3, 18, 14, 0, 0, 0, time.UTC)
		if _, err := store.db.Exec(`
			UPDATE issues
			SET pending_plan_markdown = ?, pending_plan_revision_markdown = ?, pending_plan_revision_requested_at = ?
			WHERE id = ?`,
			"Draft plan", "Need a revision note", requestedAt, planIssue.ID,
		); err != nil {
			t.Fatalf("UPDATE planning issue: %v", err)
		}
		planIssue, err = store.GetIssue(planIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue planning reload: %v", err)
		}
		session := issuePlanSessionRecord{
			ID:                   generateID("pls"),
			IssueID:              planIssue.ID,
			Status:               IssuePlanningStatusAwaitingApproval,
			OriginAttempt:        1,
			OriginThreadID:       "thread-plan",
			CurrentVersionNumber: 0,
			OpenedAt:             requestedAt,
			UpdatedAt:            requestedAt,
		}
		tx, err := store.db.Begin()
		if err != nil {
			t.Fatalf("Begin planning tx: %v", err)
		}
		if err := store.insertIssuePlanSessionTx(tx, session); err != nil {
			t.Fatalf("insertIssuePlanSessionTx fallback: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit planning tx: %v", err)
		}
		planning, err := store.GetIssuePlanning(planIssue)
		if err != nil {
			t.Fatalf("GetIssuePlanning fallback: %v", err)
		}
		if planning == nil || planning.CurrentVersion == nil || planning.CurrentVersion.Markdown != "Draft plan" {
			t.Fatalf("expected issue pending plan markdown fallback, got %#v", planning)
		}
		if planning.PendingRevisionNote != "Need a revision note" {
			t.Fatalf("expected issue pending revision note fallback, got %#v", planning)
		}
	})

	t.Run("delete issue cleans related records", func(t *testing.T) {
		store := setupTestStore(t)
		project, err := store.CreateProject("Lifecycle", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		if err := store.UpdateProjectState(project.ID, ProjectStateRunning); err != nil {
			t.Fatalf("UpdateProjectState: %v", err)
		}
		epic, err := store.CreateEpic(project.ID, "Lifecycle epic", "")
		if err != nil {
			t.Fatalf("CreateEpic: %v", err)
		}
		issue, err := store.CreateIssueWithOptions(project.ID, epic.ID, "Lifecycle issue", "cleanup all related rows", 2, []string{"alpha"}, IssueCreateOptions{
			IssueType: IssueTypeRecurring,
			Cron:      "*/15 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions: %v", err)
		}
		if err := store.UpdateIssue(issue.ID, map[string]interface{}{
			"agent_name":   "planner",
			"agent_prompt": "  refine the rollout  ",
		}); err != nil {
			t.Fatalf("UpdateIssue agent fields: %v", err)
		}
		requestedAt := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		if err := store.SetIssuePendingPlanApproval(issue.ID, "Initial plan", requestedAt); err != nil {
			t.Fatalf("SetIssuePendingPlanApproval: %v", err)
		}
		if err := store.SetIssuePendingPlanRevision(issue.ID, "Need a tighter rollback step.", requestedAt.Add(15*time.Minute)); err != nil {
			t.Fatalf("SetIssuePendingPlanRevision: %v", err)
		}

		workspacePath := filepath.Join(t.TempDir(), issue.Identifier)
		if err := os.MkdirAll(workspacePath, 0o755); err != nil {
			t.Fatalf("MkdirAll workspace: %v", err)
		}
		if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}

		commentDir := t.TempDir()
		commentAttachmentPath := filepath.Join(commentDir, "note.txt")
		if err := os.WriteFile(commentAttachmentPath, []byte("cleanup comment attachment"), 0o644); err != nil {
			t.Fatalf("WriteFile comment attachment: %v", err)
		}
		commentBody := "Lifecycle comment"
		comment, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
			Body: &commentBody,
			Attachments: []IssueCommentAttachmentInput{{
				Path: commentAttachmentPath,
			}},
		})
		if err != nil {
			t.Fatalf("CreateIssueComment: %v", err)
		}
		if len(comment.Attachments) != 1 {
			t.Fatalf("expected a comment attachment, got %#v", comment.Attachments)
		}

		asset, err := store.CreateIssueAsset(issue.ID, "preview", bytes.NewReader(samplePNGBytes()))
		if err != nil {
			t.Fatalf("CreateIssueAsset: %v", err)
		}

		if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, agentruntime.ActivityEvent{
			Type:     "turn.started",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
		}); err != nil {
			t.Fatalf("ApplyIssueActivityEvent: %v", err)
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
				SessionID:       "thread-1-turn-1",
				ThreadID:        "thread-1",
				TurnID:          "turn-1",
				LastEvent:       "turn.started",
				LastMessage:     "Working",
			},
		}); err != nil {
			t.Fatalf("UpsertIssueExecutionSession: %v", err)
		}

		blockedIssue, err := store.CreateIssue(project.ID, "", "Blocked issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocked: %v", err)
		}
		if err := store.UpdateIssueState(blockedIssue.ID, StateReady); err != nil {
			t.Fatalf("UpdateIssueState blocked: %v", err)
		}
		if _, err := store.SetIssueBlockers(blockedIssue.ID, []string{issue.Identifier}); err != nil {
			t.Fatalf("SetIssueBlockers: %v", err)
		}
		waitingCommand, err := store.CreateIssueAgentCommand(blockedIssue.ID, "Run after unblock.", IssueAgentCommandWaitingForUnblock)
		if err != nil {
			t.Fatalf("CreateIssueAgentCommand waiting: %v", err)
		}

		if err := store.DeleteIssue(issue.ID); err != nil {
			t.Fatalf("DeleteIssue: %v", err)
		}

		if _, err := store.GetIssue(issue.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted issue lookup to fail, got %v", err)
		}
		if recurrence, err := store.GetIssueRecurrence(issue.ID); err != nil || recurrence != nil {
			t.Fatalf("expected deleted recurrence to disappear, got recurrence=%#v err=%v", recurrence, err)
		}
		if planning, err := store.GetIssuePlanning(issue); err != nil || planning != nil {
			t.Fatalf("expected deleted planning history to disappear, got planning=%#v err=%v", planning, err)
		}
		if _, err := store.GetIssueExecutionSession(issue.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected deleted execution session lookup to fail, got %v", err)
		}
		if _, err := store.GetIssueAsset(issue.ID, asset.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted issue asset lookup to fail, got %v", err)
		}
		if _, _, err := store.GetIssueAssetContent(issue.ID, asset.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted issue asset content lookup to fail, got %v", err)
		}
		if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
			t.Fatalf("expected workspace path to be removed, got err=%v", err)
		}
		if _, err := store.GetIssueComment(issue.ID, comment.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted comment lookup to fail, got %v", err)
		}
		if _, _, err := store.GetIssueCommentAttachmentContent(issue.ID, comment.ID, comment.Attachments[0].ID); !IsNotFound(err) {
			t.Fatalf("expected deleted comment attachment lookup to fail, got %v", err)
		}
		if entries, err := store.ListIssueActivityEntries(issue.ID); err != nil || len(entries) != 0 {
			t.Fatalf("expected deleted activity entries to disappear, got entries=%#v err=%v", entries, err)
		}

		updatedBlocked, err := store.GetIssue(blockedIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue blocked issue: %v", err)
		}
		if len(updatedBlocked.BlockedBy) != 0 {
			t.Fatalf("expected blocked issue blockers to be cleared, got %#v", updatedBlocked.BlockedBy)
		}
		pending, err := store.ListPendingIssueAgentCommands(blockedIssue.ID)
		if err != nil {
			t.Fatalf("ListPendingIssueAgentCommands: %v", err)
		}
		if len(pending) != 1 || pending[0].ID != waitingCommand.ID {
			t.Fatalf("expected waiting command to reactivate, got %#v", pending)
		}
	})

	t.Run("maintenance and prune helpers", func(t *testing.T) {
		store := setupTestStore(t)
		if _, err := store.DBStats(); err != nil {
			t.Fatalf("DBStats: %v", err)
		}

		closedStore := setupTestStore(t)
		if err := closedStore.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if _, err := closedStore.DBStats(); err == nil {
			t.Fatal("expected DBStats on closed store to fail")
		}

		protectedIssue, err := store.CreateIssue("", "", "Protected", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue protected: %v", err)
		}
		staleIssue, err := store.CreateIssue("", "", "Stale", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue stale: %v", err)
		}
		cutoff := time.Now().UTC().Add(-time.Hour)
		for _, issue := range []Issue{*protectedIssue, *staleIssue} {
			if _, err := store.db.Exec(`INSERT INTO runtime_events (kind, issue_id, identifier, event_ts, payload_json) VALUES (?, ?, ?, ?, '{}')`,
				"run_started", issue.ID, issue.Identifier, cutoff.Add(-time.Minute),
			); err != nil {
				t.Fatalf("insert runtime event: %v", err)
			}
		}

		tx, err := store.db.Begin()
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if err := deleteExpiredRowsTx(tx, "runtime_events", "event_ts", "issue_id", cutoff, []string{protectedIssue.ID}, "kind = 'run_started'"); err != nil {
			_ = tx.Rollback()
			t.Fatalf("deleteExpiredRowsTx: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}

		for _, tc := range []struct {
			query string
			args  []interface{}
			want  int
		}{
			{query: `SELECT COUNT(*) FROM runtime_events WHERE issue_id = ?`, args: []interface{}{staleIssue.ID}, want: 0},
			{query: `SELECT COUNT(*) FROM runtime_events WHERE issue_id = ?`, args: []interface{}{protectedIssue.ID}, want: 1},
		} {
			var got int
			if err := store.db.QueryRow(tc.query, tc.args...).Scan(&got); err != nil {
				t.Fatalf("query %q: %v", tc.query, err)
			}
			if got != tc.want {
				t.Fatalf("query %q = %d, want %d", tc.query, got, tc.want)
			}
		}

		result, err := store.RunMaintenance([]string{protectedIssue.ID})
		if err != nil {
			t.Fatalf("RunMaintenance: %v", err)
		}
		if result.CheckpointResult == "" || result.StartedAt.IsZero() || result.CheckpointAt.IsZero() {
			t.Fatalf("expected maintenance result timestamps and checkpoint summary, got %#v", result)
		}
	})
}

func TestKanbanCoverageRollbackMatrix(t *testing.T) {
	newFaultyStore := func(t *testing.T, dbPath, failPattern string) *Store {
		t.Helper()
		store := openFaultySQLiteStoreAt(t, dbPath, failPattern)
		if err := store.configureConnection(); err != nil {
			t.Fatalf("configureConnection faulty store: %v", err)
		}
		return store
	}

	t.Run("labels and blockers helpers", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "labels-blockers.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Labels and blockers", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		blockerA, err := base.CreateIssue("", "", "Blocker A", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blockerA: %v", err)
		}
		blockerB, err := base.CreateIssue("", "", "Blocker B", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blockerB: %v", err)
		}
		tx, err := base.db.Begin()
		if err != nil {
			t.Fatalf("Begin label tx: %v", err)
		}
		if err := replaceIssueLabelsTx(tx, issue.ID, []string{"alpha", "beta"}); err != nil {
			_ = tx.Rollback()
			t.Fatalf("replaceIssueLabelsTx seed: %v", err)
		}
		if err := replaceIssueBlockersRawTx(tx, issue.ID, []string{blockerA.Identifier, blockerB.Identifier}); err != nil {
			_ = tx.Rollback()
			t.Fatalf("replaceIssueBlockersRawTx seed: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit seed tx: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		checkIssueState := func(store *Store, wantLabels, wantBlockers []string) {
			t.Helper()
			loaded, err := store.GetIssue(issue.ID)
			if err != nil {
				t.Fatalf("GetIssue: %v", err)
			}
			if len(loaded.Labels) != len(wantLabels) {
				t.Fatalf("unexpected labels: %#v", loaded.Labels)
			}
			for i := range wantLabels {
				if loaded.Labels[i] != wantLabels[i] {
					t.Fatalf("unexpected labels: %#v", loaded.Labels)
				}
			}
			if len(loaded.BlockedBy) != len(wantBlockers) {
				t.Fatalf("unexpected blockers: %#v", loaded.BlockedBy)
			}
			for i := range wantBlockers {
				if loaded.BlockedBy[i] != wantBlockers[i] {
					t.Fatalf("unexpected blockers: %#v", loaded.BlockedBy)
				}
			}
		}

		t.Run("replaceIssueLabelsTx delete failure", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "delete from issue_labels")
			tx, err := store.db.Begin()
			if err != nil {
				t.Fatalf("Begin faulty tx: %v", err)
			}
			if err := replaceIssueLabelsTx(tx, issue.ID, []string{"gamma"}); err == nil {
				_ = tx.Rollback()
				t.Fatal("expected replaceIssueLabelsTx to fail on delete")
			}
			_ = tx.Rollback()
			checkIssueState(store, []string{"alpha", "beta"}, []string{blockerA.Identifier, blockerB.Identifier})
		})

		t.Run("replaceIssueLabelsTx insert failure", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert or ignore into issue_labels")
			tx, err := store.db.Begin()
			if err != nil {
				t.Fatalf("Begin faulty tx: %v", err)
			}
			if err := replaceIssueLabelsTx(tx, issue.ID, []string{"gamma"}); err == nil {
				_ = tx.Rollback()
				t.Fatal("expected replaceIssueLabelsTx to fail on insert")
			}
			_ = tx.Rollback()
			checkIssueState(store, []string{"alpha", "beta"}, []string{blockerA.Identifier, blockerB.Identifier})
		})

		t.Run("replaceIssueBlockersRawTx delete failure", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "delete from issue_blockers")
			tx, err := store.db.Begin()
			if err != nil {
				t.Fatalf("Begin faulty tx: %v", err)
			}
			if err := replaceIssueBlockersRawTx(tx, issue.ID, []string{"noop"}); err == nil {
				_ = tx.Rollback()
				t.Fatal("expected replaceIssueBlockersRawTx to fail on delete")
			}
			_ = tx.Rollback()
			checkIssueState(store, []string{"alpha", "beta"}, []string{blockerA.Identifier, blockerB.Identifier})
		})

		t.Run("replaceIssueBlockersRawTx insert failure", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert or ignore into issue_blockers")
			tx, err := store.db.Begin()
			if err != nil {
				t.Fatalf("Begin faulty tx: %v", err)
			}
			if err := replaceIssueBlockersRawTx(tx, issue.ID, []string{"noop"}); err == nil {
				_ = tx.Rollback()
				t.Fatal("expected replaceIssueBlockersRawTx to fail on insert")
			}
			_ = tx.Rollback()
			checkIssueState(store, []string{"alpha", "beta"}, []string{blockerA.Identifier, blockerB.Identifier})
		})
	})

	t.Run("comment helpers and rollback", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "comments.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Comment coverage", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		attachmentPath := filepath.Join(t.TempDir(), "note.txt")
		if err := os.WriteFile(attachmentPath, []byte("comment attachment"), 0o644); err != nil {
			t.Fatalf("WriteFile attachment: %v", err)
		}
		body := "Initial comment"
		comment, err := base.CreateIssueComment(issue.ID, IssueCommentInput{
			Body: &body,
			Attachments: []IssueCommentAttachmentInput{{
				Path: attachmentPath,
			}},
		})
		if err != nil {
			t.Fatalf("CreateIssueComment seed: %v", err)
		}
		replyBody := "Reply comment"
		if _, err := base.CreateIssueComment(issue.ID, IssueCommentInput{
			Body:            &replyBody,
			ParentCommentID: comment.ID,
		}); err != nil {
			t.Fatalf("CreateIssueComment reply seed: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		t.Run("CreateIssueComment rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into change_events")
			newBody := "New comment body"
			if _, err := store.CreateIssueComment(issue.ID, IssueCommentInput{Body: &newBody}); err == nil {
				t.Fatal("expected CreateIssueComment to fail when change_events is injected")
			}
			comments, err := store.ListIssueComments(issue.ID)
			if err != nil {
				t.Fatalf("ListIssueComments: %v", err)
			}
			if len(comments) != 1 || comments[0].Body != body {
				t.Fatalf("expected comment rows to roll back, got %#v", comments)
			}
		})

		t.Run("UpdateIssueComment rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into change_events")
			updatedBody := "Updated body"
			updated, err := store.UpdateIssueComment(issue.ID, comment.ID, IssueCommentInput{
				Body:                &updatedBody,
				RemoveAttachmentIDs: []string{comment.Attachments[0].ID},
			})
			if err == nil {
				t.Fatalf("expected UpdateIssueComment to fail, got %#v", updated)
			}
			comments, err := store.ListIssueComments(issue.ID)
			if err != nil {
				t.Fatalf("ListIssueComments: %v", err)
			}
			if len(comments) != 1 || comments[0].Body != body || len(comments[0].Attachments) != 1 {
				t.Fatalf("expected update to roll back, got %#v", comments)
			}
		})

		t.Run("issueCommentHasRepliesTx error", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "select exists")
			tx, err := store.db.Begin()
			if err != nil {
				t.Fatalf("Begin faulty tx: %v", err)
			}
			if _, err := issueCommentHasRepliesTx(tx, comment.ID); err == nil {
				_ = tx.Rollback()
				t.Fatal("expected issueCommentHasRepliesTx to fail")
			}
			_ = tx.Rollback()
		})
	})

	t.Run("planning and epic rollback", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "planning.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProject("Planning coverage", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		epic, err := base.CreateEpic(project.ID, "Epic coverage", "")
		if err != nil {
			t.Fatalf("CreateEpic: %v", err)
		}
		issue, err := base.CreateIssue(project.ID, epic.ID, "Planned issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		approvalTime := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
		if err := base.SetIssuePendingPlanApprovalWithContext(issue, "Draft rollout", approvalTime, 1, "thread-plan", "turn-plan"); err != nil {
			t.Fatalf("SetIssuePendingPlanApprovalWithContext: %v", err)
		}
		if err := base.SetIssuePendingPlanRevision(issue.ID, "Revision note", approvalTime.Add(time.Minute)); err != nil {
			t.Fatalf("SetIssuePendingPlanRevision: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		t.Run("ApproveIssuePlanWithNote rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into issue_agent_commands")
			loaded, err := store.GetIssue(issue.ID)
			if err != nil {
				t.Fatalf("GetIssue: %v", err)
			}
			approvedAt := approvalTime.Add(2 * time.Hour)
			if _, err := store.ApproveIssuePlanWithNote(loaded, approvedAt, "Ship the rollout.", ""); err == nil {
				t.Fatal("expected ApproveIssuePlanWithNote to fail")
			}
			reloaded, err := store.GetIssue(issue.ID)
			if err != nil {
				t.Fatalf("GetIssue after rollback: %v", err)
			}
			if !reloaded.PlanApprovalPending || reloaded.PendingPlanMarkdown == "" || reloaded.PendingPlanRevisionMarkdown == "" {
				t.Fatalf("expected plan approval state to roll back, got %#v", reloaded)
			}
		})

		t.Run("ClearIssuePendingPlanApproval rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into change_events")
			if err := store.ClearIssuePendingPlanApproval(issue.ID, "manual_retry"); err == nil {
				t.Fatal("expected ClearIssuePendingPlanApproval to fail")
			}
			reloaded, err := store.GetIssue(issue.ID)
			if err != nil {
				t.Fatalf("GetIssue after rollback: %v", err)
			}
			if !reloaded.PlanApprovalPending {
				t.Fatalf("expected plan approval to remain pending, got %#v", reloaded)
			}
		})

		t.Run("ClearIssuePendingPlanRevision rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into change_events")
			if err := store.ClearIssuePendingPlanRevision(issue.ID, "manual_retry"); err == nil {
				t.Fatal("expected ClearIssuePendingPlanRevision to fail")
			}
			reloaded, err := store.GetIssue(issue.ID)
			if err != nil {
				t.Fatalf("GetIssue after rollback: %v", err)
			}
			if reloaded.PendingPlanRevisionMarkdown == "" {
				t.Fatalf("expected plan revision to remain pending, got %#v", reloaded)
			}
		})

		t.Run("DeleteEpic rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into change_events")
			if err := store.DeleteEpic(epic.ID); err == nil {
				t.Fatal("expected DeleteEpic to fail")
			}
			reloaded, err := store.GetIssue(issue.ID)
			if err != nil {
				t.Fatalf("GetIssue after rollback: %v", err)
			}
			if reloaded.EpicID != epic.ID {
				t.Fatalf("expected epic assignment to survive rollback, got %#v", reloaded.EpicID)
			}
		})

		t.Run("UpdateProjectPermissionProfile rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into change_events")
			if err := store.UpdateProjectPermissionProfile(project.ID, PermissionProfileFullAccess); err == nil {
				t.Fatal("expected UpdateProjectPermissionProfile to fail")
			}
			reloadedIssue, err := store.GetIssue(issue.ID)
			if err != nil {
				t.Fatalf("GetIssue after rollback: %v", err)
			}
			if !reloadedIssue.PlanApprovalPending || reloadedIssue.PermissionProfile != PermissionProfileDefault {
				t.Fatalf("expected issue permission state to survive rollback, got %#v", reloadedIssue)
			}
		})
	})

	t.Run("asset rollback", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "assets.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Asset coverage", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		store := newFaultyStore(t, dbPath, "insert into change_events")
		asset, err := store.CreateIssueAsset(issue.ID, "preview", bytes.NewReader(samplePNGBytes()))
		if err == nil {
			t.Fatalf("expected CreateIssueAsset to fail, got %#v", asset)
		}
		assets, err := store.ListIssueAssets(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueAssets: %v", err)
		}
		if len(assets) != 0 {
			t.Fatalf("expected asset insert to roll back, got %#v", assets)
		}
	})

	t.Run("asset helper error branches", func(t *testing.T) {
		t.Run("CreateIssueAsset rename failure", func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("read-only directory permissions behave differently on Windows")
			}
			dbPath := filepath.Join(t.TempDir(), "asset-rename-failure.db")
			store := openSQLiteStoreAt(t, dbPath)
			issue, err := store.CreateIssue("", "", "Asset rename failure", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			issueDir := filepath.Join(store.IssueAssetRoot(), issue.ID)
			if err := os.MkdirAll(issueDir, 0o755); err != nil {
				t.Fatalf("MkdirAll issue dir: %v", err)
			}
			if err := os.Chmod(issueDir, 0o555); err != nil {
				t.Fatalf("Chmod issue dir: %v", err)
			}
			t.Cleanup(func() {
				_ = os.Chmod(issueDir, 0o755)
			})
			if _, err := store.CreateIssueAsset(issue.ID, "preview.png", bytes.NewReader(samplePNGBytes())); err == nil {
				t.Fatal("expected CreateIssueAsset to fail when the issue directory is read-only")
			}
			assets, err := store.ListIssueAssets(issue.ID)
			if err != nil {
				t.Fatalf("ListIssueAssets after rename failure: %v", err)
			}
			if len(assets) != 0 {
				t.Fatalf("expected no assets to be created after rename failure, got %#v", assets)
			}
		})

		dbPath := filepath.Join(t.TempDir(), "asset-errors.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Asset helper coverage", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if runtime.GOOS != "windows" {
			t.Run("CreateIssueAsset root creation failure", func(t *testing.T) {
				assetsRoot := filepath.Dir(base.IssueAssetRoot())
				if err := os.MkdirAll(assetsRoot, 0o755); err != nil {
					t.Fatalf("MkdirAll assets root: %v", err)
				}
				if err := os.Chmod(assetsRoot, 0o555); err != nil {
					t.Fatalf("Chmod assets root: %v", err)
				}
				t.Cleanup(func() {
					_ = os.Chmod(assetsRoot, 0o755)
				})
				if _, err := base.CreateIssueAsset(issue.ID, "preview-root", bytes.NewReader(samplePNGBytes())); err == nil {
					t.Fatal("expected CreateIssueAsset to fail when asset root is read-only")
				}
				assets, err := base.ListIssueAssets(issue.ID)
				if err != nil {
					t.Fatalf("ListIssueAssets after root failure: %v", err)
				}
				if len(assets) != 0 {
					t.Fatalf("expected no assets to be created after root failure, got %#v", assets)
				}
			})
		}
		asset, err := base.CreateIssueAsset(issue.ID, "preview", bytes.NewReader(samplePNGBytes()))
		if err != nil {
			t.Fatalf("CreateIssueAsset seed: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		t.Run("CreateIssueAsset insert failure", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into issue_assets")
			if _, err := store.CreateIssueAsset(issue.ID, "preview-2", bytes.NewReader(samplePNGBytes())); err == nil {
				t.Fatal("expected CreateIssueAsset to fail when issue_assets insert is injected")
			}
			assets, err := store.ListIssueAssets(issue.ID)
			if err != nil {
				t.Fatalf("ListIssueAssets: %v", err)
			}
			if len(assets) != 1 || assets[0].ID != asset.ID {
				t.Fatalf("expected seeded asset to remain after rollback, got %#v", assets)
			}
		})

		t.Run("DeleteIssueAsset delete failure", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "delete from issue_assets")
			if err := store.DeleteIssueAsset(issue.ID, asset.ID); err == nil {
				t.Fatal("expected DeleteIssueAsset to fail when issue_assets delete is injected")
			}
			if _, err := store.GetIssueAsset(issue.ID, asset.ID); err != nil {
				t.Fatalf("expected seeded asset to survive rollback, got %v", err)
			}
		})

		t.Run("deleteIssueAssetsTx query failure", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "select storage_path from issue_assets")
			tx, err := store.db.Begin()
			if err != nil {
				t.Fatalf("Begin faulty tx: %v", err)
			}
			if _, err := store.deleteIssueAssetsTx(tx, issue.ID); err == nil {
				_ = tx.Rollback()
				t.Fatal("expected deleteIssueAssetsTx to fail when issue_assets query is injected")
			}
			_ = tx.Rollback()
			assets, err := store.ListIssueAssets(issue.ID)
			if err != nil {
				t.Fatalf("ListIssueAssets after failed helper: %v", err)
			}
			if len(assets) != 1 || assets[0].ID != asset.ID {
				t.Fatalf("expected seeded asset to remain after helper failure, got %#v", assets)
			}
		})
	})

	t.Run("issue command rollback", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "commands.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Command coverage", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		command, err := base.CreateIssueAgentCommand(issue.ID, "Run the check.", IssueAgentCommandPending)
		if err != nil {
			t.Fatalf("CreateIssueAgentCommand: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		t.Run("CreateIssueAgentCommandWithRuntimeEvent rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into runtime_events")
			if _, err := store.CreateIssueAgentCommandWithRuntimeEvent(issue.ID, "Run the check.", IssueAgentCommandPending, "manual_command_submitted", nil); err == nil {
				t.Fatal("expected CreateIssueAgentCommandWithRuntimeEvent to fail")
			}
			commands, err := store.ListIssueAgentCommands(issue.ID)
			if err != nil {
				t.Fatalf("ListIssueAgentCommands: %v", err)
			}
			if len(commands) != 1 {
				t.Fatalf("expected runtime-event rollback to preserve the seeded command only, got %#v", commands)
			}
		})

		t.Run("UpdateIssueAgentCommand rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into change_events")
			if _, err := store.UpdateIssueAgentCommand(issue.ID, command.ID, "Updated command"); err == nil {
				t.Fatal("expected UpdateIssueAgentCommand to fail")
			}
			commands, err := store.ListIssueAgentCommands(issue.ID)
			if err != nil {
				t.Fatalf("ListIssueAgentCommands: %v", err)
			}
			if len(commands) != 1 || commands[0].Command != "Run the check." {
				t.Fatalf("expected update to roll back, got %#v", commands)
			}
		})

		t.Run("SteerIssueAgentCommand rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into change_events")
			if _, err := store.SteerIssueAgentCommand(issue.ID, command.ID); err == nil {
				t.Fatal("expected SteerIssueAgentCommand to fail")
			}
			commands, err := store.ListIssueAgentCommands(issue.ID)
			if err != nil {
				t.Fatalf("ListIssueAgentCommands: %v", err)
			}
			if len(commands) != 1 || commands[0].SteeredAt != nil {
				t.Fatalf("expected steer to roll back, got %#v", commands)
			}
		})

		t.Run("DeleteIssueAgentCommand rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into change_events")
			if err := store.DeleteIssueAgentCommand(issue.ID, command.ID); err == nil {
				t.Fatal("expected DeleteIssueAgentCommand to fail")
			}
			commands, err := store.ListIssueAgentCommands(issue.ID)
			if err != nil {
				t.Fatalf("ListIssueAgentCommands: %v", err)
			}
			if len(commands) != 1 {
				t.Fatalf("expected delete to roll back, got %#v", commands)
			}
		})

		t.Run("MarkIssueAgentCommandsDeliveredIfUnchanged rollback", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into change_events")
			if err := store.MarkIssueAgentCommandsDeliveredIfUnchanged(issue.ID, []IssueAgentCommand{*command}, "manual", "thread-1", 1); err == nil {
				t.Fatal("expected MarkIssueAgentCommandsDeliveredIfUnchanged to fail")
			}
			commands, err := store.ListIssueAgentCommands(issue.ID)
			if err != nil {
				t.Fatalf("ListIssueAgentCommands: %v", err)
			}
			if len(commands) != 1 || commands[0].Status != IssueAgentCommandPending {
				t.Fatalf("expected delivery to roll back, got %#v", commands)
			}
		})
	})

	t.Run("provider rollback", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "provider.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProjectWithProvider("Provider coverage", "", "", "", testProviderKind, "PROJ-1", nil)
		if err != nil {
			t.Fatalf("CreateProjectWithProvider: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		t.Run("create rollback on change event", func(t *testing.T) {
			store := newFaultyStore(t, dbPath, "insert into change_events")
			created, err := store.UpsertProviderIssue(project.ID, &Issue{
				ProviderKind:     testProviderKind,
				ProviderIssueRef: "ext-create",
				Identifier:       "EXT-CREATE",
				Title:            "Create provider issue",
				Labels:           []string{"alpha", "beta"},
				BlockedBy:        []string{"EXT-1"},
				State:            StateReady,
			})
			if err == nil {
				t.Fatalf("expected UpsertProviderIssue create to fail, got %#v", created)
			}
			issues, err := store.ListIssues(map[string]interface{}{"provider_kind": testProviderKind})
			if err != nil {
				t.Fatalf("ListIssues: %v", err)
			}
			if len(issues) != 0 {
				t.Fatalf("expected create rollback to leave no provider issues, got %#v", issues)
			}
		})

		t.Run("update rollback on change event", func(t *testing.T) {
			seed := openSQLiteStoreAt(t, dbPath)
			created, err := seed.UpsertProviderIssue(project.ID, &Issue{
				ProviderKind:     testProviderKind,
				ProviderIssueRef: "ext-update",
				Identifier:       "EXT-UPDATE",
				Title:            "Seed provider issue",
				Labels:           []string{"seed"},
				BlockedBy:        []string{"EXT-0"},
				State:            StateReady,
			})
			if err != nil {
				t.Fatalf("UpsertProviderIssue seed: %v", err)
			}
			if err := seed.Close(); err != nil {
				t.Fatalf("Close seed store: %v", err)
			}

			store := newFaultyStore(t, dbPath, "insert into change_events")
			syncedAt := time.Date(2026, 3, 21, 9, 30, 0, 0, time.UTC)
			updated, err := store.UpsertProviderIssue(project.ID, &Issue{
				ProviderKind:     testProviderKind,
				ProviderIssueRef: "ext-update",
				Identifier:       "EXT-UPDATE",
				Title:            "Updated provider issue",
				Description:      "updated",
				Labels:           []string{"fresh"},
				BlockedBy:        []string{"EXT-2"},
				State:            StateCancelled,
				UpdatedAt:        time.Date(2026, 3, 21, 9, 0, 0, 0, time.UTC),
				LastSyncedAt:     &syncedAt,
			})
			if err == nil {
				t.Fatalf("expected UpsertProviderIssue update to fail, got %#v", updated)
			}
			reloaded, err := store.GetIssue(created.ID)
			if err != nil {
				t.Fatalf("GetIssue after rollback: %v", err)
			}
			if reloaded.Title != "Seed provider issue" || len(reloaded.Labels) != 1 || reloaded.Labels[0] != "seed" {
				t.Fatalf("expected update rollback to preserve provider issue, got %#v", reloaded)
			}
		})
	})
}

func TestKanbanActivityCompactionCoverageBranches(t *testing.T) {
	t.Run("success compaction keeps terminal entries", func(t *testing.T) {
		store := setupTestStore(t)
		issue, err := store.CreateIssue("", "", "Compaction issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		now := time.Now().UTC()
		insertEntry := func(logicalID, kind, title, status, phase string) {
			t.Helper()
			if _, err := store.db.Exec(`
				INSERT INTO issue_activity_entries (
					issue_id, identifier, logical_id, attempt, thread_id, turn_id, item_id,
					kind, item_type, phase, entry_status, tier, title, summary, detail, tone,
					expandable, created_at, updated_at, raw_payload_json
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				issue.ID,
				issue.Identifier,
				logicalID,
				1,
				"thread-1",
				"turn-1",
				"item-"+logicalID,
				kind,
				"agentMessage",
				phase,
				status,
				"primary",
				title,
				title,
				title,
				"default",
				1,
				now,
				now,
				`{}`,
			); err != nil {
				t.Fatalf("insert issue activity entry %s: %v", logicalID, err)
			}
			if _, err := store.db.Exec(`
				INSERT INTO issue_activity_updates (issue_id, entry_id, event_type, event_ts, payload_json)
				VALUES (?, ?, ?, ?, '{}')`,
				issue.ID, logicalID, "turn.completed", now,
			); err != nil {
				t.Fatalf("insert issue activity update %s: %v", logicalID, err)
			}
		}

		insertEntry("success-approval", "status", "Approval required", "open", "")
		insertEntry("success-completed", "status", "Turn Completed", "completed", "")
		insertEntry("success-filler", "agent", "Filler", "open", "implementation")
		insertEntry("success-final", "agent", "Final answer", "open", "final_answer")
		insertEntry("success-input", "status", "User input required", "open", "")

		if err := store.CompactIssueActivityAttemptSuccess(issue.ID, 1); err != nil {
			t.Fatalf("CompactIssueActivityAttemptSuccess: %v", err)
		}
		entries, err := store.ListIssueActivityEntries(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueActivityEntries success: %v", err)
		}
		if len(entries) != 4 {
			t.Fatalf("expected compacted success attempt to keep 4 entries, got %#v", entries)
		}
		kept := map[string]bool{}
		for _, entry := range entries {
			kept[entry.ID] = true
		}
		for _, want := range []string{"success-approval", "success-completed", "success-final", "success-input"} {
			if !kept[want] {
				t.Fatalf("expected %s to remain after success compaction, got %#v", want, entries)
			}
		}
		var updateCount int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM issue_activity_updates WHERE issue_id = ?`, issue.ID).Scan(&updateCount); err != nil {
			t.Fatalf("query success activity updates: %v", err)
		}
		if updateCount != 0 {
			t.Fatalf("expected activity updates to be compacted away, got %d", updateCount)
		}
	})

	t.Run("diagnostic compaction keeps tail and sticky statuses", func(t *testing.T) {
		store := setupTestStore(t)
		issue, err := store.CreateIssue("", "", "Diagnostic compaction issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue diagnostic: %v", err)
		}
		now := time.Now().UTC()
		insertEntry := func(logicalID, kind, title, status, phase string) {
			t.Helper()
			if _, err := store.db.Exec(`
				INSERT INTO issue_activity_entries (
					issue_id, identifier, logical_id, attempt, thread_id, turn_id, item_id,
					kind, item_type, phase, entry_status, tier, title, summary, detail, tone,
					expandable, created_at, updated_at, raw_payload_json
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				issue.ID,
				issue.Identifier,
				logicalID,
				1,
				"thread-1",
				"turn-1",
				"item-"+logicalID,
				kind,
				"agentMessage",
				phase,
				status,
				"primary",
				title,
				title,
				title,
				"default",
				1,
				now,
				now,
				`{}`,
			); err != nil {
				t.Fatalf("insert issue activity entry %s: %v", logicalID, err)
			}
			if _, err := store.db.Exec(`
				INSERT INTO issue_activity_updates (issue_id, entry_id, event_type, event_ts, payload_json)
				VALUES (?, ?, ?, ?, '{}')`,
				issue.ID, logicalID, "turn.failed", now,
			); err != nil {
				t.Fatalf("insert issue activity update %s: %v", logicalID, err)
			}
		}

		insertEntry("diag-approval", "status", "Approval required", "open", "")
		insertEntry("diag-input", "status", "User input submitted", "completed", "")
		for i := 0; i < 23; i++ {
			insertEntry(
				fmt.Sprintf("diag-%02d", i),
				"agent",
				fmt.Sprintf("Entry %02d", i),
				"open",
				"implementation",
			)
		}

		if err := store.CompactIssueActivityAttemptDiagnostic(issue.ID, 1); err != nil {
			t.Fatalf("CompactIssueActivityAttemptDiagnostic: %v", err)
		}
		entries, err := store.ListIssueActivityEntries(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueActivityEntries diagnostic: %v", err)
		}
		if len(entries) != 22 {
			t.Fatalf("expected diagnostic compaction to keep 22 entries, got %d", len(entries))
		}
		titles := make(map[string]bool, len(entries))
		for _, entry := range entries {
			titles[entry.Title] = true
		}
		if !titles["Approval required"] || !titles["User input submitted"] {
			t.Fatalf("expected sticky status entries to remain, got %#v", entries)
		}
		if titles["Entry 00"] || titles["Entry 01"] || titles["Entry 02"] {
			t.Fatalf("expected oldest primary entries to be compacted away, got %#v", entries)
		}
		if !titles["Entry 03"] {
			t.Fatalf("expected newer primary entries to remain, got %#v", entries)
		}
	})

	t.Run("blank issue and attempt are no-ops", func(t *testing.T) {
		store := setupTestStore(t)
		if err := store.CompactIssueActivityAttemptSuccess("", 0); err != nil {
			t.Fatalf("CompactIssueActivityAttemptSuccess blank = %v", err)
		}
		if err := store.CompactIssueActivityAttemptDiagnostic("", 0); err != nil {
			t.Fatalf("CompactIssueActivityAttemptDiagnostic blank = %v", err)
		}
	})

	t.Run("compaction delete failures roll back", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "coverage.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Compaction failure issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		now := time.Now().UTC()
		insertEntry := func(logicalID, kind, title, status, phase string) {
			t.Helper()
			if _, err := base.db.Exec(`
				INSERT INTO issue_activity_entries (
					issue_id, identifier, logical_id, attempt, thread_id, turn_id, item_id,
					kind, item_type, phase, entry_status, tier, title, summary, detail, tone,
					expandable, created_at, updated_at, raw_payload_json
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				issue.ID,
				issue.Identifier,
				logicalID,
				1,
				"thread-1",
				"turn-1",
				"item-"+logicalID,
				kind,
				"agentMessage",
				phase,
				status,
				"primary",
				title,
				title,
				title,
				"default",
				1,
				now,
				now,
				`{}`,
			); err != nil {
				t.Fatalf("insert issue activity entry %s: %v", logicalID, err)
			}
			if _, err := base.db.Exec(`
				INSERT INTO issue_activity_updates (issue_id, entry_id, event_type, event_ts, payload_json)
				VALUES (?, ?, ?, ?, '{}')`,
				issue.ID, logicalID, "turn.completed", now,
			); err != nil {
				t.Fatalf("insert issue activity update %s: %v", logicalID, err)
			}
		}

		insertEntry("keep-approval", "status", "Approval required", "open", "")
		for i := 0; i < 20; i++ {
			insertEntry(
				fmt.Sprintf("drop-%02d", i),
				"agent",
				fmt.Sprintf("Entry %02d", i),
				"open",
				"implementation",
			)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		successFaulty := openFaultySQLiteStoreAt(t, dbPath, "delete from issue_activity_updates")
		if err := successFaulty.configureConnection(); err != nil {
			t.Fatalf("configureConnection success faulty store: %v", err)
		}
		if err := successFaulty.CompactIssueActivityAttemptSuccess(issue.ID, 1); err == nil {
			t.Fatal("expected success compaction to fail when update deletion is injected")
		}
		entries, err := successFaulty.ListIssueActivityEntries(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueActivityEntries after failed success compaction: %v", err)
		}
		if len(entries) != 21 {
			t.Fatalf("expected failed success compaction to preserve all entries, got %d", len(entries))
		}
		if err := successFaulty.Close(); err != nil {
			t.Fatalf("Close success faulty store: %v", err)
		}

		diagnosticFaulty := openFaultySQLiteStoreAt(t, dbPath, "delete from issue_activity_updates")
		if err := diagnosticFaulty.configureConnection(); err != nil {
			t.Fatalf("configureConnection diagnostic faulty store: %v", err)
		}
		if err := diagnosticFaulty.CompactIssueActivityAttemptDiagnostic(issue.ID, 1); err == nil {
			t.Fatal("expected diagnostic compaction to fail when update deletion is injected")
		}
		entries, err = diagnosticFaulty.ListIssueActivityEntries(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueActivityEntries after failed diagnostic compaction: %v", err)
		}
		if len(entries) != 21 {
			t.Fatalf("expected failed diagnostic compaction to preserve all entries, got %d", len(entries))
		}
	})

	t.Run("state and maintenance helpers", func(t *testing.T) {
		store := setupTestStore(t)
		project, err := store.CreateProject("Helper project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject helper: %v", err)
		}

		bareIssue, err := store.CreateIssue(project.ID, "", "Bare lifecycle", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue helper: %v", err)
		}

		if err := store.commitTx(nil, true); err != nil {
			t.Fatalf("commitTx(nil): %v", err)
		}

		tx, err := store.db.Begin()
		if err != nil {
			t.Fatalf("Begin expiry tx: %v", err)
		}
		if _, err := tx.Exec(`
			CREATE TABLE expiry_rows (
				id TEXT PRIMARY KEY,
				issue_id TEXT NOT NULL,
				event_ts DATETIME NOT NULL,
				note TEXT NOT NULL
			)`); err != nil {
			_ = tx.Rollback()
			t.Fatalf("create expiry table: %v", err)
		}
		cutoff := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
		rows := []struct {
			id      string
			issueID string
			note    string
			ts      time.Time
		}{
			{id: "old-delete", issueID: "stale-issue", note: "purge", ts: cutoff.Add(-2 * time.Hour)},
			{id: "old-protected", issueID: bareIssue.ID, note: "purge", ts: cutoff.Add(-2 * time.Hour)},
			{id: "old-kept", issueID: "stale-issue", note: "keep", ts: cutoff.Add(-2 * time.Hour)},
			{id: "fresh-delete", issueID: "stale-issue", note: "purge", ts: cutoff.Add(2 * time.Hour)},
		}
		for _, row := range rows {
			if _, err := tx.Exec(`INSERT INTO expiry_rows (id, issue_id, event_ts, note) VALUES (?, ?, ?, ?)`, row.id, row.issueID, row.ts, row.note); err != nil {
				_ = tx.Rollback()
				t.Fatalf("insert expiry row %s: %v", row.id, err)
			}
		}
		if err := deleteExpiredRowsTx(tx, "expiry_rows", "event_ts", "issue_id", cutoff, []string{bareIssue.ID}, "note = 'purge'"); err != nil {
			_ = tx.Rollback()
			t.Fatalf("deleteExpiredRowsTx: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit expiry tx: %v", err)
		}

		for _, tc := range []struct {
			id   string
			want int
		}{
			{id: "old-delete", want: 0},
			{id: "old-protected", want: 1},
			{id: "old-kept", want: 1},
			{id: "fresh-delete", want: 1},
		} {
			var got int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM expiry_rows WHERE id = ?`, tc.id).Scan(&got); err != nil {
				t.Fatalf("query expiry row %s: %v", tc.id, err)
			}
			if got != tc.want {
				t.Fatalf("expiry row %s = %d, want %d", tc.id, got, tc.want)
			}
		}

		if err := store.UpdateIssueStateAndPhase(bareIssue.ID, State("invalid"), WorkflowPhaseImplementation); !IsValidation(err) {
			t.Fatalf("expected invalid state validation error, got %v", err)
		}
		if err := store.UpdateIssueStateAndPhase("missing", StateReady, WorkflowPhaseImplementation); !IsNotFound(err) {
			t.Fatalf("expected missing issue not found error, got %v", err)
		}

		recurring, err := store.CreateIssueWithOptions(project.ID, "", "Recurring lifecycle", "", 0, nil, IssueCreateOptions{
			IssueType: IssueTypeRecurring,
			Cron:      "0 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions recurring: %v", err)
		}
		if err := store.UpdateIssueStateAndPhase(recurring.ID, StateCancelled, WorkflowPhase("invalid")); err != nil {
			t.Fatalf("UpdateIssueStateAndPhase cancelled: %v", err)
		}
		cancelled, err := store.GetIssue(recurring.ID)
		if err != nil {
			t.Fatalf("GetIssue cancelled recurring: %v", err)
		}
		if cancelled.WorkflowPhase != WorkflowPhaseComplete || cancelled.CompletedAt == nil {
			t.Fatalf("expected cancelled issue to complete, got %#v", cancelled)
		}
		recurrence, err := store.GetIssueRecurrence(recurring.ID)
		if err != nil {
			t.Fatalf("GetIssueRecurrence cancelled: %v", err)
		}
		if recurrence == nil || recurrence.Enabled {
			t.Fatalf("expected cancelled recurrence to be disabled, got %#v", recurrence)
		}
	})

	t.Run("delete issue cleans linked rows and reactivates dependents", func(t *testing.T) {
		store := setupTestStore(t)
		project, err := store.CreateProject("Delete project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject delete: %v", err)
		}
		if err := store.UpdateProjectState(project.ID, ProjectStateRunning); err != nil {
			t.Fatalf("UpdateProjectState delete: %v", err)
		}

		target, err := store.CreateIssueWithOptions(project.ID, "", "Target issue", "", 0, nil, IssueCreateOptions{
			IssueType: IssueTypeRecurring,
			Cron:      "0 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions target: %v", err)
		}
		asset, err := store.CreateIssueAsset(target.ID, "preview", bytes.NewReader(samplePNGBytes()))
		if err != nil {
			t.Fatalf("CreateIssueAsset target: %v", err)
		}
		assetContent, assetPath, err := store.GetIssueAssetContent(target.ID, asset.ID)
		if err != nil {
			t.Fatalf("GetIssueAssetContent target: %v", err)
		}
		if assetContent.ID != asset.ID {
			t.Fatalf("unexpected target asset content: %#v", assetContent)
		}

		commentAttachmentPath := filepath.Join(t.TempDir(), "comment.txt")
		if err := os.WriteFile(commentAttachmentPath, []byte("comment attachment"), 0o644); err != nil {
			t.Fatalf("WriteFile comment attachment: %v", err)
		}
		commentBody := "Target comment"
		comment, err := store.CreateIssueComment(target.ID, IssueCommentInput{
			Body: &commentBody,
			Attachments: []IssueCommentAttachmentInput{{
				Path: commentAttachmentPath,
			}},
		})
		if err != nil {
			t.Fatalf("CreateIssueComment target: %v", err)
		}
		if _, commentPath, err := store.GetIssueCommentAttachmentContent(target.ID, comment.ID, comment.Attachments[0].ID); err != nil {
			t.Fatalf("GetIssueCommentAttachmentContent target: %v", err)
		} else {
			commentAttachmentPath = commentPath
		}

		requestedAt := time.Date(2026, 3, 18, 12, 30, 0, 0, time.UTC)
		if err := store.SetIssuePendingPlanApprovalWithContext(target, "Plan the target cleanup", requestedAt, 1, "thread-target", "turn-target"); err != nil {
			t.Fatalf("SetIssuePendingPlanApprovalWithContext target: %v", err)
		}
		if err := store.SetIssuePendingPlanRevision(target.ID, "Revise the target cleanup", requestedAt.Add(5*time.Minute)); err != nil {
			t.Fatalf("SetIssuePendingPlanRevision target: %v", err)
		}
		if err := store.ApplyIssueActivityEvent(target.ID, target.Identifier, 1, appserver.ActivityEvent{
			Type:     "turn.started",
			ThreadID: "thread-target",
			TurnID:   "turn-target",
		}); err != nil {
			t.Fatalf("ApplyIssueActivityEvent target: %v", err)
		}
		if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
			IssueID:    target.ID,
			Identifier: target.Identifier,
			Phase:      string(target.WorkflowPhase),
			Attempt:    1,
			RunKind:    "run_completed",
			UpdatedAt:  time.Now().UTC(),
			AppSession: agentruntime.Session{
				IssueID:         target.ID,
				IssueIdentifier: target.Identifier,
				SessionID:       "session-target",
				ThreadID:        "thread-target",
				ProcessID:       4242,
			},
		}); err != nil {
			t.Fatalf("UpsertIssueExecutionSession target: %v", err)
		}
		if _, err := store.CreateIssueAgentCommand(target.ID, "Cleanup target", IssueAgentCommandPending); err != nil {
			t.Fatalf("CreateIssueAgentCommand target: %v", err)
		}

		blocked, err := store.CreateIssue(project.ID, "", "Blocked issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocked: %v", err)
		}
		if err := store.UpdateIssueState(blocked.ID, StateReady); err != nil {
			t.Fatalf("UpdateIssueState blocked: %v", err)
		}
		if _, err := store.SetIssueBlockers(blocked.ID, []string{target.Identifier}); err != nil {
			t.Fatalf("SetIssueBlockers blocked: %v", err)
		}
		blockedCommand, err := store.CreateIssueAgentCommand(blocked.ID, "Resume after unblock", IssueAgentCommandWaitingForUnblock)
		if err != nil {
			t.Fatalf("CreateIssueAgentCommand blocked: %v", err)
		}

		if err := store.DeleteIssue(target.ID); err != nil {
			t.Fatalf("DeleteIssue target: %v", err)
		}
		if _, err := store.GetIssue(target.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted target issue lookup to fail, got %v", err)
		}
		if _, err := os.Stat(assetPath); !os.IsNotExist(err) {
			t.Fatalf("expected target asset to be cleaned up, got %v", err)
		}
		if _, err := store.GetIssueAsset(target.ID, asset.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted target asset lookup to fail, got %v", err)
		}
		if _, err := os.Stat(commentAttachmentPath); !os.IsNotExist(err) {
			t.Fatalf("expected target comment attachment to be cleaned up, got %v", err)
		}
		if _, err := store.GetIssueComment(target.ID, comment.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted target comment lookup to fail, got %v", err)
		}
		if count, err := store.GetIssuePlanning(target); err != nil || count != nil {
			t.Fatalf("expected deleted target planning to be removed, got %#v err=%v", count, err)
		}
		if recurrence, err := store.GetIssueRecurrence(target.ID); err != nil || recurrence != nil {
			t.Fatalf("expected deleted target recurrence to be removed, got %#v err=%v", recurrence, err)
		}
		if session, err := store.GetIssueExecutionSession(target.ID); !errors.Is(err, sql.ErrNoRows) || session != nil {
			t.Fatalf("expected deleted target execution session to be removed, got %#v err=%v", session, err)
		}
		if entries, err := store.ListIssueActivityEntries(target.ID); err != nil || len(entries) != 0 {
			t.Fatalf("expected deleted target activity entries to be removed, got %#v err=%v", entries, err)
		}
		if commands, err := store.ListIssueAgentCommands(target.ID); err != nil || len(commands) != 0 {
			t.Fatalf("expected deleted target commands to be removed, got %#v err=%v", commands, err)
		}

		reloadedBlocked, err := store.GetIssue(blocked.ID)
		if err != nil {
			t.Fatalf("GetIssue blocked after delete: %v", err)
		}
		if len(reloadedBlocked.BlockedBy) != 0 {
			t.Fatalf("expected blocked issue to be unblocked, got %#v", reloadedBlocked.BlockedBy)
		}
		pending, err := store.ListPendingIssueAgentCommands(blocked.ID)
		if err != nil {
			t.Fatalf("ListPendingIssueAgentCommands blocked after delete: %v", err)
		}
		if len(pending) != 1 || pending[0].ID != blockedCommand.ID || pending[0].Status != IssueAgentCommandPending {
			t.Fatalf("expected blocked command to be reactivated, got %#v", pending)
		}
	})
}

func TestKanbanCoverageMatrixBranches(t *testing.T) {
	t.Run("epics, workspaces, and identifiers", func(t *testing.T) {
		store := setupTestStore(t)
		project, err := store.CreateProject("Branch project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		epic, err := store.CreateEpic(project.ID, "Branch epic", "Branch epic description")
		if err != nil {
			t.Fatalf("CreateEpic: %v", err)
		}

		if _, err := store.CreateEpic("missing", "Missing epic", ""); !IsNotFound(err) {
			t.Fatalf("expected missing project to fail, got %v", err)
		}
		if err := store.UpdateEpic("missing-epic", project.ID, "Updated", ""); !IsNotFound(err) {
			t.Fatalf("expected missing epic update to fail, got %v", err)
		}
		if err := store.UpdateEpic(epic.ID, "missing-project", "Updated", ""); !IsNotFound(err) {
			t.Fatalf("expected missing project update to fail, got %v", err)
		}
		if err := store.UpdateEpic(epic.ID, project.ID, "Updated epic", "Updated description"); err != nil {
			t.Fatalf("UpdateEpic: %v", err)
		}

		otherProject, err := store.CreateProject("Other project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject other: %v", err)
		}
		resolvedProjectID, resolvedEpicID, err := store.resolveIssueAssociations("", epic.ID)
		if err != nil {
			t.Fatalf("resolveIssueAssociations(epic): %v", err)
		}
		if resolvedProjectID != project.ID || resolvedEpicID != epic.ID {
			t.Fatalf("unexpected resolved associations: %q %q", resolvedProjectID, resolvedEpicID)
		}
		if _, _, err := store.resolveIssueAssociations(otherProject.ID, epic.ID); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected mismatched epic/project validation error, got %v", err)
		}
		if _, _, err := store.resolveIssueAssociations("", "missing"); !IsNotFound(err) {
			t.Fatalf("expected missing epic lookup to fail, got %v", err)
		}
		if _, _, err := store.resolveIssueAssociations("missing-project", ""); !IsNotFound(err) {
			t.Fatalf("expected missing project lookup to fail, got %v", err)
		}

		cleanupProject, err := store.CreateProject("Cleanup project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject cleanup: %v", err)
		}
		cleanupEpic, err := store.CreateEpic(cleanupProject.ID, "Cleanup epic", "")
		if err != nil {
			t.Fatalf("CreateEpic cleanup: %v", err)
		}
		cleanupIssue, err := store.CreateIssue(cleanupProject.ID, cleanupEpic.ID, "Cleanup issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue cleanup: %v", err)
		}
		if err := store.DeleteProject("missing-project"); !IsNotFound(err) {
			t.Fatalf("expected missing project delete to fail, got %v", err)
		}
		if err := store.DeleteProject(cleanupProject.ID); err != nil {
			t.Fatalf("DeleteProject: %v", err)
		}
		if _, err := store.GetProject(cleanupProject.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted project lookup to fail, got %v", err)
		}
		if _, err := store.GetIssue(cleanupIssue.ID); !IsNotFound(err) {
			t.Fatalf("expected deleted issue lookup to fail, got %v", err)
		}

		issue, err := store.CreateIssue(project.ID, epic.ID, "Workspace issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue workspace issue: %v", err)
		}

		if err := store.DeleteWorkspace(issue.ID); err != nil {
			t.Fatalf("DeleteWorkspace no-op: %v", err)
		}

		workspacePath := filepath.Join(t.TempDir(), "workspace")
		if _, err := store.CreateWorkspace(issue.ID, workspacePath); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		updatedPath := workspacePath + "-updated"
		updatedWorkspace, err := store.UpdateWorkspacePath(issue.ID, updatedPath)
		if err != nil {
			t.Fatalf("UpdateWorkspacePath: %v", err)
		}
		if updatedWorkspace.Path != updatedPath {
			t.Fatalf("unexpected updated workspace path: %#v", updatedWorkspace)
		}
		if err := store.DeleteWorkspace(issue.ID); err != nil {
			t.Fatalf("DeleteWorkspace: %v", err)
		}
		if _, err := os.Stat(updatedPath); !os.IsNotExist(err) {
			t.Fatalf("expected workspace path to be removed, got err=%v", err)
		}
		if _, err := store.UpdateWorkspacePath(issue.ID, updatedPath); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected workspace update after delete to fail, got %v", err)
		}

		if err := store.DeleteEpic(epic.ID); err != nil {
			t.Fatalf("DeleteEpic: %v", err)
		}
		loadedIssue, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue after DeleteEpic: %v", err)
		}
		if loadedIssue.EpicID != "" {
			t.Fatalf("expected epic reference to be cleared, got %#v", loadedIssue.EpicID)
		}
		if err := store.DeleteEpic("missing-epic"); !IsNotFound(err) {
			t.Fatalf("expected missing epic delete to fail, got %v", err)
		}

		tx, err := store.db.Begin()
		if err != nil {
			t.Fatalf("Begin identifier tx: %v", err)
		}
		firstID, err := generateIdentifierTx(tx, "HELP")
		if err != nil {
			t.Fatalf("generateIdentifierTx first: %v", err)
		}
		secondID, err := generateIdentifierTx(tx, "HELP")
		if err != nil {
			t.Fatalf("generateIdentifierTx second: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("Commit identifier tx: %v", err)
		}
		if firstID != "HELP-1" || secondID != "HELP-2" {
			t.Fatalf("unexpected identifier sequence: %q, %q", firstID, secondID)
		}

		treeRoot := filepath.Join(t.TempDir(), "tree")
		if err := os.MkdirAll(filepath.Join(treeRoot, "nested"), 0o755); err != nil {
			t.Fatalf("MkdirAll tree: %v", err)
		}
		if err := os.WriteFile(filepath.Join(treeRoot, "nested", "file.txt"), []byte("hello"), 0o644); err != nil {
			t.Fatalf("WriteFile tree: %v", err)
		}
		if err := makeTreeUserWritable(treeRoot); err != nil {
			t.Fatalf("makeTreeUserWritable: %v", err)
		}

		symlinkPath := filepath.Join(treeRoot, "symlink.txt")
		targetPath := filepath.Join(treeRoot, "nested", "file.txt")
		if err := os.Symlink(targetPath, symlinkPath); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
		if err := makeTreeUserWritable(treeRoot); err != nil {
			t.Fatalf("makeTreeUserWritable with symlink: %v", err)
		}

		if err := store.Close(); err != nil {
			t.Fatalf("Close store before identifier failure checks: %v", err)
		}
		faultyStore := openFaultySQLiteStoreAt(t, store.dbPath, "insert into counters")
		if err := faultyStore.configureConnection(); err != nil {
			t.Fatalf("configureConnection faulty identifier store: %v", err)
		}
		faultyTx, err := faultyStore.db.Begin()
		if err != nil {
			t.Fatalf("Begin faulty identifier tx: %v", err)
		}
		if _, err := generateIdentifierTx(faultyTx, "FAIL"); err == nil {
			_ = faultyTx.Rollback()
			t.Fatal("expected generateIdentifierTx to fail when counter insert is injected")
		} else if rbErr := faultyTx.Rollback(); rbErr != nil {
			t.Fatalf("Rollback faulty identifier tx: %v", rbErr)
		}
	})

	t.Run("plan and recurrence guards", func(t *testing.T) {
		store := setupTestStore(t)
		project, err := store.CreateProject("Planning project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		planIssue, err := store.CreateIssue(project.ID, "", "Plan issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue plan: %v", err)
		}
		recurringIssue, err := store.CreateIssueWithOptions(project.ID, "", "Recurring issue", "", 0, nil, IssueCreateOptions{
			IssueType: IssueTypeRecurring,
			Cron:      "0 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions recurring: %v", err)
		}

		requestedAt := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		if err := store.SetIssuePendingPlanApprovalWithContext(nil, "Draft plan", requestedAt, 1, "thread-1", "turn-1"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected nil issue validation error, got %v", err)
		}
		if err := store.SetIssuePendingPlanApprovalWithContext(planIssue, "", requestedAt, 1, "thread-1", "turn-1"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty markdown validation error, got %v", err)
		}
		if err := store.SetIssuePendingPlanApprovalWithContext(planIssue, "Draft plan", requestedAt, 1, "thread-1", "turn-1"); err != nil {
			t.Fatalf("SetIssuePendingPlanApprovalWithContext: %v", err)
		}
		tx, err := store.db.Begin()
		if err != nil {
			t.Fatalf("Begin approval tx: %v", err)
		}
		if err := store.SetIssuePendingPlanApprovalWithContextTx(tx, nil, "Draft plan", requestedAt, 0, "", ""); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected nil issue tx validation error, got %v", err)
		}
		if err := store.SetIssuePendingPlanApprovalWithContextTx(tx, planIssue, "", requestedAt, 0, "", ""); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty markdown tx validation error, got %v", err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatalf("Rollback approval tx: %v", err)
		}
		if err := store.ClearIssuePendingPlanApproval("missing", "done"); !IsNotFound(err) {
			t.Fatalf("expected missing plan approval clear to fail, got %v", err)
		}
		if err := store.ClearIssuePendingPlanApproval(planIssue.ID, "done"); err != nil {
			t.Fatalf("ClearIssuePendingPlanApproval: %v", err)
		}

		if err := store.SetIssuePendingPlanRevision("", "Revise the rollout", requestedAt); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected blank issue validation error, got %v", err)
		}
		if err := store.SetIssuePendingPlanRevision(planIssue.ID, "", requestedAt); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty revision validation error, got %v", err)
		}
		if err := store.SetIssuePendingPlanRevision("missing", "Revise the rollout", requestedAt); !IsNotFound(err) {
			t.Fatalf("expected missing issue plan revision to fail, got %v", err)
		}
		if err := store.SetIssuePendingPlanRevision(planIssue.ID, "Revise the rollout", requestedAt); err != nil {
			t.Fatalf("SetIssuePendingPlanRevision: %v", err)
		}
		if err := store.ClearIssuePendingPlanRevision("missing", "done"); !IsNotFound(err) {
			t.Fatalf("expected missing plan revision clear to fail, got %v", err)
		}
		if err := store.ClearIssuePendingPlanRevision(planIssue.ID, "done"); err != nil {
			t.Fatalf("ClearIssuePendingPlanRevision: %v", err)
		}

		if err := store.MarkRecurringPendingRerun("missing", true); !IsNotFound(err) {
			t.Fatalf("expected missing recurrence to fail, got %v", err)
		}
		if err := store.MarkRecurringPendingRerun(recurringIssue.ID, true); err != nil {
			t.Fatalf("MarkRecurringPendingRerun: %v", err)
		}
		if err := store.MarkRecurringPendingRerun(recurringIssue.ID, false); err != nil {
			t.Fatalf("MarkRecurringPendingRerun(false): %v", err)
		}
		if err := store.RearmRecurringIssue(planIssue.ID, requestedAt, nil); !IsNotFound(err) {
			t.Fatalf("expected non-recurring issue rearm to fail, got %v", err)
		}
		if err := store.RearmRecurringIssue(recurringIssue.ID, requestedAt, nil); err != nil {
			t.Fatalf("RearmRecurringIssue(nil): %v", err)
		}
		loadedRecurring, err := store.GetIssueRecurrence(recurringIssue.ID)
		if err != nil {
			t.Fatalf("GetIssueRecurrence: %v", err)
		}
		if loadedRecurring == nil || loadedRecurring.PendingRerun {
			t.Fatalf("expected rearmed recurrence to clear pending rerun, got %#v", loadedRecurring)
		}
	})

	t.Run("comments, assets, and activity guards", func(t *testing.T) {
		store := setupTestStore(t)
		project, err := store.CreateProject("Comments project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		issue, err := store.CreateIssue(project.ID, "", "Comment issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}

		body := "A comment"
		if _, err := store.CreateIssueComment("", IssueCommentInput{Body: &body}); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected blank issue validation error, got %v", err)
		}
		if _, err := store.CreateIssueComment(issue.ID, IssueCommentInput{}); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected empty comment validation error, got %v", err)
		}
		if _, err := store.CreateIssueComment("missing", IssueCommentInput{Body: &body}); !IsNotFound(err) {
			t.Fatalf("expected missing issue comment create to fail, got %v", err)
		}
		missingAttachment := filepath.Join(t.TempDir(), "missing-comment-attachment.txt")
		if _, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
			Body: &body,
			Attachments: []IssueCommentAttachmentInput{{
				Path: missingAttachment,
			}},
		}); err == nil {
			t.Fatal("expected missing attachment path to fail")
		}

		parent, err := store.CreateIssueComment(issue.ID, IssueCommentInput{Body: &body})
		if err != nil {
			t.Fatalf("CreateIssueComment parent: %v", err)
		}
		if _, err := store.CreateIssueComment(issue.ID, IssueCommentInput{Body: &body, ParentCommentID: "missing"}); !IsNotFound(err) {
			t.Fatalf("expected missing parent comment to fail, got %v", err)
		}
		reply, err := store.CreateIssueComment(issue.ID, IssueCommentInput{Body: &body, ParentCommentID: parent.ID})
		if err != nil {
			t.Fatalf("CreateIssueComment reply: %v", err)
		}
		if _, err := store.CreateIssueComment(issue.ID, IssueCommentInput{Body: &body, ParentCommentID: reply.ID}); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected reply-to-reply validation error, got %v", err)
		}
		if err := store.DeleteIssueComment(issue.ID, "missing"); !IsNotFound(err) {
			t.Fatalf("expected missing comment delete to fail, got %v", err)
		}
		if err := store.DeleteIssueComment(issue.ID, parent.ID); err != nil {
			t.Fatalf("DeleteIssueComment parent: %v", err)
		}
		softDeleted, err := store.GetIssueComment(issue.ID, parent.ID)
		if err != nil {
			t.Fatalf("GetIssueComment after soft delete: %v", err)
		}
		if softDeleted.DeletedAt == nil {
			t.Fatalf("expected soft-deleted parent comment to keep deleted_at, got %#v", softDeleted)
		}
		if err := store.DeleteIssueComment(issue.ID, reply.ID); err != nil {
			t.Fatalf("DeleteIssueComment reply: %v", err)
		}

		asset, err := store.CreateIssueAsset(issue.ID, "preview", bytes.NewReader(samplePNGBytes()))
		if err != nil {
			t.Fatalf("CreateIssueAsset: %v", err)
		}
		if _, err := store.issueAssetPath("../escape"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected invalid stored asset path validation error, got %v", err)
		}
		if err := store.DeleteIssueAsset(issue.ID, "missing"); !IsNotFound(err) {
			t.Fatalf("expected missing asset delete to fail, got %v", err)
		}
		if _, err := store.db.Exec(`
			INSERT INTO issue_assets (id, issue_id, filename, content_type, byte_size, storage_path, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"ast-invalid", issue.ID, "invalid.bin", "application/octet-stream", 1, "../escape", time.Now().UTC(), time.Now().UTC(),
		); err != nil {
			t.Fatalf("insert invalid asset row: %v", err)
		}
		if err := store.DeleteIssueAsset(issue.ID, "ast-invalid"); !errors.Is(err, ErrValidation) {
			t.Fatalf("expected invalid stored asset path validation error, got %v", err)
		}
		if err := store.DeleteIssueAsset(issue.ID, asset.ID); err != nil {
			t.Fatalf("DeleteIssueAsset: %v", err)
		}

		if err := store.ApplyIssueActivityEvent("", issue.Identifier, 1, agentruntime.ActivityEvent{Type: "turn.started"}); err == nil {
			t.Fatal("expected missing issue id to fail")
		}
		if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, agentruntime.ActivityEvent{
			Type:     "item.commandExecution.outputDelta",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			ItemID:   "item-1",
			Delta:    "output chunk",
		}); err != nil {
			t.Fatalf("ApplyIssueActivityEvent(outputDelta): %v", err)
		}
		entries, err := store.ListIssueActivityEntries(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueActivityEntries: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected one activity entry, got %#v", entries)
		}
		var updatesCount int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM issue_activity_updates WHERE issue_id = ?`, issue.ID).Scan(&updatesCount); err != nil {
			t.Fatalf("query activity updates count: %v", err)
		}
		if updatesCount != 0 {
			t.Fatalf("expected skipped update row for output delta, got %d", updatesCount)
		}

		if err := store.AppendRuntimeEventOnly("run_started", nil); err != nil {
			t.Fatalf("AppendRuntimeEventOnly nil payload: %v", err)
		}
		if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
			"issue_id":     issue.ID,
			"identifier":   issue.Identifier,
			"attempt":      1,
			"total_tokens": 7,
			"ts":           "not-a-time",
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent invalid ts: %v", err)
		}
		if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
			"bad": make(chan int),
		}); err == nil {
			t.Fatal("expected append runtime event marshal failure")
		}
		if err := store.AppendRuntimeEventOnly("run_completed", map[string]interface{}{
			"bad": make(chan int),
		}); err == nil {
			t.Fatal("expected append runtime event only marshal failure")
		}
	})
}

func TestKanbanCoverageFaultInjectedChangeEventBranches(t *testing.T) {
	newFaultyStore := func(t *testing.T, dbPath string) *Store {
		t.Helper()
		store := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
		if err := store.configureConnection(); err != nil {
			t.Fatalf("configureConnection: %v", err)
		}
		return store
	}

	t.Run("comment and asset create cleanup", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "coverage.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Faulty create issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		attachmentPath := filepath.Join(t.TempDir(), "attachment.txt")
		if err := os.WriteFile(attachmentPath, []byte("comment attachment"), 0o644); err != nil {
			t.Fatalf("WriteFile attachment: %v", err)
		}
		body := "Fault injection comment"

		commentStore := newFaultyStore(t, dbPath)
		if _, err := commentStore.CreateIssueComment(issue.ID, IssueCommentInput{
			Body: &body,
			Attachments: []IssueCommentAttachmentInput{{
				Path: attachmentPath,
			}},
		}); err == nil {
			t.Fatal("expected comment create to fail when change-events write is injected")
		}
		if comments, err := commentStore.ListIssueComments(issue.ID); err != nil || len(comments) != 0 {
			t.Fatalf("expected comment create rollback to leave no comments, got %#v err=%v", comments, err)
		}
		commentRoot := filepath.Join(commentStore.IssueCommentAssetRoot(), issue.ID)
		if entries, err := os.ReadDir(commentRoot); err == nil && len(entries) > 0 {
			t.Fatalf("expected staged comment attachments to be cleaned up, got %#v", entries)
		}
		if err := commentStore.Close(); err != nil {
			t.Fatalf("Close comment store: %v", err)
		}

		assetStore := newFaultyStore(t, dbPath)
		if _, err := assetStore.CreateIssueAsset(issue.ID, "preview", bytes.NewReader(samplePNGBytes())); err == nil {
			t.Fatal("expected asset create to fail when change-events write is injected")
		}
		if assets, err := assetStore.ListIssueAssets(issue.ID); err != nil || len(assets) != 0 {
			t.Fatalf("expected asset create rollback to leave no assets, got %#v err=%v", assets, err)
		}
		assetRoot := filepath.Join(assetStore.IssueAssetRoot(), issue.ID)
		if entries, err := os.ReadDir(assetRoot); err == nil && len(entries) > 0 {
			t.Fatalf("expected staged asset files to be cleaned up, got %#v", entries)
		}
	})

	t.Run("comment and asset delete rollback", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "coverage.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Faulty delete issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		commentBody := "Comment to delete"
		commentAttachment := filepath.Join(t.TempDir(), "comment.txt")
		if err := os.WriteFile(commentAttachment, []byte("comment attachment"), 0o644); err != nil {
			t.Fatalf("WriteFile comment attachment: %v", err)
		}
		comment, err := base.CreateIssueComment(issue.ID, IssueCommentInput{
			Body: &commentBody,
			Attachments: []IssueCommentAttachmentInput{{
				Path: commentAttachment,
			}},
		})
		if err != nil {
			t.Fatalf("CreateIssueComment: %v", err)
		}
		asset, err := base.CreateIssueAsset(issue.ID, "preview", bytes.NewReader(samplePNGBytes()))
		if err != nil {
			t.Fatalf("CreateIssueAsset: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		commentStore := newFaultyStore(t, dbPath)
		if err := commentStore.DeleteIssueComment(issue.ID, comment.ID); err == nil {
			t.Fatal("expected comment delete to fail when change-events write is injected")
		}
		if _, path, err := commentStore.GetIssueCommentAttachmentContent(issue.ID, comment.ID, comment.Attachments[0].ID); err != nil {
			t.Fatalf("expected comment attachment to survive rollback, got %v", err)
		} else if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("expected comment attachment file to remain, got %v", statErr)
		}
		if err := commentStore.Close(); err != nil {
			t.Fatalf("Close comment store: %v", err)
		}

		assetStore := newFaultyStore(t, dbPath)
		if err := assetStore.DeleteIssueAsset(issue.ID, asset.ID); err == nil {
			t.Fatal("expected asset delete to fail when change-events write is injected")
		}
		if _, path, err := assetStore.GetIssueAssetContent(issue.ID, asset.ID); err != nil {
			t.Fatalf("expected asset to survive rollback, got %v", err)
		} else if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("expected asset file to remain, got %v", statErr)
		}
	})

	t.Run("approval and recurrence rollback", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "coverage.db")
		base := openSQLiteStoreAt(t, dbPath)
		blankIssue, err := base.CreateIssue("", "", "Blank approval issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blank approval: %v", err)
		}
		approvedAt := time.Date(2026, 3, 18, 11, 45, 0, 0, time.UTC)
		command, err := base.ApproveIssuePlanWithNote(blankIssue, approvedAt, "   ", "")
		if err != nil {
			t.Fatalf("ApproveIssuePlanWithNote blank note: %v", err)
		}
		if command != nil {
			t.Fatalf("expected blank note approval to skip command creation, got %#v", command)
		}

		planIssue, err := base.CreateIssue("", "", "Faulty approval issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue plan: %v", err)
		}
		recurringIssue, err := base.CreateIssueWithOptions("", "", "Faulty recurring issue", "", 0, nil, IssueCreateOptions{
			IssueType: IssueTypeRecurring,
			Cron:      "0 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions recurring: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		approvalStore := newFaultyStore(t, dbPath)
		if _, err := approvalStore.ApproveIssuePlanWithNote(planIssue, approvedAt, "Ship the guarded rollout.", ""); err == nil {
			t.Fatal("expected approval with note to fail when change-events write is injected")
		}
		updated, err := approvalStore.GetIssue(planIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue after approval rollback: %v", err)
		}
		if updated.PermissionProfile != PermissionProfileDefault || updated.CollaborationModeOverride != CollaborationModeOverrideNone {
			t.Fatalf("expected approval rollback to preserve issue permissions, got %#v", updated)
		}
		if err := approvalStore.Close(); err != nil {
			t.Fatalf("Close approval store: %v", err)
		}

		recurrenceStore := newFaultyStore(t, dbPath)
		enqueuedAt := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
		if err := recurrenceStore.RearmRecurringIssue(recurringIssue.ID, enqueuedAt, nil); err == nil {
			t.Fatal("expected recurring rearm to fail when change-events write is injected")
		}
		loadedRecurring, err := recurrenceStore.GetIssue(recurringIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue recurring after rollback: %v", err)
		}
		if loadedRecurring.State != StateBacklog || loadedRecurring.WorkflowPhase != WorkflowPhaseImplementation {
			t.Fatalf("expected recurrence rollback to preserve issue state, got %#v", loadedRecurring)
		}
		if recurrence, err := recurrenceStore.GetIssueRecurrence(recurringIssue.ID); err != nil || recurrence == nil {
			t.Fatalf("expected recurrence row to remain after rollback, got recurrence=%#v err=%v", recurrence, err)
		}
	})

	t.Run("activity no-op and rollback", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "coverage.db")
		base := openSQLiteStoreAt(t, dbPath)
		issue, err := base.CreateIssue("", "", "Activity issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		store := newFaultyStore(t, dbPath)
		if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, agentruntime.ActivityEvent{Type: "custom_event"}); err != nil {
			t.Fatalf("expected unsupported activity event to no-op, got %v", err)
		}
		if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, agentruntime.ActivityEvent{
			Type:     "turn.started",
			ThreadID: "thread-1",
			TurnID:   "turn-1",
		}); err == nil {
			t.Fatal("expected activity event persistence to fail when change-events write is injected")
		}
		entries, err := store.ListIssueActivityEntries(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueActivityEntries after rollback: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("expected activity rollback to leave no entries, got %#v", entries)
		}
	})
}

func TestKanbanCoverageFaultInjectedMutationBranches(t *testing.T) {
	newFaultyMigratedStore := func(t *testing.T, failPattern string) *Store {
		t.Helper()
		store := openFaultySQLiteStore(t, failPattern)
		if err := store.configureConnection(); err != nil {
			t.Fatalf("configureConnection: %v", err)
		}
		if err := store.migrate(); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		return store
	}

	t.Run("project delete rolls back nested cleanup when change-events write fails", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "coverage.db")
		base := openSQLiteStoreAt(t, dbPath)
		project, err := base.CreateProject("Rollback project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		epic, err := base.CreateEpic(project.ID, "Rollback epic", "")
		if err != nil {
			t.Fatalf("CreateEpic: %v", err)
		}
		issue, err := base.CreateIssueWithOptions(project.ID, epic.ID, "Rollback issue", "cleanup related rows", 2, []string{"alpha"}, IssueCreateOptions{
			IssueType: IssueTypeRecurring,
			Cron:      "*/15 * * * *",
		})
		if err != nil {
			t.Fatalf("CreateIssueWithOptions: %v", err)
		}
		if err := base.UpdateIssue(issue.ID, map[string]interface{}{
			"agent_name":   "planner",
			"agent_prompt": "  refine the rollout  ",
		}); err != nil {
			t.Fatalf("UpdateIssue agent fields: %v", err)
		}
		workspacePath := filepath.Join(t.TempDir(), issue.Identifier)
		if err := os.MkdirAll(workspacePath, 0o755); err != nil {
			t.Fatalf("MkdirAll workspace: %v", err)
		}
		if _, err := base.CreateWorkspace(issue.ID, workspacePath); err != nil {
			t.Fatalf("CreateWorkspace: %v", err)
		}
		commentDir := t.TempDir()
		commentAttachmentPath := filepath.Join(commentDir, "note.txt")
		if err := os.WriteFile(commentAttachmentPath, []byte("rollback comment attachment"), 0o644); err != nil {
			t.Fatalf("WriteFile comment attachment: %v", err)
		}
		commentBody := "Rollback comment"
		if _, err := base.CreateIssueComment(issue.ID, IssueCommentInput{
			Body: &commentBody,
			Attachments: []IssueCommentAttachmentInput{{
				Path: commentAttachmentPath,
			}},
		}); err != nil {
			t.Fatalf("CreateIssueComment: %v", err)
		}
		if _, err := base.CreateIssueAsset(issue.ID, "preview", bytes.NewReader(samplePNGBytes())); err != nil {
			t.Fatalf("CreateIssueAsset: %v", err)
		}
		requestedAt := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		if err := base.SetIssuePendingPlanApproval(issue.ID, "Initial plan", requestedAt); err != nil {
			t.Fatalf("SetIssuePendingPlanApproval: %v", err)
		}
		if err := base.SetIssuePendingPlanRevision(issue.ID, "Need a tighter rollback step.", requestedAt.Add(15*time.Minute)); err != nil {
			t.Fatalf("SetIssuePendingPlanRevision: %v", err)
		}
		blockedIssue, err := base.CreateIssue(project.ID, "", "Blocked issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue blocked: %v", err)
		}
		if err := base.UpdateIssueState(blockedIssue.ID, StateReady); err != nil {
			t.Fatalf("UpdateIssueState blocked: %v", err)
		}
		if _, err := base.CreateIssueAgentCommand(issue.ID, "Cleanup target", IssueAgentCommandPending); err != nil {
			t.Fatalf("CreateIssueAgentCommand target: %v", err)
		}
		blockedCommand, err := base.CreateIssueAgentCommand(blockedIssue.ID, "Run after unblock", IssueAgentCommandWaitingForUnblock)
		if err != nil {
			t.Fatalf("CreateIssueAgentCommand blocked: %v", err)
		}
		if _, err := base.SetIssueBlockers(blockedIssue.ID, []string{issue.Identifier}); err != nil {
			t.Fatalf("SetIssueBlockers blocked: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		faulty := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
		if err := faulty.configureConnection(); err != nil {
			t.Fatalf("configureConnection faulty store: %v", err)
		}
		if err := faulty.DeleteProject(project.ID); err == nil {
			t.Fatal("expected DeleteProject to fail when change-events write is injected")
		}

		loadedIssue, err := faulty.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue after rollback: %v", err)
		}
		if loadedIssue.ProjectID != project.ID || loadedIssue.EpicID != epic.ID {
			t.Fatalf("expected rollback to preserve issue associations, got %#v", loadedIssue)
		}
		if workspace, err := faulty.GetWorkspace(issue.ID); err != nil || workspace.Path != workspacePath {
			t.Fatalf("expected rollback to preserve workspace, got workspace=%#v err=%v", workspace, err)
		}
		comments, err := faulty.ListIssueComments(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueComments after rollback: %v", err)
		}
		if len(comments) != 1 || len(comments[0].Attachments) != 1 {
			t.Fatalf("expected rollback to preserve comment attachments, got %#v", comments)
		}
		assets, err := faulty.ListIssueAssets(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueAssets after rollback: %v", err)
		}
		if len(assets) != 1 {
			t.Fatalf("expected rollback to preserve assets, got %#v", assets)
		}
		planning, err := faulty.GetIssuePlanning(loadedIssue)
		if err != nil {
			t.Fatalf("GetIssuePlanning after rollback: %v", err)
		}
		if planning == nil || planning.Status != IssuePlanningStatusRevisionRequested {
			t.Fatalf("expected rollback to preserve planning state, got %#v", planning)
		}
		recurrence, err := faulty.GetIssueRecurrence(issue.ID)
		if err != nil {
			t.Fatalf("GetIssueRecurrence after rollback: %v", err)
		}
		if recurrence == nil || !recurrence.Enabled {
			t.Fatalf("expected rollback to preserve recurrence, got %#v", recurrence)
		}
		commands, err := faulty.ListIssueAgentCommands(issue.ID)
		if err != nil {
			t.Fatalf("ListIssueAgentCommands after rollback: %v", err)
		}
		if len(commands) != 1 {
			t.Fatalf("expected rollback to preserve issue commands, got %#v", commands)
		}
		reloadedBlocked, err := faulty.GetIssue(blockedIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue blocked after rollback: %v", err)
		}
		if len(reloadedBlocked.BlockedBy) != 1 || reloadedBlocked.BlockedBy[0] != issue.Identifier {
			t.Fatalf("expected blocked issue to remain blocked, got %#v", reloadedBlocked.BlockedBy)
		}
		pending, err := faulty.ListPendingIssueAgentCommands(blockedIssue.ID)
		if err != nil {
			t.Fatalf("ListPendingIssueAgentCommands blocked after rollback: %v", err)
		}
		if len(pending) != 0 {
			t.Fatalf("expected waiting-for-unblock command to stay out of the pending list, got %#v", pending)
		}
		var blockedStatus string
		if err := faulty.db.QueryRow(`SELECT status FROM issue_agent_commands WHERE id = ?`, blockedCommand.ID).Scan(&blockedStatus); err != nil {
			t.Fatalf("query blocked command status after rollback: %v", err)
		}
		if blockedStatus != string(IssueAgentCommandWaitingForUnblock) {
			t.Fatalf("expected blocked command to remain waiting, got %q", blockedStatus)
		}
	})

	t.Run("workspace and project mutations surface change-event failures", func(t *testing.T) {
		t.Run("create workspace", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "coverage.db")
			base := openSQLiteStoreAt(t, dbPath)
			issue, err := base.CreateIssue("", "", "Workspace create issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
			if err := store.configureConnection(); err != nil {
				t.Fatalf("configureConnection faulty store: %v", err)
			}
			workspacePath := filepath.Join(t.TempDir(), "workspace")
			if _, err := store.CreateWorkspace(issue.ID, workspacePath); err == nil {
				t.Fatal("expected CreateWorkspace to fail when change-events write is injected")
			}
			workspace, err := store.GetWorkspace(issue.ID)
			if err != nil {
				t.Fatalf("GetWorkspace after failed create: %v", err)
			}
			if workspace.Path != workspacePath {
				t.Fatalf("expected workspace row to persist before change-event failure, got %#v", workspace)
			}
		})

		t.Run("update workspace run", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "coverage.db")
			base := openSQLiteStoreAt(t, dbPath)
			issue, err := base.CreateIssue("", "", "Workspace run issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			workspacePath := filepath.Join(t.TempDir(), "workspace")
			if _, err := base.CreateWorkspace(issue.ID, workspacePath); err != nil {
				t.Fatalf("CreateWorkspace: %v", err)
			}
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
			if err := store.configureConnection(); err != nil {
				t.Fatalf("configureConnection faulty store: %v", err)
			}
			if err := store.UpdateWorkspaceRun(issue.ID); err == nil {
				t.Fatal("expected UpdateWorkspaceRun to fail when change-events write is injected")
			}
			workspace, err := store.GetWorkspace(issue.ID)
			if err != nil {
				t.Fatalf("GetWorkspace after failed run update: %v", err)
			}
			if workspace.RunCount != 1 || workspace.LastRunAt == nil {
				t.Fatalf("expected run count increment to persist before change-event failure, got %#v", workspace)
			}
		})

		t.Run("update workspace path", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "coverage.db")
			base := openSQLiteStoreAt(t, dbPath)
			project, err := base.CreateProject("Workspace project", "", "", "")
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			issue, err := base.CreateIssue(project.ID, "", "Workspace issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			workspacePath := filepath.Join(t.TempDir(), "workspace")
			if _, err := base.CreateWorkspace(issue.ID, workspacePath); err != nil {
				t.Fatalf("CreateWorkspace: %v", err)
			}
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
			if err := store.configureConnection(); err != nil {
				t.Fatalf("configureConnection faulty store: %v", err)
			}
			updatedPath := workspacePath + "-updated"
			if _, err := store.UpdateWorkspacePath(issue.ID, updatedPath); err == nil {
				t.Fatal("expected UpdateWorkspacePath to fail when change-events write is injected")
			}
			workspace, err := store.GetWorkspace(issue.ID)
			if err != nil {
				t.Fatalf("GetWorkspace after failed update: %v", err)
			}
			if workspace.Path != updatedPath {
				t.Fatalf("expected path update to persist before change-event failure, got %q", workspace.Path)
			}
		})

		t.Run("delete workspace", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "coverage.db")
			base := openSQLiteStoreAt(t, dbPath)
			project, err := base.CreateProject("Workspace delete project", "", "", "")
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			issue, err := base.CreateIssue(project.ID, "", "Workspace delete issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			workspacePath := filepath.Join(t.TempDir(), "workspace")
			if err := os.MkdirAll(workspacePath, 0o755); err != nil {
				t.Fatalf("MkdirAll workspace: %v", err)
			}
			if _, err := base.CreateWorkspace(issue.ID, workspacePath); err != nil {
				t.Fatalf("CreateWorkspace: %v", err)
			}
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
			if err := store.configureConnection(); err != nil {
				t.Fatalf("configureConnection faulty store: %v", err)
			}
			if err := store.DeleteWorkspace(issue.ID); err == nil {
				t.Fatal("expected DeleteWorkspace to fail when change-events write is injected")
			}
			if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
				t.Fatalf("expected workspace tree to be removed before change-event failure, got %v", err)
			}
			if _, err := store.GetWorkspace(issue.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("expected workspace row to be deleted even when change-event append fails, got err=%v", err)
			}
		})
	})

	t.Run("sql helper failures and no-ops", func(t *testing.T) {
		t.Run("identifier generation", func(t *testing.T) {
			store := newFaultyMigratedStore(t, "insert into counters")
			if _, err := store.generateIdentifier(""); err == nil {
				t.Fatal("expected generateIdentifier to fail when counter insert is injected")
			}
		})

		t.Run("transactional delete helpers", func(t *testing.T) {
			cases := []struct {
				name        string
				failPattern string
				call        func(*Store, *sql.Tx) error
			}{
				{
					name:        "planning",
					failPattern: "delete from issue_plan_versions",
					call: func(store *Store, tx *sql.Tx) error {
						return store.deleteIssuePlanningTx(tx, "issue-1")
					},
				},
				{
					name:        "assets",
					failPattern: "delete from issue_assets",
					call: func(store *Store, tx *sql.Tx) error {
						_, err := store.deleteIssueAssetsTx(tx, "issue-1")
						return err
					},
				},
				{
					name:        "comments",
					failPattern: "delete from issue_comment_attachments",
					call: func(store *Store, tx *sql.Tx) error {
						_, err := store.deleteIssueCommentsTx(tx, "issue-1")
						return err
					},
				},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					store := newFaultyMigratedStore(t, tc.failPattern)
					tx, err := store.db.Begin()
					if err != nil {
						t.Fatalf("Begin: %v", err)
					}
					if err := tc.call(store, tx); err == nil {
						_ = tx.Rollback()
						t.Fatalf("expected %s helper to fail when %s is injected", tc.name, tc.failPattern)
					}
					if err := tx.Rollback(); err != nil {
						t.Fatalf("Rollback %s tx: %v", tc.name, err)
					}
				})
			}
		})

		t.Run("comment and activity insert failures", func(t *testing.T) {
			t.Run("comment insert", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Faulty comment insert", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into issue_comments")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				body := "Faulty insert comment"
				if _, err := store.CreateIssueComment(issue.ID, IssueCommentInput{Body: &body}); err == nil {
					t.Fatal("expected comment insert to fail when issue_comments insert is injected")
				}
				comments, err := store.ListIssueComments(issue.ID)
				if err != nil {
					t.Fatalf("ListIssueComments after failed insert: %v", err)
				}
				if len(comments) != 0 {
					t.Fatalf("expected comment insert rollback to leave no comments, got %#v", comments)
				}
			})

			t.Run("comment attachment insert", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Faulty comment attachment insert", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				attachmentPath := filepath.Join(t.TempDir(), "attachment.txt")
				if err := os.WriteFile(attachmentPath, []byte("attachment"), 0o644); err != nil {
					t.Fatalf("WriteFile attachment: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into issue_comment_attachments")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				body := "Faulty attachment insert"
				if _, err := store.CreateIssueComment(issue.ID, IssueCommentInput{
					Body: &body,
					Attachments: []IssueCommentAttachmentInput{{
						Path: attachmentPath,
					}},
				}); err == nil {
					t.Fatal("expected comment attachment insert to fail when issue_comment_attachments insert is injected")
				}
				comments, err := store.ListIssueComments(issue.ID)
				if err != nil {
					t.Fatalf("ListIssueComments after failed attachment insert: %v", err)
				}
				if len(comments) != 0 {
					t.Fatalf("expected attachment insert rollback to leave no comments, got %#v", comments)
				}
			})

			t.Run("activity update insert", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Faulty activity insert", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into issue_activity_updates")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, agentruntime.ActivityEvent{
					Type:     "turn.started",
					ThreadID: "thread-1",
					TurnID:   "turn-1",
				}); err == nil {
					t.Fatal("expected activity update insert to fail when issue_activity_updates insert is injected")
				}
				entries, err := store.ListIssueActivityEntries(issue.ID)
				if err != nil {
					t.Fatalf("ListIssueActivityEntries after failed activity update insert: %v", err)
				}
				if len(entries) != 0 {
					t.Fatalf("expected activity update rollback to leave no entries, got %#v", entries)
				}
			})

			t.Run("activity entry insert", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Faulty activity entry insert", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into issue_activity_entries")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, agentruntime.ActivityEvent{
					Type:      "item.completed",
					ThreadID:  "thread-2",
					TurnID:    "turn-2",
					ItemID:    "item-1",
					ItemType:  "agentMessage",
					ItemPhase: "commentary",
					Item: map[string]interface{}{
						"id":    "item-1",
						"type":  "agentMessage",
						"phase": "commentary",
						"text":  "faulty entry",
					},
				}); err == nil {
					t.Fatal("expected activity entry insert to fail when issue_activity_entries insert is injected")
				}
				entries, err := store.ListIssueActivityEntries(issue.ID)
				if err != nil {
					t.Fatalf("ListIssueActivityEntries after failed activity entry insert: %v", err)
				}
				if len(entries) != 0 {
					t.Fatalf("expected activity entry rollback to leave no entries, got %#v", entries)
				}
			})
		})

		t.Run("asset, comment, and recurrence rollback failures", func(t *testing.T) {
			t.Run("asset insert", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Faulty asset insert", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into issue_assets")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				if _, err := store.CreateIssueAsset(issue.ID, "preview.png", bytes.NewReader(samplePNGBytes())); err == nil {
					t.Fatal("expected asset insert to fail when issue_assets insert is injected")
				}
				assets, err := store.ListIssueAssets(issue.ID)
				if err != nil {
					t.Fatalf("ListIssueAssets after failed insert: %v", err)
				}
				if len(assets) != 0 {
					t.Fatalf("expected asset insert rollback to leave no assets, got %#v", assets)
				}
			})

			t.Run("asset delete", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Faulty asset delete", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				asset, err := base.CreateIssueAsset(issue.ID, "preview.png", bytes.NewReader(samplePNGBytes()))
				if err != nil {
					t.Fatalf("CreateIssueAsset: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				if err := store.DeleteIssueAsset(issue.ID, asset.ID); err == nil {
					t.Fatal("expected asset delete to fail when change-events write is injected")
				}
				loaded, err := store.GetIssueAsset(issue.ID, asset.ID)
				if err != nil {
					t.Fatalf("GetIssueAsset after failed delete: %v", err)
				}
				if loaded.ID != asset.ID {
					t.Fatalf("expected asset delete rollback to preserve the asset, got %#v", loaded)
				}
			})

			t.Run("comment delete attachment cleanup", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Faulty comment delete", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				attachmentPath := filepath.Join(t.TempDir(), "attachment.txt")
				if err := os.WriteFile(attachmentPath, []byte("attachment"), 0o644); err != nil {
					t.Fatalf("WriteFile attachment: %v", err)
				}
				body := "Comment with attachment"
				comment, err := base.CreateIssueComment(issue.ID, IssueCommentInput{
					Body: &body,
					Attachments: []IssueCommentAttachmentInput{{
						Path: attachmentPath,
					}},
				})
				if err != nil {
					t.Fatalf("CreateIssueComment: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "delete from issue_comment_attachments")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				if err := store.DeleteIssueComment(issue.ID, comment.ID); err == nil {
					t.Fatal("expected comment delete to fail when attachment cleanup is injected")
				}
				comments, err := store.ListIssueComments(issue.ID)
				if err != nil {
					t.Fatalf("ListIssueComments after failed delete: %v", err)
				}
				if len(comments) != 1 || len(comments[0].Attachments) != 1 {
					t.Fatalf("expected comment delete rollback to preserve comment attachments, got %#v", comments)
				}
			})

			t.Run("comment soft delete update", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Faulty soft delete", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				parentBody := "Parent comment"
				parent, err := base.CreateIssueComment(issue.ID, IssueCommentInput{Body: &parentBody})
				if err != nil {
					t.Fatalf("CreateIssueComment parent: %v", err)
				}
				replyBody := "Reply comment"
				reply, err := base.CreateIssueComment(issue.ID, IssueCommentInput{
					Body:            &replyBody,
					ParentCommentID: parent.ID,
				})
				if err != nil {
					t.Fatalf("CreateIssueComment reply: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "update issue_comments")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				if err := store.DeleteIssueComment(issue.ID, parent.ID); err == nil {
					t.Fatal("expected soft delete to fail when issue_comments update is injected")
				}
				parentComment, err := store.GetIssueComment(issue.ID, parent.ID)
				if err != nil {
					t.Fatalf("GetIssueComment parent after failed delete: %v", err)
				}
				if strings.TrimSpace(parentComment.Body) != parentBody {
					t.Fatalf("expected parent comment to remain unchanged, got %#v", parentComment)
				}
				reloadedReply, err := store.GetIssueComment(issue.ID, reply.ID)
				if err != nil {
					t.Fatalf("GetIssueComment reply after failed delete: %v", err)
				}
				if reloadedReply.ParentCommentID != parent.ID {
					t.Fatalf("expected reply to remain attached to parent, got %#v", reloadedReply)
				}
			})

			t.Run("recurrence insert and update", func(t *testing.T) {
				t.Run("create recurring issue", func(t *testing.T) {
					store := newFaultyMigratedStore(t, "insert into issue_recurrences")
					if _, err := store.CreateIssueWithOptions("", "", "Faulty recurring issue", "", 0, nil, IssueCreateOptions{
						IssueType: IssueTypeRecurring,
						Cron:      "0 * * * *",
					}); err == nil {
						t.Fatal("expected recurring issue creation to fail when issue_recurrences insert is injected")
					}
				})

				t.Run("mark pending rerun", func(t *testing.T) {
					dbPath := filepath.Join(t.TempDir(), "coverage.db")
					base := openSQLiteStoreAt(t, dbPath)
					issue, err := base.CreateIssueWithOptions("", "", "Recurring to mark", "", 0, nil, IssueCreateOptions{
						IssueType: IssueTypeRecurring,
						Cron:      "0 * * * *",
					})
					if err != nil {
						t.Fatalf("CreateIssueWithOptions: %v", err)
					}
					if err := base.Close(); err != nil {
						t.Fatalf("Close base store: %v", err)
					}

					store := openFaultySQLiteStoreAt(t, dbPath, "update issue_recurrences")
					if err := store.configureConnection(); err != nil {
						t.Fatalf("configureConnection faulty store: %v", err)
					}
					if err := store.MarkRecurringPendingRerun(issue.ID, true); err == nil {
						t.Fatal("expected pending rerun update to fail when issue_recurrences update is injected")
					}
					recurrence, err := store.GetIssueRecurrence(issue.ID)
					if err != nil {
						t.Fatalf("GetIssueRecurrence after failed mark: %v", err)
					}
					if recurrence == nil || recurrence.PendingRerun {
						t.Fatalf("expected pending rerun rollback to leave recurrence unchanged, got %#v", recurrence)
					}
				})

				t.Run("rearm recurring issue", func(t *testing.T) {
					dbPath := filepath.Join(t.TempDir(), "coverage.db")
					base := openSQLiteStoreAt(t, dbPath)
					issue, err := base.CreateIssueWithOptions("", "", "Recurring to rearm", "", 0, nil, IssueCreateOptions{
						IssueType: IssueTypeRecurring,
						Cron:      "*/15 * * * *",
					})
					if err != nil {
						t.Fatalf("CreateIssueWithOptions: %v", err)
					}
					if err := base.MarkRecurringPendingRerun(issue.ID, true); err != nil {
						t.Fatalf("MarkRecurringPendingRerun base: %v", err)
					}
					original, err := base.GetIssue(issue.ID)
					if err != nil {
						t.Fatalf("GetIssue original: %v", err)
					}
					if err := base.Close(); err != nil {
						t.Fatalf("Close base store: %v", err)
					}

					store := openFaultySQLiteStoreAt(t, dbPath, "update issue_recurrences")
					if err := store.configureConnection(); err != nil {
						t.Fatalf("configureConnection faulty store: %v", err)
					}
					enqueuedAt := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
					nextRunAt := enqueuedAt.Add(15 * time.Minute)
					if err := store.RearmRecurringIssue(issue.ID, enqueuedAt, &nextRunAt); err == nil {
						t.Fatal("expected recurring rearm to fail when issue_recurrences update is injected")
					}
					reloaded, err := store.GetIssue(issue.ID)
					if err != nil {
						t.Fatalf("GetIssue after failed rearm: %v", err)
					}
					if reloaded.State != original.State || reloaded.WorkflowPhase != original.WorkflowPhase {
						t.Fatalf("expected failed rearm to leave issue unchanged, got %#v want %#v", reloaded, original)
					}
				})
			})
		})

		t.Run("issue update and delete rollback failures", func(t *testing.T) {
			t.Run("update issue", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				blocker, err := base.CreateIssue("", "", "Blocker issue", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue blocker: %v", err)
				}
				if err := base.UpdateIssueState(blocker.ID, StateReady); err != nil {
					t.Fatalf("UpdateIssueState blocker: %v", err)
				}
				issue, err := base.CreateIssue("", "", "Update target", "original description", 1, []string{"alpha"})
				if err != nil {
					t.Fatalf("CreateIssue target: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				requestedAt := time.Date(2026, 3, 18, 9, 0, 0, 0, time.UTC)
				revisionRequestedAt := requestedAt.Add(15 * time.Minute)
				if err := store.UpdateIssue(issue.ID, map[string]interface{}{
					"title":                              "Updated title",
					"description":                        "Updated description",
					"priority":                           3,
					"agent_name":                         "planner",
					"agent_prompt":                       "  refine the rollout  ",
					"permission_profile":                 PermissionProfilePlanThenFullAccess,
					"collaboration_mode_override":        CollaborationModeOverridePlan,
					"plan_approval_pending":              true,
					"pending_plan_markdown":              "Draft the rollout",
					"pending_plan_requested_at":          &requestedAt,
					"pending_plan_revision_markdown":     "Revise the rollout",
					"pending_plan_revision_requested_at": &revisionRequestedAt,
					"issue_type":                         IssueTypeRecurring,
					"cron":                               "*/15 * * * *",
					"labels":                             []string{"beta", "release"},
					"blocked_by":                         []string{blocker.Identifier},
				}); err == nil {
					t.Fatal("expected UpdateIssue to fail when change-events write is injected")
				}
				reloaded, err := store.GetIssue(issue.ID)
				if err != nil {
					t.Fatalf("GetIssue after failed update: %v", err)
				}
				if reloaded.Title != "Update target" || reloaded.Description != "original description" || reloaded.Priority != 1 || reloaded.IssueType != IssueTypeStandard {
					t.Fatalf("expected rollback to preserve original issue fields, got %#v", reloaded)
				}
				if reloaded.PermissionProfile != PermissionProfileDefault || reloaded.CollaborationModeOverride != CollaborationModeOverrideNone {
					t.Fatalf("expected rollback to preserve original issue access settings, got %#v", reloaded)
				}
				if len(reloaded.BlockedBy) != 0 {
					t.Fatalf("expected rollback to preserve original blockers, got %#v", reloaded.BlockedBy)
				}
				if recurrences, err := store.GetIssueRecurrence(issue.ID); err != nil {
					t.Fatalf("GetIssueRecurrence after failed update: %v", err)
				} else if recurrences != nil {
					t.Fatalf("expected rollback to keep issue non-recurring, got %#v", recurrences)
				}
			})

			t.Run("delete issue", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				project, err := base.CreateProject("Delete project", "", "", "")
				if err != nil {
					t.Fatalf("CreateProject: %v", err)
				}
				blockedIssue, err := base.CreateIssue(project.ID, "", "Blocked issue", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue blocked: %v", err)
				}
				if err := base.UpdateIssueState(blockedIssue.ID, StateReady); err != nil {
					t.Fatalf("UpdateIssueState blocked: %v", err)
				}
				issue, err := base.CreateIssueWithOptions(project.ID, "", "Delete target", "cleanup everything", 2, []string{"alpha"}, IssueCreateOptions{
					IssueType: IssueTypeRecurring,
					Cron:      "*/10 * * * *",
				})
				if err != nil {
					t.Fatalf("CreateIssueWithOptions target: %v", err)
				}
				workspacePath := filepath.Join(t.TempDir(), issue.Identifier)
				if err := os.MkdirAll(workspacePath, 0o755); err != nil {
					t.Fatalf("MkdirAll workspace: %v", err)
				}
				if _, err := base.CreateWorkspace(issue.ID, workspacePath); err != nil {
					t.Fatalf("CreateWorkspace: %v", err)
				}
				commentDir := t.TempDir()
				attachmentPath := filepath.Join(commentDir, "note.txt")
				if err := os.WriteFile(attachmentPath, []byte("comment attachment"), 0o644); err != nil {
					t.Fatalf("WriteFile attachment: %v", err)
				}
				commentBody := "Delete me"
				if _, err := base.CreateIssueComment(issue.ID, IssueCommentInput{
					Body: &commentBody,
					Attachments: []IssueCommentAttachmentInput{{
						Path: attachmentPath,
					}},
				}); err != nil {
					t.Fatalf("CreateIssueComment: %v", err)
				}
				if _, err := base.CreateIssueAsset(issue.ID, "preview.png", bytes.NewReader(samplePNGBytes())); err != nil {
					t.Fatalf("CreateIssueAsset: %v", err)
				}
				requestedAt := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
				if err := base.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
					IssueID:        issue.ID,
					Identifier:     issue.Identifier,
					Phase:          string(WorkflowPhaseImplementation),
					Attempt:        1,
					RunKind:        "run_started",
					ResumeEligible: true,
					UpdatedAt:      requestedAt,
					AppSession: agentruntime.Session{
						IssueID:         issue.ID,
						IssueIdentifier: issue.Identifier,
						SessionID:       "session-delete",
						ThreadID:        "thread-delete",
					},
				}); err != nil {
					t.Fatalf("UpsertIssueExecutionSession: %v", err)
				}
				if err := base.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, agentruntime.ActivityEvent{
					Type:     "turn.started",
					ThreadID: "thread-delete",
					TurnID:   "turn-delete",
				}); err != nil {
					t.Fatalf("ApplyIssueActivityEvent started: %v", err)
				}
				if err := base.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 1, agentruntime.ActivityEvent{
					Type:     "turn.completed",
					ThreadID: "thread-delete",
					TurnID:   "turn-delete",
				}); err != nil {
					t.Fatalf("ApplyIssueActivityEvent completed: %v", err)
				}
				if err := base.SetIssuePendingPlanApproval(issue.ID, "Approve the delete target", requestedAt); err != nil {
					t.Fatalf("SetIssuePendingPlanApproval: %v", err)
				}
				if _, err := base.CreateIssueAgentCommand(issue.ID, "Clean up after delete", IssueAgentCommandPending); err != nil {
					t.Fatalf("CreateIssueAgentCommand: %v", err)
				}
				if _, err := base.SetIssueBlockers(blockedIssue.ID, []string{issue.Identifier}); err != nil {
					t.Fatalf("SetIssueBlockers: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				if err := store.DeleteIssue(issue.ID); err == nil {
					t.Fatal("expected DeleteIssue to fail when change-events write is injected")
				}

				reloaded, err := store.GetIssue(issue.ID)
				if err != nil {
					t.Fatalf("GetIssue after failed delete: %v", err)
				}
				if reloaded.Title != "Delete target" || reloaded.State != StateBacklog {
					t.Fatalf("expected failed delete to preserve issue row, got %#v", reloaded)
				}
				if workspace, err := store.GetWorkspace(issue.ID); err != nil || workspace.Path != workspacePath {
					t.Fatalf("expected failed delete to preserve workspace, got workspace=%#v err=%v", workspace, err)
				}
				comments, err := store.ListIssueComments(issue.ID)
				if err != nil {
					t.Fatalf("ListIssueComments after failed delete: %v", err)
				}
				if len(comments) != 1 || len(comments[0].Attachments) != 1 {
					t.Fatalf("expected failed delete to preserve comment attachments, got %#v", comments)
				}
				assets, err := store.ListIssueAssets(issue.ID)
				if err != nil {
					t.Fatalf("ListIssueAssets after failed delete: %v", err)
				}
				if len(assets) != 1 {
					t.Fatalf("expected failed delete to preserve assets, got %#v", assets)
				}
				recurrence, err := store.GetIssueRecurrence(issue.ID)
				if err != nil {
					t.Fatalf("GetIssueRecurrence after failed delete: %v", err)
				}
				if recurrence == nil || !recurrence.Enabled {
					t.Fatalf("expected failed delete to preserve recurrence, got %#v", recurrence)
				}
				commands, err := store.ListIssueAgentCommands(issue.ID)
				if err != nil {
					t.Fatalf("ListIssueAgentCommands after failed delete: %v", err)
				}
				if len(commands) != 1 {
					t.Fatalf("expected failed delete to preserve agent commands, got %#v", commands)
				}
				reloadedBlocked, err := store.GetIssue(blockedIssue.ID)
				if err != nil {
					t.Fatalf("GetIssue blocked after failed delete: %v", err)
				}
				if len(reloadedBlocked.BlockedBy) != 1 || reloadedBlocked.BlockedBy[0] != issue.Identifier {
					t.Fatalf("expected failed delete to preserve blocker relation, got %#v", reloadedBlocked.BlockedBy)
				}
			})
		})

		t.Run("plan approval and maintenance failures", func(t *testing.T) {
			t.Run("set pending approval rollback", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Pending approval issue", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into issue_plan_versions")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				requestedAt := time.Date(2026, 3, 18, 11, 0, 0, 0, time.UTC)
				if err := store.SetIssuePendingPlanApproval(issue.ID, "Draft the plan", requestedAt); err == nil {
					t.Fatal("expected SetIssuePendingPlanApproval to fail when plan version insert is injected")
				}
				reloaded, err := store.GetIssue(issue.ID)
				if err != nil {
					t.Fatalf("GetIssue after failed approval request: %v", err)
				}
				if reloaded.PlanApprovalPending {
					t.Fatalf("expected rollback to keep plan approval disabled, got %#v", reloaded)
				}
			})

			t.Run("approve plan with note rollback", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Approve plan issue", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				requestedAt := time.Date(2026, 3, 18, 11, 30, 0, 0, time.UTC)
				if err := base.SetIssuePendingPlanApproval(issue.ID, "Approve the rollout", requestedAt); err != nil {
					t.Fatalf("SetIssuePendingPlanApproval: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into issue_agent_commands")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				approvedAt := requestedAt.Add(15 * time.Minute)
				if _, err := store.ApproveIssuePlanWithNote(issue, approvedAt, "Ship it", ""); err == nil {
					t.Fatal("expected ApproveIssuePlanWithNote to fail when command insert is injected")
				}
				reloaded, err := store.GetIssue(issue.ID)
				if err != nil {
					t.Fatalf("GetIssue after failed approval: %v", err)
				}
				if !reloaded.PlanApprovalPending || reloaded.PendingPlanMarkdown == "" {
					t.Fatalf("expected failed approval to preserve pending plan state, got %#v", reloaded)
				}
			})

			t.Run("clear pending approval rollback", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Clear approval issue", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				requestedAt := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
				if err := base.SetIssuePendingPlanApproval(issue.ID, "Clear the rollout", requestedAt); err != nil {
					t.Fatalf("SetIssuePendingPlanApproval: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				if err := store.ClearIssuePendingPlanApproval(issue.ID, "manual_retry"); err == nil {
					t.Fatal("expected ClearIssuePendingPlanApproval to fail when change-events write is injected")
				}
				reloaded, err := store.GetIssue(issue.ID)
				if err != nil {
					t.Fatalf("GetIssue after failed clear: %v", err)
				}
				if !reloaded.PlanApprovalPending {
					t.Fatalf("expected failed clear to preserve pending approval, got %#v", reloaded)
				}
			})

			t.Run("set pending revision rollback", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Revision issue", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				requestedAt := time.Date(2026, 3, 18, 12, 30, 0, 0, time.UTC)
				if err := base.SetIssuePendingPlanApproval(issue.ID, "Approve the revision flow", requestedAt); err != nil {
					t.Fatalf("SetIssuePendingPlanApproval: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				if err := store.SetIssuePendingPlanRevision(issue.ID, "Revise the rollout", requestedAt.Add(10*time.Minute)); err == nil {
					t.Fatal("expected SetIssuePendingPlanRevision to fail when change-events write is injected")
				}
				reloaded, err := store.GetIssue(issue.ID)
				if err != nil {
					t.Fatalf("GetIssue after failed revision request: %v", err)
				}
				if reloaded.PendingPlanRevisionMarkdown != "" {
					t.Fatalf("expected failed revision request to preserve empty revision state, got %#v", reloaded)
				}
			})

			t.Run("clear pending revision rollback", func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "coverage.db")
				base := openSQLiteStoreAt(t, dbPath)
				issue, err := base.CreateIssue("", "", "Clear revision issue", "", 0, nil)
				if err != nil {
					t.Fatalf("CreateIssue: %v", err)
				}
				requestedAt := time.Date(2026, 3, 18, 13, 0, 0, 0, time.UTC)
				if err := base.SetIssuePendingPlanApproval(issue.ID, "Approve the revision clear", requestedAt); err != nil {
					t.Fatalf("SetIssuePendingPlanApproval: %v", err)
				}
				if err := base.SetIssuePendingPlanRevision(issue.ID, "Revise the rollout", requestedAt.Add(10*time.Minute)); err != nil {
					t.Fatalf("SetIssuePendingPlanRevision: %v", err)
				}
				if err := base.Close(); err != nil {
					t.Fatalf("Close base store: %v", err)
				}

				store := openFaultySQLiteStoreAt(t, dbPath, "insert into change_events")
				if err := store.configureConnection(); err != nil {
					t.Fatalf("configureConnection faulty store: %v", err)
				}
				if err := store.ClearIssuePendingPlanRevision(issue.ID, "manual_retry"); err == nil {
					t.Fatal("expected ClearIssuePendingPlanRevision to fail when change-events write is injected")
				}
				reloaded, err := store.GetIssue(issue.ID)
				if err != nil {
					t.Fatalf("GetIssue after failed clear: %v", err)
				}
				if reloaded.PendingPlanRevisionMarkdown == "" {
					t.Fatalf("expected failed clear to preserve revision markdown, got %#v", reloaded)
				}
			})

			t.Run("maintenance delete failures", func(t *testing.T) {
				cases := []struct {
					name        string
					failPattern string
				}{
					{name: "runtime events", failPattern: "delete from runtime_events"},
					{name: "change events", failPattern: "delete from change_events"},
					{name: "activity updates", failPattern: "delete from issue_activity_updates"},
					{name: "activity entries", failPattern: "delete from issue_activity_entries"},
					{name: "execution sessions", failPattern: "delete from issue_execution_sessions"},
				}
				for _, tc := range cases {
					t.Run(tc.name, func(t *testing.T) {
						store := newFaultyMigratedStore(t, tc.failPattern)
						if _, err := store.RunMaintenance(nil); err == nil {
							t.Fatalf("expected RunMaintenance to fail when %s is injected", tc.failPattern)
						}
					})
				}
			})
		})

		t.Run("workspace tree and command guards", func(t *testing.T) {
			if err := makeTreeUserWritable(filepath.Join(t.TempDir(), "missing")); err != nil {
				t.Fatalf("makeTreeUserWritable missing path: %v", err)
			}
			writableRoot := filepath.Join(t.TempDir(), "writable")
			if err := os.MkdirAll(filepath.Join(writableRoot, "nested"), 0o700); err != nil {
				t.Fatalf("MkdirAll writable tree: %v", err)
			}
			if err := os.WriteFile(filepath.Join(writableRoot, "nested", "file.txt"), []byte("hello"), 0o600); err != nil {
				t.Fatalf("WriteFile writable tree: %v", err)
			}
			if err := makeTreeUserWritable(writableRoot); err != nil {
				t.Fatalf("makeTreeUserWritable already-writable tree: %v", err)
			}

			store := setupTestStore(t)
			project, err := store.CreateProject("Command guards", "", "", "")
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			issue, err := store.CreateIssue(project.ID, "", "Command guards issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			command, err := store.CreateIssueAgentCommand(issue.ID, "Run the check.", IssueAgentCommandPending)
			if err != nil {
				t.Fatalf("CreateIssueAgentCommand: %v", err)
			}
			if err := store.reactivateIssueAgentCommandsForIssues([]string{"", "missing", issue.ID, issue.ID}); err != nil {
				t.Fatalf("reactivateIssueAgentCommandsForIssues: %v", err)
			}
			pending, err := store.ListPendingIssueAgentCommands(issue.ID)
			if err != nil {
				t.Fatalf("ListPendingIssueAgentCommands: %v", err)
			}
			if len(pending) != 1 || pending[0].ID != command.ID {
				t.Fatalf("expected pending issue command to remain untouched, got %#v", pending)
			}
		})
	})
}

func TestKanbanCoverageRemainingFailureBranches(t *testing.T) {
	newFaultyStoreAt := func(t *testing.T, dbPath, failPattern string) *Store {
		t.Helper()
		store := openFaultySQLiteStoreAt(t, dbPath, failPattern)
		if err := store.configureConnection(); err != nil {
			t.Fatalf("configureConnection: %v", err)
		}
		return store
	}

	t.Run("blocker validation", func(t *testing.T) {
		store := setupTestStore(t)
		issue, err := store.CreateIssue("", "", "Blocker validation issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}

		if _, err := store.SetIssueBlockers(issue.ID, []string{"missing-blocker"}); err == nil || !strings.Contains(err.Error(), "blocker issue not found") {
			t.Fatalf("expected missing blocker validation error, got %v", err)
		}
		if _, err := store.SetIssueBlockers(issue.ID, []string{issue.Identifier}); err == nil || !strings.Contains(err.Error(), "cannot block itself") {
			t.Fatalf("expected self-block validation error, got %v", err)
		}
	})

	t.Run("asset directory collision", func(t *testing.T) {
		store := setupTestStore(t)
		issue, err := store.CreateIssue("", "", "Asset mkdir issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}

		root := store.IssueAssetRoot()
		issueDir := filepath.Join(root, issue.ID)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("MkdirAll asset root: %v", err)
		}
		if err := os.WriteFile(issueDir, []byte("collision"), 0o644); err != nil {
			t.Fatalf("WriteFile issue dir collision: %v", err)
		}

		if _, err := store.CreateIssueAsset(issue.ID, "preview.png", bytes.NewReader(samplePNGBytes())); err == nil {
			t.Fatal("expected CreateIssueAsset to fail when the issue asset directory is a file")
		}
	})

	t.Run("query failures", func(t *testing.T) {
		t.Run("issue detail workspace lookup", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "detail.db")
			base := openSQLiteStoreAt(t, dbPath)
			issue, err := base.CreateIssue("", "", "Detail lookup issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := newFaultyStoreAt(t, dbPath, "from workspaces where issue_id")
			if _, err := store.GetIssueDetailByIdentifier(issue.Identifier); err == nil {
				t.Fatal("expected GetIssueDetailByIdentifier to fail when workspace lookup is injected")
			}
		})

		t.Run("issue detail blocker lookup", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "detail-blocked.db")
			base := openSQLiteStoreAt(t, dbPath)
			issue, err := base.CreateIssue("", "", "Detail blocked lookup issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := newFaultyStoreAt(t, dbPath, "where i.id = ?")
			if _, err := store.GetIssueDetailByIdentifier(issue.Identifier); err == nil {
				t.Fatal("expected GetIssueDetailByIdentifier to fail when blocker lookup is injected")
			}
		})

		t.Run("issue detail asset lookup", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "detail-assets.db")
			base := openSQLiteStoreAt(t, dbPath)
			issue, err := base.CreateIssue("", "", "Detail asset lookup issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			if _, err := base.CreateIssueAsset(issue.ID, "detail.png", bytes.NewReader(samplePNGBytes())); err != nil {
				t.Fatalf("CreateIssueAsset: %v", err)
			}
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := newFaultyStoreAt(t, dbPath, "from issue_assets")
			if _, err := store.GetIssueDetailByIdentifier(issue.Identifier); err == nil {
				t.Fatal("expected GetIssueDetailByIdentifier to fail when asset lookup is injected")
			}
		})

		t.Run("dispatch issue listing", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "dispatch.db")
			base := openSQLiteStoreAt(t, dbPath)
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := newFaultyStoreAt(t, dbPath, "case when p.id is null then 0 else 1 end")
			if _, err := store.ListDispatchIssues([]string{string(StateReady)}); err == nil {
				t.Fatal("expected ListDispatchIssues to fail when the dispatch query is injected")
			}
		})

		t.Run("agent command listing", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "agent-commands.db")
			base := openSQLiteStoreAt(t, dbPath)
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := newFaultyStoreAt(t, dbPath, "from issue_agent_commands")
			if _, err := store.ListIssueAgentCommands("issue-1"); err == nil {
				t.Fatal("expected ListIssueAgentCommands to fail when the command query is injected")
			}
		})

		t.Run("pending agent command listing", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "pending-agent-commands.db")
			base := openSQLiteStoreAt(t, dbPath)
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := newFaultyStoreAt(t, dbPath, "where issue_id = ? and status = ?")
			if _, err := store.ListPendingIssueAgentCommands("issue-1"); err == nil {
				t.Fatal("expected ListPendingIssueAgentCommands to fail when the pending command query is injected")
			}
		})

		t.Run("recurring issue listing", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "recurring.db")
			base := openSQLiteStoreAt(t, dbPath)
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := newFaultyStoreAt(t, dbPath, "inner join issue_recurrences r on r.issue_id = i.id")
			if _, err := store.ListDueRecurringIssues(time.Now().UTC(), "", 10); err == nil {
				t.Fatal("expected ListDueRecurringIssues to fail when the recurring query is injected")
			}
		})

		t.Run("git common dir lookup", func(t *testing.T) {
			if _, err := gitCommonDirForWorkspace(t.TempDir()); err == nil {
				t.Fatal("expected gitCommonDirForWorkspace to fail outside a git repository")
			}
		})

		t.Run("workspace tree removal fails with inaccessible parent", func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "parent")
			workspacePath := filepath.Join(parent, "workspace")
			if err := os.MkdirAll(filepath.Join(workspacePath, "nested"), 0o755); err != nil {
				t.Fatalf("MkdirAll workspace tree: %v", err)
			}
			if err := os.WriteFile(filepath.Join(workspacePath, "nested", "file.txt"), []byte("workspace"), 0o644); err != nil {
				t.Fatalf("WriteFile workspace tree: %v", err)
			}
			if err := os.Chmod(parent, 0o000); err != nil {
				t.Fatalf("Chmod parent: %v", err)
			}
			t.Cleanup(func() {
				_ = os.Chmod(parent, 0o755)
			})

			if err := removeWorkspaceTree(workspacePath); err == nil {
				t.Fatal("expected removeWorkspaceTree to fail when the parent directory is inaccessible")
			}
		})

		t.Run("workspace tree falls back after git worktree removal fails", func(t *testing.T) {
			repoPath := filepath.Join(t.TempDir(), "repo")
			if err := os.MkdirAll(repoPath, 0o755); err != nil {
				t.Fatalf("MkdirAll repo path: %v", err)
			}
			initGitRepoForStoreTest(t, repoPath)

			workspacePath := filepath.Join(repoPath, "workspace")
			if err := os.MkdirAll(workspacePath, 0o755); err != nil {
				t.Fatalf("MkdirAll workspace path: %v", err)
			}
			if err := os.WriteFile(filepath.Join(workspacePath, "note.txt"), []byte("workspace"), 0o644); err != nil {
				t.Fatalf("WriteFile workspace note: %v", err)
			}

			if err := removeWorkspaceTree(workspacePath); err != nil {
				t.Fatalf("removeWorkspaceTree: %v", err)
			}
			if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
				t.Fatalf("expected git fallback cleanup to remove workspace path, got %v", err)
			}
		})

		t.Run("make tree user writable no-op when already writable", func(t *testing.T) {
			treeRoot := filepath.Join(t.TempDir(), "tree")
			if err := os.MkdirAll(filepath.Join(treeRoot, "nested"), 0o700); err != nil {
				t.Fatalf("MkdirAll tree: %v", err)
			}
			if err := os.WriteFile(filepath.Join(treeRoot, "nested", "file.txt"), []byte("hello"), 0o600); err != nil {
				t.Fatalf("WriteFile tree file: %v", err)
			}
			if err := makeTreeUserWritable(treeRoot); err != nil {
				t.Fatalf("makeTreeUserWritable no-op: %v", err)
			}
		})

		t.Run("remove issue asset file ignores non-empty directories", func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "asset-dir")
			if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
				t.Fatalf("MkdirAll asset dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "nested", "file.txt"), []byte("asset"), 0o644); err != nil {
				t.Fatalf("WriteFile asset file: %v", err)
			}
			removeIssueAssetFile(dir)
			if _, err := os.Stat(dir); err != nil {
				t.Fatalf("expected non-empty directory to remain after removeIssueAssetFile, got %v", err)
			}
		})

		t.Run("remove issue pr number column migration", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "migration.db")
			store := openSQLiteStoreAt(t, dbPath)
			projectA, err := store.CreateProject("PR number project A", "", "", "")
			if err != nil {
				t.Fatalf("CreateProject A: %v", err)
			}
			projectB, err := store.CreateProject("PR number project B", "", "", "")
			if err != nil {
				t.Fatalf("CreateProject B: %v", err)
			}
			epicB, err := store.CreateEpic(projectB.ID, "PR number epic", "")
			if err != nil {
				t.Fatalf("CreateEpic: %v", err)
			}
			projectIssue, err := store.CreateIssue(projectA.ID, "", "Project issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue project issue: %v", err)
			}
			epicIssue, err := store.CreateIssue(projectB.ID, epicB.ID, "Epic issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue epic issue: %v", err)
			}

			if _, err := store.db.Exec(`ALTER TABLE issues ADD COLUMN pr_number TEXT NOT NULL DEFAULT ''`); err != nil {
				t.Fatalf("ALTER TABLE issues ADD COLUMN pr_number: %v", err)
			}
			if _, err := store.db.Exec(`DELETE FROM store_metadata WHERE key = 'issue_pr_number_drop_v1'`); err != nil {
				t.Fatalf("DELETE store_metadata migration marker: %v", err)
			}
			if _, err := store.db.Exec(`UPDATE issues SET pr_number = ? WHERE id = ?`, "PR-1", projectIssue.ID); err != nil {
				t.Fatalf("update project issue pr_number: %v", err)
			}
			if _, err := store.db.Exec(`UPDATE issues SET pr_number = ? WHERE id = ?`, "PR-2", epicIssue.ID); err != nil {
				t.Fatalf("update epic issue pr_number: %v", err)
			}
			hasColumnBefore, err := store.tableHasColumn("issues", "pr_number")
			if err != nil {
				t.Fatalf("tableHasColumn pr_number before removal: %v", err)
			}
			if !hasColumnBefore {
				t.Fatal("expected pr_number column to exist before removal")
			}

			if err := store.removeIssuePRNumberColumn(); err != nil {
				t.Fatalf("removeIssuePRNumberColumn: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("Close store after PR-number migration: %v", err)
			}
			reloadedStore := openSQLiteStoreAt(t, store.dbPath)
			hasColumn, err := reloadedStore.tableHasColumn("issues", "pr_number")
			if err != nil {
				t.Fatalf("tableHasColumn pr_number: %v", err)
			}
			if hasColumn {
				t.Fatal("expected pr_number column to be removed")
			}
			if err := reloadedStore.removeIssuePRNumberColumn(); err != nil {
				t.Fatalf("removeIssuePRNumberColumn idempotent: %v", err)
			}

			reloadedProjectIssue, err := reloadedStore.GetIssue(projectIssue.ID)
			if err != nil {
				t.Fatalf("GetIssue project issue: %v", err)
			}
			if reloadedProjectIssue.ProjectID != projectA.ID || reloadedProjectIssue.EpicID != "" {
				t.Fatalf("unexpected migrated project issue: %#v", reloadedProjectIssue)
			}
			reloadedEpicIssue, err := reloadedStore.GetIssue(epicIssue.ID)
			if err != nil {
				t.Fatalf("GetIssue epic issue: %v", err)
			}
			if reloadedEpicIssue.ProjectID != projectB.ID || reloadedEpicIssue.EpicID != epicB.ID {
				t.Fatalf("unexpected migrated epic issue: %#v", reloadedEpicIssue)
			}
		})
	})
}
