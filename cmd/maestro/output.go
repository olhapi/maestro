package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/olhapi/maestro/internal/kanban"
)

type outputMode struct {
	json  bool
	wide  bool
	quiet bool
}

func writeJSON(out io.Writer, value interface{}) error {
	enc := json.NewEncoder(out)
	return enc.Encode(value)
}

func printIssueTable(out io.Writer, issues []kanban.IssueSummary, mode outputMode) {
	if mode.quiet {
		for _, issue := range issues {
			fmt.Fprintln(out, issue.Identifier)
		}
		return
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if mode.wide {
		fmt.Fprintln(tw, "IDENTIFIER\tSTATE\tPRIORITY\tPROJECT\tEPIC\tBLOCKED\tTITLE")
		for _, issue := range issues {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%t\t%s\n",
				issue.Identifier, issue.State, issue.Priority, issue.ProjectName, issue.EpicName, issue.IsBlocked, issue.Title)
		}
	} else {
		fmt.Fprintln(tw, "IDENTIFIER\tSTATE\tPRIORITY\tTITLE")
		for _, issue := range issues {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", issue.Identifier, issue.State, issue.Priority, issue.Title)
		}
	}
	_ = tw.Flush()
}

func printProjectTable(out io.Writer, projects []kanban.ProjectSummary, mode outputMode) {
	if mode.quiet {
		for _, project := range projects {
			fmt.Fprintln(out, project.ID)
		}
		return
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if mode.wide {
		fmt.Fprintln(tw, "ID\tNAME\tREADY\tREPO\tWORKFLOW\tTOTAL\tACTIVE\tDESCRIPTION")
		for _, project := range projects {
			fmt.Fprintf(tw, "%s\t%s\t%t\t%s\t%s\t%d\t%d\t%s\n",
				project.ID, project.Name, project.OrchestrationReady, project.RepoPath, project.WorkflowPath, project.Counts.Total(), project.Counts.Active(), project.Description)
		}
	} else {
		fmt.Fprintln(tw, "ID\tNAME\tTOTAL\tACTIVE\tREPO")
		for _, project := range projects {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n",
				project.ID, project.Name, project.Counts.Total(), project.Counts.Active(), project.RepoPath)
		}
	}
	_ = tw.Flush()
}

func printEpicTable(out io.Writer, epics []kanban.EpicSummary, mode outputMode) {
	if mode.quiet {
		for _, epic := range epics {
			fmt.Fprintln(out, epic.ID)
		}
		return
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if mode.wide {
		fmt.Fprintln(tw, "ID\tNAME\tPROJECT\tTOTAL\tACTIVE\tDESCRIPTION")
		for _, epic := range epics {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n",
				epic.ID, epic.Name, epic.ProjectName, epic.Counts.Total(), epic.Counts.Active(), epic.Description)
		}
	} else {
		fmt.Fprintln(tw, "ID\tNAME\tPROJECT\tTOTAL\tACTIVE")
		for _, epic := range epics {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\n",
				epic.ID, epic.Name, epic.ProjectName, epic.Counts.Total(), epic.Counts.Active())
		}
	}
	_ = tw.Flush()
}

func printIssueDetail(out io.Writer, issue *kanban.IssueDetail) {
	fmt.Fprintf(out, "Identifier:\t%s\n", issue.Identifier)
	fmt.Fprintf(out, "ID:\t%s\n", issue.ID)
	fmt.Fprintf(out, "Title:\t%s\n", issue.Title)
	fmt.Fprintf(out, "State:\t%s\n", issue.State)
	fmt.Fprintf(out, "Phase:\t%s\n", issue.WorkflowPhase)
	fmt.Fprintf(out, "Priority:\t%d\n", issue.Priority)
	if issue.ProjectID != "" {
		fmt.Fprintf(out, "Project:\t%s (%s)\n", issue.ProjectName, issue.ProjectID)
	}
	if issue.EpicID != "" {
		fmt.Fprintf(out, "Epic:\t%s (%s)\n", issue.EpicName, issue.EpicID)
	}
	if issue.Description != "" {
		fmt.Fprintf(out, "Description:\t%s\n", issue.Description)
	}
	if len(issue.Labels) > 0 {
		fmt.Fprintf(out, "Labels:\t%s\n", strings.Join(issue.Labels, ", "))
	}
	if issue.BranchName != "" {
		fmt.Fprintf(out, "Branch:\t%s\n", issue.BranchName)
	}
	if issue.PRNumber != 0 || issue.PRURL != "" {
		fmt.Fprintf(out, "PR:\t#%d %s\n", issue.PRNumber, issue.PRURL)
	}
	if len(issue.BlockedBy) > 0 {
		fmt.Fprintf(out, "Blocked By:\t%s\n", strings.Join(issue.BlockedBy, ", "))
	}
}

