import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { api } from '@/lib/api'
import type { SessionFeedEntry } from '@/lib/types'
import { formatDateTime, formatNumber, formatRelativeTime, toTitleCase } from '@/lib/utils'

const quietThresholdMs = 10_000
const historyLimit = 8

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
  return entry.last_message || entry.error || entry.last_event || 'No progress details yet.'
}

function badgeClassForStatus(status: SessionFeedEntry['status']) {
  switch (status) {
    case 'active':
      return 'border-lime-400/20 bg-lime-400/10 text-lime-100'
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

function EntryDetails({ entry }: { entry: SessionFeedEntry }) {
  const history = entry.history?.slice(-historyLimit) ?? []
  const context = [
    entry.phase ? toTitleCase(entry.phase) : '',
    entry.attempt ? `Attempt ${entry.attempt}` : '',
    entry.run_kind ? toTitleCase(entry.run_kind.replaceAll('_', ' ')) : '',
    entry.terminal_reason ? toTitleCase(entry.terminal_reason.replaceAll(/[._]/g, ' ')) : '',
  ]
    .filter(Boolean)
    .join(' · ')

  return (
    <div className="mt-4 grid gap-4 border-t border-white/8 pt-4">
      <div className="rounded-2xl border border-white/8 bg-white/[0.03] p-4">
        <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Run context</p>
        <p className="mt-2 text-sm text-white">{context || 'No additional run context recorded.'}</p>
        {entry.failure_class || entry.error ? (
          <p className="mt-2 text-sm text-[var(--muted-foreground)]">
            {[entry.failure_class ? toTitleCase(entry.failure_class.replaceAll('_', ' ')) : '', entry.error ?? '']
              .filter(Boolean)
              .join(' · ')}
          </p>
        ) : null}
      </div>

      <div className="rounded-2xl border border-white/8 bg-white/[0.03] p-4">
        <div className="flex items-center justify-between gap-3">
          <p className="text-sm font-medium text-white">Recent session history</p>
          <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{history.length} events</span>
        </div>
        <div className="mt-4 space-y-3">
          {history.length === 0 ? (
            <p className="text-sm text-[var(--muted-foreground)]">No session history captured for this run.</p>
          ) : (
            history.map((event, index) => (
              <div key={`${event.type}-${event.turn_id || index}`} className="rounded-2xl border border-white/8 bg-black/20 p-3">
                <div className="flex items-center justify-between gap-3">
                  <p className="text-sm font-medium text-white">{event.type}</p>
                  <span className="text-xs text-[var(--muted-foreground)]">{formatNumber(event.total_tokens)} tokens</span>
                </div>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">{event.message || 'No message'}</p>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  )
}

export function SessionsPage() {
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
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
    <div className="grid gap-5">
      <div>
        <h3 className="font-display text-4xl font-semibold">Threads, turns, and event traces</h3>
      </div>
      <div className="grid gap-5 xl:grid-cols-[1.2fr_.8fr]">
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Run transparency</CardTitle>
              <CardDescription>Live sessions first, followed by recent persisted runs so operators can see movement instead of guessing.</CardDescription>
            </div>
          </CardHeader>
          <CardContent className="grid gap-3">
            {entries.length === 0 ? (
              <p className="text-sm text-[var(--muted-foreground)]">No live or recent runs are available.</p>
            ) : (
              entries.map((entry) => {
                const expandedKey = entry.issue_identifier
                const detailsOpen = expanded[expandedKey] ?? false
                const quiet = isQuiet(entry)

                return (
                  <div key={`${entry.source}-${entry.issue_identifier}`} className="rounded-[1.75rem] border border-white/8 bg-black/20 p-5">
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div>
                        <div className="flex flex-wrap items-center gap-2">
                          <p className="font-medium text-white">{entry.issue_identifier}</p>
                          <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(entry.source)}</Badge>
                          <Badge className={badgeClassForStatus(entry.status)}>{toTitleCase(entry.status)}</Badge>
                          {quiet ? <Badge className="border-orange-400/20 bg-orange-400/10 text-orange-100">Quiet</Badge> : null}
                        </div>
                        <p className="mt-2 text-sm text-[var(--muted-foreground)]">{summaryText(entry)}</p>
                        <p className="mt-2 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                          Updated {formatRelativeTime(entry.updated_at)} · {formatDateTime(entry.updated_at)}
                        </p>
                      </div>
                      <button
                        type="button"
                        className="rounded-full border border-white/10 px-3 py-1.5 text-sm text-white transition hover:border-white/20 hover:bg-white/5"
                        aria-expanded={detailsOpen}
                        onClick={() =>
                          setExpanded((current) => ({
                            ...current,
                            [expandedKey]: !detailsOpen,
                          }))
                        }
                      >
                        {detailsOpen ? 'Hide details' : 'Show details'}
                      </button>
                    </div>

                    <div className="mt-4 grid grid-cols-3 gap-3 text-sm">
                      <div className="rounded-2xl border border-white/8 bg-white/4 p-3 text-[var(--muted-foreground)]">
                        <p className="text-xs uppercase tracking-[0.18em]">Tokens</p>
                        <p className="mt-2 text-xl text-white">{formatNumber(entry.total_tokens)}</p>
                      </div>
                      <div className="rounded-2xl border border-white/8 bg-white/4 p-3 text-[var(--muted-foreground)]">
                        <p className="text-xs uppercase tracking-[0.18em]">Turns</p>
                        <p className="mt-2 text-xl text-white">{formatNumber(entry.turns_started)}</p>
                        <p className="mt-1 text-xs">Completed {formatNumber(entry.turns_completed)}</p>
                      </div>
                      <div className="rounded-2xl border border-white/8 bg-white/4 p-3 text-[var(--muted-foreground)]">
                        <p className="text-xs uppercase tracking-[0.18em]">Events</p>
                        <p className="mt-2 text-xl text-white">{formatNumber(entry.events_processed)}</p>
                      </div>
                    </div>

                    {detailsOpen ? <EntryDetails entry={entry} /> : null}
                  </div>
                )
              })
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div>
              <CardTitle>Recent runtime events</CardTitle>
              <CardDescription>Global orchestration context for the current control-plane state.</CardDescription>
            </div>
          </CardHeader>
          <CardContent className="space-y-3">
            {events.data.events.slice(-12).reverse().map((event) => (
              <div key={event.seq} className="rounded-2xl border border-white/8 bg-black/20 p-4">
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
