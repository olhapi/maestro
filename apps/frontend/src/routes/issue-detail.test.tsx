import type { ReactNode } from "react";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { vi } from "vitest";

import { IssueDetailPage } from "@/routes/issue-detail";
import {
  makeBootstrapResponse,
  makeIssueDetail,
  makeIssueImage,
} from "@/test/fixtures";
import { renderWithQueryClient, selectOption } from "@/test/test-utils";
import { formatDateTime } from "@/lib/utils";

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
    runIssueNow: vi.fn(),
    deleteIssue: vi.fn(),
    updateIssue: vi.fn(),
    setIssueState: vi.fn(),
    setIssueBlockers: vi.fn(),
    sendIssueCommand: vi.fn(),
    uploadIssueImage: vi.fn(),
    deleteIssueImage: vi.fn(),
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
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("Persisted")).toBeInTheDocument();
    });

    expect(screen.getByText("Interrupted")).toBeInTheDocument();
    expect(screen.getByText("Activity log")).toBeInTheDocument();
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
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(
        screen.getByText(/stopped retrying after 3 stalled runs/i),
      ).toBeInTheDocument();
    });

    expect(screen.getAllByText("Paused").length).toBeGreaterThan(0);
    expect(
      screen.getByText(/stopped retrying after 3 stalled runs/i),
    ).toBeInTheDocument();
  });

  it("renders the issue identifier only once in breadcrumbs when no epic is attached", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({
      identifier: "TEST-3",
      epic_id: undefined,
      epic_name: undefined,
    });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: "implementation",
      attempt_number: 0,
      retry_state: "none",
      session_source: "none",
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByRole("navigation", { name: /breadcrumb/i })).toBeInTheDocument();
    });

    const breadcrumb = screen.getByRole("navigation", { name: /breadcrumb/i });
    expect(within(breadcrumb).getAllByText(issue.identifier)).toHaveLength(1);
    expect(within(breadcrumb).getByText(issue.project_name!)).toBeInTheDocument();
    expect(within(breadcrumb).queryByText("Observability")).not.toBeInTheDocument();
  });

  it("renders the shared progress-first activity feed for issue execution details", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({ state: "in_progress" });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: true,
      phase: "implementation",
      attempt_number: 2,
      retry_state: "active",
      session_source: "live",
      activity_groups: [
        {
          attempt: 2,
          phase: "implementation",
          status: "active",
          entries: [
            {
              id: "attempt-2-agent-1",
              kind: "agent",
              title: "Agent update",
              summary: "Planning the verification pass",
              expandable: false,
              phase: "commentary",
              tone: "default",
            },
            {
              id: "attempt-2-command-1",
              kind: "command",
              title: "Command completed",
              summary: "npm test",
              detail: "$ npm test\n\nall checks green\n\nexit code: 0",
              expandable: true,
              tone: "success",
              item_type: "commandExecution",
              status: "completed",
            },
          ],
        },
      ],
      debug_activity_groups: [],
      session: {
        issue_id: issue.id,
        issue_identifier: issue.identifier,
        session_id: "thread-live-turn-live",
        thread_id: "thread-live",
        turn_id: "turn-live",
        last_event: "turn.started",
        last_timestamp: "2026-03-10T12:00:00Z",
        last_message: "Planning the verification pass",
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 30,
        events_processed: 2,
        turns_started: 2,
        turns_completed: 1,
        terminal: false,
        history: [],
      },
      runtime_events: [
        {
          seq: 1,
          kind: "run_started",
          phase: "implementation",
          attempt: 2,
          ts: "2026-03-10T12:00:01Z",
          payload: {},
        },
      ],
      agent_commands: [],
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("Activity log")).toBeInTheDocument();
    });

    expect(screen.queryByText("Current activity")).not.toBeInTheDocument();
    const transcript = screen.getByTestId("activity-log");
    expect(
      within(transcript).getByText("Planning the verification pass"),
    ).toBeInTheDocument();
    expect(
      within(transcript).getByText("Command completed"),
    ).toBeInTheDocument();
    expect(within(transcript).getByText("npm test")).toBeInTheDocument();
    expect(screen.getByText("Debug signals").closest("details")).not.toHaveAttribute("open");
  });

  it("shows recurring schedule details and triggers run-now", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({
      issue_type: "recurring",
      cron: "*/15 * * * *",
      enabled: true,
      next_run_at: "2026-03-10T12:30:00Z",
    });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: "implementation",
      attempt_number: 1,
      retry_state: "none",
      session_source: "none",
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
    });
    vi.mocked(api.runIssueNow).mockResolvedValue({ status: "queued_now" });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("Recurring")).toBeInTheDocument();
    });

    expect(screen.getByText("Schedule")).toBeInTheDocument();
    expect(screen.getByText("*/15 * * * *")).toBeInTheDocument();
    expect(screen.getByText(formatDateTime("2026-03-10T12:30:00Z"))).toBeInTheDocument();

    fireEvent.click(screen.getByText("Run now"));

    await waitFor(() => {
      expect(api.runIssueNow).toHaveBeenCalledWith("ISS-1");
    });
  });

  it("renders issue images and supports direct upload and removal", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({
      images: [makeIssueImage()],
    });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: "implementation",
      attempt_number: 0,
      retry_state: "none",
      session_source: "none",
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
    });
    vi.mocked(api.uploadIssueImage).mockResolvedValue(makeIssueImage({ id: "img-2" }));
    vi.mocked(api.deleteIssueImage).mockResolvedValue({
      deleted: true,
      identifier: issue.identifier,
      image_id: "img-1",
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByTestId("issue-images-card")).toBeInTheDocument();
    });

    const imagesCard = screen.getByTestId("issue-images-card");
    expect(
      within(imagesCard).getByRole("button", { name: /attach images/i }),
    ).toBeInTheDocument();
    expect(
      within(imagesCard).queryByText(/drop files here/i),
    ).not.toBeInTheDocument();
    expect(
      within(imagesCard).getByRole("button", { name: /open runtime\.png/i }),
    ).toBeInTheDocument();

    const file = new File(["png"], "capture.png", { type: "image/png" });
    fireEvent.change(within(imagesCard).getByLabelText(/attach images/i, { selector: "input" }), {
      target: { files: [file] },
    });

    await waitFor(() => {
      expect(api.uploadIssueImage).toHaveBeenCalledWith(issue.identifier, file);
    });

    fireEvent.click(
      within(imagesCard).getByRole("button", { name: /open runtime\.png/i }),
    );

    await waitFor(() => {
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });
    expect(screen.getByText("runtime.png")).toBeInTheDocument();
    expect(screen.getByText("image/png")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /remove image/i }));
    expect(api.deleteIssueImage).not.toHaveBeenCalled();

    const confirmDialog = await screen.findByRole("dialog", {
      name: /delete runtime\.png\?/i,
    });
    fireEvent.click(
      within(confirmDialog).getByRole("button", { name: /delete image/i }),
    );

    await waitFor(() => {
      expect(api.deleteIssueImage).toHaveBeenCalledWith(issue.identifier, "img-1");
    });
  });

  it("confirms issue deletion before deleting the issue", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({ state: "backlog" });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: "implementation",
      attempt_number: 0,
      retry_state: "none",
      session_source: "none",
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
    });
    vi.mocked(api.deleteIssue).mockResolvedValue({ deleted: true });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("Issue actions")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /^delete$/i }));
    expect(api.deleteIssue).not.toHaveBeenCalled();

    const confirmDialog = await screen.findByRole("dialog", {
      name: /delete iss-1\?/i,
    });
    fireEvent.click(
      within(confirmDialog).getByRole("button", { name: /delete issue/i }),
    );

    await waitFor(() => {
      expect(api.deleteIssue).toHaveBeenCalledWith("ISS-1");
    });
  });

  it("keeps the composer in the rail and renders command history in the main column", async () => {
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
        activity_groups: [],
        debug_activity_groups: [],
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
        activity_groups: [],
        debug_activity_groups: [],
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

    const mainColumn = screen.getByTestId("issue-main-column");
    const controlRail = screen.getByTestId("issue-control-rail");

    expect(within(mainColumn).getByText("Execution triage")).toBeInTheDocument();
    expect(within(mainColumn).queryByText("Command history")).not.toBeInTheDocument();
    expect(within(controlRail).getByText("Issue actions")).toBeInTheDocument();
    expect(within(controlRail).getByText("Agent commands")).toBeInTheDocument();
    expect(screen.queryByText("Command history")).not.toBeInTheDocument();
    expect(within(controlRail).queryByText("Latest command")).not.toBeInTheDocument();
    expect(within(controlRail).queryByText("Follow-up command")).not.toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText(/tell the agent/i), {
      target: { value: "Merge the branch to master." },
    });
    expect(screen.queryByText("Send to agent")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /send to agent/i }));

    await waitFor(() => {
      expect(api.sendIssueCommand).toHaveBeenCalledWith(
        issue.identifier,
        "Merge the branch to master.",
      );
    });

    await waitFor(() => {
      expect(
        within(controlRail).getByText("Waiting for unblock"),
      ).toBeInTheDocument();
    });
    expect(within(mainColumn).queryByText("Merge the branch to master.")).not.toBeInTheDocument();
    expect(
      within(controlRail).getByText("Merge the branch to master."),
    ).toBeInTheDocument();
  });

  it("keeps the state selector under issue actions and removes the extra control cards", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({ state: "backlog" });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: "implementation",
      attempt_number: 0,
      retry_state: "none",
      session_source: "none",
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
    });
    vi.mocked(api.setIssueState).mockResolvedValue({
      ...issue,
      state: "ready",
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("Issue actions")).toBeInTheDocument();
    });

    const controlRail = screen.getByTestId("issue-control-rail");
    expect(within(controlRail).queryByText("Live adjustments")).not.toBeInTheDocument();
    expect(within(controlRail).queryByText("Blockers")).not.toBeInTheDocument();

    await selectOption(/issue state/i, /ready/i);

    await waitFor(() => {
      expect(api.setIssueState).toHaveBeenCalledWith(
        issue.identifier,
        "ready",
      );
    });
  });

  it("renders edit, retry, and delete in a single icon action row", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({ state: "backlog" });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: "implementation",
      attempt_number: 0,
      retry_state: "none",
      session_source: "none",
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("Issue actions")).toBeInTheDocument();
    });

    const actionRow = screen.getByTestId("issue-actions-row");
    const editButton = within(actionRow).getByRole("button", {
      name: /edit issue/i,
    });
    const retryButton = within(actionRow).getByRole("button", {
      name: /retry now/i,
    });
    const deleteButton = within(actionRow).getByRole("button", {
      name: /delete/i,
    });

    expect(actionRow).toHaveClass("grid-cols-3");
    expect(within(actionRow).getAllByRole("button")).toHaveLength(3);
    expect(editButton.querySelector("svg")).not.toBeNull();
    expect(retryButton.querySelector("svg")).not.toBeNull();
    expect(deleteButton.querySelector("svg")).not.toBeNull();
    expect(within(actionRow).queryByText("Edit issue")).not.toBeInTheDocument();
    expect(within(actionRow).queryByText("Retry now")).not.toBeInTheDocument();
    expect(within(actionRow).queryByText("Delete")).not.toBeInTheDocument();
  });
});
