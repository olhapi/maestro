import type { ReactNode } from "react";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";

import { IssueDetailPage } from "@/routes/issue-detail";
import { makeBootstrapResponse, makeIssueDetail } from "@/test/fixtures";
import { renderWithQueryClient } from "@/test/test-utils";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
  useNavigate: () => vi.fn(),
  useParams: () => ({ identifier: "ISS-1" }),
}));

vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("@/lib/api", () => ({
  api: {
    bootstrap: vi.fn(),
    getIssue: vi.fn(),
    getIssueExecution: vi.fn(),
    retryIssue: vi.fn(),
    deleteIssue: vi.fn(),
    updateIssue: vi.fn(),
    setIssueState: vi.fn(),
    setIssueBlockers: vi.fn(),
    sendIssueCommand: vi.fn(),
  },
}));

const { api } = await import("@/lib/api");

describe("IssueDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows interrupted persisted session details instead of an idle no-session view", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({ state: "in_progress" });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: "implementation",
      attempt_number: 2,
      failure_class: "run_interrupted",
      current_error: "run_interrupted",
      retry_state: "none",
      session_source: "persisted",
      session: {
        issue_id: issue.id,
        issue_identifier: issue.identifier,
        session_id: "thread-stale-turn-stale",
        thread_id: "thread-stale",
        turn_id: "turn-stale",
        last_event: "turn.started",
        last_timestamp: "2026-03-09T12:00:00Z",
        last_message: "",
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

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("Last run interrupted")).toBeInTheDocument();
    });

    expect(screen.getByText("Interrupted")).toBeInTheDocument();
    expect(screen.getByText(/Last session update/i)).toBeInTheDocument();
    expect(screen.getByText("Persisted")).toBeInTheDocument();
  });

  it("shows a paused retry banner when automatic retries are halted", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({ state: "in_progress" });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
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
      paused_at: "2026-03-09T12:05:00Z",
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
        last_timestamp: "2026-03-09T12:05:00Z",
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

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(
        screen.getByText(/stopped retrying after 3 interrupted runs/i),
      ).toBeInTheDocument();
    });

    expect(screen.getAllByText("Paused").length).toBeGreaterThan(0);
    expect(
      screen.getByText(/stopped retrying after 3 interrupted runs/i),
    ).toBeInTheDocument();
  });

  it("submits agent commands and renders the command log", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({ state: "done" });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.sendIssueCommand).mockResolvedValue({ ok: true });
    vi.mocked(api.getIssueExecution)
      .mockResolvedValueOnce({
        issue_id: issue.id,
        identifier: issue.identifier,
        active: false,
        phase: "done",
        attempt_number: 0,
        retry_state: "none",
        session_source: "none",
        runtime_events: [],
        agent_commands: [],
      })
      .mockResolvedValue({
        issue_id: issue.id,
        identifier: issue.identifier,
        active: false,
        phase: "implementation",
        attempt_number: 1,
        retry_state: "none",
        session_source: "persisted",
        runtime_events: [],
        agent_commands: [
          {
            id: "cmd-1",
            issue_id: issue.id,
            command: "Merge the branch to master.",
            status: "waiting_for_unblock",
            created_at: "2026-03-09T12:10:00Z",
          },
        ],
      });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("Agent commands")).toBeInTheDocument();
    });

    fireEvent.change(screen.getByPlaceholderText(/tell the agent/i), {
      target: { value: "Merge the branch to master." },
    });
    fireEvent.click(screen.getByRole("button", { name: /send to agent/i }));

    await waitFor(() => {
      expect(api.sendIssueCommand).toHaveBeenCalledWith(
        issue.identifier,
        "Merge the branch to master.",
      );
    });

    await waitFor(() => {
      expect(screen.getByText("Waiting for unblock")).toBeInTheDocument();
    });
    expect(screen.getByText("Merge the branch to master.")).toBeInTheDocument();
  });
});
