package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

func TestOutputRenderersCoverTextModes(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	counts := kanban.IssueStateCounts{
		Backlog:    1,
		Ready:      2,
		InProgress: 1,
		InReview:   1,
		Done:       1,
		Cancelled:  1,
	}
	issue := kanban.IssueSummary{
		Issue: kanban.Issue{
			Identifier: "ISS-1",
			Title:      "Ship CLI coverage",
			State:      kanban.StateReady,
			Priority:   2,
		},
		ProjectName: "Platform",
		EpicName:    "CLI",
		IsBlocked:   true,
	}
	project := kanban.ProjectSummary{
		Project: kanban.Project{
			ID:                 "PRJ-1",
			Name:               "Platform",
			Description:        "Core workflows",
			RepoPath:           "/repo/platform",
			WorkflowPath:       "/repo/platform/WORKFLOW.md",
			OrchestrationReady: true,
		},
		Counts: counts,
	}
	epic := kanban.EpicSummary{
		Epic: kanban.Epic{
			ID:          "EPC-1",
			ProjectID:   "PRJ-1",
			Name:        "CLI",
			Description: "Improve text mode",
		},
		ProjectName: "Platform",
		Counts:      counts,
	}
	detail := &kanban.IssueDetail{
		IssueSummary: kanban.IssueSummary{
			Issue: kanban.Issue{
				ID:            "issue-1",
				Identifier:    "ISS-1",
				Title:         "Ship CLI coverage",
				Description:   "Raise coverage in cmd/maestro",
				State:         kanban.StateInProgress,
				WorkflowPhase: kanban.WorkflowPhaseImplementation,
				Priority:      3,
				Labels:        []string{"cli", "coverage"},
				BranchName:    "feat/coverage",
				PRNumber:      17,
				PRURL:         "https://example.com/pr/17",
				BlockedBy:     []string{"ISS-2"},
			},
			ProjectName: "Platform",
			EpicName:    "CLI",
		},
	}

	t.Run("issue table", func(t *testing.T) {
		var quiet bytes.Buffer
		printIssueTable(&quiet, []kanban.IssueSummary{issue}, outputMode{quiet: true})
		if strings.TrimSpace(quiet.String()) != "ISS-1" {
			t.Fatalf("unexpected quiet issue table: %q", quiet.String())
		}

		var wide bytes.Buffer
		printIssueTable(&wide, []kanban.IssueSummary{issue}, outputMode{wide: true})
		text := wide.String()
		for _, want := range []string{"IDENTIFIER", "PROJECT", "Ship CLI coverage"} {
			if !strings.Contains(text, want) {
				t.Fatalf("expected %q in issue table %q", want, text)
			}
		}
	})

	t.Run("project and epic tables", func(t *testing.T) {
		var projectWide bytes.Buffer
		printProjectTable(&projectWide, []kanban.ProjectSummary{project}, outputMode{wide: true})
		if text := projectWide.String(); !strings.Contains(text, "WORKFLOW") || !strings.Contains(text, "Core workflows") {
			t.Fatalf("unexpected project table %q", text)
		}

		var projectQuiet bytes.Buffer
		printProjectTable(&projectQuiet, []kanban.ProjectSummary{project}, outputMode{quiet: true})
		if strings.TrimSpace(projectQuiet.String()) != "PRJ-1" {
			t.Fatalf("unexpected quiet project table: %q", projectQuiet.String())
		}

		var epicWide bytes.Buffer
		printEpicTable(&epicWide, []kanban.EpicSummary{epic}, outputMode{wide: true})
		if text := epicWide.String(); !strings.Contains(text, "Improve text mode") || !strings.Contains(text, "Platform") {
			t.Fatalf("unexpected epic table %q", text)
		}

		var epicQuiet bytes.Buffer
		printEpicTable(&epicQuiet, []kanban.EpicSummary{epic}, outputMode{quiet: true})
		if strings.TrimSpace(epicQuiet.String()) != "EPC-1" {
			t.Fatalf("unexpected quiet epic table: %q", epicQuiet.String())
		}
	})

	t.Run("detail and board", func(t *testing.T) {
		var out bytes.Buffer
		printIssueDetail(&out, detail)
		text := out.String()
		for _, want := range []string{"Description:", "Labels:", "Branch:", "PR:", "Blocked By:"} {
			if !strings.Contains(text, want) {
				t.Fatalf("expected %q in issue detail %q", want, text)
			}
		}

		columns := map[string][]kanban.IssueSummary{
			"backlog":     nil,
			"ready":       {issue},
			"in_progress": {{Issue: kanban.Issue{Identifier: "ISS-2", Title: "Review tests", Priority: 1}, ProjectName: "Platform", EpicName: "CLI"}},
			"in_review":   nil,
			"done":        nil,
			"cancelled":   nil,
		}

		var board bytes.Buffer
		printBoard(&board, columns, counts, outputMode{wide: true})
		boardText := board.String()
		for _, want := range []string{"Board", "READY", "(empty)", "project=Platform"} {
			if !strings.Contains(boardText, want) {
				t.Fatalf("expected %q in board output %q", want, boardText)
			}
		}

		var quiet bytes.Buffer
		printBoard(&quiet, columns, counts, outputMode{quiet: true})
		if lines := strings.Fields(quiet.String()); len(lines) != 2 || lines[0] != "ISS-1" || lines[1] != "ISS-2" {
			t.Fatalf("unexpected quiet board output: %q", quiet.String())
		}
	})

	t.Run("verification output", func(t *testing.T) {
		var typed bytes.Buffer
		printVerification(&typed, "Doctor", map[string]interface{}{
			"checks":      map[string]string{"beta": "ok", "alpha": "ok"},
			"errors":      []string{"missing env"},
			"warnings":    []string{"slow disk"},
			"remediation": map[string]string{"fix": "set env"},
		})
		typedText := typed.String()
		for _, want := range []string{"Doctor", "alpha: ok", "Errors:", "Warnings:", "Remediation:"} {
			if !strings.Contains(typedText, want) {
				t.Fatalf("expected %q in verification output %q", want, typedText)
			}
		}

		var raw bytes.Buffer
		printVerification(&raw, "Spec Check", map[string]interface{}{
			"checks":      map[string]interface{}{"spec": "ok"},
			"errors":      []interface{}{"bad input"},
			"warnings":    []interface{}{"deprecated"},
			"remediation": map[string]interface{}{"retry": "run again"},
		})
		rawText := raw.String()
		for _, want := range []string{"spec: ok", "- bad input", "- deprecated", "- retry: run again"} {
			if !strings.Contains(rawText, want) {
				t.Fatalf("expected %q in raw verification output %q", want, rawText)
			}
		}
	})

	t.Run("dashboard and runtime tables", func(t *testing.T) {
		payload := map[string]interface{}{
			"generated_at": "2026-03-09T12:00:00Z",
			"counts":       map[string]interface{}{"running": 1, "retrying": 1},
			"codex_totals": map[string]interface{}{"total_tokens": 42},
			"running": []interface{}{
				map[string]interface{}{
					"issue_identifier": "ISS-1",
					"state":            "in_progress",
					"session_id":       "sess-1",
					"turn_count":       3,
					"started_at":       "2026-03-09T12:00:00Z",
					"last_event":       "turn.started",
				},
			},
			"retrying": []interface{}{
				map[string]interface{}{
					"issue_identifier": "ISS-2",
					"attempt":          2,
					"due_at":           "2026-03-09T12:05:00Z",
					"error":            "boom",
				},
			},
		}
		text := formatLiveDashboard(payload)
		for _, want := range []string{"MAESTRO STATUS", "running_entries:", "retry_queue:"} {
			if !strings.Contains(text, want) {
				t.Fatalf("expected %q in dashboard output %q", want, text)
			}
		}

		idle := formatLiveDashboard(map[string]interface{}{
			"generated_at": "2026-03-09T12:00:00Z",
			"counts":       map[string]interface{}{"running": 0, "retrying": 0},
			"codex_totals": map[string]interface{}{"total_tokens": 0},
		})
		if !strings.Contains(idle, "running_entries=idle") || !strings.Contains(idle, "retry_queue=empty") {
			t.Fatalf("unexpected idle dashboard output %q", idle)
		}

		sessionsPayload := map[string]interface{}{
			"sessions": map[string]interface{}{
				"ISS-2": map[string]interface{}{"session_id": "sess-2", "last_event": "turn.completed", "last_timestamp": "2026-03-09T12:01:00Z"},
				"ISS-1": map[string]interface{}{"session_id": "sess-1", "last_event": "turn.started", "last_timestamp": "2026-03-09T12:00:00Z"},
			},
		}
		var sessions bytes.Buffer
		printSessions(&sessions, sessionsPayload, outputMode{})
		if text := sessions.String(); !strings.Contains(text, "ISSUE") || !strings.Contains(text, "sess-1") {
			t.Fatalf("unexpected sessions table %q", text)
		}

		var sessionsQuiet bytes.Buffer
		printSessions(&sessionsQuiet, sessionsPayload, outputMode{quiet: true})
		if strings.TrimSpace(sessionsQuiet.String()) != "ISS-1\nISS-2" {
			t.Fatalf("unexpected quiet sessions output %q", sessionsQuiet.String())
		}

		events := []kanban.RuntimeEvent{
			{Seq: 1, TS: now, Kind: "run_started", Identifier: "ISS-1", Attempt: 2, Error: "boom"},
		}
		var eventTable bytes.Buffer
		printRuntimeEvents(&eventTable, events, outputMode{})
		if text := eventTable.String(); !strings.Contains(text, "run_started") || !strings.Contains(text, "2026-03-09T12:00:00Z") {
			t.Fatalf("unexpected runtime events output %q", text)
		}

		var eventQuiet bytes.Buffer
		printRuntimeEvents(&eventQuiet, events, outputMode{quiet: true})
		if strings.TrimSpace(eventQuiet.String()) != "1" {
			t.Fatalf("unexpected quiet runtime events output %q", eventQuiet.String())
		}

		var series bytes.Buffer
		printRuntimeSeries(&series, []kanban.RuntimeSeriesPoint{{
			Bucket:        "12:00",
			RunsStarted:   1,
			RunsCompleted: 1,
			RunsFailed:    0,
			Retries:       0,
			Tokens:        42,
		}})
		if text := series.String(); !strings.Contains(text, "BUCKET") || !strings.Contains(text, "12:00") {
			t.Fatalf("unexpected runtime series output %q", text)
		}
	})
}

