import type { DashboardRuntimeSource, IssueState, IssueSummary, PausedEntry, RetryEntry, Session } from '@/lib/types'

export const issueStates: IssueState[] = ['backlog', 'ready', 'in_progress', 'in_review', 'done', 'cancelled']

export const issueSortOptions = [
  { value: 'updated_desc', label: 'Recently updated' },
  { value: 'priority_asc', label: 'Highest priority' },
  { value: 'identifier_asc', label: 'Identifier A-Z' },
  { value: 'state_asc', label: 'State grouping' },
  { value: 'project_asc', label: 'Project A-Z' },
  { value: 'epic_asc', label: 'Epic A-Z' },
] as const

export const stateMeta: Record<string, { label: string; accent: string; boardTint: string; progressFill: string }> = {
  backlog: { label: 'Backlog', accent: 'text-slate-200', boardTint: 'from-slate-500/20 to-slate-900/20', progressFill: 'bg-slate-400/90' },
  ready: { label: 'Ready', accent: 'text-cyan-200', boardTint: 'from-cyan-500/20 to-slate-900/20', progressFill: 'bg-cyan-400/90' },
  in_progress: { label: 'In Progress', accent: 'text-lime-200', boardTint: 'from-lime-500/20 to-slate-900/20', progressFill: 'bg-lime-400/90' },
  in_review: { label: 'In Review', accent: 'text-amber-200', boardTint: 'from-amber-500/20 to-slate-900/20', progressFill: 'bg-amber-400/90' },
  done: { label: 'Done', accent: 'text-emerald-200', boardTint: 'from-emerald-500/20 to-slate-900/20', progressFill: 'bg-emerald-400/90' },
  cancelled: { label: 'Cancelled', accent: 'text-rose-200', boardTint: 'from-rose-500/20 to-slate-900/20', progressFill: 'bg-rose-400/90' },
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
    progressFill: 'bg-sky-400/90',
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

function compareDatesDescending(left: string, right: string) {
  return new Date(right).getTime() - new Date(left).getTime()
}

function compareDatesAscending(left: string, right: string) {
  return new Date(left).getTime() - new Date(right).getTime()
}

function compareNullableStringsAscending(left?: string, right?: string) {
  const leftValue = left?.trim() ?? ''
  const rightValue = right?.trim() ?? ''
  const leftEmpty = leftValue === ''
  const rightEmpty = rightValue === ''
  if (leftEmpty !== rightEmpty) {
    return leftEmpty ? 1 : -1
  }
  return leftValue.localeCompare(rightValue)
}

export function sortIssues(items: IssueSummary[], sort: string) {
  return [...items].sort((left, right) => {
    switch (sort) {
      case 'created_asc':
        return compareDatesAscending(left.created_at, right.created_at)
      case 'priority_asc': {
        const leftPriorityGroup = left.priority > 0 ? 0 : 1
        const rightPriorityGroup = right.priority > 0 ? 0 : 1
        if (leftPriorityGroup !== rightPriorityGroup) {
          return leftPriorityGroup - rightPriorityGroup
        }
        if (left.priority !== right.priority) {
          return left.priority - right.priority
        }
        return compareDatesDescending(left.updated_at, right.updated_at)
      }
      case 'identifier_asc':
        return left.identifier.localeCompare(right.identifier)
      case 'state_asc': {
        const stateDelta = left.state.localeCompare(right.state)
        if (stateDelta !== 0) {
          return stateDelta
        }
        const leftPriorityGroup = left.priority > 0 ? 0 : 1
        const rightPriorityGroup = right.priority > 0 ? 0 : 1
        if (leftPriorityGroup !== rightPriorityGroup) {
          return leftPriorityGroup - rightPriorityGroup
        }
        if (left.priority !== right.priority) {
          return left.priority - right.priority
        }
        return compareDatesDescending(left.updated_at, right.updated_at)
      }
      case 'project_asc': {
        const projectDelta = compareNullableStringsAscending(left.project_name, right.project_name)
        if (projectDelta !== 0) {
          return projectDelta
        }
        const updatedDelta = compareDatesDescending(left.updated_at, right.updated_at)
        if (updatedDelta !== 0) {
          return updatedDelta
        }
        return left.identifier.localeCompare(right.identifier)
      }
      case 'epic_asc': {
        const epicDelta = compareNullableStringsAscending(left.epic_name, right.epic_name)
        if (epicDelta !== 0) {
          return epicDelta
        }
        const updatedDelta = compareDatesDescending(left.updated_at, right.updated_at)
        if (updatedDelta !== 0) {
          return updatedDelta
        }
        return left.identifier.localeCompare(right.identifier)
      }
      case 'updated_desc':
      default: {
        const updatedDelta = compareDatesDescending(left.updated_at, right.updated_at)
        if (updatedDelta !== 0) {
          return updatedDelta
        }
        return compareDatesDescending(left.created_at, right.created_at)
      }
    }
  })
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
  const groups = states.reduce<Record<IssueState, IssueSummary[]>>((acc, state) => {
    acc[state] = []
    return acc
  }, {} as Record<IssueState, IssueSummary[]>)

  for (const item of items) {
    const state = item.state as IssueState
    if (!state) continue
    const group = groups[state] ?? (groups[state] = [])
    group.push(item)
  }

  return groups
}
