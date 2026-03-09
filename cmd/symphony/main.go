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
	"github.com/olhapi/symphony-go/internal/extensions"
	"github.com/olhapi/symphony-go/internal/httpserver"
	"github.com/olhapi/symphony-go/internal/kanban"
	"github.com/olhapi/symphony-go/internal/logsink"
	"github.com/olhapi/symphony-go/internal/mcp"
	"github.com/olhapi/symphony-go/internal/observability"
	"github.com/olhapi/symphony-go/internal/orchestrator"
	"github.com/olhapi/symphony-go/internal/speccheck"
	"github.com/olhapi/symphony-go/internal/verification"
	"github.com/olhapi/symphony-go/pkg/config"
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
		fmt.Printf("symphony %s\n", version)
	case "workflow":
		runWorkflow()
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`symphony - Agent orchestration with local kanban

Usage:
  symphony <command> [options]

Commands:
  run              Start the orchestrator (polls for work, dispatches to agents)
  mcp              Start the MCP server (for Codex/ChatGPT integration)
  board            View the kanban board
  issue            Manage issues
  project          Manage projects
  status           Show orchestrator status
  verify           Run local readiness checks
  spec-check       Run lightweight Symphony spec conformance checks
  workflow         Initialize WORKFLOW.md
  version          Show version

Examples:
  symphony --log-level debug run /path/to/repo
  symphony run                           # Start orchestrator in current directory
  symphony run /path/to/repo             # Start orchestrator for a specific repo
  symphony run --workflow ./custom.md    # Use a non-default workflow file
  symphony run --extensions ./ext.json   # Enable extension-backed dynamic tools
  symphony run --logs-root ./log         # Write structured JSON logs to file + stdout
  symphony run --logs-root ./log --log-max-bytes 1048576 --log-max-files 5
  symphony run --port 8787               # Expose observability API on /api/v1/state
  symphony mcp                           # Start MCP server over stdio
  symphony mcp --extensions ./ext.json   # Load extension tools
  symphony status --dashboard            # Render a dashboard-style snapshot
  symphony board                         # Show kanban board
  symphony issue create "Fix bug"        # Create an issue
  symphony issue list --state ready      # List ready issues
  symphony issue move ISS-1 in_progress  # Change issue state
  symphony project create "My App"       # Create a project
  symphony verify                         # Verify local setup
  symphony spec-check --json              # Run spec conformance checks
  symphony workflow init                  # Create a WORKFLOW.md

Database:
  Symphony stores data in the current workspace's .symphony/symphony.db by default.
  Use --db flag to specify a different location.

Global options:
  --log-level <debug|info|warn|error>

`)
}

