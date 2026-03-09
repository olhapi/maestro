package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

const defaultLinearEndpoint = "https://api.linear.app/graphql"

type LinearProvider struct {
	http *http.Client
}

type linearAssigneeMatcher struct {
	matchValues map[string]struct{}
}

func NewLinearProvider() *LinearProvider {
	return &LinearProvider{
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *LinearProvider) Kind() string {
	return kanban.ProviderKindLinear
}

func (p *LinearProvider) Capabilities() kanban.ProviderCapabilities {
	return kanban.DefaultCapabilities(kanban.ProviderKindLinear)
}

func (p *LinearProvider) ValidateProject(_ context.Context, project *kanban.Project) error {
	if strings.TrimSpace(p.projectSlug(project)) == "" {
		return fmt.Errorf("%w: linear project_slug is required", ErrUnsupportedCapability)
	}
	if strings.TrimSpace(os.Getenv("LINEAR_API_KEY")) == "" {
		return fmt.Errorf("missing LINEAR_API_KEY")
	}
	return nil
}

func (p *LinearProvider) ListIssues(ctx context.Context, project *kanban.Project, query kanban.IssueQuery) ([]kanban.Issue, error) {
	assigneeMatcher, err := p.resolveAssigneeMatcher(ctx, project, strings.TrimSpace(query.Assignee))
	if err != nil {
		return nil, err
	}
	const gql = `
query MaestroLinearIssues($projectSlug: String!, $first: Int!, $after: String) {
  issues(filter: {project: {slugId: {eq: $projectSlug}}}, first: $first, after: $after) {
    nodes {
      id
      identifier
      title
      description
      priority
      branchName
      createdAt
      updatedAt
      assignee { id }
      state { name }
      labels { nodes { name } }
      inverseRelations(first: 50) {
        nodes {
          type
          issue {
            identifier
          }
        }
      }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}`
	var out []kanban.Issue
	after := ""
	for {
		body, err := p.graphql(ctx, project, gql, map[string]interface{}{
			"projectSlug": p.projectSlug(project),
			"first":       100,
			"after":       nullableString(after),
		})
		if err != nil {
			return nil, err
		}
		nodes, _ := getMapSlice(body, "data", "issues", "nodes")
		for _, node := range nodes {
			if !matchesAssigneeRaw(node, assigneeMatcher) {
				continue
			}
			issue := p.normalizeIssue(node)
			if !matchesIssueQuery(issue, query) {
				continue
			}
			if project != nil {
				issue.ProjectID = project.ID
			}
			out = append(out, issue)
		}
		pageInfo, _ := getMap(body, "data", "issues", "pageInfo")
		hasNext, _ := pageInfo["hasNextPage"].(bool)
		next, _ := pageInfo["endCursor"].(string)
		if !hasNext || strings.TrimSpace(next) == "" {
			return out, nil
		}
		after = next
	}
}

func (p *LinearProvider) GetIssue(ctx context.Context, project *kanban.Project, identifier string) (*kanban.Issue, error) {
	const gql = `
query MaestroLinearIssue($projectSlug: String!, $identifier: String!) {
  issues(filter: {project: {slugId: {eq: $projectSlug}}, identifier: {eq: $identifier}}, first: 1) {
    nodes {
      id
      identifier
      title
      description
      priority
      branchName
      createdAt
      updatedAt
      assignee { id }
      state { name }
      labels { nodes { name } }
      inverseRelations(first: 50) {
        nodes {
          type
          issue {
            identifier
          }
        }
      }
    }
  }
}`
	body, err := p.graphql(ctx, project, gql, map[string]interface{}{
		"projectSlug": p.projectSlug(project),
		"identifier":  identifier,
	})
	if err != nil {
		return nil, err
	}
	nodes, _ := getMapSlice(body, "data", "issues", "nodes")
	if len(nodes) == 0 {
		return nil, fmt.Errorf("%w: linear issue %q", kanban.ErrNotFound, identifier)
	}
	issue := p.normalizeIssue(nodes[0])
	if project != nil {
		issue.ProjectID = project.ID
	}
	return &issue, nil
}

func (p *LinearProvider) CreateIssue(ctx context.Context, project *kanban.Project, input IssueCreateInput) (*kanban.Issue, error) {
	if len(input.Labels) > 0 || len(input.BlockedBy) > 0 {
		return nil, fmt.Errorf("%w: linear labels/blockers create is not supported in v1", ErrUnsupportedCapability)
	}
	projectID, err := p.resolveProjectID(ctx, project)
	if err != nil {
		return nil, err
	}
	const gql = `
mutation MaestroLinearCreateIssue($projectId: String!, $title: String!, $description: String!, $priority: Float!, $branchName: String) {
  issueCreate(input: {projectId: $projectId, title: $title, description: $description, priority: $priority, branchName: $branchName}) {
    success
    issue {
      id
      identifier
      title
      description
      priority
      branchName
      createdAt
      updatedAt
      assignee { id }
      state { name }
      labels { nodes { name } }
      inverseRelations(first: 50) { nodes { type issue { identifier } } }
    }
  }
}`
	body, err := p.graphql(ctx, project, gql, map[string]interface{}{
		"projectId":   projectID,
		"title":       input.Title,
		"description": input.Description,
		"priority":    input.Priority,
		"branchName":  nullableString(input.BranchName),
	})
	if err != nil {
		return nil, err
	}
	issueNode, ok := getMap(body, "data", "issueCreate", "issue")
	if !ok {
		return nil, fmt.Errorf("linear issue creation returned no issue")
	}
	issue := p.normalizeIssue(issueNode)
	if project != nil {
		issue.ProjectID = project.ID
	}
	if state := strings.TrimSpace(input.State); state != "" && !strings.EqualFold(state, string(issue.State)) {
		return p.SetIssueState(ctx, project, &issue, state)
	}
	return &issue, nil
}

func (p *LinearProvider) UpdateIssue(ctx context.Context, project *kanban.Project, issue *kanban.Issue, updates map[string]interface{}) (*kanban.Issue, error) {
	for _, key := range []string{"project_id", "epic_id", "blocked_by"} {
		if value, ok := updates[key]; ok && !isZeroValue(value) {
			return nil, fmt.Errorf("%w: linear %s updates are not supported in v1", ErrUnsupportedCapability, key)
		}
	}
	if value, ok := updates["labels"]; ok {
		if labels, ok := value.([]string); ok && len(labels) > 0 {
			return nil, fmt.Errorf("%w: linear labels updates are not supported in v1", ErrUnsupportedCapability)
		}
	}
	input := map[string]interface{}{}
	if value, ok := updates["title"].(string); ok && strings.TrimSpace(value) != "" {
		input["title"] = value
	}
	if value, ok := updates["description"].(string); ok {
		input["description"] = value
	}
	if value, ok := updates["priority"].(int); ok {
		input["priority"] = value
	}
	if value, ok := updates["branch_name"].(string); ok {
		input["branchName"] = nullableString(value)
	}
	if len(input) == 0 {
		return issue, nil
	}
	const gql = `
mutation MaestroLinearUpdateIssue($issueId: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $issueId, input: $input) {
    success
  }
}`
	if _, err := p.graphql(ctx, project, gql, map[string]interface{}{
		"issueId": issue.ProviderIssueRef,
		"input":   input,
	}); err != nil {
		return nil, err
	}
	return p.GetIssue(ctx, project, issue.Identifier)
}

func (p *LinearProvider) DeleteIssue(ctx context.Context, project *kanban.Project, issue *kanban.Issue) error {
	const gql = `
mutation MaestroLinearArchiveIssue($issueId: String!) {
  issueArchive(id: $issueId) {
    success
  }
}`
	_, err := p.graphql(ctx, project, gql, map[string]interface{}{"issueId": issue.ProviderIssueRef})
	return err
}

func (p *LinearProvider) SetIssueState(ctx context.Context, project *kanban.Project, issue *kanban.Issue, state string) (*kanban.Issue, error) {
	stateID, err := p.resolveStateID(ctx, project, issue.ProviderIssueRef, state)
	if err != nil {
		return nil, err
	}
	const gql = `
mutation MaestroLinearUpdateIssueState($issueId: String!, $stateId: String!) {
  issueUpdate(id: $issueId, input: {stateId: $stateId}) {
    success
  }
}`
	if _, err := p.graphql(ctx, project, gql, map[string]interface{}{
		"issueId": issue.ProviderIssueRef,
		"stateId": stateID,
	}); err != nil {
		return nil, err
	}
	return p.GetIssue(ctx, project, issue.Identifier)
}

func (p *LinearProvider) endpoint(project *kanban.Project) string {
	if project != nil {
		if value, ok := project.ProviderConfig["endpoint"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return defaultLinearEndpoint
}

func (p *LinearProvider) projectSlug(project *kanban.Project) string {
	if project == nil {
		return ""
	}
	if strings.TrimSpace(project.ProviderProjectRef) != "" {
		return strings.TrimSpace(project.ProviderProjectRef)
	}
	if value, ok := project.ProviderConfig["project_slug"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func (p *LinearProvider) resolveProjectID(ctx context.Context, project *kanban.Project) (string, error) {
	const gql = `
query MaestroLinearProject($projectSlug: String!) {
  projects(filter: {slugId: {eq: $projectSlug}}, first: 1) {
    nodes { id }
  }
}`
	body, err := p.graphql(ctx, project, gql, map[string]interface{}{"projectSlug": p.projectSlug(project)})
	if err != nil {
		return "", err
	}
	nodes, _ := getMapSlice(body, "data", "projects", "nodes")
	if len(nodes) == 0 {
		return "", fmt.Errorf("linear project not found for slug %s", p.projectSlug(project))
	}
	id, _ := nodes[0]["id"].(string)
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("linear project lookup returned empty id")
	}
	return id, nil
}

func (p *LinearProvider) resolveStateID(ctx context.Context, project *kanban.Project, issueID, stateName string) (string, error) {
	const gql = `
query MaestroLinearState($issueId: String!, $stateName: String!) {
  issue(id: $issueId) {
    team {
      states(filter: {name: {eq: $stateName}}, first: 1) {
        nodes { id }
      }
    }
  }
}`
	body, err := p.graphql(ctx, project, gql, map[string]interface{}{
		"issueId":   issueID,
		"stateName": stateName,
	})
	if err != nil {
		return "", err
	}
	nodes, _ := getMapSlice(body, "data", "issue", "team", "states", "nodes")
	if len(nodes) == 0 {
		return "", fmt.Errorf("linear state not found: %s", stateName)
	}
	stateID, _ := nodes[0]["id"].(string)
	if strings.TrimSpace(stateID) == "" {
		return "", fmt.Errorf("linear state lookup returned empty id")
	}
	return stateID, nil
}

func (p *LinearProvider) resolveAssigneeMatcher(ctx context.Context, project *kanban.Project, assignee string) (*linearAssigneeMatcher, error) {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return nil, nil
	}
	if strings.EqualFold(assignee, "me") {
		viewerID, err := p.resolveViewerID(ctx, project)
		if err != nil {
			return nil, err
		}
		if viewerID == "" {
			return nil, fmt.Errorf("linear viewer lookup returned empty id")
		}
		return &linearAssigneeMatcher{matchValues: map[string]struct{}{viewerID: {}}}, nil
	}
	return &linearAssigneeMatcher{matchValues: map[string]struct{}{assignee: {}}}, nil
}

func (p *LinearProvider) resolveViewerID(ctx context.Context, project *kanban.Project) (string, error) {
	const gql = `
query MaestroLinearViewer {
  viewer {
    id
  }
}`
	body, err := p.graphql(ctx, project, gql, map[string]interface{}{})
	if err != nil {
		return "", err
	}
	viewer, ok := getMap(body, "data", "viewer")
	if !ok {
		return "", fmt.Errorf("linear viewer lookup returned no viewer")
	}
	return strings.TrimSpace(asString(viewer["id"])), nil
}

func (p *LinearProvider) normalizeIssue(raw map[string]interface{}) kanban.Issue {
	issue := kanban.Issue{
		ProviderKind:     kanban.ProviderKindLinear,
		ProviderIssueRef: asString(raw["id"]),
		Identifier:       asString(raw["identifier"]),
		Title:            asString(raw["title"]),
		Description:      asString(raw["description"]),
		State:            kanban.State(asStringNested(raw, "state", "name")),
		Priority:         asInt(raw["priority"]),
		BranchName:       asString(raw["branchName"]),
		ProviderShadow:   true,
	}
	if labels, ok := getMapSlice(raw, "labels", "nodes"); ok {
		for _, label := range labels {
			name := asString(label["name"])
			if name != "" {
				issue.Labels = append(issue.Labels, name)
			}
		}
	}
	if relations, ok := getMapSlice(raw, "inverseRelations", "nodes"); ok {
		for _, relation := range relations {
			if !isBlocksRelation(relation) {
				continue
			}
			identifier := asStringNested(relation, "issue", "identifier")
			if identifier != "" {
				issue.BlockedBy = append(issue.BlockedBy, identifier)
			}
		}
	}
	if createdAt, err := time.Parse(time.RFC3339, asString(raw["createdAt"])); err == nil {
		issue.CreatedAt = createdAt
	}
	if updatedAt, err := time.Parse(time.RFC3339, asString(raw["updatedAt"])); err == nil {
		issue.UpdatedAt = updatedAt
	}
	now := time.Now().UTC()
	issue.LastSyncedAt = &now
	return issue
}

func matchesAssigneeRaw(raw map[string]interface{}, matcher *linearAssigneeMatcher) bool {
	if matcher == nil {
		return true
	}
	assigneeID := strings.TrimSpace(asStringNested(raw, "assignee", "id"))
	if assigneeID == "" {
		return false
	}
	_, ok := matcher.matchValues[assigneeID]
	return ok
}

func isBlocksRelation(relation map[string]interface{}) bool {
	return strings.EqualFold(strings.TrimSpace(asString(relation["type"])), "blocks")
}

func (p *LinearProvider) graphql(ctx context.Context, project *kanban.Project, query string, variables map[string]interface{}) (map[string]interface{}, error) {
	token := strings.TrimSpace(os.Getenv("LINEAR_API_KEY"))
	if token == "" {
		return nil, fmt.Errorf("missing LINEAR_API_KEY")
	}
	payload, err := json.Marshal(map[string]interface{}{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(project), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("linear api status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var body map[string]interface{}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	if rawErrors, ok := body["errors"].([]interface{}); ok && len(rawErrors) > 0 {
		return nil, fmt.Errorf("linear graphql errors: %v", rawErrors)
	}
	return body, nil
}

func matchesIssueQuery(issue kanban.Issue, query kanban.IssueQuery) bool {
	if query.State != "" && !strings.EqualFold(string(issue.State), query.State) {
		return false
	}
	if query.Search != "" {
		needle := strings.ToLower(strings.TrimSpace(query.Search))
		haystacks := []string{strings.ToLower(issue.Identifier), strings.ToLower(issue.Title), strings.ToLower(issue.Description)}
		found := false
		for _, haystack := range haystacks {
			if strings.Contains(haystack, needle) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func nullableString(value string) interface{} {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func getMap(root map[string]interface{}, path ...string) (map[string]interface{}, bool) {
	current := root
	for _, key := range path {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func getMapSlice(root map[string]interface{}, path ...string) ([]map[string]interface{}, bool) {
	current := root
	for i, key := range path {
		if i == len(path)-1 {
			raw, ok := current[key].([]interface{})
			if !ok {
				return nil, false
			}
			out := make([]map[string]interface{}, 0, len(raw))
			for _, item := range raw {
				if mapped, ok := item.(map[string]interface{}); ok {
					out = append(out, mapped)
				}
			}
			return out, true
		}
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}

func asString(value interface{}) string {
	return strings.TrimSpace(fmt.Sprint(value))
}

func asStringNested(root map[string]interface{}, path ...string) string {
	current := root
	for i, key := range path {
		value, ok := current[key]
		if !ok {
			return ""
		}
		if i == len(path)-1 {
			return asString(value)
		}
		next, ok := value.(map[string]interface{})
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

func asInt(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func isZeroValue(value interface{}) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []string:
		return len(typed) == 0
	case int:
		return typed == 0
	case nil:
		return true
	default:
		return false
	}
}
