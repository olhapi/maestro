import { useMemo, useState } from 'react'

import { Badge } from '@/components/ui/badge'
import type {
  PendingAlert,
  PendingApprovalDecision,
  PendingInterrupt,
  PendingUserInputQuestion,
} from '@/lib/types'
import { cn, formatRelativeTimeCompact, toTitleCase } from '@/lib/utils'

const EMPTY_QUESTIONS: PendingUserInputQuestion[] = []

type InterruptResponsePayload = {
  interruptId: string
  decision?: string
  decision_payload?: Record<string, unknown>
  answers?: Record<string, string[]>
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
  if (interrupt.kind === 'approval') {
    return interrupt.approval?.command || interrupt.approval?.reason || 'Operator approval required.'
  }
  return interrupt.user_input?.questions?.[0]?.question || 'Operator input required.'
}

function approvalPrompt(interrupt: PendingInterrupt) {
  if (interrupt.approval?.command) {
    return 'Allow the agent to run this command?'
  }
  if (interrupt.approval?.reason) {
    return 'Approve this request before the agent continues.'
  }
  return 'Review this request before the agent continues.'
}

function classifyApprovalDecision(option: PendingApprovalDecision) {
  const value = option.value.toLowerCase()
  const label = option.label.toLowerCase()
  if (
    value.includes('deny') ||
    value.includes('decline') ||
    value.includes('abort') ||
    value.includes('cancel') ||
    label.includes('deny') ||
    label.includes('decline') ||
    label.includes('abort') ||
    label.includes('cancel')
  ) {
    return 'destructive'
  }
  if (
    value.includes('session') ||
    value.includes('grant') ||
    value.includes('network_policy') ||
    value.includes('amendment') ||
    label.includes('session') ||
    label.includes('store') ||
    label.includes('persist') ||
    label.includes('grant')
  ) {
    return 'secondary'
  }
  return 'primary'
}

function approvalDecisionGroups(decisions: PendingApprovalDecision[]) {
  return decisions.reduce(
    (groups, option) => {
      groups[classifyApprovalDecision(option)].push(option)
      return groups
    },
    {
      primary: [] as PendingApprovalDecision[],
      secondary: [] as PendingApprovalDecision[],
      destructive: [] as PendingApprovalDecision[],
    },
  )
}