func getStore(dbPath string) *kanban.Store {
	if dbPath == "" {
		cwd, _ := os.Getwd()
		dbPath = filepath.Join(cwd, ".symphony", "symphony.db")
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
		"This Symphony implementation is a low key engineering preview.",
		"Codex will run without any guardrails.",
		"Symphony-Go is not a supported product and is presented as-is.",
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
		logPath = filepath.Join(logsRoot, "symphony.log")
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
	if len(os.Args) < 3 {
		fmt.Print(`Usage:
  symphony workflow init [repo_path]
`)
		os.Exit(1)
	}

	switch os.Args[2] {
	case "init":
		repoPath := ""
		if len(os.Args) > 3 {
			repoPath = os.Args[3]
		}
		interactive := isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
		if err := config.InitWorkflow(repoPath, config.InitOptions{
			Interactive: interactive,
			Stdin:       bufio.NewReader(os.Stdin),
			Stdout:      os.Stdout,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "failed to initialize workflow: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Initialized %s\n", config.WorkflowPath(repoPath))
	default:
		fmt.Printf("Unknown workflow command: %s\n", os.Args[2])
		os.Exit(1)
	}
}

func runBoard() {
	var dbPath string
	var projectID string

	args := os.Args[2:]
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

	// Get issues grouped by state
	issues, err := store.ListIssues(map[string]interface{}{"project_id": projectID})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Group by state
	states := map[kanban.State][]kanban.Issue{}
	for _, issue := range issues {
		states[issue.State] = append(states[issue.State], issue)
	}

	// Print board
	fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                        SYMPHONY KANBAN                            ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")

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
		issues := states[col.state]
		fmt.Printf("\n%s %s (%d)\n", col.icon, col.name, len(issues))
		fmt.Println(strings.Repeat("─", 50))
		if len(issues) == 0 {
			fmt.Println("  (empty)")
		} else {
			for _, issue := range issues {
				fmt.Printf("  [%s] %s\n", issue.Identifier, issue.Title)
				if len(issue.Labels) > 0 {
					fmt.Printf("    Labels: %s\n", strings.Join(issue.Labels, ", "))
				}
			}
		}
	}
	fmt.Println()
}

func runIssue() {
	if len(os.Args) < 3 {
		fmt.Print(`Usage:
  symphony issue create <title> [--desc <description>] [--project <id>] [--priority <n>] [--labels <label1,label2>]
  symphony issue list [--state <state>] [--project <id>]
  symphony issue show <identifier>
  symphony issue move <identifier> <state>
  symphony issue update <identifier> [--title <title>] [--desc <description>] [--pr <number> <url>]
  symphony issue delete <identifier>
  symphony issue block <identifier> <blocker_identifier...>
`)
		os.Exit(1)
	}

	var dbPath string
	args := os.Args[3:]

	// Parse db flag
	for i := 0; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}

	store := getStore(dbPath)
	defer store.Close()

	cmd := os.Args[2]

	switch cmd {
	case "create":
		if len(args) < 1 {
			fmt.Println("Usage: symphony issue create <title> [options]")
			os.Exit(1)
		}
		title := args[0]
		description := ""
		projectID := ""
		priority := 0
		var labels []string

		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--desc":
				description = args[i+1]
				i++
			case "--project":
				projectID = args[i+1]
				i++
			case "--priority":
				fmt.Sscanf(args[i+1], "%d", &priority)
				i++
			case "--labels":
				labels = strings.Split(args[i+1], ",")
				i++
			}
		}

		issue, err := store.CreateIssue(projectID, "", title, description, priority, labels)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Created issue %s: %s\n", issue.Identifier, issue.Title)

	case "list":
		filter := make(map[string]interface{})
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--state":
				filter["state"] = args[i+1]
				i++
			case "--project":
				filter["project_id"] = args[i+1]
				i++
			}
		}

		issues, err := store.ListIssues(filter)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		for _, issue := range issues {
			fmt.Printf("[%s] %s: %s\n", issue.State, issue.Identifier, issue.Title)
		}

	case "show":
		if len(args) < 1 {
			fmt.Println("Usage: symphony issue show <identifier>")
			os.Exit(1)
		}
		issue, err := store.GetIssueByIdentifier(args[0])
		if err != nil {
			fmt.Printf("Issue not found: %s\n", args[0])
			os.Exit(1)
		}

		fmt.Printf("ID:          %s\n", issue.ID)
		fmt.Printf("Identifier:  %s\n", issue.Identifier)
		fmt.Printf("Title:       %s\n", issue.Title)
		fmt.Printf("State:       %s\n", issue.State)
		fmt.Printf("Priority:    %d\n", issue.Priority)
		if issue.Description != "" {
			fmt.Printf("Description: %s\n", issue.Description)
		}
		if len(issue.Labels) > 0 {
			fmt.Printf("Labels:      %s\n", strings.Join(issue.Labels, ", "))
		}
		if issue.PRURL != "" {
			fmt.Printf("PR:          #%d - %s\n", issue.PRNumber, issue.PRURL)
		}
		if len(issue.BlockedBy) > 0 {
			fmt.Printf("Blocked by:  %s\n", strings.Join(issue.BlockedBy, ", "))
		}

	case "move":
		if len(args) < 2 {
			fmt.Println("Usage: symphony issue move <identifier> <state>")
			os.Exit(1)
		}
		issue, err := store.GetIssueByIdentifier(args[0])
		if err != nil {
			fmt.Printf("Issue not found: %s\n", args[0])
			os.Exit(1)
		}
		if err := store.UpdateIssueState(issue.ID, kanban.State(args[1])); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Moved %s to %s\n", args[0], args[1])

	case "update":
		if len(args) < 1 {
			fmt.Println("Usage: symphony issue update <identifier> [options]")
			os.Exit(1)
		}
		identifier := args[0]
		issue, err := store.GetIssueByIdentifier(identifier)
		if err != nil {
			fmt.Printf("Issue not found: %s\n", identifier)
			os.Exit(1)
		}

		updates := make(map[string]interface{})
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--title":
				updates["title"] = args[i+1]
				i++
			case "--desc":
				updates["description"] = args[i+1]
				i++
			case "--pr":
				var prNum int
				fmt.Sscanf(args[i+1], "%d", &prNum)
				updates["pr_number"] = prNum
				updates["pr_url"] = args[i+2]
				i += 2
			}
		}

		if err := store.UpdateIssue(issue.ID, updates); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Updated issue %s\n", identifier)

	case "delete":
		if len(args) < 1 {
			fmt.Println("Usage: symphony issue delete <identifier>")
			os.Exit(1)
		}
		issue, err := store.GetIssueByIdentifier(args[0])
		if err != nil {
			fmt.Printf("Issue not found: %s\n", args[0])
			os.Exit(1)
		}
		if err := store.DeleteIssue(issue.ID); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Deleted issue %s\n", args[0])

	case "block":
		if len(args) < 2 {
			fmt.Println("Usage: symphony issue block <identifier> <blocker_identifier...>")
			os.Exit(1)
		}
		identifier := args[0]
		blockers := args[1:]

		issue, err := store.GetIssueByIdentifier(identifier)
		if err != nil {
			fmt.Printf("Issue not found: %s\n", identifier)
			os.Exit(1)
		}

		updates := map[string]interface{}{"blocked_by": blockers}
		if err := store.UpdateIssue(issue.ID, updates); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Set blockers for %s: %s\n", identifier, strings.Join(blockers, ", "))

	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}