func printBoard(out io.Writer, columns map[string][]kanban.IssueSummary, counts kanban.IssueStateCounts, mode outputMode) {
	if mode.quiet {
		states := []string{"backlog", "ready", "in_progress", "in_review", "done", "cancelled"}
		for _, state := range states {
			for _, issue := range columns[state] {
				fmt.Fprintln(out, issue.Identifier)
			}
		}
		return
	}
	fmt.Fprintf(out, "Board\n")
	fmt.Fprintf(out, "Backlog: %d  Ready: %d  In Progress: %d  In Review: %d  Done: %d  Cancelled: %d\n\n",
		counts.Backlog, counts.Ready, counts.InProgress, counts.InReview, counts.Done, counts.Cancelled)
	for _, state := range []string{"backlog", "ready", "in_progress", "in_review", "done", "cancelled"} {
		fmt.Fprintf(out, "%s\n", strings.ToUpper(strings.ReplaceAll(state, "_", " ")))
		items := columns[state]
		if len(items) == 0 {
			fmt.Fprintln(out, "  (empty)")
			continue
		}
		for _, issue := range items {
			if mode.wide {
				fmt.Fprintf(out, "  [%s] %s (p=%d project=%s epic=%s)\n", issue.Identifier, issue.Title, issue.Priority, issue.ProjectName, issue.EpicName)
			} else {
				fmt.Fprintf(out, "  [%s] %s\n", issue.Identifier, issue.Title)
			}
		}
		fmt.Fprintln(out)
	}
}

func printVerification(out io.Writer, title string, res map[string]interface{}) {
	fmt.Fprintln(out, title)
	fmt.Fprintln(out, strings.Repeat("=", len(title)))
	checks, _ := res["checks"].(map[string]string)
	if checks == nil {
		if raw, ok := res["checks"].(map[string]interface{}); ok {
			checks = make(map[string]string, len(raw))
			for k, v := range raw {
				checks[k] = fmt.Sprint(v)
			}
		}
	}
	keys := make([]string, 0, len(checks))
	for k := range checks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(out, "%s: %s\n", key, checks[key])
	}
	if errors, ok := res["errors"].([]string); ok && len(errors) > 0 {
		fmt.Fprintln(out, "Errors:")
		for _, item := range errors {
			fmt.Fprintf(out, "- %s\n", item)
		}
	} else if raw, ok := res["errors"].([]interface{}); ok && len(raw) > 0 {
		fmt.Fprintln(out, "Errors:")
		for _, item := range raw {
			fmt.Fprintf(out, "- %v\n", item)
		}
	}
	if warnings, ok := res["warnings"].([]string); ok && len(warnings) > 0 {
		fmt.Fprintln(out, "Warnings:")
		for _, item := range warnings {
			fmt.Fprintf(out, "- %s\n", item)
		}
	} else if raw, ok := res["warnings"].([]interface{}); ok && len(raw) > 0 {
		fmt.Fprintln(out, "Warnings:")
		for _, item := range raw {
			fmt.Fprintf(out, "- %v\n", item)
		}
	}
	if raw, ok := res["remediation"].(map[string]string); ok && len(raw) > 0 {
		fmt.Fprintln(out, "Remediation:")
		keys = keys[:0]
		for k := range raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(out, "- %s: %s\n", key, raw[key])
		}
	} else if raw, ok := res["remediation"].(map[string]interface{}); ok && len(raw) > 0 {
		fmt.Fprintln(out, "Remediation:")
		keys = keys[:0]
		for k := range raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(out, "- %s: %v\n", key, raw[key])
		}
	}
}

