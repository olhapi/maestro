package kanban

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	moderncsqlite "modernc.org/sqlite"
)

func TestStoreMigrationHelpersCoverage(t *testing.T) {
	t.Run("backfill open issue plan sessions", func(t *testing.T) {
		store := setupTestStore(t)
		issue, err := store.CreateIssue("", "", "Legacy planning backfill", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}

		requestedAt := time.Date(2026, 3, 18, 8, 45, 0, 0, time.UTC)
		revisionRequestedAt := requestedAt.Add(10 * time.Minute)
		if _, err := store.db.Exec(`
			UPDATE issues
			SET plan_approval_pending = 1,
			    pending_plan_markdown = ?,
			    pending_plan_requested_at = ?,
			    pending_plan_revision_markdown = ?,
			    pending_plan_revision_requested_at = ?,
			    updated_at = ?
			WHERE id = ?`,
			"Legacy plan body",
			requestedAt,
			"Need a tighter rollback path.",
			revisionRequestedAt,
			revisionRequestedAt,
			issue.ID,
		); err != nil {
			t.Fatalf("UPDATE issues legacy planning state: %v", err)
		}
		if _, err := store.db.Exec(`DELETE FROM store_metadata WHERE key = ?`, "issue_plan_sessions_open_backfill_v1"); err != nil {
			t.Fatalf("DELETE store_metadata backfill marker: %v", err)
		}

		if err := store.backfillOpenIssuePlanSessions(); err != nil {
			t.Fatalf("backfillOpenIssuePlanSessions: %v", err)
		}
		if err := store.backfillOpenIssuePlanSessions(); err != nil {
			t.Fatalf("backfillOpenIssuePlanSessions idempotent call: %v", err)
		}

		planning, err := store.GetIssuePlanning(issue)
		if err != nil {
			t.Fatalf("GetIssuePlanning(backfilled): %v", err)
		}
		if planning == nil || planning.Status != IssuePlanningStatusRevisionRequested {
			t.Fatalf("expected revision requested planning state after backfill, got %+v", planning)
		}
		if planning.CurrentVersionNumber != 1 || planning.CurrentVersion == nil {
			t.Fatalf("expected backfilled current version metadata, got %+v", planning)
		}
		if planning.CurrentVersion.Markdown != "Legacy plan body" {
			t.Fatalf("unexpected backfilled markdown: %+v", planning.CurrentVersion)
		}
		if planning.PendingRevisionNote != "Need a tighter rollback path." {
			t.Fatalf("unexpected backfilled revision note: %+v", planning)
		}
		if !planning.UpdatedAt.Equal(revisionRequestedAt.UTC()) {
			t.Fatalf("unexpected backfilled revision update time: %+v", planning.UpdatedAt)
		}
	})

	t.Run("remove issue pr number column no-op", func(t *testing.T) {
		store := setupTestStore(t)
		if _, err := store.db.Exec(`DELETE FROM store_metadata WHERE key = ?`, "issue_pr_number_drop_v1"); err != nil {
			t.Fatalf("DELETE store_metadata PR-number marker: %v", err)
		}
		if err := store.removeIssuePRNumberColumn(); err != nil {
			t.Fatalf("removeIssuePRNumberColumn no-op: %v", err)
		}
		if err := store.removeIssuePRNumberColumn(); err != nil {
			t.Fatalf("removeIssuePRNumberColumn idempotent no-op: %v", err)
		}
		hasColumn, err := store.tableHasColumn("issues", "pr_number")
		if err != nil {
			t.Fatalf("tableHasColumn pr_number after no-op: %v", err)
		}
		if hasColumn {
			t.Fatal("expected pr_number column to remain absent")
		}
	})

	t.Run("issue type and workflow backfills", func(t *testing.T) {
		store := setupTestStore(t)
		doneIssue, err := store.CreateIssue("", "", "Done issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue done: %v", err)
		}
		backlogIssue, err := store.CreateIssue("", "", "Backlog issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue backlog: %v", err)
		}

		if _, err := store.db.Exec(`
			UPDATE issues
			SET issue_type = '',
			    workflow_phase = '',
			    state = CASE id WHEN ? THEN 'done' ELSE 'backlog' END
			WHERE id IN (?, ?)`,
			doneIssue.ID, doneIssue.ID, backlogIssue.ID,
		); err != nil {
			t.Fatalf("UPDATE issues legacy workflow state: %v", err)
		}
		if _, err := store.db.Exec(`DELETE FROM store_metadata WHERE key IN ('issue_type_backfill_v1', 'workflow_phase_backfill_v1')`); err != nil {
			t.Fatalf("DELETE backfill markers: %v", err)
		}

		if err := store.backfillIssueTypes(); err != nil {
			t.Fatalf("backfillIssueTypes: %v", err)
		}
		if err := store.backfillWorkflowPhases(); err != nil {
			t.Fatalf("backfillWorkflowPhases: %v", err)
		}
		if err := store.backfillIssueTypes(); err != nil {
			t.Fatalf("backfillIssueTypes idempotent call: %v", err)
		}
		if err := store.backfillWorkflowPhases(); err != nil {
			t.Fatalf("backfillWorkflowPhases idempotent call: %v", err)
		}

		doneLoaded, err := store.GetIssue(doneIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue done: %v", err)
		}
		if doneLoaded.IssueType != IssueTypeStandard {
			t.Fatalf("expected standard issue type after backfill, got %s", doneLoaded.IssueType)
		}
		if doneLoaded.WorkflowPhase != WorkflowPhaseComplete {
			t.Fatalf("expected complete workflow phase for done issue, got %s", doneLoaded.WorkflowPhase)
		}

		backlogLoaded, err := store.GetIssue(backlogIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue backlog: %v", err)
		}
		if backlogLoaded.IssueType != IssueTypeStandard {
			t.Fatalf("expected standard issue type after backfill, got %s", backlogLoaded.IssueType)
		}
		if backlogLoaded.WorkflowPhase != WorkflowPhaseImplementation {
			t.Fatalf("expected implementation workflow phase for backlog issue, got %s", backlogLoaded.WorkflowPhase)
		}
	})

	t.Run("plan session row helpers", func(t *testing.T) {
		store := setupTestStore(t)
		issue, err := store.CreateIssue("", "", "Plan helper issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}

		if record, err := store.getLatestIssuePlanSessionTx(store.db, "", true); err != nil || record != nil {
			t.Fatalf("expected blank issue id to be ignored, got record=%#v err=%v", record, err)
		}
		if record, err := store.getLatestIssuePlanSessionTx(store.db, issue.ID, true); err != nil || record != nil {
			t.Fatalf("expected missing plan sessions to return nil, got record=%#v err=%v", record, err)
		}

		session := issuePlanSessionRecord{
			ID:                         generateID("pls"),
			IssueID:                    issue.ID,
			Status:                     IssuePlanningStatusAwaitingApproval,
			OriginAttempt:              2,
			OriginThreadID:             "thread-1",
			CurrentVersionNumber:       3,
			PendingRevisionNote:        "revise the rollout",
			PendingRevisionRequestedAt: func() *time.Time { v := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC); return &v }(),
			OpenedAt:                   time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
			UpdatedAt:                  time.Date(2026, 3, 9, 12, 15, 0, 0, time.UTC),
			ClosedAt:                   func() *time.Time { v := time.Date(2026, 3, 9, 12, 30, 0, 0, time.UTC); return &v }(),
			ClosedReason:               "complete",
		}
		tx, err := store.db.Begin()
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if err := store.insertIssuePlanSessionTx(tx, session); err != nil {
			t.Fatalf("insertIssuePlanSessionTx: %v", err)
		}
		if err := store.insertIssuePlanVersionTx(tx, IssuePlanVersion{
			ID:            generateID("plv"),
			SessionID:     session.ID,
			VersionNumber: 1,
			Markdown:      "plan body",
			CreatedAt:     time.Time{},
		}); err != nil {
			t.Fatalf("insertIssuePlanVersionTx: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit plan session tx: %v", err)
		}

		record, err := store.getLatestIssuePlanSessionTx(store.db, issue.ID, true)
		if err != nil {
			t.Fatalf("getLatestIssuePlanSessionTx: %v", err)
		}
		if record == nil || record.ID != session.ID || record.ClosedAt == nil {
			t.Fatalf("expected inserted plan session to be returned, got %#v", record)
		}

		scanned, err := scanIssuePlanSessionRow(store.db.QueryRow(`
			SELECT id, issue_id, status, origin_attempt, origin_thread_id, current_version_number,
			       pending_revision_note, pending_revision_requested_at, opened_at, updated_at,
			       closed_at, closed_reason
			FROM issue_plan_sessions
			WHERE id = ?`,
			session.ID,
		))
		if err != nil {
			t.Fatalf("scanIssuePlanSessionRow: %v", err)
		}
		if scanned == nil || scanned.ID != session.ID || scanned.PendingRevisionRequestedAt == nil || scanned.ClosedAt == nil {
			t.Fatalf("unexpected scanned session: %#v", scanned)
		}
		if scanned.PendingRevisionRequestedAt.UTC() != session.PendingRevisionRequestedAt.UTC() || scanned.ClosedAt.UTC() != session.ClosedAt.UTC() {
			t.Fatalf("expected scanned timestamps to round-trip, got %#v", scanned)
		}

		var versionCount int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM issue_plan_versions WHERE session_id = ?`, session.ID).Scan(&versionCount); err != nil {
			t.Fatalf("query issue_plan_versions count: %v", err)
		}
		if versionCount != 1 {
			t.Fatalf("expected one plan version, got %d", versionCount)
		}
	})

	t.Run("issue asset tables", func(t *testing.T) {
		store := setupTestStore(t)
		if err := store.ensureIssueAssetTables(); err != nil {
			t.Fatalf("ensureIssueAssetTables: %v", err)
		}

		for _, query := range []struct {
			name string
			sql  string
			want int
		}{
			{name: "table", sql: `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'issue_assets'`, want: 1},
			{name: "index", sql: `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_issue_assets_issue_created'`, want: 1},
		} {
			var got int
			if err := store.db.QueryRow(query.sql).Scan(&got); err != nil {
				t.Fatalf("query %s: %v", query.name, err)
			}
			if got != query.want {
				t.Fatalf("expected %s count %d, got %d", query.name, query.want, got)
			}
		}
	})

	t.Run("schema and runtime helpers", func(t *testing.T) {
		store := setupTestStore(t)
		if got := stringFromNull(sql.NullString{}); got != "" {
			t.Fatalf("expected invalid null string to collapse to empty, got %q", got)
		}
		if got := stringFromNull(sql.NullString{Valid: true, String: "ready"}); got != "ready" {
			t.Fatalf("expected valid null string to return value, got %q", got)
		}

		for _, fn := range []struct {
			name string
			call func() error
		}{
			{name: "issue columns", call: store.ensureIssueColumns},
			{name: "issue comment tables", call: store.ensureIssueCommentTables},
			{name: "recurrence", call: store.ensureIssueRecurrenceTables},
			{name: "execution session", call: store.ensureIssueExecutionSessionColumns},
			{name: "agent command", call: store.ensureIssueAgentCommandColumns},
			{name: "planning", call: store.ensureIssuePlanningTables},
		} {
			if err := fn.call(); err != nil {
				t.Fatalf("ensure %s tables/columns: %v", fn.name, err)
			}
		}

		issue, err := store.CreateIssue("", "", "Runtime series issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		future := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Minute)
		if err := store.AppendRuntimeEvent("run_completed", map[string]interface{}{
			"issue_id":     issue.ID,
			"identifier":   issue.Identifier,
			"attempt":      1,
			"total_tokens": 7,
			"ts":           future.Format(time.RFC3339),
			"thread_id":    "thread-runtime",
			"turn_id":      "turn-runtime",
			"payload_json": `{"thread_id":"thread-runtime"}`,
		}); err != nil {
			t.Fatalf("AppendRuntimeEvent future: %v", err)
		}
		series, err := store.RuntimeSeries(1)
		if err != nil {
			t.Fatalf("RuntimeSeries: %v", err)
		}
		if len(series) != 1 {
			t.Fatalf("expected one runtime series bucket, got %d", len(series))
		}
		if series[0].Tokens != 0 || series[0].RunsCompleted != 0 || series[0].RunsStarted != 0 {
			t.Fatalf("expected out-of-range runtime event to be ignored, got %#v", series[0])
		}
	})

	t.Run("schema helper failure branches", func(t *testing.T) {
		t.Run("issue columns", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "issue-columns.db")
			base := openSQLiteStoreAt(t, dbPath)
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := openFaultySQLiteStoreAt(t, dbPath, "alter table issues add column workflow_phase")
			if err := store.configureConnection(); err != nil {
				t.Fatalf("configureConnection faulty store: %v", err)
			}
			if err := store.ensureIssueColumns(); err == nil {
				t.Fatal("expected ensureIssueColumns to fail when an ALTER TABLE statement is injected")
			}
		})

		t.Run("issue comment tables", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "issue-comments.db")
			base := openSQLiteStoreAt(t, dbPath)
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := openFaultySQLiteStoreAt(t, dbPath, "alter table issue_comments add column parent_comment_id")
			if err := store.configureConnection(); err != nil {
				t.Fatalf("configureConnection faulty store: %v", err)
			}
			if err := store.ensureIssueCommentTables(); err == nil {
				t.Fatal("expected ensureIssueCommentTables to fail when a comment-table ALTER TABLE statement is injected")
			}
		})

		t.Run("open issue plan sessions metadata", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "plan-backfill.db")
			base := openSQLiteStoreAt(t, dbPath)
			issue, err := base.CreateIssue("", "", "Legacy planning failure", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			requestedAt := time.Date(2026, 3, 18, 8, 45, 0, 0, time.UTC)
			revisionRequestedAt := requestedAt.Add(10 * time.Minute)
			if _, err := base.db.Exec(`
				UPDATE issues
				SET plan_approval_pending = 1,
				    pending_plan_markdown = ?,
				    pending_plan_requested_at = ?,
				    pending_plan_revision_markdown = ?,
				    pending_plan_revision_requested_at = ?,
				    updated_at = ?
				WHERE id = ?`,
				"Legacy plan body",
				requestedAt,
				"Need a tighter rollback path.",
				revisionRequestedAt,
				revisionRequestedAt,
				issue.ID,
			); err != nil {
				t.Fatalf("UPDATE issues legacy planning state: %v", err)
			}
			if _, err := base.db.Exec(`DELETE FROM store_metadata WHERE key = ?`, "issue_plan_sessions_open_backfill_v1"); err != nil {
				t.Fatalf("DELETE store_metadata backfill marker: %v", err)
			}
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}

			store := openFaultySQLiteStoreAt(t, dbPath, "insert into issue_plan_sessions")
			if err := store.configureConnection(); err != nil {
				t.Fatalf("configureConnection faulty store: %v", err)
			}
			if err := store.backfillOpenIssuePlanSessions(); err == nil {
				t.Fatal("expected backfillOpenIssuePlanSessions to fail when plan-session persistence is injected")
			}
			planning, err := store.GetIssuePlanning(issue)
			if err != nil {
				t.Fatalf("GetIssuePlanning after failed backfill: %v", err)
			}
			if planning != nil {
				t.Fatalf("expected failed backfill to roll back plan rows, got %#v", planning)
			}
		})
	})

	t.Run("foreign key normalization backfills", func(t *testing.T) {
		store := setupTestStore(t)

		repoA := t.TempDir()
		projectA, err := store.CreateProject("Project A", "", repoA, "")
		if err != nil {
			t.Fatalf("CreateProject A: %v", err)
		}

		repoB := t.TempDir()
		projectB, err := store.CreateProject("Project B", "", repoB, "")
		if err != nil {
			t.Fatalf("CreateProject B: %v", err)
		}

		epic, err := store.CreateEpic(projectA.ID, "Backfill epic", "")
		if err != nil {
			t.Fatalf("CreateEpic: %v", err)
		}
		trimIssue, err := store.CreateIssue(projectA.ID, "", "Trimmed foreign keys", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue trim: %v", err)
		}
		fallbackIssue, err := store.CreateIssue("", "", "Epic fallback", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue fallback: %v", err)
		}
		nullableIssue, err := store.CreateIssue(projectB.ID, "", "Nullable foreign keys", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue nullable: %v", err)
		}

		if _, err := store.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
			t.Fatalf("disable foreign keys: %v", err)
		}
		t.Cleanup(func() {
			_, _ = store.db.Exec(`PRAGMA foreign_keys = ON`)
		})
		if _, err := store.db.Exec(`DELETE FROM store_metadata WHERE key = ?`, "issue_foreign_key_normalization_v1"); err != nil {
			t.Fatalf("delete foreign key normalization marker: %v", err)
		}

		if _, err := store.db.Exec(`UPDATE issues SET project_id = ?, epic_id = ? WHERE id = ?`, "  "+projectA.ID+"  ", "  "+epic.ID+"  ", trimIssue.ID); err != nil {
			t.Fatalf("UPDATE trim issue: %v", err)
		}
		if _, err := store.db.Exec(`UPDATE issues SET project_id = ?, epic_id = ? WHERE id = ?`, "missing-project", epic.ID, fallbackIssue.ID); err != nil {
			t.Fatalf("UPDATE fallback issue: %v", err)
		}
		if _, err := store.db.Exec(`UPDATE issues SET project_id = ?, epic_id = ? WHERE id = ?`, "missing-project", "missing-epic", nullableIssue.ID); err != nil {
			t.Fatalf("UPDATE nullable issue: %v", err)
		}

		if err := store.normalizeIssueForeignKeys(); err != nil {
			t.Fatalf("normalizeIssueForeignKeys: %v", err)
		}
		if err := store.normalizeIssueForeignKeys(); err != nil {
			t.Fatalf("normalizeIssueForeignKeys idempotent call: %v", err)
		}

		trimmed, err := store.GetIssue(trimIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue trim: %v", err)
		}
		if trimmed.ProjectID != projectA.ID || trimmed.EpicID != epic.ID {
			t.Fatalf("expected trimmed issue foreign keys to normalize, got %#v", trimmed)
		}
		fallback, err := store.GetIssue(fallbackIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue fallback: %v", err)
		}
		if fallback.ProjectID != projectA.ID || fallback.EpicID != epic.ID {
			t.Fatalf("expected epic-derived project foreign key to normalize, got %#v", fallback)
		}
		nullable, err := store.GetIssue(nullableIssue.ID)
		if err != nil {
			t.Fatalf("GetIssue nullable: %v", err)
		}
		if nullable.ProjectID != "" || nullable.EpicID != "" {
			t.Fatalf("expected invalid foreign keys to be cleared, got %#v", nullable)
		}
	})

	t.Run("schema helpers surface db errors", func(t *testing.T) {
		store := setupTestStore(t)
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		cases := []struct {
			name string
			call func() error
		}{
			{name: "recurrence", call: store.ensureIssueRecurrenceTables},
			{name: "execution session", call: store.ensureIssueExecutionSessionColumns},
			{name: "agent command", call: store.ensureIssueAgentCommandColumns},
			{name: "planning", call: store.ensureIssuePlanningTables},
			{name: "asset", call: store.ensureIssueAssetTables},
			{name: "issue types", call: store.backfillIssueTypes},
			{name: "workflow phases", call: store.backfillWorkflowPhases},
			{name: "plan sessions", call: store.backfillOpenIssuePlanSessions},
			{name: "foreign key normalization", call: store.normalizeIssueForeignKeys},
		}
		for _, tc := range cases {
			if err := tc.call(); err == nil {
				t.Fatalf("expected %s helper to fail on closed db", tc.name)
			}
		}
		if record, err := store.getLatestIssuePlanSessionTx(store.db, "issue", true); err == nil || record != nil {
			t.Fatalf("expected getLatestIssuePlanSessionTx to fail on closed db, got record=%#v err=%v", record, err)
		}
		if _, err := store.RuntimeSeries(1); err == nil {
			t.Fatal("expected RuntimeSeries to fail on closed db")
		}
	})
}

