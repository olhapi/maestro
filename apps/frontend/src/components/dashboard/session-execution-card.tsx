import { AlertTriangle, ChevronDown } from 'lucide-react'
import { useState } from 'react'

import { PlanApprovalActionBar, PlanApprovalDocument } from '@/components/dashboard/plan-approval-review'
import { MarkdownText, wrappedOutputClassName } from '@/components/ui/markdown'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { SessionActivityTranscript } from '@/components/dashboard/session-activity-transcript'
import { describeFailureRuns, failureStatusLabel } from '@/lib/execution'
import type { IssueExecutionDetail } from '@/lib/types'
import { formatCompactNumber, formatDateTime, formatNumber, formatRelativeTime, toTitleCase } from '@/lib/utils'

function planningHeadline(status: string | undefined) {
  switch (status) {
    case 'drafting':
      return 'Revising the plan'
    case 'revision_requested':
      return 'Plan revision queued'
    case 'approved':
      return 'Plan approved'
    case 'abandoned':
      return 'Planning session abandoned'
    case 'awaiting_approval':
    default:
      return 'Plan ready for approval'
  }
}

function planningSummary(status: string | undefined) {
  switch (status) {
    case 'drafting':
      return 'Maestro started the next planning turn with your latest revision note. The last published draft stays visible until the revised plan is ready.'
    case 'revision_requested':
      return 'Maestro queued your revision note and will carry it into the next planning turn before it asks for approval again.'
    case 'approved':
      return 'The planning session is complete. Maestro can continue the implementation flow from this approved draft.'
    case 'abandoned':
      return 'The pending plan was cleared without approval. The latest published draft is still available here for reference.'
    case 'awaiting_approval':
    default:
      return 'Maestro paused after drafting the plan. Review the proposal, request changes if the plan needs another pass, or approve when it is ready to continue.'
  }
}

function continuationHeadline(reason: string | undefined) {
  switch (reason) {
    case 'retry_limit_reached':
      return 'Continue after retry limit'
    case 'no_state_transition':
    default:
      return 'Continue this issue'
  }
}

function continuationSummary(reason: string | undefined) {
  switch (reason) {
    case 'retry_limit_reached':
      return 'The last turn finished cleanly, but Maestro paused before the next continuation turn after reaching the automatic retry limit.'
    case 'no_state_transition':
    default:
      return 'The last turn finished cleanly, but Maestro needs a manual continuation because the issue stayed in the same phase and state.'
  }
}