func runProject() {
	if len(os.Args) < 3 {
		fmt.Print(`Usage:
  symphony project create <name> --repo <repo_path> [--desc <description>] [--workflow <workflow_path>]
  symphony project list
  symphony project delete <id>
`)
		os.Exit(1)
	}

	var dbPath string
	args := os.Args[3:]

	for i := 0; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}

	store := getStore(dbPath)
	defer store.Close()

	cmd := os.Args[2]

	switch cmd {
	case "create":
		if len(args) < 1 {
			fmt.Println("Usage: symphony project create <name> --repo <repo_path> [--desc <description>] [--workflow <workflow_path>]")
			os.Exit(1)
		}
		name := args[0]
		description := ""
		repoPath := ""
		workflowPath := ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--desc":
				if i+1 < len(args) {
					description = args[i+1]
					i++
				}
			case "--repo":
				if i+1 < len(args) {
					repoPath = args[i+1]
					i++
				}
			case "--workflow":
				if i+1 < len(args) {
					workflowPath = args[i+1]
					i++
				}
			}
		}
		if strings.TrimSpace(repoPath) == "" {
			fmt.Println("Error: --repo is required")
			os.Exit(1)
		}

		project, err := store.CreateProject(name, description, repoPath, workflowPath)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Created project %s (ID: %s, Repo: %s)\n", project.Name, project.ID, project.RepoPath)

	case "list":
		projects, err := store.ListProjects()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		for _, p := range projects {
			fmt.Printf("%s\t%s\t%s\t%s\n", p.ID, p.Name, p.RepoPath, p.Description)
		}

	case "delete":
		if len(args) < 1 {
			fmt.Println("Usage: symphony project delete <id>")
			os.Exit(1)
		}
		if err := store.DeleteProject(args[0]); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Project deleted")

	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}

func runSpecCheck() {
	var repoPath string
	jsonOnly := false
	args := os.Args[2:]
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
		_ = json.NewEncoder(os.Stdout).Encode(r)
		if !r.OK {
			os.Exit(1)
		}
		return
	}
	fmt.Println("Spec Check")
	fmt.Println(strings.Repeat("=", 40))
	for k, v := range r.Checks {
		fmt.Printf("%s: %s\n", k, v)
	}
	if !r.OK {
		os.Exit(1)
	}
}

func runVerify() {
	var dbPath, repoPath string
	jsonOnly := false
	args := os.Args[2:]
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
		_ = json.NewEncoder(os.Stdout).Encode(res)
		if !res.OK {
			os.Exit(1)
		}
		return
	}
	fmt.Println("Verification")
	fmt.Println(strings.Repeat("=", 40))
	for k, v := range res.Checks {
		fmt.Printf("%s: %s\n", k, v)
	}
	if len(res.Errors) > 0 {
		fmt.Println("Errors:")
		for _, e := range res.Errors {
			fmt.Printf("- %s\n", e)
		}
	}
	if !res.OK {
		os.Exit(1)
	}
}

func runStatus() {
	var dbPath string
	jsonOnly := false
	dashboard := false
	dashboardURL := ""
	args := os.Args[2:]

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
			_ = json.NewEncoder(os.Stdout).Encode(snapshot)
			return
		}
		fmt.Println(observability.FormatDashboard(snapshot, observability.DashboardOptions{
			Now:          time.Now().UTC(),
			DashboardURL: dashboardURL,
		}))
		return
	}

	if jsonOnly {
		_ = json.NewEncoder(os.Stdout).Encode(data)
		return
	}

	fmt.Println("Symphony Status")
	fmt.Println(strings.Repeat("=", 40))
	fmt.Printf("Projects: %d\n", len(projects))
	fmt.Printf("Total Issues: %d\n", len(issues))
	fmt.Println()
	fmt.Println("Issue Breakdown:")
	for _, state := range []kanban.State{kanban.StateBacklog, kanban.StateReady, kanban.StateInProgress, kanban.StateInReview, kanban.StateDone, kanban.StateCancelled} {
		fmt.Printf("  %s: %d\n", state, counts[state])
	}
}
