package kanban

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/olhapi/maestro/internal/agentruntime"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/pkg/config"
)

// Store manages persistence for the kanban board
type Store struct {
	db       *sql.DB
	dbPath   string
	storeID  string
	readOnly bool
}

const (
	sqliteMaxOpenConns = 8
	sqliteMaxIdleConns = 4
	issueSelectColumns = `id, project_id, epic_id, identifier, issue_type, provider_kind, provider_issue_ref, provider_shadow, title, description, state, workflow_phase, permission_profile, collaboration_mode_override, plan_approval_pending, pending_plan_markdown, pending_plan_requested_at, pending_plan_revision_markdown, pending_plan_revision_requested_at, priority,
	       agent_name, agent_prompt, branch_name, pr_url, created_at, updated_at, total_tokens_spent, started_at, completed_at, last_synced_at`
	qualifiedIssueSelectColumns = `i.id, i.project_id, i.epic_id, i.identifier, i.issue_type, i.provider_kind, i.provider_issue_ref, i.provider_shadow, i.title, i.description, i.state, i.workflow_phase, i.permission_profile, i.collaboration_mode_override, i.plan_approval_pending, i.pending_plan_markdown, i.pending_plan_requested_at, i.pending_plan_revision_markdown, i.pending_plan_revision_requested_at, i.priority,
	       i.agent_name, i.agent_prompt, i.branch_name, i.pr_url, i.created_at, i.updated_at, i.total_tokens_spent, i.started_at, i.completed_at, i.last_synced_at`
)

func gitCommandEnv() []string {
	env := os.Environ()
	filtered := env[:0]
	for _, value := range env {
		if !strings.HasPrefix(value, "GIT_") {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func nullableStringValue(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func stringFromNull(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func isResolvedBlockerState(state State) bool {
	return state == StateDone || state == StateCancelled
}

func unresolvedBlockerExistsClause(issueAlias string) string {
	return `EXISTS (
		SELECT 1
		FROM issue_blockers b
		LEFT JOIN issues blocker ON blocker.identifier = b.blocked_by
		WHERE b.issue_id = ` + issueAlias + `.id
		  AND (blocker.id IS NULL OR blocker.state NOT IN ('done', 'cancelled'))
	)`
}

func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".maestro", "maestro.db")
	}
	return filepath.Join(".", ".maestro", "maestro.db")
}

func ResolveDBPath(dbPath string) string {
	if strings.TrimSpace(dbPath) == "" {
		return DefaultDBPath()
	}
	return expandPathValue(dbPath)
}

// HasUnresolvedExpandedEnvPath reports whether expansion left a path segment unresolved.
func HasUnresolvedExpandedEnvPath(rawPath, resolvedPath string) bool {
	if !strings.HasPrefix(strings.TrimSpace(rawPath), "$") {
		return false
	}

	cleaned := filepath.Clean(strings.TrimSpace(resolvedPath))
	for _, segment := range strings.Split(cleaned, string(filepath.Separator)) {
		if strings.HasPrefix(segment, "$") {
			return true
		}
	}
	return false
}

func expandPathValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}

	if strings.HasPrefix(value, "$") {
		value = os.Expand(value, func(name string) string {
			resolved, ok := os.LookupEnv(name)
			if !ok || strings.TrimSpace(resolved) == "" {
				return "$" + name
			}
			return resolved
		})
	}

	if strings.HasPrefix(value, "~") {
		home, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(home) != "" {
			switch {
			case value == "~":
				return home
			case strings.HasPrefix(value, "~/"):
				return filepath.Join(home, value[2:])
			}
		}
	}

	return value
}

func ResolveProjectPaths(repoPath, workflowPath string) (string, string, error) {
	repoPath = strings.TrimSpace(repoPath)
	workflowPath = strings.TrimSpace(workflowPath)
	if repoPath == "" {
		return "", "", nil
	}

	absRepoPath, err := resolveConfiguredPath(repoPath)
	if err != nil {
		return "", "", err
	}
	if workflowPath == "" {
		return absRepoPath, filepath.Join(absRepoPath, "WORKFLOW.md"), nil
	}

	absWorkflowPath, err := resolveConfiguredPath(workflowPath)
	if err != nil {
		return "", "", err
	}
	return absRepoPath, absWorkflowPath, nil
}

func resolveConfiguredPath(raw string) (string, error) {
	value := expandPathValue(raw)
	if strings.HasPrefix(value, "$") {
		return filepath.Clean(value), nil
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	baseDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(baseDir, value)), nil
}

// NewStore creates a new store with the given database path
func NewStore(dbPath string) (*Store, error) {
	return newStoreWithMode(dbPath, false, nil)
}

// NewReadOnlyStore opens an existing store without running migrations or backfills.
func NewReadOnlyStore(dbPath string) (*Store, error) {
	return newStoreWithMode(dbPath, true, nil)
}

func newStoreWithMigrator(dbPath string, migrateFn func(*Store) error) (*Store, error) {
	return newStoreWithMode(dbPath, false, migrateFn)
}

func newStoreWithMode(dbPath string, readOnly bool, migrateFn func(*Store) error) (*Store, error) {
	rawPath := dbPath
	dbPath = ResolveDBPath(dbPath)
	if HasUnresolvedExpandedEnvPath(rawPath, dbPath) {
		return nil, fmt.Errorf("failed to resolve database path: unresolved environment variable in %q", dbPath)
	}
	absDBPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve database path: %w", err)
	}
	if migrateFn == nil {
		migrateFn = func(store *Store) error {
			return store.migrate()
		}
	}
	dsn := sqliteDSN(absDBPath)
	if readOnly {
		dsn = sqliteReadOnlyDSN(absDBPath)
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	db.SetMaxOpenConns(sqliteMaxOpenConns)
	db.SetMaxIdleConns(sqliteMaxIdleConns)

	store := &Store{db: db, dbPath: absDBPath, readOnly: readOnly}
	if err := store.configureConnection(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to configure sqlite connection: %w", err)
	}
	if !readOnly {
		if err := migrateFn(store); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to migrate: %w", err)
		}
		if err := store.backfillLegacyProjectPermissionProfiles(); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to backfill legacy project permissions: %w", err)
		}
	}

	return store, nil
}

