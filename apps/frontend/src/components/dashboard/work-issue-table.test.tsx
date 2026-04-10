import type { AnchorHTMLAttributes, ReactNode } from "react";
import { useState } from "react";

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { vi } from "vitest";

import { makeIssueSummary } from "@/test/fixtures";
import type { IssueSummary } from "@/lib/types";
import type { WorkSort } from "@/lib/work-url-state";

vi.mock("@tanstack/react-router", () => ({
  Link: (
    props: {
      children: ReactNode;
      to?: unknown;
      params?: Record<string, string>;
    } & AnchorHTMLAttributes<HTMLAnchorElement>,
  ) => {
    const { children, to, params, ...anchorProps } = props;
    void to;
    void params;

    return <a {...anchorProps}>{children}</a>;
  },
}));

import { WorkIssueTable } from "@/components/dashboard/work-issue-table";

function TableHarness({
  issue,
  onSortChange,
  showProjectColumn = false,
}: {
  issue: IssueSummary;
  onSortChange: (sort: WorkSort) => void;
  showProjectColumn?: boolean;
}) {
  const [sort, setSort] = useState<WorkSort>("priority_asc");

  return (
    <WorkIssueTable
      items={[issue]}
      sort={sort}
      onOpenIssue={vi.fn()}
      onSortChange={(nextSort) => {
        onSortChange(nextSort);
        setSort(nextSort);
      }}
      renderListActions={() => <button type="button">Move</button>}
      showProjectColumn={showProjectColumn}
    />
  );
}

describe("WorkIssueTable", () => {
  it("cycles sortable headers through asc, desc, and none", async () => {
    const issue = makeIssueSummary({
      identifier: "ISS-123",
      title:
        "Implement an exceptionally long issue title that should be truncated in the list table",
      project_id: undefined,
      project_name: undefined,
      epic_id: undefined,
      epic_name: undefined,
      updated_at: "2026-03-09T11:00:00Z",
    });
    const onSortChange = vi.fn();

    render(<TableHarness issue={issue} onSortChange={onSortChange} />);

    expect(screen.getByRole("button", { name: "Sort by Issue" })).toHaveClass("text-xs");
    expect(screen.getByRole("columnheader", { name: "Priority" })).toHaveAttribute(
      "aria-sort",
      "ascending",
    );

    fireEvent.click(screen.getByRole("button", { name: "Sort by Issue" }));

    await waitFor(() => {
      expect(onSortChange).toHaveBeenLastCalledWith("identifier_asc");
    });
    expect(screen.getByRole("columnheader", { name: "Issue" })).toHaveAttribute(
      "aria-sort",
      "ascending",
    );

    fireEvent.click(screen.getByRole("button", { name: "Sort by Issue" }));

    await waitFor(() => {
      expect(onSortChange).toHaveBeenLastCalledWith("identifier_desc");
    });
    expect(screen.getByRole("columnheader", { name: "Issue" })).toHaveAttribute(
      "aria-sort",
      "descending",
    );

    fireEvent.click(screen.getByRole("button", { name: "Sort by Issue" }));

    await waitFor(() => {
      expect(onSortChange).toHaveBeenLastCalledWith("none");
    });
    expect(screen.getByRole("columnheader", { name: "Issue" })).not.toHaveAttribute(
      "aria-sort",
    );
  });

  it("moves the outer padding onto the first and last visible cells", () => {
    const issue = makeIssueSummary({
      identifier: "ISS-123",
      title:
        "Implement an exceptionally long issue title that should be truncated in the list table",
      project_name:
        "Platform with a very long project name that should not stretch the table",
      epic_name: "Workflow & Branching",
      updated_at: "2026-03-09T11:00:00Z",
    });

    render(
      <WorkIssueTable
        items={[issue]}
        sort="updated_desc"
        onOpenIssue={vi.fn()}
        onSortChange={vi.fn()}
        renderListActions={() => <button type="button">Move</button>}
        showProjectColumn
      />,
    );

    expect(screen.getByRole("columnheader", { name: "Issue" })).toHaveClass(
      "pl-[var(--panel-padding)]",
    );
    expect(screen.getByRole("columnheader", { name: "Actions" })).toHaveClass(
      "pr-[var(--panel-padding)]",
    );

    expect(
      screen
        .getByRole("button", {
          name: /ISS-123.*long issue title/i,
        })
        .closest("td"),
    ).toHaveClass("pl-[var(--panel-padding)]");
    expect(screen.getByRole("button", { name: "Move" }).closest("td")).toHaveClass(
      "pr-[var(--panel-padding)]",
    );
  });
});