function interruptHeading(interrupt: PendingInterrupt) {
  if (interrupt.kind === 'alert') {
    return interrupt.alert?.title || interrupt.issue_title || interrupt.issue_identifier || interrupt.project_name || 'Maestro alert'
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
  if (interrupt.kind === 'user_input') {
    return 'User input'
  }
  return 'Approval'
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
  respondableInterruptId,
  isSubmitting,
  onAcknowledge,
  onRespond,
}: {
  items: PendingInterrupt[]
  respondableInterruptId?: string | null
  isSubmitting: boolean
  onAcknowledge: (interruptId: string) => void
  onRespond: (payload: InterruptResponsePayload) => void
}) {
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [interactionState, setInteractionState] = useState<{
    interruptId: string | null
    decision: string
    draftAnswers: Record<string, string>
  }>({
    interruptId: null,
    decision: '',
    draftAnswers: {},
  })

  const selectedInterrupt = useMemo(
    () => items.find((interrupt) => interrupt.id === selectedId) ?? defaultSelectedInterrupt(items),
    [items, selectedId],
  )
  const activeRespondableInterruptId = respondableInterruptId ?? defaultRespondableInterruptId(items)
  const decision = interactionState.interruptId === selectedInterrupt?.id ? interactionState.decision : ''
  const questions = selectedInterrupt?.user_input?.questions ?? EMPTY_QUESTIONS
  const draftAnswers = useMemo(
    () => (interactionState.interruptId === selectedInterrupt?.id ? interactionState.draftAnswers : {}),
    [interactionState.draftAnswers, interactionState.interruptId, selectedInterrupt?.id],
  )
  const answers = useMemo(() => buildAnswers(questions, draftAnswers), [draftAnswers, questions])
  const isApproval = selectedInterrupt?.kind === 'approval'
  const isUserInput = selectedInterrupt?.kind === 'user_input'
  const isAlert = selectedInterrupt?.kind === 'alert'
  const requiresExplicitSubmit =
    isUserInput && questions.some((question) => questionHasTextInput(question))
  const valid =
    isApproval
      ? !!decision
      : isUserInput
        ? questions.length > 0 &&
          questions.every((question) => (answers[question.id]?.[0] ?? '').trim().length > 0)
        : false
  const approvalGroups =
    isApproval
      ? approvalDecisionGroups(selectedInterrupt?.approval?.decisions ?? [])
      : null
  const issueLink = selectedInterrupt ? issueHref(selectedInterrupt) : null
  const projectLink = selectedInterrupt ? projectHref(selectedInterrupt) : null
  const canRespondToSelectedInterrupt =
    !!selectedInterrupt && selectedInterrupt.kind !== 'alert' && selectedInterrupt.id === activeRespondableInterruptId
  const responseLocked = isSubmitting || !canRespondToSelectedInterrupt

  if (!selectedInterrupt) {
    return null
  }

  const respondToSelectedInterrupt = (payload: Omit<InterruptResponsePayload, 'interruptId'>) => {
    if (responseLocked) {
      return
    }
    onRespond({ interruptId: selectedInterrupt.id, ...payload })
  }

  const updateDecision = (nextDecision: string) => {
    setInteractionState((current) => ({
      interruptId: selectedInterrupt.id,
      decision: nextDecision,
      draftAnswers:
        current.interruptId === selectedInterrupt.id ? current.draftAnswers : {},
    }))
  }

  const updateDraftAnswer = (questionId: string, value: string) => {
    setInteractionState((current) => ({
      interruptId: selectedInterrupt.id,
      decision: current.interruptId === selectedInterrupt.id ? current.decision : '',
      draftAnswers: {
        ...(current.interruptId === selectedInterrupt.id ? current.draftAnswers : {}),
        [questionId]: value,
      },
    }))
  }

  return (
    <section
      className="sticky top-[4.75rem] z-20 border-b border-white/10 bg-[rgba(9,12,16,0.88)] backdrop-blur-2xl lg:top-[4.6rem]"
      data-testid="global-interrupt-panel"
    >
      <div className="mx-auto w-full max-w-[1600px] px-[var(--shell-padding)] py-4">
        <div className="rounded-[calc(var(--panel-radius)+0.2rem)] border border-white/10 bg-[linear-gradient(180deg,rgba(255,255,255,.055),rgba(255,255,255,.025))] p-[var(--panel-padding)] shadow-[0_24px_80px_rgba(0,0,0,.34)]">
          <div className="flex flex-wrap items-center gap-2">
            <Badge className="border-amber-400/25 bg-amber-400/12 text-amber-100">
              {items.length} waiting
            </Badge>
            <Badge className={cn('border-white/10 bg-white/5 text-white', isAlert && alertSeverityClasses(selectedInterrupt.alert))}>
              {interruptKindLabel(selectedInterrupt)}
            </Badge>
            {selectedInterrupt.collaboration_mode === 'plan' ? (
              <Badge className="border-sky-400/25 bg-sky-400/12 text-sky-100">Plan turn</Badge>
            ) : null}
            <Badge className="border-white/10 bg-white/5 text-white">
              {interruptSubject(selectedInterrupt)}
            </Badge>
            {selectedInterrupt.phase ? (
              <Badge className="border-white/10 bg-white/5 text-white">
                {toTitleCase(selectedInterrupt.phase)}
              </Badge>
            ) : null}
            {selectedInterrupt.attempt ? (
              <Badge className="border-white/10 bg-white/5 text-white">
                Attempt {selectedInterrupt.attempt}
              </Badge>
            ) : null}
          </div>

          <div className="mt-4 grid gap-4 xl:grid-cols-[minmax(18rem,0.38fr)_minmax(0,1fr)]">
            <div className="grid gap-2.5">
              {items.map((interrupt) => {
                const selected = interrupt.id === selectedInterrupt.id
                const issueRowLink = issueHref(interrupt)
                const projectRowLink = projectHref(interrupt)
                const acknowledgeable = interrupt.kind === 'alert' && interruptHasAcknowledgeAction(interrupt)
                return (
                  <div
                    key={interrupt.id}
                    className={cn(
                      'rounded-[var(--panel-radius)] border transition',
                      selected
                        ? 'border-[var(--accent)]/35 bg-[linear-gradient(135deg,rgba(196,255,87,.14),rgba(255,255,255,.06))]'
                        : 'border-white/8 bg-black/25 hover:border-white/12 hover:bg-black/30',
                    )}
                  >
                    <button
                      className="w-full px-4 py-4 text-left"
                      type="button"
                      onClick={() => {
                        setSelectedId(interrupt.id)
                      }}
                    >
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge className="border-white/10 bg-white/5 text-white">
                          {interruptKindLabel(interrupt)}
                        </Badge>
                        {interrupt.kind === 'alert' ? (
                          <Badge className={alertSeverityClasses(interrupt.alert)}>
                            {toTitleCase(interrupt.alert?.severity || 'error')}
                          </Badge>
                        ) : null}
                      </div>
                      <p className="mt-3 truncate text-sm font-medium text-white">
                        {interruptHeading(interrupt)}
                      </p>
                      <p className="mt-1 text-sm leading-6 text-[var(--muted-foreground)]">
                        {interruptSummary(interrupt)}
                      </p>
                      <p className="mt-2 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                        Last activity {formatRelativeTimeCompact(interrupt.last_activity_at || interrupt.requested_at)}
                      </p>
                    </button>
                    {interrupt.kind === 'alert' ? (
                      <div className="flex flex-wrap gap-2 border-t border-white/8 px-4 pb-4 pt-3">
                        {issueRowLink ? (
                          <a
                            className="inline-flex items-center rounded-full border border-white/10 px-3 py-1.5 text-xs text-white transition hover:border-white/20 hover:bg-white/5"
                            href={issueRowLink}
                          >
                            Open issue
                          </a>
                        ) : null}
                        {projectRowLink ? (
                          <a
                            className="inline-flex items-center rounded-full border border-white/10 px-3 py-1.5 text-xs text-white transition hover:border-white/20 hover:bg-white/5"
                            href={projectRowLink}
                          >
                            Open project
                          </a>
                        ) : null}
                        {acknowledgeable ? (
                          <button
                            className="inline-flex items-center rounded-full border border-white/10 px-3 py-1.5 text-xs text-white transition hover:border-white/20 hover:bg-white/5 disabled:cursor-not-allowed disabled:text-white/45"
                            disabled={isSubmitting}
                            type="button"
                            onClick={() => {
                              if (isSubmitting) {
                                return
                              }
                              onAcknowledge(interrupt.id)
                            }}
                          >
                            Acknowledge
                          </button>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                )
              })}
            </div>

            <div className="rounded-[var(--panel-radius)] border border-white/8 bg-black/25 p-[var(--panel-padding)]">
              <div className="min-w-0">
                <p className="truncate text-base font-medium text-white">
                  {interruptHeading(selectedInterrupt)}
                </p>
                <p className="mt-1 text-sm leading-6 text-[var(--muted-foreground)]">
                  {interruptSummary(selectedInterrupt)}
                </p>
                <p className="mt-2 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  Last activity {formatRelativeTimeCompact(selectedInterrupt.last_activity_at || selectedInterrupt.requested_at)}
                </p>
              </div>

              {isAlert ? (
                <div className="mt-4 grid gap-4">
                  <div
                    className={cn(
                      'rounded-[calc(var(--panel-radius)-0.2rem)] border px-4 py-4',
                      selectedInterrupt.alert?.severity === 'info' && 'border-sky-400/20 bg-sky-400/10',
                      selectedInterrupt.alert?.severity === 'warning' && 'border-amber-400/20 bg-amber-400/10',
                      (!selectedInterrupt.alert?.severity || selectedInterrupt.alert?.severity === 'error') &&
                        'border-rose-400/20 bg-rose-400/10',
                    )}
                  >
                    <p className="text-lg font-medium text-white">
                      {selectedInterrupt.alert?.title || 'Maestro alert'}
                    </p>
                    <p className="mt-2 max-w-4xl text-sm leading-6 text-white/85">
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
                    {interruptHasAcknowledgeAction(selectedInterrupt) ? (
                      <button
                        className="inline-flex h-11 items-center rounded-2xl border border-white/10 px-4 text-sm font-medium text-white transition hover:border-white/20 hover:bg-white/5 disabled:cursor-not-allowed disabled:text-white/45"
                        disabled={isSubmitting}
                        type="button"
                        onClick={() => {
                          if (isSubmitting) {
                            return
                          }
                          onAcknowledge(selectedInterrupt.id)
                        }}
                      >
                        Acknowledge
                      </button>
                    ) : null}
                  </div>
                </div>
              ) : (
                <form
                  className="mt-4 grid gap-4"
                  onSubmit={(event) => {
                    event.preventDefault()
                    if (!valid || responseLocked) {
                      return
                    }
                    if (isApproval) {
                      const selectedDecision = selectedInterrupt.approval?.decisions.find((option) => option.value === decision)
                      if (!selectedDecision) {
                        return
                      }
                      if (selectedDecision.decision_payload) {
                        respondToSelectedInterrupt({ decision_payload: selectedDecision.decision_payload })
                        return
                      }
                      respondToSelectedInterrupt({ decision: selectedDecision.value })
                      return
                    }
                    respondToSelectedInterrupt({ answers })
                  }}
                >
                  {isApproval ? (
                    <>
                      <div className="grid gap-4">
                        <div className="space-y-2">
                          <p className="text-lg font-medium text-white">{approvalPrompt(selectedInterrupt)}</p>
                          {selectedInterrupt.approval?.reason ? (
                            <p className="max-w-4xl text-sm leading-6 text-[var(--muted-foreground)]">
                              {selectedInterrupt.approval.reason}
                            </p>
                          ) : null}
                          {!canRespondToSelectedInterrupt ? (
                            <p className="max-w-4xl text-sm leading-6 text-amber-100/90">
                              An earlier interrupt is still pending. Review this request now, but wait until it reaches the front of the queue before responding.
                            </p>
                          ) : null}
                        </div>

                        <div className="rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/12 bg-[linear-gradient(180deg,rgba(255,255,255,.03),rgba(0,0,0,.18))] px-4 py-4">
                          <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                            Requested command
                          </p>
                          <code className="mt-3 block overflow-x-auto whitespace-pre-wrap break-all rounded-[calc(var(--panel-radius)-0.45rem)] border border-white/10 bg-black/35 px-4 py-3 font-mono text-[0.96rem] leading-7 text-white">
                            {selectedInterrupt.approval?.command || selectedInterrupt.approval?.reason || 'Operator approval required.'}
                          </code>
                        </div>

                        {selectedInterrupt.approval?.cwd ? (
                          <div className="flex flex-wrap items-center gap-3 rounded-[calc(var(--panel-radius)-0.3rem)] border border-white/8 bg-white/[0.03] px-4 py-3">
                            <span className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                              Working directory
                            </span>
                            <code className="truncate rounded-full border border-white/10 bg-black/25 px-3 py-1.5 font-mono text-xs text-white">
                              {selectedInterrupt.approval.cwd}
                            </code>
                          </div>
                        ) : null}
                      </div>

                      <div className="grid gap-3 border-t border-white/8 pt-4 xl:grid-cols-[minmax(0,1fr)_auto] xl:items-end">
                        <div className="grid gap-3">
                          {approvalGroups && approvalGroups.primary.length > 0 ? (
                            <div className="flex flex-wrap gap-3">
                              {approvalGroups.primary.map((option) => {
                                const selected = decision === option.value
                                return (
                                  <button
                                    key={option.value}
                                    className={cn(
                                      'min-w-[12rem] rounded-[calc(var(--panel-radius)-0.25rem)] border px-4 py-3 text-left transition duration-200',
                                      selected
                                        ? 'border-[var(--accent)]/70 bg-[linear-gradient(135deg,rgba(196,255,87,.28),rgba(255,255,255,.08))] text-white shadow-[0_12px_40px_rgba(196,255,87,.15)]'
                                        : 'border-[var(--accent)]/20 bg-[linear-gradient(135deg,rgba(196,255,87,.16),rgba(255,255,255,.04))] text-white hover:border-[var(--accent)]/45 hover:bg-[linear-gradient(135deg,rgba(196,255,87,.22),rgba(255,255,255,.06))]',
                                    )}
                                    disabled={responseLocked}
                                    type="button"
                                    onClick={() => {
                                      if (responseLocked) {
                                        return
                                      }
                                      updateDecision(option.value)
                                      if (option.decision_payload) {
                                        respondToSelectedInterrupt({ decision_payload: option.decision_payload })
                                        return
                                      }
                                      respondToSelectedInterrupt({ decision: option.value })
                                    }}
                                  >
                                    <p className="text-base font-medium">{option.label}</p>
                                    {option.description ? (
                                      <p className="mt-1.5 text-sm leading-6 text-white/72">{option.description}</p>
                                    ) : null}
                                  </button>
                                )
                              })}
                            </div>
                          ) : null}

                          {approvalGroups && approvalGroups.secondary.length > 0 ? (
                            <div className="flex flex-wrap gap-3">
                              {approvalGroups.secondary.map((option) => {
                                const selected = decision === option.value
                                return (
                                  <button
                                    key={option.value}
                                    className={cn(
                                      'min-w-[13rem] rounded-[calc(var(--panel-radius)-0.25rem)] border px-4 py-3 text-left transition duration-200',
                                      selected
                                        ? 'border-white/25 bg-white/10 text-white'
                                        : 'border-white/12 bg-white/[0.04] text-white hover:border-white/18 hover:bg-white/[0.07]',
                                    )}
                                    disabled={responseLocked}
                                    type="button"
                                    onClick={() => {
                                      if (responseLocked) {
                                        return
                                      }
                                      updateDecision(option.value)
                                      if (option.decision_payload) {
                                        respondToSelectedInterrupt({ decision_payload: option.decision_payload })
                                        return
                                      }
                                      respondToSelectedInterrupt({ decision: option.value })
                                    }}
                                  >
                                    <p className="text-base font-medium">{option.label}</p>
                                    {option.description ? (
                                      <p className="mt-1.5 text-sm leading-6 text-[var(--muted-foreground)]">
                                        {option.description}
                                      </p>
                                    ) : null}
                                  </button>
                                )
                              })}
                            </div>
                          ) : null}
                        </div>

                        {approvalGroups && approvalGroups.destructive.length > 0 ? (
                          <div className="flex flex-wrap justify-start gap-3 xl:justify-end">
                            {approvalGroups.destructive.map((option) => {
                              const selected = decision === option.value
                              return (
                                <button
                                  key={option.value}
                                  className={cn(
                                    'min-w-[10rem] rounded-[calc(var(--panel-radius)-0.25rem)] border px-4 py-3 text-left transition duration-200',
                                    selected
                                      ? 'border-red-400/45 bg-red-500/15 text-white'
                                      : 'border-white/10 bg-black/20 text-white hover:border-red-300/30 hover:bg-red-500/10',
                                  )}
                                  disabled={responseLocked}
                                  type="button"
                                  onClick={() => {
                                    if (responseLocked) {
                                      return
                                    }
                                    updateDecision(option.value)
                                    if (option.decision_payload) {
                                      respondToSelectedInterrupt({ decision_payload: option.decision_payload })
                                      return
                                    }
                                    respondToSelectedInterrupt({ decision: option.value })
                                  }}
                                >
                                  <p className="text-base font-medium">{option.label}</p>
                                  {option.description ? (
                                    <p className="mt-1.5 text-sm leading-6 text-[var(--muted-foreground)]">
                                      {option.description}
                                    </p>
                                  ) : null}
                                </button>
                              )
                            })}
                          </div>
                        ) : null}
                      </div>
                    </>
                  ) : (
                    <>
                      <div className="space-y-2">
                        <p className="text-lg font-medium text-white">Respond to this request</p>
                        <p className="text-sm leading-6 text-[var(--muted-foreground)]">
                          Provide the information the agent needs so it can continue the current turn.
                        </p>
                        {!canRespondToSelectedInterrupt ? (
                          <p className="text-sm leading-6 text-amber-100/90">
                            An earlier interrupt is still pending. You can review these questions now, but responses stay locked until this request reaches the front of the queue.
                          </p>
                        ) : null}
                      </div>

                      <div className="grid gap-3">
                        {questions.map((question) => (
                          <label
                            key={question.id}
                            className="grid gap-2 rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/8 bg-white/[0.03] p-3"
                          >
                            <div className="space-y-1">
                              {question.header ? (
                                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
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
                    </>
                  )}

                  {requiresExplicitSubmit ? (
                    <div className="flex items-center justify-end gap-3 border-t border-white/8 pt-4">
                      <button
                        className={cn(
                          'inline-flex h-11 items-center rounded-2xl border px-4 text-sm font-medium transition',
                          valid && !responseLocked
                            ? 'border-[var(--accent)]/45 bg-[linear-gradient(135deg,rgba(196,255,87,.24),rgba(255,255,255,.06))] text-white hover:border-[var(--accent)]/60'
                            : 'border-white/10 bg-white/5 text-white/45',
                        )}
                        disabled={!valid || responseLocked}
                        type="submit"
                      >
                        {isSubmitting ? 'Submitting...' : 'Submit response'}
                      </button>
                    </div>
                  ) : null}
                </form>
              )}
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}