func (s *Store) configureConnection() error {
	pragmas := []string{
		`PRAGMA busy_timeout=10000`,
		`PRAGMA foreign_keys=ON`,
	}
	if !s.readOnly {
		pragmas = append([]string{
			`PRAGMA journal_mode=WAL`,
			`PRAGMA synchronous=NORMAL`,
		}, pragmas...)
	}
	for _, pragma := range pragmas {
		if _, err := s.db.Exec(pragma); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}

// ReadOnly reports whether the store was opened without migration/write support.
func (s *Store) ReadOnly() bool {
	return s != nil && s.readOnly
}

func (s *Store) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS store_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS projects (
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
		`CREATE TABLE IF NOT EXISTS epics (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			name TEXT NOT NULL,
			description TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (project_id) REFERENCES projects(id)
		)`,
		`CREATE TABLE IF NOT EXISTS issues (
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
			permission_profile TEXT NOT NULL DEFAULT 'default',
			collaboration_mode_override TEXT NOT NULL DEFAULT '',
			plan_approval_pending INTEGER NOT NULL DEFAULT 0,
			pending_plan_markdown TEXT NOT NULL DEFAULT '',
			pending_plan_requested_at DATETIME,
			pending_plan_revision_markdown TEXT NOT NULL DEFAULT '',
			pending_plan_revision_requested_at DATETIME,
			priority INTEGER DEFAULT 0,
			agent_name TEXT NOT NULL DEFAULT '',
			agent_prompt TEXT NOT NULL DEFAULT '',
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
		`CREATE TABLE IF NOT EXISTS issue_recurrences (
			issue_id TEXT PRIMARY KEY,
			cron TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			next_run_at DATETIME,
			last_enqueued_at DATETIME,
			pending_rerun INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE TABLE IF NOT EXISTS issue_labels (
			issue_id TEXT NOT NULL,
			label TEXT NOT NULL,
			PRIMARY KEY (issue_id, label),
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE TABLE IF NOT EXISTS issue_blockers (
			issue_id TEXT NOT NULL,
			blocked_by TEXT NOT NULL,
			PRIMARY KEY (issue_id, blocked_by),
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE TABLE IF NOT EXISTS issue_assets (
			id TEXT PRIMARY KEY,
			issue_id TEXT NOT NULL,
			filename TEXT NOT NULL,
			content_type TEXT NOT NULL,
			byte_size INTEGER NOT NULL,
			storage_path TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE TABLE IF NOT EXISTS issue_comments (
			id TEXT PRIMARY KEY,
			issue_id TEXT NOT NULL,
			parent_comment_id TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			author_json TEXT NOT NULL DEFAULT '{}',
			provider_kind TEXT NOT NULL DEFAULT 'kanban',
			provider_comment_ref TEXT NOT NULL DEFAULT '',
			deleted_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issue_comments_issue_created ON issue_comments(issue_id, created_at ASC, id ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_issue_comments_issue_parent_created ON issue_comments(issue_id, parent_comment_id, created_at ASC, id ASC)`,
		`CREATE TABLE IF NOT EXISTS issue_comment_attachments (
			id TEXT PRIMARY KEY,
			comment_id TEXT NOT NULL,
			filename TEXT NOT NULL,
			content_type TEXT NOT NULL,
			byte_size INTEGER NOT NULL,
			url TEXT NOT NULL DEFAULT '',
			storage_path TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (comment_id) REFERENCES issue_comments(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issue_comment_attachments_comment_created ON issue_comment_attachments(comment_id, created_at ASC, id ASC)`,
		`CREATE TABLE IF NOT EXISTS workspaces (
			issue_id TEXT PRIMARY KEY,
			path TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			last_run_at DATETIME,
			run_count INTEGER DEFAULT 0,
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_state ON issues(state)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_project ON issues(project_id)`,
		`CREATE INDEX IF NOT EXISTS idx_issues_epic ON issues(epic_id)`,
		`CREATE TABLE IF NOT EXISTS counters (
			name TEXT PRIMARY KEY,
			value INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS runtime_events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			kind TEXT NOT NULL,
			issue_id TEXT,
			identifier TEXT,
			title TEXT,
			attempt INTEGER DEFAULT 0,
			delay_type TEXT,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			total_tokens INTEGER DEFAULT 0,
			error TEXT,
			event_ts DATETIME NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_runtime_events_ts ON runtime_events(event_ts)`,
		`CREATE INDEX IF NOT EXISTS idx_runtime_events_kind ON runtime_events(kind)`,
		`CREATE INDEX IF NOT EXISTS idx_runtime_events_issue_id ON runtime_events(issue_id)`,
		`CREATE TABLE IF NOT EXISTS issue_execution_sessions (
			issue_id TEXT PRIMARY KEY,
			identifier TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL DEFAULT '',
			attempt INTEGER NOT NULL DEFAULT 0,
			run_kind TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			resume_eligible INTEGER NOT NULL DEFAULT 0,
			stop_reason TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL,
			session_json TEXT NOT NULL DEFAULT '{}',
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE TABLE IF NOT EXISTS issue_activity_entries (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			issue_id TEXT NOT NULL,
			identifier TEXT NOT NULL DEFAULT '',
			logical_id TEXT NOT NULL UNIQUE,
			attempt INTEGER NOT NULL DEFAULT 0,
			thread_id TEXT NOT NULL DEFAULT '',
			turn_id TEXT NOT NULL DEFAULT '',
			item_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			item_type TEXT NOT NULL DEFAULT '',
			phase TEXT NOT NULL DEFAULT '',
			entry_status TEXT NOT NULL DEFAULT '',
			tier TEXT NOT NULL DEFAULT 'primary',
			title TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			tone TEXT NOT NULL DEFAULT '',
			expandable INTEGER NOT NULL DEFAULT 0,
			started_at DATETIME,
			completed_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			raw_payload_json TEXT NOT NULL DEFAULT '{}',
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issue_activity_entries_issue_attempt_seq ON issue_activity_entries(issue_id, attempt, seq)`,
		`CREATE TABLE IF NOT EXISTS issue_activity_updates (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			issue_id TEXT NOT NULL,
			entry_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			event_ts DATETIME NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}',
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issue_activity_updates_issue_seq ON issue_activity_updates(issue_id, seq)`,
		`CREATE TABLE IF NOT EXISTS issue_agent_commands (
			id TEXT PRIMARY KEY,
			issue_id TEXT NOT NULL,
			command TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME NOT NULL,
			delivered_at DATETIME,
			steered_at DATETIME,
			delivery_mode TEXT NOT NULL DEFAULT '',
			delivery_thread_id TEXT NOT NULL DEFAULT '',
			delivery_attempt INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issue_agent_commands_issue_created ON issue_agent_commands(issue_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_issue_agent_commands_issue_status_created ON issue_agent_commands(issue_id, status, created_at ASC)`,
		`CREATE TABLE IF NOT EXISTS interrupt_acknowledgements (
			interrupt_id TEXT PRIMARY KEY,
			acknowledged_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS change_events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			entity_type TEXT NOT NULL,
			entity_id TEXT,
			action TEXT NOT NULL,
			event_ts DATETIME NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_change_events_ts ON change_events(event_ts)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return err
		}
	}
	if err := s.ensureProjectColumns(); err != nil {
		return err
	}
	if err := s.ensureIssueColumns(); err != nil {
		return err
	}
	if err := s.ensureIssueRecurrenceTables(); err != nil {
		return err
	}
	if err := s.ensureIssueExecutionSessionColumns(); err != nil {
		return err
	}
	if err := s.ensureIssueAssetTables(); err != nil {
		return err
	}
	if err := s.ensureIssueCommentTables(); err != nil {
		return err
	}
	if err := s.ensureIssueAgentCommandColumns(); err != nil {
		return err
	}
	if err := s.ensureIssuePlanningTables(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DROP INDEX IF EXISTS idx_projects_repo_path_unique`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_repo_path_unique ON projects(repo_path) WHERE repo_path <> ''`); err != nil {
		return err
	}
	return s.ensureStoreID()
}

func (s *Store) ensureProjectColumns() error {
	for _, stmt := range []string{
		`ALTER TABLE projects ADD COLUMN state TEXT NOT NULL DEFAULT 'stopped'`,
		`ALTER TABLE projects ADD COLUMN permission_profile TEXT NOT NULL DEFAULT 'default'`,
		`ALTER TABLE projects ADD COLUMN repo_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE projects ADD COLUMN workflow_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE projects ADD COLUMN provider_kind TEXT NOT NULL DEFAULT 'kanban'`,
		`ALTER TABLE projects ADD COLUMN provider_project_ref TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE projects ADD COLUMN provider_config_json TEXT NOT NULL DEFAULT '{}'`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	return nil
}

func (s *Store) ensureIssueColumns() error {
	for _, stmt := range []string{
		`ALTER TABLE issues ADD COLUMN workflow_phase TEXT NOT NULL DEFAULT 'implementation'`,
		`ALTER TABLE issues ADD COLUMN issue_type TEXT NOT NULL DEFAULT 'standard'`,
		`ALTER TABLE issues ADD COLUMN provider_kind TEXT NOT NULL DEFAULT 'kanban'`,
		`ALTER TABLE issues ADD COLUMN provider_issue_ref TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issues ADD COLUMN provider_shadow INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE issues ADD COLUMN permission_profile TEXT NOT NULL DEFAULT 'default'`,
		`ALTER TABLE issues ADD COLUMN collaboration_mode_override TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issues ADD COLUMN plan_approval_pending INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE issues ADD COLUMN pending_plan_markdown TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issues ADD COLUMN pending_plan_requested_at DATETIME`,
		`ALTER TABLE issues ADD COLUMN pending_plan_revision_markdown TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issues ADD COLUMN pending_plan_revision_requested_at DATETIME`,
		`ALTER TABLE issues ADD COLUMN total_tokens_spent INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE issues ADD COLUMN last_synced_at DATETIME`,
		`ALTER TABLE issues ADD COLUMN agent_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issues ADD COLUMN agent_prompt TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_issues_provider_ref_unique ON issues(provider_kind, provider_issue_ref) WHERE provider_issue_ref <> ''`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_issue_type ON issues(issue_type)`); err != nil {
		return err
	}
	if err := s.backfillIssueTypes(); err != nil {
		return err
	}
	if err := s.backfillWorkflowPhases(); err != nil {
		return err
	}
	if err := s.removeIssuePRNumberColumn(); err != nil {
		return err
	}
	return s.normalizeIssueForeignKeys()
}

func (s *Store) ensureIssueRecurrenceTables() error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS issue_recurrences (
			issue_id TEXT PRIMARY KEY,
			cron TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			next_run_at DATETIME,
			last_enqueued_at DATETIME,
			pending_rerun INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`ALTER TABLE issue_recurrences ADD COLUMN next_run_at DATETIME`,
		`ALTER TABLE issue_recurrences ADD COLUMN last_enqueued_at DATETIME`,
		`ALTER TABLE issue_recurrences ADD COLUMN pending_rerun INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_issue_recurrences_enabled_next_run ON issue_recurrences(enabled, next_run_at)`); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureIssueExecutionSessionColumns() error {
	for _, stmt := range []string{
		`ALTER TABLE issue_execution_sessions ADD COLUMN resume_eligible INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE issue_execution_sessions ADD COLUMN stop_reason TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	return nil
}

func (s *Store) ensureIssueAgentCommandColumns() error {
	for _, stmt := range []string{
		`ALTER TABLE issue_agent_commands ADD COLUMN steered_at DATETIME`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	return nil
}

func (s *Store) ensureIssuePlanningTables() error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS issue_plan_sessions (
			id TEXT PRIMARY KEY,
			issue_id TEXT NOT NULL,
			status TEXT NOT NULL,
			origin_attempt INTEGER NOT NULL DEFAULT 0,
			origin_thread_id TEXT NOT NULL DEFAULT '',
			current_version_number INTEGER NOT NULL DEFAULT 0,
			pending_revision_note TEXT NOT NULL DEFAULT '',
			pending_revision_requested_at DATETIME,
			opened_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			closed_at DATETIME,
			closed_reason TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issue_plan_sessions_issue_updated ON issue_plan_sessions(issue_id, updated_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_issue_plan_sessions_open_issue ON issue_plan_sessions(issue_id) WHERE closed_at IS NULL`,
		`CREATE TABLE IF NOT EXISTS issue_plan_versions (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			version_number INTEGER NOT NULL,
			markdown TEXT NOT NULL DEFAULT '',
			revision_note TEXT NOT NULL DEFAULT '',
			attempt INTEGER NOT NULL DEFAULT 0,
			thread_id TEXT NOT NULL DEFAULT '',
			turn_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			FOREIGN KEY (session_id) REFERENCES issue_plan_sessions(id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_issue_plan_versions_session_version ON issue_plan_versions(session_id, version_number)`,
		`CREATE INDEX IF NOT EXISTS idx_issue_plan_versions_session_created ON issue_plan_versions(session_id, created_at DESC)`,
		`ALTER TABLE issue_plan_sessions ADD COLUMN pending_revision_note TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issue_plan_sessions ADD COLUMN pending_revision_requested_at DATETIME`,
		`ALTER TABLE issue_plan_sessions ADD COLUMN origin_attempt INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE issue_plan_sessions ADD COLUMN origin_thread_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issue_plan_sessions ADD COLUMN current_version_number INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE issue_plan_sessions ADD COLUMN closed_reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issue_plan_versions ADD COLUMN revision_note TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issue_plan_versions ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE issue_plan_versions ADD COLUMN thread_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE issue_plan_versions ADD COLUMN turn_id TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	return s.backfillOpenIssuePlanSessions()
}

type issuePlanSessionRecord struct {
	ID                         string
	IssueID                    string
	Status                     IssuePlanningStatus
	OriginAttempt              int
	OriginThreadID             string
	CurrentVersionNumber       int
	PendingRevisionNote        string
	PendingRevisionRequestedAt *time.Time
	OpenedAt                   time.Time
	UpdatedAt                  time.Time
	ClosedAt                   *time.Time
	ClosedReason               string
}

func (s *Store) backfillOpenIssuePlanSessions() error {
	const migrationKey = "issue_plan_sessions_open_backfill_v1"

	var applied string
	switch err := s.db.QueryRow(`SELECT value FROM store_metadata WHERE key = ?`, migrationKey).Scan(&applied); {
	case err == nil && applied == "done":
		return nil
	case err != nil && err != sql.ErrNoRows:
		return err
	}

	rows, err := s.db.Query(`
		SELECT ` + issueSelectColumns + `
		FROM issues
		WHERE plan_approval_pending = 1
			OR (TRIM(pending_plan_revision_markdown) <> '' AND pending_plan_revision_requested_at IS NOT NULL)
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	issues := make([]Issue, 0)
	for rows.Next() {
		record, err := scanIssueRecord(rows)
		if err != nil {
			return err
		}
		if record != nil {
			issues = append(issues, *record)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	for i := range issues {
		issue := &issues[i]
		existing, err := s.getLatestIssuePlanSessionTx(tx, issue.ID, true)
		if err != nil {
			return err
		}
		if existing != nil {
			continue
		}
		requestedAt := issue.UpdatedAt.UTC()
		if issue.PendingPlanRequestedAt != nil && !issue.PendingPlanRequestedAt.IsZero() {
			requestedAt = issue.PendingPlanRequestedAt.UTC()
		}
		status := IssuePlanningStatusAwaitingApproval
		if strings.TrimSpace(issue.PendingPlanRevisionMarkdown) != "" && issue.PendingPlanRevisionRequestedAt != nil {
			status = IssuePlanningStatusRevisionRequested
		}
		session := issuePlanSessionRecord{
			ID:                   generateID("pls"),
			IssueID:              issue.ID,
			Status:               status,
			CurrentVersionNumber: 1,
			PendingRevisionNote:  strings.TrimSpace(issue.PendingPlanRevisionMarkdown),
			OpenedAt:             requestedAt,
			UpdatedAt:            requestedAt,
		}
		if issue.PendingPlanRevisionRequestedAt != nil && !issue.PendingPlanRevisionRequestedAt.IsZero() {
			revisionRequestedAt := issue.PendingPlanRevisionRequestedAt.UTC()
			session.PendingRevisionRequestedAt = &revisionRequestedAt
			session.UpdatedAt = revisionRequestedAt
		}
		if err := s.insertIssuePlanSessionTx(tx, session); err != nil {
			return err
		}
		if strings.TrimSpace(issue.PendingPlanMarkdown) != "" {
			if err := s.insertIssuePlanVersionTx(tx, IssuePlanVersion{
				ID:            generateID("plv"),
				SessionID:     session.ID,
				VersionNumber: 1,
				Markdown:      issue.PendingPlanMarkdown,
				CreatedAt:     requestedAt,
			}); err != nil {
				return err
			}
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO store_metadata (key, value) VALUES (?, 'done')`, migrationKey); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) getLatestIssuePlanSessionTx(queryer interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}, issueID string, preferOpen bool) (*issuePlanSessionRecord, error) {
	if strings.TrimSpace(issueID) == "" {
		return nil, nil
	}
	query := `
		SELECT id, issue_id, status, origin_attempt, origin_thread_id, current_version_number,
		       pending_revision_note, pending_revision_requested_at, opened_at, updated_at, closed_at, closed_reason
		FROM issue_plan_sessions
		WHERE issue_id = ?
		ORDER BY `
	if preferOpen {
		query += `CASE WHEN closed_at IS NULL THEN 0 ELSE 1 END, `
	}
	query += `opened_at DESC
		LIMIT 1`
	record, err := scanIssuePlanSessionRow(queryer.QueryRow(query, issueID))
	switch {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, err
	default:
		return record, nil
	}
}

func (s *Store) insertIssuePlanSessionTx(tx *sql.Tx, session issuePlanSessionRecord) error {
	_, err := tx.Exec(`
		INSERT INTO issue_plan_sessions (
			id, issue_id, status, origin_attempt, origin_thread_id, current_version_number,
			pending_revision_note, pending_revision_requested_at, opened_at, updated_at, closed_at, closed_reason
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.IssueID,
		session.Status,
		session.OriginAttempt,
		session.OriginThreadID,
		session.CurrentVersionNumber,
		session.PendingRevisionNote,
		session.PendingRevisionRequestedAt,
		session.OpenedAt.UTC(),
		session.UpdatedAt.UTC(),
		session.ClosedAt,
		session.ClosedReason,
	)
	return err
}

func (s *Store) insertIssuePlanVersionTx(tx *sql.Tx, version IssuePlanVersion) error {
	createdAt := version.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := tx.Exec(`
		INSERT INTO issue_plan_versions (
			id, session_id, version_number, markdown, revision_note, attempt, thread_id, turn_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		version.ID,
		version.SessionID,
		version.VersionNumber,
		version.Markdown,
		version.RevisionNote,
		version.Attempt,
		version.ThreadID,
		version.TurnID,
		createdAt,
	)
	return err
}

func scanIssuePlanSessionRow(scanner interface {
	Scan(dest ...interface{}) error
}) (*issuePlanSessionRecord, error) {
	var record issuePlanSessionRecord
	var pendingRevisionRequestedAt sql.NullTime
	var closedAt sql.NullTime
	if err := scanner.Scan(
		&record.ID,
		&record.IssueID,
		&record.Status,
		&record.OriginAttempt,
		&record.OriginThreadID,
		&record.CurrentVersionNumber,
		&record.PendingRevisionNote,
		&pendingRevisionRequestedAt,
		&record.OpenedAt,
		&record.UpdatedAt,
		&closedAt,
		&record.ClosedReason,
	); err != nil {
		return nil, err
	}
	if pendingRevisionRequestedAt.Valid {
		ts := pendingRevisionRequestedAt.Time.UTC()
		record.PendingRevisionRequestedAt = &ts
	}
	if closedAt.Valid {
		ts := closedAt.Time.UTC()
		record.ClosedAt = &ts
	}
	record.OpenedAt = record.OpenedAt.UTC()
	record.UpdatedAt = record.UpdatedAt.UTC()
	return &record, nil
}

func (s *Store) ensureIssueAssetTables() error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS issue_assets (
			id TEXT PRIMARY KEY,
			issue_id TEXT NOT NULL,
			filename TEXT NOT NULL,
			content_type TEXT NOT NULL,
			byte_size INTEGER NOT NULL,
			storage_path TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (issue_id) REFERENCES issues(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_issue_assets_issue_created ON issue_assets(issue_id, created_at ASC)`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) backfillIssueTypes() error {
	var applied string
	err := s.db.QueryRow(`SELECT value FROM store_metadata WHERE key = 'issue_type_backfill_v1'`).Scan(&applied)
	switch {
	case err == nil && applied == "done":
		return nil
	case err != nil && err != sql.ErrNoRows:
		return err
	}
	if _, err := s.db.Exec(`
		UPDATE issues
		SET issue_type = 'standard'
		WHERE issue_type IS NULL OR issue_type = ''`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO store_metadata (key, value) VALUES ('issue_type_backfill_v1', 'done')`); err != nil {
		return err
	}
	return nil
}

func (s *Store) backfillWorkflowPhases() error {
	var applied string
	err := s.db.QueryRow(`SELECT value FROM store_metadata WHERE key = 'workflow_phase_backfill_v1'`).Scan(&applied)
	switch {
	case err == sql.ErrNoRows:
	case err != nil:
		return err
	default:
		if applied == "done" {
			return nil
		}
	}

	if _, err := s.db.Exec(`
		UPDATE issues
		SET workflow_phase = CASE
			WHEN state IN ('done', 'cancelled') THEN 'complete'
			ELSE 'implementation'
		END
		WHERE workflow_phase IS NULL OR workflow_phase = '' OR workflow_phase = 'implementation'`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO store_metadata (key, value) VALUES ('workflow_phase_backfill_v1', 'done')`); err != nil {
		return err
	}
	return nil
}

func (s *Store) removeIssuePRNumberColumn() (err error) {
	const migrationKey = "issue_pr_number_drop_v1"

	var applied string
	switch err := s.db.QueryRow(`SELECT value FROM store_metadata WHERE key = ?`, migrationKey).Scan(&applied); {
	case err == nil && applied == "done":
		return nil
	case err != nil && err != sql.ErrNoRows:
		return err
	}

	hasColumn, err := s.tableHasColumn("issues", "pr_number")
	if err != nil {
		return err
	}
	if !hasColumn {
		_, err := s.db.Exec(`INSERT OR REPLACE INTO store_metadata (key, value) VALUES (?, 'done')`, migrationKey)
		return err
	}

	if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer func() {
		if _, pragmaErr := s.db.Exec(`PRAGMA foreign_keys=ON`); err == nil && pragmaErr != nil {
			err = pragmaErr
		}
	}()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	for _, stmt := range []string{
		`CREATE TABLE issues_new (
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
			permission_profile TEXT NOT NULL DEFAULT 'default',
			collaboration_mode_override TEXT NOT NULL DEFAULT '',
			plan_approval_pending INTEGER NOT NULL DEFAULT 0,
			pending_plan_markdown TEXT NOT NULL DEFAULT '',
			pending_plan_requested_at DATETIME,
			pending_plan_revision_markdown TEXT NOT NULL DEFAULT '',
			pending_plan_revision_requested_at DATETIME,
			priority INTEGER DEFAULT 0,
			agent_name TEXT NOT NULL DEFAULT '',
			agent_prompt TEXT NOT NULL DEFAULT '',
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
		`INSERT INTO issues_new (
				id, project_id, epic_id, identifier, issue_type, provider_kind, provider_issue_ref, provider_shadow, title, description,
				state, workflow_phase, permission_profile, collaboration_mode_override, plan_approval_pending, pending_plan_markdown, pending_plan_requested_at, pending_plan_revision_markdown, pending_plan_revision_requested_at, priority, agent_name, agent_prompt, branch_name, pr_url, created_at, updated_at, total_tokens_spent, started_at, completed_at, last_synced_at
			)
		SELECT
			legacy.id,
			CASE
				WHEN legacy.project_id IS NOT NULL
					AND TRIM(legacy.project_id) <> ''
					AND EXISTS (SELECT 1 FROM projects WHERE id = TRIM(legacy.project_id))
				THEN TRIM(legacy.project_id)
				WHEN legacy.epic_id IS NOT NULL
					AND TRIM(legacy.epic_id) <> ''
					AND EXISTS (SELECT 1 FROM epics WHERE id = TRIM(legacy.epic_id))
				THEN (SELECT project_id FROM epics WHERE id = TRIM(legacy.epic_id))
				ELSE NULL
			END,
			CASE
				WHEN legacy.epic_id IS NOT NULL
					AND TRIM(legacy.epic_id) <> ''
					AND EXISTS (SELECT 1 FROM epics WHERE id = TRIM(legacy.epic_id))
				THEN TRIM(legacy.epic_id)
				ELSE NULL
			END,
			legacy.identifier, legacy.issue_type, legacy.provider_kind, legacy.provider_issue_ref, legacy.provider_shadow, legacy.title, legacy.description,
			legacy.state, legacy.workflow_phase, COALESCE(NULLIF(TRIM(legacy.permission_profile), ''), 'default'), COALESCE(NULLIF(TRIM(legacy.collaboration_mode_override), ''), ''), COALESCE(legacy.plan_approval_pending, 0), COALESCE(legacy.pending_plan_markdown, ''), legacy.pending_plan_requested_at, COALESCE(legacy.pending_plan_revision_markdown, ''), legacy.pending_plan_revision_requested_at, legacy.priority, COALESCE(legacy.agent_name, ''), COALESCE(legacy.agent_prompt, ''), legacy.branch_name, legacy.pr_url, legacy.created_at, legacy.updated_at, legacy.total_tokens_spent, legacy.started_at, legacy.completed_at, legacy.last_synced_at
			FROM issues AS legacy`,
		`DROP TABLE issues`,
		`ALTER TABLE issues_new RENAME TO issues`,
		`CREATE INDEX idx_issues_state ON issues(state)`,
		`CREATE INDEX idx_issues_project ON issues(project_id)`,
		`CREATE INDEX idx_issues_epic ON issues(epic_id)`,
		`CREATE UNIQUE INDEX idx_issues_provider_ref_unique ON issues(provider_kind, provider_issue_ref) WHERE provider_issue_ref <> ''`,
		`CREATE INDEX idx_issues_issue_type ON issues(issue_type)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil

	if err := s.verifyForeignKeys(migrationKey); err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO store_metadata (key, value) VALUES (?, 'done')`, migrationKey)
	return err
}

func (s *Store) normalizeIssueForeignKeys() error {
	const migrationKey = "issue_foreign_key_normalization_v1"

	var applied string
	switch err := s.db.QueryRow(`SELECT value FROM store_metadata WHERE key = ?`, migrationKey).Scan(&applied); {
	case err == nil && applied == "done":
		return nil
	case err != nil && err != sql.ErrNoRows:
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	for _, stmt := range []string{
		`UPDATE issues
		SET epic_id = CASE
			WHEN epic_id IS NOT NULL
				AND TRIM(epic_id) <> ''
				AND EXISTS (SELECT 1 FROM epics WHERE epics.id = TRIM(issues.epic_id))
			THEN TRIM(epic_id)
			ELSE NULL
		END
		WHERE epic_id IS NOT NULL
			AND (
				epic_id <> TRIM(epic_id)
				OR TRIM(epic_id) = ''
				OR NOT EXISTS (SELECT 1 FROM epics WHERE epics.id = TRIM(issues.epic_id))
			)`,
		`UPDATE issues
		SET project_id = CASE
			WHEN project_id IS NOT NULL
				AND TRIM(project_id) <> ''
				AND EXISTS (SELECT 1 FROM projects WHERE projects.id = TRIM(issues.project_id))
			THEN TRIM(project_id)
			WHEN epic_id IS NOT NULL
				AND EXISTS (SELECT 1 FROM epics WHERE epics.id = issues.epic_id)
			THEN (SELECT project_id FROM epics WHERE epics.id = issues.epic_id)
			ELSE NULL
		END
		WHERE project_id IS NULL
			OR project_id <> TRIM(project_id)
			OR TRIM(project_id) = ''
			OR NOT EXISTS (SELECT 1 FROM projects WHERE projects.id = TRIM(issues.project_id))`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil

	if err := s.verifyForeignKeys(migrationKey); err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO store_metadata (key, value) VALUES (?, 'done')`, migrationKey)
	return err
}

func (s *Store) verifyForeignKeys(migrationKey string) error {
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowid int64
		var parent string
		var fkIndex int
		if scanErr := rows.Scan(&table, &rowid, &parent, &fkIndex); scanErr != nil {
			return scanErr
		}
		return fmt.Errorf("foreign key check failed after %s for table %s row %d referencing %s (%d)", migrationKey, table, rowid, parent, fkIndex)
	}

	return rows.Err()
}

func (s *Store) tableHasColumn(tableName, columnName string) (bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + tableName + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, columnName) {
			return true, nil
		}
	}

	return false, rows.Err()
}

func (s *Store) ensureStoreID() error {
	var storeID string
	err := s.db.QueryRow(`SELECT value FROM store_metadata WHERE key = 'store_id'`).Scan(&storeID)
	switch {
	case err == sql.ErrNoRows:
		storeID = generateID("store")
		if _, err := s.db.Exec(`INSERT INTO store_metadata (key, value) VALUES ('store_id', ?)`, storeID); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	s.storeID = storeID
	return nil
}

func (s *Store) Identity() StoreIdentity {
	return StoreIdentity{
		DBPath:  s.dbPath,
		StoreID: s.storeID,
	}
}

func (s *Store) DBPath() string {
	return s.dbPath
}

func (s *Store) StoreID() string {
	return s.storeID
}

func normalizeProjectPaths(repoPath, workflowPath string) (string, string, error) {
	return ResolveProjectPaths(repoPath, workflowPath)
}

func normalizeProviderKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", ProviderKindKanban:
		return ProviderKindKanban
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func legacyWorkflowPermissionProfile(repoPath, workflowPath string) PermissionProfile {
	repoPath = strings.TrimSpace(repoPath)
	workflowPath = strings.TrimSpace(workflowPath)
	if repoPath == "" && workflowPath == "" {
		return PermissionProfileDefault
	}
	usesFullAccess, err := config.LegacyWorkflowUsesFullAccess(config.ResolveWorkflowPath(repoPath, workflowPath))
	if err != nil || !usesFullAccess {
		return PermissionProfileDefault
	}
	return PermissionProfileFullAccess
}

func cloneProviderConfig(config map[string]interface{}) map[string]interface{} {
	if len(config) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(config))
	for key, value := range config {
		out[key] = value
	}
	return out
}

func decodeProviderConfig(raw string) map[string]interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]interface{}{}
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return map[string]interface{}{}
	}
	return out
}

func encodeProviderConfig(config map[string]interface{}) string {
	if len(config) == 0 {
		return "{}"
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func projectDefaultActiveStates(project Project) []string {
	config := cloneProviderConfig(project.ProviderConfig)
	if values, ok := config["active_states"]; ok {
		if states := interfaceSliceToStrings(values); len(states) > 0 {
			return states
		}
	}
	return []string{string(StateReady), string(StateInProgress), string(StateInReview)}
}

func projectDefaultTerminalStates(project Project) []string {
	config := cloneProviderConfig(project.ProviderConfig)
	if values, ok := config["terminal_states"]; ok {
		if states := interfaceSliceToStrings(values); len(states) > 0 {
			return states
		}
	}
	return []string{string(StateDone), string(StateCancelled)}
}

func interfaceSliceToStrings(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, raw := range typed {
			item := strings.TrimSpace(fmt.Sprint(raw))
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	default:
		return nil
	}
}

func hydrateProject(project *Project) {
	if project == nil {
		return
	}
	project.State = NormalizeProjectState(string(project.State))
	project.RepoPath = strings.TrimSpace(project.RepoPath)
	project.WorkflowPath = strings.TrimSpace(project.WorkflowPath)
	project.ProviderKind = normalizeProviderKind(project.ProviderKind)
	project.ProviderProjectRef = strings.TrimSpace(project.ProviderProjectRef)
	project.ProviderConfig = cloneProviderConfig(project.ProviderConfig)
	project.Capabilities = DefaultCapabilities(project.ProviderKind)
	project.DispatchError = ""
	if project.RepoPath != "" && project.WorkflowPath == "" {
		project.WorkflowPath = filepath.Join(project.RepoPath, "WORKFLOW.md")
	}
	if project.RepoPath == "" {
		project.OrchestrationReady = false
		project.DispatchReady = false
		return
	}
	repoInfo, repoErr := os.Stat(project.RepoPath)
	if repoErr != nil || !repoInfo.IsDir() {
		project.OrchestrationReady = false
		project.DispatchReady = false
		return
	}
	workflowInfo, workflowErr := os.Stat(project.WorkflowPath)
	project.OrchestrationReady = workflowErr == nil && !workflowInfo.IsDir()
	project.DispatchReady = project.OrchestrationReady
}

// Project operations

func (s *Store) CreateProject(name, description, repoPath, workflowPath string) (*Project, error) {
	return s.CreateProjectWithProvider(name, description, repoPath, workflowPath, ProviderKindKanban, "", nil)
}

func (s *Store) CreateProjectWithProvider(name, description, repoPath, workflowPath, providerKind, providerProjectRef string, providerConfig map[string]interface{}) (*Project, error) {
	now := time.Now()
	id := generateID("proj")
	repoPath, workflowPath, err := normalizeProjectPaths(repoPath, workflowPath)
	if err != nil {
		return nil, err
	}
	providerKind = normalizeProviderKind(providerKind)
	providerProjectRef = strings.TrimSpace(providerProjectRef)
	providerConfigJSON := encodeProviderConfig(providerConfig)
	permissionProfile := legacyWorkflowPermissionProfile(repoPath, workflowPath)

	_, err = s.db.Exec(`
		INSERT INTO projects (id, name, description, state, permission_profile, repo_path, workflow_path, provider_kind, provider_project_ref, provider_config_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, description, ProjectStateStopped, permissionProfile, repoPath, workflowPath, providerKind, providerProjectRef, providerConfigJSON, now, now,
	)
	if err != nil {
		return nil, err
	}
	project := &Project{
		ID:                 id,
		Name:               name,
		Description:        description,
		State:              ProjectStateStopped,
		PermissionProfile:  permissionProfile,
		RepoPath:           repoPath,
		WorkflowPath:       workflowPath,
		ProviderKind:       providerKind,
		ProviderProjectRef: providerProjectRef,
		ProviderConfig:     cloneProviderConfig(providerConfig),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	hydrateProject(project)
	if err := s.appendChange("project", id, "created", map[string]interface{}{"name": name, "repo_path": repoPath, "provider_kind": providerKind, "provider_project_ref": providerProjectRef}); err != nil {
		return nil, err
	}
	return project, nil
}

func (s *Store) backfillLegacyProjectPermissionProfiles() error {
	rows, err := s.db.Query(`SELECT id, repo_path, workflow_path, permission_profile FROM projects`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type candidate struct {
		id      string
		profile PermissionProfile
	}
	var updates []candidate
	for rows.Next() {
		var (
			id                string
			repoPath          string
			workflowPath      string
			permissionProfile string
		)
		if err := rows.Scan(&id, &repoPath, &workflowPath, &permissionProfile); err != nil {
			return err
		}
		if NormalizePermissionProfile(permissionProfile) != PermissionProfileDefault {
			continue
		}
		if profile := legacyWorkflowPermissionProfile(repoPath, workflowPath); profile != PermissionProfileDefault {
			updates = append(updates, candidate{id: id, profile: profile})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, update := range updates {
		if err := s.UpdateProjectPermissionProfile(update.id, update.profile); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetProject(id string) (*Project, error) {
	p := &Project{}
	var providerConfigJSON string
	err := s.db.QueryRow(`
		SELECT id, name, description, state, permission_profile, repo_path, workflow_path, provider_kind, provider_project_ref, provider_config_json, created_at, updated_at
		FROM projects WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.State, &p.PermissionProfile, &p.RepoPath, &p.WorkflowPath, &p.ProviderKind, &p.ProviderProjectRef, &providerConfigJSON, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.PermissionProfile = NormalizePermissionProfile(string(p.PermissionProfile))
	p.ProviderConfig = decodeProviderConfig(providerConfigJSON)
	hydrateProject(p)
	return p, nil
}

func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id, name, description, state, permission_profile, repo_path, workflow_path, provider_kind, provider_project_ref, provider_config_json, created_at, updated_at FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		p := Project{}
		var providerConfigJSON string
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.State, &p.PermissionProfile, &p.RepoPath, &p.WorkflowPath, &p.ProviderKind, &p.ProviderProjectRef, &providerConfigJSON, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.PermissionProfile = NormalizePermissionProfile(string(p.PermissionProfile))
		p.ProviderConfig = decodeProviderConfig(providerConfigJSON)
		hydrateProject(&p)
		projects = append(projects, p)
	}
	return projects, nil
}

func (s *Store) UpdateProject(id, name, description, repoPath, workflowPath string) error {
	return s.UpdateProjectWithProvider(id, name, description, repoPath, workflowPath, ProviderKindKanban, "", nil)
}

func (s *Store) UpdateProjectWithProvider(id, name, description, repoPath, workflowPath, providerKind, providerProjectRef string, providerConfig map[string]interface{}) error {
	current, err := s.GetProject(id)
	if err != nil {
		return err
	}
	repoPath, workflowPath, err = normalizeProjectPaths(repoPath, workflowPath)
	if err != nil {
		return err
	}
	providerKind = normalizeProviderKind(providerKind)
	providerProjectRef = strings.TrimSpace(providerProjectRef)
	permissionProfile := current.PermissionProfile
	if NormalizePermissionProfile(string(permissionProfile)) == PermissionProfileDefault {
		permissionProfile = legacyWorkflowPermissionProfile(repoPath, workflowPath)
	}
	res, err := s.db.Exec(`
		UPDATE projects SET name = ?, description = ?, permission_profile = ?, repo_path = ?, workflow_path = ?, provider_kind = ?, provider_project_ref = ?, provider_config_json = ?, updated_at = ?
		WHERE id = ?`,
		name, description, permissionProfile, repoPath, workflowPath, providerKind, providerProjectRef, encodeProviderConfig(providerConfig), time.Now(), id,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("project", id)
	}
	return s.appendChange("project", id, "updated", map[string]interface{}{"name": name, "repo_path": repoPath, "provider_kind": providerKind, "provider_project_ref": providerProjectRef})
}

func (s *Store) UpdateProjectPermissionProfile(id string, profile PermissionProfile) error {
	profile = NormalizePermissionProfile(string(profile))
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	res, err := tx.Exec(`
		UPDATE projects SET permission_profile = ?, updated_at = ?
		WHERE id = ?`,
		profile, now, id,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("project", id)
	}
	if profile != PermissionProfilePlanThenFullAccess {
		if _, err := tx.Exec(`
			UPDATE issues
			SET collaboration_mode_override = '',
			    plan_approval_pending = 0,
			    pending_plan_markdown = '',
			    pending_plan_requested_at = NULL,
			    pending_plan_revision_markdown = '',
			    pending_plan_revision_requested_at = NULL,
			    updated_at = ?
			WHERE project_id = ? AND permission_profile = ?`,
			now, id, PermissionProfileDefault,
		); err != nil {
			return err
		}
	}
	if err := s.appendChangeTx(tx, "project", id, "permission_profile_updated", map[string]interface{}{"permission_profile": profile}); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) UpdateIssuePermissionProfile(id string, profile PermissionProfile) error {
	profile = NormalizePermissionProfile(string(profile))
	now := time.Now().UTC()
	res, err := s.db.Exec(`
		UPDATE issues
		SET permission_profile = ?,
		    collaboration_mode_override = '',
		    plan_approval_pending = 0,
		    pending_plan_markdown = '',
		    pending_plan_requested_at = NULL,
		    pending_plan_revision_markdown = '',
		    pending_plan_revision_requested_at = NULL,
		    updated_at = ?
		WHERE id = ?`,
		profile, now, id,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue", id)
	}
	return s.appendChange("issue", id, "permission_profile_updated", map[string]interface{}{"permission_profile": profile})
}

func (s *Store) loadIssueRecordTx(tx *sql.Tx, id string) (*Issue, error) {
	record, err := scanIssueRecord(tx.QueryRow(`SELECT `+issueSelectColumns+` FROM issues WHERE id = ?`, id))
	switch {
	case err == sql.ErrNoRows:
		return nil, notFoundError("issue", id)
	case err != nil:
		return nil, err
	default:
		return record, nil
	}
}

func (s *Store) ensureLegacyIssuePlanSessionTx(tx *sql.Tx, issue *Issue, fallbackAt time.Time) (*issuePlanSessionRecord, bool, error) {
	if issue == nil || strings.TrimSpace(issue.ID) == "" || strings.TrimSpace(issue.PendingPlanMarkdown) == "" {
		return nil, false, nil
	}
	existing, err := s.getLatestIssuePlanSessionTx(tx, issue.ID, true)
	if err != nil {
		return nil, false, err
	}
	if existing != nil && existing.ClosedAt == nil {
		return existing, false, nil
	}

	openedAt := fallbackAt.UTC()
	if issue.PendingPlanRequestedAt != nil && !issue.PendingPlanRequestedAt.IsZero() {
		openedAt = issue.PendingPlanRequestedAt.UTC()
	}
	status := IssuePlanningStatusAwaitingApproval
	if strings.TrimSpace(issue.PendingPlanRevisionMarkdown) != "" && issue.PendingPlanRevisionRequestedAt != nil {
		status = IssuePlanningStatusRevisionRequested
	}
	record := issuePlanSessionRecord{
		ID:                   generateID("pls"),
		IssueID:              issue.ID,
		Status:               status,
		CurrentVersionNumber: 1,
		PendingRevisionNote:  strings.TrimSpace(issue.PendingPlanRevisionMarkdown),
		OpenedAt:             openedAt,
		UpdatedAt:            openedAt,
	}
	if issue.PendingPlanRevisionRequestedAt != nil && !issue.PendingPlanRevisionRequestedAt.IsZero() {
		revisionRequestedAt := issue.PendingPlanRevisionRequestedAt.UTC()
		record.PendingRevisionRequestedAt = &revisionRequestedAt
		record.UpdatedAt = revisionRequestedAt
	}
	if err := s.insertIssuePlanSessionTx(tx, record); err != nil {
		return nil, false, err
	}
	if err := s.insertIssuePlanVersionTx(tx, IssuePlanVersion{
		ID:            generateID("plv"),
		SessionID:     record.ID,
		VersionNumber: 1,
		Markdown:      issue.PendingPlanMarkdown,
		CreatedAt:     openedAt,
	}); err != nil {
		return nil, false, err
	}
	return &record, true, nil
}

func (s *Store) closeIssuePlanSessionTx(tx *sql.Tx, issue *Issue, status IssuePlanningStatus, closedAt time.Time, reason string) (*issuePlanSessionRecord, error) {
	if issue == nil {
		return nil, nil
	}
	record, _, err := s.ensureLegacyIssuePlanSessionTx(tx, issue, closedAt)
	if err != nil || record == nil {
		return record, err
	}
	closedAt = closedAt.UTC()
	if _, err := tx.Exec(`
		UPDATE issue_plan_sessions
		SET status = ?,
		    pending_revision_note = '',
		    pending_revision_requested_at = NULL,
		    updated_at = ?,
		    closed_at = ?,
		    closed_reason = ?
		WHERE id = ?`,
		status,
		closedAt,
		closedAt,
		strings.TrimSpace(reason),
		record.ID,
	); err != nil {
		return nil, err
	}
	record.Status = status
	record.PendingRevisionNote = ""
	record.PendingRevisionRequestedAt = nil
	record.UpdatedAt = closedAt
	record.ClosedAt = &closedAt
	record.ClosedReason = strings.TrimSpace(reason)
	return record, nil
}

func (s *Store) SetIssuePendingPlanApproval(id, markdown string, requestedAt time.Time) error {
	if strings.TrimSpace(id) == "" {
		return validationErrorf("issue id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	issue, err := s.loadIssueRecordTx(tx, id)
	if err != nil {
		return err
	}
	if err := s.SetIssuePendingPlanApprovalWithContextTx(tx, issue, markdown, requestedAt, 0, "", ""); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) SetIssuePendingPlanApprovalWithContext(issue *Issue, markdown string, requestedAt time.Time, attempt int, threadID, turnID string) error {
	if issue == nil || strings.TrimSpace(issue.ID) == "" {
		return validationErrorf("issue is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	loaded, err := s.loadIssueRecordTx(tx, issue.ID)
	if err != nil {
		return err
	}
	if err := s.SetIssuePendingPlanApprovalWithContextTx(tx, loaded, markdown, requestedAt, attempt, threadID, turnID); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) SetIssuePendingPlanApprovalWithContextTx(tx *sql.Tx, issue *Issue, markdown string, requestedAt time.Time, attempt int, threadID, turnID string) error {
	if issue == nil || strings.TrimSpace(issue.ID) == "" {
		return validationErrorf("issue is required")
	}
	if strings.TrimSpace(markdown) == "" {
		return validationErrorf("pending plan markdown is required")
	}
	requestedAt = requestedAt.UTC()
	res, err := tx.Exec(`
		UPDATE issues
		SET collaboration_mode_override = '',
		    plan_approval_pending = 1,
		    pending_plan_markdown = ?,
		    pending_plan_requested_at = ?,
		    pending_plan_revision_markdown = '',
		    pending_plan_revision_requested_at = NULL,
		    updated_at = ?
		WHERE id = ?`,
		markdown, requestedAt, requestedAt, issue.ID,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue", issue.ID)
	}

	existing, err := s.getLatestIssuePlanSessionTx(tx, issue.ID, true)
	if err != nil {
		return err
	}
	sessionStarted := existing == nil || existing.ClosedAt != nil
	versionNumber := 1
	revisionNote := ""
	sessionID := generateID("pls")
	if sessionStarted {
		record := issuePlanSessionRecord{
			ID:                   sessionID,
			IssueID:              issue.ID,
			Status:               IssuePlanningStatusAwaitingApproval,
			OriginAttempt:        attempt,
			OriginThreadID:       strings.TrimSpace(threadID),
			CurrentVersionNumber: versionNumber,
			OpenedAt:             requestedAt,
			UpdatedAt:            requestedAt,
		}
		if err := s.insertIssuePlanSessionTx(tx, record); err != nil {
			return err
		}
	} else {
		sessionID = existing.ID
		versionNumber = existing.CurrentVersionNumber + 1
		revisionNote = strings.TrimSpace(existing.PendingRevisionNote)
		if _, err := tx.Exec(`
			UPDATE issue_plan_sessions
			SET status = ?,
			    current_version_number = ?,
			    pending_revision_note = '',
			    pending_revision_requested_at = NULL,
			    updated_at = ?,
			    closed_at = NULL,
			    closed_reason = ''
			WHERE id = ?`,
			IssuePlanningStatusAwaitingApproval,
			versionNumber,
			requestedAt,
			existing.ID,
		); err != nil {
			return err
		}
	}

	version := IssuePlanVersion{
		ID:            generateID("plv"),
		SessionID:     sessionID,
		VersionNumber: versionNumber,
		Markdown:      markdown,
		RevisionNote:  revisionNote,
		Attempt:       attempt,
		ThreadID:      strings.TrimSpace(threadID),
		TurnID:        strings.TrimSpace(turnID),
		CreatedAt:     requestedAt,
	}
	if err := s.insertIssuePlanVersionTx(tx, version); err != nil {
		return err
	}

	if err := s.appendChangeTx(tx, "issue", issue.ID, "plan_approval_requested", map[string]interface{}{
		"requested_at":    requestedAt.Format(time.RFC3339),
		"markdown":        markdown,
		"session_id":      sessionID,
		"version_number":  versionNumber,
		"session_started": sessionStarted,
		"revision_note":   revisionNote,
	}); err != nil {
		return err
	}
	if sessionStarted {
		if err := s.applyIssueActivityEventTx(tx, issue.ID, issue.Identifier, attempt, agentruntime.ActivityEvent{
			Type:     "plan.sessionStarted",
			ThreadID: strings.TrimSpace(threadID),
			TurnID:   strings.TrimSpace(turnID),
			Raw: map[string]interface{}{
				"session_id": sessionID,
				"opened_at":  requestedAt.Format(time.RFC3339),
			},
		}); err != nil {
			return err
		}
	}
	return s.applyIssueActivityEventTx(tx, issue.ID, issue.Identifier, attempt, agentruntime.ActivityEvent{
		Type:     "plan.versionPublished",
		ThreadID: strings.TrimSpace(threadID),
		TurnID:   strings.TrimSpace(turnID),
		Raw: map[string]interface{}{
			"session_id":     sessionID,
			"version_number": versionNumber,
			"markdown":       markdown,
			"revision_note":  revisionNote,
			"requested_at":   requestedAt.Format(time.RFC3339),
		},
	})
}

func (s *Store) ApproveIssuePlan(id string) error {
	if strings.TrimSpace(id) == "" {
		return validationErrorf("issue id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	issue, err := s.loadIssueRecordTx(tx, id)
	if err != nil {
		return err
	}
	if err := s.approveIssuePlanTx(tx, issue, time.Now().UTC(), false); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) approveIssuePlanTx(tx *sql.Tx, issue *Issue, approvedAt time.Time, projectActivity bool) error {
	if issue == nil || strings.TrimSpace(issue.ID) == "" {
		return validationErrorf("issue is required")
	}
	approvedAt = approvedAt.UTC()
	res, err := tx.Exec(`
		UPDATE issues
		SET permission_profile = ?,
		    collaboration_mode_override = ?,
		    plan_approval_pending = 0,
		    pending_plan_markdown = '',
		    pending_plan_requested_at = NULL,
		    pending_plan_revision_markdown = '',
		    pending_plan_revision_requested_at = NULL,
		    updated_at = ?
		WHERE id = ?`,
		PermissionProfileFullAccess, CollaborationModeOverrideDefault, approvedAt, issue.ID,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue", issue.ID)
	}
	session, err := s.closeIssuePlanSessionTx(tx, issue, IssuePlanningStatusApproved, approvedAt, "")
	if err != nil {
		return err
	}
	fields := map[string]interface{}{
		"permission_profile":          PermissionProfileFullAccess,
		"collaboration_mode_override": CollaborationModeOverrideDefault,
	}
	if session != nil {
		fields["session_id"] = session.ID
		fields["closed_at"] = approvedAt.Format(time.RFC3339)
	}
	if err := s.appendChangeTx(tx, "issue", issue.ID, "plan_approved", fields); err != nil {
		return err
	}
	if projectActivity {
		sessionID := ""
		if session != nil {
			sessionID = session.ID
		}
		return s.applyIssueActivityEventTx(tx, issue.ID, issue.Identifier, 0, agentruntime.ActivityEvent{
			Type: "plan.approved",
			Raw: map[string]interface{}{
				"approved_at": approvedAt.Format(time.RFC3339),
				"session_id":  sessionID,
			},
		})
	}
	return nil
}

func (s *Store) ApproveIssuePlanWithNote(issue *Issue, approvedAt time.Time, note string, noteStatus IssueAgentCommandStatus) (*IssueAgentCommand, error) {
	if issue == nil || strings.TrimSpace(issue.ID) == "" {
		return nil, validationErrorf("issue is required")
	}

	approvedAt = approvedAt.UTC()
	note = strings.TrimSpace(note)
	if note != "" && noteStatus == "" {
		noteStatus = IssueAgentCommandPending
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	loaded, err := s.loadIssueRecordTx(tx, issue.ID)
	if err != nil {
		return nil, err
	}
	if err := s.approveIssuePlanTx(tx, loaded, approvedAt, true); err != nil {
		return nil, err
	}
	if err := s.appendRuntimeEventTx(tx, "plan_approved", map[string]interface{}{
		"issue_id":    loaded.ID,
		"identifier":  loaded.Identifier,
		"phase":       string(loaded.WorkflowPhase),
		"approved_at": approvedAt.Format(time.RFC3339),
	}); err != nil {
		return nil, err
	}

	var commandRecord *IssueAgentCommand
	if note != "" {
		commandRecord, err = s.createIssueAgentCommandTx(tx, loaded.ID, note, noteStatus)
		if err != nil {
			return nil, err
		}
		if err := s.appendRuntimeEventTx(tx, "manual_command_submitted", map[string]interface{}{
			"issue_id":   loaded.ID,
			"identifier": loaded.Identifier,
			"phase":      string(loaded.WorkflowPhase),
			"command_id": commandRecord.ID,
			"command":    commandRecord.Command,
		}); err != nil {
			return nil, err
		}
	}

	if err := s.commitTx(tx, true); err != nil {
		return nil, err
	}
	tx = nil
	return commandRecord, nil
}

func (s *Store) ClearIssuePendingPlanApproval(id string, reason string) error {
	if strings.TrimSpace(id) == "" {
		return validationErrorf("issue id is required")
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	issue, err := s.loadIssueRecordTx(tx, id)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`
		UPDATE issues
		SET plan_approval_pending = 0,
		    pending_plan_markdown = '',
		    pending_plan_requested_at = NULL,
		    pending_plan_revision_markdown = '',
		    pending_plan_revision_requested_at = NULL,
		    updated_at = ?
		WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue", id)
	}
	session, err := s.closeIssuePlanSessionTx(tx, issue, IssuePlanningStatusAbandoned, now, strings.TrimSpace(reason))
	if err != nil {
		return err
	}
	fields := map[string]interface{}{
		"cleared_at": now.Format(time.RFC3339),
	}
	if strings.TrimSpace(reason) != "" {
		fields["reason"] = strings.TrimSpace(reason)
	}
	if session != nil {
		fields["session_id"] = session.ID
	}
	if err := s.appendChangeTx(tx, "issue", id, "plan_approval_cleared", fields); err != nil {
		return err
	}
	if session != nil {
		if err := s.applyIssueActivityEventTx(tx, issue.ID, issue.Identifier, session.OriginAttempt, agentruntime.ActivityEvent{
			Type: "plan.abandoned",
			Raw: map[string]interface{}{
				"session_id":   session.ID,
				"reason":       strings.TrimSpace(reason),
				"abandoned_at": now.Format(time.RFC3339),
			},
		}); err != nil {
			return err
		}
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) SetIssuePendingPlanRevision(id, markdown string, requestedAt time.Time) error {
	if strings.TrimSpace(id) == "" {
		return validationErrorf("issue id is required")
	}
	if strings.TrimSpace(markdown) == "" {
		return validationErrorf("pending plan revision markdown is required")
	}
	requestedAt = requestedAt.UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	issue, err := s.loadIssueRecordTx(tx, id)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`
		UPDATE issues
		SET pending_plan_revision_markdown = ?,
		    pending_plan_revision_requested_at = ?,
		    updated_at = ?
		WHERE id = ?`,
		markdown, requestedAt, requestedAt, id,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue", id)
	}
	session, _, err := s.ensureLegacyIssuePlanSessionTx(tx, issue, requestedAt)
	if err != nil {
		return err
	}
	if session != nil {
		if _, err := tx.Exec(`
			UPDATE issue_plan_sessions
			SET status = ?,
			    pending_revision_note = ?,
			    pending_revision_requested_at = ?,
			    updated_at = ?
			WHERE id = ?`,
			IssuePlanningStatusRevisionRequested,
			markdown,
			requestedAt,
			requestedAt,
			session.ID,
		); err != nil {
			return err
		}
		session.Status = IssuePlanningStatusRevisionRequested
		session.PendingRevisionNote = markdown
		session.PendingRevisionRequestedAt = &requestedAt
		session.UpdatedAt = requestedAt
	}
	fields := map[string]interface{}{
		"requested_at": requestedAt.Format(time.RFC3339),
		"markdown":     markdown,
	}
	if session != nil {
		fields["session_id"] = session.ID
	}
	if err := s.appendChangeTx(tx, "issue", id, "plan_revision_requested", fields); err != nil {
		return err
	}
	if session != nil {
		if err := s.applyIssueActivityEventTx(tx, issue.ID, issue.Identifier, session.OriginAttempt, agentruntime.ActivityEvent{
			Type: "plan.revisionRequested",
			Raw: map[string]interface{}{
				"session_id":    session.ID,
				"requested_at":  requestedAt.Format(time.RFC3339),
				"revision_note": markdown,
			},
		}); err != nil {
			return err
		}
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) ClearIssuePendingPlanRevision(id, reason string) error {
	if strings.TrimSpace(id) == "" {
		return validationErrorf("issue id is required")
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	issue, err := s.loadIssueRecordTx(tx, id)
	if err != nil {
		return err
	}
	session, _, err := s.ensureLegacyIssuePlanSessionTx(tx, issue, now)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`
		UPDATE issues
		SET pending_plan_revision_markdown = '',
		    pending_plan_revision_requested_at = NULL,
		    updated_at = ?
		WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue", id)
	}
	fields := map[string]interface{}{
		"cleared_at": now.Format(time.RFC3339),
	}
	if strings.TrimSpace(reason) != "" {
		fields["reason"] = strings.TrimSpace(reason)
	}
	if session != nil {
		fields["session_id"] = session.ID
		nextStatus := IssuePlanningStatusAwaitingApproval
		if strings.EqualFold(strings.TrimSpace(reason), "turn_started") {
			nextStatus = IssuePlanningStatusDrafting
		}
		clearPending := !strings.EqualFold(strings.TrimSpace(reason), "turn_started")
		if _, err := tx.Exec(`
			UPDATE issue_plan_sessions
			SET status = ?,
			    pending_revision_note = CASE WHEN ? THEN '' ELSE pending_revision_note END,
			    pending_revision_requested_at = CASE WHEN ? THEN NULL ELSE pending_revision_requested_at END,
			    updated_at = ?
			WHERE id = ?`,
			nextStatus,
			clearPending,
			clearPending,
			now,
			session.ID,
		); err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(reason), "turn_started") {
			if err := s.applyIssueActivityEventTx(tx, issue.ID, issue.Identifier, session.OriginAttempt, agentruntime.ActivityEvent{
				Type: "plan.revisionApplied",
				Raw: map[string]interface{}{
					"session_id":    session.ID,
					"cleared_at":    now.Format(time.RFC3339),
					"revision_note": session.PendingRevisionNote,
					"requested_at": func() string {
						if session.PendingRevisionRequestedAt == nil {
							return ""
						}
						return session.PendingRevisionRequestedAt.UTC().Format(time.RFC3339)
					}(),
				},
			}); err != nil {
				return err
			}
		}
	}
	if err := s.appendChangeTx(tx, "issue", id, "plan_revision_cleared", fields); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}
func (s *Store) UpdateProjectState(id string, state ProjectState) error {
	state = NormalizeProjectState(string(state))
	res, err := s.db.Exec(`
		UPDATE projects SET state = ?, updated_at = ?
		WHERE id = ?`,
		state, time.Now().UTC(), id,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("project", id)
	}
	return s.appendChange("project", id, "state_changed", map[string]interface{}{"state": state})
}

func (s *Store) DeleteProject(id string) error {
	issues, err := s.ListIssues(map[string]interface{}{"project_id": id})
	if err != nil {
		return err
	}
	for i := range issues {
		if err := s.DeleteIssue(issues[i].ID); err != nil && !IsNotFound(err) {
			return err
		}
	}
	epics, err := s.ListEpics(id)
	if err != nil {
		return err
	}
	for i := range epics {
		if err := s.DeleteEpic(epics[i].ID); err != nil && !IsNotFound(err) {
			return err
		}
	}
	res, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("project", id)
	}
	return s.appendChange("project", id, "deleted", nil)
}

// Epic operations

func (s *Store) CreateEpic(projectID, name, description string) (*Epic, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		if _, err := s.GetProject(projectID); err != nil {
			if err == sql.ErrNoRows {
				return nil, notFoundError("project", projectID)
			}
			return nil, err
		}
	}
	now := time.Now()
	id := generateID("epic")

	_, err := s.db.Exec(`
		INSERT INTO epics (id, project_id, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, nullableStringValue(projectID), name, description, now, now,
	)
	if err != nil {
		return nil, err
	}
	if err := s.appendChange("epic", id, "created", map[string]interface{}{"project_id": projectID, "name": name}); err != nil {
		return nil, err
	}
	return &Epic{ID: id, ProjectID: projectID, Name: name, Description: description, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) ListEpics(projectID string) ([]Epic, error) {
	query := `SELECT id, project_id, name, description, created_at, updated_at FROM epics`
	args := []interface{}{}

	if projectID != "" {
		query += " WHERE project_id = ?"
		args = append(args, projectID)
	}
	query += " ORDER BY name"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var epics []Epic
	for rows.Next() {
		e := Epic{}
		var projectID sql.NullString
		if err := rows.Scan(&e.ID, &projectID, &e.Name, &e.Description, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.ProjectID = stringFromNull(projectID)
		epics = append(epics, e)
	}
	return epics, nil
}

func (s *Store) GetEpic(id string) (*Epic, error) {
	e := &Epic{}
	var projectID sql.NullString
	err := s.db.QueryRow(`
		SELECT id, project_id, name, description, created_at, updated_at
		FROM epics WHERE id = ?`, id,
	).Scan(&e.ID, &projectID, &e.Name, &e.Description, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	e.ProjectID = stringFromNull(projectID)
	return e, nil
}

func (s *Store) UpdateEpic(id, projectID, name, description string) error {
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		if _, err := s.GetProject(projectID); err != nil {
			if err == sql.ErrNoRows {
				return notFoundError("project", projectID)
			}
			return err
		}
	}
	res, err := s.db.Exec(`
		UPDATE epics SET project_id = ?, name = ?, description = ?, updated_at = ?
		WHERE id = ?`,
		nullableStringValue(projectID), name, description, time.Now(), id,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("epic", id)
	}
	return s.appendChange("epic", id, "updated", map[string]interface{}{"project_id": projectID, "name": name})
}

func (s *Store) DeleteEpic(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`UPDATE issues SET epic_id = NULL, updated_at = ? WHERE epic_id = ?`, time.Now(), id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM epics WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("epic", id)
	}
	if err := s.appendChangeTx(tx, "epic", id, "deleted", nil); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

// Issue operations

func (s *Store) identifierPrefix(projectID string) (string, error) {
	var prefix string
	if projectID != "" {
		p, err := s.GetProject(projectID)
		if err != nil {
			return "", err
		}
		if p != nil {
			prefix = identifierPrefixFromProjectName(p.Name)
		}
	}
	if prefix == "" {
		prefix = "ISS"
	}
	return prefix, nil
}

func identifierPrefixFromProjectName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToUpper(r)
	}, strings.TrimSpace(name))
	if cleaned == "" {
		return ""
	}
	runes := []rune(cleaned)
	if len(runes) > 4 {
		runes = runes[:4]
	}
	return string(runes)
}

func generateIdentifierTx(tx *sql.Tx, prefix string) (string, error) {
	if _, err := tx.Exec(`INSERT INTO counters (name, value) VALUES (?, 1) ON CONFLICT(name) DO UPDATE SET value = value + 1`, prefix); err != nil {
		return "", err
	}

	var counter int
	if err := tx.QueryRow(`SELECT value FROM counters WHERE name = ?`, prefix).Scan(&counter); err != nil {
		return "", err
	}

	return fmt.Sprintf("%s-%d", prefix, counter), nil
}

func (s *Store) generateIdentifier(projectID string) (string, error) {
	prefix, err := s.identifierPrefix(projectID)
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	identifier, err := generateIdentifierTx(tx, prefix)
	if err != nil {
		return "", err
	}
	if err := s.commitTx(tx, false); err != nil {
		return "", err
	}
	tx = nil
	return identifier, nil
}

func (s *Store) resolveIssueAssociations(projectID, epicID string) (string, string, error) {
	projectID = strings.TrimSpace(projectID)
	epicID = strings.TrimSpace(epicID)

	if epicID != "" {
		epic, err := s.GetEpic(epicID)
		if err != nil {
			if err == sql.ErrNoRows {
				return "", "", notFoundError("epic", epicID)
			}
			return "", "", err
		}
		if projectID == "" {
			projectID = epic.ProjectID
		} else if projectID != epic.ProjectID {
			return "", "", validationErrorf("epic %s belongs to project %s, not %s", epicID, epic.ProjectID, projectID)
		}
	}

	if projectID != "" {
		if _, err := s.GetProject(projectID); err != nil {
			if err == sql.ErrNoRows {
				return "", "", notFoundError("project", projectID)
			}
			return "", "", err
		}
	}

	return projectID, epicID, nil
}

func (s *Store) resolveIssueUpdateAssociations(issue *Issue, updates map[string]interface{}) error {
	if issue == nil {
		return validationErrorf("issue is required")
	}

	projectID := issue.ProjectID
	epicID := issue.EpicID
	projectSpecified := false
	epicSpecified := false

	if raw, ok := updates["project_id"]; ok {
		projectSpecified = true
		projectID, _ = raw.(string)
		projectID = strings.TrimSpace(projectID)
	}
	if raw, ok := updates["epic_id"]; ok {
		epicSpecified = true
		epicID, _ = raw.(string)
		epicID = strings.TrimSpace(epicID)
	}

	if projectSpecified && projectID == "" && !epicSpecified {
		epicID = ""
		epicSpecified = true
	}
	if projectSpecified && projectID == "" && epicSpecified && epicID != "" {
		return validationErrorf("cannot set epic without a project")
	}

	resolvedProjectID, resolvedEpicID, err := s.resolveIssueAssociations(projectID, epicID)
	if err != nil {
		return err
	}
	if projectSpecified || resolvedProjectID != issue.ProjectID || (projectSpecified && resolvedProjectID == "") {
		updates["project_id"] = resolvedProjectID
	}
	if epicSpecified || resolvedEpicID != issue.EpicID || (projectSpecified && resolvedProjectID == "") {
		updates["epic_id"] = resolvedEpicID
	}
	return nil
}

func (s *Store) CreateIssue(projectID, epicID, title, description string, priority int, labels []string) (*Issue, error) {
	return s.CreateIssueWithOptions(projectID, epicID, title, description, priority, labels, IssueCreateOptions{})
}

func (s *Store) CreateIssueWithOptions(projectID, epicID, title, description string, priority int, labels []string, opts IssueCreateOptions) (*Issue, error) {
	projectID, epicID, err := s.resolveIssueAssociations(projectID, epicID)
	if err != nil {
		return nil, err
	}
	if _, err := s.defaultPermissionProfileForProjectID(projectID); err != nil {
		return nil, err
	}
	issueType, err := ParseIssueType(string(opts.IssueType))
	if err != nil {
		return nil, err
	}
	prefix, err := s.identifierPrefix(projectID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	id := generateID("iss")
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	identifier, err := generateIdentifierTx(tx, prefix)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(`
			INSERT INTO issues (id, project_id, epic_id, identifier, issue_type, provider_kind, provider_issue_ref, provider_shadow, title, description, state, workflow_phase, permission_profile, priority, agent_name, agent_prompt, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, nullableStringValue(projectID), nullableStringValue(epicID), identifier, issueType, ProviderKindKanban, "", 0, title, description, StateBacklog, WorkflowPhaseImplementation, PermissionProfileDefault, priority, strings.TrimSpace(opts.AgentName), strings.TrimSpace(opts.AgentPrompt), now, now,
	)
	if err != nil {
		return nil, err
	}
	if issueType == IssueTypeRecurring {
		recurrence, err := buildIssueRecurrence(id, opts.Cron, defaultRecurringEnabled(opts.Enabled), nil, nil, now)
		if err != nil {
			return nil, err
		}
		if recurrence.Enabled {
			nextRunAt, err := NextRecurringRun(recurrence.Cron, now, time.Local)
			if err != nil {
				return nil, err
			}
			recurrence.NextRunAt = &nextRunAt
		}
		if err := saveIssueRecurrenceTx(tx, recurrence); err != nil {
			return nil, err
		}
	}

	// Insert labels
	for _, label := range labels {
		_, _ = tx.Exec(`INSERT OR IGNORE INTO issue_labels (issue_id, label) VALUES (?, ?)`, id, label)
	}
	payload := map[string]interface{}{
		"project_id":         projectID,
		"identifier":         identifier,
		"title":              title,
		"issue_type":         issueType,
		"permission_profile": PermissionProfileDefault,
	}
	if agentName := strings.TrimSpace(opts.AgentName); agentName != "" {
		payload["agent_name"] = agentName
	}
	if agentPrompt := strings.TrimSpace(opts.AgentPrompt); agentPrompt != "" {
		payload["agent_prompt"] = agentPrompt
	}
	if issueType == IssueTypeRecurring {
		payload["cron"] = normalizeCronSpec(opts.Cron)
		payload["enabled"] = defaultRecurringEnabled(opts.Enabled)
	}
	if err := s.appendChangeTx(tx, "issue", id, "created", payload); err != nil {
		return nil, err
	}
	if err := s.commitTx(tx, true); err != nil {
		return nil, err
	}
	tx = nil

	return s.GetIssue(id)
}

func (s *Store) defaultPermissionProfileForProjectID(projectID string) (PermissionProfile, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return PermissionProfileDefault, nil
	}
	project, err := s.GetProject(projectID)
	if err != nil {
		return "", err
	}
	return NormalizePermissionProfile(string(project.PermissionProfile)), nil
}

type issueScanner interface {
	Scan(dest ...interface{}) error
}

func scanIssueRecord(scanner issueScanner) (*Issue, error) {
	issue := &Issue{}
	var startedAt, completedAt, lastSyncedAt, pendingPlanRequestedAt, pendingPlanRevisionRequestedAt sql.NullTime
	var projectID, epicID, branchName, prURL, providerIssueRef sql.NullString
	var providerShadow, planApprovalPending int
	var permissionProfile, collaborationModeOverride string

	if err := scanner.Scan(
		&issue.ID, &projectID, &epicID, &issue.Identifier, &issue.IssueType, &issue.ProviderKind, &providerIssueRef, &providerShadow, &issue.Title, &issue.Description, &issue.State, &issue.WorkflowPhase, &permissionProfile, &collaborationModeOverride, &planApprovalPending, &issue.PendingPlanMarkdown, &pendingPlanRequestedAt, &issue.PendingPlanRevisionMarkdown, &pendingPlanRevisionRequestedAt, &issue.Priority,
		&issue.AgentName, &issue.AgentPrompt, &branchName, &prURL, &issue.CreatedAt, &issue.UpdatedAt, &issue.TotalTokensSpent, &startedAt, &completedAt, &lastSyncedAt,
	); err != nil {
		return nil, err
	}

	issue.IssueType = NormalizeIssueType(string(issue.IssueType))
	issue.PermissionProfile = NormalizePermissionProfile(permissionProfile)
	issue.CollaborationModeOverride = NormalizeCollaborationModeOverride(collaborationModeOverride)
	issue.PlanApprovalPending = planApprovalPending != 0
	if !issue.WorkflowPhase.IsValid() {
		issue.WorkflowPhase = DefaultWorkflowPhaseForState(issue.State)
	}
	issue.ProviderKind = normalizeProviderKind(issue.ProviderKind)
	if projectID.Valid {
		issue.ProjectID = projectID.String
	}
	if epicID.Valid {
		issue.EpicID = epicID.String
	}
	if providerIssueRef.Valid {
		issue.ProviderIssueRef = providerIssueRef.String
	}
	issue.ProviderShadow = providerShadow != 0
	if branchName.Valid {
		issue.BranchName = branchName.String
	}
	if prURL.Valid {
		issue.PRURL = prURL.String
	}
	if startedAt.Valid {
		issue.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		issue.CompletedAt = &completedAt.Time
	}
	if lastSyncedAt.Valid {
		issue.LastSyncedAt = &lastSyncedAt.Time
	}
	if pendingPlanRequestedAt.Valid {
		issue.PendingPlanRequestedAt = &pendingPlanRequestedAt.Time
	}
	if pendingPlanRevisionRequestedAt.Valid {
		issue.PendingPlanRevisionRequestedAt = &pendingPlanRevisionRequestedAt.Time
	}
	return issue, nil
}

func normalizeProviderIncomingIssue(incoming *Issue) (WorkflowPhase, time.Time, time.Time, time.Time) {
	now := time.Now().UTC()
	workflowPhase := incoming.WorkflowPhase
	if !workflowPhase.IsValid() {
		workflowPhase = DefaultWorkflowPhaseForState(incoming.State)
	}
	updatedAt := incoming.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	createdAt := incoming.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	lastSyncedAt := now
	if incoming.LastSyncedAt != nil {
		lastSyncedAt = incoming.LastSyncedAt.UTC()
	}
	return workflowPhase, updatedAt, createdAt, lastSyncedAt
}

func (s *Store) listDispatchIssuesForQuery(query string, args ...interface{}) ([]DispatchIssue, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]DispatchIssue, 0)
	for rows.Next() {
		record, err := scanDispatchIssueRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	issueIDs := make([]string, 0, len(out))
	for i := range out {
		if strings.TrimSpace(out[i].ID) == "" {
			continue
		}
		issueIDs = append(issueIDs, out[i].ID)
	}
	labelMap, blockerMap, _, err := s.issueRelations(issueIDs)
	if err != nil {
		return nil, err
	}
	recurrenceMap, err := s.issueRecurrenceMap(issueIDs)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Labels = labelMap[out[i].ID]
		out[i].BlockedBy = blockerMap[out[i].ID]
		if recurrence, ok := recurrenceMap[out[i].ID]; ok {
			applyRecurrenceToIssue(&out[i].Issue, &recurrence)
		}
	}
	return out, nil
}

func scanDispatchIssueRow(rows *sql.Rows) (*DispatchIssue, error) {
	issue := &Issue{}
	var startedAt, completedAt, lastSyncedAt, pendingPlanRequestedAt, pendingPlanRevisionRequestedAt sql.NullTime
	var projectID, epicID, branchName, prURL, providerIssueRef sql.NullString
	var providerShadow, planApprovalPending int
	var permissionProfile, collaborationModeOverride string
	var projectExists int
	var rawProjectState string
	var unresolved int

	if err := rows.Scan(
		&issue.ID, &projectID, &epicID, &issue.Identifier, &issue.IssueType, &issue.ProviderKind, &providerIssueRef, &providerShadow, &issue.Title, &issue.Description, &issue.State, &issue.WorkflowPhase, &permissionProfile, &collaborationModeOverride, &planApprovalPending, &issue.PendingPlanMarkdown, &pendingPlanRequestedAt, &issue.PendingPlanRevisionMarkdown, &pendingPlanRevisionRequestedAt, &issue.Priority,
		&issue.AgentName, &issue.AgentPrompt, &branchName, &prURL, &issue.CreatedAt, &issue.UpdatedAt, &issue.TotalTokensSpent, &startedAt, &completedAt, &lastSyncedAt,
		&projectExists, &rawProjectState, &unresolved,
	); err != nil {
		return nil, err
	}

	issue.IssueType = NormalizeIssueType(string(issue.IssueType))
	issue.PermissionProfile = NormalizePermissionProfile(permissionProfile)
	issue.CollaborationModeOverride = NormalizeCollaborationModeOverride(collaborationModeOverride)
	issue.PlanApprovalPending = planApprovalPending != 0
	if !issue.WorkflowPhase.IsValid() {
		issue.WorkflowPhase = DefaultWorkflowPhaseForState(issue.State)
	}
	issue.ProviderKind = normalizeProviderKind(issue.ProviderKind)
	if projectID.Valid {
		issue.ProjectID = projectID.String
	}
	if epicID.Valid {
		issue.EpicID = epicID.String
	}
	if providerIssueRef.Valid {
		issue.ProviderIssueRef = providerIssueRef.String
	}
	issue.ProviderShadow = providerShadow != 0
	if branchName.Valid {
		issue.BranchName = branchName.String
	}
	if prURL.Valid {
		issue.PRURL = prURL.String
	}
	if startedAt.Valid {
		issue.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		issue.CompletedAt = &completedAt.Time
	}
	if lastSyncedAt.Valid {
		issue.LastSyncedAt = &lastSyncedAt.Time
	}
	if pendingPlanRequestedAt.Valid {
		issue.PendingPlanRequestedAt = &pendingPlanRequestedAt.Time
	}
	if pendingPlanRevisionRequestedAt.Valid {
		issue.PendingPlanRevisionRequestedAt = &pendingPlanRevisionRequestedAt.Time
	}

	return &DispatchIssue{
		Issue: *issue,
		DispatchState: IssueDispatchState{
			ProjectExists:         projectExists != 0,
			ProjectState:          NormalizeProjectState(rawProjectState),
			HasUnresolvedBlockers: unresolved != 0,
		},
	}, nil
}

func (s *Store) loadIssuesByIDs(issueIDs []string) ([]Issue, error) {
	order := make([]string, 0, len(issueIDs))
	seen := make(map[string]struct{}, len(issueIDs))
	args := make([]interface{}, 0, len(issueIDs))
	for _, issueID := range issueIDs {
		issueID = strings.TrimSpace(issueID)
		if issueID == "" {
			continue
		}
		if _, ok := seen[issueID]; ok {
			continue
		}
		seen[issueID] = struct{}{}
		order = append(order, issueID)
		args = append(args, issueID)
	}
	if len(order) == 0 {
		return []Issue{}, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(order)), ",")
	rows, err := s.db.Query(`
			SELECT `+issueSelectColumns+`
			FROM issues
		WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	issuesByID := make(map[string]*Issue, len(order))
	for rows.Next() {
		issue, err := scanIssueRecord(rows)
		if err != nil {
			return nil, err
		}
		issuesByID[issue.ID] = issue
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	labelMap, blockerMap, _, err := s.issueRelations(order)
	if err != nil {
		return nil, err
	}
	recurrenceMap, err := s.issueRecurrenceMap(order)
	if err != nil {
		return nil, err
	}

	out := make([]Issue, 0, len(order))
	for _, issueID := range order {
		issue, ok := issuesByID[issueID]
		if !ok {
			return nil, sql.ErrNoRows
		}
		issue.Labels = labelMap[issueID]
		issue.BlockedBy = blockerMap[issueID]
		if recurrence, ok := recurrenceMap[issueID]; ok {
			applyRecurrenceToIssue(issue, &recurrence)
		}
		out = append(out, *issue)
	}
	return out, nil
}

func (s *Store) GetIssue(id string) (*Issue, error) {
	issues, err := s.loadIssuesByIDs([]string{id})
	if err != nil {
		return nil, err
	}
	if len(issues) == 0 {
		return nil, sql.ErrNoRows
	}
	return &issues[0], nil
}

func (s *Store) GetIssueByIdentifier(identifier string) (*Issue, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM issues WHERE identifier = ?`, identifier).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetIssue(id)
}

func (s *Store) GetIssueByProviderRef(providerKind, providerIssueRef string) (*Issue, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM issues WHERE provider_kind = ? AND provider_issue_ref = ?`, normalizeProviderKind(providerKind), strings.TrimSpace(providerIssueRef)).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetIssue(id)
}

func (s *Store) HasProviderIssues(projectID, providerKind string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM issues WHERE project_id = ? AND provider_kind = ? LIMIT 1)`,
		strings.TrimSpace(projectID),
		normalizeProviderKind(providerKind),
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (s *Store) ReconcileProviderIssues(projectID, providerKind string, issues []Issue) error {
	projectID = strings.TrimSpace(projectID)
	providerKind = normalizeProviderKind(providerKind)
	if projectID == "" {
		return validationErrorf("project_id is required")
	}
	if providerKind == "" || providerKind == ProviderKindKanban {
		return validationErrorf("provider_kind must be a non-kanban provider")
	}
	if _, err := s.defaultPermissionProfileForProjectID(projectID); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	rows, err := tx.Query(`SELECT id, project_id, provider_issue_ref, provider_shadow FROM issues WHERE provider_kind = ? AND provider_issue_ref <> ''`, providerKind)
	if err != nil {
		return err
	}
	existingByRef := make(map[string]string, len(issues))
	currentProjectShadowByRef := make(map[string]string, len(issues))
	for rows.Next() {
		var id, providerIssueRef string
		var existingProjectID sql.NullString
		var providerShadow int
		if err := rows.Scan(&id, &existingProjectID, &providerIssueRef, &providerShadow); err != nil {
			_ = rows.Close()
			return err
		}
		trimmedRef := strings.TrimSpace(providerIssueRef)
		existingByRef[trimmedRef] = id
		if providerShadow != 0 && existingProjectID.Valid && strings.TrimSpace(existingProjectID.String) == projectID {
			currentProjectShadowByRef[trimmedRef] = id
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	seenRefs := make(map[string]struct{}, len(issues))
	for i := range issues {
		incoming := &issues[i]
		normalizedProviderKind := normalizeProviderKind(incoming.ProviderKind)
		if normalizedProviderKind == "" {
			normalizedProviderKind = providerKind
		}
		providerIssueRef := strings.TrimSpace(incoming.ProviderIssueRef)
		if normalizedProviderKind != providerKind || providerIssueRef == "" {
			return validationErrorf("provider issue reference is required for provider-backed issues")
		}
		seenRefs[providerIssueRef] = struct{}{}

		workflowPhase, updatedAt, createdAt, lastSyncedAt := normalizeProviderIncomingIssue(incoming)
		if currentID, ok := existingByRef[providerIssueRef]; ok {
			res, err := tx.Exec(`
				UPDATE issues
				SET project_id = ?, identifier = ?, issue_type = ?, title = ?, description = ?, state = ?, workflow_phase = ?, priority = ?, provider_kind = ?, provider_issue_ref = ?, provider_shadow = 1, updated_at = ?, last_synced_at = ?
				WHERE id = ?`,
				projectID, incoming.Identifier, IssueTypeStandard, incoming.Title, incoming.Description, incoming.State, workflowPhase, incoming.Priority, providerKind, providerIssueRef, updatedAt, lastSyncedAt, currentID,
			)
			if err != nil {
				return err
			}
			if rows, err := res.RowsAffected(); err == nil && rows == 0 {
				return notFoundError("issue", currentID)
			}
			if err := replaceIssueLabelsTx(tx, currentID, incoming.Labels); err != nil {
				return err
			}
			if err := replaceIssueBlockersRawTx(tx, currentID, incoming.BlockedBy); err != nil {
				return err
			}
			if err := deleteIssueRecurrenceTx(tx, currentID); err != nil {
				return err
			}
			if err := s.appendChangeTx(tx, "issue", currentID, "updated", map[string]interface{}{"identifier": incoming.Identifier, "provider_kind": providerKind, "provider_issue_ref": providerIssueRef}); err != nil {
				return err
			}
			continue
		}

		id := generateID("iss")
		_, err = tx.Exec(`
			INSERT INTO issues (id, project_id, epic_id, identifier, issue_type, provider_kind, provider_issue_ref, provider_shadow, title, description, state, workflow_phase, permission_profile, priority, agent_name, agent_prompt, created_at, updated_at, last_synced_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, projectID, nil, incoming.Identifier, IssueTypeStandard, providerKind, providerIssueRef, incoming.Title, incoming.Description, incoming.State, workflowPhase, PermissionProfileDefault, incoming.Priority, strings.TrimSpace(incoming.AgentName), strings.TrimSpace(incoming.AgentPrompt), createdAt, updatedAt, lastSyncedAt,
		)
		if err != nil {
			return err
		}
		if err := replaceIssueLabelsTx(tx, id, incoming.Labels); err != nil {
			return err
		}
		if err := replaceIssueBlockersRawTx(tx, id, incoming.BlockedBy); err != nil {
			return err
		}
		if err := deleteIssueRecurrenceTx(tx, id); err != nil {
			return err
		}
		if err := s.appendChangeTx(tx, "issue", id, "created", map[string]interface{}{"project_id": projectID, "identifier": incoming.Identifier, "provider_kind": providerKind, "provider_issue_ref": providerIssueRef}); err != nil {
			return err
		}
		existingByRef[providerIssueRef] = id
		currentProjectShadowByRef[providerIssueRef] = id
	}

	var assetPaths []string
	var commentAttachments []IssueCommentAttachment
	issueAssetDirs := make(map[string]struct{})
	reactivateIssueIDs := make([]string, 0)
	for providerIssueRef, issueID := range currentProjectShadowByRef {
		if _, ok := seenRefs[providerIssueRef]; ok {
			continue
		}
		removedPaths, removedCommentAttachments, issueAssetDir, unblockedIssueIDs, err := s.deleteIssueTx(tx, issueID)
		if err != nil {
			return err
		}
		assetPaths = append(assetPaths, removedPaths...)
		commentAttachments = append(commentAttachments, removedCommentAttachments...)
		reactivateIssueIDs = append(reactivateIssueIDs, unblockedIssueIDs...)
		if issueAssetDir != "" {
			issueAssetDirs[issueAssetDir] = struct{}{}
		}
	}

	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil

	s.cleanupIssueAssetPaths(assetPaths)
	s.cleanupIssueCommentAttachmentPaths(commentAttachments)
	for issueAssetDir := range issueAssetDirs {
		_ = os.Remove(issueAssetDir)
	}
	return s.reactivateIssueAgentCommandsForIssues(reactivateIssueIDs)
}

func (s *Store) ListDispatchIssues(states []string) ([]DispatchIssue, error) {
	cleanStates := make([]string, 0, len(states))
	for _, state := range states {
		state = strings.TrimSpace(state)
		if state == "" {
			continue
		}
		cleanStates = append(cleanStates, state)
	}
	if len(cleanStates) == 0 {
		return []DispatchIssue{}, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cleanStates)), ",")
	args := make([]interface{}, 0, len(cleanStates))
	for _, state := range cleanStates {
		args = append(args, state)
	}
	query := `
		SELECT ` + qualifiedIssueSelectColumns + `,
		       CASE WHEN p.id IS NULL THEN 0 ELSE 1 END,
		       COALESCE(p.state, ''),
		       CASE WHEN ` + unresolvedBlockerExistsClause("i") + ` THEN 1 ELSE 0 END
		FROM issues i
		LEFT JOIN projects p ON p.id = i.project_id
		WHERE i.state IN (` + placeholders + `)
		ORDER BY CASE WHEN i.priority > 0 THEN 0 ELSE 1 END ASC, CASE WHEN i.priority > 0 THEN i.priority END ASC, i.created_at ASC, i.identifier ASC`
	return s.listDispatchIssuesForQuery(query, args...)
}

func (s *Store) GetIssueDispatchState(issueID string) (IssueDispatchState, error) {
	if strings.TrimSpace(issueID) == "" {
		return IssueDispatchState{}, notFoundError("issue", issueID)
	}
	var projectExists int
	var projectState string
	var unresolved int
	err := s.db.QueryRow(`
		SELECT CASE WHEN p.id IS NULL THEN 0 ELSE 1 END,
		       COALESCE(p.state, ''),
		       CASE WHEN `+unresolvedBlockerExistsClause("i")+` THEN 1 ELSE 0 END
		FROM issues i
		LEFT JOIN projects p ON p.id = i.project_id
		WHERE i.id = ?`, issueID).Scan(&projectExists, &projectState, &unresolved)
	if err != nil {
		if err == sql.ErrNoRows {
			return IssueDispatchState{}, notFoundError("issue", issueID)
		}
		return IssueDispatchState{}, err
	}
	return IssueDispatchState{
		ProjectExists:         projectExists != 0,
		ProjectState:          NormalizeProjectState(projectState),
		HasUnresolvedBlockers: unresolved != 0,
	}, nil
}

func (s *Store) LookupIssueTitles(issueIDs, identifiers []string) (map[string]string, map[string]string, error) {
	seen := make(map[string]struct{}, len(issueIDs)+len(identifiers))
	cleanIDs := make([]string, 0, len(issueIDs))
	for _, issueID := range issueIDs {
		issueID = strings.TrimSpace(issueID)
		if issueID == "" {
			continue
		}
		key := "id:" + issueID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		cleanIDs = append(cleanIDs, issueID)
	}
	cleanIdentifiers := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		identifier = strings.TrimSpace(identifier)
		if identifier == "" {
			continue
		}
		key := "identifier:" + identifier
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		cleanIdentifiers = append(cleanIdentifiers, identifier)
	}
	if len(cleanIDs) == 0 && len(cleanIdentifiers) == 0 {
		return map[string]string{}, map[string]string{}, nil
	}

	clauses := make([]string, 0, 2)
	args := make([]interface{}, 0, len(cleanIDs)+len(cleanIdentifiers))
	if len(cleanIDs) > 0 {
		clauses = append(clauses, "id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(cleanIDs)), ",")+")")
		for _, issueID := range cleanIDs {
			args = append(args, issueID)
		}
	}
	if len(cleanIdentifiers) > 0 {
		clauses = append(clauses, "identifier IN ("+strings.TrimSuffix(strings.Repeat("?,", len(cleanIdentifiers)), ",")+")")
		for _, identifier := range cleanIdentifiers {
			args = append(args, identifier)
		}
	}

	rows, err := s.db.Query(
		`SELECT id, identifier, title FROM issues WHERE `+strings.Join(clauses, " OR "),
		args...,
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	titlesByID := make(map[string]string, len(cleanIDs))
	titlesByIdentifier := make(map[string]string, len(cleanIdentifiers))
	for rows.Next() {
		var id, identifier, title string
		if err := rows.Scan(&id, &identifier, &title); err != nil {
			return nil, nil, err
		}
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		titlesByID[id] = title
		titlesByIdentifier[identifier] = title
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return titlesByID, titlesByIdentifier, nil
}

func (s *Store) UpsertProviderIssue(projectID string, incoming *Issue) (*Issue, error) {
	if incoming == nil {
		return nil, validationErrorf("issue is required")
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, validationErrorf("project_id is required")
	}
	providerKind := normalizeProviderKind(incoming.ProviderKind)
	providerIssueRef := strings.TrimSpace(incoming.ProviderIssueRef)
	if providerKind == ProviderKindKanban || providerIssueRef == "" {
		return nil, validationErrorf("provider issue reference is required for provider-backed issues")
	}
	if _, err := s.defaultPermissionProfileForProjectID(projectID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	workflowPhase := incoming.WorkflowPhase
	if !workflowPhase.IsValid() {
		workflowPhase = DefaultWorkflowPhaseForState(incoming.State)
	}
	updatedAt := incoming.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	var currentID string
	err = tx.QueryRow(`SELECT id FROM issues WHERE provider_kind = ? AND provider_issue_ref = ?`, providerKind, providerIssueRef).Scan(&currentID)
	switch {
	case err == sql.ErrNoRows:
		id := generateID("iss")
		createdAt := incoming.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		lastSyncedAt := now
		if incoming.LastSyncedAt != nil {
			lastSyncedAt = incoming.LastSyncedAt.UTC()
		}
		_, err = tx.Exec(`
				INSERT INTO issues (id, project_id, epic_id, identifier, issue_type, provider_kind, provider_issue_ref, provider_shadow, title, description, state, workflow_phase, permission_profile, priority, agent_name, agent_prompt, created_at, updated_at, last_synced_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, projectID, nil, incoming.Identifier, IssueTypeStandard, providerKind, providerIssueRef, incoming.Title, incoming.Description, incoming.State, workflowPhase, PermissionProfileDefault, incoming.Priority, strings.TrimSpace(incoming.AgentName), strings.TrimSpace(incoming.AgentPrompt), createdAt, updatedAt, lastSyncedAt,
		)
		if err != nil {
			return nil, err
		}
		if err := replaceIssueLabelsTx(tx, id, incoming.Labels); err != nil {
			return nil, err
		}
		if err := replaceIssueBlockersRawTx(tx, id, incoming.BlockedBy); err != nil {
			return nil, err
		}
		if err := deleteIssueRecurrenceTx(tx, id); err != nil {
			return nil, err
		}
		if err := s.appendChangeTx(tx, "issue", id, "created", map[string]interface{}{"project_id": projectID, "identifier": incoming.Identifier, "provider_kind": providerKind, "provider_issue_ref": providerIssueRef}); err != nil {
			return nil, err
		}
		if err := s.commitTx(tx, true); err != nil {
			return nil, err
		}
		tx = nil
		return s.GetIssue(id)
	case err != nil:
		return nil, err
	default:
		lastSyncedAt := now
		if incoming.LastSyncedAt != nil {
			lastSyncedAt = incoming.LastSyncedAt.UTC()
		}
		res, err := tx.Exec(`
			UPDATE issues
			SET project_id = ?, identifier = ?, issue_type = ?, title = ?, description = ?, state = ?, workflow_phase = ?, priority = ?, provider_kind = ?, provider_issue_ref = ?, provider_shadow = 1, updated_at = ?, last_synced_at = ?
			WHERE id = ?`,
			projectID, incoming.Identifier, IssueTypeStandard, incoming.Title, incoming.Description, incoming.State, workflowPhase, incoming.Priority, providerKind, providerIssueRef, updatedAt, lastSyncedAt, currentID,
		)
		if err != nil {
			return nil, err
		}
		if rows, err := res.RowsAffected(); err == nil && rows == 0 {
			return nil, notFoundError("issue", currentID)
		}
		if err := replaceIssueLabelsTx(tx, currentID, incoming.Labels); err != nil {
			return nil, err
		}
		if err := replaceIssueBlockersRawTx(tx, currentID, incoming.BlockedBy); err != nil {
			return nil, err
		}
		if err := deleteIssueRecurrenceTx(tx, currentID); err != nil {
			return nil, err
		}
		if err := s.appendChangeTx(tx, "issue", currentID, "updated", map[string]interface{}{"identifier": incoming.Identifier, "provider_kind": providerKind, "provider_issue_ref": providerIssueRef}); err != nil {
			return nil, err
		}
		if err := s.commitTx(tx, true); err != nil {
			return nil, err
		}
		tx = nil
		return s.GetIssue(currentID)
	}
}

func replaceIssueLabelsTx(tx *sql.Tx, issueID string, labels []string) error {
	if _, err := tx.Exec(`DELETE FROM issue_labels WHERE issue_id = ?`, issueID); err != nil {
		return err
	}
	for _, label := range labels {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO issue_labels (issue_id, label) VALUES (?, ?)`, issueID, label); err != nil {
			return err
		}
	}
	return nil
}

func replaceIssueBlockersRawTx(tx *sql.Tx, issueID string, blockers []string) error {
	if _, err := tx.Exec(`DELETE FROM issue_blockers WHERE issue_id = ?`, issueID); err != nil {
		return err
	}
	for _, blocker := range normalizeBlockers(blockers) {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO issue_blockers (issue_id, blocked_by) VALUES (?, ?)`, issueID, blocker); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteProviderIssuesExcept(projectID, providerKind string, keepRefs []string) error {
	projectID = strings.TrimSpace(projectID)
	providerKind = normalizeProviderKind(providerKind)
	if projectID == "" || providerKind == "" || providerKind == ProviderKindKanban {
		return nil
	}

	query := `SELECT id FROM issues WHERE project_id = ? AND provider_kind = ? AND provider_shadow = 1`
	args := []interface{}{projectID, providerKind}
	if len(keepRefs) > 0 {
		placeholders := make([]string, 0, len(keepRefs))
		for _, ref := range keepRefs {
			trimmed := strings.TrimSpace(ref)
			if trimmed == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, trimmed)
		}
		if len(placeholders) > 0 {
			query += ` AND provider_issue_ref NOT IN (` + strings.Join(placeholders, ",") + `)`
		}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	var staleIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		staleIDs = append(staleIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range staleIDs {
		if err := s.DeleteIssue(id); err != nil && !IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (s *Store) ListIssues(filter map[string]interface{}) ([]Issue, error) {
	query := `SELECT id FROM issues WHERE 1=1`
	args := []interface{}{}

	if projectID, ok := filter["project_id"].(string); ok && projectID != "" {
		query += " AND project_id = ?"
		args = append(args, projectID)
	}
	if providerKind, ok := filter["provider_kind"].(string); ok && providerKind != "" {
		query += " AND provider_kind = ?"
		args = append(args, normalizeProviderKind(providerKind))
	}
	if issueType, ok := filter["issue_type"].(string); ok && strings.TrimSpace(issueType) != "" {
		query += " AND issue_type = ?"
		args = append(args, NormalizeIssueType(issueType))
	}
	if epicID, ok := filter["epic_id"].(string); ok && epicID != "" {
		query += " AND epic_id = ?"
		args = append(args, epicID)
	}
	if state, ok := filter["state"].(string); ok && state != "" {
		query += " AND state = ?"
		args = append(args, state)
	}
	if states, ok := filter["states"].([]string); ok && len(states) > 0 {
		placeholders := strings.Repeat("?,", len(states))
		query += " AND state IN (" + placeholders[:len(placeholders)-1] + ")"
		for _, s := range states {
			args = append(args, s)
		}
	}
	if planApprovalPending, ok := filter["plan_approval_pending"].(bool); ok {
		query += " AND plan_approval_pending = ?"
		if planApprovalPending {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}

	query += " ORDER BY CASE WHEN priority > 0 THEN 0 ELSE 1 END ASC, CASE WHEN priority > 0 THEN priority END ASC, created_at ASC, identifier ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.loadIssuesByIDs(ids)
}

func (s *Store) CountIssuesByState(projectID string) (map[State]int, error) {
	query := `SELECT state, COUNT(*) FROM issues`
	args := []interface{}{}
	if strings.TrimSpace(projectID) != "" {
		query += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	query += ` GROUP BY state`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[State]int)
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		counts[State(state)] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}

func issueSummaryFilters(query IssueQuery) (string, []interface{}) {
	where := []string{"1=1"}
	args := []interface{}{}
	if query.ProjectID != "" {
		where = append(where, "i.project_id = ?")
		args = append(args, query.ProjectID)
	}
	if query.ProjectName != "" {
		where = append(where, "p.name = ? COLLATE NOCASE")
		args = append(args, query.ProjectName)
	}
	if query.EpicID != "" {
		where = append(where, "i.epic_id = ?")
		args = append(args, query.EpicID)
	}
	if query.State != "" {
		where = append(where, "i.state = ?")
		args = append(args, query.State)
	}
	if strings.TrimSpace(query.IssueType) != "" {
		where = append(where, "i.issue_type = ?")
		args = append(args, NormalizeIssueType(query.IssueType))
	}
	if query.Search != "" {
		where = append(where, "(i.identifier LIKE ? OR i.title LIKE ? OR i.description LIKE ?)")
		needle := "%" + query.Search + "%"
		args = append(args, needle, needle, needle)
	}
	if query.Blocked != nil {
		if *query.Blocked {
			where = append(where, unresolvedBlockerExistsClause("i"))
		} else {
			where = append(where, "NOT "+unresolvedBlockerExistsClause("i"))
		}
	}

	return strings.Join(where, " AND "), args
}

func (s *Store) CountIssueSummariesByState(query IssueQuery) (IssueStateCounts, error) {
	where, args := issueSummaryFilters(query)
	rows, err := s.db.Query(`SELECT i.state, COUNT(*) FROM issues i LEFT JOIN projects p ON p.id = i.project_id WHERE `+where+` GROUP BY i.state`, args...)
	if err != nil {
		return IssueStateCounts{}, err
	}
	defer rows.Close()

	var counts IssueStateCounts
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return IssueStateCounts{}, err
		}
		counts.AddCount(State(state), count)
	}
	if err := rows.Err(); err != nil {
		return IssueStateCounts{}, err
	}
	return counts, nil
}

func (s *Store) UpdateIssueState(id string, state State) error {
	return s.UpdateIssueStateAndPhase(id, state, DefaultWorkflowPhaseForState(state))
}

func (s *Store) UpdateProviderIssueState(id string, state State, phase WorkflowPhase, syncedAt *time.Time) error {
	if strings.TrimSpace(string(state)) == "" {
		return invalidStateError(state)
	}
	now := time.Now()
	if !phase.IsValid() {
		current, err := s.GetIssue(id)
		if err != nil {
			return err
		}
		phase = current.WorkflowPhase
		if !phase.IsValid() {
			phase = WorkflowPhaseImplementation
		}
	}
	var lastSynced interface{}
	if syncedAt != nil {
		lastSynced = syncedAt.UTC()
	} else {
		lastSynced = now.UTC()
	}
	res, err := s.db.Exec(`
		UPDATE issues
		SET state = ?, workflow_phase = ?, updated_at = ?, last_synced_at = ?
		WHERE id = ?`,
		state, phase, now, lastSynced, id,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue", id)
	}
	if err := s.appendChange("issue", id, "state_changed", map[string]interface{}{"state": state, "workflow_phase": phase}); err != nil {
		return err
	}
	return s.ActivateIssueAgentCommandsIfDispatchable(id)
}

func (s *Store) UpdateIssueStateAndPhase(id string, state State, phase WorkflowPhase) error {
	if !state.IsValid() {
		return invalidStateError(state)
	}
	if state == StateInProgress {
		unresolved, err := s.unresolvedBlockersForIssue(id)
		if err != nil {
			return err
		}
		if len(unresolved) > 0 {
			return blockedInProgressError(unresolved)
		}
	}
	now := time.Now().UTC()
	var startedAt, completedAt interface{}
	if !phase.IsValid() {
		phase = DefaultWorkflowPhaseForState(state)
	}

	if state == StateInProgress {
		startedAt = now
	}
	if state == StateDone || state == StateCancelled {
		completedAt = now
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	res, err := tx.Exec(`
		UPDATE issues
		SET state = ?, workflow_phase = ?, updated_at = ?, started_at = COALESCE(?, started_at), completed_at = COALESCE(?, completed_at)
		WHERE id = ?`,
		state, phase, now, startedAt, completedAt, id,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue", id)
	}
	if state == StateCancelled {
		if err := disableIssueRecurrenceTx(tx, id, now); err != nil {
			return err
		}
	}
	if err := s.appendChangeTx(tx, "issue", id, "state_changed", map[string]interface{}{"state": state, "workflow_phase": phase}); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return s.ActivateIssueAgentCommandsIfDispatchable(id)
}

func (s *Store) unresolvedBlockersForIssue(id string) ([]string, error) {
	issue, err := s.GetIssue(id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, notFoundError("issue", id)
		}
		return nil, err
	}
	if len(issue.BlockedBy) == 0 {
		return nil, nil
	}

	unresolved := make([]string, 0, len(issue.BlockedBy))
	for _, blocker := range issue.BlockedBy {
		blockerIssue, err := s.GetIssueByIdentifier(blocker)
		switch {
		case err == nil:
			if !isResolvedBlockerState(blockerIssue.State) {
				unresolved = append(unresolved, blockerIssue.Identifier)
			}
		case err == sql.ErrNoRows:
			unresolved = append(unresolved, blocker)
		default:
			return nil, err
		}
	}
	sort.Strings(unresolved)
	return unresolved, nil
}

func (s *Store) UpdateIssueWorkflowPhase(id string, phase WorkflowPhase) error {
	if !phase.IsValid() {
		current, err := s.GetIssue(id)
		if err != nil {
			return err
		}
		phase = DefaultWorkflowPhaseForState(current.State)
	}
	res, err := s.db.Exec(`UPDATE issues SET workflow_phase = ?, updated_at = ? WHERE id = ?`, phase, time.Now(), id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue", id)
	}
	return s.appendChange("issue", id, "phase_changed", map[string]interface{}{"workflow_phase": phase})
}

func (s *Store) UpdateIssue(id string, updates map[string]interface{}) error {
	current, err := s.GetIssue(id)
	if err != nil {
		if err == sql.ErrNoRows {
			return notFoundError("issue", id)
		}
		return err
	}
	if err := s.resolveIssueUpdateAssociations(current, updates); err != nil {
		return err
	}
	currentType := NormalizeIssueType(string(current.IssueType))
	nextType := currentType
	issueTypeSpecified := false
	if raw, ok := updates["issue_type"]; ok {
		issueTypeSpecified = true
		nextType, err = ParseIssueType(fmt.Sprint(raw))
		if err != nil {
			return err
		}
		updates["issue_type"] = nextType
	}
	cronValue := current.Cron
	cronSpecified := false
	if raw, ok := updates["cron"]; ok {
		cronSpecified = true
		cronValue = normalizeCronSpec(fmt.Sprint(raw))
		updates["cron"] = cronValue
	}
	enabledValue := current.Enabled
	enabledSpecified := false
	if currentType != IssueTypeRecurring && nextType == IssueTypeRecurring {
		enabledValue = defaultRecurringEnabled(nil)
	}
	if raw, ok := updates["enabled"]; ok {
		parsed, ok := boolFromValue(raw)
		if !ok {
			return validationErrorf("enabled must be a boolean")
		}
		enabledSpecified = true
		enabledValue = parsed
		updates["enabled"] = enabledValue
	}
	if currentType != IssueTypeRecurring && nextType != IssueTypeRecurring && (cronSpecified || enabledSpecified) {
		return validationErrorf("cron and enabled are only valid for recurring issues")
	}
	if nextType == IssueTypeRecurring && strings.TrimSpace(cronValue) == "" {
		return validationErrorf("cron is required for recurring issues")
	}

	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	// Build dynamic update
	query := "UPDATE issues SET updated_at = ?"
	args := []interface{}{now}

	if title, ok := updates["title"].(string); ok {
		query += ", title = ?"
		args = append(args, title)
	}
	if desc, ok := updates["description"].(string); ok {
		query += ", description = ?"
		args = append(args, desc)
	}
	if priority, ok := updates["priority"].(int); ok {
		query += ", priority = ?"
		args = append(args, priority)
	}
	if branch, ok := updates["branch_name"].(string); ok {
		query += ", branch_name = ?"
		args = append(args, branch)
	}
	if agentName, ok := updates["agent_name"].(string); ok {
		query += ", agent_name = ?"
		args = append(args, strings.TrimSpace(agentName))
	}
	if agentPrompt, ok := updates["agent_prompt"].(string); ok {
		query += ", agent_prompt = ?"
		args = append(args, strings.TrimSpace(agentPrompt))
	}
	if projectID, ok := updates["project_id"].(string); ok {
		query += ", project_id = ?"
		args = append(args, nullableStringValue(projectID))
	}
	if epicID, ok := updates["epic_id"].(string); ok {
		query += ", epic_id = ?"
		args = append(args, nullableStringValue(epicID))
	}
	if prURL, ok := updates["pr_url"].(string); ok {
		query += ", pr_url = ?"
		args = append(args, prURL)
	}
	var permissionProfile PermissionProfile
	var permissionProfileSpecified bool
	if raw, ok := updates["permission_profile"]; ok {
		parsed, err := ParsePermissionProfile(fmt.Sprint(raw))
		if err != nil {
			return err
		}
		permissionProfile = parsed
		permissionProfileSpecified = true
		updates["permission_profile"] = permissionProfile
		delete(updates, "collaboration_mode_override")
		delete(updates, "plan_approval_pending")
		delete(updates, "pending_plan_markdown")
		delete(updates, "pending_plan_requested_at")
		delete(updates, "pending_plan_revision_markdown")
		delete(updates, "pending_plan_revision_requested_at")
	}
	if permissionProfileSpecified {
		query += ", permission_profile = ?, collaboration_mode_override = '', plan_approval_pending = 0, pending_plan_markdown = '', pending_plan_requested_at = NULL, pending_plan_revision_markdown = '', pending_plan_revision_requested_at = NULL"
		args = append(args, permissionProfile)
	}
	if !permissionProfileSpecified {
		if collaborationModeOverride, ok := updates["collaboration_mode_override"].(CollaborationModeOverride); ok {
			query += ", collaboration_mode_override = ?"
			args = append(args, NormalizeCollaborationModeOverride(string(collaborationModeOverride)))
		}
		if planApprovalPending, ok := updates["plan_approval_pending"].(bool); ok {
			query += ", plan_approval_pending = ?"
			if planApprovalPending {
				args = append(args, 1)
			} else {
				args = append(args, 0)
			}
		}
		if pendingPlanMarkdown, ok := updates["pending_plan_markdown"].(string); ok {
			query += ", pending_plan_markdown = ?"
			args = append(args, pendingPlanMarkdown)
		}
		if pendingPlanRequestedAt, ok := updates["pending_plan_requested_at"].(*time.Time); ok {
			query += ", pending_plan_requested_at = ?"
			if pendingPlanRequestedAt == nil {
				args = append(args, nil)
			} else {
				args = append(args, pendingPlanRequestedAt.UTC())
			}
		}
		if pendingPlanRevisionMarkdown, ok := updates["pending_plan_revision_markdown"].(string); ok {
			query += ", pending_plan_revision_markdown = ?"
			args = append(args, pendingPlanRevisionMarkdown)
		}
		if pendingPlanRevisionRequestedAt, ok := updates["pending_plan_revision_requested_at"].(*time.Time); ok {
			query += ", pending_plan_revision_requested_at = ?"
			if pendingPlanRevisionRequestedAt == nil {
				args = append(args, nil)
			} else {
				args = append(args, pendingPlanRevisionRequestedAt.UTC())
			}
		}
	}
	if issueTypeSpecified {
		query += ", issue_type = ?"
		args = append(args, nextType)
	}

	query += " WHERE id = ?"
	args = append(args, id)

	res, err := tx.Exec(query, args...)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue", id)
	}

	// Handle labels separately
	if labels, ok := updates["labels"].([]string); ok {
		if _, err := tx.Exec(`DELETE FROM issue_labels WHERE issue_id = ?`, id); err != nil {
			return err
		}
		for _, label := range labels {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO issue_labels (issue_id, label) VALUES (?, ?)`, id, label); err != nil {
				return err
			}
		}
	}

	// Handle blockers
	if blockers, ok := updates["blocked_by"].([]string); ok {
		persisted, err := s.setIssueBlockersTx(tx, id, blockers)
		if err != nil {
			return err
		}
		updates["blocked_by"] = persisted
	}
	recurrenceChanged := false
	switch {
	case nextType == IssueTypeRecurring && (currentType != IssueTypeRecurring || issueTypeSpecified || cronSpecified || enabledSpecified):
		recurrenceChanged = true
		var currentRecurrence *IssueRecurrence
		if currentType == IssueTypeRecurring {
			currentRecurrence = &IssueRecurrence{
				IssueID:        current.ID,
				Cron:           current.Cron,
				Enabled:        current.Enabled,
				NextRunAt:      current.NextRunAt,
				LastEnqueuedAt: current.LastEnqueuedAt,
				PendingRerun:   current.PendingRerun,
				CreatedAt:      current.CreatedAt,
				UpdatedAt:      current.UpdatedAt,
			}
		}
		nextRunAt := current.NextRunAt
		if currentType != IssueTypeRecurring || cronSpecified || nextRunAt == nil || (enabledSpecified && enabledValue && !nextRunAt.After(now)) {
			computed, err := NextRecurringRun(cronValue, now, time.Local)
			if err != nil {
				return err
			}
			nextRunAt = &computed
		}
		recurrence, err := buildIssueRecurrence(id, cronValue, enabledValue, nextRunAt, currentRecurrence, now)
		if err != nil {
			return err
		}
		if err := saveIssueRecurrenceTx(tx, recurrence); err != nil {
			return err
		}
	case currentType == IssueTypeRecurring && nextType != IssueTypeRecurring:
		recurrenceChanged = true
		if err := deleteIssueRecurrenceTx(tx, id); err != nil {
			return err
		}
	}
	if recurrenceChanged {
		updates["issue_type"] = nextType
	}
	if err := s.appendChangeTx(tx, "issue", id, "updated", updates); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return s.ActivateIssueAgentCommandsIfDispatchable(id)
}

func (s *Store) SetIssueBlockers(issueID string, blockers []string) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	persisted, err := s.setIssueBlockersTx(tx, issueID, blockers)
	if err != nil {
		return nil, err
	}
	if err := s.appendChangeTx(tx, "issue", issueID, "updated", map[string]interface{}{"blocked_by": persisted}); err != nil {
		return nil, err
	}
	if err := s.commitTx(tx, true); err != nil {
		return nil, err
	}
	tx = nil
	_ = s.ActivateIssueAgentCommandsIfDispatchable(issueID)
	return persisted, nil
}

func (s *Store) deleteIssueTx(tx *sql.Tx, id string) ([]string, []IssueCommentAttachment, string, []string, error) {
	var workspacePath string
	err := tx.QueryRow(`SELECT path FROM workspaces WHERE issue_id = ?`, id).Scan(&workspacePath)
	switch {
	case err == nil:
	case err == sql.ErrNoRows:
		workspacePath = ""
	default:
		return nil, nil, "", nil, err
	}

	var blockerIdentifier string
	err = tx.QueryRow(`SELECT identifier FROM issues WHERE id = ?`, id).Scan(&blockerIdentifier)
	switch {
	case err == nil:
	case err == sql.ErrNoRows:
		blockerIdentifier = ""
	default:
		return nil, nil, "", nil, err
	}

	blockedIssueIDs := []string{}
	if blockerIdentifier != "" {
		rows, err := tx.Query(`SELECT DISTINCT issue_id FROM issue_blockers WHERE blocked_by = ? ORDER BY issue_id`, blockerIdentifier)
		if err != nil {
			return nil, nil, "", nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var issueID string
			if err := rows.Scan(&issueID); err != nil {
				return nil, nil, "", nil, err
			}
			blockedIssueIDs = append(blockedIssueIDs, issueID)
		}
		if err := rows.Err(); err != nil {
			return nil, nil, "", nil, err
		}
	}

	if _, err := tx.Exec(`DELETE FROM issue_labels WHERE issue_id = ?`, id); err != nil {
		return nil, nil, "", nil, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_blockers WHERE issue_id = ?`, id); err != nil {
		return nil, nil, "", nil, err
	}
	if blockerIdentifier != "" {
		if _, err := tx.Exec(`DELETE FROM issue_blockers WHERE blocked_by = ?`, blockerIdentifier); err != nil {
			return nil, nil, "", nil, err
		}
	}
	assetPaths, err := s.deleteIssueAssetsTx(tx, id)
	if err != nil {
		return nil, nil, "", nil, err
	}
	commentAttachments, err := s.deleteIssueCommentsTx(tx, id)
	if err != nil {
		return nil, nil, "", nil, err
	}
	if err := s.deleteIssuePlanningTx(tx, id); err != nil {
		return nil, nil, "", nil, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_recurrences WHERE issue_id = ?`, id); err != nil {
		return nil, nil, "", nil, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_execution_sessions WHERE issue_id = ?`, id); err != nil {
		return nil, nil, "", nil, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_activity_updates WHERE issue_id = ?`, id); err != nil {
		return nil, nil, "", nil, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_activity_entries WHERE issue_id = ?`, id); err != nil {
		return nil, nil, "", nil, err
	}
	if _, err := tx.Exec(`DELETE FROM issue_agent_commands WHERE issue_id = ?`, id); err != nil {
		return nil, nil, "", nil, err
	}
	if _, err := tx.Exec(`DELETE FROM workspaces WHERE issue_id = ?`, id); err != nil {
		return nil, nil, "", nil, err
	}
	res, err := tx.Exec(`DELETE FROM issues WHERE id = ?`, id)
	if err != nil {
		return nil, nil, "", nil, err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return nil, nil, "", nil, notFoundError("issue", id)
	}
	if workspacePath != "" {
		if err := s.appendChangeTx(tx, "workspace", id, "deleted", map[string]interface{}{"path": workspacePath}); err != nil {
			return nil, nil, "", nil, err
		}
	}
	if err := s.appendChangeTx(tx, "issue", id, "deleted", nil); err != nil {
		return nil, nil, "", nil, err
	}
	return assetPaths, commentAttachments, filepath.Join(s.IssueAssetRoot(), id), blockedIssueIDs, nil
}

func (s *Store) deleteIssuePlanningTx(tx *sql.Tx, issueID string) error {
	if _, err := tx.Exec(`DELETE FROM issue_plan_versions WHERE session_id IN (SELECT id FROM issue_plan_sessions WHERE issue_id = ?)`, issueID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM issue_plan_sessions WHERE issue_id = ?`, issueID); err != nil {
		return err
	}
	return nil
}

func (s *Store) DeleteIssue(id string) error {
	var workspace *Workspace
	workspace, err := s.GetWorkspace(id)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	issueAssetDir := filepath.Join(s.IssueAssetRoot(), id)
	if workspace != nil {
		if err := removeWorkspaceTree(workspace.Path); err != nil {
			return err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	assetPaths, commentAttachments, issueAssetDir, unblockedIssueIDs, err := s.deleteIssueTx(tx, id)
	if err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	s.cleanupIssueAssetPaths(assetPaths)
	s.cleanupIssueCommentAttachmentPaths(commentAttachments)
	_ = os.Remove(issueAssetDir)
	return s.reactivateIssueAgentCommandsForIssues(unblockedIssueIDs)
}

func (s *Store) reactivateIssueAgentCommandsForIssues(issueIDs []string) error {
	if len(issueIDs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(issueIDs))
	clean := make([]string, 0, len(issueIDs))
	for _, issueID := range issueIDs {
		issueID = strings.TrimSpace(issueID)
		if issueID == "" {
			continue
		}
		if _, ok := seen[issueID]; ok {
			continue
		}
		seen[issueID] = struct{}{}
		clean = append(clean, issueID)
	}
	if len(clean) == 0 {
		return nil
	}
	sort.Strings(clean)
	errs := make([]error, 0, len(clean))
	for _, issueID := range clean {
		if err := s.ActivateIssueAgentCommandsIfDispatchable(issueID); err != nil {
			if IsNotFound(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("issue %s: %w", issueID, err))
		}
	}
	return errors.Join(errs...)
}

// Workspace operations

func (s *Store) CreateWorkspace(issueID, path string) (*Workspace, error) {
	now := time.Now()
	_, err := s.db.Exec(`
		INSERT INTO workspaces (issue_id, path, created_at, run_count)
		VALUES (?, ?, ?, 0)`,
		issueID, path, now,
	)
	if err != nil {
		return nil, err
	}
	if err := s.appendChange("workspace", issueID, "created", map[string]interface{}{"path": path}); err != nil {
		return nil, err
	}
	return &Workspace{IssueID: issueID, Path: path, CreatedAt: now, RunCount: 0}, nil
}

func (s *Store) GetWorkspace(issueID string) (*Workspace, error) {
	w := &Workspace{}
	var lastRun sql.NullTime
	err := s.db.QueryRow(`
		SELECT issue_id, path, created_at, last_run_at, run_count
		FROM workspaces WHERE issue_id = ?`, issueID,
	).Scan(&w.IssueID, &w.Path, &w.CreatedAt, &lastRun, &w.RunCount)
	if err != nil {
		return nil, err
	}
	if lastRun.Valid {
		w.LastRunAt = &lastRun.Time
	}
	return w, nil
}

func (s *Store) UpdateWorkspaceRun(issueID string) error {
	now := time.Now()
	_, err := s.db.Exec(`
		UPDATE workspaces SET last_run_at = ?, run_count = run_count + 1
		WHERE issue_id = ?`,
		now, issueID,
	)
	if err != nil {
		return err
	}
	return s.appendChange("workspace", issueID, "run_updated", nil)
}

func (s *Store) UpdateWorkspacePath(issueID, path string) (*Workspace, error) {
	_, err := s.db.Exec(`
		UPDATE workspaces SET path = ?
		WHERE issue_id = ?`,
		path, issueID,
	)
	if err != nil {
		return nil, err
	}
	if err := s.appendChange("workspace", issueID, "path_updated", map[string]interface{}{"path": path}); err != nil {
		return nil, err
	}
	return s.GetWorkspace(issueID)
}

func (s *Store) DeleteWorkspace(issueID string) error {
	workspace, err := s.GetWorkspace(issueID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	if err := removeWorkspaceTree(workspace.Path); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM workspaces WHERE issue_id = ?`, issueID)
	if err != nil {
		return err
	}
	return s.appendChange("workspace", issueID, "deleted", map[string]interface{}{"path": workspace.Path})
}

func removeWorkspaceTree(path string) error {
	if commonDir, err := gitCommonDirForWorkspace(path); err == nil && filepath.Base(commonDir) == ".git" {
		repoPath := filepath.Dir(commonDir)
		cmd := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", path)
		cmd.Env = gitCommandEnv()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	err := os.RemoveAll(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	if err := makeTreeUserWritable(path); err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func gitCommonDirForWorkspace(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--git-common-dir")
	cmd.Env = gitCommandEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail != "" {
			return "", fmt.Errorf("%w: %s", err, detail)
		}
		return "", err
	}
	value := strings.TrimSpace(stdout.String())
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	return filepath.Abs(filepath.Join(path, value))
}

func makeTreeUserWritable(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}

		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		perm := info.Mode().Perm()
		want := perm | 0o600
		if info.IsDir() {
			want |= 0o100
		}
		if want == perm {
			return nil
		}
		if err := os.Chmod(path, want); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}

func (s *Store) ListProjectSummaries() ([]ProjectSummary, error) {
	projects, err := s.ListProjects()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT project_id, state, COUNT(*) FROM issues GROUP BY project_id, state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	countsByProject := map[string]IssueStateCounts{}
	stateCountsByProject := map[string]map[string]int{}
	for rows.Next() {
		var projectID sql.NullString
		var state State
		var count int
		if err := rows.Scan(&projectID, &state, &count); err != nil {
			return nil, err
		}
		projectKey := stringFromNull(projectID)
		counts := countsByProject[projectKey]
		counts.AddCount(state, count)
		countsByProject[projectKey] = counts
		if stateCountsByProject[projectKey] == nil {
			stateCountsByProject[projectKey] = map[string]int{}
		}
		stateCountsByProject[projectKey][string(state)] += count
	}
	tokenRows, err := s.db.Query(`SELECT project_id, COALESCE(SUM(total_tokens_spent), 0) FROM issues GROUP BY project_id`)
	if err != nil {
		return nil, err
	}
	defer tokenRows.Close()

	tokensByProject := map[string]int{}
	for tokenRows.Next() {
		var projectID sql.NullString
		var totalTokens int
		if err := tokenRows.Scan(&projectID, &totalTokens); err != nil {
			return nil, err
		}
		tokensByProject[stringFromNull(projectID)] = totalTokens
	}

	out := make([]ProjectSummary, 0, len(projects))
	for _, project := range projects {
		buckets := BuildStateBuckets(stateCountsByProject[project.ID], projectDefaultActiveStates(project), projectDefaultTerminalStates(project))
		total, active, terminal := AggregateStateBuckets(buckets)
		out = append(out, ProjectSummary{
			Project:          project,
			TotalTokensSpent: tokensByProject[project.ID],
			Counts:           countsByProject[project.ID],
			StateBuckets:     buckets,
			TotalCount:       total,
			ActiveCount:      active,
			TerminalCount:    terminal,
		})
	}
	return out, nil
}

func (s *Store) ListEpicSummaries(projectID string) ([]EpicSummary, error) {
	epics, err := s.ListEpics(projectID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT e.id, COALESCE(p.name, ''), i.state, COUNT(*)
		FROM epics e
		LEFT JOIN projects p ON p.id = e.project_id
		LEFT JOIN issues i ON i.epic_id = e.id
		WHERE (? = '' OR e.project_id = ?)
		GROUP BY e.id, p.name, i.state
		ORDER BY e.name`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	countsByEpic := map[string]IssueStateCounts{}
	projectNames := map[string]string{}
	stateCountsByEpic := map[string]map[string]int{}
	for rows.Next() {
		var epicID, projectName string
		var state sql.NullString
		var count int
		if err := rows.Scan(&epicID, &projectName, &state, &count); err != nil {
			return nil, err
		}
		projectNames[epicID] = projectName
		if !state.Valid {
			continue
		}
		counts := countsByEpic[epicID]
		counts.AddCount(State(state.String), count)
		countsByEpic[epicID] = counts
		if stateCountsByEpic[epicID] == nil {
			stateCountsByEpic[epicID] = map[string]int{}
		}
		stateCountsByEpic[epicID][state.String] += count
	}

	out := make([]EpicSummary, 0, len(epics))
	for _, epic := range epics {
		buckets := BuildStateBuckets(stateCountsByEpic[epic.ID], []string{string(StateReady), string(StateInProgress), string(StateInReview)}, []string{string(StateDone), string(StateCancelled)})
		total, active, terminal := AggregateStateBuckets(buckets)
		out = append(out, EpicSummary{
			Epic:          epic,
			ProjectName:   projectNames[epic.ID],
			Counts:        countsByEpic[epic.ID],
			StateBuckets:  buckets,
			TotalCount:    total,
			ActiveCount:   active,
			TerminalCount: terminal,
		})
	}
	return out, nil
}

func (s *Store) ListIssueSummariesWithCounts(query IssueQuery) ([]IssueSummary, int, IssueStateCounts, error) {
	if query.Limit <= 0 || query.Limit > 500 {
		query.Limit = 200
	}
	if query.Offset < 0 {
		query.Offset = 0
	}

	baseWhere, args := issueSummaryFilters(query)
	counts, err := s.CountIssueSummariesByState(query)
	if err != nil {
		return nil, 0, IssueStateCounts{}, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM issues i LEFT JOIN projects p ON p.id = i.project_id WHERE `+baseWhere, args...).Scan(&total); err != nil {
		return nil, 0, IssueStateCounts{}, err
	}

	orderBy := "i.updated_at DESC, i.created_at DESC"
	switch query.Sort {
	case "created_asc":
		orderBy = "i.created_at ASC"
	case "priority_asc":
		orderBy = "CASE WHEN i.priority > 0 THEN 0 ELSE 1 END ASC, CASE WHEN i.priority > 0 THEN i.priority END ASC, i.updated_at DESC"
	case "identifier_asc":
		orderBy = "i.identifier ASC"
	case "state_asc":
		orderBy = "i.state ASC, CASE WHEN i.priority > 0 THEN 0 ELSE 1 END ASC, CASE WHEN i.priority > 0 THEN i.priority END ASC, i.updated_at DESC"
	case "project_asc":
		orderBy = "CASE WHEN COALESCE(p.name, '') = '' THEN 1 ELSE 0 END ASC, COALESCE(p.name, '') ASC, i.updated_at DESC, i.identifier ASC"
	case "epic_asc":
		orderBy = "CASE WHEN COALESCE(e.name, '') = '' THEN 1 ELSE 0 END ASC, COALESCE(e.name, '') ASC, i.updated_at DESC, i.identifier ASC"
	}

	rows, err := s.db.Query(`
			SELECT i.id, i.project_id, i.epic_id, i.identifier, i.issue_type, i.provider_kind, i.provider_issue_ref, i.provider_shadow, i.title, i.description, i.state, i.workflow_phase, i.permission_profile, i.collaboration_mode_override, i.plan_approval_pending, i.pending_plan_markdown, i.pending_plan_requested_at, i.pending_plan_revision_markdown, i.pending_plan_revision_requested_at, i.priority,
			       i.agent_name, i.agent_prompt, i.branch_name, i.pr_url, i.created_at, i.updated_at, i.total_tokens_spent, i.started_at, i.completed_at, i.last_synced_at,
			       COALESCE(p.name, ''), COALESCE(p.description, ''), COALESCE(e.name, ''), COALESCE(e.description, ''),
		       COALESCE(w.path, ''), COALESCE(w.run_count, 0), w.last_run_at
		FROM issues i
		LEFT JOIN projects p ON p.id = i.project_id
		LEFT JOIN epics e ON e.id = i.epic_id
		LEFT JOIN workspaces w ON w.issue_id = i.id
		WHERE `+baseWhere+`
		ORDER BY `+orderBy+`
		LIMIT ? OFFSET ?`, append(args, query.Limit, query.Offset)...)
	if err != nil {
		return nil, 0, IssueStateCounts{}, err
	}
	defer rows.Close()

	out := make([]IssueSummary, 0, query.Limit)
	issueIDs := make([]string, 0, query.Limit)
	for rows.Next() {
		var item IssueSummary
		var projectID, epicID, branchName, prURL, providerIssueRef sql.NullString
		var startedAt, completedAt, lastRun, lastSyncedAt, pendingPlanRequestedAt, pendingPlanRevisionRequestedAt sql.NullTime
		var providerShadow, planApprovalPending int
		var permissionProfile, collaborationModeOverride string
		var projectDesc, epicDesc string
		if err := rows.Scan(
			&item.ID, &projectID, &epicID, &item.Identifier, &item.IssueType, &item.ProviderKind, &providerIssueRef, &providerShadow, &item.Title, &item.Description, &item.State, &item.WorkflowPhase, &permissionProfile, &collaborationModeOverride, &planApprovalPending, &item.PendingPlanMarkdown, &pendingPlanRequestedAt, &item.PendingPlanRevisionMarkdown, &pendingPlanRevisionRequestedAt, &item.Priority,
			&item.AgentName, &item.AgentPrompt, &branchName, &prURL, &item.CreatedAt, &item.UpdatedAt, &item.TotalTokensSpent, &startedAt, &completedAt, &lastSyncedAt,
			&item.ProjectName, &projectDesc, &item.EpicName, &epicDesc, &item.WorkspacePath, &item.WorkspaceRunCount, &lastRun,
		); err != nil {
			return nil, 0, IssueStateCounts{}, err
		}
		item.IssueType = NormalizeIssueType(string(item.IssueType))
		item.PermissionProfile = NormalizePermissionProfile(permissionProfile)
		item.CollaborationModeOverride = NormalizeCollaborationModeOverride(collaborationModeOverride)
		item.PlanApprovalPending = planApprovalPending != 0
		if !item.WorkflowPhase.IsValid() {
			item.WorkflowPhase = DefaultWorkflowPhaseForState(item.State)
		}
		item.ProviderKind = normalizeProviderKind(item.ProviderKind)
		if projectID.Valid {
			item.ProjectID = projectID.String
		}
		if epicID.Valid {
			item.EpicID = epicID.String
		}
		if providerIssueRef.Valid {
			item.ProviderIssueRef = providerIssueRef.String
		}
		item.ProviderShadow = providerShadow != 0
		if branchName.Valid {
			item.BranchName = branchName.String
		}
		if prURL.Valid {
			item.PRURL = prURL.String
		}
		if startedAt.Valid {
			item.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			item.CompletedAt = &completedAt.Time
		}
		if lastSyncedAt.Valid {
			item.LastSyncedAt = &lastSyncedAt.Time
		}
		if pendingPlanRequestedAt.Valid {
			item.PendingPlanRequestedAt = &pendingPlanRequestedAt.Time
		}
		if pendingPlanRevisionRequestedAt.Valid {
			item.PendingPlanRevisionRequestedAt = &pendingPlanRevisionRequestedAt.Time
		}
		if lastRun.Valid {
			item.WorkspaceLastRun = &lastRun.Time
		}
		out = append(out, item)
		issueIDs = append(issueIDs, item.ID)
	}

	labelMap, blockerMap, unresolvedBlockerMap, err := s.issueRelations(issueIDs)
	if err != nil {
		return nil, 0, IssueStateCounts{}, err
	}
	recurrenceMap, err := s.issueRecurrenceMap(issueIDs)
	if err != nil {
		return nil, 0, IssueStateCounts{}, err
	}
	for i := range out {
		out[i].Labels = labelMap[out[i].ID]
		out[i].BlockedBy = blockerMap[out[i].ID]
		out[i].IsBlocked = unresolvedBlockerMap[out[i].ID]
		if recurrence, ok := recurrenceMap[out[i].ID]; ok {
			applyRecurrenceToIssue(&out[i].Issue, &recurrence)
		}
	}
	return out, total, counts, nil
}

func (s *Store) ListIssueSummaries(query IssueQuery) ([]IssueSummary, int, error) {
	items, total, _, err := s.ListIssueSummariesWithCounts(query)
	return items, total, err
}

func (s *Store) GetIssueDetailByIdentifier(identifier string) (*IssueDetail, error) {
	issue, err := s.GetIssueByIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	detail := &IssueDetail{
		IssueSummary: IssueSummary{
			Issue: *issue,
		},
	}
	if issue.ProjectID != "" {
		project, err := s.GetProject(issue.ProjectID)
		switch {
		case err == nil && project != nil:
			detail.ProjectName = project.Name
			detail.ProjectDescription = project.Description
			detail.ProjectPermissionProfile = project.PermissionProfile
		case err != nil && err != sql.ErrNoRows:
			return nil, err
		}
	}
	if issue.EpicID != "" {
		epic, err := s.GetEpic(issue.EpicID)
		switch {
		case err == nil && epic != nil:
			detail.EpicName = epic.Name
			detail.EpicDescription = epic.Description
		case err != nil && err != sql.ErrNoRows:
			return nil, err
		}
	}
	workspace, err := s.GetWorkspace(issue.ID)
	switch {
	case err == nil && workspace != nil:
		detail.WorkspacePath = workspace.Path
		detail.WorkspaceRunCount = workspace.RunCount
		detail.WorkspaceLastRun = workspace.LastRunAt
	case err != nil && err != sql.ErrNoRows:
		return nil, err
	}
	if detail.IsBlocked, err = s.isIssueBlocked(issue.ID); err != nil {
		return nil, err
	}
	assets, err := s.ListIssueAssets(issue.ID)
	if err != nil {
		return nil, err
	}
	detail.Assets = assets
	return detail, nil
}

func (s *Store) AddIssueTokenSpend(issueID string, delta int) error {
	if delta <= 0 {
		return nil
	}
	res, err := s.db.Exec(`UPDATE issues SET total_tokens_spent = total_tokens_spent + ? WHERE id = ?`, delta, issueID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RecomputeIssueTokenSpend(issueID string) (int, error) {
	rows, err := s.db.Query(`
		SELECT total_tokens, payload_json
		FROM runtime_events
		WHERE issue_id = ?
		  AND (
			kind IN ('run_completed', 'run_failed', 'run_unsuccessful', 'run_interrupted')
			OR (kind = 'retry_paused' AND error = 'plan_approval_pending')
		  )`,
		issueID,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	total := 0
	threadTotals := map[string]int{}
	for rows.Next() {
		var (
			eventTotal int
			rawPayload string
		)
		if err := rows.Scan(&eventTotal, &rawPayload); err != nil {
			return 0, err
		}
		threadID := runtimeEventThreadID(rawPayload)
		if threadID == "" {
			total += eventTotal
			continue
		}
		if current := threadTotals[threadID]; eventTotal > current {
			threadTotals[threadID] = eventTotal
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, eventTotal := range threadTotals {
		total += eventTotal
	}
	res, err := s.db.Exec(`UPDATE issues SET total_tokens_spent = ? WHERE id = ?`, total, issueID)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if affected == 0 {
		return 0, sql.ErrNoRows
	}
	return total, nil
}

func runtimeEventThreadID(rawPayload string) string {
	if strings.TrimSpace(rawPayload) == "" {
		return ""
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(rawPayload), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(asString(payload["thread_id"]))
}

func (s *Store) RecomputeProjectIssueTokenSpend(projectID string) (int, error) {
	rows, err := s.db.Query(`SELECT id FROM issues WHERE project_id = ?`, projectID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	recomputed := 0
	for rows.Next() {
		var issueID string
		if err := rows.Scan(&issueID); err != nil {
			return recomputed, err
		}
		if _, err := s.RecomputeIssueTokenSpend(issueID); err != nil {
			return recomputed, err
		}
		recomputed++
	}
	return recomputed, rows.Err()
}

func (s *Store) RecomputeAllIssueTokenSpend() (int, error) {
	rows, err := s.db.Query(`SELECT id FROM issues`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	recomputed := 0
	for rows.Next() {
		var issueID string
		if err := rows.Scan(&issueID); err != nil {
			return recomputed, err
		}
		if _, err := s.RecomputeIssueTokenSpend(issueID); err != nil {
			return recomputed, err
		}
		recomputed++
	}
	return recomputed, rows.Err()
}

func (s *Store) issueRelations(issueIDs []string) (map[string][]string, map[string][]string, map[string]bool, error) {
	labels := map[string][]string{}
	blockers := map[string][]string{}
	unresolved := map[string]bool{}
	if len(issueIDs) == 0 {
		return labels, blockers, unresolved, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(issueIDs)), ",")
	args := make([]interface{}, 0, len(issueIDs))
	for _, id := range issueIDs {
		args = append(args, id)
	}

	labelRows, err := s.db.Query(`SELECT issue_id, label FROM issue_labels WHERE issue_id IN (`+placeholders+`) ORDER BY label`, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer labelRows.Close()
	for labelRows.Next() {
		var issueID, label string
		if err := labelRows.Scan(&issueID, &label); err != nil {
			return nil, nil, nil, err
		}
		labels[issueID] = append(labels[issueID], label)
	}
	if err := labelRows.Err(); err != nil {
		return nil, nil, nil, err
	}

	blockRows, err := s.db.Query(`
		SELECT b.issue_id, b.blocked_by, blocker.state
		FROM issue_blockers b
		LEFT JOIN issues blocker ON blocker.identifier = b.blocked_by
		WHERE b.issue_id IN (`+placeholders+`)
		ORDER BY b.blocked_by`, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer blockRows.Close()
	for blockRows.Next() {
		var issueID, blockedBy string
		var blockerState sql.NullString
		if err := blockRows.Scan(&issueID, &blockedBy, &blockerState); err != nil {
			return nil, nil, nil, err
		}
		blockers[issueID] = append(blockers[issueID], blockedBy)
		if !blockerState.Valid || !isResolvedBlockerState(State(blockerState.String)) {
			unresolved[issueID] = true
		}
	}
	if err := blockRows.Err(); err != nil {
		return nil, nil, nil, err
	}
	return labels, blockers, unresolved, nil
}

func (s *Store) isIssueBlocked(issueID string) (bool, error) {
	var blocked int
	err := s.db.QueryRow(`
		SELECT CASE WHEN `+unresolvedBlockerExistsClause("i")+` THEN 1 ELSE 0 END
		FROM issues i
		WHERE i.id = ?`, issueID,
	).Scan(&blocked)
	if err != nil {
		return false, err
	}
	return blocked != 0, nil
}

func (s *Store) AppendRuntimeEvent(kind string, payload map[string]interface{}) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	if err := s.appendRuntimeEventTx(tx, kind, payload); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) AppendRuntimeEventOnly(kind string, payload map[string]interface{}) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	if err := s.appendRuntimeEventOnlyTx(tx, kind, payload); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) appendRuntimeEventTx(tx *sql.Tx, kind string, payload map[string]interface{}) error {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	ts := time.Now().UTC()
	if raw, ok := payload["ts"].(string); ok && raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			ts = parsed
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO runtime_events (kind, issue_id, identifier, title, attempt, delay_type, input_tokens, output_tokens, total_tokens, error, event_ts, payload_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		kind,
		asString(payload["issue_id"]),
		asString(payload["identifier"]),
		asString(payload["title"]),
		asInt(payload["attempt"]),
		asString(payload["delay_type"]),
		asInt(payload["input_tokens"]),
		asInt(payload["output_tokens"]),
		asInt(payload["total_tokens"]),
		asString(payload["error"]),
		ts,
		string(body),
	)
	if err != nil {
		return err
	}
	return s.appendChangeTx(tx, "runtime_event", asString(payload["issue_id"]), kind, payload)
}

func (s *Store) appendRuntimeEventOnlyTx(tx *sql.Tx, kind string, payload map[string]interface{}) error {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	ts := time.Now().UTC()
	if raw, ok := payload["ts"].(string); ok && raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			ts = parsed
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO runtime_events (kind, issue_id, identifier, title, attempt, delay_type, input_tokens, output_tokens, total_tokens, error, event_ts, payload_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		kind,
		asString(payload["issue_id"]),
		asString(payload["identifier"]),
		asString(payload["title"]),
		asInt(payload["attempt"]),
		asString(payload["delay_type"]),
		asInt(payload["input_tokens"]),
		asInt(payload["output_tokens"]),
		asInt(payload["total_tokens"]),
		asString(payload["error"]),
		ts,
		string(body),
	)
	return err
}

func (s *Store) appendChange(entityType, entityID, action string, payload map[string]interface{}) error {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO change_events (entity_type, entity_id, action, event_ts, payload_json)
		VALUES (?, ?, ?, ?, ?)`,
		entityType,
		entityID,
		action,
		time.Now().UTC(),
		string(body),
	)
	if err != nil {
		return err
	}
	observability.BroadcastUpdate()
	return nil
}

func (s *Store) appendChangeTx(tx *sql.Tx, entityType, entityID, action string, payload map[string]interface{}) error {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO change_events (entity_type, entity_id, action, event_ts, payload_json)
		VALUES (?, ?, ?, ?, ?)`,
		entityType,
		entityID,
		action,
		time.Now().UTC(),
		string(body),
	)
	return err
}

func (s *Store) commitTx(tx *sql.Tx, broadcast bool) error {
	if tx == nil {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if broadcast {
		observability.BroadcastUpdate()
	}
	return nil
}

func (s *Store) setIssueBlockersTx(tx *sql.Tx, issueID string, blockers []string) ([]string, error) {
	current, err := s.getIssueIdentityTx(tx, issueID)
	if err != nil {
		return nil, err
	}

	normalized := normalizeBlockers(blockers)
	validated := make([]string, 0, len(normalized))
	for _, blocker := range normalized {
		blockerIssue, err := s.getIssueIdentityByIdentifierTx(tx, blocker)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("blocker issue not found: %s", blocker)
			}
			return nil, err
		}
		if blockerIssue.ID == current.ID || blockerIssue.Identifier == current.Identifier {
			return nil, fmt.Errorf("issue %s cannot block itself", current.Identifier)
		}
		if blockerIssue.ProjectID != current.ProjectID {
			return nil, fmt.Errorf("blocker %s must belong to the same project", blockerIssue.Identifier)
		}
		validated = append(validated, blockerIssue.Identifier)
	}

	if _, err := tx.Exec(`DELETE FROM issue_blockers WHERE issue_id = ?`, issueID); err != nil {
		return nil, err
	}
	for _, blocker := range validated {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO issue_blockers (issue_id, blocked_by) VALUES (?, ?)`, issueID, blocker); err != nil {
			return nil, err
		}
	}
	return validated, nil
}

type issueIdentity struct {
	ID         string
	Identifier string
	ProjectID  string
}

func (s *Store) getIssueIdentityTx(tx *sql.Tx, issueID string) (*issueIdentity, error) {
	ident := &issueIdentity{}
	var projectID sql.NullString
	err := tx.QueryRow(`SELECT id, identifier, project_id FROM issues WHERE id = ?`, issueID).Scan(&ident.ID, &ident.Identifier, &projectID)
	if err != nil {
		return nil, err
	}
	if projectID.Valid {
		ident.ProjectID = projectID.String
	}
	return ident, nil
}

func (s *Store) getIssueIdentityByIdentifierTx(tx *sql.Tx, identifier string) (*issueIdentity, error) {
	ident := &issueIdentity{}
	var projectID sql.NullString
	err := tx.QueryRow(`SELECT id, identifier, project_id FROM issues WHERE identifier = ?`, identifier).Scan(&ident.ID, &ident.Identifier, &projectID)
	if err != nil {
		return nil, err
	}
	if projectID.Valid {
		ident.ProjectID = projectID.String
	}
	return ident, nil
}

func normalizeBlockers(blockers []string) []string {
	seen := make(map[string]struct{}, len(blockers))
	out := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		blocker = strings.TrimSpace(blocker)
		if blocker == "" {
			continue
		}
		if _, ok := seen[blocker]; ok {
			continue
		}
		seen[blocker] = struct{}{}
		out = append(out, blocker)
	}
	return out
}

func (s *Store) LatestChangeSeq() (int64, error) {
	var seq sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(seq) FROM change_events`).Scan(&seq); err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 0, nil
	}
	return seq.Int64, nil
}

func (s *Store) ListRuntimeEvents(since int64, limit int) ([]RuntimeEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT seq, kind, issue_id, identifier, title, attempt, delay_type, input_tokens, output_tokens, total_tokens, error, event_ts, payload_json
		FROM runtime_events
		WHERE seq > ?
		ORDER BY seq DESC
		LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]RuntimeEvent, 0, limit)
	for rows.Next() {
		var event RuntimeEvent
		var rawPayload string
		if err := rows.Scan(
			&event.Seq, &event.Kind, &event.IssueID, &event.Identifier, &event.Title, &event.Attempt, &event.DelayType,
			&event.InputTokens, &event.OutputTokens, &event.TotalTokens, &event.Error, &event.TS, &rawPayload,
		); err != nil {
			return nil, err
		}
		if rawPayload != "" {
			if err := json.Unmarshal([]byte(rawPayload), &event.Payload); err != nil {
				slog.Warn("Failed to decode runtime event payload", "seq", event.Seq, "kind", event.Kind, "error", err)
			}
		}
		if event.Payload != nil {
			event.Phase = asString(event.Payload["phase"])
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *Store) ListIssueRuntimeEvents(issueID string, limit int) ([]RuntimeEvent, error) {
	if strings.TrimSpace(issueID) == "" {
		return []RuntimeEvent{}, nil
	}
	query := `
		SELECT seq, kind, issue_id, identifier, title, attempt, delay_type, input_tokens, output_tokens, total_tokens, error, event_ts, payload_json
		FROM runtime_events
		WHERE issue_id = ?
			AND kind IN ('run_started', 'run_interrupted', 'run_failed', 'run_unsuccessful', 'retry_scheduled', 'retry_paused', 'manual_retry_requested', 'run_completed', 'workspace_bootstrap_created', 'workspace_bootstrap_reused', 'workspace_bootstrap_preserved', 'workspace_bootstrap_recovery', 'workspace_bootstrap_failed', 'plan_approval_requested', 'plan_approved', 'plan_revision_requested', 'plan_revision_cleared')
		ORDER BY seq DESC`
	var (
		rows *sql.Rows
		err  error
	)
	if limit > 0 && limit <= 200 {
		rows, err = s.db.Query(query+" LIMIT ?", issueID, limit)
	} else {
		rows, err = s.db.Query(query, issueID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]RuntimeEvent, 0)
	for rows.Next() {
		var event RuntimeEvent
		var rawPayload string
		if err := rows.Scan(
			&event.Seq, &event.Kind, &event.IssueID, &event.Identifier, &event.Title, &event.Attempt, &event.DelayType,
			&event.InputTokens, &event.OutputTokens, &event.TotalTokens, &event.Error, &event.TS, &rawPayload,
		); err != nil {
			return nil, err
		}
		if rawPayload != "" {
			if err := json.Unmarshal([]byte(rawPayload), &event.Payload); err != nil {
				slog.Warn("Failed to decode issue runtime event payload", "seq", event.Seq, "issue_id", event.IssueID, "kind", event.Kind, "error", err)
			}
		}
		if event.Payload != nil {
			event.Phase = asString(event.Payload["phase"])
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *Store) UpsertIssueExecutionSession(snapshot ExecutionSessionSnapshot) error {
	if strings.TrimSpace(snapshot.IssueID) == "" {
		return fmt.Errorf("missing issue_id")
	}
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = time.Now().UTC()
	}
	snapshot.AppSession = snapshot.AppSession.Summary()
	body, err := json.Marshal(snapshot.AppSession)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO issue_execution_sessions (issue_id, identifier, phase, attempt, run_kind, error, resume_eligible, stop_reason, updated_at, session_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(issue_id) DO UPDATE SET
			identifier = excluded.identifier,
			phase = excluded.phase,
			attempt = excluded.attempt,
			run_kind = excluded.run_kind,
			error = excluded.error,
			resume_eligible = excluded.resume_eligible,
			stop_reason = excluded.stop_reason,
			updated_at = excluded.updated_at,
			session_json = excluded.session_json`,
		snapshot.IssueID,
		snapshot.Identifier,
		snapshot.Phase,
		snapshot.Attempt,
		snapshot.RunKind,
		snapshot.Error,
		snapshot.ResumeEligible,
		snapshot.StopReason,
		snapshot.UpdatedAt.UTC(),
		string(body),
	)
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) GetIssueExecutionSession(issueID string) (*ExecutionSessionSnapshot, error) {
	if strings.TrimSpace(issueID) == "" {
		return nil, sql.ErrNoRows
	}
	var snapshot ExecutionSessionSnapshot
	var rawSession string
	err := s.db.QueryRow(`
		SELECT issue_id, identifier, phase, attempt, run_kind, error, resume_eligible, stop_reason, updated_at, session_json
		FROM issue_execution_sessions
		WHERE issue_id = ?`, issueID).Scan(
		&snapshot.IssueID,
		&snapshot.Identifier,
		&snapshot.Phase,
		&snapshot.Attempt,
		&snapshot.RunKind,
		&snapshot.Error,
		&snapshot.ResumeEligible,
		&snapshot.StopReason,
		&snapshot.UpdatedAt,
		&rawSession,
	)
	if err != nil {
		return nil, err
	}
	if rawSession != "" {
		if err := json.Unmarshal([]byte(rawSession), &snapshot.AppSession); err != nil {
			return nil, err
		}
	}
	return &snapshot, nil
}

func (s *Store) GetIssuePlanning(issue *Issue) (*IssuePlanning, error) {
	if issue == nil || strings.TrimSpace(issue.ID) == "" {
		return nil, nil
	}
	session, err := s.getLatestIssuePlanSessionTx(s.db, issue.ID, true)
	if err != nil || session == nil {
		return sessionPlanningRecord(session, nil, issue), err
	}
	rows, err := s.db.Query(`
		SELECT id, session_id, version_number, markdown, revision_note, attempt, thread_id, turn_id, created_at
		FROM issue_plan_versions
		WHERE session_id = ?
		ORDER BY version_number ASC`, session.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := make([]IssuePlanVersion, 0)
	for rows.Next() {
		var version IssuePlanVersion
		if err := rows.Scan(
			&version.ID,
			&version.SessionID,
			&version.VersionNumber,
			&version.Markdown,
			&version.RevisionNote,
			&version.Attempt,
			&version.ThreadID,
			&version.TurnID,
			&version.CreatedAt,
		); err != nil {
			return nil, err
		}
		version.CreatedAt = version.CreatedAt.UTC()
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessionPlanningRecord(session, versions, issue), nil
}

func sessionPlanningRecord(session *issuePlanSessionRecord, versions []IssuePlanVersion, issue *Issue) *IssuePlanning {
	if session == nil {
		return nil
	}
	planning := &IssuePlanning{
		SessionID:            session.ID,
		Status:               session.Status,
		CurrentVersionNumber: session.CurrentVersionNumber,
		Versions:             append([]IssuePlanVersion(nil), versions...),
		PendingRevisionNote:  strings.TrimSpace(session.PendingRevisionNote),
		OpenedAt:             session.OpenedAt.UTC(),
		UpdatedAt:            session.UpdatedAt.UTC(),
		ClosedAt:             session.ClosedAt,
		ClosedReason:         strings.TrimSpace(session.ClosedReason),
	}
	if planning.PendingRevisionNote == "" && issue != nil && strings.TrimSpace(issue.PendingPlanRevisionMarkdown) != "" {
		planning.PendingRevisionNote = strings.TrimSpace(issue.PendingPlanRevisionMarkdown)
	}
	if len(planning.Versions) > 0 {
		current := planning.Versions[len(planning.Versions)-1]
		planning.CurrentVersion = &current
		if planning.CurrentVersionNumber == 0 {
			planning.CurrentVersionNumber = current.VersionNumber
		}
	}
	if planning.CurrentVersion == nil && issue != nil && strings.TrimSpace(issue.PendingPlanMarkdown) != "" {
		planning.CurrentVersion = &IssuePlanVersion{
			SessionID:     session.ID,
			VersionNumber: planning.CurrentVersionNumber,
			Markdown:      issue.PendingPlanMarkdown,
		}
	}
	return planning
}

func (s *Store) CreateIssueAgentCommand(issueID, command string, status IssueAgentCommandStatus) (*IssueAgentCommand, error) {
	return s.CreateIssueAgentCommandWithRuntimeEvent(issueID, command, status, "", nil)
}

func (s *Store) CreateIssueAgentCommandWithRuntimeEvent(issueID, command string, status IssueAgentCommandStatus, eventKind string, eventPayload map[string]interface{}) (*IssueAgentCommand, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	record, err := s.createIssueAgentCommandTx(tx, issueID, command, status)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(eventKind) != "" {
		if eventPayload == nil {
			eventPayload = map[string]interface{}{}
		}
		if _, ok := eventPayload["command_id"]; !ok {
			eventPayload["command_id"] = record.ID
		}
		if _, ok := eventPayload["command"]; !ok {
			eventPayload["command"] = record.Command
		}
		if err := s.appendRuntimeEventTx(tx, eventKind, eventPayload); err != nil {
			return nil, err
		}
	}
	if err := s.commitTx(tx, true); err != nil {
		return nil, err
	}
	tx = nil
	return record, nil
}

func (s *Store) createIssueAgentCommandTx(tx *sql.Tx, issueID, command string, status IssueAgentCommandStatus) (*IssueAgentCommand, error) {
	command = strings.TrimSpace(command)
	if strings.TrimSpace(issueID) == "" {
		return nil, fmt.Errorf("missing issue_id")
	}
	if command == "" {
		return nil, validationErrorf("command is required")
	}
	if status == "" {
		status = IssueAgentCommandPending
	}
	now := time.Now().UTC()
	record := &IssueAgentCommand{
		ID:        generateID("cmd"),
		IssueID:   issueID,
		Command:   command,
		Status:    status,
		CreatedAt: now,
	}
	_, err := tx.Exec(`
		INSERT INTO issue_agent_commands (id, issue_id, command, status, created_at, delivered_at, steered_at, delivery_mode, delivery_thread_id, delivery_attempt)
		VALUES (?, ?, ?, ?, ?, NULL, NULL, '', '', 0)`,
		record.ID,
		record.IssueID,
		record.Command,
		record.Status,
		record.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := s.appendChangeTx(tx, "issue_agent_command", issueID, "created", map[string]interface{}{
		"id":       record.ID,
		"issue_id": issueID,
		"status":   record.Status,
	}); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Store) ListIssueAgentCommands(issueID string) ([]IssueAgentCommand, error) {
	rows, err := s.db.Query(`
		SELECT id, issue_id, command, status, created_at, delivered_at, steered_at, delivery_mode, delivery_thread_id, delivery_attempt
		FROM issue_agent_commands
		WHERE issue_id = ?
		ORDER BY created_at DESC`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIssueAgentCommands(rows)
}

func (s *Store) UpdateIssueAgentCommandStatus(id string, status IssueAgentCommandStatus) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("missing id")
	}
	if status == "" {
		return validationErrorf("status is required")
	}
	res, err := s.db.Exec(`UPDATE issue_agent_commands SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue_agent_command", id)
	}
	return nil
}

func (s *Store) ListPendingIssueAgentCommands(issueID string) ([]IssueAgentCommand, error) {
	rows, err := s.db.Query(`
		SELECT id, issue_id, command, status, created_at, delivered_at, steered_at, delivery_mode, delivery_thread_id, delivery_attempt
		FROM issue_agent_commands
		WHERE issue_id = ? AND status = ?
		ORDER BY CASE WHEN steered_at IS NULL THEN 1 ELSE 0 END ASC, steered_at DESC, created_at ASC, id ASC`, issueID, IssueAgentCommandPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIssueAgentCommands(rows)
}

func (s *Store) ActivateIssueAgentCommandsIfDispatchable(issueID string) error {
	if strings.TrimSpace(issueID) == "" {
		return nil
	}
	issue, err := s.GetIssue(issueID)
	if err != nil {
		return err
	}
	if issue.State != StateReady && issue.State != StateInProgress && issue.State != StateInReview {
		return nil
	}
	if issue.PlanApprovalPending {
		return nil
	}
	if strings.TrimSpace(issue.ProjectID) != "" {
		project, err := s.GetProject(issue.ProjectID)
		if err != nil {
			return err
		}
		if project.State != ProjectStateRunning {
			return nil
		}
	}
	unresolved, err := s.unresolvedBlockersForIssue(issueID)
	if err != nil {
		return err
	}
	if len(unresolved) > 0 {
		return nil
	}
	_, err = s.db.Exec(`
		UPDATE issue_agent_commands
		SET status = ?
		WHERE issue_id = ? AND status = ?`,
		IssueAgentCommandPending,
		issueID,
		IssueAgentCommandWaitingForUnblock,
	)
	return err
}

func (s *Store) UnresolvedBlockersForIssue(issueID string) ([]string, error) {
	return s.unresolvedBlockersForIssue(issueID)
}

func (s *Store) MarkIssueAgentCommandsDelivered(issueID string, ids []string, mode, threadID string, attempt int) error {
	if strings.TrimSpace(issueID) == "" || len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	args := make([]interface{}, 0, 5+len(ids))
	args = append(args, IssueAgentCommandDelivered, now, strings.TrimSpace(mode), strings.TrimSpace(threadID), attempt, issueID)
	placeholders := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	if len(placeholders) == 0 {
		return nil
	}
	query := `
		UPDATE issue_agent_commands
		SET status = ?, delivered_at = ?, delivery_mode = ?, delivery_thread_id = ?, delivery_attempt = ?
		WHERE issue_id = ? AND id IN (` + strings.Join(placeholders, ",") + `)`
	if _, err := s.db.Exec(query, args...); err != nil {
		return err
	}
	return s.appendChange("issue_agent_command", issueID, "delivered", map[string]interface{}{
		"ids":                ids,
		"delivery_mode":      strings.TrimSpace(mode),
		"delivery_thread_id": strings.TrimSpace(threadID),
		"delivery_attempt":   attempt,
	})
}

func (s *Store) MarkIssueAgentCommandsDeliveredIfUnchanged(issueID string, commands []IssueAgentCommand, mode, threadID string, attempt int) error {
	if strings.TrimSpace(issueID) == "" || len(commands) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC()
	deliveredIDs := make([]string, 0, len(commands))
	for _, command := range commands {
		commandID := strings.TrimSpace(command.ID)
		commandText := strings.TrimSpace(command.Command)
		if commandID == "" || commandText == "" {
			continue
		}
		res, err := tx.Exec(`
			UPDATE issue_agent_commands
			SET status = ?, delivered_at = ?, delivery_mode = ?, delivery_thread_id = ?, delivery_attempt = ?
			WHERE issue_id = ? AND id = ? AND status = ? AND command = ?`,
			IssueAgentCommandDelivered,
			now,
			strings.TrimSpace(mode),
			strings.TrimSpace(threadID),
			attempt,
			issueID,
			commandID,
			IssueAgentCommandPending,
			commandText,
		)
		if err != nil {
			return err
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return validationErrorf("issue agent command changed before delivery")
		}
		deliveredIDs = append(deliveredIDs, commandID)
	}
	if len(deliveredIDs) == 0 {
		return nil
	}
	if err := s.appendChangeTx(tx, "issue_agent_command", issueID, "delivered", map[string]interface{}{
		"ids":                deliveredIDs,
		"delivery_mode":      strings.TrimSpace(mode),
		"delivery_thread_id": strings.TrimSpace(threadID),
		"delivery_attempt":   attempt,
	}); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) UpdateIssueAgentCommand(issueID, commandID, command string) (*IssueAgentCommand, error) {
	if strings.TrimSpace(issueID) == "" {
		return nil, validationErrorf("issue id is required")
	}
	if strings.TrimSpace(commandID) == "" {
		return nil, validationErrorf("command id is required")
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, validationErrorf("command is required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	res, err := tx.Exec(`
		UPDATE issue_agent_commands
		SET command = ?
		WHERE id = ? AND issue_id = ? AND status IN (?, ?)`,
		command,
		commandID,
		issueID,
		IssueAgentCommandPending,
		IssueAgentCommandWaitingForUnblock,
	)
	if err != nil {
		return nil, err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return nil, notFoundError("issue_agent_command", commandID)
	}

	record, err := scanIssueAgentCommandRow(tx.QueryRow(`
		SELECT id, issue_id, command, status, created_at, delivered_at, steered_at, delivery_mode, delivery_thread_id, delivery_attempt
		FROM issue_agent_commands
		WHERE id = ? AND issue_id = ?`,
		commandID,
		issueID,
	))
	if err != nil {
		return nil, err
	}

	if err := s.appendChangeTx(tx, "issue_agent_command", issueID, "updated", map[string]interface{}{
		"id":       record.ID,
		"issue_id": issueID,
		"command":  record.Command,
		"status":   record.Status,
	}); err != nil {
		return nil, err
	}
	if err := s.commitTx(tx, true); err != nil {
		return nil, err
	}
	tx = nil
	return record, nil
}

func (s *Store) SteerIssueAgentCommand(issueID, commandID string) (*IssueAgentCommand, error) {
	if strings.TrimSpace(issueID) == "" {
		return nil, validationErrorf("issue id is required")
	}
	if strings.TrimSpace(commandID) == "" {
		return nil, validationErrorf("command id is required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC()
	res, err := tx.Exec(`
		UPDATE issue_agent_commands
		SET steered_at = ?
		WHERE id = ? AND issue_id = ? AND status IN (?, ?)`,
		now,
		commandID,
		issueID,
		IssueAgentCommandPending,
		IssueAgentCommandWaitingForUnblock,
	)
	if err != nil {
		return nil, err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return nil, notFoundError("issue_agent_command", commandID)
	}

	record, err := scanIssueAgentCommandRow(tx.QueryRow(`
		SELECT id, issue_id, command, status, created_at, delivered_at, steered_at, delivery_mode, delivery_thread_id, delivery_attempt
		FROM issue_agent_commands
		WHERE id = ? AND issue_id = ?`,
		commandID,
		issueID,
	))
	if err != nil {
		return nil, err
	}

	if err := s.appendChangeTx(tx, "issue_agent_command", issueID, "steered", map[string]interface{}{
		"id":         record.ID,
		"issue_id":   issueID,
		"command":    record.Command,
		"status":     record.Status,
		"steered_at": record.SteeredAt,
	}); err != nil {
		return nil, err
	}
	if err := s.commitTx(tx, true); err != nil {
		return nil, err
	}
	tx = nil
	return record, nil
}

func (s *Store) DeleteIssueAgentCommand(issueID, commandID string) error {
	if strings.TrimSpace(issueID) == "" {
		return validationErrorf("issue id is required")
	}
	if strings.TrimSpace(commandID) == "" {
		return validationErrorf("command id is required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	res, err := tx.Exec(`
		DELETE FROM issue_agent_commands
		WHERE id = ? AND issue_id = ? AND status IN (?, ?)`,
		commandID,
		issueID,
		IssueAgentCommandPending,
		IssueAgentCommandWaitingForUnblock,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return notFoundError("issue_agent_command", commandID)
	}

	if err := s.appendChangeTx(tx, "issue_agent_command", issueID, "deleted", map[string]interface{}{
		"id":       commandID,
		"issue_id": issueID,
	}); err != nil {
		return err
	}
	if err := s.commitTx(tx, true); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *Store) ListRecentExecutionSessions(since time.Time, limit int) ([]ExecutionSessionSnapshot, error) {
	if limit <= 0 {
		limit = 12
	}
	query := `
		SELECT issue_id, identifier, phase, attempt, run_kind, error, resume_eligible, stop_reason, updated_at, session_json
		FROM issue_execution_sessions`
	args := make([]interface{}, 0, 2)
	if !since.IsZero() {
		query += ` WHERE updated_at >= ?`
		args = append(args, since.UTC())
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ExecutionSessionSnapshot, 0, limit)
	for rows.Next() {
		var snapshot ExecutionSessionSnapshot
		var rawSession string
		if err := rows.Scan(
			&snapshot.IssueID,
			&snapshot.Identifier,
			&snapshot.Phase,
			&snapshot.Attempt,
			&snapshot.RunKind,
			&snapshot.Error,
			&snapshot.ResumeEligible,
			&snapshot.StopReason,
			&snapshot.UpdatedAt,
			&rawSession,
		); err != nil {
			return nil, err
		}
		if rawSession != "" {
			if err := json.Unmarshal([]byte(rawSession), &snapshot.AppSession); err != nil {
				return nil, err
			}
		}
		out = append(out, snapshot)
	}
	return out, rows.Err()
}

func scanIssueAgentCommands(rows *sql.Rows) ([]IssueAgentCommand, error) {
	out := []IssueAgentCommand{}
	for rows.Next() {
		record, err := scanIssueAgentCommandRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *record)
	}
	return out, rows.Err()
}

type issueAgentCommandRowScanner interface {
	Scan(dest ...interface{}) error
}

func scanIssueAgentCommandRow(scanner issueAgentCommandRowScanner) (*IssueAgentCommand, error) {
	var record IssueAgentCommand
	var deliveredAt sql.NullTime
	var steeredAt sql.NullTime
	if err := scanner.Scan(
		&record.ID,
		&record.IssueID,
		&record.Command,
		&record.Status,
		&record.CreatedAt,
		&deliveredAt,
		&steeredAt,
		&record.DeliveryMode,
		&record.DeliveryThreadID,
		&record.DeliveryAttempt,
	); err != nil {
		return nil, err
	}
	if deliveredAt.Valid {
		ts := deliveredAt.Time
		record.DeliveredAt = &ts
	}
	if steeredAt.Valid {
		ts := steeredAt.Time
		record.SteeredAt = &ts
	}
	return &record, nil
}

const runtimeSeriesTokenEventKinds = `kind IN ('run_completed', 'run_failed', 'run_unsuccessful', 'run_interrupted') OR (kind = 'retry_paused' AND error = 'plan_approval_pending')`

func (s *Store) RuntimeSeries(hours int) ([]RuntimeSeriesPoint, error) {
	if hours <= 0 {
		hours = 24
	}
	start := time.Now().UTC().Add(-time.Duration(hours-1) * time.Hour).Truncate(time.Hour)

	points := make([]RuntimeSeriesPoint, 0, hours)
	indexByBucket := make(map[string]int, hours)
	for i := 0; i < hours; i++ {
		bucketTime := start.Add(time.Duration(i) * time.Hour)
		bucket := bucketTime.Format("15:04")
		indexByBucket[bucket] = len(points)
		points = append(points, RuntimeSeriesPoint{Bucket: bucket})
	}

	tokenTotalsByThread := map[string]int{}
	seedRows, err := s.db.Query(`
		SELECT kind, issue_id, identifier, attempt, total_tokens, COALESCE(error, ''), event_ts, payload_json
		FROM runtime_events
		WHERE event_ts < ?
		  AND (`+runtimeSeriesTokenEventKinds+`)
		ORDER BY event_ts ASC`, start)
	if err != nil {
		return nil, err
	}
	defer seedRows.Close()
	for seedRows.Next() {
		var (
			kind        string
			issueID     string
			identifier  string
			attempt     int
			totalTokens int
			errText     string
			ts          time.Time
			payload     string
		)
		if err := seedRows.Scan(&kind, &issueID, &identifier, &attempt, &totalTokens, &errText, &ts, &payload); err != nil {
			return nil, err
		}
		key := runtimeSeriesEventKey(issueID, identifier, attempt, payload, kind, ts)
		if current, ok := tokenTotalsByThread[key]; !ok || totalTokens > current {
			tokenTotalsByThread[key] = totalTokens
		}
	}
	if err := seedRows.Err(); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`
		SELECT kind, issue_id, identifier, attempt, total_tokens, COALESCE(error, ''), event_ts, payload_json
		FROM runtime_events
		WHERE event_ts >= ?
		ORDER BY event_ts ASC`, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			kind        string
			issueID     string
			identifier  string
			attempt     int
			totalTokens int
			errText     string
			ts          time.Time
			payload     string
		)
		if err := rows.Scan(&kind, &issueID, &identifier, &attempt, &totalTokens, &errText, &ts, &payload); err != nil {
			return nil, err
		}

		bucket := ts.UTC().Truncate(time.Hour).Format("15:04")
		index, ok := indexByBucket[bucket]
		if !ok {
			continue
		}
		switch kind {
		case "run_started":
			points[index].RunsStarted++
		case "run_completed":
			points[index].RunsCompleted++
		case "run_failed", "run_unsuccessful", "run_interrupted":
			points[index].RunsFailed++
		case "retry_scheduled":
			points[index].Retries++
		}

		if !runtimeSeriesTokenEvent(kind, errText) {
			continue
		}
		key := runtimeSeriesEventKey(issueID, identifier, attempt, payload, kind, ts)
		prev := tokenTotalsByThread[key]
		if totalTokens > prev {
			points[index].Tokens += totalTokens - prev
			tokenTotalsByThread[key] = totalTokens
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return points, nil
}

func runtimeSeriesTokenEvent(kind, errText string) bool {
	switch kind {
	case "run_completed", "run_failed", "run_unsuccessful", "run_interrupted":
		return true
	case "retry_paused":
		return strings.TrimSpace(errText) == "plan_approval_pending"
	default:
		return false
	}
}

func runtimeSeriesEventKey(issueID, identifier string, attempt int, payloadJSON, kind string, ts time.Time) string {
	if threadID := runtimeEventThreadID(payloadJSON); threadID != "" {
		return "thread:" + threadID
	}
	if trimmedIssueID := strings.TrimSpace(issueID); trimmedIssueID != "" {
		return fmt.Sprintf("issue:%s#attempt:%d", trimmedIssueID, attempt)
	}
	if trimmedIdentifier := strings.TrimSpace(identifier); trimmedIdentifier != "" {
		return fmt.Sprintf("identifier:%s#attempt:%d", trimmedIdentifier, attempt)
	}
	return fmt.Sprintf("event:%s#attempt:%d#ts:%s", strings.TrimSpace(kind), attempt, ts.UTC().Format(time.RFC3339Nano))
}

func asString(v interface{}) string {
	switch value := v.(type) {
	case string:
		return value
	default:
		return ""
	}
}

func asInt(v interface{}) int {
	switch value := v.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}
