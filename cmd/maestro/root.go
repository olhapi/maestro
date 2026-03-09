package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/olhapi/maestro/internal/httpserver"
	"github.com/olhapi/maestro/internal/kanban"
	"github.com/olhapi/maestro/internal/mcp"
	"github.com/olhapi/maestro/internal/orchestrator"
	"github.com/olhapi/maestro/internal/providers"
	"github.com/olhapi/maestro/internal/speccheck"
	"github.com/olhapi/maestro/internal/verification"
	"github.com/olhapi/maestro/pkg/config"
)

type rootOptions struct {
	dbPath   string
	apiURL   string
	logLevel string
	mode     outputMode
}

type cliApp struct {
	stdout io.Writer
	stderr io.Writer
	opts   rootOptions
}

func execute(args []string, stdout, stderr io.Writer) int {
	cmd := newRootCmd(stdout, stderr)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitCode(err)
	}
	return exitCodeSuccess
}

func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	app := &cliApp{
		stdout: stdout,
		stderr: stderr,
		opts: rootOptions{
			logLevel: "info",
		},
	}

	rootCmd := &cobra.Command{
		Use:           "maestro",
		Short:         "Agent orchestration with local kanban",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return usageErrorf("a command is required")
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			level, _, err := parseLogLevel(app.opts.logLevel)
			if err != nil {
				return usageErrorf("invalid --log-level: %v", err)
			}
			_, err = setupLoggerWithWriter(app.stderr, "", 0, 0, level)
			return err
		},
	}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.PersistentFlags().StringVar(&app.opts.dbPath, "db", "", "Path to the Maestro SQLite database")
	rootCmd.PersistentFlags().StringVar(&app.opts.apiURL, "api-url", "", "Base URL for the live Maestro API")
	rootCmd.PersistentFlags().StringVar(&app.opts.logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	rootCmd.PersistentFlags().BoolVar(&app.opts.mode.json, "json", false, "Emit machine-readable JSON")
	rootCmd.PersistentFlags().BoolVar(&app.opts.mode.wide, "wide", false, "Expand text output with extra columns")
	rootCmd.PersistentFlags().BoolVar(&app.opts.mode.quiet, "quiet", false, "Emit identifiers only in text mode")

	rootCmd.AddCommand(
		app.newRunCmd(),
		app.newMCPCmd(),
		app.newBoardCmd(),
		app.newIssueCmd(),
		app.newProjectCmd(),
		app.newEpicCmd(),
		app.newStatusCmd(),
		app.newVerifyCmd("verify"),
		app.newVerifyCmd("doctor"),
		app.newSpecCheckCmd(),
		app.newWorkflowCmd(),
		app.newSessionsCmd(),
		app.newEventsCmd(),
		app.newRuntimeSeriesCmd(),
		app.newVersionCmd(),
	)
	rootCmd.AddCommand(newCompletionCmd(rootCmd))
	return rootCmd
}

func (a *cliApp) newRunCmd() *cobra.Command {
	var workflowPath string
	var extensionsFile string
	var logsRoot string
	var port string
	var logMaxBytes int64 = 10 * 1024 * 1024
	var logMaxFiles int = 3
	var acknowledgedUnsafe bool

	cmd := &cobra.Command{
		Use:   "run [repo_path]",
		Short: "Start the orchestrator",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			level, _, err := parseLogLevel(a.opts.logLevel)
			if err != nil {
				return usageErrorf("invalid --log-level: %v", err)
			}
			if _, err := setupLoggerWithWriter(a.stdout, logsRoot, logMaxBytes, logMaxFiles, level); err != nil {
				return wrapRuntime(err, "failed to setup logger")
			}
			if !acknowledgedUnsafe {
				_, _ = fmt.Fprintln(a.stderr, guardrailsAcknowledgementBanner())
			}
			store, err := openStore(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			registry, err := loadExtensions(extensionsFile)
			if err != nil {
				return wrapRuntime(err, "failed to load extensions")
			}

			repoPath := ""
			if len(args) == 1 {
				repoPath = args[0]
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			orch := orchestrator.NewSharedWithExtensions(store, registry, repoPath, workflowPath)
			if port != "" {
				addr := port
				if !strings.Contains(addr, ":") {
					addr = ":" + addr
				}
				httpserver.Start(ctx, addr, store, orch)
			}
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigChan
				cancel()
			}()
			if err := orch.Run(ctx); err != nil && err != context.Canceled {
				return wrapRuntime(err, "orchestrator error")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workflowPath, "workflow", "", "Path to WORKFLOW.md")
	cmd.Flags().StringVar(&extensionsFile, "extensions", "", "Path to an extensions JSON file")
	cmd.Flags().StringVar(&logsRoot, "logs-root", "", "Directory for structured logs")
	cmd.Flags().StringVar(&port, "port", "", "HTTP port for observability endpoints")
	cmd.Flags().Int64Var(&logMaxBytes, "log-max-bytes", logMaxBytes, "Max size of the rotating log file")
	cmd.Flags().IntVar(&logMaxFiles, "log-max-files", logMaxFiles, "Number of rotated log files to keep")
	cmd.Flags().BoolVar(&acknowledgedUnsafe, strings.TrimPrefix(guardrailsAcknowledgementFlag, "--"), false, "Silence the guardrails warning")
	return cmd
}

func (a *cliApp) newMCPCmd() *cobra.Command {
	var extensionsFile string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the MCP server over stdio",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			registry, err := loadExtensions(extensionsFile)
			if err != nil {
				return wrapRuntime(err, "failed to load extensions")
			}
			server := mcp.NewServerWithRegistry(store, nil, registry)
			if err := server.ServeStdio(); err != nil {
				return wrapRuntime(err, "mcp server error")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&extensionsFile, "extensions", "", "Path to an extensions JSON file")
	return cmd
}

func (a *cliApp) newWorkflowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Manage WORKFLOW.md files",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return usageErrorf("a workflow subcommand is required")
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "init [repo_path]",
		Short: "Initialize WORKFLOW.md",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repoPath := ""
			if len(args) == 1 {
				repoPath = args[0]
			}
			interactive := isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
			if err := config.InitWorkflow(repoPath, config.InitOptions{
				Interactive: interactive,
				Stdin:       bufio.NewReader(os.Stdin),
				Stdout:      a.stdout,
			}); err != nil {
				return wrapRuntime(err, "failed to initialize workflow")
			}
			_, _ = fmt.Fprintf(a.stdout, "Initialized %s\n", config.WorkflowPath(repoPath))
			return nil
		},
	})
	return cmd
}

