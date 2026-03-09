package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/olhapi/maestro/internal/extensions"
	"github.com/olhapi/maestro/internal/httpserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/logsink"
	"github.com/olhapi/maestro/internal/mcp"
	"github.com/olhapi/maestro/internal/observability"
	"github.com/olhapi/maestro/internal/orchestrator"
	"github.com/olhapi/maestro/internal/speccheck"
	"github.com/olhapi/maestro/internal/verification"
	"github.com/olhapi/maestro/pkg/config"
)

var version = "dev"

const guardrailsAcknowledgementFlag = "--i-understand-that-this-will-be-running-without-the-usual-guardrails"

type globalOptions struct {
	logLevel     slog.Level
	logLevelName string
}

func main() {
	globalOpts, args, err := parseGlobalOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid global options: %v\n", err)
		os.Exit(1)
	}
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}
	os.Args = append([]string{os.Args[0]}, args...)

	command := os.Args[1]
	logsRoot := ""
	logMaxBytes := int64(0)
	logMaxFiles := 0
	if command == "run" {
		runOpts := parseRunOptions(os.Args[2:])
		logsRoot = runOpts.logsRoot
		logMaxBytes = runOpts.logMaxBytes
		logMaxFiles = runOpts.logMaxFiles
	}
	if _, err := setupLoggerWithWriter(os.Stdout, logsRoot, logMaxBytes, logMaxFiles, globalOpts.logLevel); err != nil {
		fmt.Fprintf(os.Stderr, "failed to setup logger: %v\n", err)
		os.Exit(1)
	}

	switch command {
	case "run":
		runOrchestrator()
	case "mcp":
		runMCP()
	case "board":
		runBoard()
	case "issue":
		runIssue()
	case "project":
		runProject()
	case "status":
		runStatus()
	case "verify":
		runVerify()
	case "spec-check":
		runSpecCheck()
	case "version":
		fmt.Printf("maestro %s\n", version)
	case "workflow":
		runWorkflow()
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`maestro - Agent orchestration with local kanban

Usage:
  maestro <command> [options]

Commands:
  run              Start the orchestrator (polls for work, dispatches to agents)
  mcp              Start the MCP server (for Codex/ChatGPT integration)
  board            View the kanban board
  issue            Manage issues
  project          Manage projects
  status           Show orchestrator status
  verify           Run local readiness checks
  spec-check       Run lightweight Maestro spec conformance checks
  workflow         Initialize WORKFLOW.md
  version          Show version

Examples:
  maestro --log-level debug run /path/to/repo
  maestro run                           # Start orchestrator in current directory
  maestro run /path/to/repo             # Start orchestrator for a specific repo
  maestro run --workflow ./custom.md    # Use a non-default workflow file
  maestro run --extensions ./ext.json   # Enable extension-backed dynamic tools
  maestro run --logs-root ./log         # Write structured JSON logs to file + stdout
  maestro run --logs-root ./log --log-max-bytes 1048576 --log-max-files 5
  maestro run --port 8787               # Expose observability API on /api/v1/state
  maestro mcp                           # Start MCP server over stdio
  maestro mcp --extensions ./ext.json   # Load extension tools
  maestro status --dashboard            # Render a dashboard-style snapshot
  maestro board                         # Show kanban board
  maestro issue create "Fix bug"        # Create an issue
  maestro issue list --state ready      # List ready issues
  maestro issue move ISS-1 in_progress  # Change issue state
  maestro project create "My App"       # Create a project
  maestro verify                         # Verify local setup
  maestro spec-check --json              # Run spec conformance checks
  maestro workflow init                  # Create a WORKFLOW.md

Database:
  Maestro stores data in the current workspace's .maestro/maestro.db by default.
  Use --db flag to specify a different location.

Global options:
  --log-level <debug|info|warn|error>

`)
}

func getStore(dbPath string) *kanban.Store {
	if dbPath == "" {
		cwd, _ := os.Getwd()
		dbPath = filepath.Join(cwd, ".maestro", "maestro.db")
	}

	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Error("Failed to create database directory", "error", err)
		os.Exit(1)
	}

	store, err := kanban.NewStore(dbPath)
	if err != nil {
		slog.Error("Failed to open database", "error", err)
		os.Exit(1)
	}
	return store
}