func formatLiveDashboard(payload map[string]interface{}) string {
	counts, _ := payload["counts"].(map[string]interface{})
	codexTotals, _ := payload["codex_totals"].(map[string]interface{})
	var b strings.Builder
	fmt.Fprintln(&b, "MAESTRO STATUS")
	fmt.Fprintf(&b, "generated_at=%v\n", payload["generated_at"])
	fmt.Fprintf(&b, "running=%v retrying=%v total_tokens=%v\n",
		counts["running"], counts["retrying"], codexTotals["total_tokens"])

	if running, ok := payload["running"].([]interface{}); ok && len(running) > 0 {
		fmt.Fprintln(&b, "running_entries:")
		for _, item := range running {
			entry, _ := item.(map[string]interface{})
			fmt.Fprintf(&b, "  %v state=%v session=%v turns=%v started_at=%v event=%v\n",
				entry["issue_identifier"], entry["state"], entry["session_id"], entry["turn_count"], entry["started_at"], entry["last_event"])
		}
	} else {
		fmt.Fprintln(&b, "running_entries=idle")
	}
	if retrying, ok := payload["retrying"].([]interface{}); ok && len(retrying) > 0 {
		fmt.Fprintln(&b, "retry_queue:")
		for _, item := range retrying {
			entry, _ := item.(map[string]interface{})
			fmt.Fprintf(&b, "  %v attempt=%v due_at=%v error=%v\n",
				entry["issue_identifier"], entry["attempt"], entry["due_at"], entry["error"])
		}
	} else {
		fmt.Fprintln(&b, "retry_queue=empty")
	}
	return strings.TrimSpace(b.String())
}

func printSessions(out io.Writer, payload map[string]interface{}, mode outputMode) {
	sessions, _ := payload["sessions"].(map[string]interface{})
	if mode.json {
		_ = writeJSON(out, payload)
		return
	}
	if mode.quiet {
		keys := make([]string, 0, len(sessions))
		for key := range sessions {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintln(out, key)
		}
		return
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ISSUE\tSESSION\tEVENT\tUPDATED")
	keys := make([]string, 0, len(sessions))
	for key := range sessions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		session, _ := sessions[key].(map[string]interface{})
		fmt.Fprintf(tw, "%s\t%v\t%v\t%v\n", key, session["session_id"], session["last_event"], session["last_timestamp"])
	}
	_ = tw.Flush()
}

func printRuntimeEvents(out io.Writer, events []kanban.RuntimeEvent, mode outputMode) {
	if mode.quiet {
		for _, event := range events {
			fmt.Fprintln(out, event.Seq)
		}
		return
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SEQ\tTIME\tKIND\tISSUE\tATTEMPT\tERROR")
	for _, event := range events {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\n",
			event.Seq, event.TS.UTC().Format(time.RFC3339), event.Kind, event.Identifier, event.Attempt, event.Error)
	}
	_ = tw.Flush()
}

func printRuntimeSeries(out io.Writer, series []kanban.RuntimeSeriesPoint) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BUCKET\tSTARTED\tCOMPLETED\tFAILED\tRETRIES\tTOKENS")
	for _, point := range series {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\n",
			point.Bucket, point.RunsStarted, point.RunsCompleted, point.RunsFailed, point.Retries, point.Tokens)
	}
	_ = tw.Flush()
}

func printWorkflowInitNextSteps(out io.Writer, hasAdvisories bool, verifyCmd, projectCmd, runCmd string) {
	fmt.Fprintln(out, "Next steps")
	fmt.Fprintln(out, strings.Repeat("=", len("Next steps")))
	if hasAdvisories {
		fmt.Fprintln(out, "1. Review the warnings and remediation above, then update WORKFLOW.md if needed.")
		fmt.Fprintf(out, "2. Re-run readiness checks: %s\n", verifyCmd)
		fmt.Fprintf(out, "3. Start the orchestrator after the repo is ready: %s\n", runCmd)
		return
	}
	fmt.Fprintf(out, "1. Register the repo: %s\n", projectCmd)
	fmt.Fprintf(out, "2. Start the orchestrator: %s\n", runCmd)
	fmt.Fprintf(out, "3. Re-run readiness checks anytime: %s\n", verifyCmd)
}