func TestStoreMigrationLegacySchemaBranches(t *testing.T) {
	t.Run("migrate adds missing columns and normalizes foreign keys", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "legacy.db")
		db, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			t.Fatalf("sql.Open: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		stmts := []string{
			`CREATE TABLE store_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
			`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT)`,
			`CREATE TABLE epics (id TEXT PRIMARY KEY, project_id TEXT, name TEXT NOT NULL, description TEXT)`,
			`CREATE TABLE issues (
				id TEXT PRIMARY KEY,
				project_id TEXT,
				epic_id TEXT,
				identifier TEXT UNIQUE NOT NULL,
				issue_type TEXT,
				provider_kind TEXT,
				provider_issue_ref TEXT,
				provider_shadow INTEGER,
				title TEXT NOT NULL,
				description TEXT,
				state TEXT NOT NULL DEFAULT 'backlog',
				workflow_phase TEXT,
				permission_profile TEXT,
				collaboration_mode_override TEXT,
				plan_approval_pending INTEGER,
				pending_plan_markdown TEXT,
				pending_plan_requested_at DATETIME,
				pending_plan_revision_markdown TEXT,
				pending_plan_revision_requested_at DATETIME,
				priority INTEGER,
				agent_name TEXT,
				agent_prompt TEXT,
				branch_name TEXT,
				pr_url TEXT,
				created_at DATETIME NOT NULL,
				updated_at DATETIME NOT NULL,
				total_tokens_spent INTEGER,
				started_at DATETIME,
				completed_at DATETIME,
				last_synced_at DATETIME
			)`,
		}
		for _, stmt := range stmts {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("exec %q: %v", stmt, err)
			}
		}

		projectID := "proj-legacy"
		epicID := "epic-legacy"
		now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
		if _, err := db.Exec(`INSERT INTO projects (id, name, description) VALUES (?, ?, ?)`, projectID, "Legacy Project", ""); err != nil {
			t.Fatalf("insert project: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO epics (id, project_id, name, description) VALUES (?, ?, ?, ?)`, epicID, projectID, "Legacy Epic", ""); err != nil {
			t.Fatalf("insert epic: %v", err)
		}
		for _, row := range []struct {
			id         string
			projectID  string
			epicID     string
			identifier string
			state      string
		}{
			{id: "issue-direct", projectID: projectID, epicID: "", identifier: "ISS-DIRECT", state: "done"},
			{id: "issue-through-epic", projectID: "", epicID: epicID, identifier: "ISS-EPIC", state: "backlog"},
		} {
			if _, err := db.Exec(`INSERT INTO issues (
				id, project_id, epic_id, identifier, issue_type, provider_kind, provider_issue_ref, provider_shadow,
				title, description, state, workflow_phase, permission_profile, collaboration_mode_override,
				plan_approval_pending, pending_plan_markdown, pending_plan_requested_at, pending_plan_revision_markdown,
				pending_plan_revision_requested_at, priority, agent_name, agent_prompt, branch_name, pr_url,
				created_at, updated_at, total_tokens_spent, started_at, completed_at, last_synced_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				row.id, row.projectID, row.epicID, row.identifier, "", ProviderKindKanban, "", 0,
				row.identifier, "", row.state, "", "", "",
				0, "", nil, "", nil, 0, "", "", nil, nil,
				now, now, 0, nil, nil, nil,
			); err != nil {
				t.Fatalf("insert issue %s: %v", row.id, err)
			}
		}

		store := &Store{db: db}
		if err := store.migrate(); err != nil {
			t.Fatalf("migrate legacy schema: %v", err)
		}
		if err := store.ensureStoreID(); err != nil {
			t.Fatalf("ensureStoreID: %v", err)
		}

		for _, column := range []string{"issue_type", "provider_kind", "workflow_phase", "agent_name", "pending_plan_markdown"} {
			has, err := store.tableHasColumn("issues", column)
			if err != nil {
				t.Fatalf("tableHasColumn(%s): %v", column, err)
			}
			if !has {
				t.Fatalf("expected issues table to gain %s", column)
			}
		}

		direct, err := store.GetIssue("issue-direct")
		if err != nil {
			t.Fatalf("GetIssue direct: %v", err)
		}
		if direct.ProjectID != projectID || direct.EpicID != "" {
			t.Fatalf("expected direct foreign key to survive migration, got %#v", direct)
		}
		if direct.IssueType != IssueTypeStandard || direct.WorkflowPhase != WorkflowPhaseComplete {
			t.Fatalf("expected direct issue backfills to run, got %#v", direct)
		}

		throughEpic, err := store.GetIssue("issue-through-epic")
		if err != nil {
			t.Fatalf("GetIssue epic: %v", err)
		}
		if throughEpic.ProjectID != projectID || throughEpic.EpicID != epicID {
			t.Fatalf("expected epic-derived foreign keys to survive migration, got %#v", throughEpic)
		}
		if throughEpic.IssueType != IssueTypeStandard || throughEpic.WorkflowPhase != WorkflowPhaseImplementation {
			t.Fatalf("expected backlog issue backfills to run, got %#v", throughEpic)
		}
	})

	t.Run("remove pr_number rebuilds issues table", func(t *testing.T) {
		store := setupTestStore(t)
		project, err := store.CreateProject("Legacy Project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		issue, err := store.CreateIssue(project.ID, "", "Legacy PR issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if _, err := store.db.Exec(`ALTER TABLE issues ADD COLUMN pr_number INTEGER`); err != nil {
			t.Fatalf("ALTER TABLE pr_number: %v", err)
		}
		if _, err := store.db.Exec(`UPDATE issues SET pr_number = 42 WHERE id = ?`, issue.ID); err != nil {
			t.Fatalf("update pr_number: %v", err)
		}
		if _, err := store.db.Exec(`DELETE FROM store_metadata WHERE key = ?`, "issue_pr_number_drop_v1"); err != nil {
			t.Fatalf("delete migration marker: %v", err)
		}

		if err := store.removeIssuePRNumberColumn(); err != nil {
			t.Fatalf("removeIssuePRNumberColumn: %v", err)
		}
		has, err := store.tableHasColumn("issues", "pr_number")
		if err != nil {
			t.Fatalf("tableHasColumn(pr_number): %v", err)
		}
		if has {
			t.Fatal("expected pr_number column to be dropped")
		}
		loaded, err := store.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue after rebuild: %v", err)
		}
		if loaded.ProjectID != project.ID || loaded.Identifier != issue.Identifier {
			t.Fatalf("expected rebuilt issue row to survive migration, got %#v", loaded)
		}
	})

	t.Run("remove pr_number failure branches", func(t *testing.T) {
		prepareLegacyPrNumberStore := func(t *testing.T, dbPath string) {
			t.Helper()
			base := openSQLiteStoreAt(t, dbPath)
			if _, err := base.db.Exec(`ALTER TABLE issues ADD COLUMN pr_number INTEGER`); err != nil {
				t.Fatalf("ALTER TABLE pr_number: %v", err)
			}
			if _, err := base.db.Exec(`DELETE FROM store_metadata WHERE key = ?`, "issue_pr_number_drop_v1"); err != nil {
				t.Fatalf("delete migration marker: %v", err)
			}
			if err := base.Close(); err != nil {
				t.Fatalf("Close base store: %v", err)
			}
		}

		t.Run("metadata lookup", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "pr-number-metadata.db")
			prepareLegacyPrNumberStore(t, dbPath)

			store := openFaultySQLiteStoreAt(t, dbPath, "select value from store_metadata where key = ?")
			if err := store.configureConnection(); err != nil {
				t.Fatalf("configureConnection faulty store: %v", err)
			}
			if err := store.removeIssuePRNumberColumn(); err == nil {
				t.Fatal("expected removeIssuePRNumberColumn to fail on metadata lookup")
			}
		})

		t.Run("table info lookup", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "pr-number-table-info.db")
			prepareLegacyPrNumberStore(t, dbPath)

			store := openFaultySQLiteStoreAt(t, dbPath, "pragma table_info(issues)")
			if err := store.configureConnection(); err != nil {
				t.Fatalf("configureConnection faulty store: %v", err)
			}
			if err := store.removeIssuePRNumberColumn(); err == nil {
				t.Fatal("expected removeIssuePRNumberColumn to fail on table_info lookup")
			}
		})

		t.Run("foreign keys off", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "pr-number-foreign-keys.db")
			prepareLegacyPrNumberStore(t, dbPath)

			store := openFaultySQLiteStoreAt(t, dbPath, "pragma foreign_keys=off")
			if err := store.configureConnection(); err != nil {
				t.Fatalf("configureConnection faulty store: %v", err)
			}
			if err := store.removeIssuePRNumberColumn(); err == nil {
				t.Fatal("expected removeIssuePRNumberColumn to fail when disabling foreign keys")
			}
		})

		t.Run("foreign keys on", func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "pr-number-foreign-keys-on.db")
			prepareLegacyPrNumberStore(t, dbPath)

			store := openFaultySQLiteStoreAt(t, dbPath, "pragma foreign_keys=on")
			if err := store.removeIssuePRNumberColumn(); err == nil {
				t.Fatal("expected removeIssuePRNumberColumn to fail when re-enabling foreign keys")
			}
		})

		t.Run("foreign key verification", func(t *testing.T) {
			store := setupTestStore(t)
			issue, err := store.CreateIssue("", "", "Broken foreign keys issue", "", 0, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			if _, err := store.CreateIssueAsset(issue.ID, "broken.txt", bytes.NewReader([]byte("broken"))); err != nil {
				t.Fatalf("CreateIssueAsset: %v", err)
			}
			if _, err := store.db.Exec(`ALTER TABLE issues ADD COLUMN pr_number INTEGER`); err != nil {
				t.Fatalf("ALTER TABLE pr_number: %v", err)
			}
			if _, err := store.db.Exec(`DELETE FROM store_metadata WHERE key = ?`, "issue_pr_number_drop_v1"); err != nil {
				t.Fatalf("delete migration marker: %v", err)
			}
			if _, err := store.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
				t.Fatalf("disable foreign keys: %v", err)
			}
			if _, err := store.db.Exec(`DELETE FROM issues WHERE id = ?`, issue.ID); err != nil {
				t.Fatalf("delete issue: %v", err)
			}
			if _, err := store.db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
				t.Fatalf("re-enable foreign keys: %v", err)
			}

			if err := store.removeIssuePRNumberColumn(); err == nil {
				t.Fatal("expected removeIssuePRNumberColumn to fail on foreign key verification")
			}
		})
	})

	t.Run("normalize foreign keys begin failure", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "normalize-begin.db")
		base := openSQLiteStoreAt(t, dbPath)
		if _, err := base.db.Exec(`DELETE FROM store_metadata WHERE key = ?`, "issue_foreign_key_normalization_v1"); err != nil {
			t.Fatalf("delete normalization marker: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		store := openFaultySQLiteStoreAt(t, dbPath, "__begin__")
		if err := store.configureConnection(); err != nil {
			t.Fatalf("configureConnection faulty store: %v", err)
		}
		if err := store.normalizeIssueForeignKeys(); err == nil {
			t.Fatal("expected normalizeIssueForeignKeys to fail when beginning the normalization transaction")
		}
	})

	t.Run("normalize foreign keys rollback failure", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "normalize-rollback.db")
		base := openSQLiteStoreAt(t, dbPath)
		if _, err := base.db.Exec(`DELETE FROM store_metadata WHERE key = ?`, "issue_foreign_key_normalization_v1"); err != nil {
			t.Fatalf("delete normalization marker: %v", err)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("Close base store: %v", err)
		}

		store := openFaultySQLiteStoreAt(t, dbPath, "update issues")
		if err := store.configureConnection(); err != nil {
			t.Fatalf("configureConnection faulty store: %v", err)
		}
		if err := store.normalizeIssueForeignKeys(); err == nil {
			t.Fatal("expected normalizeIssueForeignKeys to fail when the first update statement is injected")
		}
	})

	t.Run("migrate is idempotent on existing schema", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "reopen.db")
		store, err := NewStore(dbPath)
		if err != nil {
			t.Fatalf("NewStore initial: %v", err)
		}
		project, err := store.CreateProject("Reopened Project", "", "", "")
		if err != nil {
			t.Fatalf("CreateProject initial: %v", err)
		}
		issue, err := store.CreateIssue(project.ID, "", "Reopened issue", "", 0, nil)
		if err != nil {
			t.Fatalf("CreateIssue initial: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close initial store: %v", err)
		}

		reopened, err := NewStore(dbPath)
		if err != nil {
			t.Fatalf("NewStore reopened: %v", err)
		}
		t.Cleanup(func() {
			_ = reopened.Close()
		})

		loaded, err := reopened.GetIssue(issue.ID)
		if err != nil {
			t.Fatalf("GetIssue reopened: %v", err)
		}
		if loaded.ProjectID != project.ID || loaded.Identifier != issue.Identifier {
			t.Fatalf("expected reopened store to preserve data, got %#v", loaded)
		}
	})
}

func TestStoreMigrationInjectedFailureBranches(t *testing.T) {
	cases := []struct {
		name        string
		failPattern string
	}{
		{name: "migration loop", failPattern: "create index if not exists idx_change_events_ts"},
		{name: "project columns", failPattern: "alter table projects add column state"},
		{name: "issue columns", failPattern: "alter table issues add column workflow_phase"},
		{name: "issue recurrence tables", failPattern: "create table if not exists issue_recurrences"},
		{name: "execution session columns", failPattern: "alter table issue_execution_sessions add column resume_eligible"},
		{name: "issue asset tables", failPattern: "create table if not exists issue_assets"},
		{name: "issue comment tables", failPattern: "create table if not exists issue_comments"},
		{name: "agent command columns", failPattern: "alter table issue_agent_commands add column steered_at"},
		{name: "planning tables", failPattern: "create table if not exists issue_plan_sessions"},
		{name: "issue type backfill", failPattern: "update issues\n\t\tset issue_type = 'standard'"},
		{name: "workflow phase backfill", failPattern: "update issues\n\t\tset workflow_phase = case"},
		{name: "foreign key normalization", failPattern: "pragma foreign_key_check"},
		{name: "store id", failPattern: "insert into store_metadata (key, value) values ('store_id'"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openFaultySQLiteStore(t, tc.failPattern)
			if err := store.configureConnection(); err != nil {
				t.Fatalf("configureConnection: %v", err)
			}
			if err := store.migrate(); err == nil {
				t.Fatalf("expected migrate to fail for pattern %q", tc.failPattern)
			}
		})
	}
}

func TestRemoveIssuePRNumberColumnInjectedFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-pr-number.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	for _, stmt := range []string{
		`CREATE TABLE store_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT)`,
		`CREATE TABLE epics (id TEXT PRIMARY KEY, project_id TEXT, name TEXT NOT NULL, description TEXT)`,
		`CREATE TABLE issues (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			epic_id TEXT,
			identifier TEXT UNIQUE NOT NULL,
			issue_type TEXT,
			provider_kind TEXT,
			provider_issue_ref TEXT,
			provider_shadow INTEGER,
			title TEXT NOT NULL,
			description TEXT,
			state TEXT NOT NULL DEFAULT 'backlog',
			workflow_phase TEXT,
			permission_profile TEXT,
			collaboration_mode_override TEXT,
			plan_approval_pending INTEGER,
			pending_plan_markdown TEXT,
			pending_plan_requested_at DATETIME,
			pending_plan_revision_markdown TEXT,
			pending_plan_revision_requested_at DATETIME,
			priority INTEGER,
			agent_name TEXT,
			agent_prompt TEXT,
			branch_name TEXT,
			pr_url TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			total_tokens_spent INTEGER,
			started_at DATETIME,
			completed_at DATETIME,
			last_synced_at DATETIME,
			pr_number INTEGER
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO projects (id, name, description) VALUES (?, ?, ?)`, "proj-legacy", "Legacy Project", ""); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO epics (id, project_id, name, description) VALUES (?, ?, ?, ?)`, "epic-legacy", "proj-legacy", "Legacy Epic", ""); err != nil {
		t.Fatalf("insert epic: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO issues (
			id, project_id, epic_id, identifier, issue_type, provider_kind, provider_issue_ref, provider_shadow,
			title, description, state, workflow_phase, permission_profile, collaboration_mode_override,
			plan_approval_pending, pending_plan_markdown, pending_plan_requested_at, pending_plan_revision_markdown,
			pending_plan_revision_requested_at, priority, agent_name, agent_prompt, branch_name, pr_url,
			created_at, updated_at, total_tokens_spent, started_at, completed_at, last_synced_at, pr_number
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"issue-legacy", "proj-legacy", "epic-legacy", "ISS-LEGACY", "", ProviderKindKanban, "", 0,
		"Legacy issue", "", StateBacklog, "", "", "",
		0, "", nil, "", nil, 0, "", "", nil, nil,
		now, now, 0, nil, nil, nil, 42,
	); err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	driverName := fmt.Sprintf("sqlite3-faulty-remove-pr-number-%d", atomic.AddInt64(&faultySQLiteDriverSeq, 1))
	sql.Register(driverName, &failingSQLiteDriver{
		inner:       &moderncsqlite.Driver{},
		failPattern: "create table issues_new",
		failErr:     errors.New("injected sqlite failure"),
	})
	faultyDB, err := sql.Open(driverName, sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open faulty: %v", err)
	}
	t.Cleanup(func() { _ = faultyDB.Close() })

	store := &Store{db: faultyDB, dbPath: dbPath}
	if err := store.configureConnection(); err != nil {
		t.Fatalf("configureConnection: %v", err)
	}
	if err := store.removeIssuePRNumberColumn(); err == nil {
		t.Fatal("expected removeIssuePRNumberColumn to fail")
	}
}
