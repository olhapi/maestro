import type { ReactNode } from "react";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { vi } from "vitest";

import { ProjectDetailPage } from "@/routes/project-detail";
import { projectPermissionProfileButtonCopy } from "@/lib/project-permission-profiles";
import { makeBootstrapResponse, makeIssueSummary } from "@/test/fixtures";
import { renderWithQueryClient, selectOption } from "@/test/test-utils";
import type { PermissionProfile } from "@/lib/types";

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
    setProjectPermissionProfile: vi.fn(),
    setIssueState: vi.fn(),
    deleteIssue: vi.fn(),
    runProject: vi.fn(),
    stopProject: vi.fn(),
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

    const heading = screen.getByRole("heading", { name: /platform/i });
    const repoBinding = within(heading).getByLabelText(/repo path: \/repo/i);
    expect(repoBinding).toHaveTextContent("repo");
    expect(within(heading).queryByRole("img", { name: /provider/i })).not.toBeInTheDocument();
    expect(screen.queryByText("Repo binding")).not.toBeInTheDocument();
    expect(
      screen.queryByText(
        /uses the workspace baseline agent settings until a more specific access mode is chosen/i,
      ),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("Project setup")).not.toBeInTheDocument();
    expect(screen.queryByText("Workflow path")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Workflow defaults to <repo>/WORKFLOW.md."),
    ).not.toBeInTheDocument();

    expect(screen.getByText("Tokens")).toBeInTheDocument();
    expect(
      screen.getByText("Lifetime tokens spent across all project issues."),
    ).toBeInTheDocument();
    expect(screen.getByText("Epics driving this project")).toBeInTheDocument();
    expect(screen.getByText("What changed most recently")).toBeInTheDocument();
    expect(screen.queryByText(/^\d+\s+active$/i)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /collapse backlog status row/i }),
    ).not.toBeInTheDocument();

    const accessButton = screen.getByRole("button", {
      name: projectPermissionProfileButtonCopy(
        "Project access",
        "default",
      ).ariaLabel,
    });
    expect(
      screen.getByRole("button", { name: /run project/i }).nextElementSibling,
    ).toBe(accessButton);

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

  it("sorts project work and switches between list and board views", async () => {
    const bootstrap = makeBootstrapResponse()
    const projectIssues = {
      items: [
        makeIssueSummary({
          id: "issue-1",
          identifier: "ISS-1",
          title: "Triage release",
          priority: 5,
          state: "ready",
        }),
        makeIssueSummary({
          id: "issue-2",
          identifier: "ISS-2",
          title: "Investigate retries",
          priority: 1,
          state: "in_progress",
        }),
      ],
      total: 2,
      limit: 200,
      offset: 0,
    }

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)
    vi.mocked(api.getProject).mockResolvedValue({
      project: bootstrap.projects[0],
      epics: bootstrap.epics,
      issues: projectIssues,
    })

    renderWithQueryClient(<ProjectDetailPage />)

    await waitFor(() => {
      expect(screen.getByText("Project work")).toBeInTheDocument()
    })

    expect(screen.getByRole("combobox", { name: /sort issues/i })).toHaveTextContent("Highest priority")
    expect(screen.getByRole("radio", { name: "Board view" })).toHaveAttribute("data-state", "on")

    fireEvent.click(screen.getByRole("radio", { name: "List view" }))

    await waitFor(() => {
      expect(screen.getByRole("columnheader", { name: "Issue" })).toBeInTheDocument()
    })

    const table = screen.getByRole("table")
    expect(within(table).getAllByRole("button")[0]).toHaveTextContent("ISS-2")
    expect(within(table).getAllByRole("button")[1]).toHaveTextContent("ISS-1")
    expect(screen.queryByRole("columnheader", { name: "Project" })).not.toBeInTheDocument()

    await selectOption(/sort issues/i, /identifier a-z/i)

    await waitFor(() => {
      const sortedButtons = within(screen.getByRole("table")).getAllByRole("button")
      expect(sortedButtons[0]).toHaveTextContent("ISS-1")
      expect(sortedButtons[1]).toHaveTextContent("ISS-2")
    })
  });

  it("renders grouped status row collapse controls on mobile layouts", async () => {
    const initialInnerWidth = window.innerWidth;
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      writable: true,
      value: 390,
    });
    await act(async () => {
      window.dispatchEvent(new Event("resize"));
    });

    try {
      const bootstrap = makeBootstrapResponse();
      vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
      vi.mocked(api.getProject).mockResolvedValue({
        project: bootstrap.projects[0],
        epics: bootstrap.epics,
        issues: bootstrap.issues,
      });

      renderWithQueryClient(<ProjectDetailPage />);

      await waitFor(() => {
        expect(
          screen.getByRole("button", { name: /collapse backlog status row/i }),
        ).toBeInTheDocument();
      });

      expect(
        screen.queryByRole("radio", { name: "Board view" }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole("radio", { name: "List view" }),
      ).not.toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: /collapse backlog status row/i }),
      ).toHaveAttribute("aria-expanded", "true");
      expect(
        screen.getByRole("button", { name: /collapse ready status row/i }),
      ).toHaveAttribute("aria-expanded", "true");
    } finally {
      Object.defineProperty(window, "innerWidth", {
        configurable: true,
        writable: true,
        value: initialInnerWidth,
      });
      await act(async () => {
        window.dispatchEvent(new Event("resize"));
      });
    }
  });

  it("shows dispatch guidance when dispatch is not ready", async () => {
    const initialInnerWidth = window.innerWidth;
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      writable: true,
      value: 390,
    });
    await act(async () => {
      window.dispatchEvent(new Event("resize"));
    });

    try {
      const bootstrap = makeBootstrapResponse({
        projects: [
          {
            ...makeBootstrapResponse().projects[0],
            repo_path: "/repo/other",
            workflow_path: "/repo/other/WORKFLOW.md",
            orchestration_ready: false,
            dispatch_ready: false,
            dispatch_error:
              "Project repo is outside the current server scope (/repo/current)",
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

      const heading = await screen.findByRole("heading", { name: /platform/i });
      expect(within(heading).getByLabelText(/repo path: \/repo\/other/i)).toBeInTheDocument();

      expect(screen.getByText("Out of scope")).toBeInTheDocument();
      expect(screen.getByText("Bring the repo into this server scope")).toBeInTheDocument();
      expect(
        screen.getByText(
          "The current Maestro server can only dispatch work inside the repo scope it was started with.",
        ),
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          "Tip: move the repo under the current server scope or restart Maestro for that repo.",
        ),
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          "Move the project's repo path under /repo/current, or restart Maestro scoped to /repo/other.",
        ),
      ).toBeInTheDocument();
      expect(screen.getByText("Project repo: /repo/other")).toBeInTheDocument();
      expect(screen.getByText("Server scope: /repo/current")).toBeInTheDocument();
    } finally {
      Object.defineProperty(window, "innerWidth", {
        configurable: true,
        writable: true,
        value: initialInnerWidth,
      });
      await act(async () => {
        window.dispatchEvent(new Event("resize"));
      });
    }
  });

  it("renders epic state distribution using colored segments", async () => {
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
          state_buckets: [
            { state: "done", count: 2, is_terminal: true },
            { state: "backlog", count: 1 },
            { state: "cancelled", count: 1, is_terminal: true },
            { state: "in_progress", count: 1, is_active: true },
            { state: "ready", count: 0, is_active: true },
            { state: "in_review", count: 0, is_active: true },
          ],
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

    const distribution = await screen.findByRole("list", {
      name: /observability state distribution/i,
    });

    const segments = within(distribution).getAllByRole("listitem");
    expect(segments).toHaveLength(4);
    expect(
      segments.map((segment) => segment.getAttribute("data-state")),
    ).toEqual(["backlog", "in_progress", "done", "cancelled"]);

    const [backlogSegment, inProgressSegment, doneSegment, cancelledSegment] =
      segments;
    expect(backlogSegment).toHaveClass("bg-slate-400/90");
    expect(backlogSegment).toHaveStyle({ width: "20%" });
    expect(inProgressSegment).toHaveClass("bg-lime-400/90");
    expect(inProgressSegment).toHaveStyle({ width: "20%" });
    expect(doneSegment).toHaveClass("bg-emerald-400/90");
    expect(doneSegment).toHaveStyle({ width: "40%" });
    expect(cancelledSegment).toHaveClass("bg-rose-400/90");
    expect(cancelledSegment).toHaveStyle({ width: "20%" });

    expect(screen.queryByText("Backlog 1")).not.toBeInTheDocument();
    expect(screen.queryByText("Ready 0")).not.toBeInTheDocument();
    expect(screen.queryByText("In progress 1")).not.toBeInTheDocument();
    expect(screen.queryByText("Review 0")).not.toBeInTheDocument();
  });

  it("cycles the default permissions button through all three profiles", async () => {
    const bootstrap = makeBootstrapResponse();
    let permissionProfile: PermissionProfile = "default";

    vi.mocked(api.bootstrap).mockImplementation(async () => ({
      ...bootstrap,
      projects: [
        {
          ...bootstrap.projects[0],
          permission_profile: permissionProfile,
        },
      ],
    }));
    vi.mocked(api.getProject).mockImplementation(async () => ({
      project: {
        ...bootstrap.projects[0],
        permission_profile: permissionProfile,
      },
      epics: bootstrap.epics,
      issues: bootstrap.issues,
    }));
    vi.mocked(api.setProjectPermissionProfile).mockImplementation(
      async (_projectId, nextProfile) => {
        permissionProfile = nextProfile as PermissionProfile;
        return {
          ...bootstrap.projects[0],
          permission_profile: permissionProfile,
        };
      },
    );

    renderWithQueryClient(<ProjectDetailPage />);

    const button = () =>
      screen.getByRole("button", {
        name: projectPermissionProfileButtonCopy(
          "Project access",
          permissionProfile,
        ).ariaLabel,
      });

    await waitFor(() => {
      expect(button()).toBeInTheDocument();
    });
    expect(
      screen.queryByText(
        /uses the workspace baseline agent settings until a more specific access mode is chosen/i,
      ),
    ).not.toBeInTheDocument();
    expect(button()).toHaveTextContent("Default access");
    expect(button()).toHaveClass("w-fit");

    fireEvent.click(button());
    await waitFor(() => {
      expect(api.setProjectPermissionProfile).toHaveBeenCalledWith(
        "project-1",
        "full-access",
      );
    });
    await waitFor(() => {
      expect(button()).toHaveAccessibleName(
        projectPermissionProfileButtonCopy(
          "Project access",
          "full-access",
        ).ariaLabel,
      );
    });
    expect(button()).toHaveTextContent("Full access");

    fireEvent.click(button());
    await waitFor(() => {
      expect(api.setProjectPermissionProfile).toHaveBeenCalledWith(
        "project-1",
        "plan-then-full-access",
      );
    });
    await waitFor(() => {
      expect(button()).toHaveAccessibleName(
        projectPermissionProfileButtonCopy(
          "Project access",
          "plan-then-full-access",
        ).ariaLabel,
      );
    });
    expect(button()).toHaveTextContent("Plan, then full access");

    fireEvent.click(button());
    await waitFor(() => {
      expect(api.setProjectPermissionProfile).toHaveBeenCalledWith(
        "project-1",
        "default",
      );
    });
    await waitFor(() => {
      expect(button()).toHaveAccessibleName(
        projectPermissionProfileButtonCopy(
          "Project access",
          "default",
        ).ariaLabel,
      );
    });
    expect(button()).toHaveTextContent("Default access");
  });
});
