import { Link } from "@tanstack/react-router";
import {
  flexRender,
  getCoreRowModel,
  type ColumnDef,
  type SortingState,
  useReactTable,
} from "@tanstack/react-table";
import type { ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { DataTableColumnHeader } from "@/components/ui/data-table-column-header";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { getSessionForIssue, getStateMeta } from "@/lib/dashboard";
import { appRoutes } from "@/lib/routes";
import type { DashboardWorkSource, IssueSummary } from "@/lib/types";
import { formatRelativeTime, cn } from "@/lib/utils";
import type { WorkSort } from "@/lib/work-url-state";

type IssueColumnId =
  | "identifier"
  | "state"
  | "priority"
  | "project"
  | "epic"
  | "updated"
  | "actions";

const sortableColumns: Record<
  Exclude<IssueColumnId, "actions">,
  {
    label: string;
    primarySort: WorkSort;
    alternateSort: WorkSort;
    primaryAriaSort: "ascending" | "descending";
  }
> = {
  identifier: {
    alternateSort: "identifier_desc",
    label: "Issue",
    primaryAriaSort: "ascending",
    primarySort: "identifier_asc",
  },
  state: {
    alternateSort: "state_desc",
    label: "State",
    primaryAriaSort: "ascending",
    primarySort: "state_asc",
  },
  priority: {
    alternateSort: "priority_desc",
    label: "Priority",
    primaryAriaSort: "ascending",
    primarySort: "priority_asc",
  },
  project: {
    alternateSort: "project_desc",
    label: "Project",
    primaryAriaSort: "ascending",
    primarySort: "project_asc",
  },
  epic: {
    alternateSort: "epic_desc",
    label: "Epic",
    primaryAriaSort: "ascending",
    primarySort: "epic_asc",
  },
  updated: {
    alternateSort: "updated_asc",
    label: "Updated",
    primaryAriaSort: "descending",
    primarySort: "updated_desc",
  },
};

function sortingForWorkSort(sort: WorkSort): SortingState {
  switch (sort) {
    case "identifier_asc":
      return [{ id: "identifier", desc: false }];
    case "identifier_desc":
      return [{ id: "identifier", desc: true }];
    case "state_asc":
      return [{ id: "state", desc: false }];
    case "state_desc":
      return [{ id: "state", desc: true }];
    case "priority_asc":
      return [{ id: "priority", desc: false }];
    case "priority_desc":
      return [{ id: "priority", desc: true }];
    case "project_asc":
      return [{ id: "project", desc: false }];
    case "project_desc":
      return [{ id: "project", desc: true }];
    case "epic_asc":
      return [{ id: "epic", desc: false }];
    case "epic_desc":
      return [{ id: "epic", desc: true }];
    case "updated_desc":
      return [{ id: "updated", desc: true }];
    case "updated_asc":
      return [{ id: "updated", desc: false }];
    case "none":
    default:
      return [];
  }
}

function getColumnSortState(
  columnId: Exclude<IssueColumnId, "actions">,
  sort: WorkSort,
): "ascending" | "descending" | undefined {
  const columnSort = sortableColumns[columnId];
  if (sort === columnSort.primarySort) {
    return columnSort.primaryAriaSort;
  }
  if (sort === columnSort.alternateSort) {
    return columnSort.primaryAriaSort === "ascending" ? "descending" : "ascending";
  }
  return undefined;
}

function getNextSort(
  columnId: Exclude<IssueColumnId, "actions">,
  sort: WorkSort,
): WorkSort {
  const columnSort = sortableColumns[columnId];
  if (sort === columnSort.primarySort) {
    return columnSort.alternateSort;
  }
  if (sort === columnSort.alternateSort) {
    return "none";
  }
  return columnSort.primarySort;
}

function getEdgePaddingClass(index: number, total: number) {
  return cn(
    index === 0 ? "pl-[var(--panel-padding)]" : "",
    index === total - 1 ? "pr-[var(--panel-padding)]" : "",
  );
}

function getColumnWidthClass(columnId: IssueColumnId, showProjectColumn: boolean) {
  switch (columnId) {
    case "identifier":
      return showProjectColumn ? "w-[30%]" : "w-[36%]";
    case "state":
      return "w-[11%]";
    case "priority":
      return "w-[9%]";
    case "project":
      return "w-[13%]";
    case "epic":
      return showProjectColumn ? "w-[17%] pl-4" : "w-[21%]";
    case "updated":
      return showProjectColumn ? "w-[10%]" : "w-[12%]";
    case "actions":
      return "w-[120px]";
  }
}

function IssueIdentityCell({
  bootstrap,
  issue,
  onOpenIssue,
}: {
  bootstrap?: DashboardWorkSource;
  issue: IssueSummary;
  onOpenIssue: (issue: IssueSummary) => void;
}) {
  const liveSession = getSessionForIssue(bootstrap, issue.id, issue.identifier);

  return (
    <button
      className="flex w-full min-w-0 flex-col items-start gap-1 overflow-hidden text-left"
      onClick={() => onOpenIssue(issue)}
      type="button"
    >
      <div className="flex min-w-0 max-w-full flex-wrap items-center gap-1.5">
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
      <p className="block w-full truncate text-sm font-medium leading-5 text-white" title={issue.title}>
        {issue.title}
      </p>
    </button>
  );
}

function issueColumns({
  bootstrap,
  onOpenIssue,
  onSortChange,
  renderListActions,
  sort,
  showProjectColumn,
}: {
  bootstrap?: DashboardWorkSource;
  onOpenIssue: (issue: IssueSummary) => void;
  onSortChange: (sort: WorkSort) => void;
  renderListActions?: (issue: IssueSummary) => ReactNode;
  sort: WorkSort;
  showProjectColumn: boolean;
}): ColumnDef<IssueSummary>[] {
  const columns: ColumnDef<IssueSummary>[] = [
    {
      id: "identifier",
      accessorKey: "identifier",
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={sortableColumns.identifier.label}
          onSortChange={onSortChange}
          nextSort={getNextSort("identifier", sort)}
        />
      ),
      cell: ({ row }) => (
        <IssueIdentityCell bootstrap={bootstrap} issue={row.original} onOpenIssue={onOpenIssue} />
      ),
    },
    {
      id: "state",
      accessorKey: "state",
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={sortableColumns.state.label}
          onSortChange={onSortChange}
          nextSort={getNextSort("state", sort)}
        />
      ),
      cell: ({ row }) => (
        <Badge className="border-white/10 bg-white/5 text-white">
          {getStateMeta(row.original.state).label}
        </Badge>
      ),
    },
    {
      id: "priority",
      accessorKey: "priority",
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={sortableColumns.priority.label}
          onSortChange={onSortChange}
          nextSort={getNextSort("priority", sort)}
        />
      ),
      cell: ({ row }) =>
        row.original.priority > 0 ? (
          <Badge
            aria-label={`Priority ${row.original.priority}`}
            className="border-amber-400/20 bg-amber-400/10 text-amber-200"
            title={`Priority ${row.original.priority}`}
          >
            P{row.original.priority}
          </Badge>
        ) : (
          <Badge
            aria-label="No priority"
            className="border-white/10 bg-white/5 text-[var(--muted-foreground)]"
            title="No priority"
          >
            —
          </Badge>
        ),
    },
  ];

  if (showProjectColumn) {
    columns.push(
      {
        id: "project",
        accessorKey: "project_name",
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={sortableColumns.project.label}
            onSortChange={onSortChange}
            nextSort={getNextSort("project", sort)}
          />
        ),
        cell: ({ row }) =>
          row.original.project_id ? (
            <Link
              className="block w-full min-w-0 truncate text-inherit transition hover:text-white"
              params={{ projectId: row.original.project_id }}
              title={row.original.project_name || "Unassigned"}
              to={appRoutes.projectDetail}
            >
              {row.original.project_name || "Unassigned"}
            </Link>
          ) : (
            <span className="block w-full min-w-0 truncate" title="Unassigned">
              Unassigned
            </span>
          ),
      },
    );
  }

  columns.push(
    {
      id: "epic",
      accessorKey: "epic_name",
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={sortableColumns.epic.label}
          onSortChange={onSortChange}
          nextSort={getNextSort("epic", sort)}
        />
      ),
      cell: ({ row }) =>
        row.original.epic_id ? (
          <Link
            className="block w-full min-w-0 truncate text-inherit transition hover:text-white"
            params={{ epicId: row.original.epic_id }}
            title={row.original.epic_name || "None"}
            to={appRoutes.epicDetail}
          >
            {row.original.epic_name || "None"}
          </Link>
        ) : (
          <span className="block w-full min-w-0 truncate" title="None">
            None
          </span>
        ),
    },
    {
      id: "updated",
      accessorKey: "updated_at",
      header: ({ column }) => (
        <DataTableColumnHeader
          column={column}
          title={sortableColumns.updated.label}
          onSortChange={onSortChange}
          nextSort={getNextSort("updated", sort)}
        />
      ),
      cell: ({ row }) => (
        <span className="block w-full min-w-0 truncate whitespace-nowrap text-[var(--muted-foreground)]">
          {formatRelativeTime(row.original.updated_at)}
        </span>
      ),
    },
  );

  if (renderListActions) {
    columns.push(
      {
        id: "actions",
        header: () => (
          <div className="flex h-8 items-center justify-end text-xs font-medium leading-4 normal-case">
            Actions
          </div>
        ),
        cell: ({ row }) => (
          <div className="flex justify-end gap-2">{renderListActions(row.original)}</div>
        ),
      },
    );
  }

  return columns;
}

