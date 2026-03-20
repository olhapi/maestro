import { KanbanSquare, ListTodo } from "lucide-react";

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { toTitleCase } from "@/lib/utils";
import { cn } from "@/lib/utils";

export function ProjectProviderChip({
  className,
  providerKind,
}: {
  className?: string;
  providerKind?: string;
}) {
  const normalizedKind = providerKind?.trim().toLowerCase() || "kanban";
  const ProviderIcon = normalizedKind === "linear" ? ListTodo : KanbanSquare;
  const label = `Provider ${toTitleCase(normalizedKind)}`;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          aria-label={label}
          className={cn(
            "inline-flex size-7 shrink-0 cursor-help items-center justify-center rounded-full border border-white/10 bg-white/6 text-[var(--muted-foreground)]",
            className,
          )}
          role="img"
          tabIndex={0}
        >
          <ProviderIcon className="size-3.5" />
        </span>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}
