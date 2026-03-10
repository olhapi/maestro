package dashboardapi

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type projectControlProvider struct {
	testProvider
	refreshed []string
	stopped   []string
}

func (p *projectControlProvider) RequestProjectRefresh(projectID string) map[string]interface{} {
	p.refreshed = append(p.refreshed, projectID)
	return map[string]interface{}{"status": "accepted", "project_id": projectID}
}

func (p *projectControlProvider) StopProjectRuns(projectID string) map[string]interface{} {
	p.stopped = append(p.stopped, projectID)
	return map[string]interface{}{"status": "stopped", "project_id": projectID, "stopped_runs": 1}
}

func TestProjectControlEndpoints(t *testing.T) {
	provider := &projectControlProvider{}
	store, srv := setupDashboardServerTest(t, provider)

	repoPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoPath, "WORKFLOW.md"), []byte("workflow"), 0o644); err != nil {
		t.Fatalf("WriteFile WORKFLOW.md: %v", err)
	}
	project, err := store.CreateProject("Runtime", "", repoPath, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	runResp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/projects/"+project.ID+"/run", nil)
	if runResp.StatusCode != http.StatusOK {
		t.Fatalf("run project expected 200, got %d", runResp.StatusCode)
	}
	if len(provider.refreshed) != 1 || provider.refreshed[0] != project.ID {
		t.Fatalf("expected project run to be forwarded, got %#v", provider.refreshed)
	}

	stopResp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/projects/"+project.ID+"/stop", nil)
	if stopResp.StatusCode != http.StatusOK {
		t.Fatalf("stop project expected 200, got %d", stopResp.StatusCode)
	}
	if len(provider.stopped) != 1 || provider.stopped[0] != project.ID {
		t.Fatalf("expected project stop to be forwarded, got %#v", provider.stopped)
	}
}

func TestProjectRunEndpointRejectsOutOfScopeProjects(t *testing.T) {
	provider := &projectControlProvider{
		testProvider: testProvider{
			status: map[string]interface{}{
				"active_runs":      0,
				"scoped_repo_path": "/repo/current",
			},
		},
	}
	store, srv := setupDashboardServerTest(t, provider)

	project, err := store.CreateProject("Runtime", "", "/repo/other", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	resp := requestJSON(t, srv, http.MethodPost, "/api/v1/app/projects/"+project.ID+"/run", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for out-of-scope run, got %d", resp.StatusCode)
	}
	payload := decodeResponse(t, resp)
	if payload["error"] != "Project repo is outside the current server scope (/repo/current)" {
		t.Fatalf("unexpected error payload: %#v", payload)
	}
}
