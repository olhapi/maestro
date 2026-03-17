import type { ReactNode } from "react";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { vi } from "vitest";

import { ProjectDetailPage } from "@/routes/project-detail";
import { makeBootstrapResponse } from "@/test/fixtures";
import { renderWithQueryClient, selectOption } from "@/test/test-utils";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
  useNavigate: () => vi.fn(),
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
    bootstrap: vi.fn(),
    getProject: vi.fn(),
    setIssueState: vi.fn(),
    deleteIssue: vi.fn(),
    runProject: vi.fn(),
    stopProject: vi.fn(),
    setProjectPermissionProfile: vi.fn(),
    createIssue: vi.fn(),
    createEpic: vi.fn(),
  },
}));

const { api } = await import("@/lib/api");

describe("ProjectDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders a run-stop toggle for the project and triggers the actions", async () => {
    const bootstrap = makeBootstrapResponse({
      projects: [{ ...makeBootstrapResponse().projects[0], state: "stopped" }],
    });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getProject).mockResolvedValue({
      project: bootstrap.projects[0],
      epics: bootstrap.epics,
      issues: bootstrap.issues,
    });
    vi.mocked(api.runProject).mockResolvedValue({ status: "accepted" });
    vi.mocked(api.stopProject).mockResolvedValue({ status: "stopped" });

    renderWithQueryClient(<ProjectDetailPage />);

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: /run project/i }),
      ).toBeInTheDocument();
    });

    expect(screen.getByText("Tokens")).toBeInTheDocument();
    expect(
      screen.getByText("Lifetime tokens spent across all project issues."),
    ).toBeInTheDocument();
    expect(screen.getByText("Repo binding").parentElement?.parentElement).toHaveClass(
      "pt-[var(--panel-padding)]",
    );
    expect(
      screen.getByText("Epics driving this project").parentElement?.parentElement?.parentElement,
    ).toHaveClass("pt-[var(--panel-padding)]");
    expect(
      screen.getByText("What changed most recently").parentElement,
    ).toHaveClass("pt-[var(--panel-padding)]");
    expect(screen.queryByText(/^\d+\s+active$/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /run project/i }));
    await waitFor(() => {
      expect(api.runProject).toHaveBeenCalledWith("project-1");
    });

    const runningBootstrap = makeBootstrapResponse({
      projects: [{ ...bootstrap.projects[0], state: "running" }],
    });
    vi.mocked(api.bootstrap).mockResolvedValue(runningBootstrap);
    vi.mocked(api.getProject).mockResolvedValue({
      project: runningBootstrap.projects[0],
      epics: runningBootstrap.epics,
      issues: runningBootstrap.issues,
    });

    renderWithQueryClient(<ProjectDetailPage />);

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: /stop project/i }),
      ).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /stop project/i }));
    await waitFor(() => {
      expect(api.stopProject).toHaveBeenCalledWith("project-1");
    });
  });

  it("shows repo setup guidance when dispatch is not ready", async () => {
    const bootstrap = makeBootstrapResponse({
      projects: [
        {
          ...makeBootstrapResponse().projects[0],
          repo_path: "",
          workflow_path: "",
          orchestration_ready: false,
          dispatch_ready: false,
        },
      ],
    });

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getProject).mockResolvedValue({
      project: bootstrap.projects[0],
      epics: bootstrap.epics,
      issues: bootstrap.issues,
    });

    renderWithQueryClient(<ProjectDetailPage />);

    await waitFor(() => {
      expect(screen.getByText("Needs repo setup")).toBeInTheDocument();
    });

    const badge = screen.getByText("Needs repo setup");
    await act(async () => {
      fireEvent.pointerEnter(badge, { pointerType: "mouse" });
      fireEvent.mouseEnter(badge);
    });

    await waitFor(() => {
      expect(screen.getByText("Attach this project to a local repository")).toBeInTheDocument();
    });
    expect(screen.getByText("Open the project settings and set Repo path to the local checkout for this project.")).toBeInTheDocument();
    expect(screen.getByText("Leave Workflow path empty to use WORKFLOW.md at the repo root, or set an explicit workflow file.")).toBeInTheDocument();
  });

  it("updates the project permission profile from the inline selector", async () => {
    const bootstrap = makeBootstrapResponse();
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getProject).mockResolvedValue({
      project: bootstrap.projects[0],
      epics: bootstrap.epics,
      issues: bootstrap.issues,
    });
    vi.mocked(api.setProjectPermissionProfile).mockResolvedValue({
      ...bootstrap.projects[0],
      permission_profile: "full-access",
    });

    renderWithQueryClient(<ProjectDetailPage />);

    await waitFor(() => {
      expect(screen.getByRole("combobox", { name: /agent permissions/i })).toBeInTheDocument();
    });

    expect(screen.getByText(/default follows `WORKFLOW\.md`\. full access switches codex to unrestricted filesystem and network access/i)).toBeInTheDocument();

    await selectOption(/agent permissions/i, /full access/i);

    await waitFor(() => {
      expect(api.setProjectPermissionProfile).toHaveBeenCalledWith("project-1", "full-access");
    });
  });

  it("renders epic progress using terminal work counts", async () => {
    const bootstrap = makeBootstrapResponse({
      epics: [
        {
          ...makeBootstrapResponse().epics[0],
          counts: {
            backlog: 1,
            ready: 0,
            in_progress: 1,
            in_review: 0,
            done: 2,
            cancelled: 1,
          },
        },
      ],
    });

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.getProject).mockResolvedValue({
      project: bootstrap.projects[0],
      epics: bootstrap.epics,
      issues: bootstrap.issues,
    });

    renderWithQueryClient(<ProjectDetailPage />);

    const progress = await screen.findByRole("progressbar", {
      name: /observability completion/i,
    });

    expect(progress).toHaveAttribute("aria-valuetext", "3 of 5 issues closed");
    expect(progress).toHaveAttribute("aria-valuenow", "3");
    expect(progress).toHaveAttribute("aria-valuemax", "5");

    const epicCard = progress.parentElement as HTMLElement;
    expect(within(epicCard).getByText("Backlog 1")).toBeInTheDocument();
    expect(within(epicCard).getByText("Ready 0")).toBeInTheDocument();
    expect(within(epicCard).getByText("In progress 1")).toBeInTheDocument();
    expect(within(epicCard).getByText("Review 0")).toBeInTheDocument();
  });
});
