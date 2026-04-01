package fake

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/olhapi/maestro/internal/agentruntime"
	runtimefactory "github.com/olhapi/maestro/internal/agentruntime/factory"
)

type Scenario struct {
	Capabilities   agentruntime.Capabilities
	InitialSession agentruntime.Session
	Turns          []Turn
	StartErr       error
	Respond        func(ctx context.Context, interactionID string, response agentruntime.PendingInteractionResponse) error
}

type Turn struct {
	Match func(agentruntime.TurnRequest) error

	StartedSession      *agentruntime.Session
	SessionUpdates      []agentruntime.Session
	Activities          []agentruntime.ActivityEvent
	PendingInteractions []agentruntime.PendingInteraction
	ClearedInteractions []string

	AfterStarted func() error

	FinalSession *agentruntime.Session
	Output       string
	Error        error
}

type ResponseCall struct {
	InteractionID string
	Response      agentruntime.PendingInteractionResponse
}

type Starter struct {
	mu        sync.Mutex
	requests  []runtimefactory.WorkflowStartRequest
	scenarios []Scenario
	clients   []*Client
}

func NewStarter(scenarios ...Scenario) *Starter {
	cloned := make([]Scenario, len(scenarios))
	copy(cloned, scenarios)
	return &Starter{scenarios: cloned}
}

func (s *Starter) Start(ctx context.Context, request runtimefactory.WorkflowStartRequest, observers agentruntime.Observers) (agentruntime.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, cloneRequest(request))
	if len(s.scenarios) == 0 {
		return nil, fmt.Errorf("fake runtime starter exhausted")
	}
	scenario := s.scenarios[0]
	s.scenarios = s.scenarios[1:]
	if scenario.StartErr != nil {
		return nil, scenario.StartErr
	}
	client := NewClient(scenario, observers)
	s.clients = append(s.clients, client)
	return client, nil
}

func (s *Starter) Requests() []runtimefactory.WorkflowStartRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]runtimefactory.WorkflowStartRequest, 0, len(s.requests))
	for _, request := range s.requests {
		out = append(out, cloneRequest(request))
	}
	return out
}

func (s *Starter) Clients() []*Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Client(nil), s.clients...)
}

type Client struct {
	mu sync.Mutex

	scenario  Scenario
	observers agentruntime.Observers
	session   agentruntime.Session
	output    string

	turnIndex         int
	requests          []agentruntime.TurnRequest
	permissionUpdates []agentruntime.PermissionConfig
	responses         []ResponseCall
}

func NewClient(scenario Scenario, observers agentruntime.Observers) *Client {
	session := scenario.InitialSession.Clone()
	if session.MaxHistory <= 0 {
		session.MaxHistory = agentruntime.DefaultSessionHistoryLimit
	}
	return &Client{
		scenario:  scenario,
		observers: observers,
		session:   session,
	}
}

func (c *Client) Capabilities() agentruntime.Capabilities {
	return c.scenario.Capabilities
}

func (c *Client) RunTurn(ctx context.Context, request agentruntime.TurnRequest, onStarted func(*agentruntime.Session)) error {
	c.mu.Lock()
	if c.turnIndex >= len(c.scenario.Turns) {
		c.mu.Unlock()
		return fmt.Errorf("fake runtime exhausted turns")
	}
	turn := c.scenario.Turns[c.turnIndex]
	c.turnIndex++
	c.requests = append(c.requests, cloneTurnRequest(request))
	c.mu.Unlock()

	if turn.Match != nil {
		if err := turn.Match(request); err != nil {
			return err
		}
	}

	started := c.startedSessionForTurn(turn)
	if onStarted != nil {
		cp := started.Clone()
		onStarted(&cp)
	}
	c.emitSession(started)

	if turn.AfterStarted != nil {
		if err := turn.AfterStarted(); err != nil {
			return err
		}
	}
	for _, update := range turn.SessionUpdates {
		c.setSession(update)
		c.emitSession(update)
	}
	for _, activity := range turn.Activities {
		c.emitActivity(activity)
	}
	for _, interaction := range turn.PendingInteractions {
		c.emitPendingInteraction(interaction)
	}
	for _, interactionID := range turn.ClearedInteractions {
		c.emitPendingInteractionDone(interactionID)
	}

	final := c.finalSessionForTurn(turn, started)
	c.setSession(final)
	c.appendOutput(turn.Output)
	c.emitSession(final)
	return turn.Error
}