type runOptions struct {
	repoPath           string
	dbPath             string
	logsRoot           string
	port               string
	workflowPath       string
	extensionsFile     string
	logMaxBytes        int64
	logMaxFiles        int
	acknowledgedUnsafe bool
}

func parseGlobalOptions(args []string) (globalOptions, []string, error) {
	opts := globalOptions{
		logLevel:     slog.LevelInfo,
		logLevelName: "info",
	}
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--log-level":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("--log-level requires a value")
			}
			level, normalized, err := parseLogLevel(args[i+1])
			if err != nil {
				return opts, nil, err
			}
			opts.logLevel = level
			opts.logLevelName = normalized
			i++
		default:
			remaining = append(remaining, args[i])
		}
	}
	return opts, remaining, nil
}

func parseLogLevel(raw string) (slog.Level, string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(raw)); normalized {
	case "", "info":
		return slog.LevelInfo, "info", nil
	case "debug":
		return slog.LevelDebug, "debug", nil
	case "warn", "warning":
		return slog.LevelWarn, "warn", nil
	case "error":
		return slog.LevelError, "error", nil
	default:
		return slog.LevelInfo, "", fmt.Errorf("unsupported log level %q", raw)
	}
}

func parseRunOptions(args []string) runOptions {
	opts := runOptions{
		logMaxBytes: 10 * 1024 * 1024,
		logMaxFiles: 3,
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db":
			if i+1 < len(args) {
				opts.dbPath = args[i+1]
				i++
			}
		case "--logs-root":
			if i+1 < len(args) {
				opts.logsRoot = args[i+1]
				i++
			}
		case "--port":
			if i+1 < len(args) {
				opts.port = args[i+1]
				i++
			}
		case "--log-max-bytes":
			if i+1 < len(args) {
				if v, err := strconv.ParseInt(args[i+1], 10, 64); err == nil {
					opts.logMaxBytes = v
				}
				i++
			}
		case "--log-max-files":
			if i+1 < len(args) {
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					opts.logMaxFiles = v
				}
				i++
			}
		case "--workflow":
			if i+1 < len(args) {
				opts.workflowPath = args[i+1]
				i++
			}
		case "--extensions":
			if i+1 < len(args) {
				opts.extensionsFile = args[i+1]
				i++
			}
		case guardrailsAcknowledgementFlag:
			opts.acknowledgedUnsafe = true
		default:
			if !strings.HasPrefix(args[i], "--") {
				opts.repoPath = args[i]
			}
		}
	}
	return opts
}

func guardrailsAcknowledgementBanner() string {
	return strings.Join([]string{
		"This Maestro implementation is a low key engineering preview.",
		"Codex will run without any guardrails.",
		"Maestro is not a supported product and is presented as-is.",
		"To silence this warning, pass " + guardrailsAcknowledgementFlag + ".",
	}, "\n")
}

func setupLoggerWithWriter(stdout io.Writer, logsRoot string, maxBytes int64, maxFiles int, level slog.Level) (string, error) {
	if stdout == nil {
		stdout = io.Discard
	}
	writer := io.Writer(stdout)
	logPath := ""
	if strings.TrimSpace(logsRoot) != "" {
		if err := os.MkdirAll(logsRoot, 0o755); err != nil {
			return "", err
		}
		logPath = filepath.Join(logsRoot, "maestro.log")
		f, err := logsink.New(logPath, maxBytes, maxFiles)
		if err != nil {
			return "", err
		}
		writer = logsink.Multi(stdout, f)
	}
	h := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
	if logPath != "" {
		slog.Info("Logger initialized",
			"log_file", logPath,
			"log_level", level.String(),
			"rotate_max_bytes", maxBytes,
			"rotate_max_files", maxFiles,
		)
	}
	return logPath, nil
}

// === Commands ===

func runOrchestrator() {
	opts := parseRunOptions(os.Args[2:])
	if !opts.acknowledgedUnsafe {
		fmt.Fprintln(os.Stderr, guardrailsAcknowledgementBanner())
	}

	store := getStore(opts.dbPath)
	defer store.Close()
	registry, err := extensions.LoadFile(opts.extensionsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load extensions: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	orch := orchestrator.NewSharedWithExtensions(store, registry, opts.repoPath, opts.workflowPath)
	if opts.port != "" {
		addr := opts.port
		if !strings.Contains(addr, ":") {
			addr = ":" + addr
		}
		httpserver.Start(ctx, addr, store, orch)
	}

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		slog.Info("Shutting down...")
		cancel()
	}()

	if err := orch.Run(ctx); err != nil && err != context.Canceled {
		slog.Error("Orchestrator error", "error", err)
		os.Exit(1)
	}
}

