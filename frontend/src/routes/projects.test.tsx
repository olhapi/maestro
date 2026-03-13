import type { ReactNode } from "react";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";

import { ProjectsPage } from "@/routes/projects";
import { makeBootstrapResponse } from "@/test/fixtures";
import { renderWithQueryClient } from "@/test/test-utils";

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

describe("ProjectsPage", () => {
  it("does not render the portfolio surface badge in the header", async () => {
    const bootstrap = makeBootstrapResponse();

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap);
    vi.mocked(api.listProjects).mockResolvedValue({
      items: bootstrap.projects,
    });
    vi.mocked(api.listEpics).mockResolvedValue({ items: bootstrap.epics });

    renderWithQueryClient(<ProjectsPage />);

    await waitFor(() => {
      expect(
        screen.getByText("Projects are now entry points, not dead-end rollups"),
      ).toBeInTheDocument();
    });

    expect(screen.queryByText("Portfolio surface")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^\+?\s*project$/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^\+?\s*epic$/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^\+?\s*issue$/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /new project/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /new epic/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /new issue/i })).not.toBeInTheDocument();

    const runButton = screen.getByRole("button", { name: /^(run|stop)$/i });
    const editButton = screen.getByRole("button", { name: /^edit$/i });
    const deleteButton = screen.getByRole("button", { name: /^delete$/i });

    expect(runButton.parentElement).toHaveClass("flex-nowrap");
    expect(runButton.parentElement).toContainElement(editButton);
    expect(runButton.parentElement).toContainElement(deleteButton);
    expect(screen.queryByText(/^(run|stop)$/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^edit$/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^delete$/i)).not.toBeInTheDocument();

    const tokenStat = screen.getByText("Tokens").closest("div");
    expect(tokenStat?.parentElement).toHaveClass(
      "grid-cols-[repeat(auto-fit,minmax(min(100%,12rem),1fr))]",
    );
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
          dispatch_error:
            "Project repo is outside the current server scope (/repo/current)",
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

    expect(
      screen.getByText(
        "Project repo is outside the current server scope (/repo/current)",
      ),
    ).toBeInTheDocument();
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
      expect(
        screen.getByRole("button", { name: /^run$/i }),
      ).toBeInTheDocument();
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
});
