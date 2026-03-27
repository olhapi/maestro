import type { ReactNode } from "react";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { vi } from "vitest";

import { IssueDetailPage } from "@/routes/issue-detail";
import {
  makeBootstrapResponse,
  makeIssueComment,
  makeIssueDetail,
  makeIssueAsset,
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
    listIssueComments: vi.fn(),
    getIssueExecution: vi.fn(),
    retryIssue: vi.fn(),
    runIssueNow: vi.fn(),
    deleteIssue: vi.fn(),
    updateIssue: vi.fn(),
    setIssueState: vi.fn(),
    setIssuePermissionProfile: vi.fn(),
    approveIssuePlan: vi.fn(),
    respondToInterrupt: vi.fn(),
    setIssueBlockers: vi.fn(),
    requestIssuePlanRevision: vi.fn(),
    sendIssueCommand: vi.fn(),
    steerIssueCommand: vi.fn(),
    updateIssueCommand: vi.fn(),
    deleteIssueCommand: vi.fn(),
    uploadIssueAsset: vi.fn(),
    deleteIssueAsset: vi.fn(),
    createIssueComment: vi.fn(),
    updateIssueComment: vi.fn(),
    deleteIssueComment: vi.fn(),
  },
}));

const { api } = await import("@/lib/api");

function ensureObjectURLMocks() {
  if (!vi.isMockFunction(URL.createObjectURL)) {
    const createObjectURL = vi.fn(() => "blob:queued-image");
    try {
      vi.spyOn(URL, "createObjectURL").mockImplementation(createObjectURL);
    } catch {
      Object.defineProperty(URL, "createObjectURL", {
        configurable: true,
        value: createObjectURL,
      });
    }
  }

  if (!vi.isMockFunction(URL.revokeObjectURL)) {
    const revokeObjectURL = vi.fn();
    try {
      vi.spyOn(URL, "revokeObjectURL").mockImplementation(revokeObjectURL);
    } catch {
      Object.defineProperty(URL, "revokeObjectURL", {
        configurable: true,
        value: revokeObjectURL,
      });
    }
  }
}

