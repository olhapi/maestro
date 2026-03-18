package providers

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	filterParts := []string{`project: {slugId: {eq: $projectSlug}}`}
	varDecls := []string{"$projectSlug: String!", "$first: Int!", "$after: String"}
	variables := map[string]interface{}{
		"projectSlug": p.projectSlug(project),
		"first":       100,
		"after":       nil,
	}
	if assigneeID := linearAssigneeFilterValue(assigneeMatcher); assigneeID != "" {
		varDecls = append(varDecls, "$assigneeID: String!")
		filterParts = append(filterParts, `assignee: {id: {eq: $assigneeID}}`)
		variables["assigneeID"] = assigneeID
	}
	if stateName := strings.TrimSpace(query.State); stateName != "" {
		varDecls = append(varDecls, "$stateName: String!")
		filterParts = append(filterParts, `state: {name: {eq: $stateName}}`)
		variables["stateName"] = stateName
	}
	gql := fmt.Sprintf(`
query MaestroLinearIssues(%s) {
  issues(filter: {%s}, first: $first, after: $after) {
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
}`, strings.Join(varDecls, ", "), strings.Join(filterParts, ", "))
	var out []kanban.Issue
	after := ""
	for {
		variables["after"] = nullableString(after)
		body, err := p.graphql(ctx, project, gql, variables)
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

func (p *LinearProvider) ListIssueComments(ctx context.Context, project *kanban.Project, issue *kanban.Issue) ([]kanban.IssueComment, error) {
	if issue == nil || strings.TrimSpace(issue.ProviderIssueRef) == "" {
		return nil, fmt.Errorf("%w: linear issue ref is required", ErrUnsupportedCapability)
	}
	return p.listIssueComments(ctx, project, issue.ProviderIssueRef)
}

func (p *LinearProvider) CreateIssueComment(ctx context.Context, project *kanban.Project, issue *kanban.Issue, input IssueCommentInput) (*kanban.IssueComment, error) {
	if issue == nil || strings.TrimSpace(issue.ProviderIssueRef) == "" {
		return nil, fmt.Errorf("%w: linear issue ref is required", ErrUnsupportedCapability)
	}
	body := strings.TrimSpace(commentBodyValue(input.Body))
	if body == "" && len(input.Attachments) == 0 {
		return nil, fmt.Errorf("%w: comment body or attachments are required", kanban.ErrValidation)
	}
	attachments, err := p.uploadLinearCommentAttachments(ctx, project, input.Attachments)
	if err != nil {
		return nil, err
	}
	renderedBody := renderLinearCommentBody(body, attachments)

	const gql = `
mutation MaestroLinearCreateComment($issueId: String!, $body: String!, $parentCommentId: String) {
  commentCreate(input: {issueId: $issueId, body: $body, parentCommentId: $parentCommentId}) {
    success
    comment {
      id
      body
      createdAt
      updatedAt
      parent { id }
      user { name displayName email }
    }
  }
}`
	bodyMap, err := p.graphql(ctx, project, gql, map[string]interface{}{
		"issueId":         issue.ProviderIssueRef,
		"body":            renderedBody,
		"parentCommentId": nullableString(input.ParentCommentID),
	})
	if err != nil {
		return nil, err
	}
	if raw, ok := getMap(bodyMap, "data", "commentCreate", "comment"); ok {
		comment := p.normalizeIssueComment(raw)
		comment.IssueID = issue.ID
		comment.Attachments = attachments
		comment.ProviderKind = kanban.ProviderKindLinear
		return &comment, nil
	}
	return &kanban.IssueComment{
		IssueID:         issue.ID,
		ParentCommentID: strings.TrimSpace(input.ParentCommentID),
		Body:            body,
		ProviderKind:    kanban.ProviderKindLinear,
		Attachments:     attachments,
	}, nil
}

func (p *LinearProvider) UpdateIssueComment(ctx context.Context, project *kanban.Project, issue *kanban.Issue, commentID string, input IssueCommentInput) (*kanban.IssueComment, error) {
	if issue == nil || strings.TrimSpace(issue.ProviderIssueRef) == "" {
		return nil, fmt.Errorf("%w: linear issue ref is required", ErrUnsupportedCapability)
	}
	current, err := p.getLinearIssueComment(ctx, project, issue.ProviderIssueRef, commentID)
	if err != nil {
		return nil, err
	}
	body := current.Body
	if input.Body != nil {
		body = strings.TrimSpace(commentBodyValue(input.Body))
	}
	attachments := append([]kanban.IssueCommentAttachment(nil), current.Attachments...)
	if len(input.RemoveAttachmentIDs) > 0 {
		filtered := attachments[:0]
		for _, attachment := range attachments {
			if containsString(input.RemoveAttachmentIDs, attachment.ID) {
				continue
			}
			filtered = append(filtered, attachment)
		}
		attachments = filtered
	}
	if body == "" && len(attachments)+len(input.Attachments) == 0 {
		return nil, fmt.Errorf("%w: comment body or attachments are required", kanban.ErrValidation)
	}
	addedAttachments, err := p.uploadLinearCommentAttachments(ctx, project, input.Attachments)
	if err != nil {
		return nil, err
	}
	attachments = append(attachments, addedAttachments...)
	renderedBody := renderLinearCommentBody(body, attachments)

	const gql = `
mutation MaestroLinearUpdateComment($commentId: String!, $body: String!) {
  commentUpdate(id: $commentId, input: {body: $body}) {
    success
    comment {
      id
      body
      createdAt
      updatedAt
      parent { id }
      user { name displayName email }
    }
  }
}`
	bodyMap, err := p.graphql(ctx, project, gql, map[string]interface{}{
		"commentId": commentID,
		"body":      renderedBody,
	})
	if err != nil {
		return nil, err
	}
	if raw, ok := getMap(bodyMap, "data", "commentUpdate", "comment"); ok {
		comment := p.normalizeIssueComment(raw)
		comment.IssueID = issue.ID
		comment.Attachments = attachments
		comment.ProviderKind = kanban.ProviderKindLinear
		return &comment, nil
	}
	updated, err := p.getLinearIssueComment(ctx, project, issue.ProviderIssueRef, commentID)
	if err != nil {
		return nil, err
	}
	updated.IssueID = issue.ID
	return updated, nil
}

func (p *LinearProvider) DeleteIssueComment(ctx context.Context, project *kanban.Project, issue *kanban.Issue, commentID string) error {
	if issue == nil || strings.TrimSpace(issue.ProviderIssueRef) == "" {
		return fmt.Errorf("%w: linear issue ref is required", ErrUnsupportedCapability)
	}
	if _, err := p.getLinearIssueComment(ctx, project, issue.ProviderIssueRef, commentID); err != nil {
		return err
	}
	mutations := []string{
		`
mutation MaestroLinearDeleteComment($commentId: String!) {
  commentDelete(id: $commentId) {
    success
  }
}`,
		`
mutation MaestroLinearArchiveComment($commentId: String!) {
  commentArchive(id: $commentId) {
    success
  }
}`,
	}
	var lastErr error
	for _, gql := range mutations {
		if _, err := p.graphql(ctx, project, gql, map[string]interface{}{"commentId": commentID}); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

func (p *LinearProvider) GetIssueCommentAttachmentContent(ctx context.Context, project *kanban.Project, issue *kanban.Issue, commentID, attachmentID string) (*IssueCommentAttachmentContent, error) {
	if issue == nil || strings.TrimSpace(issue.ProviderIssueRef) == "" {
		return nil, fmt.Errorf("%w: linear issue ref is required", ErrUnsupportedCapability)
	}
	comment, err := p.getLinearIssueComment(ctx, project, issue.ProviderIssueRef, commentID)
	if err != nil {
		return nil, err
	}
	for _, attachment := range comment.Attachments {
		if attachment.ID != attachmentID {
			continue
		}
		attachmentURL, err := p.trustedIssueCommentAttachmentURL(project, attachment.URL)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, attachmentURL.String(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := p.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 400 {
			data, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return nil, fmt.Errorf("linear attachment status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
		}
		return &IssueCommentAttachmentContent{
			Attachment: attachment,
			Content:    resp.Body,
		}, nil
	}
	return nil, kanban.ErrNotFound
}

func (p *LinearProvider) trustedIssueCommentAttachmentURL(project *kanban.Project, raw string) (*url.URL, error) {
	attachmentURL, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid linear attachment url", kanban.ErrValidation)
	}
	if attachmentURL.Scheme == "" || attachmentURL.Host == "" {
		return nil, fmt.Errorf("%w: invalid linear attachment url", kanban.ErrValidation)
	}

	endpointURL, err := url.Parse(p.endpoint(project))
	if err != nil {
		return nil, err
	}
	if !isTrustedLinearAttachmentHost(endpointURL, attachmentURL) {
		return nil, fmt.Errorf("%w: untrusted linear attachment host %q", kanban.ErrValidation, attachmentURL.Host)
	}
	if isLinearOwnedHost(attachmentURL.Hostname()) && !strings.EqualFold(attachmentURL.Scheme, "https") {
		return nil, fmt.Errorf("%w: invalid linear attachment scheme %q", kanban.ErrValidation, attachmentURL.Scheme)
	}
	if strings.EqualFold(attachmentURL.Scheme, "http") || strings.EqualFold(attachmentURL.Scheme, "https") {
		return attachmentURL, nil
	}
	return nil, fmt.Errorf("%w: invalid linear attachment scheme %q", kanban.ErrValidation, attachmentURL.Scheme)
}

func isTrustedLinearAttachmentHost(endpointURL, attachmentURL *url.URL) bool {
	if endpointURL == nil || attachmentURL == nil {
		return false
	}
	if strings.EqualFold(endpointURL.Host, attachmentURL.Host) {
		return true
	}
	return isLinearOwnedHost(endpointURL.Hostname()) && isLinearOwnedHost(attachmentURL.Hostname())
}

func isLinearOwnedHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	return host == "linear.app" || strings.HasSuffix(host, ".linear.app")
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

func linearAssigneeFilterValue(matcher *linearAssigneeMatcher) string {
	if matcher == nil || len(matcher.matchValues) != 1 {
		return ""
	}
	for value := range matcher.matchValues {
		return strings.TrimSpace(value)
	}
	return ""
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

func (p *LinearProvider) listIssueComments(ctx context.Context, project *kanban.Project, issueID string) ([]kanban.IssueComment, error) {
	const gql = `
query MaestroLinearIssueComments($issueId: String!, $after: String) {
  issue(id: $issueId) {
    comments(first: 200, after: $after) {
      nodes {
        id
        body
        createdAt
        updatedAt
        parent { id }
        user { name displayName email }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
}`
	var comments []kanban.IssueComment
	after := ""
	for {
		body, err := p.graphql(ctx, project, gql, map[string]interface{}{
			"issueId": issueID,
			"after":   nullableString(after),
		})
		if err != nil {
			return nil, err
		}
		nodes, _ := getMapSlice(body, "data", "issue", "comments", "nodes")
		for _, node := range nodes {
			comment := p.normalizeIssueComment(node)
			comments = append(comments, comment)
		}
		pageInfo, _ := getMap(body, "data", "issue", "comments", "pageInfo")
		hasNext, _ := pageInfo["hasNextPage"].(bool)
		next, _ := pageInfo["endCursor"].(string)
		if !hasNext || strings.TrimSpace(next) == "" {
			return kanbanCommentsNested(comments), nil
		}
		after = next
	}
}

func (p *LinearProvider) getLinearIssueComment(ctx context.Context, project *kanban.Project, issueID, commentID string) (*kanban.IssueComment, error) {
	comments, err := p.listIssueComments(ctx, project, issueID)
	if err != nil {
		return nil, err
	}
	var walk func(items []kanban.IssueComment) *kanban.IssueComment
	walk = func(items []kanban.IssueComment) *kanban.IssueComment {
		for i := range items {
			if items[i].ID == commentID {
				cp := items[i]
				return &cp
			}
			if result := walk(items[i].Replies); result != nil {
				return result
			}
		}
		return nil
	}
	if comment := walk(comments); comment != nil {
		return comment, nil
	}
	return nil, kanban.ErrNotFound
}

func (p *LinearProvider) uploadLinearCommentAttachments(ctx context.Context, project *kanban.Project, attachments []IssueCommentAttachment) ([]kanban.IssueCommentAttachment, error) {
	if len(attachments) == 0 {
		return nil, nil
	}
	out := make([]kanban.IssueCommentAttachment, 0, len(attachments))
	now := time.Now().UTC()
	for _, attachment := range attachments {
		assetURL, err := p.uploadAttachment(ctx, project, attachment)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(strings.TrimSpace(attachment.Path))
		if err != nil {
			return nil, err
		}
		filename := filepath.Base(strings.TrimSpace(attachment.Path))
		if filename == "." || filename == "" {
			filename = "attachment"
		}
		out = append(out, kanban.IssueCommentAttachment{
			ID:          generateLinearManagedAttachmentID(filename),
			Filename:    filename,
			ContentType: strings.TrimSpace(attachment.ContentType),
			ByteSize:    info.Size(),
			URL:         assetURL,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}
	return out, nil
}

func (p *LinearProvider) normalizeIssueComment(raw map[string]interface{}) kanban.IssueComment {
	plainBody, attachments := parseLinearCommentBody(asString(raw["body"]))
	comment := kanban.IssueComment{
		ID:                 asString(raw["id"]),
		Body:               plainBody,
		ParentCommentID:    asStringNested(raw, "parent", "id"),
		ProviderKind:       kanban.ProviderKindLinear,
		ProviderCommentRef: asString(raw["id"]),
		Attachments:        attachments,
		Author: kanban.IssueCommentAuthor{
			Type:  "user",
			Name:  firstNonEmpty(asStringNested(raw, "user", "displayName"), asStringNested(raw, "user", "name")),
			Email: asStringNested(raw, "user", "email"),
		},
	}
	if createdAt, err := time.Parse(time.RFC3339, asString(raw["createdAt"])); err == nil {
		comment.CreatedAt = createdAt
	}
	if updatedAt, err := time.Parse(time.RFC3339, asString(raw["updatedAt"])); err == nil {
		comment.UpdatedAt = updatedAt
	}
	return comment
}

func (p *LinearProvider) uploadAttachment(ctx context.Context, project *kanban.Project, attachment IssueCommentAttachment) (string, error) {
	path := strings.TrimSpace(attachment.Path)
	if path == "" {
		return "", fmt.Errorf("attachment path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	filename := filepath.Base(path)
	contentType := strings.TrimSpace(attachment.ContentType)
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	const gql = `
mutation MaestroLinearFileUpload($contentType: String!, $filename: String!, $size: Int!) {
  fileUpload(contentType: $contentType, filename: $filename, size: $size) {
    success
    uploadFile {
      uploadUrl
      assetUrl
      headers
    }
  }
}`
	body, err := p.graphql(ctx, project, gql, map[string]interface{}{
		"contentType": contentType,
		"filename":    filename,
		"size":        int(info.Size()),
	})
	if err != nil {
		return "", err
	}
	uploadFile, ok := getMap(body, "data", "fileUpload", "uploadFile")
	if !ok {
		return "", fmt.Errorf("linear file upload returned no upload target")
	}
	uploadURL := strings.TrimSpace(asString(uploadFile["uploadUrl"]))
	assetURL := strings.TrimSpace(asString(uploadFile["assetUrl"]))
	if uploadURL == "" || assetURL == "" {
		return "", fmt.Errorf("linear file upload returned incomplete upload target")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, file)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", contentType)
	for key, value := range flattenHeaders(uploadFile["headers"]) {
		req.Header.Set(key, value)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("linear upload status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return assetURL, nil
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

func flattenHeaders(value interface{}) map[string]string {
	headers := map[string]string{}
	rawHeaders, ok := value.([]interface{})
	if !ok {
		return headers
	}
	for _, raw := range rawHeaders {
		header, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		key := strings.TrimSpace(asString(header["key"]))
		val := strings.TrimSpace(asString(header["value"]))
		if key == "" || val == "" {
			continue
		}
		headers[key] = val
	}
	return headers
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

type linearManagedAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	ByteSize    int64  `json:"byte_size,omitempty"`
	URL         string `json:"url"`
}

func renderLinearCommentBody(body string, attachments []kanban.IssueCommentAttachment) string {
	body = strings.TrimSpace(body)
	if len(attachments) == 0 {
		return body
	}
	metadata := make([]linearManagedAttachment, 0, len(attachments))
	lines := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		metadata = append(metadata, linearManagedAttachment{
			ID:          attachment.ID,
			Filename:    attachment.Filename,
			ContentType: attachment.ContentType,
			ByteSize:    attachment.ByteSize,
			URL:         attachment.URL,
		})
		lines = append(lines, fmt.Sprintf("- [%s](%s)", attachment.Filename, attachment.URL))
	}
	encoded, _ := json.Marshal(metadata)
	section := "<!-- maestro:attachments " + string(encoded) + " -->\nAttachments:\n" + strings.Join(lines, "\n")
	if body == "" {
		return section
	}
	return body + "\n\n" + section
}

func parseLinearCommentBody(body string) (string, []kanban.IssueCommentAttachment) {
	body = strings.TrimSpace(body)
	const prefix = "<!-- maestro:attachments "
	if idx := strings.Index(body, prefix); idx >= 0 {
		metaStart := idx + len(prefix)
		metaEnd := strings.Index(body[metaStart:], " -->")
		if metaEnd >= 0 {
			metaJSON := body[metaStart : metaStart+metaEnd]
			var metadata []linearManagedAttachment
			if err := json.Unmarshal([]byte(metaJSON), &metadata); err == nil {
				attachments := make([]kanban.IssueCommentAttachment, 0, len(metadata))
				for _, item := range metadata {
					attachments = append(attachments, kanban.IssueCommentAttachment{
						ID:          item.ID,
						Filename:    item.Filename,
						ContentType: item.ContentType,
						ByteSize:    item.ByteSize,
						URL:         item.URL,
					})
				}
				return strings.TrimSpace(body[:idx]), attachments
			}
		}
	}
	return parseLinearLegacyCommentBody(body)
}

func parseLinearLegacyCommentBody(body string) (string, []kanban.IssueCommentAttachment) {
	const heading = "Reviewer preview artifacts:\n"
	switch {
	case strings.HasPrefix(body, heading):
		attachments := extractLinearMarkdownAttachments(body)
		if len(attachments) == 0 {
			return body, nil
		}
		return "", attachments
	case strings.Contains(body, "\n\n"+heading):
		idx := strings.Index(body, "\n\n"+heading)
		attachments := extractLinearMarkdownAttachments(body[idx+2:])
		if len(attachments) == 0 {
			return body, nil
		}
		return strings.TrimSpace(body[:idx]), attachments
	default:
		return body, nil
	}
}

func extractLinearMarkdownAttachments(body string) []kanban.IssueCommentAttachment {
	lines := strings.Split(body, "\n")
	attachments := make([]kanban.IssueCommentAttachment, 0, len(lines))
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- [") {
			continue
		}
		closeLabel := strings.Index(line, "](")
		if closeLabel <= 3 {
			continue
		}
		closeURL := strings.LastIndex(line, ")")
		if closeURL <= closeLabel+2 {
			continue
		}
		filename := line[3:closeLabel]
		url := line[closeLabel+2 : closeURL]
		if strings.TrimSpace(url) == "" {
			continue
		}
		attachments = append(attachments, kanban.IssueCommentAttachment{
			ID:       generateLinearLegacyAttachmentID(filename, strings.TrimSpace(url), index),
			Filename: filename,
			URL:      strings.TrimSpace(url),
		})
	}
	return attachments
}

func generateLinearLegacyAttachmentID(filename, url string, index int) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%d:%s:%s", index, strings.TrimSpace(filename), strings.TrimSpace(url))))
	return fmt.Sprintf("lca-legacy-%x", sum[:8])
}

func generateLinearManagedAttachmentID(filename string) string {
	base := strings.ToLower(strings.TrimSpace(filename))
	base = strings.ReplaceAll(base, " ", "-")
	base = strings.ReplaceAll(base, "/", "-")
	if base == "" {
		base = "attachment"
	}
	return fmt.Sprintf("lca-%d-%s", time.Now().UnixNano(), base)
}

func commentBodyValue(body *string) string {
	if body == nil {
		return ""
	}
	return *body
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(needle) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func kanbanCommentsNested(flat []kanban.IssueComment) []kanban.IssueComment {
	if len(flat) == 0 {
		return nil
	}
	byParent := map[string][]kanban.IssueComment{}
	exists := map[string]struct{}{}
	for _, comment := range flat {
		exists[comment.ID] = struct{}{}
	}
	for _, comment := range flat {
		parent := strings.TrimSpace(comment.ParentCommentID)
		if parent != "" {
			if _, ok := exists[parent]; !ok {
				parent = ""
				comment.ParentCommentID = ""
			}
		}
		byParent[parent] = append(byParent[parent], comment)
	}
	var build func(parent string) []kanban.IssueComment
	build = func(parent string) []kanban.IssueComment {
		items := byParent[parent]
		out := make([]kanban.IssueComment, 0, len(items))
		for _, item := range items {
			item.Replies = build(item.ID)
			out = append(out, item)
		}
		return out
	}
	return build("")
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
