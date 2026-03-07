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
}