describe("IssueDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    ensureObjectURLMocks();
    vi.mocked(api.listIssueComments).mockResolvedValue({ items: [] });
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

  it("renders comments when attachments and replies are omitted from the payload", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail();
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
    vi.mocked(api.listIssueComments).mockResolvedValue({
      items: [
        {
          ...makeIssueComment({
            body: "Missing arrays should not crash the page",
          }),
          attachments: undefined,
          replies: undefined,
        } as unknown as ReturnType<typeof makeIssueComment>,
      ],
    });

    renderWithQueryClient(<IssueDetailPage />);

    expect(await screen.findByText("Missing arrays should not crash the page")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /reply/i })).toBeInTheDocument();
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

  it("updates the issue permission profile from the inline selector", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail();
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
    vi.mocked(api.setIssuePermissionProfile).mockResolvedValue({
      ...issue,
      permission_profile: "full-access",
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByRole("combobox", { name: /agent permissions/i })).toBeInTheDocument();
    });

    expect(screen.getByText(/default inherits the project permission profile/i)).toBeInTheDocument();

    await selectOption(/agent permissions/i, /^full access$/i);

    await waitFor(() => {
      expect(api.setIssuePermissionProfile).toHaveBeenCalledWith("ISS-1", "full-access");
    });
  });

  it("shows inherited plan-first help text and can approve the extracted plan", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({
      project_permission_profile: "plan-then-full-access",
    });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: "implementation",
      attempt_number: 2,
      retry_state: "none",
      session_source: "persisted",
      session: {
        issue_id: issue.id,
        issue_identifier: issue.identifier,
        session_id: "thread-plan-turn-plan",
        thread_id: "thread-plan",
        turn_id: "turn-plan",
        last_event: "turn.completed",
        last_timestamp: "2026-03-18T12:00:00Z",
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 0,
        events_processed: 1,
        turns_started: 1,
        turns_completed: 1,
        terminal: true,
      },
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
      plan_approval: {
        markdown: "Review findings and then continue.",
        requested_at: "2026-03-18T12:00:00Z",
        attempt: 2,
      },
    });
    vi.mocked(api.approveIssuePlan).mockResolvedValue({
      ok: true,
      issue: { ...issue, permission_profile: "full-access" },
      dispatch: { status: "queued_now", issue: issue.identifier },
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText(/project's plan-first profile/i)).toBeInTheDocument();
    });

    expect(screen.getByText("Plan ready for approval")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /approve plan/i }));

    await waitFor(() => {
      expect(api.approveIssuePlan).toHaveBeenCalledWith("ISS-1");
    });
  });

  it("passes approval notes through the fallback approve-plan route when the interrupt is missing", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({
      project_permission_profile: "plan-then-full-access",
    });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: "implementation",
      attempt_number: 2,
      retry_state: "none",
      session_source: "persisted",
      session: {
        issue_id: issue.id,
        issue_identifier: issue.identifier,
        session_id: "thread-plan-turn-plan",
        thread_id: "thread-plan",
        turn_id: "turn-plan",
        last_event: "turn.completed",
        last_timestamp: "2026-03-18T12:00:00Z",
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 0,
        events_processed: 1,
        turns_started: 1,
        turns_completed: 1,
        terminal: true,
      },
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
      plan_approval: {
        markdown: "Review findings and then continue.",
        requested_at: "2026-03-18T12:00:00Z",
        attempt: 2,
      },
    });
    vi.mocked(api.approveIssuePlan).mockResolvedValue({
      ok: true,
      issue: { ...issue, permission_profile: "full-access" },
      dispatch: { status: "queued_now", issue: issue.identifier },
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText(/project's plan-first profile/i)).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /add steering note/i }));
    fireEvent.change(screen.getByPlaceholderText(/explain what should change in the plan/i), {
      target: { value: "Prefer a smaller rollout and keep the rollback step explicit." },
    });
    fireEvent.click(screen.getByRole("button", { name: /approve plan/i }));

    await waitFor(() => {
      expect(api.approveIssuePlan).toHaveBeenCalledWith(
        "ISS-1",
        "Prefer a smaller rollout and keep the rollback step explicit.",
      );
    });
  });

  it("uses the dedicated revision route even when the plan approval interrupt is present", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({
      project_permission_profile: "plan-then-full-access",
    });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: "implementation",
      attempt_number: 2,
      retry_state: "paused",
      pause_reason: "plan_approval_pending",
      current_error: "plan_approval_pending",
      session_source: "persisted",
      session: {
        issue_id: issue.id,
        issue_identifier: issue.identifier,
        session_id: "thread-plan-turn-plan",
        thread_id: "thread-plan",
        turn_id: "turn-plan",
        last_event: "turn.completed",
        last_timestamp: "2026-03-18T12:00:00Z",
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 0,
        events_processed: 1,
        turns_started: 1,
        turns_completed: 1,
        terminal: true,
      },
      activity_groups: [],
      debug_activity_groups: [],
      runtime_events: [],
      agent_commands: [],
      pending_interrupt: {
        id: "plan-approval-interrupt",
        kind: "approval",
        issue_identifier: issue.identifier,
        issue_title: issue.title,
        phase: "implementation",
        attempt: 2,
        requested_at: "2026-03-18T12:00:00Z",
        collaboration_mode: "plan",
        approval: {
          markdown: "Review findings and then continue.",
          reason: "Review the proposed plan before execution.",
          decisions: [
            {
              value: "approved",
              label: "Approve plan",
            },
          ],
        },
      },
      plan_approval: {
        markdown: "Review findings and then continue.",
        requested_at: "2026-03-18T12:00:00Z",
        attempt: 2,
      },
    });
    vi.mocked(api.approveIssuePlan).mockResolvedValue({
      ok: true,
      issue: { ...issue, permission_profile: "full-access" },
      dispatch: { status: "queued_now", issue: issue.identifier },
    });
    vi.mocked(api.requestIssuePlanRevision).mockResolvedValue({
      ok: true,
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByRole("button", { name: /restart plan thread/i })).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /add steering note/i }));
    const noteInput = screen.getByPlaceholderText(/explain what should change in the plan/i);
    fireEvent.change(noteInput, { target: { value: "Keep the plan, then approve it." } });
    fireEvent.click(screen.getByRole("button", { name: /approve plan/i }));

    await waitFor(() => {
      expect(api.approveIssuePlan).toHaveBeenCalledWith(
        "ISS-1",
        "Keep the plan, then approve it.",
      );
    });

    expect(api.respondToInterrupt).not.toHaveBeenCalled();

    fireEvent.change(noteInput, { target: { value: "Call out the missing tests before approval." } });
    fireEvent.click(screen.getByRole("button", { name: /request changes/i }));

    await waitFor(() => {
      expect(api.requestIssuePlanRevision).toHaveBeenCalledWith(
        "ISS-1",
        "Call out the missing tests before approval.",
      );
    });

    expect(api.sendIssueCommand).not.toHaveBeenCalled();
    expect(api.respondToInterrupt).not.toHaveBeenCalled();
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
    expect(within(transcript).getByRole("button", { name: /copy all/i })).toBeInTheDocument();
    expect(screen.getByText("Debug signals").closest("details")).not.toHaveAttribute("open");
  });

  it("shows assigned agent metadata in the issue overview cards", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({
      agent_name: "marketing",
      agent_prompt: "Review homepage messaging before implementation.",
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
      expect(screen.getByText("Assigned agent")).toBeInTheDocument();
    });

    expect(screen.getByText("marketing")).toBeInTheDocument();
    expect(screen.getByText("Review homepage messaging before implementation.")).toBeInTheDocument();
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

  it("renders issue assets and supports direct upload and removal", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({
      assets: [makeIssueAsset()],
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
    vi.mocked(api.uploadIssueAsset).mockResolvedValue(makeIssueAsset({ id: "ast-2" }));
    vi.mocked(api.deleteIssueAsset).mockResolvedValue({
      deleted: true,
      identifier: issue.identifier,
      asset_id: "img-1",
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByTestId("issue-assets-card")).toBeInTheDocument();
    });

    const assetsCard = screen.getByTestId("issue-assets-card");
    expect(
      within(assetsCard).getByRole("button", { name: /attach files/i }),
    ).toBeInTheDocument();
    expect(
      within(assetsCard).getByRole("button", { name: /open runtime\.png/i }),
    ).toBeInTheDocument();

    const file = new File(["png"], "capture.png", { type: "image/png" });
    fireEvent.change(within(assetsCard).getByLabelText(/attach assets/i, { selector: "input" }), {
      target: { files: [file] },
    });

    await waitFor(() => {
      expect(api.uploadIssueAsset).toHaveBeenCalledWith(issue.identifier, file);
    });

    fireEvent.click(
      within(assetsCard).getByRole("button", { name: /open runtime\.png/i }),
    );

    await waitFor(() => {
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });
    expect(screen.getByText("runtime.png")).toBeInTheDocument();
    expect(screen.getByText("image/png")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /remove asset/i }));
    expect(api.deleteIssueAsset).not.toHaveBeenCalled();

    const confirmDialog = await screen.findByRole("dialog", {
      name: /delete runtime\.png\?/i,
    });
    fireEvent.click(
      within(confirmDialog).getByRole("button", { name: /delete asset/i }),
    );

    await waitFor(() => {
      expect(api.deleteIssueAsset).toHaveBeenCalledWith(issue.identifier, "img-1");
    });
  });

  it("pastes files into the comment composer without blocking text-only paste", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail();
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
    vi.mocked(api.createIssueComment).mockResolvedValue(makeIssueComment());

    renderWithQueryClient(<IssueDetailPage />);

    const composer = await screen.findByPlaceholderText(/add context, ask for a change/i);
    expect(
      within(screen.getByTestId("issue-comments-card")).getByRole("button", { name: /attach files/i }),
    ).toBeInTheDocument();
    const pastedFile = new File(["hello"], "paste.txt", { type: "text/plain" });
    fireEvent.paste(composer, {
      clipboardData: {
        items: [{ kind: "file", getAsFile: () => pastedFile }],
        files: [pastedFile],
      },
    });

    expect(await screen.findByRole("button", { name: /remove paste\.txt/i })).toBeInTheDocument();

    fireEvent.paste(composer, {
      clipboardData: { items: [], files: [] },
    });
    expect(screen.queryByRole("button", { name: /remove undefined/i })).not.toBeInTheDocument();
  });

  it("shows queued image previews in the comment composer and cleans up blob URLs", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail();
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
    vi.mocked(api.createIssueComment).mockResolvedValue(makeIssueComment());

    renderWithQueryClient(<IssueDetailPage />);

    const composer = await screen.findByPlaceholderText(/add context, ask for a change/i);
    const queuedImage = new File(["image-bytes"], "queued.png", { type: "image/png" });
    fireEvent.paste(composer, {
      clipboardData: {
        items: [{ kind: "file", getAsFile: () => queuedImage }],
        files: [queuedImage],
      },
    });

    const removeButton = await screen.findByRole("button", { name: /remove queued\.png/i });
    expect(within(removeButton).getByRole("img", { name: /queued\.png/i })).toBeInTheDocument();
    expect(URL.createObjectURL).toHaveBeenCalledWith(queuedImage);

    fireEvent.change(composer, { target: { value: "Queued image comment" } });
    fireEvent.click(screen.getByRole("button", { name: /post comment/i }));

    await waitFor(() => {
      expect(api.createIssueComment).toHaveBeenCalledWith(issue.identifier, {
        body: "Queued image comment",
        parent_comment_id: undefined,
        files: [queuedImage],
      });
    });
    await waitFor(() => {
      expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:queued-image");
    });
  });

  it("renders comments and supports create, reply, update, and delete flows", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail();
    const reply = makeIssueComment({
      id: "cmt-2",
      parent_comment_id: "cmt-1",
      body: "I checked it in the latest run.",
      author: { type: "source", name: "CLI" },
    });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.listIssueComments).mockResolvedValue({
      items: [makeIssueComment({ replies: [reply] })],
    });
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
    vi.mocked(api.createIssueComment).mockResolvedValue(makeIssueComment({ id: "cmt-3" }));
    vi.mocked(api.updateIssueComment).mockResolvedValue(makeIssueComment({ body: "Updated comment" }));
    vi.mocked(api.deleteIssueComment).mockResolvedValue({
      deleted: true,
      identifier: issue.identifier,
      comment_id: "cmt-1",
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByTestId("issue-comments-card")).toBeInTheDocument();
    });

    expect(screen.getByText("Please verify the retry threshold before merge.")).toBeInTheDocument();
    expect(screen.getByText("I checked it in the latest run.")).toBeInTheDocument();

    const commentBody = screen.getByPlaceholderText(/add context, ask for a change/i);
    fireEvent.change(commentBody, { target: { value: "New comment body" } });
    fireEvent.click(screen.getByRole("button", { name: /post comment/i }));
    await waitFor(() => {
      expect(api.createIssueComment).toHaveBeenCalledWith(issue.identifier, {
        body: "New comment body",
        parent_comment_id: undefined,
        files: [],
      });
    });

    fireEvent.click(screen.getByRole("button", { name: /^reply$/i }));
    fireEvent.change(screen.getByPlaceholderText(/write a reply/i), { target: { value: "Reply body" } });
    fireEvent.click(screen.getAllByRole("button", { name: /^reply$/i })[1]);
    await waitFor(() => {
      expect(api.createIssueComment).toHaveBeenCalledWith(issue.identifier, {
        body: "Reply body",
        parent_comment_id: "cmt-1",
        files: [],
      });
    });

    fireEvent.click(screen.getAllByRole("button", { name: /edit/i })[0]);
    fireEvent.change(screen.getByDisplayValue("Please verify the retry threshold before merge."), {
      target: { value: "Updated comment" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => {
      expect(api.updateIssueComment).toHaveBeenCalledWith(issue.identifier, "cmt-1", {
        body: "Updated comment",
        files: [],
        remove_attachment_ids: [],
      });
    });

    fireEvent.click(screen.getAllByRole("button", { name: /^delete$/i })[0]);
    await waitFor(() => {
      expect(api.deleteIssueComment).toHaveBeenCalledWith(issue.identifier, "cmt-1");
    });
  });

  it("renders comment actions as icon buttons with tooltips", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail();
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.listIssueComments).mockResolvedValue({
      items: [makeIssueComment()],
    });
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
      expect(screen.getByTestId("issue-comments-card")).toBeInTheDocument();
    });

    const commentsCard = screen.getByTestId("issue-comments-card");
    const replyButton = within(commentsCard).getByRole("button", { name: /^reply$/i });
    const editButton = within(commentsCard).getByRole("button", { name: /^edit$/i });
    const deleteButton = within(commentsCard).getByRole("button", { name: /^delete$/i });

    expect(replyButton).toHaveTextContent("");
    expect(editButton).toHaveTextContent("");
    expect(deleteButton).toHaveTextContent("");
    expect(replyButton.querySelector("svg")).not.toBeNull();
    expect(editButton.querySelector("svg")).not.toBeNull();
    expect(deleteButton.querySelector("svg")).not.toBeNull();

    fireEvent.focus(editButton);

    expect(await screen.findByRole("tooltip")).toHaveTextContent("Edit");
  });

  it("disables the reply composer while a reply is being created", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail();
    let resolveCreate!: (value: ReturnType<typeof makeIssueComment>) => void;
    const createPromise = new Promise<ReturnType<typeof makeIssueComment>>((resolve) => {
      resolveCreate = resolve;
    });

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.listIssueComments).mockResolvedValue({
      items: [makeIssueComment()],
    });
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
    vi.mocked(api.createIssueComment).mockImplementation(() => createPromise);

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByTestId("issue-comments-card")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /^reply$/i }));
    fireEvent.change(screen.getByPlaceholderText(/write a reply/i), { target: { value: "Reply body" } });
    fireEvent.click(screen.getAllByRole("button", { name: /^reply$/i })[1]);

    await waitFor(() => {
      expect(screen.getByRole("button", { name: /replying/i })).toBeDisabled();
    });

    resolveCreate(makeIssueComment({ id: "cmt-2", parent_comment_id: "cmt-1", body: "Reply body" }));

    await waitFor(() => {
      expect(api.createIssueComment).toHaveBeenCalledWith(issue.identifier, {
        body: "Reply body",
        parent_comment_id: "cmt-1",
        files: [],
      });
    });
  });

  it("clears pending attachment removals when edit is cancelled", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail();
    const attachment = {
      id: "att-1",
      comment_id: "cmt-1",
      filename: "note.png",
      content_type: "image/png",
      byte_size: 12,
      created_at: "2026-03-09T11:40:00Z",
      updated_at: "2026-03-09T11:40:00Z",
    };

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.listIssueComments).mockResolvedValue({
      items: [makeIssueComment({ attachments: [attachment] })],
    });
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
    vi.mocked(api.updateIssueComment).mockResolvedValue(makeIssueComment({ attachments: [attachment] }));

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByTestId("issue-comments-card")).toBeInTheDocument();
    });

    const commentsCard = screen.getByTestId("issue-comments-card");
    expect(within(commentsCard).getByRole("link", { name: /open note\.png/i })).toBeInTheDocument();
    expect(within(commentsCard).getByRole("img", { name: /note\.png/i })).toBeInTheDocument();

    fireEvent.click(within(commentsCard).getByRole("button", { name: /^edit$/i }));
    fireEvent.click(within(commentsCard).getByRole("button", { name: /remove note\.png/i }));
    fireEvent.click(within(commentsCard).getByRole("button", { name: /^cancel$/i }));

    fireEvent.click(within(commentsCard).getByRole("button", { name: /^edit$/i }));
    fireEvent.click(within(commentsCard).getByRole("button", { name: /^save$/i }));

    await waitFor(() => {
      expect(api.updateIssueComment).toHaveBeenCalledWith(issue.identifier, "cmt-1", {
        body: "Please verify the retry threshold before merge.",
        files: [],
        remove_attachment_ids: [],
      });
    });
  });

  it("keeps the issue page usable when comments fail to load", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail();
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.listIssueComments).mockRejectedValue(new Error("comments unavailable"));
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
      expect(screen.getByText(issue.title)).toBeInTheDocument();
    });
    expect(screen.getByText("Comments are temporarily unavailable")).toBeInTheDocument();
    expect(screen.getByText("comments unavailable")).toBeInTheDocument();
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
      .mockResolvedValueOnce({
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
          {
            id: "cmd-2",
            issue_id: issue.id,
            command: "Already delivered.",
            status: "delivered",
            created_at: "2026-03-09T11:45:00Z",
            delivered_at: "2026-03-09T11:50:00Z",
            delivery_mode: "same_thread",
            delivery_thread_id: "thread-live",
            delivery_attempt: 1,
          },
          {
            id: "cmd-3",
            issue_id: issue.id,
            command:
              "Review the execution log and make sure this very-long-command-token-stays-inside-the-card without widening the rail.",
            status: "delivered",
            created_at: "2026-03-09T11:30:00Z",
            delivered_at: "2026-03-09T11:35:00Z",
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
    expect(
      within(screen.getByTestId("agent-command-cmd-1")).getByRole("button", { name: /edit command/i }),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("agent-command-cmd-1")).getByRole("button", { name: /delete command/i }),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("agent-command-cmd-2")).queryByRole("button", { name: /edit command/i }),
    ).not.toBeInTheDocument();
    expect(
      within(screen.getByTestId("agent-command-cmd-2")).queryByRole("button", { name: /delete command/i }),
    ).not.toBeInTheDocument();
    const wrappedCommand = within(screen.getByTestId("agent-command-cmd-3")).getByText(
      /very-long-command-token-stays-inside-the-card/,
    );
    expect(wrappedCommand).toHaveClass("whitespace-pre-wrap", "break-words");
    expect(wrappedCommand).not.toHaveClass("overflow-x-auto");
  });

  it("renders a steer button for mutable commands and refreshes the command when steered", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({ state: "done" });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);

    let resolveSteer!: (value: { ok: boolean; command: Record<string, unknown> }) => void;
    const steerPromise = new Promise<{ ok: boolean; command: Record<string, unknown> }>((resolve) => {
      resolveSteer = resolve;
    });
    vi.mocked(api.steerIssueCommand).mockReturnValue(steerPromise);
    vi.mocked(api.getIssueExecution)
      .mockResolvedValueOnce({
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
          {
            id: "cmd-2",
            issue_id: issue.id,
            command: "Already delivered.",
            status: "delivered",
            created_at: "2026-03-09T11:45:00Z",
            delivered_at: "2026-03-09T11:50:00Z",
            delivery_mode: "same_thread",
            delivery_thread_id: "thread-live",
            delivery_attempt: 1,
          },
        ],
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
            steered_at: "2026-03-09T12:12:00Z",
          },
          {
            id: "cmd-2",
            issue_id: issue.id,
            command: "Already delivered.",
            status: "delivered",
            created_at: "2026-03-09T11:45:00Z",
            delivered_at: "2026-03-09T11:50:00Z",
            delivery_mode: "same_thread",
            delivery_thread_id: "thread-live",
            delivery_attempt: 1,
          },
        ],
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
            steered_at: "2026-03-09T12:12:00Z",
          },
          {
            id: "cmd-2",
            issue_id: issue.id,
            command: "Already delivered.",
            status: "delivered",
            created_at: "2026-03-09T11:45:00Z",
            delivered_at: "2026-03-09T11:50:00Z",
            delivery_mode: "same_thread",
            delivery_thread_id: "thread-live",
            delivery_attempt: 1,
          },
        ],
      });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByTestId("agent-command-cmd-1")).toBeInTheDocument();
    });

    const mutableCommand = screen.getByTestId("agent-command-cmd-1");
    const steerButton = within(mutableCommand).getByRole("button", { name: /steer command/i });
    expect(within(screen.getByTestId("agent-command-cmd-2")).queryByRole("button", { name: /steer command/i })).toBeNull();

    fireEvent.click(steerButton);

    await waitFor(() => {
      expect(steerButton).toBeDisabled();
    });

    expect(api.steerIssueCommand).toHaveBeenCalledWith(issue.identifier, "cmd-1");

    resolveSteer({
      ok: true,
      command: {
        id: "cmd-1",
        issue_id: issue.id,
        command: "Merge the branch to master.",
        status: "waiting_for_unblock",
        created_at: "2026-03-09T12:10:00Z",
        steered_at: "2026-03-09T12:12:00Z",
      },
    });

    await waitFor(() => {
      expect(within(screen.getByTestId("agent-command-cmd-1")).getByText(/steered/i)).toBeInTheDocument();
    });
  });

  it("submits agent commands on Enter", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({ state: "done" });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.sendIssueCommand).mockResolvedValue({ ok: true });
    vi.mocked(api.getIssueExecution).mockResolvedValue({
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
      agent_commands: [],
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByPlaceholderText(/tell the agent/i)).toBeInTheDocument();
    });

    fireEvent.change(screen.getByPlaceholderText(/tell the agent/i), {
      target: { value: "Merge the branch to master." },
    });
    fireEvent.keyDown(screen.getByPlaceholderText(/tell the agent/i), {
      code: "Enter",
      key: "Enter",
    });

    await waitFor(() => {
      expect(api.sendIssueCommand).toHaveBeenCalledWith(
        issue.identifier,
        "Merge the branch to master.",
      );
    });
  });

  it("does not submit agent commands on Shift+Enter", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({ state: "done" });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.sendIssueCommand).mockResolvedValue({ ok: true });
    vi.mocked(api.getIssueExecution).mockResolvedValue({
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
      agent_commands: [],
    });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByPlaceholderText(/tell the agent/i)).toBeInTheDocument();
    });

    fireEvent.change(screen.getByPlaceholderText(/tell the agent/i), {
      target: { value: "Add a line break" },
    });
    fireEvent.keyDown(screen.getByPlaceholderText(/tell the agent/i), {
      code: "Enter",
      key: "Enter",
      shiftKey: true,
    });

    expect(api.sendIssueCommand).not.toHaveBeenCalled();
  });

  it("allows editing and deleting queued agent commands inline", async () => {
    const bootstrap = makeBootstrapResponse();
    const issue = makeIssueDetail({ state: "done" });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getIssue).mockResolvedValue(issue);
    vi.mocked(api.sendIssueCommand).mockResolvedValue({ ok: true });
    vi.mocked(api.updateIssueCommand).mockResolvedValue({
      ok: true,
      command: {
        id: "cmd-1",
        issue_id: issue.id,
        command: "Merge the branch after fixing the tests.",
        status: "waiting_for_unblock",
        created_at: "2026-03-09T12:10:00Z",
      },
    });
    vi.mocked(api.deleteIssueCommand).mockResolvedValue({
      ok: true,
      deleted: true,
      command_id: "cmd-1",
    });
    vi.mocked(api.getIssueExecution)
      .mockResolvedValueOnce({
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
      })
      .mockResolvedValueOnce({
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
            command: "Merge the branch after fixing the tests.",
            status: "waiting_for_unblock",
            created_at: "2026-03-09T12:10:00Z",
          },
        ],
      })
      .mockResolvedValueOnce({
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
        agent_commands: [],
      });

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByTestId("agent-command-cmd-1")).toBeInTheDocument();
    });

    fireEvent.click(
      within(screen.getByTestId("agent-command-cmd-1")).getByRole("button", {
        name: /edit command/i,
      }),
    );

    const editor = within(screen.getByTestId("agent-command-cmd-1")).getByPlaceholderText(
      /update the command/i,
    );
    fireEvent.change(editor, {
      target: { value: "Merge the branch after fixing the tests." },
    });
    fireEvent.click(
      within(screen.getByTestId("agent-command-cmd-1")).getByRole("button", {
        name: /^save$/i,
      }),
    );

    await waitFor(() => {
      expect(api.updateIssueCommand).toHaveBeenCalledWith(
        issue.identifier,
        "cmd-1",
        "Merge the branch after fixing the tests.",
      );
    });
    await waitFor(() => {
      expect(
        within(screen.getByTestId("agent-command-cmd-1")).getByText(
          "Merge the branch after fixing the tests.",
        ),
      ).toBeInTheDocument();
    });

    fireEvent.click(
      within(screen.getByTestId("agent-command-cmd-1")).getByRole("button", {
        name: /delete command/i,
      }),
    );

    const deleteDialog = await screen.findByRole("dialog", {
      name: /delete command\?/i,
    });
    fireEvent.click(
      within(deleteDialog).getByRole("button", {
        name: /delete command/i,
      }),
    );

    await waitFor(() => {
      expect(api.deleteIssueCommand).toHaveBeenCalledWith(issue.identifier, "cmd-1");
    });
    await waitFor(() => {
      expect(screen.queryByTestId("agent-command-cmd-1")).not.toBeInTheDocument();
    });
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

  it("shows an error toast when a state transition is rejected", async () => {
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
    vi.mocked(api.setIssueState).mockRejectedValue(new Error("blocked transition"));

    renderWithQueryClient(<IssueDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("Issue actions")).toBeInTheDocument();
    });

    await selectOption(/issue state/i, /ready/i);

    const { toast } = await import("sonner");
    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith("Unable to update state: blocked transition");
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