func (a *cliApp) newBoardCmd() *cobra.Command {
	var projectID string
	cmd := &cobra.Command{
		Use:   "board",
		Short: "Show the kanban board",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			payload, columns, counts, err := buildBoardPayload(context.Background(), svc, store, projectID)
			if err != nil {
				return wrapRuntime(err, "failed to build board")
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, payload)
			}
			printBoard(a.stdout, columns, counts, a.opts.mode)
			return nil
		},
	}
	cmd.Flags().StringVar(&projectID, "project", "", "Filter by project ID")
	return cmd
}

func (a *cliApp) newIssueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Manage issues",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return usageErrorf("an issue subcommand is required")
		},
	}
	cmd.AddCommand(
		a.newIssueCreateCmd(),
		a.newIssueListCmd(),
		a.newIssueShowCmd(),
		a.newIssueMoveCmd(),
		a.newIssueUpdateCmd(),
		a.newIssueDeleteCmd(),
		a.newIssueExecutionCmd(),
		a.newIssueRetryCmd(),
		a.newIssueUnblockCmd(),
		a.newIssueBlockCmd(),
		a.newIssueBlockersCmd(),
	)
	return cmd
}

func (a *cliApp) newIssueCreateCmd() *cobra.Command {
	var description string
	var projectID string
	var epicID string
	var labels string
	var priority int
	cmd := &cobra.Command{
		Use:   "create <title>",
		Short: "Create an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			detail, err := svc.CreateIssue(context.Background(), providers.IssueCreateInput{
				ProjectID:   projectID,
				EpicID:      epicID,
				Title:       args[0],
				Description: description,
				Priority:    priority,
				Labels:      parseCSV(labels),
			})
			if err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, detail)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, detail.Identifier)
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Created issue %s\n", detail.Identifier)
			return nil
		},
	}
	cmd.Flags().StringVar(&description, "desc", "", "Issue description")
	cmd.Flags().StringVar(&projectID, "project", "", "Project ID")
	cmd.Flags().StringVar(&epicID, "epic", "", "Epic ID")
	cmd.Flags().StringVar(&labels, "labels", "", "Comma-separated labels")
	cmd.Flags().IntVar(&priority, "priority", 0, "Issue priority (lower is higher)")
	return cmd
}