func runMCP() {
	var dbPath string
	var extensionsFile string
	args := os.Args[2:]
	for i := 0; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			i++
			continue
		}
		if args[i] == "--extensions" && i+1 < len(args) {
			extensionsFile = args[i+1]
			i++
		}
	}

	store := getStore(dbPath)
	defer store.Close()

	server := mcp.NewServerWithExtensions(store, extensionsFile)
	if err := server.ServeStdio(); err != nil {
		slog.Error("MCP server error", "error", err)
		os.Exit(1)
	}
}

func runWorkflow() {
	if code := workflowCommand(os.Args[2:], os.Stdin, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func workflowCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, `Usage:
  maestro workflow init [repo_path]
`)
		return 1
	}

	switch args[0] {
	case "init":
		repoPath := ""
		if len(args) > 1 {
			repoPath = args[1]
		}
		interactive := false
		if in, ok := stdin.(*os.File); ok {
			if out, ok := stdout.(*os.File); ok {
				interactive = isatty.IsTerminal(in.Fd()) && isatty.IsTerminal(out.Fd())
			}
		}
		if err := config.InitWorkflow(repoPath, config.InitOptions{
			Interactive: interactive,
			Stdin:       bufio.NewReader(stdin),
			Stdout:      stdout,
		}); err != nil {
			fmt.Fprintf(stderr, "failed to initialize workflow: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Initialized %s\n", config.WorkflowPath(repoPath))
		return 0
	default:
		fmt.Fprintf(stdout, "Unknown workflow command: %s\n", args[0])
		return 1
	}
}

func runBoard() {
	if code := boardCommand(os.Args[2:], os.Stdout); code != 0 {
		os.Exit(code)
	}
}

func boardCommand(args []string, stdout io.Writer) int {
	var dbPath string
	var projectID string

	for i := 0; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			i++
		} else if args[i] == "--project" && i+1 < len(args) {
			projectID = args[i+1]
			i++
		}
	}

	store := getStore(dbPath)
	defer store.Close()

	issues, err := store.ListIssues(map[string]interface{}{"project_id": projectID})
	if err != nil {
		fmt.Fprintf(stdout, "Error: %v\n", err)
		return 1
	}

	states := map[kanban.State][]kanban.Issue{}
	for _, issue := range issues {
		states[issue.State] = append(states[issue.State], issue)
	}

	fmt.Fprintln(stdout, "\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Fprintln(stdout, "║                        MAESTRO KANBAN                            ║")
	fmt.Fprintln(stdout, "╚══════════════════════════════════════════════════════════════════╝")

	columns := []struct {
		state kanban.State
		icon  string
		name  string
	}{
		{kanban.StateBacklog, "📋", "BACKLOG"},
		{kanban.StateReady, "✅", "READY"},
		{kanban.StateInProgress, "🔄", "IN PROGRESS"},
		{kanban.StateInReview, "👀", "IN REVIEW"},
		{kanban.StateDone, "✨", "DONE"},
		{kanban.StateCancelled, "❌", "CANCELLED"},
	}

	for _, col := range columns {
		items := states[col.state]
		fmt.Fprintf(stdout, "\n%s %s (%d)\n", col.icon, col.name, len(items))
		fmt.Fprintln(stdout, strings.Repeat("─", 50))
		if len(items) == 0 {
			fmt.Fprintln(stdout, "  (empty)")
			continue
		}
		for _, issue := range items {
			fmt.Fprintf(stdout, "  [%s] %s\n", issue.Identifier, issue.Title)
			if len(issue.Labels) > 0 {
				fmt.Fprintf(stdout, "    Labels: %s\n", strings.Join(issue.Labels, ", "))
			}
		}
	}
	fmt.Fprintln(stdout)
	return 0
}

func runIssue() {
	if code := issueCommand(os.Args[2:], os.Stdout); code != 0 {
		os.Exit(code)
	}
}

func runProject() {
	if code := projectCommand(os.Args[2:], os.Stdout); code != 0 {
		os.Exit(code)
	}
}

