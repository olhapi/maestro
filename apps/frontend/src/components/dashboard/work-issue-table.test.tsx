import type { AnchorHTMLAttributes, ReactNode } from "react";

import { fireEvent, render, screen } from "@testing-library/react";
import { vi } from "vitest";

import { makeIssueSummary } from "@/test/fixtures";

vi.mock("@tanstack/react-router", () => ({
  Link: (props: {
    children: ReactNode;
    to?: unknown;
    params?: Record<string, string>;
  } & AnchorHTMLAttributes<HTMLAnchorElement>) => {
    const { children, to, params, ...anchorProps } = props;
    void to;
    void params;

    return <a {...anchorProps}>{children}</a>;
  },
}));

import { WorkIssueTable } from "@/components/dashboard/work-issue-table";

describe("WorkIssueTable", () => {
  it("renders sortable headers and truncates long issue metadata", () => {
    const longTitle =
      "Implement an exceptionally long issue title that should be truncated in the list table";
    const longProject =
      "Platform with a very long project name that should not stretch the table";
    const longEpic =
      "Observability with a very long epic name that should not stretch the table";
    const issue = makeIssueSummary({
      identifier: "ISS-123",
      title: longTitle,
      project_id: "project-1",
      project_name: longProject,
      epic_id: "epic-1",
      epic_name: longEpic,
      updated_at: "2026-03-09T11:00:00Z",
    });
    const onSortChange = vi.fn();
    const onOpenIssue = vi.fn();

    render(
      <WorkIssueTable
        items={[issue]}
        sort="updated_desc"
        onOpenIssue={onOpenIssue}
        onSortChange={onSortChange}
        renderListActions={() => <button type="button">Move</button>}
        showProjectColumn
      />,
    );

    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sort by Issue" })).toHaveClass("text-xs");
    expect(screen.getByRole("columnheader", { name: "Updated" })).toHaveAttribute(
      "aria-sort",
      "descending",
    );

    fireEvent.click(screen.getByRole("button", { name: "Sort by Issue" }));

    expect(onSortChange).toHaveBeenCalledWith("identifier_asc");
    expect(screen.getByRole("columnheader", { name: "Actions" })).toBeInTheDocument();

    expect(screen.getByRole("button", { name: /ISS-123.*long issue title/i })).toHaveClass(
      "overflow-hidden",
    );
    expect(
      screen.getByRole("columnheader", { name: "Actions" }).querySelector("div"),
    ).toHaveClass(
      "flex",
      "items-center",
      "justify-end",
      "px-2",
      "text-xs",
      "font-medium",
      "leading-4",
      "normal-case",
    );
    expect(screen.getByText(longTitle)).toHaveClass("truncate", "w-full");
    expect(screen.getByText(longTitle)).toHaveAttribute("title", longTitle);
    expect(screen.getByText(longProject).closest("a")).toHaveClass("truncate", "w-full");
    expect(screen.getByText(longProject)).toHaveAttribute("title", longProject);
    expect(screen.getByText(longEpic).closest("a")).toHaveClass("truncate", "w-full");
    expect(screen.getByText(longEpic)).toHaveAttribute("title", longEpic);
  });

  it("hides the project column when requested", () => {
    const issue = makeIssueSummary({
      project_id: undefined,
      project_name: undefined,
      epic_id: "epic-1",
      epic_name: "Observability",
    });

    render(
      <WorkIssueTable
        items={[issue]}
        sort="priority_asc"
        onOpenIssue={vi.fn()}
        onSortChange={vi.fn()}
        showProjectColumn={false}
      />,
    );

    expect(screen.getByRole("columnheader", { name: "Issue" })).toBeInTheDocument();
    expect(screen.queryByRole("columnheader", { name: "Project" })).not.toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Epic" })).toBeInTheDocument();
  });
});
