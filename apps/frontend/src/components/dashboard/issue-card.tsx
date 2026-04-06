import {
  AlertTriangle,
  ArrowUpRight,
  Clock3,
  Coins,
  GitBranch,
  Link2,
  PlayCircle,
  RotateCcw,
  Workflow,
} from "lucide-react";
import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@/components/ui/hover-card";
import { MarkdownText } from "@/components/ui/markdown";
import type { DashboardWorkSource, IssueState, IssueSummary } from "@/lib/types";
import {
  getPausedForIssue,
  getRetryForIssue,
  getSessionForIssue,
  getStateMeta,
  issueStatesFor,
} from "@/lib/dashboard";
import { describeFailureRuns, failureStatusLabel } from "@/lib/execution";
import { appRoutes } from "@/lib/routes";
import { cn, formatCompactNumber, formatRelativeTime, toTitleCase } from "@/lib/utils";

export function IssueCard({
  issue,
  bootstrap,
  compact,
  onOpen,
  onStateChange,
  menuFooter,
  className,
}: {
  issue: IssueSummary;
  bootstrap?: DashboardWorkSource;
  compact?: boolean;
  onOpen: (issue: IssueSummary) => void;
  onStateChange?: (issue: IssueSummary, state: IssueState) => void;
  menuFooter?: ReactNode;
  className?: string;
}) {
  const session = getSessionForIssue(bootstrap, issue.id, issue.identifier);
  const retry = getRetryForIssue(bootstrap, issue.id, issue.identifier);
  const paused = getPausedForIssue(bootstrap, issue.id, issue.identifier);
  const availableStates = issueStatesFor([issue]);
  const cardBadgeClass = "px-1.75 py-0.5 text-[9px] tracking-[0.14em]";
  const blockedBy = issue.blocked_by?.filter(Boolean).join(", ");
  const labels = issue.labels?.filter(Boolean) ?? [];
  const retryReason = retry?.error
    ? failureStatusLabel(retry.error) ?? toTitleCase(retry.error)
    : null;
  const liveSummary = session?.last_message
    || (session?.last_event ? toTitleCase(session.last_event.replaceAll(".", "_")) : null);

  const content = (
    <button
      className={cn(
        "group block w-full rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/10 bg-[linear-gradient(180deg,rgba(255,255,255,.06),rgba(255,255,255,.03))] p-[var(--panel-padding-tight)] text-left transition hover:border-white/20 hover:bg-white/[0.08]",
        compact ? "min-h-[var(--issue-card-min-height-compact)]" : "min-h-[var(--issue-card-min-height)]",
        className,
      )}
      draggable={false}
      type="button"
      onClick={() => onOpen(issue)}
    >
      <div className="flex items-start justify-between gap-2.5">
        <div className="space-y-1.5">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="font-mono text-xs uppercase tracking-[0.22em] text-[var(--muted-foreground)]">
              {issue.identifier}
            </span>
            {issue.priority <= 1 ? (
              <Badge className={cn(cardBadgeClass, "border-amber-400/20 bg-amber-400/10 text-amber-200")}>
                P{issue.priority}
              </Badge>
            ) : null}
            {issue.is_blocked ? (
              <Badge className={cn(cardBadgeClass, "border-red-500/20 bg-red-500/10 text-red-200")}>
                Blocked
              </Badge>
            ) : null}
            {issue.issue_type === "recurring" ? (
              <Badge className={cn(cardBadgeClass, "border-cyan-400/20 bg-cyan-400/10 text-cyan-100")}>
                Automation
              </Badge>
            ) : null}
            {session ? (
              <Badge className={cn(cardBadgeClass, "border-lime-400/20 bg-lime-400/10 text-lime-200")}>
                Live
              </Badge>
            ) : null}
            {paused ? (
              <Badge className={cn(cardBadgeClass, "border-rose-400/20 bg-rose-400/10 text-rose-100")}>
                Paused
              </Badge>
            ) : null}
          </div>
          <p className="text-sm font-semibold leading-5 text-white">
            {issue.title}
          </p>
        </div>
        <ArrowUpRight className="size-4 text-[var(--muted-foreground)] transition group-hover:text-white" />
      </div>

      <div className="mt-2.5 flex flex-wrap gap-x-2 gap-y-1 text-xs text-[var(--muted-foreground)]">
        {issue.project_name ? <span>{issue.project_name}</span> : null}
        {issue.epic_name ? <span>/ {issue.epic_name}</span> : null}
      </div>

      {!compact ? (
        issue.description?.trim() ? (
          <MarkdownText
            className="mt-2.5 line-clamp-3 space-y-1 text-sm leading-5 text-[var(--muted-foreground)]"
            components={{
              a({ children }) {
                return <span className="font-medium text-inherit">{children}</span>;
              },
            }}
            content={issue.description}
          />
        ) : (
          <p className="mt-2.5 line-clamp-3 text-sm leading-5 text-[var(--muted-foreground)]">
            No description yet.
          </p>
        )
      ) : null}

      <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-xs text-[var(--muted-foreground)]">
        <span className="inline-flex items-center gap-1.5">
          <Clock3 className="size-3.5" />
          {formatRelativeTime(issue.updated_at)}
        </span>
        {issue.branch_name ? (
          <span className="inline-flex items-center gap-1.5">
            <GitBranch className="size-3.5" />
            {issue.branch_name}
          </span>
        ) : null}
        {issue.pr_url ? (
          <span className="inline-flex items-center gap-1.5">
            <Link2 className="size-3.5" />
            PR linked
          </span>
        ) : null}
        {issue.workspace_run_count > 0 ? (
          <span className="inline-flex items-center gap-1.5">
            <PlayCircle className="size-3.5" />
            {issue.workspace_run_count} runs
          </span>
        ) : null}
        {issue.issue_type === "recurring" && issue.next_run_at ? (
          <span className="inline-flex items-center gap-1.5">
            <RotateCcw className="size-3.5" />
            Next automation run {formatRelativeTime(issue.next_run_at)}
          </span>
        ) : null}
        <span className="inline-flex items-center gap-1.5">
          <Coins className="size-3.5" />
          {formatCompactNumber(issue.total_tokens_spent)} tokens
        </span>
      </div>
    </button>
  );

  const hoverPreview = (
    <HoverCardContent align="start" className="space-y-3.5">
      {compact ? (
        issue.description?.trim() ? (
          <MarkdownText
            className="line-clamp-5 space-y-2 text-sm leading-6 text-[var(--muted-foreground)]"
            content={issue.description}
          />
        ) : (
          <p className="line-clamp-5 text-sm leading-6 text-[var(--muted-foreground)]">
            No additional context yet.
          </p>
        )
      ) : null}

      {labels.length > 0 ? (
        <div className="flex flex-wrap gap-1.5">
          {labels.slice(0, 4).map((label) => (
            <Badge key={label} className="border-white/12 bg-white/5 text-white">
              {label}
            </Badge>
          ))}
          {labels.length > 4 ? (
            <Badge className="border-white/12 bg-white/5 text-[var(--muted-foreground)]">
              +{labels.length - 4} more
            </Badge>
          ) : null}
        </div>
      ) : null}

      <div className="grid gap-2 text-xs text-[var(--muted-foreground)]">
        {session ? (
          <div className="inline-flex items-center gap-2">
            <Workflow className="size-3.5 text-lime-300" />
            {liveSummary ? `Live session · ${liveSummary}` : "Live session in progress"}
          </div>
        ) : null}
        {retry ? (
          <div className="grid gap-0.5">
            <div className="inline-flex items-center gap-2">
              <RotateCcw className="size-3.5 text-amber-300" />
              Retry scheduled {formatRelativeTime(retry.due_at)}
            </div>
            {retryReason ? (
              <div className="pl-[1.375rem] text-[11px] text-[var(--muted-foreground)]">
                Reason: {retryReason}
              </div>
            ) : null}
          </div>
        ) : null}
        {paused ? (
          <div className="grid gap-0.5">
            <div className="inline-flex items-center gap-2">
              <AlertTriangle className="size-3.5 text-rose-300" />
              Auto-retries paused
            </div>
            <div className="pl-[1.375rem] text-[11px] text-[var(--muted-foreground)]">
              {describeFailureRuns(paused.consecutive_failures, paused.error)}
            </div>
          </div>
        ) : null}
        {issue.is_blocked ? (
          <div className="inline-flex items-center gap-2">
            <AlertTriangle className="size-3.5 text-red-300" />
            {blockedBy ? `Blocked by ${blockedBy}` : "Blocked by another issue"}
          </div>
        ) : null}
        {issue.workspace_path ? (
          <div className="grid gap-0.5">
            <div className="inline-flex items-center gap-2">
              <PlayCircle className="size-3.5 text-lime-300" />
              Workspace ready
            </div>
            <div className="overflow-x-auto whitespace-nowrap pl-[1.375rem] text-[11px] text-[var(--muted-foreground)]">
              {issue.workspace_path}
            </div>
            {issue.workspace_last_run ? (
              <div className="pl-[1.375rem] text-[11px] text-[var(--muted-foreground)]">
                Last run {formatRelativeTime(issue.workspace_last_run)}
              </div>
            ) : null}
          </div>
        ) : null}
        {issue.issue_type === "recurring" && !issue.next_run_at ? (
          <div className="inline-flex items-center gap-2">
            <RotateCcw className="size-3.5 text-cyan-300" />
            {issue.enabled === false ? "Automation schedule disabled" : "Automation schedule ready"}
          </div>
        ) : null}
        {issue.pr_url ? (
          <a
            className="inline-flex items-center gap-2 text-[var(--accent)] transition hover:text-[var(--accent-strong)]"
            href={issue.pr_url}
            rel="noreferrer"
            target="_blank"
          >
            <Link2 className="size-3.5" />
            Open linked PR
          </a>
        ) : null}
      </div>
    </HoverCardContent>
  );

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div>
          <HoverCard openDelay={120} closeDelay={160}>
            <HoverCardTrigger asChild>{content}</HoverCardTrigger>
            {hoverPreview}
          </HoverCard>
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuLabel>Issue actions</ContextMenuLabel>
        <ContextMenuItem asChild>
          <Link params={{ identifier: issue.identifier }} to={appRoutes.issueDetail}>
            Open details
          </Link>
        </ContextMenuItem>
        <ContextMenuItem onSelect={() => onOpen(issue)}>
          Quick preview
        </ContextMenuItem>
        {onStateChange || menuFooter ? <ContextMenuSeparator /> : null}
        {onStateChange ? <ContextMenuLabel>Move to</ContextMenuLabel> : null}
        {onStateChange
          ? availableStates.map((state) => (
              <ContextMenuItem
                key={state}
                onSelect={() => onStateChange(issue, state)}
              >
                {getStateMeta(state).label}
              </ContextMenuItem>
            ))
          : null}
        {menuFooter ? (
          <>
            {onStateChange ? <ContextMenuSeparator /> : null}
            {menuFooter}
          </>
        ) : null}
      </ContextMenuContent>
    </ContextMenu>
  );
}
