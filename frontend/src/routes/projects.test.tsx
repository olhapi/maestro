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
      overview: {
        ...makeBootstrapResponse().overview,
        snapshot: {
          ...makeBootstrapResponse().overview.snapshot,
          running: [],
        },
      },
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
      overview: {
        ...bootstrap.overview,
        snapshot: {
          ...bootstrap.overview.snapshot,
          running: [
            {
              ...makeBootstrapResponse().overview.snapshot.running[0],
              issue_id: bootstrap.issues.items[0].id,
            },
          ],
        },
      },
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
