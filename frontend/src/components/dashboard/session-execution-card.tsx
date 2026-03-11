import { useState } from 'react'
import { AlertTriangle, ChevronDown, ChevronUp } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import type { IssueExecutionDetail } from '@/lib/types'
import { formatCompactNumber, formatDateTime, formatNumber, formatRelativeTime, toTitleCase } from '@/lib/utils'

export function SessionExecutionCard({
  execution,
  issueTotalTokens,
  title = 'Execution triage',
  pausedActionHint = 'Use Retry now after checking the workspace or runtime conditions.',
}: {
  execution: IssueExecutionDetail
  issueTotalTokens: number
  title?: string
  pausedActionHint?: string
}) {
  const session = execution.session
  const sessionHistory = execution.session_display_history?.slice(-8) ?? []
  const runtimeEvents = execution.runtime_events.slice(-8)
  const [expandedRows, setExpandedRows] = useState<Record<string, boolean>>({})
  const sessionStatusLabel = execution.retry_state === 'paused'
    ? 'Paused'
    : execution.failure_class === 'run_interrupted'
      ? 'Interrupted'
      : execution.active
        ? 'Active session'
        : 'Idle'
  const sessionHeadline = execution.retry_state === 'paused'
    ? 'Automatic retries paused'
    : execution.failure_class === 'run_interrupted'
      ? 'Last run interrupted'
      : session?.last_event || 'No app-server session recorded'
  const sessionMessage = (() => {
    if (execution.retry_state === 'paused') {
      return `Retry loop paused after ${execution.consecutive_failures ?? 0} interrupted runs.`
    }
    if (execution.session_source === 'persisted' && session?.last_timestamp) {
      return `Last session update ${formatRelativeTime(session.last_timestamp)}`
    }
    if (execution.failure_class === 'run_interrupted') {
      return 'The last known execution ended without a live completion signal.'
    }
    return session?.last_message || 'No message'
  })()
  const toggleHistoryRow = (rowKey: string) => {
    setExpandedRows((current) => ({ ...current, [rowKey]: !current[rowKey] }))
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3.5">
        <div className="flex flex-wrap gap-2">
          <Badge className="border-white/10 bg-white/5 text-white">{sessionStatusLabel}</Badge>
          <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(execution.retry_state)}</Badge>
          <Badge className="border-white/10 bg-white/5 text-white">Attempt {execution.attempt_number || 0}</Badge>
          <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(execution.phase || 'implementation')}</Badge>
          {execution.failure_class ? (
            <Badge className="border-rose-400/20 bg-rose-400/10 text-rose-100">{toTitleCase(execution.failure_class)}</Badge>
          ) : null}
          {execution.next_retry_at ? (
            <Badge className="border-amber-400/20 bg-amber-400/10 text-amber-100">
              Retry {formatRelativeTime(execution.next_retry_at)}
            </Badge>
          ) : null}
        </div>

        {execution.retry_state === 'paused' ? (
          <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-amber-400/25 bg-amber-400/10 p-3.5 text-sm text-amber-50">
            <div className="flex items-start gap-3">
              <AlertTriangle className="mt-0.5 size-4 text-amber-200" />
              <div>
                <p className="font-medium text-amber-100">Automatic retries paused</p>
                <p className="mt-2 text-amber-50/90">
                  Maestro stopped retrying after {execution.consecutive_failures ?? 0} interrupted runs.
                  {execution.pause_reason ? ` Last reason: ${execution.pause_reason}.` : ''}
                </p>
                <p className="mt-2 text-amber-100/80">{pausedActionHint}</p>
              </div>
            </div>
          </div>
        ) : null}

        {execution.current_error ? (
          <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-rose-400/15 bg-rose-400/10 p-3.5 text-sm text-rose-100">
            <p className="text-xs uppercase tracking-[0.18em] text-rose-200/80">Current error</p>
            <p className="mt-2 whitespace-pre-wrap break-words">{execution.current_error}</p>
          </div>
        ) : null}

        <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="text-sm text-[var(--muted-foreground)]">Session snapshot</p>
              <p className="mt-2 font-medium text-white [overflow-wrap:anywhere] break-all">{sessionHeadline}</p>
              <p className="mt-2 text-sm text-[var(--muted-foreground)] [overflow-wrap:anywhere] break-all">{sessionMessage}</p>
            </div>
            <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(execution.session_source)}</Badge>
          </div>
        </div>

        <div className="grid gap-2.5 md:grid-cols-3">
          <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5">
            <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Turns</p>
            <p className="mt-2 font-display text-[calc(var(--metric-value-size)-0.25rem)] leading-none text-white">{session?.turns_started ?? 0}</p>
            <p className="mt-2 text-sm text-[var(--muted-foreground)]">Completed: {formatNumber(session?.turns_completed)}</p>
          </div>
          <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5">
            <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Session tokens</p>
            <p className="mt-2 font-display text-[calc(var(--metric-value-size)-0.25rem)] leading-none text-white">{formatCompactNumber(session?.total_tokens)}</p>
            <p className="mt-2 text-sm text-[var(--muted-foreground)]">Updated: {session ? formatDateTime(session.last_timestamp) : 'n/a'}</p>
          </div>
          <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5">
            <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Issue total</p>
            <p className="mt-2 font-display text-[calc(var(--metric-value-size)-0.25rem)] leading-none text-white">{formatCompactNumber(issueTotalTokens)}</p>
            <p className="mt-2 text-sm text-[var(--muted-foreground)]">Lifetime tokens across all runs</p>
          </div>
        </div>

        <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5">
          <div className="flex items-center justify-between gap-3">
            <p className="text-sm font-medium text-white">Recent session history</p>
            <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{sessionHistory.length} events</span>
          </div>
          <div className="mt-3.5 space-y-2.5">
            {sessionHistory.length === 0 ? (
              <p className="text-sm text-[var(--muted-foreground)]">No session history captured for this issue yet.</p>
            ) : (
              sessionHistory.map((event, index) => {
                const rowKey = `${event.id}-${index}`
                return (
                  <div
                    key={rowKey}
                    className={`rounded-[calc(var(--panel-radius)-0.25rem)] border p-3 ${
                      event.tone === 'error'
                        ? 'border-rose-400/20 bg-rose-400/10'
                        : event.tone === 'success'
                          ? 'border-emerald-400/20 bg-emerald-400/10'
                          : 'border-white/8 bg-white/[0.03]'
                    }`}
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <p className="text-sm font-medium text-white">{event.title}</p>
                        <p className="mt-2 text-sm text-[var(--muted-foreground)]">{event.summary || 'Execution signal'}</p>
                      </div>
                      <div className="flex items-center gap-2">
                        {event.token_count && event.token_count > 0 ? (
                          <span className="text-xs text-[var(--muted-foreground)]">{formatCompactNumber(event.token_count)} tokens</span>
                        ) : null}
                        {event.expandable ? (
                          <button
                            className="inline-flex items-center gap-1 rounded-md border border-white/10 bg-white/[0.04] px-2 py-1 text-xs text-[var(--muted-foreground)] transition hover:bg-white/[0.08] hover:text-white"
                            onClick={() => toggleHistoryRow(rowKey)}
                            type="button"
                          >
                            {expandedRows[rowKey] ? 'Collapse' : 'Expand'}
                            {expandedRows[rowKey] ? <ChevronUp className="size-3" /> : <ChevronDown className="size-3" />}
                          </button>
                        ) : null}
                      </div>
                    </div>
                    {event.detail && expandedRows[rowKey] ? (
                      <pre className="mt-3 overflow-x-auto whitespace-pre-wrap break-words rounded-md border border-white/10 bg-black/30 p-2.5 text-xs text-white/90">
                        {event.detail}
                      </pre>
                    ) : null}
                  </div>
                )
              })
            )}
          </div>
        </div>

        <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5">
          <div className="flex items-center justify-between gap-3">
            <p className="text-sm font-medium text-white">Runtime events</p>
            <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{execution.runtime_events.length} tracked</span>
          </div>
          <div className="mt-3.5 space-y-2.5">
            {runtimeEvents.length === 0 ? (
              <p className="text-sm text-[var(--muted-foreground)]">No persisted runtime events for this issue yet.</p>
            ) : (
              runtimeEvents.map((event) => (
                <div key={event.seq} className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/8 bg-white/[0.03] p-3">
                  <div className="flex items-center justify-between gap-3">
                    <p className="text-sm font-medium text-white">{toTitleCase(event.kind)}</p>
                    <span className="text-xs text-[var(--muted-foreground)]">{formatDateTime(event.ts)}</span>
                  </div>
                  <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                    {[
                      event.phase && toTitleCase(event.phase),
                      event.attempt ? `Attempt ${event.attempt}` : '',
                      event.delay_type && toTitleCase(event.delay_type),
                    ]
                      .filter(Boolean)
                      .join(' · ') || 'Execution signal'}
                  </p>
                  {event.error ? <p className="mt-2 text-sm text-rose-100">{event.error}</p> : null}
                </div>
              ))
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
