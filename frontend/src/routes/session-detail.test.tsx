import type { ReactNode } from "react";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";

import { SessionDetailPage } from "@/routes/session-detail";
import { makeIssueDetail } from "@/test/fixtures";
import { renderWithQueryClient } from "@/test/test-utils";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
  useParams: () => ({ identifier: "ISS-1" }),
}));

vi.mock("@/lib/api", () => ({
  api: {
    getIssue: vi.fn(),
    getIssueExecution: vi.fn(),
  },
}));

const { api } = await import("@/lib/api");

describe("SessionDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows live execution details and links back to the issue page", async () => {
    const issue = makeIssueDetail({ state: "in_progress" });
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: true,
      phase: "implementation",
      attempt_number: 2,
      retry_state: "active",
      session_source: "live",
      session_display_history: [
        {
          id: "session-command-call-1",
          kind: "command",
          title: "Command output",
          summary: "Starting vite dev server",
          detail:
            "$ npm run dev\ncwd: /repo/apps/frontend\n\nStarting vite dev server",
          expandable: true,
          tone: "default",
          event_type: "exec_command_output_delta",
        },
        {
          id: "session-event-1",
          kind: "event",
          title: "Turn started",
          summary: "Applying changes",
          expandable: false,
          tone: "default",
          event_type: "turn.started",
        },
      ],
      session: {
        issue_id: issue.id,
        issue_identifier: issue.identifier,
        session_id: "thread-live-turn-live",
        thread_id: "thread-live",
        turn_id: "turn-live",
        last_event: "turn.started",
        last_timestamp: "2026-03-10T12:00:00Z",
        last_message: "Applying changes",
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 30,
        events_processed: 1,
        turns_started: 2,
        turns_completed: 1,
        terminal: false,
        history: [
          {
            type: "turn.started",
            thread_id: "thread-live",
            turn_id: "turn-live",
            total_tokens: 30,
            message: "Applying changes",
          },
        ],
      },
      runtime_events: [],
      agent_commands: [],
    });

    renderWithQueryClient(<SessionDetailPage />);

    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: issue.title }),
      ).toBeInTheDocument();
    });

    expect(screen.getByText(issue.title)).toBeInTheDocument();
    expect(screen.getByText("Open issue")).toBeInTheDocument();
    expect(screen.getAllByText("Session detail")).toHaveLength(1);
    expect(screen.getByText("Live session")).toBeInTheDocument();
    expect(screen.getByText("Command output")).toBeInTheDocument();
    expect(screen.queryByText("0 tokens")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /expand/i }));
    expect(screen.getByText(/\$ npm run dev/i)).toBeInTheDocument();
    expect(screen.getAllByText("Applying changes").length).toBeGreaterThan(0);
  });

  it("shows persisted paused execution context", async () => {
    const issue = makeIssueDetail({ state: "in_progress" });
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: "implementation",
      attempt_number: 3,
      failure_class: "stall_timeout",
      current_error: "stall_timeout",
      retry_state: "paused",
      paused_at: "2026-03-10T12:05:00Z",
      pause_reason: "stall_timeout",
      consecutive_failures: 3,
      pause_threshold: 3,
      session_source: "persisted",
      session: {
        issue_id: issue.id,
        issue_identifier: issue.identifier,
        session_id: "thread-paused-turn-paused",
        thread_id: "thread-paused",
        turn_id: "turn-paused",
        last_event: "item.started",
        last_timestamp: "2026-03-10T12:05:00Z",
        last_message: "Paused after repeated failures",
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 0,
        events_processed: 1,
        turns_started: 1,
        turns_completed: 0,
        terminal: false,
        history: [],
      },
      runtime_events: [],
      agent_commands: [],
    });

    renderWithQueryClient(<SessionDetailPage />);

    await waitFor(() => {
      expect(screen.getAllByText("Paused").length).toBeGreaterThan(0);
    });

    expect(
      screen.getByText(/Open the issue page to retry/i),
    ).toBeInTheDocument();
    expect(screen.getByText("Persisted session")).toBeInTheDocument();
    expect(
      screen.getByText(/stopped retrying after 3 interrupted runs/i),
    ).toBeInTheDocument();
  });

  it("expands only the clicked history row when command IDs collide", async () => {
    const issue = makeIssueDetail({ state: "in_progress" });
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: true,
      phase: "implementation",
      attempt_number: 2,
      retry_state: "active",
      session_source: "live",
      session_display_history: [
        {
          id: "session-command-call-1",
          kind: "command",
          title: "Command output",
          summary: "First summary",
          detail: "$ npm run dev\nfirst detail chunk",
          expandable: true,
          tone: "default",
          event_type: "exec_command_output_delta",
        },
        {
          id: "session-command-call-1",
          kind: "command",
          title: "Command output",
          summary: "Second summary",
          detail: "$ npm run dev\nsecond detail chunk",
          expandable: true,
          tone: "default",
          event_type: "exec_command_output_delta",
        },
      ],
      session: {
        issue_id: issue.id,
        issue_identifier: issue.identifier,
        session_id: "thread-live-turn-live",
        thread_id: "thread-live",
        turn_id: "turn-live",
        last_event: "turn.started",
        last_timestamp: "2026-03-10T12:00:00Z",
        last_message: "Applying changes",
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 30,
        events_processed: 1,
        turns_started: 2,
        turns_completed: 1,
        terminal: false,
        history: [
          {
            type: "turn.started",
            thread_id: "thread-live",
            turn_id: "turn-live",
            total_tokens: 30,
            message: "Applying changes",
          },
        ],
      },
      runtime_events: [],
      agent_commands: [],
    });

    renderWithQueryClient(<SessionDetailPage />);

    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: issue.title }),
      ).toBeInTheDocument();
    });

    const expandButtons = screen.getAllByRole("button", { name: /expand/i });
    fireEvent.click(expandButtons[1]);

    expect(screen.getByText(/second detail chunk/i)).toBeInTheDocument();
    expect(screen.queryByText(/first detail chunk/i)).not.toBeInTheDocument();
  });
});