func (a *cliApp) newIssueListCmd() *cobra.Command {
	var state string
	var projectID string
	var projectName string
	var epicID string
	var search string
	var sortBy string = "updated_desc"
	var limit int = 200
	var offset int
	var blocked bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List issues",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			query := kanban.IssueQuery{
				ProjectID:   projectID,
				ProjectName: projectName,
				EpicID:      epicID,
				State:       state,
				Search:      search,
				Sort:        sortBy,
				Limit:       limit,
				Offset:      offset,
			}
			if blocked {
				query.Blocked = &blocked
			}
			issues, total, err := svc.ListIssueSummaries(context.Background(), query)
			if err != nil {
				return wrapRuntime(err, "failed to list issues")
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, map[string]interface{}{
					"items":  issues,
					"total":  total,
					"limit":  limit,
					"offset": offset,
					"sort":   sortBy,
				})
			}
			printIssueTable(a.stdout, issues, a.opts.mode)
			return nil
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "Filter by state")
	cmd.Flags().StringVar(&projectID, "project", "", "Filter by project ID")
	cmd.Flags().StringVar(&projectName, "project-name", "", "Filter by exact project name")
	cmd.Flags().StringVar(&epicID, "epic", "", "Filter by epic ID")
	cmd.Flags().StringVar(&search, "search", "", "Search identifier, title, or description")
	cmd.Flags().StringVar(&sortBy, "sort", sortBy, "Sort: updated_desc, created_asc, priority_asc, identifier_asc, state_asc")
	cmd.Flags().IntVar(&limit, "limit", limit, "Maximum issues to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "Number of issues to skip")
	cmd.Flags().BoolVar(&blocked, "blocked", false, "Only show blocked issues")
	return cmd
}

func (a *cliApp) newIssueShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <identifier>",
		Short: "Show a single issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			detail, err := svc.GetIssueDetailByIdentifier(context.Background(), args[0])
			if err != nil {
				if kanban.IsNotFound(err) || err == os.ErrNotExist {
					return notFoundErrorf("issue not found: %s", args[0])
				}
				if strings.Contains(err.Error(), "no rows") {
					return notFoundErrorf("issue not found: %s", args[0])
				}
				return wrapRuntime(err, "failed to load issue")
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, detail)
			}
			printIssueDetail(a.stdout, detail)
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newIssueMoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "move <identifier> <state>",
		Aliases: []string{"mv"},
		Short:   "Change an issue state",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			detail, err := svc.SetIssueState(context.Background(), args[0], args[1])
			if err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, detail)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, detail.Identifier)
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Moved %s to %s\n", detail.Identifier, detail.State)
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newIssueUpdateCmd() *cobra.Command {
	var title string
	var description string
	var projectID string
	var epicID string
	var labels string
	var priority int
	var branch string
	var prNumber int
	var prURL string
	var clearLabels bool
	var clearPriority bool
	var clearProject bool
	var clearEpic bool
	var clearBranch bool
	var clearPR bool

	cmd := &cobra.Command{
		Use:   "update <identifier>",
		Short: "Update an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()

			updates := map[string]interface{}{}
			if cmd.Flags().Changed("title") {
				updates["title"] = title
			}
			if cmd.Flags().Changed("desc") {
				updates["description"] = description
			}
			if cmd.Flags().Changed("labels") {
				updates["labels"] = parseCSV(labels)
			}
			if clearLabels {
				updates["labels"] = []string{}
			}
			if cmd.Flags().Changed("priority") {
				updates["priority"] = priority
			}
			if clearPriority {
				updates["priority"] = 0
			}
			if cmd.Flags().Changed("project") {
				updates["project_id"] = projectID
			}
			if clearProject {
				updates["project_id"] = ""
			}
			if cmd.Flags().Changed("epic") {
				updates["epic_id"] = epicID
			}
			if clearEpic {
				updates["epic_id"] = ""
			}
			if cmd.Flags().Changed("branch") {
				updates["branch_name"] = branch
			}
			if clearBranch {
				updates["branch_name"] = ""
			}
			if cmd.Flags().Changed("pr") {
				updates["pr_number"] = prNumber
			}
			if cmd.Flags().Changed("pr-url") {
				updates["pr_url"] = prURL
			}
			if clearPR {
				updates["pr_number"] = 0
				updates["pr_url"] = ""
			}
			if len(updates) == 0 {
				return usageErrorf("no updates specified")
			}
			detail, err := svc.UpdateIssue(context.Background(), args[0], updates)
			if err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, detail)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, detail.Identifier)
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Updated issue %s\n", detail.Identifier)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "New title")
	cmd.Flags().StringVar(&description, "desc", "", "New description")
	cmd.Flags().StringVar(&labels, "labels", "", "Comma-separated labels")
	cmd.Flags().IntVar(&priority, "priority", 0, "New priority")
	cmd.Flags().StringVar(&projectID, "project", "", "Project ID")
	cmd.Flags().StringVar(&epicID, "epic", "", "Epic ID")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch name")
	cmd.Flags().IntVar(&prNumber, "pr", 0, "Pull request number")
	cmd.Flags().StringVar(&prURL, "pr-url", "", "Pull request URL")
	cmd.Flags().BoolVar(&clearLabels, "clear-labels", false, "Clear all labels")
	cmd.Flags().BoolVar(&clearPriority, "clear-priority", false, "Clear the priority")
	cmd.Flags().BoolVar(&clearProject, "clear-project", false, "Clear the project")
	cmd.Flags().BoolVar(&clearEpic, "clear-epic", false, "Clear the epic")
	cmd.Flags().BoolVar(&clearBranch, "clear-branch", false, "Clear the branch name")
	cmd.Flags().BoolVar(&clearPR, "clear-pr", false, "Clear the pull request number and URL")
	return cmd
}