func TestErrorHelpersAndAPIClientBranches(t *testing.T) {
	var nilErr *cliError
	if nilErr.Error() != "" || nilErr.Unwrap() != nil {
		t.Fatal("expected nil cliError helpers to be empty")
	}

	baseErr := fmt.Errorf("boom")
	if got := (&cliError{msg: "context", err: baseErr}).Error(); got != "context: boom" {
		t.Fatalf("unexpected combined error %q", got)
	}
	if got := (&cliError{msg: "usage"}).Error(); got != "usage" {
		t.Fatalf("unexpected message-only error %q", got)
	}
	if got := (&cliError{err: baseErr}).Error(); got != "boom" {
		t.Fatalf("unexpected wrapped error %q", got)
	}
	if got := (&cliError{}).Error(); got != "" {
		t.Fatalf("unexpected empty cliError %q", got)
	}

	if exitCode(notFoundErrorf("missing")) != exitCodeNotFound {
		t.Fatal("expected notFoundErrorf exit code")
	}
	if exitCode(runtimeErrorf("broken")) != exitCodeRuntime {
		t.Fatal("expected runtimeErrorf exit code")
	}
	if wrapRuntime(nil, "ignored") != nil {
		t.Fatal("expected nil wrapRuntime when base error is nil")
	}
	if exitCode(fmt.Errorf("%w", kanban.ErrValidation)) != exitCodeUsage {
		t.Fatal("expected kanban validation errors to map to usage")
	}
	if exitCode(fmt.Errorf("%w", kanban.ErrNotFound)) != exitCodeNotFound {
		t.Fatal("expected kanban not found errors to map to not found")
	}
	if exitCode(fmt.Errorf("unknown flag: --bad")) != exitCodeUsage {
		t.Fatal("expected flag parsing errors to map to usage")
	}

	if _, err := newAPIClient(" "); err == nil {
		t.Fatal("expected empty api url to fail")
	}
	if _, err := newAPIClient("http://%zz"); err == nil {
		t.Fatal("expected malformed api url to fail")
	}

	client, err := newAPIClient("127.0.0.1:8080/api/")
	if err != nil {
		t.Fatalf("newAPIClient failed: %v", err)
	}
	if client.baseURL != "http://127.0.0.1:8080/api" {
		t.Fatalf("unexpected normalized baseURL %q", client.baseURL)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/post":
			if r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "missing content-type", http.StatusBadRequest)
				return
			}
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"echo": body["value"]})
		case "/error-empty":
			w.WriteHeader(http.StatusBadGateway)
		case "/error-body":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		case "/bad-json":
			_, _ = w.Write([]byte("{"))
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err = newAPIClient(server.URL)
	if err != nil {
		t.Fatalf("newAPIClient(server) failed: %v", err)
	}

	var getResp map[string]string
	if err := client.get("/ok", &getResp); err != nil || getResp["status"] != "ok" {
		t.Fatalf("unexpected get response: %#v err=%v", getResp, err)
	}

	var postResp map[string]interface{}
	if err := client.post("/post", map[string]interface{}{"value": 17}, &postResp); err != nil || postResp["echo"].(float64) != 17 {
		t.Fatalf("unexpected post response: %#v err=%v", postResp, err)
	}

	if err := client.get("/empty", nil); err != nil {
		t.Fatalf("expected nil output request to succeed: %v", err)
	}
	if err := client.get("/error-empty", &getResp); err == nil || !strings.Contains(err.Error(), "502 Bad Gateway") {
		t.Fatalf("expected empty-body HTTP error, got %v", err)
	}
	if err := client.get("/error-body", &getResp); err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("expected body HTTP error, got %v", err)
	}
	if err := client.get("/bad-json", &getResp); err == nil || !strings.Contains(err.Error(), "decode GET /bad-json response") {
		t.Fatalf("expected decode failure, got %v", err)
	}
}