export function WorkIssueTable({
  bootstrap,
  items,
  onOpenIssue,
  onSortChange,
  renderListActions,
  showProjectColumn = true,
  sort,
}: {
  bootstrap?: DashboardWorkSource;
  items: IssueSummary[];
  onOpenIssue: (issue: IssueSummary) => void;
  onSortChange: (sort: WorkSort) => void;
  renderListActions?: (issue: IssueSummary) => ReactNode;
  showProjectColumn?: boolean;
  sort: WorkSort;
}) {
  // TanStack Table intentionally returns helper functions that the React compiler flags here.
  // The table is still controlled by props, so this warning is expected.
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data: items,
    columns: issueColumns({
      bootstrap,
      onOpenIssue,
      onSortChange,
      renderListActions,
      sort,
      showProjectColumn,
    }),
    state: {
      sorting: sortingForWorkSort(sort),
    },
    manualSorting: true,
    getCoreRowModel: getCoreRowModel(),
    getRowId: (row) => row.id,
  });

  const tableMinWidthClass = showProjectColumn ? "min-w-[1120px]" : "min-w-[960px]";

  return (
    <div className="overflow-x-auto">
      <Table className={cn("table-fixed text-left text-sm", tableMinWidthClass)}>
        <TableHeader className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
          {table.getHeaderGroups().map((headerGroup) => (
            <TableRow key={headerGroup.id}>
              {headerGroup.headers.map((header, headerIndex) => {
                const columnId = header.column.id as IssueColumnId;
                const ariaSort =
                  columnId === "actions"
                    ? undefined
                    : getColumnSortState(columnId, sort);

                return (
                  <TableHead
                    key={header.id}
                    aria-sort={ariaSort}
                    className={cn(
                      "px-0 pb-4 align-bottom",
                      getEdgePaddingClass(headerIndex, headerGroup.headers.length),
                      getColumnWidthClass(columnId, showProjectColumn),
                      columnId === "actions" ? "text-right" : "",
                    )}
                    scope="col"
                  >
                    {header.isPlaceholder
                      ? null
                      : flexRender(
                          header.column.columnDef.header,
                          header.getContext(),
                        )}
                  </TableHead>
                );
              })}
            </TableRow>
          ))}
        </TableHeader>
        <TableBody>
          {table.getRowModel().rows.map((row) => {
            const visibleCells = row.getVisibleCells();

            return (
              <TableRow key={row.id}>
                {visibleCells.map((cell, cellIndex) => (
                  <TableCell
                    key={cell.id}
                    className={cn(
                      "px-0 py-4 align-top overflow-hidden",
                      getEdgePaddingClass(cellIndex, visibleCells.length),
                      getColumnWidthClass(cell.column.id as IssueColumnId, showProjectColumn),
                      cell.column.id === "state" ||
                        cell.column.id === "priority" ||
                        cell.column.id === "updated" ||
                        cell.column.id === "actions"
                        ? ""
                        : "min-w-0",
                      cell.column.id === "updated" ? "whitespace-nowrap" : "",
                      cell.column.id === "actions" ? "text-right" : "",
                    )}
                  >
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </TableCell>
                ))}
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}
