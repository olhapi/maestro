package kanban

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/agentruntime"
)

func TestRuntimePersistenceRoundTrip(t *testing.T) {
	store := setupTestStore(t)

	project, err := store.CreateProject("Runtime project", "", "", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if project.RuntimeName != string(agentruntime.ProviderCodex) {
		t.Fatalf("expected project runtime default to codex, got %q", project.RuntimeName)
	}

	if err := store.UpdateProject(project.ID, "Runtime project updated", "desc", "", "", "runtime-alpha"); err != nil {
		t.Fatalf("UpdateProject runtime-alpha: %v", err)
	}
	updatedProject, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject updated: %v", err)
	}
	if updatedProject.RuntimeName != "runtime-alpha" {
		t.Fatalf("expected updated project runtime name, got %q", updatedProject.RuntimeName)
	}

	if err := store.UpdateProject(project.ID, "Runtime project preserved", "desc", "", ""); err != nil {
		t.Fatalf("UpdateProject preserve runtime: %v", err)
	}
	preservedProject, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject preserved: %v", err)
	}
	if preservedProject.RuntimeName != "runtime-alpha" {
		t.Fatalf("expected project runtime name to persist across updates, got %q", preservedProject.RuntimeName)
	}

	issue, err := store.CreateIssueWithOptions(project.ID, "", "Runtime issue", "Issue description", 7, []string{"tag-a"}, IssueCreateOptions{
		RuntimeName: "runtime-issue",
		AgentName:   "agent-a",
		AgentPrompt: "prompt-a",
	})
	if err != nil {
		t.Fatalf("CreateIssueWithOptions: %v", err)
	}
	if issue.RuntimeName != "runtime-issue" {
		t.Fatalf("expected issue runtime override to persist, got %q", issue.RuntimeName)
	}
	if issue.AgentName != "agent-a" || issue.AgentPrompt != "prompt-a" {
		t.Fatalf("expected agent metadata to stay separate, got %#v", issue)
	}

	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"runtime_name": "runtime-issue-2"}); err != nil {
		t.Fatalf("UpdateIssue runtime-issue-2: %v", err)
	}
	updatedIssue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue updated: %v", err)
	}
	if updatedIssue.RuntimeName != "runtime-issue-2" {
		t.Fatalf("expected updated issue runtime name, got %q", updatedIssue.RuntimeName)
	}

	if err := store.UpdateIssue(issue.ID, map[string]interface{}{"runtime_name": ""}); err != nil {
		t.Fatalf("UpdateIssue clear runtime: %v", err)
	}
	clearedIssue, err := store.GetIssue(issue.ID)
	if err != nil {
		t.Fatalf("GetIssue cleared: %v", err)
	}
	if clearedIssue.RuntimeName != "" {
		t.Fatalf("expected issue runtime override to clear, got %q", clearedIssue.RuntimeName)
	}

	persistedAt := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertIssueExecutionSession(ExecutionSessionSnapshot{
		IssueID:           issue.ID,
		Identifier:        issue.Identifier,
		Phase:             string(WorkflowPhaseImplementation),
		Attempt:           2,
		RunKind:           "run_started",
		RuntimeName:       "runtime-issue-2",
		RuntimeProvider:   string(agentruntime.ProviderCodex),
		RuntimeTransport:  string(agentruntime.TransportAppServer),
		RuntimeAuthSource: "cli",
		Error:             "",
		ResumeEligible:    true,
		UpdatedAt:         persistedAt,
		AppSession: agentruntime.Session{
			IssueID:         issue.ID,
			IssueIdentifier: issue.Identifier,
			SessionID:       "session-1",
			ThreadID:        "thread-1",
			TurnID:          "turn-1",
			LastEvent:       "turn.started",
			LastTimestamp:   persistedAt,
			Metadata: map[string]interface{}{
				"provider":    string(agentruntime.ProviderCodex),
				"transport":   string(agentruntime.TransportAppServer),
				"auth_source": "cli",
				"extra_field": "kept",
			},
		},
	}); err != nil {
		t.Fatalf("UpsertIssueExecutionSession: %v", err)
	}

	snapshot, err := store.GetIssueExecutionSession(issue.ID)
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if snapshot.RuntimeName != "runtime-issue-2" || snapshot.RuntimeProvider != string(agentruntime.ProviderCodex) || snapshot.RuntimeTransport != string(agentruntime.TransportAppServer) || snapshot.RuntimeAuthSource != "cli" {
		t.Fatalf("unexpected execution session runtime metadata: %#v", snapshot)
	}
	if snapshot.AppSession.SessionID != "session-1" || snapshot.AppSession.Metadata["extra_field"] != "kept" {
		t.Fatalf("unexpected execution session payload: %#v", snapshot.AppSession)
	}

	recent, err := store.ListRecentExecutionSessions(time.Time{}, 10)
	if err != nil {
		t.Fatalf("ListRecentExecutionSessions: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected one execution session, got %d", len(recent))
	}
	if recent[0].RuntimeName != snapshot.RuntimeName || recent[0].RuntimeProvider != snapshot.RuntimeProvider || recent[0].RuntimeTransport != snapshot.RuntimeTransport || recent[0].RuntimeAuthSource != snapshot.RuntimeAuthSource {
		t.Fatalf("unexpected recent execution session metadata: %#v", recent[0])
	}
}

func TestRuntimeMigrationAddsLegacyColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-runtime.db")
	db, err := sql.Open("sqlite3", sqliteDSN(dbPath))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	for _, stmt := range []string{
		`CREATE TABLE store_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE projects (
			id TEXT PRIMARY KEY,
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
			priority INTEGER DEFAULT 0,
			branch_name TEXT,
			pr_url TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			started_at DATETIME,
			completed_at DATETIME,
			total_tokens_spent INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE issue_execution_sessions (
			issue_id TEXT PRIMARY KEY,
			identifier TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL DEFAULT '',
			attempt INTEGER NOT NULL DEFAULT 0,
			run_kind TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			session_json TEXT NOT NULL DEFAULT '{}'
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, "proj-legacy", "Legacy Project", "legacy", now, now); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO issues (id, project_id, epic_id, identifier, title, description, state, priority, branch_name, pr_url, created_at, updated_at, started_at, completed_at, total_tokens_spent) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"issue-legacy", "proj-legacy", nil, "LEG-1", "Legacy issue", "legacy issue", StateBacklog, 0, "", "", now, now, nil, nil, 0,
	); err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO issue_execution_sessions (issue_id, identifier, phase, attempt, run_kind, error, updated_at, session_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"issue-legacy", "LEG-1", string(WorkflowPhaseImplementation), 1, "run_started", "", now, `{"session_id":"legacy-session","thread_id":"legacy-thread"}`,
	); err != nil {
		t.Fatalf("insert execution session: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store := openSQLiteStoreAt(t, dbPath)

	hasProjectRuntime, err := store.tableHasColumn("projects", "runtime_name")
	if err != nil {
		t.Fatalf("tableHasColumn projects runtime_name: %v", err)
	}
	if !hasProjectRuntime {
		t.Fatal("expected projects.runtime_name column after migration")
	}
	hasIssueRuntime, err := store.tableHasColumn("issues", "runtime_name")
	if err != nil {
		t.Fatalf("tableHasColumn issues runtime_name: %v", err)
	}
	if !hasIssueRuntime {
		t.Fatal("expected issues.runtime_name column after migration")
	}
	for _, column := range []string{"runtime_name", "runtime_provider", "runtime_transport", "runtime_auth_source"} {
		hasColumn, err := store.tableHasColumn("issue_execution_sessions", column)
		if err != nil {
			t.Fatalf("tableHasColumn issue_execution_sessions %s: %v", column, err)
		}
		if !hasColumn {
			t.Fatalf("expected issue_execution_sessions.%s column after migration", column)
		}
	}

	project, err := store.GetProject("proj-legacy")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if project.RuntimeName != string(agentruntime.ProviderCodex) {
		t.Fatalf("expected migrated project runtime default to codex, got %q", project.RuntimeName)
	}

	summary, total, err := store.ListIssueSummaries(IssueQuery{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ListIssueSummaries: %v", err)
	}
	if total != 1 || len(summary) != 1 {
		t.Fatalf("expected one migrated issue summary, got total=%d items=%#v", total, summary)
	}
	if summary[0].RuntimeName != "" {
		t.Fatalf("expected migrated issue runtime override to remain empty, got %q", summary[0].RuntimeName)
	}

	session, err := store.GetIssueExecutionSession("issue-legacy")
	if err != nil {
		t.Fatalf("GetIssueExecutionSession: %v", err)
	}
	if session.RuntimeName != "" || session.RuntimeProvider != "" || session.RuntimeTransport != "" || session.RuntimeAuthSource != "" {
		t.Fatalf("expected migrated execution session runtime metadata to default blank, got %#v", session)
	}
}