func issueCommand(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, `Usage:
  maestro issue create <title> [--desc <description>] [--project <id>] [--priority <n>] [--labels <label1,label2>]
  maestro issue list [--state <state>] [--project <id>]
  maestro issue show <identifier>
  maestro issue move <identifier> <state>
  maestro issue update <identifier> [--title <title>] [--desc <description>] [--pr <number> <url>]
  maestro issue delete <identifier>
  maestro issue block <identifier> <blocker_identifier...>
`)
		return 1
	}

	cmd := args[0]
	subargs := append([]string(nil), args[1:]...)
	var dbPath string
	for i := 0; i < len(subargs); i++ {
		if subargs[i] == "--db" && i+1 < len(subargs) {
			dbPath = subargs[i+1]
			subargs = append(subargs[:i], subargs[i+2:]...)
			break
		}
	}

	store := getStore(dbPath)
	defer store.Close()

	switch cmd {
	case "create":
		if len(subargs) < 1 {
			fmt.Fprintln(stdout, "Usage: maestro issue create <title> [options]")
			return 1
		}
		title := subargs[0]
		description := ""
		projectID := ""
		priority := 0
		var labels []string
		for i := 1; i < len(subargs); i++ {
			switch subargs[i] {
			case "--desc":
				description = subargs[i+1]
				i++
			case "--project":
				projectID = subargs[i+1]
				i++
			case "--priority":
				fmt.Sscanf(subargs[i+1], "%d", &priority)
				i++
			case "--labels":
				labels = strings.Split(subargs[i+1], ",")
				i++
			}
		}
		issue, err := store.CreateIssue(projectID, "", title, description, priority, labels)
		if err != nil {
			fmt.Fprintf(stdout, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Created issue %s: %s\n", issue.Identifier, issue.Title)
	case "list":
		filter := make(map[string]interface{})
		for i := 0; i < len(subargs); i++ {
			switch subargs[i] {
			case "--state":
				filter["state"] = subargs[i+1]
				i++
			case "--project":
				filter["project_id"] = subargs[i+1]
				i++
			}
		}
		issues, err := store.ListIssues(filter)
		if err != nil {
			fmt.Fprintf(stdout, "Error: %v\n", err)
			return 1
		}
		for _, issue := range issues {
			fmt.Fprintf(stdout, "[%s] %s: %s\n", issue.State, issue.Identifier, issue.Title)
		}
	case "show":
		if len(subargs) < 1 {
			fmt.Fprintln(stdout, "Usage: maestro issue show <identifier>")
			return 1
		}
		issue, err := store.GetIssueByIdentifier(subargs[0])
		if err != nil {
			fmt.Fprintf(stdout, "Issue not found: %s\n", subargs[0])
			return 1
		}
		fmt.Fprintf(stdout, "ID:          %s\n", issue.ID)
		fmt.Fprintf(stdout, "Identifier:  %s\n", issue.Identifier)
		fmt.Fprintf(stdout, "Title:       %s\n", issue.Title)
		fmt.Fprintf(stdout, "State:       %s\n", issue.State)
		fmt.Fprintf(stdout, "Phase:       %s\n", issue.WorkflowPhase)
		fmt.Fprintf(stdout, "Priority:    %d\n", issue.Priority)
		if issue.Description != "" {
			fmt.Fprintf(stdout, "Description: %s\n", issue.Description)
		}
		if len(issue.Labels) > 0 {
			fmt.Fprintf(stdout, "Labels:      %s\n", strings.Join(issue.Labels, ", "))
		}
		if issue.PRURL != "" {
			fmt.Fprintf(stdout, "PR:          #%d - %s\n", issue.PRNumber, issue.PRURL)
		}
		if len(issue.BlockedBy) > 0 {
			fmt.Fprintf(stdout, "Blocked by:  %s\n", strings.Join(issue.BlockedBy, ", "))
		}
	case "move":
		if len(subargs) < 2 {
			fmt.Fprintln(stdout, "Usage: maestro issue move <identifier> <state>")
			return 1
		}
		issue, err := store.GetIssueByIdentifier(subargs[0])
		if err != nil {
			fmt.Fprintf(stdout, "Issue not found: %s\n", subargs[0])
			return 1
		}
		if err := store.UpdateIssueState(issue.ID, kanban.State(subargs[1])); err != nil {
			fmt.Fprintf(stdout, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Moved %s to %s\n", subargs[0], subargs[1])
	case "update":
		if len(subargs) < 1 {
			fmt.Fprintln(stdout, "Usage: maestro issue update <identifier> [options]")
			return 1
		}
		identifier := subargs[0]
		issue, err := store.GetIssueByIdentifier(identifier)
		if err != nil {
			fmt.Fprintf(stdout, "Issue not found: %s\n", identifier)
			return 1
		}
		updates := make(map[string]interface{})
		for i := 1; i < len(subargs); i++ {
			switch subargs[i] {
			case "--title":
				updates["title"] = subargs[i+1]
				i++
			case "--desc":
				updates["description"] = subargs[i+1]
				i++
			case "--pr":
				var prNum int
				fmt.Sscanf(subargs[i+1], "%d", &prNum)
				updates["pr_number"] = prNum
				updates["pr_url"] = subargs[i+2]
				i += 2
			}
		}
		if err := store.UpdateIssue(issue.ID, updates); err != nil {
			fmt.Fprintf(stdout, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Updated issue %s\n", identifier)
	case "delete":
		if len(subargs) < 1 {
			fmt.Fprintln(stdout, "Usage: maestro issue delete <identifier>")
			return 1
		}
		issue, err := store.GetIssueByIdentifier(subargs[0])
		if err != nil {
			fmt.Fprintf(stdout, "Issue not found: %s\n", subargs[0])
			return 1
		}
		if err := store.DeleteIssue(issue.ID); err != nil {
			fmt.Fprintf(stdout, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Deleted issue %s\n", subargs[0])
	case "block":
		if len(subargs) < 2 {
			fmt.Fprintln(stdout, "Usage: maestro issue block <identifier> <blocker_identifier...>")
			return 1
		}
		identifier := subargs[0]
		issue, err := store.GetIssueByIdentifier(identifier)
		if err != nil {
			fmt.Fprintf(stdout, "Issue not found: %s\n", identifier)
			return 1
		}
		blockers := subargs[1:]
		if err := store.UpdateIssue(issue.ID, map[string]interface{}{"blocked_by": blockers}); err != nil {
			fmt.Fprintf(stdout, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Set blockers for %s: %s\n", identifier, strings.Join(blockers, ", "))
	default:
		fmt.Fprintf(stdout, "Unknown command: %s\n", cmd)
		return 1
	}
	return 0
}

func projectCommand(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, `Usage:
  maestro project create <name> --repo <repo_path> [--desc <description>] [--workflow <workflow_path>]
  maestro project list
  maestro project delete <id>
`)
		return 1
	}

	cmd := args[0]
	subargs := append([]string(nil), args[1:]...)
	var dbPath string
	for i := 0; i < len(subargs); i++ {
		if subargs[i] == "--db" && i+1 < len(subargs) {
			dbPath = subargs[i+1]
			subargs = append(subargs[:i], subargs[i+2:]...)
			break
		}
	}

	store := getStore(dbPath)
	defer store.Close()

	switch cmd {
	case "create":
		if len(subargs) < 1 {
			fmt.Fprintln(stdout, "Usage: maestro project create <name> --repo <repo_path> [--desc <description>] [--workflow <workflow_path>]")
			return 1
		}
		name := subargs[0]
		description := ""
		repoPath := ""
		workflowPath := ""
		for i := 1; i < len(subargs); i++ {
			switch subargs[i] {
			case "--desc":
				if i+1 < len(subargs) {
					description = subargs[i+1]
					i++
				}
			case "--repo":
				if i+1 < len(subargs) {
					repoPath = subargs[i+1]
					i++
				}
			case "--workflow":
				if i+1 < len(subargs) {
					workflowPath = subargs[i+1]
					i++
				}
			}
		}
		if strings.TrimSpace(repoPath) == "" {
			fmt.Fprintln(stdout, "Error: --repo is required")
			return 1
		}
		project, err := store.CreateProject(name, description, repoPath, workflowPath)
		if err != nil {
			fmt.Fprintf(stdout, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Created project %s (ID: %s, Repo: %s)\n", project.Name, project.ID, project.RepoPath)
	case "list":
		projects, err := store.ListProjects()
		if err != nil {
			fmt.Fprintf(stdout, "Error: %v\n", err)
			return 1
		}
		for _, p := range projects {
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", p.ID, p.Name, p.RepoPath, p.Description)
		}
	case "delete":
		if len(subargs) < 1 {
			fmt.Fprintln(stdout, "Usage: maestro project delete <id>")
			return 1
		}
		if err := store.DeleteProject(subargs[0]); err != nil {
			fmt.Fprintf(stdout, "Error: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "Project deleted")
	default:
		fmt.Fprintf(stdout, "Unknown command: %s\n", cmd)
		return 1
	}
	return 0
}

func runSpecCheck() {
	if code := specCheckCommand(os.Args[2:], os.Stdout); code != 0 {
		os.Exit(code)
	}
}

func runVerify() {
	if code := verifyCommand(os.Args[2:], os.Stdout); code != 0 {
		os.Exit(code)
	}
}

func runStatus() {
	if code := statusCommand(os.Args[2:], os.Stdout); code != 0 {
		os.Exit(code)
	}
}

func specCheckCommand(args []string, stdout io.Writer) int {
	var repoPath string
	jsonOnly := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--repo" && i+1 < len(args) {
			repoPath = args[i+1]
			i++
			continue
		}
		if args[i] == "--json" {
			jsonOnly = true
		}
	}
	r := speccheck.Run(repoPath)
	if jsonOnly {
		_ = json.NewEncoder(stdout).Encode(r)
		if !r.OK {
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "Spec Check")
	fmt.Fprintln(stdout, strings.Repeat("=", 40))
	for k, v := range r.Checks {
		fmt.Fprintf(stdout, "%s: %s\n", k, v)
	}
	if !r.OK {
		return 1
	}
	return 0
}

func verifyCommand(args []string, stdout io.Writer) int {
	var dbPath, repoPath string
	jsonOnly := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			i++
			continue
		}
		if args[i] == "--repo" && i+1 < len(args) {
			repoPath = args[i+1]
			i++
			continue
		}
		if args[i] == "--json" {
			jsonOnly = true
		}
	}
	res := verification.Run(repoPath, dbPath)
	if jsonOnly {
		_ = json.NewEncoder(stdout).Encode(res)
		if !res.OK {
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "Verification")
	fmt.Fprintln(stdout, strings.Repeat("=", 40))
	for k, v := range res.Checks {
		fmt.Fprintf(stdout, "%s: %s\n", k, v)
	}
	if len(res.Errors) > 0 {
		fmt.Fprintln(stdout, "Errors:")
		for _, e := range res.Errors {
			fmt.Fprintf(stdout, "- %s\n", e)
		}
	}
	if !res.OK {
		return 1
	}
	return 0
}

func statusCommand(args []string, stdout io.Writer) int {
	var dbPath string
	jsonOnly := false
	dashboard := false
	dashboardURL := ""

	for i := 0; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			i++
			continue
		}
		if args[i] == "--json" {
			jsonOnly = true
		}
		if args[i] == "--dashboard" {
			dashboard = true
		}
		if args[i] == "--dashboard-url" && i+1 < len(args) {
			dashboardURL = args[i+1]
			i++
		}
	}

	store := getStore(dbPath)
	defer store.Close()

	// Get counts
	issues, _ := store.ListIssues(nil)
	counts := make(map[kanban.State]int)
	for _, issue := range issues {
		counts[issue.State]++
	}
	projects, _ := store.ListProjects()

	data := map[string]interface{}{
		"projects": len(projects),
		"issues":   counts,
		"total":    len(issues),
	}

	if dashboard {
		snapshot := observability.Snapshot{
			GeneratedAt: time.Now().UTC(),
			CodexTotals: observability.TokenTotals{},
		}
		if jsonOnly {
			_ = json.NewEncoder(stdout).Encode(snapshot)
			return 0
		}
		fmt.Fprintln(stdout, observability.FormatDashboard(snapshot, observability.DashboardOptions{
			Now:          time.Now().UTC(),
			DashboardURL: dashboardURL,
		}))
		return 0
	}

	if jsonOnly {
		_ = json.NewEncoder(stdout).Encode(data)
		return 0
	}

	fmt.Fprintln(stdout, "Maestro Status")
	fmt.Fprintln(stdout, strings.Repeat("=", 40))
	fmt.Fprintf(stdout, "Projects: %d\n", len(projects))
	fmt.Fprintf(stdout, "Total Issues: %d\n", len(issues))
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Issue Breakdown:")
	for _, state := range []kanban.State{kanban.StateBacklog, kanban.StateReady, kanban.StateInProgress, kanban.StateInReview, kanban.StateDone, kanban.StateCancelled} {
		fmt.Fprintf(stdout, "  %s: %d\n", state, counts[state])
	}
	return 0
}
