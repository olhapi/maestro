package dashboardapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const webhookBearerTokenEnv = "MAESTRO_WEBHOOK_BEARER_TOKEN"

type webhookAuthConfig struct {
	bearerToken string
}

type webhookRequest struct {
	Event      string                 `json:"event"`
	DeliveryID string                 `json:"delivery_id"`
	Payload    map[string]interface{} `json:"payload"`
}

func loadWebhookAuthConfig() webhookAuthConfig {
	return webhookAuthConfig{
		bearerToken: strings.TrimSpace(os.Getenv(webhookBearerTokenEnv)),
	}
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.webhook.enabled() {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "webhooks are disabled; set " + webhookBearerTokenEnv,
		})
		return
	}
	if !s.webhook.authorized(r) {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "unauthorized",
		})
		return
	}

	var body webhookRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Event = strings.TrimSpace(body.Event)
	body.DeliveryID = strings.TrimSpace(body.DeliveryID)

	if body.Event == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{"error": "event is required"})
		return
	}

	status, result, err := s.dispatchWebhook(r.Context(), body)
	if err != nil {
		writeJSONStatus(w, status, map[string]interface{}{
			"event":       body.Event,
			"delivery_id": body.DeliveryID,
			"error":       err.Error(),
		})
		return
	}

	writeJSONStatus(w, status, map[string]interface{}{
		"event":       body.Event,
		"delivery_id": body.DeliveryID,
		"received_at": time.Now().UTC().Format(time.RFC3339),
		"result":      result,
	})
}

func (s *Server) dispatchWebhook(ctx context.Context, body webhookRequest) (int, map[string]interface{}, error) {
	switch body.Event {
	case "issue.retry":
		identifier, err := webhookString(body.Payload, "issue_identifier")
		if err != nil {
			return http.StatusBadRequest, nil, err
		}
		result := s.provider.RetryIssueNow(ctx, identifier)
		return webhookResponseStatus(result), result, nil
	case "issue.run_now":
		identifier, err := webhookString(body.Payload, "issue_identifier")
		if err != nil {
			return http.StatusBadRequest, nil, err
		}
		result := s.provider.RunRecurringIssueNow(ctx, identifier)
		return webhookResponseStatus(result), result, nil
	case "project.run":
		projectID, err := webhookString(body.Payload, "project_id")
		if err != nil {
			return http.StatusBadRequest, nil, err
		}
		project, err := s.store.GetProject(projectID)
		if err != nil {
			return http.StatusNotFound, nil, err
		}
		decorateProject(project, scopedRepoPathFromStatus(s.provider.Status()))
		if !project.DispatchReady {
			errText := strings.TrimSpace(project.DispatchError)
			if errText == "" {
				errText = "project is not dispatchable"
			}
			return http.StatusBadRequest, nil, errors.New(errText)
		}
		result := s.provider.RequestProjectRefresh(projectID)
		return webhookResponseStatus(result), result, nil
	case "project.stop":
		projectID, err := webhookString(body.Payload, "project_id")
		if err != nil {
			return http.StatusBadRequest, nil, err
		}
		if _, err := s.store.GetProject(projectID); err != nil {
			return http.StatusNotFound, nil, err
		}
		result := s.provider.StopProjectRuns(projectID)
		return webhookResponseStatus(result), result, nil
	default:
		return http.StatusBadRequest, nil, fmt.Errorf("unsupported event %q", body.Event)
	}
}

func webhookResponseStatus(result map[string]interface{}) int {
	status, _ := result["status"].(string)
	switch status {
	case "accepted", "queued_now", "refresh_requested", "stopped", "pending_rerun_recorded", "pending_rerun_already_set":
		return http.StatusAccepted
	case "not_found":
		return http.StatusNotFound
	case "not_recurring":
		return http.StatusConflict
	case "error":
		return http.StatusInternalServerError
	default:
		return http.StatusOK
	}
}

func webhookString(payload map[string]interface{}, key string) (string, error) {
	raw, ok := payload[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func (c webhookAuthConfig) enabled() bool {
	return c.bearerToken != ""
}

func (c webhookAuthConfig) authorized(r *http.Request) bool {
	return r.Header.Get("Authorization") == "Bearer "+c.bearerToken
}