func (a *cliApp) newIssueDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <identifier>",
		Short: "Delete an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			issue, err := svc.GetIssueByIdentifier(context.Background(), args[0])
			if err != nil {
				return notFoundErrorf("issue not found: %s", args[0])
			}
			if err := svc.DeleteIssue(context.Background(), issue.Identifier); err != nil {
				return err
			}
			payload := map[string]interface{}{"deleted": true, "identifier": issue.Identifier}
			if a.opts.mode.json {
				return writeJSON(a.stdout, payload)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, issue.Identifier)
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Deleted issue %s\n", issue.Identifier)
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newIssueExecutionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "execution <identifier>",
		Short: "Show live execution details for an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(a.opts.apiURL)
			if err != nil {
				return err
			}
			var payload map[string]interface{}
			if err := client.get("/api/v1/app/issues/"+url.PathEscape(args[0])+"/execution", &payload); err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, payload)
			}
			_, _ = fmt.Fprintf(a.stdout, "Identifier:\t%v\n", payload["identifier"])
			_, _ = fmt.Fprintf(a.stdout, "Active:\t%v\n", payload["active"])
			_, _ = fmt.Fprintf(a.stdout, "Phase:\t%v\n", payload["phase"])
			_, _ = fmt.Fprintf(a.stdout, "Attempt:\t%v\n", payload["attempt_number"])
			_, _ = fmt.Fprintf(a.stdout, "Retry State:\t%v\n", payload["retry_state"])
			_, _ = fmt.Fprintf(a.stdout, "Failure Class:\t%v\n", payload["failure_class"])
			_, _ = fmt.Fprintf(a.stdout, "Current Error:\t%v\n", payload["current_error"])
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newIssueRetryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retry <identifier>",
		Short: "Request an immediate live retry for an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(a.opts.apiURL)
			if err != nil {
				return err
			}
			var payload map[string]interface{}
			if err := client.post("/api/v1/app/issues/"+url.PathEscape(args[0])+"/retry", map[string]interface{}{}, &payload); err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, payload)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, args[0])
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Retry status for %s: %v\n", args[0], payload["status"])
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newIssueUnblockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unblock <identifier> <blocker_identifier...>",
		Short: "Remove one or more blockers from an issue",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			issue, err := store.GetIssueByIdentifier(args[0])
			if err != nil {
				return notFoundErrorf("issue not found: %s", args[0])
			}
			remove := map[string]struct{}{}
			for _, blocker := range args[1:] {
				remove[blocker] = struct{}{}
			}
			kept := make([]string, 0, len(issue.BlockedBy))
			for _, blocker := range issue.BlockedBy {
				if _, ok := remove[blocker]; ok {
					continue
				}
				kept = append(kept, blocker)
			}
			persisted, err := store.SetIssueBlockers(issue.ID, kept)
			if err != nil {
				return err
			}
			payload := map[string]interface{}{"identifier": issue.Identifier, "blocked_by": persisted}
			if a.opts.mode.json {
				return writeJSON(a.stdout, payload)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, issue.Identifier)
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Updated blockers for %s\n", issue.Identifier)
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newIssueBlockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "block <identifier> [blocker_identifier...]",
		Short: "Alias for `issue blockers set`",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runIssueBlockersSet(args[0], args[1:])
		},
	}
}

func (a *cliApp) newIssueBlockersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blockers",
		Short: "Manage issue blockers",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "set <identifier> [blocker_identifier...]",
			Short: "Replace the full blocker set for an issue",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.runIssueBlockersSet(args[0], args[1:])
			},
		},
		&cobra.Command{
			Use:   "clear <identifier>",
			Short: "Clear all blockers from an issue",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return a.runIssueBlockersSet(args[0], nil)
			},
		},
	)
	return cmd
}

func (a *cliApp) runIssueBlockersSet(identifier string, blockers []string) error {
	store, err := openStore(a.opts.dbPath)
	if err != nil {
		return wrapRuntime(err, "failed to open database")
	}
	defer store.Close()
	issue, err := store.GetIssueByIdentifier(identifier)
	if err != nil {
		return notFoundErrorf("issue not found: %s", identifier)
	}
	persisted, err := store.SetIssueBlockers(issue.ID, blockers)
	if err != nil {
		return err
	}
	payload := map[string]interface{}{"identifier": issue.Identifier, "blocked_by": persisted}
	if a.opts.mode.json {
		return writeJSON(a.stdout, payload)
	}
	if a.opts.mode.quiet {
		_, _ = fmt.Fprintln(a.stdout, issue.Identifier)
		return nil
	}
	_, _ = fmt.Fprintf(a.stdout, "Updated blockers for %s\n", issue.Identifier)
	return nil
}

