import { useMemo, useState } from 'react'
import { X } from 'lucide-react'

import {
  ApprovalReviewPanel,
  PlanApprovalDocument,
  type ApprovalReviewAction,
  type ApprovalReviewOverflowGroup,
} from '@/components/dashboard/plan-approval-review'
import { ElicitationForm } from '@/components/dashboard/elicitation-form'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { CompactRelativeTime } from '@/components/ui/compact-relative-time'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import type {
  PendingAlert,
  PendingApprovalDecision,
  PendingInterrupt,
  PendingUserInputQuestion,
} from '@/lib/types'
import { cn, toTitleCase } from '@/lib/utils'

const EMPTY_QUESTIONS: PendingUserInputQuestion[] = []
const EMPTY_DRAFT_ANSWERS: Record<string, string> = {}

type InterruptResponsePayload = {
  interruptId: string
  decision?: string
  decision_payload?: Record<string, unknown>
  answers?: Record<string, string[]>
  note?: string
  action?: 'accept' | 'decline' | 'cancel'
  content?: unknown
}

type PlanRevisionRequestPayload = {
  issueIdentifier: string
  note: string
}

function answerValue(draft: string) {
  return draft
}

function buildAnswers(questions: PendingUserInputQuestion[], draftAnswers: Record<string, string>) {
  const next: Record<string, string[]> = {}
  for (const question of questions) {
    const value = answerValue(draftAnswers[question.id] ?? '')
    if (value.trim()) {
      next[question.id] = [value]
    }
  }
  return next
}

function questionHasTextInput(question: PendingUserInputQuestion) {
  return !question.options?.length || question.is_other
}

function interruptSummary(interrupt: PendingInterrupt) {
  if (interrupt.last_activity) {
    return interrupt.last_activity
  }
  if (interrupt.kind === 'alert') {
    return interrupt.alert?.message || interrupt.alert?.detail || interrupt.alert?.title || 'Maestro needs attention.'
  }
  if (interrupt.kind === 'elicitation') {
    return interrupt.elicitation?.message || interrupt.elicitation?.url || 'MCP elicitation required.'
  }
  if (interrupt.kind === 'approval') {
    return interrupt.approval?.command || interrupt.approval?.reason || 'Operator approval required.'
  }
  return interrupt.user_input?.questions?.[0]?.question || 'Operator input required.'
}

function approvalPrompt(interrupt: PendingInterrupt) {
  if (interrupt.approval?.markdown?.trim()) {
    return 'Review this proposed plan before the agent continues.'
  }
  if (interrupt.approval?.command) {
    return 'Allow the agent to run this command?'
  }
  if (interrupt.approval?.reason) {
    return 'Approve this request before the agent continues.'
  }
  return 'Review this request before the agent continues.'
}

function interruptDraftKey(interrupt: PendingInterrupt) {
  const elicitationDraftKey = interrupt.elicitation
    ? [
        interrupt.elicitation.mode,
        interrupt.elicitation.message?.trim() ?? '',
        interrupt.elicitation.url?.trim() ?? '',
        JSON.stringify(interrupt.elicitation.requested_schema ?? {}),
      ].join('|')
    : ''
  return [interrupt.requested_at, interrupt.approval?.markdown?.trim() ?? '', elicitationDraftKey].join('|')
}

type ApprovalOverflowIntent = 'allow_more' | 'reject' | 'stop' | 'other'

type NormalizedApprovalDecisions = {
  primary: PendingApprovalDecision | null
  overflowGroups: Array<{
    key: ApprovalOverflowIntent
    label: string
    options: PendingApprovalDecision[]
  }>
}

function approvalDecisionIntent(option: PendingApprovalDecision): ApprovalOverflowIntent | 'primary' {
  const haystack = `${option.value} ${option.label}`.toLowerCase()

  if (haystack.includes('abort') || haystack.includes('cancel')) {
    return 'stop'
  }
  if (
    haystack.includes('persist deny') ||
    haystack.includes('network_policy_deny') ||
    haystack.includes('deny') ||
    haystack.includes('decline')
  ) {
    return 'reject'
  }
  if (
    haystack.includes('session') ||
    haystack.includes('grant') ||
    haystack.includes('store') ||
    haystack.includes('persist allow') ||
    haystack.includes('network_policy_allow') ||
    haystack.includes('amendment')
  ) {
    return 'allow_more'
  }
  return 'primary'
}

function approvalOverflowGroupLabel(intent: ApprovalOverflowIntent) {
  switch (intent) {
    case 'allow_more':
      return 'Allow more broadly'
    case 'reject':
      return 'Reject request'
    case 'stop':
      return 'Stop current turn'
    case 'other':
    default:
      return 'Other approval actions'
  }
}

function approvalDecisionVariant(option: PendingApprovalDecision): ApprovalReviewAction['variant'] {
  const intent = approvalDecisionIntent(option)
  return intent === 'reject' || intent === 'stop' ? 'destructive' : 'secondary'
}

