import { type ReactNode } from "react";
import { LayoutGrid, Table2 } from "lucide-react";

import { KanbanBoard } from "@/components/dashboard/kanban-board";
import { WorkIssueTable } from "@/components/dashboard/work-issue-table";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import {
  ToggleGroup,
  ToggleGroupItem,
} from "@/components/ui/toggle-group";
import { useIsMobileLayout } from "@/hooks/use-is-mobile-layout";
import type { WorkSort, WorkView } from "@/lib/work-url-state";
import type {
  DashboardWorkSource,
  IssueState,
  IssueStateCounts,
  IssueSummary,
} from "@/lib/types";

export function WorkIssueSurface({
  title,
  description,
  items,
  bootstrap,
  stateCounts,
  sort,
  view,
  onSortChange,
  onViewChange,
  onOpenIssue,
  onMoveIssue,
  onCreateIssue,
  renderListActions,
  showProjectColumn = true,
}: {
  title: ReactNode;
  description?: ReactNode;
  items: IssueSummary[];
  bootstrap?: DashboardWorkSource;
  stateCounts?: Partial<IssueStateCounts>;
  sort: WorkSort;
  view: WorkView;
  onSortChange: (sort: WorkSort) => void;
  onViewChange: (view: WorkView) => void;
  onOpenIssue: (issue: IssueSummary) => void;
  onMoveIssue: (issue: IssueSummary, nextState: IssueState) => void;
  onCreateIssue?: (state?: IssueState) => void;
  renderListActions?: (issue: IssueSummary) => ReactNode;
  showProjectColumn?: boolean;
}) {
  const isMobileLayout = useIsMobileLayout();
  const showBoardView = isMobileLayout || view === "board";

  return (
    <div className="grid gap-[var(--section-gap)]">
      <Card className="bg-white/[0.04]">
        <CardHeader className="flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0">
            <h2 className="text-lg font-semibold text-white">{title}</h2>
            {description ? (
              <div className="mt-2 max-w-none text-sm leading-6 text-[var(--muted-foreground)]">
                {description}
              </div>
            ) : null}
          </div>
          <div className="flex w-full items-center justify-start sm:w-auto sm:justify-end">
            {!isMobileLayout ? (
              <ToggleGroup
                aria-label="Switch work view"
                className="inline-flex rounded-[var(--panel-radius)] border border-white/10 bg-black/20 p-0.75"
                type="single"
                value={view}
                onValueChange={(next) => {
                  if (next === "board" || next === "list") {
                    onViewChange(next);
                  }
                }}
              >
                <ToggleGroupItem
                  aria-label="Board view"
                  className="px-2.5 py-1.5"
                  title="Board view"
                  value="board"
                >
                  <LayoutGrid className="size-4" />
                </ToggleGroupItem>
                <ToggleGroupItem
                  aria-label="List view"
                  className="px-2.5 py-1.5"
                  title="List view"
                  value="list"
                >
                  <Table2 className="size-4" />
                </ToggleGroupItem>
              </ToggleGroup>
            ) : null}
          </div>
        </CardHeader>
      </Card>

      {showBoardView ? (
        <div className="m-0">
          <KanbanBoard
            items={items}
            bootstrap={bootstrap}
            stateCounts={stateCounts}
            mode={isMobileLayout ? "grouped" : "board"}
            onOpenIssue={onOpenIssue}
            onMoveIssue={onMoveIssue}
            onCreateIssue={onCreateIssue}
          />
        </div>
      ) : (
        <div className="m-0">
          <Card>
            <CardContent className="pt-[var(--panel-padding)]">
              <WorkIssueTable
                bootstrap={bootstrap}
                items={items}
                onOpenIssue={onOpenIssue}
                onSortChange={onSortChange}
                renderListActions={renderListActions}
                showProjectColumn={showProjectColumn}
                sort={sort}
              />
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  );
}
