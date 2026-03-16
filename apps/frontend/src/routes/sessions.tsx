import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'

import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { api } from '@/lib/api'
import { appRoutes } from '@/lib/routes'
import type { SessionFeedEntry } from '@/lib/types'
import { formatCompactNumber, formatDateTime, formatNumber, formatRelativeTime, toTitleCase } from '@/lib/utils'

const quietThresholdMs = 10_000

function isQuiet(entry: SessionFeedEntry) {
  if (!entry.active || entry.source !== 'live') {
    return false
  }
  const updatedAt = Date.parse(entry.updated_at)
  if (Number.isNaN(updatedAt)) {
    return false
  }
  return Date.now() - updatedAt > quietThresholdMs
}

function summaryText(entry: SessionFeedEntry) {
  if (entry.pending_interrupt?.last_activity) {
    return entry.pending_interrupt.last_activity
  }
  return entry.last_message || entry.error || entry.last_event || 'No progress details yet.'
}

function badgeClassForStatus(status: SessionFeedEntry['status']) {
  switch (status) {
    case 'active':
      return 'border-lime-400/20 bg-lime-400/10 text-lime-100'
    case 'waiting':
      return 'border-amber-400/20 bg-amber-400/10 text-amber-100'
    case 'paused':
      return 'border-amber-400/20 bg-amber-400/10 text-amber-100'
    case 'completed':
      return 'border-sky-400/20 bg-sky-400/10 text-sky-100'
    case 'interrupted':
      return 'border-orange-400/20 bg-orange-400/10 text-orange-100'
    case 'failed':
    default:
      return 'border-rose-400/20 bg-rose-400/10 text-rose-100'
  }
}

export function SessionsPage() {
  const sessions = useQuery({
    queryKey: ['sessions'],
    queryFn: api.listSessions,
    refetchInterval: (query) => (query.state.data?.entries?.some((entry) => entry.active) ? 2000 : false),
    refetchIntervalInBackground: true,
  })
  const hasActiveEntries = sessions.data?.entries?.some((entry) => entry.active) ?? false
  const events = useQuery({
    queryKey: ['runtime-events'],
    queryFn: api.listRuntimeEvents,
    refetchInterval: hasActiveEntries ? 5000 : false,
    refetchIntervalInBackground: true,
  })

  if (!sessions.data || !events.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />
  }

  const entries = sessions.data.entries

  return (
    <div className="grid gap-[var(--section-gap)]">
      <div className="min-w-0">
        <h3 className="font-display text-[length:var(--page-title-size)] font-semibold leading-[var(--page-title-line-height)]">Threads, turns, and event traces</h3>
      </div>
      <div className="grid min-w-0 gap-[var(--section-gap)] lg:grid-cols-[minmax(0,1.2fr)_minmax(0,.8fr)]">
        <Card className="min-w-0">
          <CardHeader>
            <div className="min-w-0">
              <CardTitle>Run transparency</CardTitle>
              <CardDescription>Live sessions first, followed by recent persisted runs sorted by issue title for faster triage.</CardDescription>
            </div>
          </CardHeader>
          <CardContent className="grid gap-2.5">
            {entries.length === 0 ? (
              <p className="text-sm text-[var(--muted-foreground)]">No live or recent runs are available.</p>
            ) : (
              entries.map((entry) => {
                const quiet = isQuiet(entry)
                const title = entry.issue_title || entry.issue_identifier
                const context = [
                  entry.issue_identifier,
                  entry.phase ? toTitleCase(entry.phase) : '',
                  entry.attempt ? `Attempt ${entry.attempt}` : '',
                ]
                  .filter(Boolean)
                  .join(' · ')

                return (
                  <div key={`${entry.source}-${entry.issue_identifier}`} className="rounded-[var(--panel-radius)] border border-white/8 bg-black/20 p-[var(--panel-padding)]">
                    <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <p className="font-medium text-white">{title}</p>
                          <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(entry.source)}</Badge>
                          <Badge className={badgeClassForStatus(entry.status)}>{toTitleCase(entry.status)}</Badge>
                          {entry.pending_interrupt?.collaboration_mode === 'plan' ? (
                            <Badge className="border-sky-400/20 bg-sky-400/10 text-sky-100">Plan turn</Badge>
                          ) : null}
                          {quiet ? <Badge className="border-orange-400/20 bg-orange-400/10 text-orange-100">Quiet</Badge> : null}
                        </div>
                        <p className="mt-2 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{context}</p>
                        <p data-testid={`session-summary-${entry.issue_identifier}`} className="mt-2 line-clamp-2 text-sm text-[var(--muted-foreground)]">
                          {summaryText(entry)}
                        </p>
                        <p className="mt-2 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                          Updated {formatRelativeTime(entry.updated_at)} · {formatDateTime(entry.updated_at)}
                        </p>
                      </div>
                      <Link
                        className="self-start rounded-full border border-white/10 px-3 py-1.5 text-sm text-white transition hover:border-white/20 hover:bg-white/5"
                        params={{ identifier: entry.issue_identifier }}
                        to={appRoutes.issueDetail}
                      >
                        Open issue
                      </Link>
                    </div>

                    <div className="mt-3.5 grid grid-cols-1 gap-2.5 text-sm sm:grid-cols-3">
                      <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/4 p-3 text-[var(--muted-foreground)]">
                        <p className="text-xs uppercase tracking-[0.18em]">Tokens</p>
                        <p className="mt-2 text-xl text-white">{formatCompactNumber(entry.total_tokens)}</p>
                      </div>
                      <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/4 p-3 text-[var(--muted-foreground)]">
                        <p className="text-xs uppercase tracking-[0.18em]">Turns</p>
                        <p className="mt-2 text-xl text-white">{formatNumber(entry.turns_started)}</p>
                        <p className="mt-1 text-xs">Completed {formatNumber(entry.turns_completed)}</p>
                      </div>
                      <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/4 p-3 text-[var(--muted-foreground)]">
                        <p className="text-xs uppercase tracking-[0.18em]">Events</p>
                        <p className="mt-2 text-xl text-white">{formatNumber(entry.events_processed)}</p>
                      </div>
                    </div>
                  </div>
                )
              })
            )}
          </CardContent>
        </Card>

        <Card className="min-w-0">
          <CardHeader>
            <div className="min-w-0">
              <CardTitle>Recent runtime events</CardTitle>
              <CardDescription>Global orchestration context for the current control-plane state.</CardDescription>
            </div>
          </CardHeader>
          <CardContent className="space-y-2.5">
            {events.data.events.slice(-12).reverse().map((event) => (
              <div key={event.seq} className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5">
                <div className="flex items-center justify-between gap-3">
                  <p className="text-sm font-medium text-white">{toTitleCase(event.kind)}</p>
                  <span className="text-xs text-[var(--muted-foreground)]">{formatRelativeTime(event.ts)}</span>
                </div>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                  {event.identifier ? `${event.identifier} · ` : ''}
                  {event.error || event.title || 'Runtime signal'}
                </p>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
