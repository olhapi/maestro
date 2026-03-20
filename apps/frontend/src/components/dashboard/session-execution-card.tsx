import { AlertTriangle, ChevronDown } from 'lucide-react'

import { MarkdownText } from '@/components/ui/markdown'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { SessionActivityTranscript } from '@/components/dashboard/session-activity-transcript'
import { describeFailureRuns, failureStatusLabel } from '@/lib/execution'
import type { IssueExecutionDetail } from '@/lib/types'
import { formatCompactNumber, formatDateTime, formatNumber, formatRelativeTime, toTitleCase } from '@/lib/utils'

export function SessionExecutionCard({
  execution,
  issueTotalTokens,
  title = 'Execution triage',
  pausedActionHint = 'Use Retry now after checking the workspace or runtime conditions.',
  onApprovePlan,
  approvingPlan = false,
}: {
  execution: IssueExecutionDetail
  issueTotalTokens: number
  title?: string
  pausedActionHint?: string
  onApprovePlan?: () => void
  approvingPlan?: boolean
}) {
  const session = execution.session
  const activityGroups = execution.activity_groups ?? []
  const debugActivityGroups = execution.debug_activity_groups ?? []
  const debugEntries = debugActivityGroups.flatMap((group) => group.entries)
  const runtimeEvents = execution.runtime_events
  const failureLabel = failureStatusLabel(execution.failure_class)
  const failureSummaryReason =
    execution.pause_reason || execution.failure_class || execution.current_error
  const pendingInterrupt = execution.pending_interrupt
  const pendingPlanApproval = execution.plan_approval
  const pausedRunSummary = describeFailureRuns(
    execution.consecutive_failures,
    failureSummaryReason,
  )
  const sessionStatusLabel = pendingInterrupt
    ? 'Waiting for input'
    : execution.retry_state === 'paused'
      ? 'Paused'
      : failureLabel
        ? failureLabel
        : execution.active
          ? 'Active session'
          : 'Idle'
  const debugSignalCount = debugEntries.length + execution.runtime_events.length

  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3.5">
        <div className="flex flex-wrap gap-2">
          <Badge className="border-white/10 bg-white/5 text-white">{sessionStatusLabel}</Badge>
          <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(execution.retry_state)}</Badge>
          <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(execution.session_source)}</Badge>
          <Badge className="border-white/10 bg-white/5 text-white">Attempt {execution.attempt_number || 0}</Badge>
          <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(execution.phase || 'implementation')}</Badge>
          {pendingInterrupt ? (
            <Badge className="border-amber-400/20 bg-amber-400/10 text-amber-100">Waiting</Badge>
          ) : null}
          {pendingInterrupt?.collaboration_mode === 'plan' ? (
            <Badge className="border-sky-400/20 bg-sky-400/10 text-sky-100">Plan turn</Badge>
          ) : null}
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
                  Maestro stopped retrying after {pausedRunSummary}.
                  {execution.pause_reason ? ` Last reason: ${execution.pause_reason}.` : ''}
                </p>
                <p className="mt-2 text-amber-100/80">{pausedActionHint}</p>
              </div>
            </div>
          </div>
        ) : null}

        {pendingInterrupt ? (
          <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-amber-400/25 bg-amber-400/10 p-3.5 text-sm text-amber-50">
            <div className="flex items-start gap-3">
              <AlertTriangle className="mt-0.5 size-4 text-amber-200" />
              <div>
                <p className="font-medium text-amber-100">Waiting for operator input</p>
                <p className="mt-2 text-amber-50/90">
                  {pendingInterrupt.last_activity || 'This run is blocked on a response in the global interrupt queue.'}
                </p>
                <p className="mt-2 text-amber-100/80">
                  Respond from the global interrupt panel to let this thread continue on the same session.
                </p>
              </div>
            </div>
          </div>
        ) : null}

        {pendingPlanApproval ? (
          <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-sky-400/25 bg-sky-400/10 p-3.5 text-sm text-sky-50">
            <div className="flex items-start gap-3">
              <AlertTriangle className="mt-0.5 size-4 text-sky-200" />
              <div className="min-w-0 flex-1">
                <p className="font-medium text-sky-100">Plan ready for approval</p>
                <p className="mt-2 text-sky-50/90">
                  Maestro paused execution after the planning turn. Approve the plan to switch this issue into normal execution with full access.
                </p>
                <div className="mt-3 rounded-md border border-sky-200/15 bg-black/25 p-3 text-sm leading-6 text-sky-50/92">
                  <MarkdownText content={pendingPlanApproval.markdown} />
                </div>
                {onApprovePlan ? (
                  <Button
                    className="mt-3"
                    disabled={approvingPlan}
                    onClick={onApprovePlan}
                    type="button"
                    variant="secondary"
                  >
                    Approve plan and continue
                  </Button>
                ) : null}
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

        <SessionActivityTranscript groups={activityGroups} />

        <details className="group rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5">
          <summary className="flex cursor-pointer list-none items-center justify-between gap-3 text-sm font-medium text-white">
            <span>Debug signals</span>
            <span className="flex items-center gap-2 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
              {debugSignalCount} tracked
              <ChevronDown className="size-3 transition group-open:rotate-180" />
            </span>
          </summary>
          <div
            className="mt-3.5 max-h-[520px] space-y-2.5 overflow-y-auto pr-1"
            data-testid="debug-signals-scroll"
          >
            {debugEntries.length === 0 && runtimeEvents.length === 0 ? (
              <p className="text-sm text-[var(--muted-foreground)]">No persisted runtime events for this issue yet.</p>
            ) : (
              <>
                {debugActivityGroups.length > 0 ? (
                  <div className="space-y-2.5">
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                      Secondary Codex items
                    </p>
                    {debugActivityGroups.map((group) => (
                      <div key={`debug-attempt-${group.attempt}`} className="space-y-2.5">
                        <p className="text-xs text-[var(--muted-foreground)]">
                          Attempt {group.attempt}
                          {group.phase ? ` · ${toTitleCase(group.phase)}` : ''}
                          {group.status ? ` · ${toTitleCase(group.status)}` : ''}
                        </p>
                        {group.entries.map((event) => (
                          <div
                            key={event.id}
                            className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-white/8 bg-white/[0.03] p-3"
                          >
                            <div className="flex items-center justify-between gap-3">
                              <p className="text-sm font-medium text-white">{event.title}</p>
                              <span className="text-xs text-[var(--muted-foreground)]">
                                {event.item_type || event.kind}
                              </span>
                            </div>
                            <p className="mt-2 whitespace-pre-wrap break-words text-sm text-[var(--muted-foreground)]">
                              {event.summary || 'Execution signal'}
                            </p>
                            {event.detail ? (
                              <pre className="mt-3 overflow-x-auto whitespace-pre-wrap break-words rounded-md border border-white/10 bg-black/30 p-2.5 text-xs text-white/88">
                                {event.detail}
                              </pre>
                            ) : null}
                          </div>
                        ))}
                      </div>
                    ))}
                  </div>
                ) : null}

                {runtimeEvents.length > 0 ? (
                  <div className="space-y-2.5">
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                      Runtime events
                    </p>
                    {runtimeEvents.map((event) => (
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
                    ))}
                  </div>
                ) : null}
              </>
            )}
          </div>
        </details>
      </CardContent>
    </Card>
  )
}