func (a *cliApp) newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return usageErrorf("a project subcommand is required")
		},
	}
	cmd.AddCommand(
		a.newProjectCreateCmd(),
		a.newProjectListCmd(),
		a.newProjectShowCmd(),
		a.newProjectUpdateCmd(),
		a.newProjectDeleteCmd(),
	)
	return cmd
}

func (a *cliApp) newProjectCreateCmd() *cobra.Command {
	var description string
	var repoPath string
	var workflowPath string
	var providerKind string
	var providerProjectRef string
	var providerEndpoint string
	var providerAssignee string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(repoPath) == "" {
				return usageErrorf("--repo is required")
			}
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			providerConfig := map[string]interface{}{}
			if strings.TrimSpace(providerEndpoint) != "" {
				providerConfig["endpoint"] = strings.TrimSpace(providerEndpoint)
			}
			if strings.TrimSpace(providerAssignee) != "" {
				providerConfig["assignee"] = strings.TrimSpace(providerAssignee)
			}
			project, err := svc.CreateProject(context.Background(), args[0], description, repoPath, workflowPath, providerKind, providerProjectRef, providerConfig)
			if err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, project)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, project.ID)
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Created project %s\n", project.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&description, "desc", "", "Project description")
	cmd.Flags().StringVar(&repoPath, "repo", "", "Absolute path to the repo")
	cmd.Flags().StringVar(&workflowPath, "workflow", "", "Optional workflow path override")
	cmd.Flags().StringVar(&providerKind, "provider", kanban.ProviderKindKanban, "Provider kind: kanban or linear")
	cmd.Flags().StringVar(&providerProjectRef, "provider-project-ref", "", "Provider project reference (for linear: project slug)")
	cmd.Flags().StringVar(&providerEndpoint, "provider-endpoint", "", "Optional provider endpoint override")
	cmd.Flags().StringVar(&providerAssignee, "provider-assignee", "", "Optional provider assignee filter (Linear supports assignee IDs and 'me')")
	return cmd
}

func (a *cliApp) newProjectListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			projects, err := svc.ListProjectSummaries()
			if err != nil {
				return wrapRuntime(err, "failed to list projects")
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, map[string]interface{}{"items": projects})
			}
			printProjectTable(a.stdout, projects, a.opts.mode)
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newProjectShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a project and its related data",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			payload, project, issues, err := buildProjectPayload(context.Background(), svc, store, args[0])
			if err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, payload)
			}
			_, _ = fmt.Fprintf(a.stdout, "Project:\t%s (%s)\n", project.Name, project.ID)
			_, _ = fmt.Fprintf(a.stdout, "Provider:\t%s\n", project.ProviderKind)
			if project.ProviderProjectRef != "" {
				_, _ = fmt.Fprintf(a.stdout, "Provider Ref:\t%s\n", project.ProviderProjectRef)
			}
			_, _ = fmt.Fprintf(a.stdout, "Repo:\t%s\n", project.RepoPath)
			_, _ = fmt.Fprintf(a.stdout, "Workflow:\t%s\n", project.WorkflowPath)
			_, _ = fmt.Fprintf(a.stdout, "Ready:\t%t\n", project.OrchestrationReady)
			if project.Description != "" {
				_, _ = fmt.Fprintf(a.stdout, "Description:\t%s\n", project.Description)
			}
			_, _ = fmt.Fprintln(a.stdout)
			printIssueTable(a.stdout, issues, a.opts.mode)
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newProjectUpdateCmd() *cobra.Command {
	var name string
	var description string
	var repoPath string
	var workflowPath string
	var providerKind string
	var providerProjectRef string
	var providerEndpoint string
	var providerAssignee string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			project, err := store.GetProject(args[0])
			if err != nil {
				return notFoundErrorf("project not found: %s", args[0])
			}
			if cmd.Flags().Changed("name") {
				project.Name = name
			}
			if cmd.Flags().Changed("desc") {
				project.Description = description
			}
			if cmd.Flags().Changed("repo") {
				project.RepoPath = repoPath
			}
			if cmd.Flags().Changed("workflow") {
				project.WorkflowPath = workflowPath
			}
			if cmd.Flags().Changed("provider") {
				project.ProviderKind = providerKind
			}
			if cmd.Flags().Changed("provider-project-ref") {
				project.ProviderProjectRef = providerProjectRef
			}
			if cmd.Flags().Changed("provider-endpoint") {
				if project.ProviderConfig == nil {
					project.ProviderConfig = map[string]interface{}{}
				}
				if strings.TrimSpace(providerEndpoint) == "" {
					delete(project.ProviderConfig, "endpoint")
				} else {
					project.ProviderConfig["endpoint"] = strings.TrimSpace(providerEndpoint)
				}
			}
			if cmd.Flags().Changed("provider-assignee") {
				if project.ProviderConfig == nil {
					project.ProviderConfig = map[string]interface{}{}
				}
				if strings.TrimSpace(providerAssignee) == "" {
					delete(project.ProviderConfig, "assignee")
				} else {
					project.ProviderConfig["assignee"] = strings.TrimSpace(providerAssignee)
				}
			}
			if err := svc.UpdateProject(context.Background(), project.ID, project.Name, project.Description, project.RepoPath, project.WorkflowPath, project.ProviderKind, project.ProviderProjectRef, project.ProviderConfig); err != nil {
				return err
			}
			updated, err := store.GetProject(project.ID)
			if err != nil {
				return wrapRuntime(err, "failed to reload project")
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, updated)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, updated.ID)
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Updated project %s\n", updated.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New project name")
	cmd.Flags().StringVar(&description, "desc", "", "New project description")
	cmd.Flags().StringVar(&repoPath, "repo", "", "Absolute repo path")
	cmd.Flags().StringVar(&workflowPath, "workflow", "", "Workflow path override")
	cmd.Flags().StringVar(&providerKind, "provider", "", "Provider kind: kanban or linear")
	cmd.Flags().StringVar(&providerProjectRef, "provider-project-ref", "", "Provider project reference")
	cmd.Flags().StringVar(&providerEndpoint, "provider-endpoint", "", "Optional provider endpoint override")
	cmd.Flags().StringVar(&providerAssignee, "provider-assignee", "", "Optional provider assignee filter")
	return cmd
}

