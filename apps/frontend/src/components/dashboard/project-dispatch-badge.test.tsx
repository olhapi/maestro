import { act, screen } from "@testing-library/react";

import { ProjectDispatchBadge } from "@/components/dashboard/project-dispatch-badge";
import { renderWithQueryClient } from "@/test/test-utils";

const initialInnerWidth = window.innerWidth;

describe("ProjectDispatchBadge", () => {
  beforeEach(() => {
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      writable: true,
      value: initialInnerWidth,
    });
    act(() => {
      window.dispatchEvent(new Event("resize"));
    });
  });

  it("shows the full repo setup guidance on mobile", () => {
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      writable: true,
      value: 390,
    });
    act(() => {
      window.dispatchEvent(new Event("resize"));
    });

    renderWithQueryClient(
      <ProjectDispatchBadge
        project={{
          orchestration_ready: false,
          dispatch_ready: false,
          dispatch_error: undefined,
          repo_path: "",
          workflow_path: "",
        }}
      />,
    );

    expect(screen.getByText("Needs repo setup")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Attach this project to a local repository",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "Maestro needs a checked-out repo before it can create workspaces, branches, or run the workflow.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "Tip: open project settings and set Repo path to the local checkout.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "Open the project settings and set Repo path to the local checkout for this project.",
      ),
    ).toBeInTheDocument();
  });

  it("shows the scope recovery details on mobile", () => {
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      writable: true,
      value: 390,
    });
    act(() => {
      window.dispatchEvent(new Event("resize"));
    });

    renderWithQueryClient(
      <ProjectDispatchBadge
        project={{
          orchestration_ready: false,
          dispatch_ready: false,
          dispatch_error: "Project repo is outside the current server scope (/repo/current)",
          repo_path: "/repo/other",
          workflow_path: "/repo/other/WORKFLOW.md",
        }}
      />,
    );

    expect(screen.getByText("Out of scope")).toBeInTheDocument();
    expect(
      screen.getByText("Bring the repo into this server scope"),
    ).toBeInTheDocument();
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
      screen.getByText("Move the project's repo path under /repo/current, or restart Maestro scoped to /repo/other."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Project repo: /repo/other"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Server scope: /repo/current"),
    ).toBeInTheDocument();
  });
});
