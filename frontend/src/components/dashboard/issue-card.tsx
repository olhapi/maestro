import { AlertTriangle, ArrowUpRight, Clock3, GitBranch, Link2, PlayCircle, RotateCcw, Workflow } from 'lucide-react'
import type { ReactNode } from 'react'

import { Badge } from '@/components/ui/badge'
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from '@/components/ui/context-menu'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import type { BootstrapResponse, IssueState, IssueSummary } from '@/lib/types'
import { getRetryForIssue, getSessionForIssue, stateMeta } from '@/lib/dashboard'
import { cn, formatRelativeTime } from '@/lib/utils'

export function IssueCard({
  issue,
  bootstrap,
  compact,
  onOpen,
  onStateChange,
  menuFooter,
}: {
  issue: IssueSummary
  bootstrap?: BootstrapResponse
  compact?: boolean
  onOpen: (issue: IssueSummary) => void
  onStateChange?: (issue: IssueSummary, state: IssueState) => void
  menuFooter?: ReactNode
}) {
  const session = getSessionForIssue(bootstrap, issue.id)
  const retry = getRetryForIssue(bootstrap, issue.id)
  const meta = stateMeta[issue.state]

  const content = (
    <button
      className={cn(
        'group w-full rounded-[1.35rem] border border-white/10 bg-[linear-gradient(180deg,rgba(255,255,255,.06),rgba(255,255,255,.03))] p-4 text-left transition hover:border-white/20 hover:bg-white/[0.08]',
        compact ? 'min-h-[132px]' : 'min-h-[176px]',
      )}
      onClick={() => onOpen(issue)}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-xs uppercase tracking-[0.22em] text-[var(--muted-foreground)]">{issue.identifier}</span>
            <Badge className={cn('border-white/12 bg-white/5', meta.accent)}>{meta.label}</Badge>
            {issue.priority <= 1 ? <Badge className="border-amber-400/20 bg-amber-400/10 text-amber-200">P{issue.priority}</Badge> : null}
            {issue.is_blocked ? <Badge className="border-red-500/20 bg-red-500/10 text-red-200">Blocked</Badge> : null}
            {session ? <Badge className="border-lime-400/20 bg-lime-400/10 text-lime-200">Live</Badge> : null}
          </div>
          <p className="text-sm font-semibold leading-6 text-white">{issue.title}</p>
        </div>
        <ArrowUpRight className="size-4 text-[var(--muted-foreground)] transition group-hover:text-white" />
      </div>

      <div className="mt-3 flex flex-wrap gap-2 text-xs text-[var(--muted-foreground)]">
        {issue.project_name ? <span>{issue.project_name}</span> : null}
        {issue.epic_name ? <span>/ {issue.epic_name}</span> : null}
      </div>

      {!compact ? <p className="mt-3 line-clamp-3 text-sm leading-6 text-[var(--muted-foreground)]">{issue.description || 'No description yet.'}</p> : null}

      <div className="mt-4 flex flex-wrap items-center gap-3 text-xs text-[var(--muted-foreground)]">
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
      </div>
    </button>
  )

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div>
          <Tooltip>
            <TooltipTrigger asChild>{content}</TooltipTrigger>
            <TooltipContent align="start" className="space-y-3">
              <div className="flex items-center justify-between gap-3">
                <p className="font-medium text-white">{issue.identifier}</p>
                <Badge className="border-white/12 bg-white/5 text-white">{stateMeta[issue.state].label}</Badge>
              </div>
              <p className="line-clamp-3 text-sm leading-6 text-[var(--muted-foreground)]">{issue.description || 'No description available.'}</p>
              <div className="grid gap-2 text-xs text-[var(--muted-foreground)]">
                {session ? (
                  <div className="inline-flex items-center gap-2">
                    <Workflow className="size-3.5 text-lime-300" />
                    Live session in progress
                  </div>
                ) : null}
                {retry ? (
                  <div className="inline-flex items-center gap-2">
                    <RotateCcw className="size-3.5 text-amber-300" />
                    Retry pending
                  </div>
                ) : null}
                {issue.is_blocked ? (
                  <div className="inline-flex items-center gap-2">
                    <AlertTriangle className="size-3.5 text-red-300" />
                    Blocked by {issue.blocked_by?.join(', ')}
                  </div>
                ) : null}
              </div>
            </TooltipContent>
          </Tooltip>
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuLabel>Issue actions</ContextMenuLabel>
        <ContextMenuItem onSelect={() => onOpen(issue)}>Open details</ContextMenuItem>
        {onStateChange ? (
          <>
            <ContextMenuSeparator />
            <ContextMenuLabel>Move to</ContextMenuLabel>
            {(['backlog', 'ready', 'in_progress', 'in_review', 'done', 'cancelled'] as IssueState[]).map((state) => (
              <ContextMenuItem key={state} onSelect={() => onStateChange(issue, state)}>
                {stateMeta[state].label}
              </ContextMenuItem>
            ))}
          </>
        ) : null}
        {menuFooter ? (
          <>
            <ContextMenuSeparator />
            {menuFooter}
          </>
        ) : null}
      </ContextMenuContent>
    </ContextMenu>
  )
}
