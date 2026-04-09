import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react";
import type { Column } from "@tanstack/react-table";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { WorkSort } from "@/lib/work-url-state";

export function DataTableColumnHeader<TData, TValue>({
  column,
  title,
  onSortChange,
  sortValue,
  className,
}: {
  column: Column<TData, TValue>;
  title: string;
  onSortChange: (sort: WorkSort) => void;
  sortValue: WorkSort;
  className?: string;
}) {
  if (!column.getCanSort()) {
    return <div className={cn(className)}>{title}</div>;
  }

  const isSorted = column.getIsSorted();

  return (
    <Button
      aria-label={`Sort by ${title}`}
      className={cn(
        "h-8 justify-start gap-1.5 px-2 font-medium text-xs",
        className,
      )}
      size="sm"
      type="button"
      variant="ghost"
      onClick={() => onSortChange(sortValue)}
    >
      <span className="truncate">{title}</span>
      {isSorted === "desc" ? (
        <ArrowDown className="size-4 shrink-0" />
      ) : isSorted === "asc" ? (
        <ArrowUp className="size-4 shrink-0" />
      ) : (
        <ChevronsUpDown className="size-4 shrink-0 text-[var(--muted-foreground)]" />
      )}
    </Button>
  );
}
