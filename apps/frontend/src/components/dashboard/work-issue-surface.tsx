import { useMemo, type ReactNode } from "react";
import { LayoutGrid, List } from "lucide-react";
import { Link } from "@tanstack/react-router";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  ToggleGroup,
  ToggleGroupItem,
} from "@/components/ui/toggle-group";
import { KanbanBoard } from "@/components/dashboard/kanban-board";
import { useIsMobileLayout } from "@/hooks/use-is-mobile-layout";
import { issueSortOptions, sortIssues, getStateMeta } from "@/lib/dashboard";
import { appRoutes } from "@/lib/routes";
import type { DashboardWorkSource, IssueState, IssueSummary } from "@/lib/types";
import { formatRelativeTime } from "@/lib/utils";

type WorkView = "board" | "list";

export function WorkIssueSurface({
  title,
  description,
  items,
  bootstrap,
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
  sort: string;
  view: WorkView;
  onSortChange: (sort: string) => void;
  onViewChange: (view: WorkView) => void;
  onOpenIssue: (issue: IssueSummary) => void;
  onMoveIssue: (issue: IssueSummary, nextState: IssueState) => void;
  onCreateIssue?: (state?: IssueState) => void;
  renderListActions?: (issue: IssueSummary) => ReactNode;
  showProjectColumn?: boolean;
}) {
  const isMobileLayout = useIsMobileLayout();
  const sortedItems = useMemo(() => sortIssues(items, sort), [items, sort]);
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
          <div className="flex w-full flex-col gap-2 sm:w-auto sm:flex-row sm:items-center">
            <Select value={sort} onValueChange={onSortChange}>
              <SelectTrigger
                aria-label="Sort issues"
                className={isMobileLayout ? "h-9 w-full text-xs" : "h-9 w-[176px] text-xs"}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {issueSortOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
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
                <ToggleGroupItem aria-label="Board view" className="px-2.5 py-1.5" title="Board view" value="board">
                  <LayoutGrid className="size-4" />
                </ToggleGroupItem>
                <ToggleGroupItem aria-label="List view" className="px-2.5 py-1.5" title="List view" value="list">
                  <List className="size-4" />
                </ToggleGroupItem>
              </ToggleGroup>
            ) : null}
          </div>
        </CardHeader>
      </Card>

      {showBoardView ? (
        <div className="m-0">
          <KanbanBoard
            items={sortedItems}
            bootstrap={bootstrap}
            mode={isMobileLayout ? "grouped" : "board"}
            onOpenIssue={onOpenIssue}
            onMoveIssue={onMoveIssue}
            onCreateIssue={onCreateIssue}
          />
        </div>
      ) : (
        <div className="m-0">
          <Card>
            <CardContent className="overflow-x-auto pt-[var(--panel-padding)]">
              <table
                className={`w-full text-left text-sm ${showProjectColumn ? "min-w-[960px]" : "min-w-[820px]"}`}
              >
                <thead className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  <tr>
                    <th className="pb-4">Issue</th>
                    <th className="pb-4">State</th>
                    {showProjectColumn ? <th className="pb-4">Project</th> : null}
                    <th className="pb-4">Epic</th>
                    <th className="pb-4">Updated</th>
                    {renderListActions ? <th className="pb-4 text-right">Actions</th> : null}
                  </tr>
                </thead>
                <tbody>
                  {sortedItems.map((issue) => (
                    <tr key={issue.id} className="border-t border-white/6">
                      <td className="py-4">
                        <button className="text-left" onClick={() => onOpenIssue(issue)}>
                          <p className="font-medium text-white">{issue.identifier}</p>
                          <p className="max-w-[420px] text-sm text-[var(--muted-foreground)]">{issue.title}</p>
                        </button>
                      </td>
                      <td className="py-4">
                        <Badge className="border-white/10 bg-white/5 text-white">
                          {getStateMeta(issue.state).label}
                        </Badge>
                      </td>
                      {showProjectColumn ? (
                        <td className="py-4 text-[var(--muted-foreground)]">
                          {issue.project_id ? (
                            <Link params={{ projectId: issue.project_id }} to={appRoutes.projectDetail}>
                              {issue.project_name || "Unassigned"}
                            </Link>
                          ) : (
                            "Unassigned"
                          )}
                        </td>
                      ) : null}
                      <td className="py-4 text-[var(--muted-foreground)]">
                        {issue.epic_id ? (
                          <Link params={{ epicId: issue.epic_id }} to={appRoutes.epicDetail}>
                            {issue.epic_name || "None"}
                          </Link>
                        ) : (
                          "None"
                        )}
                      </td>
                      <td className="py-4 text-[var(--muted-foreground)]">{formatRelativeTime(issue.updated_at)}</td>
                      {renderListActions ? (
                        <td className="py-4">
                          <div className="flex justify-end gap-2">
                            {renderListActions(issue)}
                          </div>
                        </td>
                      ) : null}
                    </tr>
                  ))}
                </tbody>
              </table>
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  );
}
