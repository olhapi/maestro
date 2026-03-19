import type { DashboardRuntimeSource, IssueState, IssueSummary, PausedEntry, RetryEntry, Session } from '@/lib/types'

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

function matchesIssue(issueID: string, issueIdentifier: string | undefined, candidateIssueID?: string, candidateIdentifier?: string) {
  return candidateIssueID === issueID || (!!issueIdentifier && candidateIdentifier === issueIdentifier)
}

export function getSessionForIssue(
  bootstrap: DashboardRuntimeSource | undefined,
  issueID: string,
  issueIdentifier?: string,
): Session | undefined {
  const sessions = bootstrap?.sessions.sessions
  if (!sessions) {
    return undefined
  }
  if (issueIdentifier && sessions[issueIdentifier]) {
    return sessions[issueIdentifier]
  }
  return sessions[issueID]
}

export function getRetryForIssue(
  bootstrap: DashboardRuntimeSource | undefined,
  issueID: string,
  issueIdentifier?: string,
): RetryEntry | undefined {
  return bootstrap?.overview.snapshot.retrying.find((item) => matchesIssue(issueID, issueIdentifier, item.issue_id, item.identifier))
}

export function getPausedForIssue(
  bootstrap: DashboardRuntimeSource | undefined,
  issueID: string,
  issueIdentifier?: string,
): PausedEntry | undefined {
  return bootstrap?.overview.snapshot.paused.find((item) => matchesIssue(issueID, issueIdentifier, item.issue_id, item.identifier))
}

export function groupIssuesByState(items: IssueSummary[], states: IssueState[] = issueStatesFor(items)) {
  return states.reduce<Record<IssueState, IssueSummary[]>>((groups, state) => {
    groups[state] = items.filter((item) => item.state === state)
    return groups
  }, {} as Record<IssueState, IssueSummary[]>)
}
