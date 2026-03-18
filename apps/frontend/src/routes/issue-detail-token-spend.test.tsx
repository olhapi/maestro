import type { ReactNode } from "react";
import { screen, waitFor } from "@testing-library/react";
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
    listIssueComments: vi.fn(),
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

describe("IssueDetailPage token spend", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listIssueComments).mockResolvedValue({ items: [] });
  });

  it("shows live session tokens separately from the lifetime issue total", async () => {
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse());
    vi.mocked(api.getIssue).mockResolvedValue(
      makeIssueDetail({ total_tokens_spent: 55 }),
    );
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: "issue-1",
      identifier: "ISS-1",
      active: true,
      phase: "implementation",
      attempt_number: 1,
      retry_state: "none",
      session_source: "live",
      session: {
        issue_id: "issue-1",
        issue_identifier: "ISS-1",
        session_id: "thread-1-turn-1",
        thread_id: "thread-1",
        turn_id: "turn-1",
        last_event: "thread.tokenUsage.updated",
        last_timestamp: "2026-03-09T12:00:00Z",
        last_message: "Working",
        input_tokens: 10,
        output_tokens: 8,
        total_tokens: 18,
        events_processed: 3,
        turns_started: 1,
        turns_completed: 0,
        terminal: false,
      },
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("Session tokens")).toBeInTheDocument();
    });

    expect(screen.getByText("Issue total")).toBeInTheDocument();
    expect(
      screen.getByText("Lifetime tokens across all runs"),
    ).toBeInTheDocument();
    expect(screen.getByText("55")).toBeInTheDocument();
  });
});