function normalizeApprovalDecisions(decisions: PendingApprovalDecision[]): NormalizedApprovalDecisions {
  const primary = decisions.find((option) => approvalDecisionIntent(option) === 'primary') ?? decisions[0] ?? null
  const overflowBuckets: Record<ApprovalOverflowIntent, PendingApprovalDecision[]> = {
    allow_more: [],
    reject: [],
    stop: [],
    other: [],
  }

  for (const option of decisions) {
    if (option === primary) {
      continue
    }
    const intent = approvalDecisionIntent(option)
    overflowBuckets[intent === 'primary' ? 'other' : intent].push(option)
  }

  return {
    primary,
    overflowGroups: (['allow_more', 'reject', 'stop', 'other'] as ApprovalOverflowIntent[])
      .filter((intent) => overflowBuckets[intent].length > 0)
      .map((intent) => ({
        key: intent,
        label: approvalOverflowGroupLabel(intent),
        options: overflowBuckets[intent],
      })),
  }
}

function ApprovalCommandPreview({ command }: { command: string }) {
  const [expanded, setExpanded] = useState(false)
  const lineCount = command.split(/\r?\n/).length
  const shouldCollapse = command.length > 120 || lineCount > 2
  const preview = shouldCollapse ? `${command.slice(0, 120).trimEnd()}…` : command
  const visibleCommand = expanded || !shouldCollapse ? command : preview

  return (
    <div className="rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/12 bg-[linear-gradient(180deg,rgba(255,255,255,.03),rgba(0,0,0,.18))] px-4 py-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-[11px] uppercase tracking-[0.16em] text-[var(--muted-foreground)]">
          Requested command
        </p>
        {shouldCollapse ? (
          <Button
            className="h-8 rounded-full px-3 text-xs"
            type="button"
            variant="ghost"
            onClick={() => {
              setExpanded((current) => !current)
            }}
          >
            {expanded ? 'Show less' : 'Show full command'}
          </Button>
        ) : null}
      </div>
      <code className="mt-3 block whitespace-pre-wrap break-words [overflow-wrap:anywhere] rounded-[calc(var(--panel-radius)-0.45rem)] border border-white/10 bg-black/35 px-4 py-3 font-mono text-[0.96rem] leading-7 text-white">
        {visibleCommand}
      </code>
    </div>
  )
}

function interruptHeading(interrupt: PendingInterrupt) {
  if (interrupt.kind === 'alert') {
    return interrupt.alert?.title || interrupt.issue_title || interrupt.issue_identifier || interrupt.project_name || 'Maestro alert'
  }
  if (interrupt.kind === 'elicitation') {
    return interrupt.elicitation?.server_name || interrupt.issue_title || interrupt.issue_identifier || interrupt.project_name || 'MCP elicitation'
  }
  return (
    interrupt.issue_title ||
    interrupt.issue_identifier ||
    interrupt.project_name ||
    interrupt.alert?.title ||
    'Running agent'
  )
}

function interruptSubject(interrupt: PendingInterrupt) {
  return interrupt.issue_identifier || interrupt.project_name || 'Maestro'
}

function alertSeverityClasses(alert?: PendingAlert) {
  switch (alert?.severity) {
    case 'info':
      return 'border-sky-400/20 bg-sky-400/10 text-sky-100'
    case 'warning':
      return 'border-amber-400/20 bg-amber-400/10 text-amber-100'
    case 'error':
    default:
      return 'border-rose-400/20 bg-rose-400/10 text-rose-100'
  }
}

function defaultSelectedInterrupt(items: PendingInterrupt[]) {
  return items.find((interrupt) => interrupt.kind !== 'alert') ?? items[0]
}

function defaultRespondableInterruptId(items: PendingInterrupt[]) {
  return items.find((interrupt) => interrupt.kind !== 'alert')?.id ?? null
}

function interruptKindLabel(interrupt: PendingInterrupt) {
  if (interrupt.kind === 'alert') {
    return 'Maestro alert'
  }
  if (interrupt.kind === 'elicitation') {
    return 'MCP elicitation'
  }
  if (interrupt.kind === 'user_input') {
    return 'User input'
  }
  if (interrupt.approval?.markdown?.trim()) {
    return 'Plan approval'
  }
  return 'Approval'
}

function elicitationModeLabel(mode?: string) {
  if (!mode) {
    return ''
  }
  return mode === 'url' ? 'URL' : toTitleCase(mode)
}

function interruptHasAcknowledgeAction(interrupt: PendingInterrupt) {
  return interrupt.actions?.some((action) => action.kind === 'acknowledge') ?? false
}

function issueHref(interrupt: PendingInterrupt) {
  if (!interrupt.issue_identifier) {
    return null
  }
  return `/issues/${encodeURIComponent(interrupt.issue_identifier)}`
}

function projectHref(interrupt: PendingInterrupt) {
  if (!interrupt.project_id) {
    return null
  }
  return `/projects/${encodeURIComponent(interrupt.project_id)}`
}