func (a *cliApp) newProjectDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			if err := store.DeleteProject(args[0]); err != nil {
				return err
			}
			payload := map[string]interface{}{"deleted": true, "id": args[0]}
			if a.opts.mode.json {
				return writeJSON(a.stdout, payload)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, args[0])
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Deleted project %s\n", args[0])
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newEpicCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "epic",
		Short: "Manage epics",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return usageErrorf("an epic subcommand is required")
		},
	}
	cmd.AddCommand(
		a.newEpicCreateCmd(),
		a.newEpicListCmd(),
		a.newEpicShowCmd(),
		a.newEpicUpdateCmd(),
		a.newEpicDeleteCmd(),
	)
	return cmd
}

func (a *cliApp) newEpicCreateCmd() *cobra.Command {
	var projectID string
	var description string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an epic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(projectID) == "" {
				return usageErrorf("--project is required")
			}
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			epic, err := svc.CreateEpic(projectID, args[0], description)
			if err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, epic)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, epic.ID)
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Created epic %s\n", epic.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&projectID, "project", "", "Project ID")
	cmd.Flags().StringVar(&description, "desc", "", "Epic description")
	return cmd
}

func (a *cliApp) newEpicListCmd() *cobra.Command {
	var projectID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List epics",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			epics, err := svc.ListEpicSummaries(projectID)
			if err != nil {
				return wrapRuntime(err, "failed to list epics")
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, map[string]interface{}{"items": epics})
			}
			printEpicTable(a.stdout, epics, a.opts.mode)
			return nil
		},
	}
	cmd.Flags().StringVar(&projectID, "project", "", "Filter by project ID")
	return cmd
}

func (a *cliApp) newEpicShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show an epic and its related data",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			payload, epic, issues, err := buildEpicPayload(context.Background(), svc, store, args[0])
			if err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, payload)
			}
			_, _ = fmt.Fprintf(a.stdout, "Epic:\t%s (%s)\n", epic.Name, epic.ID)
			_, _ = fmt.Fprintf(a.stdout, "Project:\t%s\n", epic.ProjectName)
			if epic.Description != "" {
				_, _ = fmt.Fprintf(a.stdout, "Description:\t%s\n", epic.Description)
			}
			_, _ = fmt.Fprintln(a.stdout)
			printIssueTable(a.stdout, issues, a.opts.mode)
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newEpicUpdateCmd() *cobra.Command {
	var name string
	var description string
	var projectID string
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an epic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			epic, err := store.GetEpic(args[0])
			if err != nil {
				return notFoundErrorf("epic not found: %s", args[0])
			}
			if cmd.Flags().Changed("name") {
				epic.Name = name
			}
			if cmd.Flags().Changed("desc") {
				epic.Description = description
			}
			if cmd.Flags().Changed("project") {
				epic.ProjectID = projectID
			}
			if err := svc.UpdateEpic(epic.ID, epic.ProjectID, epic.Name, epic.Description); err != nil {
				return err
			}
			updated, err := store.GetEpic(epic.ID)
			if err != nil {
				return wrapRuntime(err, "failed to reload epic")
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, updated)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, updated.ID)
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Updated epic %s\n", updated.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New epic name")
	cmd.Flags().StringVar(&description, "desc", "", "New epic description")
	cmd.Flags().StringVar(&projectID, "project", "", "Project ID")
	return cmd
}

