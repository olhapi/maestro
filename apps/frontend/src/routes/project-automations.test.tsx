import type { ReactNode } from "react";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";

import { ProjectAutomationsPage } from "@/routes/project-automations";
import { appRoutes } from "@/lib/routes";
import { makeBootstrapResponse, makeIssueSummary } from "@/test/fixtures";
import { renderWithQueryClient, selectOption } from "@/test/test-utils";

const navigate = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  Link: ({
    children,
    params,
    to,
    ...props
  }: {
    children: ReactNode;
    params?: { identifier?: string; projectId?: string; epicId?: string };
    to?: string;
  }) => (
    <a
      href={
        typeof to === "string"
          ? to
          : params?.identifier
            ? `/issues/${params.identifier}`
            : params?.projectId
              ? `/projects/${params.projectId}`
              : params?.epicId
                ? `/epics/${params.epicId}`
                : "#"
      }
      {...props}
    >
      {children}
    </a>
  ),
  useNavigate: () => navigate,
  useParams: () => ({ projectId: "project-1" }),
}));

vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("@/lib/api", () => ({
  api: {
    getProject: vi.fn(),
    listIssues: vi.fn(),
    runIssueNow: vi.fn(),
    updateIssue: vi.fn(),
    deleteIssue: vi.fn(),
    createIssue: vi.fn(),
  },
}));

const { api } = await import("@/lib/api");

function makeProjectDetailResponse() {
  const bootstrap = makeBootstrapResponse();
  const projectSummary = {
    ...bootstrap.projects[0],
    total_count: 1,
    active_count: 1,
    terminal_count: 0,
    total_tokens_spent: 33,
    counts: {
      backlog: 0,
      ready: 1,
      in_progress: 0,
      in_review: 0,
      done: 0,
      cancelled: 0,
    },
    state_buckets: [
      {
        state: "ready",
        count: 1,
        is_active: true,
      },
    ],
  };
  const projectIssues = [
    makeIssueSummary({
      identifier: "ISS-1",
      title: "Existing work",
      labels: ["api", "automation"],
      issue_type: "standard",
      project_id: "project-1",
      project_name: "Platform",
    }),
  ];

  return {
    project: projectSummary,
    epics: bootstrap.epics,
    issues: {
      items: projectIssues,
      total: projectIssues.length,
      limit: 200,
      offset: 0,
    },
  };
}

function makeRecurringAutomation() {
  return makeIssueSummary({
    identifier: "ISS-9",
    title: "Nightly sync",
    description: "Sync nightly exports.",
    issue_type: "recurring",
    cron: "0 0 * * *",
    enabled: true,
    next_run_at: "2026-03-10T00:00:00Z",
    priority: 3,
    labels: ["automation"],
    permission_profile: "full-access",
    agent_name: "planner",
    agent_prompt: "Review the nightly sync.",
    project_id: "project-1",
    project_name: "Platform",
    epic_id: "epic-1",
    epic_name: "Execution",
  });
}