export function GlobalInterruptPanel({
  items,
  open,
  respondableInterruptId,
  isSubmitting,
  onAcknowledge,
  onOpenChange,
  onRequestPlanRevision,
  onRespond,
}: {
  items: PendingInterrupt[]
  open: boolean
  respondableInterruptId?: string | null
  isSubmitting: boolean
  onAcknowledge: (interruptId: string) => void
  onOpenChange: (open: boolean) => void
  onRequestPlanRevision: (payload: PlanRevisionRequestPayload) => void
  onRespond: (payload: InterruptResponsePayload) => void
}) {
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [queueOpen, setQueueOpen] = useState(false)
  const [interactionState, setInteractionState] = useState<{
    interruptId: string | null
    draftKey: string
    decision: string
    draftNote: string
    draftAnswers: Record<string, string>
    noteVisible: boolean
    noteRequired: boolean
  }>({
    interruptId: null,
    draftKey: '',
    decision: '',
    draftNote: '',
    draftAnswers: {},
    noteVisible: false,
    noteRequired: false,
  })

  const selectedInterrupt = useMemo(
    () => items.find((interrupt) => interrupt.id === selectedId) ?? defaultSelectedInterrupt(items),
    [items, selectedId],
  )
  const activeRespondableInterruptId = respondableInterruptId ?? defaultRespondableInterruptId(items)
  const questions = selectedInterrupt?.user_input?.questions ?? EMPTY_QUESTIONS
  const selectedInterruptDraftKey = selectedInterrupt ? interruptDraftKey(selectedInterrupt) : ''
  const selectedDraftMatchesInterrupt =
    interactionState.interruptId === selectedInterrupt?.id &&
    interactionState.draftKey === selectedInterruptDraftKey
  const draftNote = selectedDraftMatchesInterrupt ? interactionState.draftNote : ''
  const draftAnswers = selectedDraftMatchesInterrupt ? interactionState.draftAnswers : EMPTY_DRAFT_ANSWERS
  const noteVisible = selectedDraftMatchesInterrupt ? interactionState.noteVisible : false
  const noteRequired = selectedDraftMatchesInterrupt ? interactionState.noteRequired : false
  const answers = useMemo(() => buildAnswers(questions, draftAnswers), [draftAnswers, questions])
  const isApproval = selectedInterrupt?.kind === 'approval'
  const isUserInput = selectedInterrupt?.kind === 'user_input'
  const isElicitation = selectedInterrupt?.kind === 'elicitation'
  const isAlert = selectedInterrupt?.kind === 'alert'
  const approvalMarkdown = selectedInterrupt?.approval?.markdown?.trim() ?? ''
  const approvalPlanVersion = selectedInterrupt?.approval?.plan_version_number ?? 0
  const approvalPlanStatus = selectedInterrupt?.approval?.plan_status ?? ''
  const approvalRevisionNote = selectedInterrupt?.approval?.plan_revision_note?.trim() ?? ''
  const elicitationRequestedSchema = selectedInterrupt?.elicitation?.requested_schema
  const elicitationMode = selectedInterrupt?.elicitation?.mode ?? ''
  const isPlanApproval = isApproval && approvalMarkdown.length > 0
  const requiresExplicitSubmit =
    isUserInput && questions.some((question) => questionHasTextInput(question))
  const valid =
    isUserInput
      ? questions.length > 0 &&
        questions.every((question) => (answers[question.id]?.[0] ?? '').trim().length > 0)
      : false
  const normalizedApprovalDecisions =
    isApproval ? normalizeApprovalDecisions(selectedInterrupt?.approval?.decisions ?? []) : null
  const primaryApprovalDecision = normalizedApprovalDecisions?.primary ?? null
  const issueLink = selectedInterrupt ? issueHref(selectedInterrupt) : null
  const projectLink = selectedInterrupt ? projectHref(selectedInterrupt) : null
  const canRespondToSelectedInterrupt =
    !!selectedInterrupt && selectedInterrupt.kind !== 'alert' && selectedInterrupt.id === activeRespondableInterruptId
  const responseLocked = isSubmitting || !canRespondToSelectedInterrupt
  const canSubmitNote = draftNote.trim().length > 0
  const formId = 'global-interrupt-form'

  if (!selectedInterrupt) {
    return null
  }

  const respondToSelectedInterrupt = (payload: Omit<InterruptResponsePayload, 'interruptId'>) => {
    if (responseLocked) {
      return
    }
    onRespond({ interruptId: selectedInterrupt.id, ...payload })
  }

  const requestRevisionForSelectedInterrupt = (note: string) => {
    if (responseLocked) {
      return
    }
    const issueIdentifier = selectedInterrupt.issue_identifier?.trim()
    if (!issueIdentifier) {
      return
    }
    onRequestPlanRevision({ issueIdentifier, note })
  }

  const respondWithApprovalOption = (option: PendingApprovalDecision) => {
    if (responseLocked) {
      return
    }
    updateDecision(option.value)
    if (option.decision_payload) {
      respondToSelectedInterrupt({
        decision_payload: option.decision_payload,
        note: draftNote.trim() || undefined,
      })
      return
    }
    respondToSelectedInterrupt({
      decision: option.value,
      note: draftNote.trim() || undefined,
    })
  }

  const requestChangesForPlanApproval = () => {
    if (responseLocked) {
      return
    }
    const note = draftNote.trim()
    if (!note) {
      requireDraftNote()
      return
    }
    requestRevisionForSelectedInterrupt(note)
  }

  const selectedDraftState = (current: {
    interruptId: string | null
    draftKey: string
    decision: string
    draftNote: string
    draftAnswers: Record<string, string>
    noteVisible: boolean
    noteRequired: boolean
  }) =>
    current.interruptId === selectedInterrupt.id && current.draftKey === selectedInterruptDraftKey

  const updateDecision = (nextDecision: string) => {
    setInteractionState((current) => ({
      interruptId: selectedInterrupt.id,
      draftKey: selectedInterruptDraftKey,
      decision: nextDecision,
      draftNote: selectedDraftState(current) ? current.draftNote : '',
      draftAnswers: selectedDraftState(current) ? current.draftAnswers : {},
      noteVisible: selectedDraftState(current) ? current.noteVisible : false,
      noteRequired: selectedDraftState(current) ? current.noteRequired : false,
    }))
  }

  const sendNoteForSelectedInterrupt = () => {
    if (!isApproval || !canSubmitNote || responseLocked) {
      return
    }
    setInteractionState((current) => ({
      interruptId: selectedInterrupt.id,
      draftKey: selectedInterruptDraftKey,
      decision: selectedDraftState(current) ? current.decision : '',
      draftNote: selectedDraftState(current) ? current.draftNote : '',
      draftAnswers: selectedDraftState(current) ? current.draftAnswers : {},
      noteVisible: selectedDraftState(current) ? current.noteVisible : false,
      noteRequired: false,
    }))
    respondToSelectedInterrupt({ note: draftNote.trim() })
  }

  const updateDraftNote = (value: string) => {
    setInteractionState((current) => ({
      interruptId: selectedInterrupt.id,
      draftKey: selectedInterruptDraftKey,
      decision: selectedDraftState(current) ? current.decision : '',
      draftNote: value,
      draftAnswers: selectedDraftState(current) ? current.draftAnswers : {},
      noteVisible: selectedDraftState(current) ? current.noteVisible : false,
      noteRequired:
        selectedDraftState(current) ? current.noteRequired && value.trim().length === 0 : false,
    }))
  }

  const updateDraftAnswer = (questionId: string, value: string) => {
    setInteractionState((current) => ({
      interruptId: selectedInterrupt.id,
      draftKey: selectedInterruptDraftKey,
      decision: selectedDraftState(current) ? current.decision : '',
      draftNote: selectedDraftState(current) ? current.draftNote : '',
      draftAnswers: {
        ...(selectedDraftState(current) ? current.draftAnswers : {}),
        [questionId]: value,
      },
      noteVisible: selectedDraftState(current) ? current.noteVisible : false,
      noteRequired: selectedDraftState(current) ? current.noteRequired : false,
    }))
  }

  const setDraftNoteVisibility = (visible: boolean) => {
    setInteractionState((current) => ({
      interruptId: selectedInterrupt.id,
      draftKey: selectedInterruptDraftKey,
      decision: selectedDraftState(current) ? current.decision : '',
      draftNote: selectedDraftState(current) ? current.draftNote : '',
      draftAnswers: selectedDraftState(current) ? current.draftAnswers : {},
      noteVisible: visible,
      noteRequired: visible && selectedDraftState(current) ? current.noteRequired : false,
    }))
  }

  const requireDraftNote = () => {
    setInteractionState((current) => ({
      interruptId: selectedInterrupt.id,
      draftKey: selectedInterruptDraftKey,
      decision: selectedDraftState(current) ? current.decision : '',
      draftNote: selectedDraftState(current) ? current.draftNote : '',
      draftAnswers: selectedDraftState(current) ? current.draftAnswers : {},
      noteVisible: true,
      noteRequired: true,
    }))
  }

  const approvalOverflowGroups: ApprovalReviewOverflowGroup[] =
    normalizedApprovalDecisions?.overflowGroups.map((group) => ({
      key: group.key,
      label: group.label,
      actions: group.options.map((option) => ({
        key: option.value,
        label: option.label,
        description: option.description,
        disabled: responseLocked,
        variant: approvalDecisionVariant(option),
        onClick: () => {
          respondWithApprovalOption(option)
        },
      })),
    })) ?? []

  const detailFooter = isAlert ? (
    interruptHasAcknowledgeAction(selectedInterrupt) ? (
      <div className="flex flex-wrap items-center gap-3">
        <Button
          className="h-11 rounded-2xl border-white/10 bg-white/5 px-4 text-sm font-medium text-white hover:border-white/20 hover:bg-white/8"
          disabled={isSubmitting}
          type="button"
          variant="secondary"
          onClick={() => {
            if (isSubmitting) {
              return
            }
            onAcknowledge(selectedInterrupt.id)
          }}
        >
          Acknowledge
        </Button>
        <span className="text-sm leading-6 text-[var(--muted-foreground)]">
          Resolve the blocker, or acknowledge it if you only need to clear the alert state.
        </span>
      </div>
    ) : null
  ) : isUserInput && requiresExplicitSubmit ? (
    <div className="flex items-center justify-end gap-3">
      <Button
        className={cn(
          'h-11 rounded-2xl border px-4 text-sm font-medium transition',
          valid && !responseLocked
            ? 'border-[var(--accent)]/45 bg-[linear-gradient(135deg,rgba(196,255,87,.24),rgba(255,255,255,.06))] text-white hover:border-[var(--accent)]/60'
            : 'border-white/10 bg-white/5 text-white/45',
        )}
        disabled={!valid || responseLocked}
        form={formId}
        type="submit"
      >
        {isSubmitting ? 'Submitting...' : 'Submit response'}
      </Button>
    </div>
  ) : null

  const queueAvailable = items.length > 1
  const detailColumnClassName =
    queueAvailable && queueOpen ? 'min-w-0 w-full' : 'mx-auto min-w-0 w-full max-w-[60rem]'

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        data-testid="global-interrupt-panel"
        showCloseButton={false}
        className="left-0 top-0 flex h-[100dvh] w-[100vw] max-w-none -translate-x-0 -translate-y-0 flex-col overflow-hidden rounded-none border-0 bg-[rgba(8,9,12,0.98)] p-0 shadow-none"
      >
        <div className="flex h-full min-h-0 flex-col">
          <div className="shrink-0 border-b border-white/8 bg-[rgba(8,9,12,0.95)] px-4 py-5 backdrop-blur-xl sm:px-6">
            <div className="flex items-start justify-between gap-4">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  {queueAvailable ? (
                    <Button
                      aria-expanded={queueOpen}
                      className="h-9 rounded-full border-white/10 bg-white/5 px-3 text-sm text-white hover:border-white/18 hover:bg-white/8"
                      type="button"
                      variant="secondary"
                      onClick={() => {
                        setQueueOpen((current) => !current)
                      }}
                    >
                      {queueOpen ? 'Hide queue' : `Queue (${items.length})`}
                    </Button>
                  ) : (
                    <Badge className="border-amber-400/25 bg-amber-400/12 text-amber-100">
                      {items.length} waiting
                    </Badge>
                  )}
                  <Badge
                    className={cn(
                      'border-white/10 bg-white/5 text-white',
                      isAlert && alertSeverityClasses(selectedInterrupt.alert),
                    )}
                  >
                    {interruptKindLabel(selectedInterrupt)}
                  </Badge>
                  {isElicitation && elicitationMode ? (
                    <Badge className="border-sky-400/20 bg-sky-400/10 text-sky-100">
                      {elicitationModeLabel(elicitationMode)}
                    </Badge>
                  ) : null}
                  {selectedInterrupt.collaboration_mode === 'plan' ? (
                    <Badge className="border-sky-400/25 bg-sky-400/12 text-sky-100">Plan turn</Badge>
                  ) : null}
                </div>
                <DialogTitle className="mt-4 max-w-4xl text-2xl font-semibold leading-tight text-white sm:text-[2rem]">
                  {interruptHeading(selectedInterrupt)}
                </DialogTitle>
                <DialogDescription className="mt-3 max-w-4xl text-base leading-7 text-[var(--muted-foreground)]">
                  {interruptSummary(selectedInterrupt)}
                </DialogDescription>
                <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 text-sm text-[var(--muted-foreground)]">
                  <span>{interruptSubject(selectedInterrupt)}</span>
                  <span>Updated <CompactRelativeTime value={selectedInterrupt.last_activity_at || selectedInterrupt.requested_at} /></span>
                  {selectedInterrupt.phase ? <span>{toTitleCase(selectedInterrupt.phase)}</span> : null}
                  {selectedInterrupt.attempt ? <span>Attempt {selectedInterrupt.attempt}</span> : null}
                  {queueAvailable ? <span>{items.length} waiting</span> : null}
                </div>
              </div>
              <Button
                aria-label="Hide waiting input dialog"
                className="shrink-0 border-white/10 bg-white/5 text-white hover:border-white/20 hover:bg-white/8"
                size="icon"
                type="button"
                variant="secondary"
                onClick={() => onOpenChange(false)}
              >
                <X className="size-4" />
              </Button>
            </div>
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-6" data-testid="global-interrupt-body">
            <div
              className={cn(
                'mx-auto grid w-full max-w-[88rem] items-start gap-6',
                queueAvailable && queueOpen ? 'xl:grid-cols-[18rem_minmax(0,1fr)]' : '',
              )}
            >
              {queueAvailable && queueOpen ? (
                <aside className="min-w-0 rounded-[var(--panel-radius)] border border-white/8 bg-black/25 p-3">
                  <div className="mb-3 flex items-center justify-between gap-3 border-b border-white/8 px-1 pb-3">
                    <div>
                      <p className="text-sm font-medium text-white">Waiting queue</p>
                      <p className="text-sm text-[var(--muted-foreground)]">Switch items without losing the current draft.</p>
                    </div>
                  </div>
                  <div className="grid gap-2.5">
                    {items.map((interrupt) => {
                      const selected = interrupt.id === selectedInterrupt.id
                      const locked =
                        interrupt.kind !== 'alert' && interrupt.id !== activeRespondableInterruptId

                      return (
                        <button
                          key={interrupt.id}
                          className={cn(
                            'w-full rounded-[calc(var(--panel-radius)-0.25rem)] border px-4 py-4 text-left transition',
                            selected
                              ? 'border-[var(--accent)]/35 bg-[linear-gradient(135deg,rgba(196,255,87,.14),rgba(255,255,255,.06))]'
                              : 'border-white/8 bg-black/20 hover:border-white/12 hover:bg-black/30',
                          )}
                          type="button"
                          onClick={() => {
                            setSelectedId(interrupt.id)
                          }}
                        >
                          <div className="flex flex-wrap items-center gap-2">
                            <Badge className="border-white/10 bg-white/5 text-white">
                              {interruptKindLabel(interrupt)}
                            </Badge>
                            {interrupt.kind === 'elicitation' && interrupt.elicitation?.mode ? (
                              <Badge className="border-sky-400/20 bg-sky-400/10 text-sky-100">
                                {elicitationModeLabel(interrupt.elicitation.mode)}
                              </Badge>
                            ) : null}
                            {interrupt.kind === 'alert' ? (
                              <Badge className={alertSeverityClasses(interrupt.alert)}>
                                {toTitleCase(interrupt.alert?.severity || 'error')}
                              </Badge>
                            ) : null}
                          </div>
                          <p className="mt-3 text-sm font-medium text-white">{interruptHeading(interrupt)}</p>
                          <p className="mt-1 text-sm leading-6 text-[var(--muted-foreground)]">
                            {interruptSummary(interrupt)}
                          </p>
                          <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-[var(--muted-foreground)]">
                            <span>Updated <CompactRelativeTime value={interrupt.last_activity_at || interrupt.requested_at} /></span>
                            {locked ? <span>Waiting for earlier response</span> : null}
                          </div>
                        </button>
                      )
                    })}
                  </div>
                </aside>
              ) : null}

              {isAlert ? (
                <div className={cn(detailColumnClassName, 'rounded-[var(--panel-radius)] border border-white/8 bg-black/25 p-[var(--panel-padding)]')}>
                  <div className="grid gap-4">
                    <div
                      className={cn(
                        'rounded-[calc(var(--panel-radius)-0.2rem)] border px-5 py-5',
                        selectedInterrupt.alert?.severity === 'info' && 'border-sky-400/20 bg-sky-400/10',
                        selectedInterrupt.alert?.severity === 'warning' && 'border-amber-400/20 bg-amber-400/10',
                        (!selectedInterrupt.alert?.severity || selectedInterrupt.alert?.severity === 'error') &&
                          'border-rose-400/20 bg-rose-400/10',
                      )}
                    >
                      <p className="text-lg font-semibold text-white">
                        {selectedInterrupt.alert?.title || 'Maestro alert'}
                      </p>
                      <p className="mt-2 max-w-4xl text-sm leading-6 text-white/88">
                        {selectedInterrupt.alert?.message || 'Maestro needs your attention before execution can continue.'}
                      </p>
                      {selectedInterrupt.alert?.detail ? (
                        <p className="mt-3 text-sm leading-6 text-[var(--muted-foreground)]">
                          {selectedInterrupt.alert.detail}
                        </p>
                      ) : null}
                    </div>

                    <div className="flex flex-wrap gap-3">
                      {issueLink ? (
                        <a
                          className="inline-flex h-11 items-center rounded-2xl border border-white/10 px-4 text-sm font-medium text-white transition hover:border-white/20 hover:bg-white/5"
                          href={issueLink}
                        >
                          Open issue
                        </a>
                      ) : null}
                      {projectLink ? (
                        <a
                          className="inline-flex h-11 items-center rounded-2xl border border-white/10 px-4 text-sm font-medium text-white transition hover:border-white/20 hover:bg-white/5"
                          href={projectLink}
                        >
                          Open project
                        </a>
                      ) : null}
                    </div>
                  </div>
                </div>
              ) : (
                <form
                  id={formId}
                  className={cn(
                    detailColumnClassName,
                    isElicitation ? 'grid gap-4' : 'rounded-[var(--panel-radius)] border border-white/8 bg-black/25 p-[var(--panel-padding)]',
                  )}
                  onSubmit={(event) => {
                    event.preventDefault()
                    if (!valid || responseLocked || !isUserInput) {
                      return
                    }
                    respondToSelectedInterrupt({ answers })
                  }}
                >
                  <div className="grid gap-4">
                    {isPlanApproval ? (
                      <ApprovalReviewPanel
                        description="Focus on the plan itself. Use the queue only if you need to switch to another waiting request."
                        note={draftNote}
                        noteDescription="Add optional steering notes for the next turn. A note becomes required if you request changes."
                        notePlaceholder="Explain what should change in the plan..."
                        noteRequired={noteRequired}
                        noteVisible={noteVisible}
                        overflowGroups={approvalOverflowGroups}
                        primaryAction={
                          primaryApprovalDecision
                            ? {
                                key: primaryApprovalDecision.value,
                                label: primaryApprovalDecision.label,
                                disabled: responseLocked,
                                onClick: () => {
                                  respondWithApprovalOption(primaryApprovalDecision)
                                },
                              }
                            : {
                                key: 'approve-plan',
                                label: 'Approve plan',
                                disabled: true,
                                onClick: () => {},
                              }
                        }
                        secondaryActions={[
                          {
                            key: 'request-changes',
                            label: 'Request changes',
                            disabled: responseLocked || !selectedInterrupt.issue_identifier?.trim(),
                            variant: 'secondary',
                            onClick: requestChangesForPlanApproval,
                          },
                        ]}
                        title="Review the proposed plan"
                        onNoteChange={updateDraftNote}
                        onToggleNote={() => {
                          setDraftNoteVisibility(!noteVisible)
                        }}
                      >
                        {!canRespondToSelectedInterrupt ? (
                          <p className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-amber-400/20 bg-amber-400/10 px-4 py-3 text-sm leading-6 text-amber-100/90">
                            An earlier interrupt is still pending. You can review this plan now, but responses stay locked until it reaches the front of the queue.
                          </p>
                        ) : null}
                        {approvalPlanVersion > 0 || approvalPlanStatus || approvalRevisionNote ? (
                          <div className="grid gap-3 rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/10 bg-white/[0.03] p-4">
                            <div className="flex flex-wrap items-center gap-2">
                              {approvalPlanVersion > 0 ? (
                                <Badge className="border-white/10 bg-white/5 text-white">
                                  Version {approvalPlanVersion}
                                </Badge>
                              ) : null}
                              {approvalPlanStatus ? (
                                <Badge className="border-sky-400/20 bg-sky-400/10 text-sky-100">
                                  {toTitleCase(approvalPlanStatus.replace(/_/g, ' '))}
                                </Badge>
                              ) : null}
                            </div>
                            {approvalRevisionNote ? (
                              <div>
                                <p className="text-xs uppercase tracking-[0.16em] text-[var(--muted-foreground)]">
                                  Latest revision note
                                </p>
                                <p className="mt-2 text-sm leading-6 text-[var(--muted-foreground)]">
                                  {approvalRevisionNote}
                                </p>
                              </div>
                            ) : null}
                          </div>
                        ) : null}
                        <PlanApprovalDocument markdown={approvalMarkdown} />
                      </ApprovalReviewPanel>
                    ) : isElicitation ? (
                      <div className="grid gap-4">
                        <div className="grid gap-4">
                          <div className="flex flex-wrap items-center gap-2">
                            <Badge className="border-white/10 bg-white/5 text-white">
                              {selectedInterrupt.elicitation?.server_name || 'MCP server'}
                            </Badge>
                            {elicitationMode ? (
                              <Badge className="border-sky-400/20 bg-sky-400/10 text-sky-100">
                                {elicitationMode === 'url' ? 'URL' : toTitleCase(elicitationMode)}
                              </Badge>
                            ) : null}
                            {selectedInterrupt.elicitation?.elicitation_id ? (
                              <Badge className="border-white/10 bg-white/5 text-white">
                                ID {selectedInterrupt.elicitation.elicitation_id}
                              </Badge>
                            ) : null}
                          </div>
                          <div>
                            <p className="text-lg font-semibold text-white">
                              {selectedInterrupt.elicitation?.message || 'MCP elicitation'}
                            </p>
                            <p className="mt-2 text-sm leading-6 text-[var(--muted-foreground)]">
                              Review the requested form below, then accept to send the structured response back to the requesting agent.
                            </p>
                          </div>
                          {selectedInterrupt.elicitation?.url ? (
                            <a
                              className="inline-flex h-11 w-fit items-center rounded-2xl border border-white/10 px-4 text-sm font-medium text-white transition hover:border-white/20 hover:bg-white/5"
                              href={selectedInterrupt.elicitation.url}
                              rel="noreferrer"
                              target="_blank"
                            >
                              Open URL
                            </a>
                          ) : null}
                        </div>
                        <ElicitationForm
                          draftKey={selectedInterruptDraftKey}
                          isSubmitting={isSubmitting}
                          requestedSchema={elicitationRequestedSchema}
                          disabled={responseLocked}
                          onAccept={(content) => {
                            respondToSelectedInterrupt({
                              action: 'accept',
                              content,
                            })
                          }}
                          onCancel={() => {
                            respondToSelectedInterrupt({ action: 'cancel' })
                          }}
                          onDecline={() => {
                            respondToSelectedInterrupt({ action: 'decline' })
                          }}
                        />
                      </div>
                    ) : isApproval ? (
                      <ApprovalReviewPanel
                        description="Review the request details and use More actions only when you need a broader or more disruptive response."
                        note={draftNote}
                        noteDescription="Send a steering note to the agent without consuming this approval."
                        noteLabel="Agent note"
                        notePlaceholder="Add steering notes for the next turn..."
                        noteRequired={false}
                        noteSubmitDescription="This note will queue without approving the request."
                        noteSubmitDisabled={!canSubmitNote || responseLocked}
                        noteVisible={noteVisible}
                        overflowGroups={approvalOverflowGroups}
                        primaryAction={
                          primaryApprovalDecision
                            ? {
                                key: primaryApprovalDecision.value,
                                label: primaryApprovalDecision.label,
                                disabled: responseLocked,
                                onClick: () => {
                                  respondWithApprovalOption(primaryApprovalDecision)
                                },
                              }
                            : null
                        }
                        title={approvalPrompt(selectedInterrupt)}
                        onNoteChange={updateDraftNote}
                        onNoteSubmit={sendNoteForSelectedInterrupt}
                        onToggleNote={() => {
                          setDraftNoteVisibility(!noteVisible)
                        }}
                      >
                        {!canRespondToSelectedInterrupt ? (
                          <p className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-amber-400/20 bg-amber-400/10 px-4 py-3 text-sm leading-6 text-amber-100/90">
                            An earlier interrupt is still pending. Review this request now, but wait until it reaches the front of the queue before responding.
                          </p>
                        ) : null}
                        {selectedInterrupt.approval?.reason ? (
                          <div className="rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/10 bg-white/[0.04] p-5 text-white">
                            <p className="text-xs font-medium uppercase tracking-[0.12em] text-white/55">
                              Reason
                            </p>
                            <p className="mt-3 text-[15px] leading-7 text-white/88">
                              {selectedInterrupt.approval.reason}
                            </p>
                          </div>
                        ) : null}
                        {selectedInterrupt.approval?.command ? (
                          <ApprovalCommandPreview command={selectedInterrupt.approval.command} />
                        ) : null}
                        {selectedInterrupt.approval?.cwd ? (
                          <div className="rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/8 bg-white/[0.03] px-4 py-4">
                            <p className="text-[11px] uppercase tracking-[0.16em] text-[var(--muted-foreground)]">
                              Working directory
                            </p>
                            <code className="mt-3 inline-flex max-w-full whitespace-pre-wrap break-all rounded-full border border-white/10 bg-black/25 px-3 py-1.5 font-mono text-xs text-white">
                              {selectedInterrupt.approval.cwd}
                            </code>
                          </div>
                        ) : null}
                      </ApprovalReviewPanel>
                    ) : (
                      <>
                        <div className="space-y-2">
                          <p className="text-lg font-semibold text-white">Respond to this request</p>
                          <p className="text-sm leading-6 text-[var(--muted-foreground)]">
                            Provide the information the agent needs so it can continue the current turn.
                          </p>
                          {!canRespondToSelectedInterrupt ? (
                            <p className="rounded-[calc(var(--panel-radius)-0.25rem)] border border-amber-400/20 bg-amber-400/10 px-4 py-3 text-sm leading-6 text-amber-100/90">
                              An earlier interrupt is still pending. You can review these questions now, but responses stay locked until this request reaches the front of the queue.
                            </p>
                          ) : null}
                        </div>

                        {isUserInput ? (
                          <div className="grid gap-3">
                            {questions.map((question) => (
                              <label
                                key={question.id}
                                className="grid gap-2 rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/8 bg-white/[0.03] p-3"
                              >
                                <div className="space-y-1">
                                  {question.header ? (
                                    <p className="text-xs uppercase tracking-[0.16em] text-[var(--muted-foreground)]">
                                      {question.header}
                                    </p>
                                  ) : null}
                                  <p className="text-sm text-white">{question.question || question.id}</p>
                                </div>
                                {question.options?.length ? (
                                  <div className="grid gap-2">
                                    {question.options.map((option) => {
                                      const checked = draftAnswers[question.id] === option.label
                                      return (
                                        <button
                                          key={option.label}
                                          className={cn(
                                            'rounded-xl border px-3 py-2 text-left text-sm transition',
                                            checked
                                              ? 'border-[var(--accent)]/50 bg-[var(--accent)]/10 text-white'
                                              : 'border-white/10 bg-black/20 text-[var(--muted-foreground)] hover:border-white/20 hover:text-white',
                                          )}
                                          disabled={responseLocked}
                                          type="button"
                                          onClick={() => {
                                            if (responseLocked) {
                                              return
                                            }
                                            const nextDraftAnswers = {
                                              ...draftAnswers,
                                              [question.id]: option.label,
                                            }
                                            updateDraftAnswer(question.id, option.label)
                                            if (requiresExplicitSubmit) {
                                              return
                                            }
                                            const nextAnswers = buildAnswers(questions, nextDraftAnswers)
                                            const readyToSubmit =
                                              questions.length > 0 &&
                                              questions.every(
                                                (currentQuestion) =>
                                                  (nextAnswers[currentQuestion.id]?.[0] ?? '').trim().length > 0,
                                              )
                                            if (readyToSubmit) {
                                              respondToSelectedInterrupt({ answers: nextAnswers })
                                            }
                                          }}
                                        >
                                          <span className="font-medium">{option.label}</span>
                                          {option.description ? (
                                            <span className="ml-2 text-[var(--muted-foreground)]">{option.description}</span>
                                          ) : null}
                                        </button>
                                      )
                                    })}
                                  </div>
                                ) : null}
                                {!question.options?.length || question.is_other ? (
                                  <input
                                    className="h-11 rounded-xl border border-white/10 bg-black/20 px-3 text-sm text-white outline-none transition focus:border-[var(--accent)]/50"
                                    disabled={responseLocked}
                                    placeholder={question.is_secret ? 'Enter response' : 'Type response'}
                                    type={question.is_secret ? 'password' : 'text'}
                                    value={draftAnswers[question.id] ?? ''}
                                    onChange={(event) => {
                                      updateDraftAnswer(question.id, event.target.value)
                                    }}
                                  />
                                ) : null}
                              </label>
                            ))}
                          </div>
                        ) : null}
                      </>
                    )}
                  </div>
                </form>
              )}
            </div>
          </div>

          {detailFooter ? (
            <div className="shrink-0 border-t border-white/8 bg-[rgba(8,9,12,0.96)] px-4 py-4 sm:px-6" data-testid="global-interrupt-footer">
              {detailFooter}
            </div>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  )
}
