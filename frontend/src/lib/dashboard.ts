import type { BootstrapResponse, IssueState, IssueSummary, RetryEntry, Session } from '@/lib/types'

export const issueStates: IssueState[] = ['backlog', 'ready', 'in_progress', 'in_review', 'done', 'cancelled']

export const stateMeta: Record<string, { label: string; accent: string; boardTint: string }> = {
  backlog: { label: 'Backlog', accent: 'text-slate-200', boardTint: 'from-slate-500/20 to-slate-900/20' },
  ready: { label: 'Ready', accent: 'text-cyan-200', boardTint: 'from-cyan-500/20 to-slate-900/20' },
  in_progress: { label: 'In Progress', accent: 'text-lime-200', boardTint: 'from-lime-500/20 to-slate-900/20' },
  in_review: { label: 'In Review', accent: 'text-amber-200', boardTint: 'from-amber-500/20 to-slate-900/20' },
  done: { label: 'Done', accent: 'text-emerald-200', boardTint: 'from-emerald-500/20 to-slate-900/20' },
  cancelled: { label: 'Cancelled', accent: 'text-rose-200', boardTint: 'from-rose-500/20 to-slate-900/20' },
}

export function getStateMeta(state: string) {
  const known = stateMeta[state]
  if (known) {
    return known
  }
  const label = state
    .split(/[_-]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
  return {
    label: label || 'Unknown',
    accent: 'text-sky-200',
    boardTint: 'from-sky-500/20 to-slate-900/20',
  }
}

export function issueStatesFor(items: IssueSummary[], preferredStates: string[] = issueStates): IssueState[] {
  const seen = new Set<string>()
  const ordered: IssueState[] = []
  for (const state of preferredStates) {
    if (seen.has(state)) continue
    seen.add(state)
    ordered.push(state)
  }
  for (const item of items) {
    if (!item.state || seen.has(item.state)) continue
    seen.add(item.state)
    ordered.push(item.state)
  }
  return ordered
}

export function getSessionForIssue(bootstrap: BootstrapResponse | undefined, issueID: string): Session | undefined {
  return bootstrap?.sessions.sessions[issueID]
}

export function getRetryForIssue(bootstrap: BootstrapResponse | undefined, issueID: string): RetryEntry | undefined {
  return bootstrap?.overview.snapshot.retrying.find((item) => item.issue_id === issueID)
}

export function groupIssuesByState(items: IssueSummary[], states: IssueState[] = issueStatesFor(items)) {
  return states.reduce<Record<IssueState, IssueSummary[]>>((groups, state) => {
    groups[state] = items.filter((item) => item.state === state)
    return groups
  }, {} as Record<IssueState, IssueSummary[]>)
}