func (a *cliApp) newEpicDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an epic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, svc, err := openProviderService(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			if err := svc.DeleteEpic(args[0]); err != nil {
				return err
			}
			payload := map[string]interface{}{"deleted": true, "id": args[0]}
			if a.opts.mode.json {
				return writeJSON(a.stdout, payload)
			}
			if a.opts.mode.quiet {
				_, _ = fmt.Fprintln(a.stdout, args[0])
				return nil
			}
			_, _ = fmt.Fprintf(a.stdout, "Deleted epic %s\n", args[0])
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newStatusCmd() *cobra.Command {
	var dashboard bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Maestro status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dashboard {
				client, err := newAPIClient(a.opts.apiURL)
				if err != nil {
					return err
				}
				var payload map[string]interface{}
				if err := client.get("/api/v1/state", &payload); err != nil {
					return err
				}
				if a.opts.mode.json {
					return writeJSON(a.stdout, payload)
				}
				_, _ = fmt.Fprintln(a.stdout, formatLiveDashboard(payload))
				return nil
			}

			store, err := openStore(a.opts.dbPath)
			if err != nil {
				return wrapRuntime(err, "failed to open database")
			}
			defer store.Close()
			issues, err := store.ListIssues(nil)
			if err != nil {
				return wrapRuntime(err, "failed to list issues")
			}
			projects, err := store.ListProjects()
			if err != nil {
				return wrapRuntime(err, "failed to list projects")
			}
			counts := map[kanban.State]int{}
			for _, issue := range issues {
				counts[issue.State]++
			}
			payload := map[string]interface{}{
				"projects": len(projects),
				"issues":   counts,
				"total":    len(issues),
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, payload)
			}
			_, _ = fmt.Fprintln(a.stdout, "Maestro Status")
			_, _ = fmt.Fprintln(a.stdout, "==============")
			_, _ = fmt.Fprintf(a.stdout, "Projects: %d\n", len(projects))
			_, _ = fmt.Fprintf(a.stdout, "Total Issues: %d\n", len(issues))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dashboard, "dashboard", false, "Render live runtime dashboard data")
	return cmd
}

func (a *cliApp) newVerifyCmd(use string) *cobra.Command {
	var repoPath string
	title := "Verification"
	short := "Run local readiness checks"
	if use == "doctor" {
		title = "Doctor"
		short = "Run readiness checks with remediation guidance"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			res := verification.Run(repoPath, a.opts.dbPath)
			if a.opts.mode.json {
				return writeJSON(a.stdout, res)
			}
			payload := map[string]interface{}{
				"checks":      res.Checks,
				"errors":      res.Errors,
				"warnings":    res.Warnings,
				"remediation": res.Remediation,
			}
			printVerification(a.stdout, title, payload)
			if !res.OK {
				return runtimeErrorf("verification failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", "", "Repository path")
	return cmd
}

func (a *cliApp) newSpecCheckCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "spec-check",
		Short: "Run lightweight Maestro spec conformance checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			res := speccheck.Run(repoPath)
			if a.opts.mode.json {
				return writeJSON(a.stdout, res)
			}
			payload := map[string]interface{}{"checks": res.Checks}
			printVerification(a.stdout, "Spec Check", payload)
			if !res.OK {
				return runtimeErrorf("spec check failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoPath, "repo", "", "Repository path")
	return cmd
}

func (a *cliApp) newSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List live sessions from the Maestro API",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(a.opts.apiURL)
			if err != nil {
				return err
			}
			var payload map[string]interface{}
			if err := client.get("/api/v1/sessions", &payload); err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, payload)
			}
			printSessions(a.stdout, payload, a.opts.mode)
			return nil
		},
	}
	return cmd
}

func (a *cliApp) newEventsCmd() *cobra.Command {
	var since int64
	var limit int = 100
	cmd := &cobra.Command{
		Use:   "events",
		Short: "List live runtime events from the Maestro API",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(a.opts.apiURL)
			if err != nil {
				return err
			}
			var payload struct {
				Events []kanban.RuntimeEvent `json:"events"`
			}
			path := fmt.Sprintf("/api/v1/events?since=%d&limit=%d", since, limit)
			if err := client.get(path, &payload); err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, map[string]interface{}{"events": payload.Events})
			}
			printRuntimeEvents(a.stdout, payload.Events, a.opts.mode)
			return nil
		},
	}
	cmd.Flags().Int64Var(&since, "since", 0, "Only return events with seq greater than this value")
	cmd.Flags().IntVar(&limit, "limit", limit, "Maximum events to return")
	return cmd
}