describe("ProjectAutomationsPage", () => {
  beforeEach(() => {
    navigate.mockReset();
    vi.clearAllMocks();
  });

  it("renders the automation detail view and wires the row actions", async () => {
    vi.mocked(api.getProject).mockResolvedValue(makeProjectDetailResponse())
    vi.mocked(api.listIssues).mockResolvedValue({
      items: [makeRecurringAutomation()],
      total: 1,
      limit: 200,
      offset: 0,
    })
    vi.mocked(api.runIssueNow).mockResolvedValue({ ok: true })
    vi.mocked(api.updateIssue).mockResolvedValue(makeRecurringAutomation())

    renderWithQueryClient(<ProjectAutomationsPage />)

    await waitFor(() => {
      expect(screen.getByText("Automation list")).toBeInTheDocument()
    })

    expect(screen.getByRole("heading", { name: /platform/i })).toBeInTheDocument()
    expect(screen.getByRole("heading", { name: "Nightly sync" })).toBeInTheDocument()
    expect(screen.getAllByText("Scheduled").length).toBeGreaterThan(0)
    expect(screen.getAllByText("0 0 * * *").length).toBeGreaterThan(0)
    expect(screen.getAllByText("Priority 3").length).toBeGreaterThan(0)
    expect(screen.getByText("full-access access")).toBeInTheDocument()
    expect(screen.getByText("planner")).toBeInTheDocument()
    expect(screen.getAllByText("Execution").length).toBeGreaterThan(0)

    fireEvent.click(screen.getByRole("button", { name: /run now/i }))
    await waitFor(() => {
      expect(api.runIssueNow).toHaveBeenCalledWith("ISS-9")
    })

    fireEvent.click(screen.getByRole("button", { name: /pause/i }))
    await waitFor(() => {
      expect(api.updateIssue).toHaveBeenCalledWith("ISS-9", { enabled: false })
    })

    fireEvent.click(screen.getByRole("button", { name: /open legacy detail/i }))
    expect(navigate).toHaveBeenCalledWith({
      to: appRoutes.issueDetail,
      params: { identifier: "ISS-9" },
    })

    fireEvent.click(screen.getByRole("button", { name: /back to project/i }))
    expect(navigate).toHaveBeenCalledWith({
      to: appRoutes.projectDetail,
      params: { projectId: "project-1" },
    })
  })

  it("opens the create dialog and serializes a recurring automation payload", async () => {
    vi.mocked(api.getProject).mockResolvedValue(makeProjectDetailResponse())
    vi.mocked(api.listIssues).mockResolvedValue({
      items: [makeRecurringAutomation()],
      total: 1,
      limit: 200,
      offset: 0,
    })
    vi.mocked(api.createIssue).mockResolvedValue(makeRecurringAutomation())

    renderWithQueryClient(<ProjectAutomationsPage />)

    await waitFor(() => {
      expect(screen.getByText("Automation list")).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole("button", { name: /new automation/i }))

    fireEvent.change(screen.getByLabelText(/automation name/i), {
      target: { value: "Nightly sync" },
    })
    fireEvent.change(screen.getByRole("textbox", { name: /automation schedule/i }), {
      target: { value: "0 0 * * *" },
    })
    fireEvent.change(screen.getByLabelText(/assigned agent/i), {
      target: { value: "planner" },
    })
    fireEvent.change(screen.getByLabelText(/agent prompt/i), {
      target: { value: "Review the nightly sync." },
    })
    fireEvent.change(screen.getByLabelText(/template/i), {
      target: { value: "Sync nightly exports." },
    })

    await selectOption(/permission profile/i, /^full access$/i)

    fireEvent.click(screen.getByRole("button", { name: /create automation/i }))

    await waitFor(() => {
      expect(api.createIssue).toHaveBeenCalledWith(
        expect.objectContaining({
          project_id: "project-1",
          epic_id: "",
          title: "Nightly sync",
          description: "Sync nightly exports.",
          issue_type: "recurring",
          cron: "0 0 * * *",
          enabled: true,
          priority: 0,
          permission_profile: "full-access",
          agent_name: "planner",
          agent_prompt: "Review the nightly sync.",
        }),
      )
    })
  })

  it("opens the edit dialog and submits automation updates", async () => {
    vi.mocked(api.getProject).mockResolvedValue(makeProjectDetailResponse())
    vi.mocked(api.listIssues).mockResolvedValue({
      items: [makeRecurringAutomation()],
      total: 1,
      limit: 200,
      offset: 0,
    })
    vi.mocked(api.updateIssue).mockResolvedValue(makeRecurringAutomation())

    renderWithQueryClient(<ProjectAutomationsPage />)

    await waitFor(() => {
      expect(screen.getByText("Automation list")).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole("button", { name: /edit automation/i }))
    fireEvent.change(screen.getByLabelText(/automation name/i), {
      target: { value: "Nightly sync refreshed" },
    })
    fireEvent.change(screen.getByRole("textbox", { name: /automation schedule/i }), {
      target: { value: "*/30 * * * *" },
    })
    fireEvent.click(screen.getByRole("button", { name: /update automation/i }))

    await waitFor(() => {
      expect(api.updateIssue).toHaveBeenCalledWith(
        "ISS-9",
        expect.objectContaining({
          project_id: "project-1",
          title: "Nightly sync refreshed",
          issue_type: "recurring",
          cron: "*/30 * * * *",
          permission_profile: "full-access",
        }),
      )
    })
  })
})
