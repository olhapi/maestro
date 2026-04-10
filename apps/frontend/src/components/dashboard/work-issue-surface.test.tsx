import type { AnchorHTMLAttributes, ReactNode } from "react";

import { render, screen } from "@testing-library/react";
import { vi } from "vitest";

import { makeIssueSummary } from "@/test/fixtures";

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

vi.mock("@/components/dashboard/kanban-board", () => ({
  KanbanBoard: () => <div data-testid="kanban-board" />,
}));

vi.mock("@/hooks/use-is-mobile-layout", () => ({
  useIsMobileLayout: () => false,
}));

import { WorkIssueSurface } from "@/components/dashboard/work-issue-surface";

describe("WorkIssueSurface", () => {
  it("removes horizontal padding from the list-view wrapper", () => {
    render(
      <WorkIssueSurface
        title="Project work"
        items={[
          makeIssueSummary({
            project_id: undefined,
            project_name: undefined,
            epic_id: undefined,
            epic_name: undefined,
          }),
        ]}
        sort="priority_asc"
        view="list"
        onSortChange={vi.fn()}
        onViewChange={vi.fn()}
        onOpenIssue={vi.fn()}
        onMoveIssue={vi.fn()}
        showProjectColumn={false}
      />,
    );

    const table = screen.getByRole("table");
    const cardContent = table.closest(".overflow-x-auto")?.parentElement;

    expect(cardContent).toHaveClass("!px-0");
  });
});