func (a *cliApp) newRuntimeSeriesCmd() *cobra.Command {
	var hours int = 24
	cmd := &cobra.Command{
		Use:   "runtime-series",
		Short: "Show runtime series data from the Maestro API",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(a.opts.apiURL)
			if err != nil {
				return err
			}
			var payload struct {
				Series []kanban.RuntimeSeriesPoint `json:"series"`
			}
			if err := client.get(fmt.Sprintf("/api/v1/app/runtime/series?hours=%d", hours), &payload); err != nil {
				return err
			}
			if a.opts.mode.json {
				return writeJSON(a.stdout, map[string]interface{}{"series": payload.Series})
			}
			printRuntimeSeries(a.stdout, payload.Series)
			return nil
		},
	}
	cmd.Flags().IntVar(&hours, "hours", hours, "Number of hours to include")
	return cmd
}

func (a *cliApp) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintf(a.stdout, "maestro %s\n", version)
			return nil
		},
	}
}

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "completion <bash|zsh|fish>",
		Short: "Generate shell completion scripts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			default:
				return usageErrorf("unsupported shell %q", args[0])
			}
		},
	}
}

func buildBoardPayload(ctx context.Context, svc *providers.Service, store *kanban.Store, projectID string) (map[string]interface{}, map[string][]kanban.IssueSummary, kanban.IssueStateCounts, error) {
	issues, total, err := svc.ListIssueSummaries(ctx, kanban.IssueQuery{
		ProjectID: projectID,
		Sort:      "updated_desc",
		Limit:     500,
	})
	if err != nil {
		return nil, nil, kanban.IssueStateCounts{}, err
	}
	columns := map[string][]kanban.IssueSummary{
		"backlog":     {},
		"ready":       {},
		"in_progress": {},
		"in_review":   {},
		"done":        {},
		"cancelled":   {},
	}
	counts := kanban.IssueStateCounts{}
	for _, issue := range issues {
		columns[string(issue.State)] = append(columns[string(issue.State)], issue)
		counts.Add(issue.State)
	}
	payload := map[string]interface{}{
		"project_id": projectID,
		"total":      total,
		"counts":     counts,
		"state_buckets": kanban.BuildStateBuckets(map[string]int{
			"backlog":     counts.Backlog,
			"ready":       counts.Ready,
			"in_progress": counts.InProgress,
			"in_review":   counts.InReview,
			"done":        counts.Done,
			"cancelled":   counts.Cancelled,
		}, []string{"ready", "in_progress", "in_review"}, []string{"done", "cancelled"}),
		"columns": columns,
	}
	return payload, columns, counts, nil
}

func buildProjectPayload(ctx context.Context, svc *providers.Service, store *kanban.Store, id string) (map[string]interface{}, *kanban.ProjectSummary, []kanban.IssueSummary, error) {
	projectSummaries, err := svc.ListProjectSummaries()
	if err != nil {
		return nil, nil, nil, err
	}
	var project *kanban.ProjectSummary
	for i := range projectSummaries {
		if projectSummaries[i].ID == id {
			project = &projectSummaries[i]
			break
		}
	}
	if project == nil {
		return nil, nil, nil, notFoundErrorf("project not found: %s", id)
	}
	epics, err := svc.ListEpicSummaries(id)
	if err != nil {
		return nil, nil, nil, err
	}
	issues, total, err := svc.ListIssueSummaries(ctx, kanban.IssueQuery{ProjectID: id, Sort: "updated_desc", Limit: 200})
	if err != nil {
		return nil, nil, nil, err
	}
	payload := map[string]interface{}{
		"project": project,
		"epics":   epics,
		"issues": map[string]interface{}{
			"items":  issues,
			"total":  total,
			"limit":  200,
			"offset": 0,
		},
	}
	return payload, project, issues, nil
}

func buildEpicPayload(ctx context.Context, svc *providers.Service, store *kanban.Store, id string) (map[string]interface{}, *kanban.EpicSummary, []kanban.IssueSummary, error) {
	epicSummaries, err := svc.ListEpicSummaries("")
	if err != nil {
		return nil, nil, nil, err
	}
	var epic *kanban.EpicSummary
	for i := range epicSummaries {
		if epicSummaries[i].ID == id {
			epic = &epicSummaries[i]
			break
		}
	}
	if epic == nil {
		return nil, nil, nil, notFoundErrorf("epic not found: %s", id)
	}
	var project *kanban.Project
	if epic.ProjectID != "" {
		project, err = store.GetProject(epic.ProjectID)
		if err != nil && !kanban.IsNotFound(err) {
			return nil, nil, nil, err
		}
	}
	siblingEpics, err := svc.ListEpicSummaries(epic.ProjectID)
	if err != nil {
		return nil, nil, nil, err
	}
	issues, total, err := svc.ListIssueSummaries(ctx, kanban.IssueQuery{EpicID: id, Sort: "updated_desc", Limit: 200})
	if err != nil {
		return nil, nil, nil, err
	}
	payload := map[string]interface{}{
		"epic":          epic,
		"project":       project,
		"sibling_epics": siblingEpics,
		"issues": map[string]interface{}{
			"items":  issues,
			"total":  total,
			"limit":  200,
			"offset": 0,
		},
	}
	return payload, epic, issues, nil
}
