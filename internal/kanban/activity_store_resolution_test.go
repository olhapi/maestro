package kanban

import (
	"strings"
	"testing"

	"github.com/olhapi/maestro/internal/appserver"
)

func TestApplyIssueActivityEventProjectsSubmittedUserInputAsResolved(t *testing.T) {
	store := setupTestStore(t)
	issue, err := store.CreateIssue("", "", "Submitted input timeline", "", 0, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 6, appserver.ActivityEvent{
		Type:      "item.tool.requestUserInput",
		RequestID: "req-input",
		ThreadID:  "thread-6",
		TurnID:    "turn-6",
		Raw: map[string]interface{}{
			"params": map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{"id": "path", "question": "Where should I write the patch?"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent input request: %v", err)
	}

	if err := store.ApplyIssueActivityEvent(issue.ID, issue.Identifier, 6, appserver.ActivityEvent{
		Type:      "item.tool.userInputSubmitted",
		RequestID: "req-input",
		ThreadID:  "thread-6",
		TurnID:    "turn-6",
		Raw: map[string]interface{}{
			"answers": map[string]interface{}{
				"path": []string{"./workspaces/output.patch"},
			},
		},
	}); err != nil {
		t.Fatalf("ApplyIssueActivityEvent input submitted: %v", err)
	}

	entries, err := store.ListIssueActivityEntries(issue.ID)
	if err != nil {
		t.Fatalf("ListIssueActivityEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one merged input row, got %#v", entries)
	}
	entry := entries[0]
	if entry.Status != "input_submitted" || entry.Tone != "success" {
		t.Fatalf("expected submitted input status, got %#v", entry)
	}
	if entry.Summary != "./workspaces/output.patch" {
		t.Fatalf("expected submitted input summary, got %#v", entry)
	}
	if !strings.Contains(entry.Detail, "\"path\"") {
		t.Fatalf("expected submitted input detail payload, got %#v", entry.Detail)
	}
	if entry.CompletedAt == nil {
		t.Fatalf("expected submitted input to record completion, got %#v", entry)
	}
}

func TestApprovalDecisionSummaryAndTone(t *testing.T) {
	for _, tc := range []struct {
		name        string
		decision    string
		wantSummary string
		wantTone    string
	}{
		{
			name:        "approve once",
			decision:    "accept",
			wantSummary: "Operator approved the request once.",
			wantTone:    "success",
		},
		{
			name:        "approve for session",
			decision:    "acceptForSession",
			wantSummary: "Operator approved the request for the rest of the session.",
			wantTone:    "success",
		},
		{
			name:        "approved alias",
			decision:    "approved",
			wantSummary: "Operator approved the request once.",
			wantTone:    "success",
		},
		{
			name:        "approved for session alias",
			decision:    "approved_for_session",
			wantSummary: "Operator approved the request for the rest of the session.",
			wantTone:    "success",
		},
		{
			name:        "exec policy amendment",
			decision:    "accept_with_execpolicy_amendment",
			wantSummary: "Operator approved the request and stored the matching exec rule.",
			wantTone:    "success",
		},
		{
			name:        "allow network policy",
			decision:    "network_policy_allow_api_github_com",
			wantSummary: "Operator approved the request and stored an allow network rule.",
			wantTone:    "success",
		},
		{
			name:        "deny network policy",
			decision:    "network_policy_deny_api_github_com",
			wantSummary: "Operator denied the request and stored a deny network rule.",
			wantTone:    "error",
		},
		{
			name:        "decline",
			decision:    "decline",
			wantSummary: "Operator declined the request and allowed the turn to continue.",
			wantTone:    "error",
		},
		{
			name:        "denied alias",
			decision:    "denied",
			wantSummary: "Operator denied the request and allowed the turn to continue.",
			wantTone:    "error",
		},
		{
			name:        "cancel",
			decision:    "cancel",
			wantSummary: "Operator cancelled the request and interrupted the turn.",
			wantTone:    "error",
		},
		{
			name:        "abort",
			decision:    "abort",
			wantSummary: "Operator aborted the request and interrupted the turn.",
			wantTone:    "error",
		},
		{
			name:        "fallback",
			decision:    "something-else",
			wantSummary: "Operator resolved the request.",
			wantTone:    "error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := approvalDecisionSummary(tc.decision); got != tc.wantSummary {
				t.Fatalf("approvalDecisionSummary(%q) = %q, want %q", tc.decision, got, tc.wantSummary)
			}
			if got := approvalDecisionTone(tc.decision); got != tc.wantTone {
				t.Fatalf("approvalDecisionTone(%q) = %q, want %q", tc.decision, got, tc.wantTone)
			}
		})
	}
}

func TestInputResponseSummaryHandlesCommonAnswerShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  map[string]interface{}
		want string
	}{
		{
			name: "string slices",
			raw: map[string]interface{}{
				"answers": map[string]interface{}{
					"path": []string{"./workspaces/output.patch"},
				},
			},
			want: "./workspaces/output.patch",
		},
		{
			name: "interface slices",
			raw: map[string]interface{}{
				"answers": map[string]interface{}{
					"path": []interface{}{"./workspaces/output.patch"},
				},
			},
			want: "./workspaces/output.patch",
		},
		{
			name: "fallback",
			raw:  map[string]interface{}{},
			want: "Operator submitted input.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inputResponseSummary(tc.raw); got != tc.want {
				t.Fatalf("inputResponseSummary(%#v) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}

	if got := inputResponseDetail(nil); got != "" {
		t.Fatalf("expected nil detail to stay empty, got %q", got)
	}
}
