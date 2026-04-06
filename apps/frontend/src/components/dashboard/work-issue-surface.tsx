import { Link } from "@tanstack/react-router";
import { type ReactNode } from "react";
import { LayoutGrid, Table2 } from "lucide-react";

import { KanbanBoard } from "@/components/dashboard/kanban-board";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import {
  ToggleGroup,
  ToggleGroupItem,
} from "@/components/ui/toggle-group";
import { useIsMobileLayout } from "@/hooks/use-is-mobile-layout";
import { getSessionForIssue, getStateMeta } from "@/lib/dashboard";
import { appRoutes } from "@/lib/routes";
import type {
  DashboardWorkSource,
  IssueState,
  IssueStateCounts,
  IssueSummary,
} from "@/lib/types";
import { formatRelativeTime } from "@/lib/utils";

type WorkView = "board" | "list";

const issueSortColumns = [
  { value: "identifier_asc", label: "Issue", ariaSort: "ascending" as const },
  { value: "state_asc", label: "State", ariaSort: "ascending" as const },
  { value: "priority_asc", label: "Priority", ariaSort: "ascending" as const },
  { value: "project_asc", label: "Project", ariaSort: "ascending" as const },
  { value: "epic_asc", label: "Epic", ariaSort: "ascending" as const },
  { value: "updated_desc", label: "Updated", ariaSort: "descending" as const },
] as const;

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
  const showBoardView = isMobileLayout || view === "board";
  const projectColumnClass = showProjectColumn ? "w-[14%]" : "";
  const epicColumnClass = showProjectColumn ? "w-[18%] pl-5" : "w-[22%]";
  const issueColumnClass = showProjectColumn ? "w-[34%]" : "w-[40%]";
  const stateColumnClass = "w-[12%]";
  const priorityColumnClass = "w-[10%]";
  const updatedColumnClass = showProjectColumn ? "w-[12%]" : "w-[14%]";
  const tableMinWidthClass = showProjectColumn ? "min-w-[1120px]" : "min-w-[960px]";
  const visibleColumns = issueSortColumns.filter((column) => showProjectColumn || column.value !== "project_asc");

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
            <CardContent className="overflow-x-auto pt-[var(--panel-padding)]">
              <table className={`w-full table-fixed text-left text-sm ${tableMinWidthClass}`}>
                <thead className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  <tr>
                    {visibleColumns.map((column) => {
                      const isActive = sort === column.value;
                      const columnClass =
                        column.value === "identifier_asc"
                          ? issueColumnClass
                          : column.value === "state_asc"
                            ? stateColumnClass
                            : column.value === "priority_asc"
                              ? priorityColumnClass
                              : column.value === "project_asc"
                                ? projectColumnClass
                                : column.value === "epic_asc"
                                  ? epicColumnClass
                                  : updatedColumnClass;

                      return (
                        <th
                          key={column.value}
                          aria-sort={isActive ? column.ariaSort : undefined}
                          className={`pb-4 align-bottom ${columnClass}`}
                          scope="col"
                        >
                          <button
                            aria-label={`Sort by ${column.label}`}
                            className={`inline-flex items-center gap-1 text-left font-medium transition ${
                              isActive
                                ? "text-white"
                                : "text-[var(--muted-foreground)] hover:text-white"
                            }`}
                            onClick={() => onSortChange(column.value)}
                            type="button"
                          >
                            {column.label}
                          </button>
                        </th>
                      );
                    })}
                    {renderListActions ? (
                      <th className="w-[120px] pb-4 align-bottom text-right">
                        Actions
                      </th>
                    ) : null}
                  </tr>
                </thead>
                <tbody>
                  {items.map((issue) => {
                    const liveSession = getSessionForIssue(
                      bootstrap,
                      issue.id,
                      issue.identifier,
                    );

                    return (
                      <tr key={issue.id} className="border-t border-white/6">
                        <td className="py-4 align-top">
                          <button
                            className="flex min-w-0 flex-col items-start gap-1 text-left"
                            onClick={() => onOpenIssue(issue)}
                            type="button"
                          >
                            <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                              <p className="truncate font-mono text-xs uppercase tracking-[0.22em] text-[var(--muted-foreground)]">
                                {issue.identifier}
                              </p>
                              {liveSession ? (
                                <Badge className="border-lime-400/20 bg-lime-400/10 px-1.5 py-0.5 text-[9px] tracking-[0.14em] text-lime-200">
                                  Live
                                </Badge>
                              ) : null}
                              {issue.is_blocked ? (
                                <Badge className="border-red-500/20 bg-red-500/10 px-1.5 py-0.5 text-[9px] tracking-[0.14em] text-red-100">
                                  Blocked
                                </Badge>
                              ) : null}
                            </div>
                            <p
                              className="truncate text-sm font-medium leading-5 text-white"
                              title={issue.title}
                            >
                              {issue.title}
                            </p>
                          </button>
                        </td>
                        <td className="py-4 align-top">
                          <Badge className="border-white/10 bg-white/5 text-white">
                            {getStateMeta(issue.state).label}
                          </Badge>
                        </td>
                        <td className="py-4 align-top">
                          {issue.priority > 0 ? (
                            <Badge
                              aria-label={`Priority ${issue.priority}`}
                              className="border-amber-400/20 bg-amber-400/10 text-amber-200"
                              title={`Priority ${issue.priority}`}
                            >
                              P{issue.priority}
                            </Badge>
                          ) : (
                            <Badge
                              aria-label="No priority"
                              className="border-white/10 bg-white/5 text-[var(--muted-foreground)]"
                              title="No priority"
                            >
                              —
                            </Badge>
                          )}
                        </td>
                        {showProjectColumn ? (
                          <td className="py-4 align-top text-[var(--muted-foreground)]">
                            {issue.project_id ? (
                              <Link
                                className="block truncate text-inherit transition hover:text-white"
                                params={{ projectId: issue.project_id }}
                                title={issue.project_name || "Unassigned"}
                                to={appRoutes.projectDetail}
                              >
                                {issue.project_name || "Unassigned"}
                              </Link>
                            ) : (
                              <span className="block truncate" title="Unassigned">
                                Unassigned
                              </span>
                            )}
                          </td>
                        ) : null}
                        <td
                          className={`py-4 align-top text-[var(--muted-foreground)] ${
                            showProjectColumn ? "pl-5" : ""
                          }`}
                        >
                          {issue.epic_id ? (
                            <Link
                              className="block truncate text-inherit transition hover:text-white"
                              params={{ epicId: issue.epic_id }}
                              title={issue.epic_name || "None"}
                              to={appRoutes.epicDetail}
                            >
                              {issue.epic_name || "None"}
                            </Link>
                          ) : (
                            <span className="block truncate" title="None">
                              None
                            </span>
                          )}
                        </td>
                        <td className="whitespace-nowrap py-4 align-top text-[var(--muted-foreground)]">
                          {formatRelativeTime(issue.updated_at)}
                        </td>
                        {renderListActions ? (
                          <td className="py-4 align-top">
                            <div className="flex justify-end gap-2">
                              {renderListActions(issue)}
                            </div>
                          </td>
                        ) : null}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  );
}