func (c *Client) UpdatePermissions(config agentruntime.PermissionConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.permissionUpdates = append(c.permissionUpdates, config.Clone())
}

func (c *Client) RespondToInteraction(ctx context.Context, interactionID string, response agentruntime.PendingInteractionResponse) error {
	c.mu.Lock()
	c.responses = append(c.responses, ResponseCall{
		InteractionID: strings.TrimSpace(interactionID),
		Response:      cloneInteractionResponse(response),
	})
	respond := c.scenario.Respond
	c.mu.Unlock()
	if respond == nil {
		c.emitPendingInteractionDone(interactionID)
		return nil
	}
	if err := respond(ctx, interactionID, response); err != nil {
		return err
	}
	c.emitPendingInteractionDone(interactionID)
	return nil
}

func (c *Client) Session() *agentruntime.Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := c.session.Clone()
	return &cp
}

func (c *Client) Output() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output
}

func (c *Client) Close() error {
	return nil
}

func (c *Client) Requests() []agentruntime.TurnRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]agentruntime.TurnRequest, 0, len(c.requests))
	for _, request := range c.requests {
		out = append(out, cloneTurnRequest(request))
	}
	return out
}

func (c *Client) PermissionUpdates() []agentruntime.PermissionConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]agentruntime.PermissionConfig, 0, len(c.permissionUpdates))
	for _, config := range c.permissionUpdates {
		out = append(out, config.Clone())
	}
	return out
}

func (c *Client) Responses() []ResponseCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ResponseCall, 0, len(c.responses))
	for _, response := range c.responses {
		out = append(out, ResponseCall{
			InteractionID: response.InteractionID,
			Response:      cloneInteractionResponse(response.Response),
		})
	}
	return out
}

func (c *Client) startedSessionForTurn(turn Turn) agentruntime.Session {
	if turn.StartedSession != nil {
		session := turn.StartedSession.Clone()
		c.setSession(session)
		return session
	}
	current := c.Session()
	session := agentruntime.Session{}
	if current != nil {
		session = current.Clone()
	}
	session.ResetTurnState()
	session.MaxHistory = agentruntime.DefaultSessionHistoryLimit
	turnNumber := c.turnIndex
	threadID := strings.TrimSpace(session.ThreadID)
	if threadID == "" {
		threadID = fmt.Sprintf("fake-thread-%d", turnNumber)
	}
	turnID := fmt.Sprintf("fake-turn-%d", turnNumber)
	session.ApplyEvent(agentruntime.Event{
		Type:     "turn.started",
		ThreadID: threadID,
		TurnID:   turnID,
	})
	c.setSession(session)
	return session
}

func (c *Client) finalSessionForTurn(turn Turn, started agentruntime.Session) agentruntime.Session {
	if turn.FinalSession != nil {
		return turn.FinalSession.Clone()
	}
	final := started.Clone()
	if strings.TrimSpace(turn.Output) != "" {
		final.ApplyEvent(agentruntime.Event{
			Type:      "item.completed",
			ThreadID:  final.ThreadID,
			TurnID:    final.TurnID,
			ItemID:    "fake-final-answer",
			ItemType:  "agentMessage",
			ItemPhase: "final_answer",
			Message:   strings.TrimSpace(turn.Output),
		})
	}
	terminalType := "turn.completed"
	if turn.Error != nil {
		terminalType = "turn.failed"
	}
	final.ApplyEvent(agentruntime.Event{
		Type:     terminalType,
		ThreadID: final.ThreadID,
		TurnID:   final.TurnID,
		Message:  strings.TrimSpace(turn.Output),
	})
	return final
}

