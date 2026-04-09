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
  { sort: WorkSort; ariaSort: "ascending" | "descending"; label: string }
> = {
  identifier: { sort: "identifier_asc", ariaSort: "ascending", label: "Issue" },
  state: { sort: "state_asc", ariaSort: "ascending", label: "State" },
  priority: { sort: "priority_asc", ariaSort: "ascending", label: "Priority" },
  project: { sort: "project_asc", ariaSort: "ascending", label: "Project" },
  epic: { sort: "epic_asc", ariaSort: "ascending", label: "Epic" },
  updated: { sort: "updated_desc", ariaSort: "descending", label: "Updated" },
};

function sortingForWorkSort(sort: WorkSort): SortingState {
  switch (sort) {
    case "identifier_asc":
      return [{ id: "identifier", desc: false }];
    case "state_asc":
      return [{ id: "state", desc: false }];
    case "priority_asc":
      return [{ id: "priority", desc: false }];
    case "project_asc":
      return [{ id: "project", desc: false }];
    case "epic_asc":
      return [{ id: "epic", desc: false }];
    case "updated_desc":
    default:
      return [{ id: "updated", desc: true }];
  }
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

function getAriaSort(
  columnId: Exclude<IssueColumnId, "actions">,
  sort: WorkSort,
): "ascending" | "descending" | undefined {
  return sortableColumns[columnId].sort === sort
    ? sortableColumns[columnId].ariaSort
    : undefined;
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
  showProjectColumn,
}: {
  bootstrap?: DashboardWorkSource;
  onOpenIssue: (issue: IssueSummary) => void;
  onSortChange: (sort: WorkSort) => void;
  renderListActions?: (issue: IssueSummary) => ReactNode;
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
          sortValue={sortableColumns.identifier.sort}
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
          sortValue={sortableColumns.state.sort}
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
          sortValue={sortableColumns.priority.sort}
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
            sortValue={sortableColumns.project.sort}
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
          sortValue={sortableColumns.epic.sort}
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
          sortValue={sortableColumns.updated.sort}
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
          <div className="flex h-8 items-center justify-end px-2 text-xs font-medium leading-4 normal-case">
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
              {headerGroup.headers.map((header) => {
                const columnId = header.column.id as IssueColumnId;
                const ariaSort =
                  columnId === "actions"
                    ? undefined
                    : getAriaSort(columnId, sort);

                return (
                  <TableHead
                    key={header.id}
                    aria-sort={ariaSort}
                    className={cn(
                      "px-0 pb-4 align-bottom",
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
          {table.getRowModel().rows.map((row) => (
            <TableRow key={row.id}>
              {row.getVisibleCells().map((cell) => (
                <TableCell
                  key={cell.id}
                  className={cn(
                    "px-0 py-4 align-top overflow-hidden",
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
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
