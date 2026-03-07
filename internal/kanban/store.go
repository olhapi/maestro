package kanban

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store manages persistence for the kanban board
type Store struct {
	db *sql.DB
}

// NewStore creates a new store with the given database path
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	return store, nil
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
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
			title TEXT NOT NULL,
			description TEXT,
			state TEXT NOT NULL DEFAULT 'backlog',
			priority INTEGER DEFAULT 0,
			branch_name TEXT,
			pr_number INTEGER,
			pr_url TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			started_at DATETIME,
			completed_at DATETIME,
			FOREIGN KEY (project_id) REFERENCES projects(id),
			FOREIGN KEY (epic_id) REFERENCES epics(id)
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
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return err
		}
	}
	return nil
}

// Project operations

func (s *Store) CreateProject(name, description string) (*Project, error) {
	now := time.Now()
	id := generateID("proj")

	_, err := s.db.Exec(`
		INSERT INTO projects (id, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, name, description, now, now,
	)
	if err != nil {
		return nil, err
	}

	return &Project{ID: id, Name: name, Description: description, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) GetProject(id string) (*Project, error) {
	p := &Project{}
	err := s.db.QueryRow(`
		SELECT id, name, description, created_at, updated_at
		FROM projects WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id, name, description, created_at, updated_at FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		p := Project{}
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func (s *Store) DeleteProject(id string) error {
	_, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, id)
	return err
}

// Epic operations

func (s *Store) CreateEpic(projectID, name, description string) (*Epic, error) {
	now := time.Now()
	id := generateID("epic")

	_, err := s.db.Exec(`
		INSERT INTO epics (id, project_id, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, projectID, name, description, now, now,
	)
	if err != nil {
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
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Name, &e.Description, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		epics = append(epics, e)
	}
	return epics, nil
}

func (s *Store) DeleteEpic(id string) error {
	_, err := s.db.Exec(`DELETE FROM epics WHERE id = ?`, id)
	return err
}

// Issue operations

func (s *Store) generateIdentifier(projectID string) (string, error) {
	// Get project prefix (first 3-4 chars of ID or name)
	var prefix string
	if projectID != "" {
		p, err := s.GetProject(projectID)
		if err == nil && p != nil {
			prefix = strings.ToUpper(strings.ReplaceAll(p.Name, " ", ""))[:min(4, len(p.Name))]
		}
	}
	if prefix == "" {
		prefix = "ISS"
	}

	// Get and increment counter
	var counter int
	err := s.db.QueryRow(`SELECT value FROM counters WHERE name = ?`, prefix).Scan(&counter)
	if err == sql.ErrNoRows {
		counter = 0
		_, err = s.db.Exec(`INSERT INTO counters (name, value) VALUES (?, 1)`, prefix)
	} else if err == nil {
		_, err = s.db.Exec(`UPDATE counters SET value = value + 1 WHERE name = ?`, prefix)
		counter++
	}
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s-%d", prefix, counter+1), nil
}

func (s *Store) CreateIssue(projectID, epicID, title, description string, priority int, labels []string) (*Issue, error) {
	now := time.Now()
	id := generateID("iss")

	identifier, err := s.generateIdentifier(projectID)
	if err != nil {
		return nil, err
	}

	_, err = s.db.Exec(`
		INSERT INTO issues (id, project_id, epic_id, identifier, title, description, state, priority, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, projectID, epicID, identifier, title, description, StateBacklog, priority, now, now,
	)
	if err != nil {
		return nil, err
	}

	// Insert labels
	for _, label := range labels {
		_, _ = s.db.Exec(`INSERT OR IGNORE INTO issue_labels (issue_id, label) VALUES (?, ?)`, id, label)
	}

	return s.GetIssue(id)
}

func (s *Store) GetIssue(id string) (*Issue, error) {
	i := &Issue{}
	var prNumber sql.NullInt32
	var startedAt, completedAt sql.NullTime
	var projectID, epicID, branchName, prURL sql.NullString

	err := s.db.QueryRow(`
		SELECT id, project_id, epic_id, identifier, title, description, state, priority,
		       branch_name, pr_number, pr_url, created_at, updated_at, started_at, completed_at
		FROM issues WHERE id = ?`, id,
	).Scan(&i.ID, &projectID, &epicID, &i.Identifier, &i.Title, &i.Description, &i.State, &i.Priority,
		&branchName, &prNumber, &prURL, &i.CreatedAt, &i.UpdatedAt, &startedAt, &completedAt)
	if err != nil {
		return nil, err
	}

	if projectID.Valid {
		i.ProjectID = projectID.String
	}
	if epicID.Valid {
		i.EpicID = epicID.String
	}
	if branchName.Valid {
		i.BranchName = branchName.String
	}
	if prURL.Valid {
		i.PRURL = prURL.String
	}
	if prNumber.Valid {
		i.PRNumber = int(prNumber.Int32)
	}
	if startedAt.Valid {
		i.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		i.CompletedAt = &completedAt.Time
	}

	// Load labels
	rows, err := s.db.Query(`SELECT label FROM issue_labels WHERE issue_id = ?`, id)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var label string
			if err := rows.Scan(&label); err == nil {
				i.Labels = append(i.Labels, label)
			}
		}
	}

	// Load blockers
	rows, err = s.db.Query(`SELECT blocked_by FROM issue_blockers WHERE issue_id = ?`, id)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var blocker string
			if err := rows.Scan(&blocker); err == nil {
				i.BlockedBy = append(i.BlockedBy, blocker)
			}
		}
	}

	return i, nil
}

