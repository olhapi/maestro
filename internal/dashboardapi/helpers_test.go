package dashboardapi

import (
	"net/http"
	"testing"
)

func TestNormalizeFailureClass(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "empty", value: "", want: ""},
		{name: "plan approval pending", value: "plan_approval_pending", want: ""},
		{name: "workspace bootstrap", value: "workspace_bootstrap_failed", want: "workspace_bootstrap"},
		{name: "approval required", value: "approval_required", want: "approval_required"},
		{name: "turn input required", value: "turn_input_required", want: "turn_input_required"},
		{name: "stall timeout", value: "stall_timeout", want: "stall_timeout"},
		{name: "run unsuccessful", value: "run_unsuccessful", want: "run_unsuccessful"},
		{name: "unsuccessful alias", value: "unsuccessful", want: "run_unsuccessful"},
		{name: "run failed", value: "run_failed", want: "run_failed"},
		{name: "run interrupted", value: "run_interrupted", want: "run_interrupted"},
		{name: "interrupted alias", value: "interrupted", want: "run_interrupted"},
		{name: "preserves custom", value: "custom_failure", want: "custom_failure"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeFailureClass(tc.value); got != tc.want {
				t.Fatalf("normalizeFailureClass(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestWebhookResponseStatus(t *testing.T) {
	tests := []struct {
		name   string
		result map[string]interface{}
		want   int
	}{
		{name: "accepted", result: map[string]interface{}{"status": "accepted"}, want: http.StatusAccepted},
		{name: "queued", result: map[string]interface{}{"status": "queued_now"}, want: http.StatusAccepted},
		{name: "refresh requested", result: map[string]interface{}{"status": "refresh_requested"}, want: http.StatusAccepted},
		{name: "stopped", result: map[string]interface{}{"status": "stopped"}, want: http.StatusAccepted},
		{name: "pending rerun recorded", result: map[string]interface{}{"status": "pending_rerun_recorded"}, want: http.StatusAccepted},
		{name: "pending rerun already set", result: map[string]interface{}{"status": "pending_rerun_already_set"}, want: http.StatusAccepted},
		{name: "not found", result: map[string]interface{}{"status": "not_found"}, want: http.StatusNotFound},
		{name: "not recurring", result: map[string]interface{}{"status": "not_recurring"}, want: http.StatusConflict},
		{name: "error", result: map[string]interface{}{"status": "error"}, want: http.StatusInternalServerError},
		{name: "default", result: map[string]interface{}{"status": "something_else"}, want: http.StatusOK},
		{name: "missing", result: map[string]interface{}{}, want: http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := webhookResponseStatus(tc.result); got != tc.want {
				t.Fatalf("webhookResponseStatus(%#v) = %d, want %d", tc.result, got, tc.want)
			}
		})
	}
}
