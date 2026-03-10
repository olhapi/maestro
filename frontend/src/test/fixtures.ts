import type { BootstrapResponse, IssueDetail, IssueSummary } from "@/lib/types";

export function makeIssueSummary(
  overrides: Partial<IssueSummary> = {},
): IssueSummary {
  return {
    id: "issue-1",
    project_id: "project-1",
    epic_id: "epic-1",
    identifier: "ISS-1",
    title: "Investigate retries",
    description: "Track runtime retries",
    state: "ready",
    workflow_phase: "implementation",
    priority: 2,
    labels: ["api"],
    blocked_by: [],
    created_at: "2026-03-09T10:00:00Z",
    updated_at: "2026-03-09T11:00:00Z",
    total_tokens_spent: 34,
    project_name: "Platform",
    epic_name: "Observability",
    workspace_path: "/tmp/workspaces/ISS-1",
    workspace_run_count: 1,
    is_blocked: false,
    ...overrides,
  };
}

export function makeIssueDetail(
  overrides: Partial<IssueDetail> = {},
): IssueDetail {
  return {
    ...makeIssueSummary(),
    project_description: "Platform work",
    epic_description: "Observability improvements",
    branch_name: "feature/retries",
    pr_number: 7,
    pr_url: "https://example.com/pr/7",
    ...overrides,
  };
}

export function makeBootstrapResponse(
  overrides: Partial<BootstrapResponse> = {},
): BootstrapResponse {
  const issue = makeIssueSummary();
  return {
    generated_at: "2026-03-09T12:00:00Z",
    overview: {
      status: { active_runs: 1, retry_queue_count: 1 },
      snapshot: {
        generated_at: "2026-03-09T12:00:00Z",
        running: [
          {
            issue_id: issue.id,
            identifier: issue.identifier,
            state: "in_progress",
            phase: "implementation",
            session_id: "thread-1-turn-1",
            turn_count: 2,
            last_event: "turn.started",
            last_message: "Working",
            started_at: "2026-03-09T11:59:00Z",
            tokens: {
              input_tokens: 5,
              output_tokens: 8,
              total_tokens: 13,
              seconds_running: 60,
            },
          },
        ],
        retrying: [
          {
            issue_id: issue.id,
            identifier: issue.identifier,
            phase: "implementation",
            attempt: 2,
            due_at: "2026-03-09T12:05:00Z",
            due_in_ms: 300000,
            error: "approval_required",
            delay_type: "failure",
          },
        ],
        paused: [],
        codex_totals: {
          input_tokens: 5,
          output_tokens: 8,
          total_tokens: 13,
          seconds_running: 60,
        },
      },
      board: {
        backlog: 0,
        ready: 1,
        in_progress: 1,
        in_review: 0,
        done: 2,
        cancelled: 0,
      },
      project_count: 1,
      epic_count: 1,
      issue_count: 1,
      series: [
        {
          bucket: "12:00",
          runs_started: 1,
          runs_completed: 1,
          runs_failed: 0,
          retries: 1,
          tokens: 13,
        },
      ],
      recent_events: [
        {
          seq: 1,
          kind: "run_started",
          identifier: issue.identifier,
          ts: "2026-03-09T12:00:00Z",
        },
      ],
    },
    projects: [
      {
        id: "project-1",
        name: "Platform",
        description: "Platform work",
        repo_path: "/repo",
        workflow_path: "/repo/WORKFLOW.md",
        provider_kind: "kanban",
        provider_project_ref: "",
        provider_config: {},
        capabilities: {
          projects: true,
          epics: true,
          issues: true,
          issue_state_update: true,
          issue_delete: true,
        },
        orchestration_ready: true,
        dispatch_ready: true,
        total_tokens_spent: 34,
        created_at: "2026-03-09T10:00:00Z",
        updated_at: "2026-03-09T10:00:00Z",
        counts: {
          backlog: 0,
          ready: 1,
          in_progress: 1,
          in_review: 0,
          done: 2,
          cancelled: 0,
        },
      },
    ],
    epics: [
      {
        id: "epic-1",
        project_id: "project-1",
        project_name: "Platform",
        name: "Observability",
        description: "Observability improvements",
        created_at: "2026-03-09T10:00:00Z",
        updated_at: "2026-03-09T10:00:00Z",
        counts: {
          backlog: 0,
          ready: 1,
          in_progress: 1,
          in_review: 0,
          done: 2,
          cancelled: 0,
        },
      },
    ],
    issues: {
      items: [issue],
      total: 1,
      limit: 50,
      offset: 0,
    },
    sessions: {
      sessions: {
        [issue.id]: {
          issue_id: issue.id,
          issue_identifier: issue.identifier,
          session_id: "thread-1-turn-1",
          thread_id: "thread-1",
          turn_id: "turn-1",
          last_event: "turn.started",
          last_timestamp: "2026-03-09T12:00:00Z",
          input_tokens: 5,
          output_tokens: 8,
          total_tokens: 13,
          events_processed: 2,
          turns_started: 2,
          turns_completed: 1,
          terminal: false,
          history: [],
        },
      },
      entries: [
        {
          issue_id: issue.id,
          issue_identifier: issue.identifier,
          source: "live",
          active: true,
          status: "active",
          phase: "implementation",
          attempt: 1,
          run_kind: "run_started",
          updated_at: "2026-03-09T12:00:00Z",
          last_event: "turn.started",
          last_message: "Working",
          total_tokens: 13,
          events_processed: 2,
          turns_started: 2,
          turns_completed: 1,
          terminal: false,
          history: [],
        },
      ],
    },
    ...overrides,
  };
}
