package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type testProvider struct{}

func (testProvider) Status() map[string]interface{} {
	return map[string]interface{}{"active_runs": 1}
}

func (testProvider) LiveSessions() map[string]interface{} {
	return map[string]interface{}{"sessions": map[string]interface{}{"iss-1": map[string]interface{}{"session_id": "th-tu", "terminal": true}}}
}

func TestServerStartsAndServesState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Start(ctx, ":18987", testProvider{})
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:18987/api/v1/state")
	if err != nil {
		t.Fatalf("failed GET state: %v", err)
	}
	defer resp.Body.Close()

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["active_runs"].(float64) != 1 {
		t.Fatalf("unexpected payload: %#v", payload)
	}

	resp2, err := http.Get("http://127.0.0.1:18987/api/v1/sessions")
	if err != nil {
		t.Fatalf("failed GET sessions: %v", err)
	}
	defer resp2.Body.Close()
	var payload2 map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&payload2); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if _, ok := payload2["sessions"]; !ok {
		t.Fatalf("unexpected sessions payload: %#v", payload2)
	}

	resp3, err := http.Get("http://127.0.0.1:18987/api/v1/sessions?issue=iss-1")
	if err != nil {
		t.Fatalf("failed GET session by issue: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp3.StatusCode)
	}

	resp4, err := http.Get("http://127.0.0.1:18987/api/v1/sessions?issue=missing")
	if err != nil {
		t.Fatalf("failed GET missing issue: %v", err)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp4.StatusCode)
	}
}