func (s *Store) GetIssueByIdentifier(identifier string) (*Issue, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM issues WHERE identifier = ?`, identifier).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetIssue(id)
}

func (s *Store) ListIssues(filter map[string]interface{}) ([]Issue, error) {
	query := `SELECT id FROM issues WHERE 1=1`
	args := []interface{}{}

	if projectID, ok := filter["project_id"].(string); ok && projectID != "" {
		query += " AND project_id = ?"
		args = append(args, projectID)
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

	query += " ORDER BY priority ASC, created_at ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var issues []Issue
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		issue, err := s.GetIssue(id)
		if err != nil {
			return nil, err
		}
		issues = append(issues, *issue)
	}
	return issues, nil
}

func (s *Store) UpdateIssueState(id string, state State) error {
	now := time.Now()
	var startedAt, completedAt interface{}

	if state == StateInProgress {
		startedAt = now
	}
	if state == StateDone || state == StateCancelled {
		completedAt = now
	}

	_, err := s.db.Exec(`
		UPDATE issues SET state = ?, updated_at = ?, started_at = COALESCE(?, started_at), completed_at = COALESCE(?, completed_at)
		WHERE id = ?`,
		state, now, startedAt, completedAt, id,
	)
	return err
}

func (s *Store) UpdateIssue(id string, updates map[string]interface{}) error {
	now := time.Now()

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
	if prNum, ok := updates["pr_number"].(int); ok {
		query += ", pr_number = ?"
		args = append(args, prNum)
	}
	if prURL, ok := updates["pr_url"].(string); ok {
		query += ", pr_url = ?"
		args = append(args, prURL)
	}

	query += " WHERE id = ?"
	args = append(args, id)

	_, err := s.db.Exec(query, args...)

	// Handle labels separately
	if labels, ok := updates["labels"].([]string); ok {
		_, _ = s.db.Exec(`DELETE FROM issue_labels WHERE issue_id = ?`, id)
		for _, label := range labels {
			_, _ = s.db.Exec(`INSERT OR IGNORE INTO issue_labels (issue_id, label) VALUES (?, ?)`, id, label)
		}
	}

	// Handle blockers
	if blockers, ok := updates["blocked_by"].([]string); ok {
		_, _ = s.db.Exec(`DELETE FROM issue_blockers WHERE issue_id = ?`, id)
		for _, blocker := range blockers {
			_, _ = s.db.Exec(`INSERT OR IGNORE INTO issue_blockers (issue_id, blocked_by) VALUES (?, ?)`, id, blocker)
		}
	}

	return err
}

func (s *Store) DeleteIssue(id string) error {
	_, _ = s.db.Exec(`DELETE FROM issue_labels WHERE issue_id = ?`, id)
	_, _ = s.db.Exec(`DELETE FROM issue_blockers WHERE issue_id = ?`, id)
	_, _ = s.db.Exec(`DELETE FROM workspaces WHERE issue_id = ?`, id)
	_, err := s.db.Exec(`DELETE FROM issues WHERE id = ?`, id)
	return err
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
	return err
}

func (s *Store) DeleteWorkspace(issueID string) error {
	_, err := s.db.Exec(`DELETE FROM workspaces WHERE issue_id = ?`, issueID)
	return err
}
