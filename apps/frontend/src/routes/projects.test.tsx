import type { ReactNode } from "react";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, vi } from "vitest";

import { ProjectsPage } from "@/routes/projects";
import { projectPermissionProfileButtonCopy } from "@/lib/project-permission-profiles";
import { makeBootstrapResponse } from "@/test/fixtures";
import { renderWithQueryClient } from "@/test/test-utils";
import type { PermissionProfile } from "@/lib/types";

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
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
    listProjects: vi.fn(),
    listEpics: vi.fn(),
    deleteProject: vi.fn(),
    setProjectPermissionProfile: vi.fn(),
    deleteEpic: vi.fn(),
    createProject: vi.fn(),
    updateProject: vi.fn(),
    createEpic: vi.fn(),
    createIssue: vi.fn(),
    runProject: vi.fn(),
    stopProject: vi.fn(),
  },
}));

const { api } = await import("@/lib/api");
const { toast } = await import("sonner");

describe("ProjectsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("does not render the portfolio surface badge in the header", async () => {
    const bootstrap = makeBootstrapResponse();

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.listProjects).mockResolvedValue({
      items: bootstrap.projects,
    });
    vi.mocked(api.listEpics).mockResolvedValue({ items: bootstrap.epics });

    renderWithQueryClient(<ProjectsPage />);

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "Projects" })).toBeInTheDocument();
    });

    expect(screen.queryByText("Portfolio surface")).not.toBeInTheDocument();

    const createGroup = screen.getByRole("group", { name: /create work items/i });
    expect(within(createGroup).getByRole("button", { name: /^\+?\s*project$/i })).toBeInTheDocument();
    expect(within(createGroup).getByRole("button", { name: /^\+?\s*epic$/i })).toBeInTheDocument();
    expect(within(createGroup).getByRole("button", { name: /^\+?\s*issue$/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /new project/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /new epic/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /new issue/i })).not.toBeInTheDocument();

    const runButton = screen.getByRole("button", { name: /^(run|stop)$/i });
    const editButton = screen.getByRole("button", { name: /^edit$/i });
    const deleteButton = screen.getByRole("button", { name: /^delete$/i });

    expect(runButton.parentElement).toContainElement(editButton);
    expect(runButton.parentElement).toContainElement(deleteButton);
    expect(screen.queryByText(/^(run|stop)$/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^edit$/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^delete$/i)).not.toBeInTheDocument();

    expect(screen.getByText("Platform work")).toHaveClass("line-clamp-2");
    expect(screen.queryByText("/repo")).not.toBeInTheDocument();
    expect(screen.queryByText("kanban")).not.toBeInTheDocument();
    expect(screen.getByRole("img", { name: /provider kanban/i })).toBeInTheDocument();

    expect(screen.getByText("Tokens")).toBeInTheDocument();
  });

  it("marks out-of-scope projects as not runnable", async () => {
    const base = makeBootstrapResponse();
    const bootstrap = makeBootstrapResponse({
      overview: {
        ...base.overview,
        status: {
          active_runs: 0,
          scoped_repo_path: "/repo/current",
        },
      },
      projects: [
        {
          ...base.projects[0],
          repo_path: "/repo/other",
          dispatch_ready: false,
          dispatch_error: "Project repo is outside the current server scope (/repo/current)",
        },
      ],
    });

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.listProjects).mockResolvedValue({
      items: bootstrap.projects,
    });
    vi.mocked(api.listEpics).mockResolvedValue({ items: bootstrap.epics });

    renderWithQueryClient(<ProjectsPage />);

    await waitFor(() => {
      expect(screen.getByText("Out of scope")).toBeInTheDocument();
    });

    const badge = screen.getByText("Out of scope");
    await act(async () => {
      fireEvent.pointerEnter(badge, { pointerType: "mouse" });
      fireEvent.mouseEnter(badge);
    });

    await waitFor(() => {
      expect(screen.getByText("Bring the repo into this server scope")).toBeInTheDocument();
    });
    expect(screen.getByText("Project repo is outside the current server scope (/repo/current)")).toBeInTheDocument();
    expect(
      screen.getByText("Move the project's repo path under /repo/current, or restart Maestro scoped to /repo/other."),
    ).toBeInTheDocument();
    expect(screen.getByText("Project repo: /repo/other")).toBeInTheDocument();
    expect(screen.getByText("Server scope: /repo/current")).toBeInTheDocument();
    expect(screen.getByText("Tokens")).toBeInTheDocument();
  });

  it("uses a single run-stop toggle and calls the matching project action", async () => {
    const bootstrap = makeBootstrapResponse({
      projects: [{ ...makeBootstrapResponse().projects[0], state: "stopped" }],
    });
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.listProjects).mockResolvedValue({
      items: bootstrap.projects,
    });
    vi.mocked(api.listEpics).mockResolvedValue({ items: bootstrap.epics });
    vi.mocked(api.runProject).mockResolvedValue({ status: "accepted" });
    vi.mocked(api.stopProject).mockResolvedValue({ status: "stopped" });

    renderWithQueryClient(<ProjectsPage />);

    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^run$/i })).toBeInTheDocument();
    });

    expect(screen.queryByText("Runnable")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /^run$/i }));
    await waitFor(() => {
      expect(api.runProject).toHaveBeenCalledWith(bootstrap.projects[0].id);
    });

    const runningBootstrap = makeBootstrapResponse({
      projects: [{ ...bootstrap.projects[0], state: "running" }],
    });
    vi.mocked(api.bootstrap).mockResolvedValue(runningBootstrap);
    vi.mocked(api.listProjects).mockResolvedValue({
      items: runningBootstrap.projects,
    });
    vi.mocked(api.listEpics).mockResolvedValue({ items: runningBootstrap.epics });

    renderWithQueryClient(<ProjectsPage />);

    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^stop$/i })).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /^stop$/i }));
    await waitFor(() => {
      expect(api.stopProject).toHaveBeenCalledWith(runningBootstrap.projects[0].id);
    });
  });

  it("renders the project permissions icon button on each card and updates the profile", async () => {
    const bootstrap = makeBootstrapResponse();
    let permissionProfile: PermissionProfile = "default";
    const projectID = bootstrap.projects[0].id;
    const projectSummary = () => ({
      ...bootstrap.projects[0],
      permission_profile: permissionProfile,
    });

    vi.mocked(api.bootstrap).mockImplementation(async () => ({
      ...bootstrap,
      projects: [projectSummary()],
    }));
    vi.mocked(api.listProjects).mockImplementation(async () => ({
      items: [projectSummary()],
    }));
    vi.mocked(api.listEpics).mockResolvedValue({ items: bootstrap.epics });
    vi.mocked(api.setProjectPermissionProfile).mockImplementation(
      async (_id, nextProfile) => {
        permissionProfile = nextProfile as PermissionProfile;
        return projectSummary();
      },
    );

    renderWithQueryClient(<ProjectsPage />);

    const permissionButton = () =>
      screen.getByRole("button", {
        name: projectPermissionProfileButtonCopy(
          "Project access",
          permissionProfile,
        ).ariaLabel,
      });

    await waitFor(() => {
      expect(permissionButton()).toBeInTheDocument();
    });

    expect(permissionButton()).toHaveTextContent("Default access");
    expect(permissionButton()).not.toHaveTextContent("?");
    expect(permissionButton().querySelector("svg")).not.toBeNull();

    fireEvent.focus(permissionButton());
    expect(await screen.findByRole("tooltip")).toHaveTextContent(
      "Uses the workspace baseline agent settings until a more specific access mode is chosen.",
    );

    fireEvent.click(permissionButton());
    await waitFor(() => {
      expect(api.setProjectPermissionProfile).toHaveBeenCalledWith(
        projectID,
        "full-access",
      );
    });
    await waitFor(() => {
      expect(permissionButton()).toHaveAccessibleName(
        projectPermissionProfileButtonCopy(
          "Project access",
          "full-access",
        ).ariaLabel,
      );
    });
    expect(permissionButton()).toHaveTextContent("Full access");

    expect(
      screen.getByRole("button", { name: /^(run|stop)$/i }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^edit$/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^delete$/i })).toBeInTheDocument();
  });

  it("shows a delete error when project removal fails", async () => {
    const bootstrap = makeBootstrapResponse();

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.listProjects).mockResolvedValue({
      items: bootstrap.projects,
    });
    vi.mocked(api.listEpics).mockResolvedValue({ items: bootstrap.epics });
    vi.mocked(api.deleteProject).mockRejectedValue(new Error("project has active history"));

    renderWithQueryClient(<ProjectsPage />);

    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^delete$/i })).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /^delete$/i }));
    expect(api.deleteProject).not.toHaveBeenCalled();

    const confirmDialog = await screen.findByRole("dialog", {
      name: /delete platform\?/i,
    });
    fireEvent.click(
      within(confirmDialog).getByRole("button", { name: /delete project/i }),
    );

    await waitFor(() => {
      expect(api.deleteProject).toHaveBeenCalledWith(bootstrap.projects[0].id);
    });
    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith("Unable to delete project: project has active history");
    });
  });
});