func (c *Client) setSession(session agentruntime.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = session.Clone()
}

func (c *Client) appendOutput(output string) {
	if strings.TrimSpace(output) == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(c.output) == "" {
		c.output = strings.TrimSpace(output)
		return
	}
	c.output = strings.TrimSpace(c.output) + "\n" + strings.TrimSpace(output)
}

func (c *Client) emitSession(session agentruntime.Session) {
	if c.observers.OnSessionUpdate == nil {
		return
	}
	cp := session.Clone()
	c.observers.OnSessionUpdate(&cp)
}

func (c *Client) emitActivity(activity agentruntime.ActivityEvent) {
	if c.observers.OnActivityEvent == nil {
		return
	}
	c.observers.OnActivityEvent(activity.Clone())
}

func (c *Client) emitPendingInteraction(interaction agentruntime.PendingInteraction) {
	if c.observers.OnPendingInteraction == nil {
		return
	}
	cloned := interaction.Clone()
	c.observers.OnPendingInteraction(&cloned, c.RespondToInteraction)
}

func (c *Client) emitPendingInteractionDone(interactionID string) {
	if c.observers.OnPendingInteractionDone == nil {
		return
	}
	c.observers.OnPendingInteractionDone(strings.TrimSpace(interactionID))
}

func cloneRequest(request runtimefactory.WorkflowStartRequest) runtimefactory.WorkflowStartRequest {
	return runtimefactory.WorkflowStartRequest{
		Workflow:        request.Workflow,
		WorkspacePath:   request.WorkspacePath,
		IssueID:         request.IssueID,
		IssueIdentifier: request.IssueIdentifier,
		Env:             append([]string(nil), request.Env...),
		Permissions:     request.Permissions.Clone(),
		DynamicTools:    cloneToolSpecs(request.DynamicTools),
		ToolExecutor:    request.ToolExecutor,
		ResumeToken:     request.ResumeToken,
		Metadata:        cloneJSONMap(request.Metadata),
	}
}

func cloneTurnRequest(request agentruntime.TurnRequest) agentruntime.TurnRequest {
	return agentruntime.TurnRequest{
		Title:    request.Title,
		Input:    cloneInputItems(request.Input),
		Metadata: cloneJSONMap(request.Metadata),
	}
}

func cloneInputItems(items []agentruntime.InputItem) []agentruntime.InputItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]agentruntime.InputItem, 0, len(items))
	for _, item := range items {
		out = append(out, agentruntime.InputItem{
			Kind:     item.Kind,
			Text:     item.Text,
			Path:     item.Path,
			Name:     item.Name,
			Metadata: cloneJSONMap(item.Metadata),
		})
	}
	return out
}

func cloneInteractionResponse(response agentruntime.PendingInteractionResponse) agentruntime.PendingInteractionResponse {
	return agentruntime.PendingInteractionResponse{
		Decision:        response.Decision,
		DecisionPayload: cloneJSONMap(response.DecisionPayload),
		Answers:         cloneAnswers(response.Answers),
		Note:            response.Note,
		Action:          response.Action,
		Content:         cloneJSONValue(response.Content),
	}
}

func cloneToolSpecs(specs []map[string]interface{}) []map[string]interface{} {
	if len(specs) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(specs))
	for _, spec := range specs {
		out = append(out, cloneJSONMap(spec))
	}
	return out
}

func cloneJSONValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return cloneJSONMap(typed)
	case []interface{}:
		cloned := make([]interface{}, len(typed))
		for i := range typed {
			cloned[i] = cloneJSONValue(typed[i])
		}
		return cloned
	default:
		return typed
	}
}

func cloneJSONMap(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneAnswers(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}
	return out
}
