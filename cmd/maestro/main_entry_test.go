package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("MAESTRO_MAIN_HELPER") != "1" {
		return
	}
	raw := os.Getenv("MAESTRO_MAIN_ARGS")
	var args []string
	if raw != "" {
		args = strings.Split(raw, "\n")
	}
	os.Args = append([]string{"maestro"}, args...)
	main()
	os.Exit(0)
}

func runMainHelper(t *testing.T, env map[string]string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
	cmd.Env = append(os.Environ(),
		"MAESTRO_MAIN_HELPER=1",
		"MAESTRO_MAIN_ARGS="+strings.Join(args, "\n"),
	)
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func freeAddrForHelper(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestMainEntryUsageVersionAndErrors(t *testing.T) {
	stdout, _, err := runMainHelper(t, nil)
	if err == nil {
		t.Fatal("expected usage exit")
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Fatalf("expected usage output, got %q", stdout)
	}

	stdout, stderr, err := runMainHelper(t, nil, "--log-level", "verbose", "version")
	if err == nil {
		t.Fatal("expected invalid log level exit")
	}
	if !strings.Contains(stderr, "invalid global options") {
		t.Fatalf("expected invalid options error, got stdout=%q stderr=%q", stdout, stderr)
	}

	stdout, _, err = runMainHelper(t, nil, "unknown-command")
	if err == nil {
		t.Fatal("expected unknown command exit")
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Fatalf("expected usage for unknown command, got %q", stdout)
	}

	stdout, stderr, err = runMainHelper(t, nil, "version")
	if err != nil {
		t.Fatalf("version helper failed: %v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "maestro ") {
		t.Fatalf("unexpected version output: %q", stdout)
	}
}

func TestMainEntryDispatchesWrapperCommands(t *testing.T) {
	repoPath := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "maestro.db")
	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")

	if stdout, stderr, err := runMainHelper(t, nil, "workflow"); err == nil {
		t.Fatal("expected workflow usage exit")
	} else if !strings.Contains(stdout, "maestro workflow init") {
		t.Fatalf("unexpected workflow usage output: stdout=%q stderr=%q", stdout, stderr)
	}

	if stdout, stderr, err := runMainHelper(t, nil, "workflow", "init", repoPath); err != nil {
		t.Fatalf("workflow init failed: %v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if _, err := os.Stat(workflowPath); err != nil {
		t.Fatalf("expected workflow file: %v", err)
	}

	if stdout, _, err := runMainHelper(t, nil, "project"); err == nil {
		t.Fatal("expected project usage exit")
	} else if !strings.Contains(stdout, "maestro project create") {
		t.Fatalf("unexpected project usage output: %q", stdout)
	}

	if stdout, _, err := runMainHelper(t, nil, "project", "create", "Platform", "--db", dbPath); err == nil {
		t.Fatal("expected project create to require --repo")
	} else if !strings.Contains(stdout, "--repo is required") {
		t.Fatalf("unexpected missing repo output: %q", stdout)
	}

	if stdout, stderr, err := runMainHelper(t, nil, "project", "create", "Platform", "--repo", repoPath, "--workflow", workflowPath, "--db", dbPath); err != nil {
		t.Fatalf("project create failed: %v stdout=%q stderr=%q", err, stdout, stderr)
	}

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	projects, err := store.ListProjects()
	if err != nil || len(projects) != 1 {
		t.Fatalf("expected one project: err=%v projects=%v", err, projects)
	}
	projectID := projects[0].ID

	if stdout, _, err := runMainHelper(t, nil, "project", "list", "--db", dbPath); err != nil {
		t.Fatalf("project list failed: %v stdout=%q", err, stdout)
	} else if !strings.Contains(stdout, projectID) {
		t.Fatalf("expected project id in output: %q", stdout)
	}

	if stdout, _, err := runMainHelper(t, nil, "issue"); err == nil {
		t.Fatal("expected issue usage exit")
	} else if !strings.Contains(stdout, "maestro issue create") {
		t.Fatalf("unexpected issue usage output: %q", stdout)
	}

	if stdout, _, err := runMainHelper(t, nil, "issue", "create", "Coverage", "--project", projectID, "--priority", "2", "--labels", "cli,test", "--db", dbPath); err != nil {
		t.Fatalf("issue create failed: %v stdout=%q", err, stdout)
	}
	issues, err := store.ListIssues(nil)
	if err != nil || len(issues) != 1 {
		t.Fatalf("expected one issue: err=%v issues=%v", err, issues)
	}
	identifier := issues[0].Identifier

	if stdout, _, err := runMainHelper(t, nil, "issue", "list", "--project", projectID, "--db", dbPath); err != nil {
		t.Fatalf("issue list failed: %v stdout=%q", err, stdout)
	} else if !strings.Contains(stdout, identifier) {
		t.Fatalf("expected issue identifier in list: %q", stdout)
	}

	if stdout, _, err := runMainHelper(t, nil, "issue", "show", identifier, "--db", dbPath); err != nil {
		t.Fatalf("issue show failed: %v stdout=%q", err, stdout)
	} else if !strings.Contains(stdout, "Identifier:  "+identifier) {
		t.Fatalf("unexpected issue show output: %q", stdout)
	}

	if stdout, _, err := runMainHelper(t, nil, "issue", "move", identifier, "in_progress", "--db", dbPath); err != nil {
		t.Fatalf("issue move failed: %v stdout=%q", err, stdout)
	}

	if stdout, _, err := runMainHelper(t, nil, "issue", "update", identifier, "--title", "Coverage updated", "--desc", "more tests", "--pr", "7", "https://example.com/pr/7", "--db", dbPath); err != nil {
		t.Fatalf("issue update failed: %v stdout=%q", err, stdout)
	}

	blocker, err := store.CreateIssue(projectID, "", "Blocker", "", 0, nil)
	if err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	if stdout, _, err := runMainHelper(t, nil, "issue", "block", identifier, blocker.Identifier, "--db", dbPath); err != nil {
		t.Fatalf("issue block failed: %v stdout=%q", err, stdout)
	}

	if stdout, _, err := runMainHelper(t, nil, "board", "--db", dbPath); err != nil {
		t.Fatalf("board failed: %v stdout=%q", err, stdout)
	} else if !strings.Contains(stdout, "MAESTRO KANBAN") {
		t.Fatalf("unexpected board output: %q", stdout)
	}

	if stdout, _, err := runMainHelper(t, nil, "status", "--db", dbPath); err != nil {
		t.Fatalf("status failed: %v stdout=%q", err, stdout)
	} else if !strings.Contains(stdout, "Maestro Status") {
		t.Fatalf("unexpected status output: %q", stdout)
	}

	if stdout, _, err := runMainHelper(t, nil, "verify", "--db", dbPath, "--repo", repoPath); err != nil && !strings.Contains(stdout, "Verification") {
		t.Fatalf("unexpected verify failure output: %q", stdout)
	} else if !strings.Contains(stdout, "Verification") {
		t.Fatalf("unexpected verify output: %q", stdout)
	}

	if stdout, _, err := runMainHelper(t, nil, "spec-check", "--repo", repoPath); err != nil && !strings.Contains(stdout, "Spec Check") {
		t.Fatalf("unexpected spec-check failure output: %q", stdout)
	} else if !strings.Contains(stdout, "Spec Check") {
		t.Fatalf("unexpected spec-check output: %q", stdout)
	}

	if stdout, _, err := runMainHelper(t, nil, "issue", "delete", identifier, "--db", dbPath); err != nil {
		t.Fatalf("issue delete failed: %v stdout=%q", err, stdout)
	}
	if stdout, _, err := runMainHelper(t, nil, "project", "delete", projectID, "--db", dbPath); err != nil {
		t.Fatalf("project delete failed: %v stdout=%q", err, stdout)
	}
}

func TestMainEntryRunCommand(t *testing.T) {
	repoPath := t.TempDir()
	workflowPath := filepath.Join(repoPath, "WORKFLOW.md")
	dbPath := filepath.Join(repoPath, "maestro.db")
	workspaceRoot := filepath.Join(repoPath, "workspaces")
	workflow := `---
tracker:
  kind: kanban
  active_states: [ready, in_progress, in_review]
  terminal_states: [done, cancelled]
polling:
  interval_ms: 50
workspace:
  root: ` + workspaceRoot + `
hooks:
  timeout_ms: 1000
agent:
  max_concurrent_agents: 1
  max_turns: 1
  max_retry_backoff_ms: 100
  mode: stdio
codex:
  command: cat
  approval_policy: never
  read_timeout_ms: 500
  turn_timeout_ms: 1000
---
Test prompt for {{ issue.identifier }}
`
	if err := os.WriteFile(workflowPath, []byte(workflow), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	addr := freeAddrForHelper(t)
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess")
	cmd.Env = append(os.Environ(),
		"MAESTRO_MAIN_HELPER=1",
		"MAESTRO_MAIN_ARGS="+strings.Join([]string{
			"run", "--workflow", workflowPath, "--db", dbPath, "--port", addr, guardrailsAcknowledgementFlag, repoPath,
		}, "\n"),
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start run helper: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	healthURL := "http://" + addr + "/health"
	for {
		if ctx.Err() != nil {
			t.Fatalf("run helper never served health: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		resp, err := http.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt run helper: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait run helper: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}
