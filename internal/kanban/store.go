package kanban

import (
	"database/sql"
	"encoding/json"
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

func (s *Store) UpdateProject(id, name, description string) error {
	_, err := s.db.Exec(`
		UPDATE projects SET name = ?, description = ?, updated_at = ?
		WHERE id = ?`,
		name, description, time.Now(), id,
	)
	return err
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

func (s *Store) GetEpic(id string) (*Epic, error) {
	e := &Epic{}
	err := s.db.QueryRow(`
		SELECT id, project_id, name, description, created_at, updated_at
		FROM epics WHERE id = ?`, id,
	).Scan(&e.ID, &e.ProjectID, &e.Name, &e.Description, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Store) UpdateEpic(id, projectID, name, description string) error {
	_, err := s.db.Exec(`
		UPDATE epics SET project_id = ?, name = ?, description = ?, updated_at = ?
		WHERE id = ?`,
		projectID, name, description, time.Now(), id,
	)
	return err
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
	if projectID, ok := updates["project_id"].(string); ok {
		query += ", project_id = ?"
		args = append(args, projectID)
	}
	if epicID, ok := updates["epic_id"].(string); ok {
		query += ", epic_id = ?"
		args = append(args, epicID)
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
	for rows.Next() {
		var projectID string
		var state State
		var count int
		if err := rows.Scan(&projectID, &state, &count); err != nil {
			return nil, err
		}
		counts := countsByProject[projectID]
		for i := 0; i < count; i++ {
			counts.Add(state)
		}
		countsByProject[projectID] = counts
	}

	out := make([]ProjectSummary, 0, len(projects))
	for _, project := range projects {
		out = append(out, ProjectSummary{
			Project: project,
			Counts:  countsByProject[project.ID],
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
		for i := 0; i < count; i++ {
			counts.Add(State(state.String))
		}
		countsByEpic[epicID] = counts
	}

	out := make([]EpicSummary, 0, len(epics))
	for _, epic := range epics {
		out = append(out, EpicSummary{
			Epic:        epic,
			ProjectName: projectNames[epic.ID],
			Counts:      countsByEpic[epic.ID],
		})
	}
	return out, nil
}

func (s *Store) ListIssueSummaries(query IssueQuery) ([]IssueSummary, int, error) {
	if query.Limit <= 0 || query.Limit > 500 {
		query.Limit = 200
	}
	if query.Offset < 0 {
		query.Offset = 0
	}

	where := []string{"1=1"}
	args := []interface{}{}
	if query.ProjectID != "" {
		where = append(where, "i.project_id = ?")
		args = append(args, query.ProjectID)
	}
	if query.EpicID != "" {
		where = append(where, "i.epic_id = ?")
		args = append(args, query.EpicID)
	}
	if query.State != "" {
		where = append(where, "i.state = ?")
		args = append(args, query.State)
	}
	if query.Search != "" {
		where = append(where, "(i.identifier LIKE ? OR i.title LIKE ? OR i.description LIKE ?)")
		needle := "%" + query.Search + "%"
		args = append(args, needle, needle, needle)
	}

	baseWhere := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM issues i WHERE `+baseWhere, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	orderBy := "i.updated_at DESC, i.created_at DESC"
	switch query.Sort {
	case "created_asc":
		orderBy = "i.created_at ASC"
	case "priority_asc":
		orderBy = "i.priority ASC, i.updated_at DESC"
	case "identifier_asc":
		orderBy = "i.identifier ASC"
	case "state_asc":
		orderBy = "i.state ASC, i.priority ASC"
	}

	rows, err := s.db.Query(`
		SELECT i.id, i.project_id, i.epic_id, i.identifier, i.title, i.description, i.state, i.priority,
		       i.branch_name, i.pr_number, i.pr_url, i.created_at, i.updated_at, i.started_at, i.completed_at,
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
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]IssueSummary, 0, query.Limit)
	issueIDs := make([]string, 0, query.Limit)
	for rows.Next() {
		var item IssueSummary
		var prNumber sql.NullInt32
		var projectID, epicID, branchName, prURL sql.NullString
		var startedAt, completedAt, lastRun sql.NullTime
		var projectDesc, epicDesc string
		if err := rows.Scan(
			&item.ID, &projectID, &epicID, &item.Identifier, &item.Title, &item.Description, &item.State, &item.Priority,
			&branchName, &prNumber, &prURL, &item.CreatedAt, &item.UpdatedAt, &startedAt, &completedAt,
			&item.ProjectName, &projectDesc, &item.EpicName, &epicDesc, &item.WorkspacePath, &item.WorkspaceRunCount, &lastRun,
		); err != nil {
			return nil, 0, err
		}
		if projectID.Valid {
			item.ProjectID = projectID.String
		}
		if epicID.Valid {
			item.EpicID = epicID.String
		}
		if branchName.Valid {
			item.BranchName = branchName.String
		}
		if prURL.Valid {
			item.PRURL = prURL.String
		}
		if prNumber.Valid {
			item.PRNumber = int(prNumber.Int32)
		}
		if startedAt.Valid {
			item.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			item.CompletedAt = &completedAt.Time
		}
		if lastRun.Valid {
			item.WorkspaceLastRun = &lastRun.Time
		}
		out = append(out, item)
		issueIDs = append(issueIDs, item.ID)
	}

	labelMap, blockerMap, err := s.issueRelations(issueIDs)
	if err != nil {
		return nil, 0, err
	}
	for i := range out {
		out[i].Labels = labelMap[out[i].ID]
		out[i].BlockedBy = blockerMap[out[i].ID]
		out[i].IsBlocked = len(out[i].BlockedBy) > 0
	}
	return out, total, nil
}

func (s *Store) GetIssueDetailByIdentifier(identifier string) (*IssueDetail, error) {
	items, _, err := s.ListIssueSummaries(IssueQuery{Search: identifier, Limit: 50})
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Identifier == identifier {
			detail := &IssueDetail{IssueSummary: item}
			if item.ProjectID != "" {
				if project, err := s.GetProject(item.ProjectID); err == nil && project != nil {
					detail.ProjectDescription = project.Description
				}
			}
			if item.EpicID != "" {
				if epic, err := s.GetEpic(item.EpicID); err == nil && epic != nil {
					detail.EpicDescription = epic.Description
				}
			}
			return detail, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (s *Store) issueRelations(issueIDs []string) (map[string][]string, map[string][]string, error) {
	labels := map[string][]string{}
	blockers := map[string][]string{}
	if len(issueIDs) == 0 {
		return labels, blockers, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(issueIDs)), ",")
	args := make([]interface{}, 0, len(issueIDs))
	for _, id := range issueIDs {
		args = append(args, id)
	}

	labelRows, err := s.db.Query(`SELECT issue_id, label FROM issue_labels WHERE issue_id IN (`+placeholders+`) ORDER BY label`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer labelRows.Close()
	for labelRows.Next() {
		var issueID, label string
		if err := labelRows.Scan(&issueID, &label); err != nil {
			return nil, nil, err
		}
		labels[issueID] = append(labels[issueID], label)
	}

	blockRows, err := s.db.Query(`SELECT issue_id, blocked_by FROM issue_blockers WHERE issue_id IN (`+placeholders+`) ORDER BY blocked_by`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer blockRows.Close()
	for blockRows.Next() {
		var issueID, blockedBy string
		if err := blockRows.Scan(&issueID, &blockedBy); err != nil {
			return nil, nil, err
		}
		blockers[issueID] = append(blockers[issueID], blockedBy)
	}
	return labels, blockers, nil
}

func (s *Store) AppendRuntimeEvent(kind string, payload map[string]interface{}) error {
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
	_, err = s.db.Exec(`
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
			_ = json.Unmarshal([]byte(rawPayload), &event.Payload)
		}
		out = append(out, event)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *Store) RuntimeSeries(hours int) ([]RuntimeSeriesPoint, error) {
	if hours <= 0 {
		hours = 24
	}
	start := time.Now().UTC().Add(-time.Duration(hours-1) * time.Hour).Truncate(time.Hour)
	rows, err := s.db.Query(`
		SELECT kind, total_tokens, event_ts
		FROM runtime_events
		WHERE event_ts >= ?
		ORDER BY event_ts ASC`, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]RuntimeSeriesPoint, 0, hours)
	indexByBucket := map[string]int{}
	for i := 0; i < hours; i++ {
		bucketTime := start.Add(time.Duration(i) * time.Hour)
		bucket := bucketTime.Format("15:04")
		indexByBucket[bucket] = len(points)
		points = append(points, RuntimeSeriesPoint{Bucket: bucket})
	}

	for rows.Next() {
		var kind string
		var totalTokens int
		var ts time.Time
		if err := rows.Scan(&kind, &totalTokens, &ts); err != nil {
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
		case "run_failed", "run_unsuccessful":
			points[index].RunsFailed++
		case "retry_scheduled":
			points[index].Retries++
		}
		points[index].Tokens += totalTokens
	}
	return points, nil
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
