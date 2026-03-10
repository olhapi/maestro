import type { ReactNode } from "react";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";

import { ProjectDetailPage } from "@/routes/project-detail";
import { makeBootstrapResponse } from "@/test/fixtures";
import { renderWithQueryClient } from "@/test/test-utils";

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
      overview: {
        ...makeBootstrapResponse().overview,
        snapshot: {
          ...makeBootstrapResponse().overview.snapshot,
          running: [],
        },
      },
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

    fireEvent.click(screen.getByRole("button", { name: /run project/i }));
    await waitFor(() => {
      expect(api.runProject).toHaveBeenCalledWith("project-1");
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
});