export function SessionExecutionCard({
  execution,
  issueTotalTokens,
  title = 'Execution triage',
  pausedActionHint = 'Use Retry now after checking the workspace or runtime conditions.',
  onApprovePlan,
  onContinue,
  onRequestPlanRevision,
  approvingPlan = false,
  continuing = false,
}: {
  execution: IssueExecutionDetail
  issueTotalTokens: number
  title?: string
  pausedActionHint?: string
  onApprovePlan?: (note?: string) => void
  onContinue?: () => void
  onRequestPlanRevision?: (note: string) => void
  approvingPlan?: boolean
  continuing?: boolean
}) {
  const session = execution.session
  const activityGroups = execution.activity_groups ?? []
  const debugActivityGroups = execution.debug_activity_groups ?? []
  const debugEntries = debugActivityGroups.flatMap((group) => group.entries)
  const runtimeEvents = execution.runtime_events
  const failureLabel = failureStatusLabel(execution.failure_class)
  const workspaceRecovery = execution.workspace_recovery
  const failureSummaryReason =
    execution.pause_reason || execution.failure_class || execution.current_error
  const pendingInterrupt = execution.pending_interrupt
  const pendingAlert = pendingInterrupt?.kind === 'alert' ? pendingInterrupt : null
  const planning = execution.planning
  const planningVersions = planning?.versions ?? []
  const currentPlanVersion = planning?.current_version ?? (planningVersions.length > 0 ? planningVersions[planningVersions.length - 1] : undefined)
  const pendingPlanApproval = execution.plan_approval
  const pendingPlanRevision = execution.plan_revision
  const continueAvailable = execution.continue_available === true
  const activePlanApprovalMarkdown =
    pendingInterrupt?.approval?.markdown?.trim() ||
    pendingPlanApproval?.markdown?.trim() ||
    ''
  const displayedPlanMarkdown =
    currentPlanVersion?.markdown?.trim() ||
    activePlanApprovalMarkdown ||
    ''
  const pendingPlanRevisionMarkdown =
    planning?.pending_revision_note?.trim() ||
    pendingInterrupt?.approval?.plan_revision_note?.trim() ||
    pendingPlanRevision?.markdown?.trim() ||
    ''
  const hasPlanningSession =
    !!planning ||
    displayedPlanMarkdown.length > 0 ||
    pendingPlanRevisionMarkdown.length > 0
  const hasLegacyQueuedRevision = !planning && pendingPlanRevisionMarkdown.length > 0
  const effectivePlanningStatus =
    planning?.status ||
    (hasLegacyQueuedRevision ? 'revision_requested' : activePlanApprovalMarkdown.length > 0 ? 'awaiting_approval' : undefined)
  const isPlanDrafting = effectivePlanningStatus === 'drafting'
  const isPlanAwaitingApproval = effectivePlanningStatus === 'awaiting_approval'
  const isPlanRevisionPending = effectivePlanningStatus === 'revision_requested'
  const isPlanApproved = effectivePlanningStatus === 'approved'
  const isPlanAbandoned = effectivePlanningStatus === 'abandoned'
  const hasOpenPlanningSession =
    isPlanAwaitingApproval ||
    isPlanDrafting ||
    isPlanRevisionPending
  const planApprovalActionsVisible =
    !isPlanDrafting &&
    !isPlanRevisionPending &&
    !isPlanApproved &&
    !isPlanAbandoned &&
    activePlanApprovalMarkdown.length > 0 &&
    (!!onApprovePlan || !!onRequestPlanRevision)
  const planApprovalDraftKey = hasPlanningSession
    ? `${planning?.session_id ?? pendingInterrupt?.session_id ?? ''}|${planning?.current_version_number ?? pendingPlanApproval?.attempt ?? 0}|${activePlanApprovalMarkdown}`
    : ''
  const [planReviewState, setPlanReviewState] = useState({
    draftKey: '',
    note: '',
    noteVisible: false,
    noteRequired: false,
  })
  const planReviewMatchesDraft = isPlanAwaitingApproval && planReviewState.draftKey === planApprovalDraftKey
  const planReviewNote = planReviewMatchesDraft ? planReviewState.note : ''
  const planReviewNoteVisible = planReviewMatchesDraft ? planReviewState.noteVisible : false
  const planReviewNoteRequired = planReviewMatchesDraft ? planReviewState.noteRequired : false
  const trimmedPlanReviewNote = planReviewNote.trim()
  const pausedRunSummary = describeFailureRuns(
    execution.consecutive_failures,
    failureSummaryReason,
  )
  const continueActionsVisible =
    continueAvailable &&
    execution.retry_state === 'paused' &&
    !hasOpenPlanningSession &&
    !pendingInterrupt
  const planningBadgeLabel =
    effectivePlanningStatus === 'drafting'
      ? 'Drafting'
      : effectivePlanningStatus === 'revision_requested'
        ? 'Revision queued'
        : effectivePlanningStatus === 'approved'
          ? 'Approved'
          : effectivePlanningStatus === 'abandoned'
            ? 'Abandoned'
            : hasPlanningSession
              ? 'Awaiting approval'
              : ''
  const sessionStatusLabel = pendingAlert
      ? 'Blocked'
      : isPlanDrafting
        ? 'Drafting'
      : isPlanRevisionPending
        ? 'Revision queued'
      : pendingInterrupt
      ? pendingAlert
        ? 'Blocked'
        : isPlanAwaitingApproval
          ? 'Waiting for plan approval'
          : 'Waiting for input'
      : isPlanRevisionPending
        ? 'Revision queued'
      : execution.retry_state === 'paused'
        ? isPlanAwaitingApproval
          ? 'Waiting for plan approval'
          : 'Paused'
      : failureLabel
          ? failureLabel
          : execution.active
            ? 'Active session'
            : 'Idle'
  const debugSignalCount = debugEntries.length + execution.runtime_events.length
  const workspaceRecoveryTitle = workspaceRecovery
    ? workspaceRecovery.status === 'required'
      ? 'Workspace recovery required'
      : 'Workspace recovery in progress'
    : null
  const workspaceRecoveryTone =
    workspaceRecovery?.status === 'required'
      ? 'border-amber-400/25 bg-amber-400/10 text-amber-50'
      : 'border-sky-400/25 bg-sky-400/10 text-sky-50'
  const workspaceRecoveryAccent =
    workspaceRecovery?.status === 'required' ? 'text-amber-100' : 'text-sky-100'
  const workspaceRecoveryBody =
    workspaceRecovery?.status === 'required' ? 'text-amber-50/90' : 'text-sky-50/90'

  const handleApprovePlan = () => {
    onApprovePlan?.(trimmedPlanReviewNote || undefined)
  }

  const handleRequestPlanChanges = () => {
    if (!onRequestPlanRevision) {
      return
    }
    if (!trimmedPlanReviewNote) {
      setPlanReviewState({
        draftKey: planApprovalDraftKey,
        note: planReviewNote,
        noteVisible: true,
        noteRequired: true,
      })
      return
    }
    setPlanReviewState((current) => ({
      draftKey: planApprovalDraftKey,
      note: current.draftKey === planApprovalDraftKey ? current.note : planReviewNote,
      noteVisible: true,
      noteRequired: false,
    }))
    onRequestPlanRevision(trimmedPlanReviewNote)
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
          <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(execution.session_source)}</Badge>
          <Badge className="border-white/10 bg-white/5 text-white">Attempt {execution.attempt_number || 0}</Badge>
          <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(execution.phase || 'implementation')}</Badge>
          {pendingAlert ? (
            <Badge
              className="border-rose-400/20 bg-rose-400/10 text-rose-100"
            >
              Blocked
            </Badge>
          ) : pendingInterrupt || isPlanAwaitingApproval ? (
            <Badge className="border-sky-400/20 bg-sky-400/10 text-sky-100">Waiting</Badge>
          ) : null}
          {pendingInterrupt?.collaboration_mode === 'plan' || hasOpenPlanningSession ? (
            <Badge className="border-sky-400/20 bg-sky-400/10 text-sky-100">Plan turn</Badge>
          ) : null}
          {planningBadgeLabel ? (
            <Badge className={
              effectivePlanningStatus === 'approved'
                ? 'border-lime-400/20 bg-lime-400/10 text-lime-100'
                : effectivePlanningStatus === 'abandoned'
                  ? 'border-rose-400/20 bg-rose-400/10 text-rose-100'
                  : effectivePlanningStatus === 'drafting' || effectivePlanningStatus === 'revision_requested'
                    ? 'border-amber-400/20 bg-amber-400/10 text-amber-100'
                    : 'border-sky-400/20 bg-sky-400/10 text-sky-100'
            }>
              {planningBadgeLabel}
            </Badge>
          ) : null}
          {execution.failure_class && !hasOpenPlanningSession ? (
            <Badge className="border-rose-400/20 bg-rose-400/10 text-rose-100">{toTitleCase(execution.failure_class)}</Badge>
          ) : null}
          {execution.next_retry_at ? (
            <Badge className="border-amber-400/20 bg-amber-400/10 text-amber-100">
              Retry {formatRelativeTime(execution.next_retry_at)}
            </Badge>
          ) : null}
        </div>

        {continueActionsVisible ? (
          <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-sky-400/25 bg-[linear-gradient(180deg,rgba(83,217,255,0.12),rgba(255,255,255,0.03))] p-3.5 text-sm text-sky-50">
            <div className="flex items-start justify-between gap-4">
              <div className="flex items-start gap-3">
                <AlertTriangle className="mt-0.5 size-4 text-sky-200" />
                <div>
                  <p className="font-medium text-sky-100">{continuationHeadline(execution.pause_reason)}</p>
                  <p className="mt-2 text-sky-50/90">
                    {continuationSummary(execution.pause_reason)}
                  </p>
                  <p className="mt-2 text-sky-100/80">
                    Continue to queue the next turn for this issue.
                  </p>
                </div>
              </div>
              <Button
                className="shrink-0"
                disabled={continuing || !onContinue}
                type="button"
                onClick={() => {
                  onContinue?.()
                }}
              >
                {continuing ? 'Continuing...' : 'Continue'}
              </Button>
            </div>
          </div>
        ) : null}

        {execution.retry_state === 'paused' && !hasOpenPlanningSession && !continueActionsVisible ? (
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

        {hasPlanningSession && (displayedPlanMarkdown.length > 0 || pendingPlanRevisionMarkdown.length > 0) ? (
          <div
            className={
              isPlanRevisionPending || isPlanDrafting
                ? 'rounded-[calc(var(--panel-radius)-0.125rem)] border border-amber-400/25 bg-[linear-gradient(180deg,rgba(251,191,36,0.12),rgba(255,255,255,0.03))] p-4 text-sm text-amber-50'
                : isPlanApproved
                  ? 'rounded-[calc(var(--panel-radius)-0.125rem)] border border-lime-400/22 bg-[linear-gradient(180deg,rgba(132,204,22,0.12),rgba(255,255,255,0.03))] p-4 text-sm text-lime-50'
                  : isPlanAbandoned
                    ? 'rounded-[calc(var(--panel-radius)-0.125rem)] border border-rose-400/22 bg-[linear-gradient(180deg,rgba(244,63,94,0.12),rgba(255,255,255,0.03))] p-4 text-sm text-rose-50'
                    : 'rounded-[calc(var(--panel-radius)-0.125rem)] border border-sky-400/22 bg-[linear-gradient(180deg,rgba(83,217,255,0.08),rgba(255,255,255,0.03))] p-4 text-sm text-sky-50'
            }
          >
            <div className="flex items-start gap-3">
              <AlertTriangle
                className={`mt-0.5 size-4 ${
                  isPlanRevisionPending || isPlanDrafting
                    ? 'text-amber-200'
                    : isPlanApproved
                      ? 'text-lime-200'
                      : isPlanAbandoned
                        ? 'text-rose-200'
                        : 'text-sky-200'
                }`}
              />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge
                    className={
                      isPlanRevisionPending || isPlanDrafting
                        ? 'border-amber-400/25 bg-amber-400/10 text-amber-100'
                        : isPlanApproved
                          ? 'border-lime-400/25 bg-lime-400/10 text-lime-100'
                          : isPlanAbandoned
                            ? 'border-rose-400/25 bg-rose-400/10 text-rose-100'
                            : 'border-sky-400/25 bg-sky-400/10 text-sky-100'
                    }
                  >
                    {planningBadgeLabel || 'Plan approval'}
                  </Badge>
                  <span
                    className={
                      isPlanRevisionPending || isPlanDrafting
                        ? 'text-sm text-amber-100/70'
                        : isPlanApproved
                          ? 'text-sm text-lime-100/70'
                          : isPlanAbandoned
                            ? 'text-sm text-rose-100/70'
                            : 'text-sm text-sky-100/70'
                    }
                  >
                    Planning session
                  </span>
                </div>
                <p className="mt-3 text-lg font-semibold text-white">
                  {planningHeadline(effectivePlanningStatus)}
                </p>
                <p
                  className={`mt-2 max-w-3xl text-sm leading-6 ${
                    isPlanRevisionPending || isPlanDrafting
                      ? 'text-amber-50/80'
                      : isPlanApproved
                        ? 'text-lime-50/80'
                        : isPlanAbandoned
                          ? 'text-rose-50/80'
                          : 'text-sky-50/80'
                  }`}
                >
                  {planningSummary(effectivePlanningStatus)}
                </p>
                {pendingPlanRevisionMarkdown && (isPlanRevisionPending || isPlanDrafting) ? (
                  <div className="mt-4 rounded-md border border-amber-200/15 bg-black/25 p-3 text-sm leading-6 text-amber-50/92">
                    <p className="text-xs uppercase tracking-[0.16em] text-amber-100/70">
                      {isPlanRevisionPending ? 'Revision note queued' : 'Revision note'}
                    </p>
                    <div className="mt-2">
                      <MarkdownText content={pendingPlanRevisionMarkdown} />
                    </div>
                  </div>
                ) : null}
                {displayedPlanMarkdown ? (
                  <div className="mt-4 max-w-[58rem]">
                    <PlanApprovalDocument markdown={displayedPlanMarkdown} />
                  </div>
                ) : null}
                {planningVersions.length > 0 ? (
                  <div className="mt-4 grid gap-2">
                    <p className="text-xs uppercase tracking-[0.16em] text-[var(--muted-foreground)]">Revision history</p>
                    <div className="grid gap-2">
                      {[...planningVersions].reverse().map((version) => (
                        <div key={version.id || `${version.session_id}-${version.version_number}`} className="rounded-md border border-white/8 bg-black/20 p-3">
                          <div className="flex flex-wrap items-center gap-2">
                            <Badge className="border-white/10 bg-white/5 text-white">Version {version.version_number}</Badge>
                            {planning?.current_version_number === version.version_number ? (
                              <Badge className="border-sky-400/20 bg-sky-400/10 text-sky-100">Current</Badge>
                            ) : null}
                            <span className="text-xs text-[var(--muted-foreground)]">{formatDateTime(version.created_at)}</span>
                          </div>
                          {version.revision_note ? (
                            <p className="mt-2 text-sm leading-6 text-[var(--muted-foreground)]">
                              From revision note: {version.revision_note}
                            </p>
                          ) : (
                            <p className="mt-2 text-sm leading-6 text-[var(--muted-foreground)]">
                              Initial plan draft.
                            </p>
                          )}
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
                {planApprovalActionsVisible ? (
                  <div className="mt-4">
                    <PlanApprovalActionBar
                      approveDisabled={approvingPlan || !onApprovePlan}
                      approveLabel="Approve plan"
                      note={planReviewNote}
                      notePlaceholder="Explain what should change in the plan..."
                      noteRequired={planReviewNoteRequired}
                      noteVisible={planReviewNoteVisible}
                      requestChangesDisabled={approvingPlan || !onRequestPlanRevision}
                      onApprove={handleApprovePlan}
                      onNoteChange={(value) => {
                        setPlanReviewState((current) => ({
                          draftKey: planApprovalDraftKey,
                          note: value,
                          noteVisible: current.draftKey === planApprovalDraftKey ? current.noteVisible : false,
                          noteRequired:
                            current.draftKey === planApprovalDraftKey
                              ? current.noteRequired && value.trim().length === 0
                              : false,
                        }))
                      }}
                      onRequestChanges={handleRequestPlanChanges}
                      onToggleNote={() => {
                        const nextVisible = !planReviewNoteVisible
                        setPlanReviewState((current) => ({
                          draftKey: planApprovalDraftKey,
                          note: current.draftKey === planApprovalDraftKey ? current.note : '',
                          noteVisible: nextVisible,
                          noteRequired: nextVisible && current.draftKey === planApprovalDraftKey ? current.noteRequired : false,
                        }))
                      }}
                    />
                  </div>
                ) : null}
              </div>
            </div>
          </div>
        ) : pendingInterrupt ? (
          <div
            className={
              pendingAlert
                ? 'rounded-[calc(var(--panel-radius)-0.125rem)] border border-rose-400/25 bg-rose-400/10 p-3.5 text-sm text-rose-50'
                : 'rounded-[calc(var(--panel-radius)-0.125rem)] border border-amber-400/25 bg-amber-400/10 p-3.5 text-sm text-amber-50'
            }
          >
            <div className="flex items-start gap-3">
              <AlertTriangle className={pendingAlert ? 'mt-0.5 size-4 text-rose-200' : 'mt-0.5 size-4 text-amber-200'} />
              <div>
                <p className={pendingAlert ? 'font-medium text-rose-100' : 'font-medium text-amber-100'}>
                  {pendingAlert ? pendingAlert.alert?.title || 'Execution blocked' : 'Waiting for operator input'}
                </p>
                <p className={pendingAlert ? 'mt-2 text-rose-50/90' : 'mt-2 text-amber-50/90'}>
                  {pendingAlert
                    ? pendingAlert.alert?.message || pendingInterrupt.last_activity || 'Maestro found a blocker for this issue.'
                    : pendingInterrupt.last_activity || 'This run is blocked on a response in the global interrupt queue.'}
                </p>
                {pendingAlert?.alert?.detail ? (
                  <p className="mt-2 text-white/70">{pendingAlert.alert.detail}</p>
                ) : null}
                <p className={pendingAlert ? 'mt-2 text-rose-100/80' : 'mt-2 text-amber-100/80'}>
                  {pendingAlert
                    ? 'Resolve the underlying blocker from the issue or project context, then re-run once the dispatch path is clear.'
                    : 'Respond from the global interrupt panel to let this thread continue on the same session.'}
                </p>
              </div>
            </div>
          </div>
        ) : null}

        {workspaceRecovery ? (
          <div className={`rounded-[calc(var(--panel-radius)-0.125rem)] border p-3.5 text-sm ${workspaceRecoveryTone}`}>
            <div className="flex items-start gap-3">
              <AlertTriangle className={`mt-0.5 size-4 ${workspaceRecoveryAccent}`} />
              <div className="min-w-0 flex-1">
                <p className={`font-medium ${workspaceRecoveryAccent}`}>{workspaceRecoveryTitle}</p>
                <p className={`mt-2 ${workspaceRecoveryBody}`}>{workspaceRecovery.message}</p>
                <p className={`mt-2 ${workspaceRecovery.status === 'required' ? 'text-amber-100/80' : 'text-sky-100/80'}`}>
                  Retry once the workspace is clean. Maestro will continue through the recovery flow automatically.
                </p>
              </div>
            </div>
          </div>
        ) : null}

        {execution.current_error ? (
          <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-rose-400/15 bg-rose-400/10 p-3.5 text-sm text-rose-100">
            <p className="text-xs uppercase tracking-[0.18em] text-rose-200/80">Current error</p>
            <p className={`${wrappedOutputClassName} mt-2`}>{execution.current_error}</p>
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
                            <p className={`${wrappedOutputClassName} mt-2 text-sm text-[var(--muted-foreground)]`}>
                              {event.summary || 'Execution signal'}
                            </p>
                            {event.detail ? (
                              <pre className={`${wrappedOutputClassName} mt-3 rounded-md border border-white/10 bg-black/30 p-2.5 text-xs text-white/88`}>
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
