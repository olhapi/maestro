package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/agentruntime"
)

func TestClaudeStreamFixtureParsing(t *testing.T) {
	client := newClaudeCoverageClient()
	state := &claudeTurnState{}

	for _, line := range readClaudeFixtureLines(t, "stream_turn.jsonl") {
		client.handleClaudeLine([]byte(line), state, nil)
	}

	if !state.turnStarted || state.turnID != "turn-fixture" {
		t.Fatalf("expected stream fixture to start a turn, got %+v", state)
	}
	if got := strings.TrimSpace(state.streamedOutput.String()); got != "streamed fixture" {
		t.Fatalf("unexpected streamed output from fixture: %q", got)
	}
	if !state.resultSeen || state.resultText != "streamed fixture" {
		t.Fatalf("expected fixture to include a final result, got %+v", state)
	}

	liveSession := client.Session()
	if liveSession == nil {
		t.Fatal("expected live session snapshot")
	}
	if liveSession.InputTokens != 3 || liveSession.OutputTokens != 4 || liveSession.TotalTokens != 7 {
		t.Fatalf("expected live usage snapshot before terminalization, got %+v", liveSession)
	}

	output, terminalType, err := client.finishTurnLocked(state, "", "", nil, nil)
	if err != nil {
		t.Fatalf("finishTurnLocked: %v", err)
	}
	if terminalType != "turn.completed" {
		t.Fatalf("expected completed terminal type, got %q", terminalType)
	}
	if output != "streamed fixture" {
		t.Fatalf("unexpected final output from fixture: %q", output)
	}

	session := client.Session()
	if session == nil {
		t.Fatal("expected session snapshot")
	}
	if session.ThreadID != "claude-session-fixture" || session.SessionID != "claude-session-fixture" {
		t.Fatalf("expected session identity from fixture, got %+v", session)
	}
	if session.Metadata["provider"] != string(agentruntime.ProviderClaude) || session.Metadata["transport"] != string(agentruntime.TransportStdio) {
		t.Fatalf("expected runtime metadata to be preserved, got %+v", session.Metadata)
	}
	if session.Metadata["session_identifier_strategy"] != claudeSessionIdentifierStrategy {
		t.Fatalf("expected session identifier strategy metadata, got %+v", session.Metadata)
	}
	if session.Metadata["provider_session_id"] != "claude-session-fixture" {
		t.Fatalf("expected provider session id metadata, got %+v", session.Metadata)
	}
	if session.Metadata["auth_source"] != "OAuth" {
		t.Fatalf("expected auth source metadata from runtime defaults, got %+v", session.Metadata)
	}
	if session.Metadata["claude_stop_reason"] != "end_turn" {
		t.Fatalf("expected stop reason metadata from fixture, got %+v", session.Metadata)
	}
}

func TestClaudeStreamFixturePublishesLiveUsageBeforeResult(t *testing.T) {
	client := newClaudeCoverageClient()
	state := &claudeTurnState{}
	lines := readClaudeFixtureLines(t, "stream_turn.jsonl")

	for _, line := range lines[:5] {
		client.handleClaudeLine([]byte(line), state, nil)
	}

	liveSession := client.Session()
	if liveSession == nil {
		t.Fatal("expected live session snapshot")
	}
	if liveSession.InputTokens != 3 || liveSession.OutputTokens != 4 || liveSession.TotalTokens != 7 {
		t.Fatalf("expected live usage snapshot before result line, got %+v", liveSession)
	}
	if !state.turnStarted {
		t.Fatalf("expected stream state to have started, got %+v", state)
	}
	if state.resultSeen {
		t.Fatalf("expected result line to remain unread in partial fixture, got %+v", state)
	}
}

func TestClaudeStreamFixtureEmitsLiveUsageObserverUpdate(t *testing.T) {
	sessions := make([]agentruntime.Session, 0, 4)
	client := &stdioClient{
		spec: agentruntime.RuntimeSpec{
			IssueID:         "iss-1",
			IssueIdentifier: "ISS-1",
			Transport:       agentruntime.TransportStdio,
			Permissions: agentruntime.PermissionConfig{
				CollaborationMode: "plan",
			},
		},
		authSource: "OAuth",
		observers: agentruntime.Observers{
			OnSessionUpdate: func(session *agentruntime.Session) {
				if session == nil {
					return
				}
				sessions = append(sessions, session.Clone())
			},
		},
	}
	state := &claudeTurnState{}
	lines := readClaudeFixtureLines(t, "stream_turn.jsonl")

	for _, line := range lines[:5] {
		client.handleClaudeLine([]byte(line), state, nil)
	}

	if len(sessions) < 3 {
		t.Fatalf("expected live session observer updates, got %#v", sessions)
	}
	liveUpdate := sessions[len(sessions)-1]
	if liveUpdate.InputTokens != 3 || liveUpdate.OutputTokens != 4 || liveUpdate.TotalTokens != 7 {
		t.Fatalf("expected live usage observer update before result line, got %+v", liveUpdate)
	}
	if liveUpdate.LastEvent != "item.started" {
		t.Fatalf("expected live usage update to preserve the active turn event, got %+v", liveUpdate)
	}
}

func TestClaudeResumeFixtureMetadata(t *testing.T) {
	client := newClaudeCoverageClient()
	client.spec.ResumeToken = "claude-session-resume"
	client.session = agentruntime.Session{
		IssueID:         "iss-1",
		IssueIdentifier: "ISS-1",
		SessionID:       "claude-session-resume",
		ThreadID:        "claude-session-resume",
		Metadata:        runtimeMetadata("claude-session-resume"),
		MaxHistory:      agentruntime.DefaultSessionHistoryLimit,
	}

	state := &claudeTurnState{}
	for _, line := range readClaudeFixtureLines(t, "resume_turn.jsonl") {
		client.handleClaudeLine([]byte(line), state, nil)
	}

	output, terminalType, err := client.finishTurnLocked(state, "", "", nil, nil)
	if err != nil {
		t.Fatalf("finishTurnLocked: %v", err)
	}
	if terminalType != "turn.completed" {
		t.Fatalf("expected completed terminal type, got %q", terminalType)
	}
	if output != "resumed fixture" {
		t.Fatalf("unexpected output from resume fixture: %q", output)
	}

	session := client.Session()
	if session == nil {
		t.Fatal("expected session snapshot")
	}
	if session.ThreadID != "claude-session-resume" || session.SessionID != "claude-session-resume" {
		t.Fatalf("expected resumed session identity, got %+v", session)
	}
	if session.Metadata["session_identifier_strategy"] != claudeSessionIdentifierStrategy {
		t.Fatalf("expected session identifier strategy metadata, got %+v", session.Metadata)
	}
	if session.Metadata["provider_session_id"] != "claude-session-resume" {
		t.Fatalf("expected resume token metadata to survive, got %+v", session.Metadata)
	}
	if session.Metadata["auth_source"] != "OAuth" {
		t.Fatalf("expected auth source metadata to survive resume, got %+v", session.Metadata)
	}
}

func readClaudeFixtureLines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	rawLines := strings.Split(strings.TrimSpace(string(data)), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
