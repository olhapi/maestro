import { screen } from "@testing-library/react";

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
    window.dispatchEvent(new Event("resize"));
  });

  it("shows a repo setup tip below the badge on mobile", () => {
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      writable: true,
      value: 390,
    });
    window.dispatchEvent(new Event("resize"));

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
      screen.getByText("Tip: open project settings and set Repo path to the local checkout."),
    ).toBeInTheDocument();
  });

  it("shows a scope recovery tip below the badge on mobile", () => {
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      writable: true,
      value: 390,
    });
    window.dispatchEvent(new Event("resize"));

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
      screen.getByText("Tip: move the repo under the current server scope or restart Maestro for that repo."),
    ).toBeInTheDocument();
  });
});
