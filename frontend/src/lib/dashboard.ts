import type { BootstrapResponse, IssueState, IssueSummary, RetryEntry, Session } from '@/lib/types'

export const issueStates: IssueState[] = ['backlog', 'ready', 'in_progress', 'in_review', 'done', 'cancelled']

export const stateMeta: Record<IssueState, { label: string; accent: string; boardTint: string }> = {
  backlog: { label: 'Backlog', accent: 'text-slate-200', boardTint: 'from-slate-500/20 to-slate-900/20' },
  ready: { label: 'Ready', accent: 'text-cyan-200', boardTint: 'from-cyan-500/20 to-slate-900/20' },
  in_progress: { label: 'In Progress', accent: 'text-lime-200', boardTint: 'from-lime-500/20 to-slate-900/20' },
  in_review: { label: 'In Review', accent: 'text-amber-200', boardTint: 'from-amber-500/20 to-slate-900/20' },
  done: { label: 'Done', accent: 'text-emerald-200', boardTint: 'from-emerald-500/20 to-slate-900/20' },
  cancelled: { label: 'Cancelled', accent: 'text-rose-200', boardTint: 'from-rose-500/20 to-slate-900/20' },
}

export function getSessionForIssue(bootstrap: BootstrapResponse | undefined, issueID: string): Session | undefined {
  return bootstrap?.sessions.sessions[issueID]
}

export function getRetryForIssue(bootstrap: BootstrapResponse | undefined, issueID: string): RetryEntry | undefined {
  return bootstrap?.overview.snapshot.retrying.find((item) => item.issue_id === issueID)
}

export function groupIssuesByState(items: IssueSummary[]) {
  return issueStates.reduce<Record<IssueState, IssueSummary[]>>((groups, state) => {
    groups[state] = items.filter((item) => item.state === state)
    return groups
  }, {} as Record<IssueState, IssueSummary[]>)
}
