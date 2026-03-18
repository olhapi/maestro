import { useEffect, useMemo, useReducer, useRef } from 'react'

import { Badge } from '@/components/ui/badge'
import type {
  PendingApprovalDecision,
  PendingInterrupt,
  PendingUserInputQuestion,
} from '@/lib/types'
import { cn, formatRelativeTimeCompact, toTitleCase } from '@/lib/utils'

const EMPTY_QUESTIONS: PendingUserInputQuestion[] = []
const ENTER_DURATION_MS = 260
const EXIT_DURATION_MS = 180

type PanelVisibility = 'hidden' | 'entering' | 'visible' | 'exiting'

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

type PanelState = {
  renderedCurrent?: PendingInterrupt
  renderedCount: number
  visibility: PanelVisibility
  interactionId?: string
  decision: string
  draftAnswers: Record<string, string>
}

type PanelAction =
  | { type: 'start-enter'; current: PendingInterrupt; count: number }
  | { type: 'finish-enter' }
  | { type: 'start-exit' }
  | { type: 'finish-exit'; current?: PendingInterrupt; count?: number }
  | { type: 'refresh-current'; current: PendingInterrupt; count: number }
  | { type: 'select-decision'; interruptId: string; decision: string }
  | { type: 'set-draft-answer'; interruptId: string; questionId: string; value: string }

type InterruptResponsePayload = {
  interruptId: string
  decision?: string
  decision_payload?: Record<string, unknown>
  answers?: Record<string, string[]>
}

function createInitialPanelState(targetCurrent: PendingInterrupt | undefined, targetCount: number): PanelState {
  return {
    renderedCurrent: targetCurrent,
    renderedCount: targetCurrent ? targetCount : 0,
    visibility: targetCurrent ? 'visible' : 'hidden',
    interactionId: targetCurrent?.id,
    decision: '',
    draftAnswers: {},
  }
}

function panelStateReducer(state: PanelState, action: PanelAction): PanelState {
  switch (action.type) {
    case 'start-enter':
      return {
        renderedCurrent: action.current,
        renderedCount: action.count,
        visibility: 'entering',
        interactionId: action.current.id,
        decision: '',
        draftAnswers: {},
      }
    case 'finish-enter':
      if (state.visibility !== 'entering') {
        return state
      }
      return {
        ...state,
        visibility: 'visible',
      }
    case 'start-exit':
      if (!state.renderedCurrent || state.visibility === 'exiting') {
        return state
      }
      return {
        ...state,
        visibility: 'exiting',
      }
    case 'finish-exit':
      if (!action.current) {
        return {
          renderedCurrent: undefined,
          renderedCount: 0,
          visibility: 'hidden',
          interactionId: undefined,
          decision: '',
          draftAnswers: {},
        }
      }
      return {
        renderedCurrent: action.current,
        renderedCount: action.count ?? 0,
        visibility: 'entering',
        interactionId: action.current.id,
        decision: '',
        draftAnswers: {},
      }
    case 'refresh-current':
      if (!state.renderedCurrent || state.renderedCurrent.id !== action.current.id) {
        return state
      }
      return {
        ...state,
        renderedCurrent: action.current,
        renderedCount: action.count,
      }
    case 'select-decision': {
      const sameInterrupt = state.interactionId === action.interruptId
      return {
        ...state,
        interactionId: action.interruptId,
        decision: action.decision,
        draftAnswers: sameInterrupt ? state.draftAnswers : {},
      }
    }
    case 'set-draft-answer': {
      const sameInterrupt = state.interactionId === action.interruptId
      return {
        ...state,
        interactionId: action.interruptId,
        decision: sameInterrupt ? state.decision : '',
        draftAnswers: {
          ...(sameInterrupt ? state.draftAnswers : {}),
          [action.questionId]: action.value,
        },
      }
    }
  }

  return state
}

export function GlobalInterruptPanel({
  current,
  count,
  hiddenCurrentId,
  isSubmitting,
  onRespond,
}: {
  current?: PendingInterrupt
  count: number
  hiddenCurrentId?: string | null
  isSubmitting: boolean
  onRespond: (payload: InterruptResponsePayload) => void
}) {
  const targetCurrent = current && current.id !== hiddenCurrentId ? current : undefined
  const targetCount = targetCurrent ? count : Math.max(0, count - (hiddenCurrentId ? 1 : 0))
  const [panelState, dispatch] = useReducer(
    panelStateReducer,
    undefined,
    () => createInitialPanelState(targetCurrent, targetCount),
  )
  const enterTimerRef = useRef<number | null>(null)
  const exitTimerRef = useRef<number | null>(null)
  const pendingTargetRef = useRef<PendingInterrupt | undefined>(targetCurrent)
  const pendingCountRef = useRef(targetCount)

  const clearEnterTimer = () => {
    if (enterTimerRef.current !== null) {
      window.clearTimeout(enterTimerRef.current)
      enterTimerRef.current = null
    }
  }

  const clearExitTimer = () => {
    if (exitTimerRef.current !== null) {
      window.clearTimeout(exitTimerRef.current)
      exitTimerRef.current = null
    }
  }

  useEffect(() => {
    return () => {
      clearEnterTimer()
      clearExitTimer()
    }
  }, [])

  useEffect(() => {
    pendingTargetRef.current = targetCurrent
    pendingCountRef.current = targetCount
  }, [targetCurrent, targetCount])

  useEffect(() => {
    if (!panelState.renderedCurrent && !targetCurrent) {
      return
    }

    if (!panelState.renderedCurrent && targetCurrent) {
      clearEnterTimer()
      clearExitTimer()
      dispatch({ type: 'start-enter', current: targetCurrent, count: targetCount })
      enterTimerRef.current = window.setTimeout(() => {
        dispatch({ type: 'finish-enter' })
        enterTimerRef.current = null
      }, ENTER_DURATION_MS)
      return
    }

    if (panelState.renderedCurrent && !targetCurrent) {
      if (panelState.visibility === 'exiting') {
        return
      }
      clearEnterTimer()
      clearExitTimer()
      dispatch({ type: 'start-exit' })
      exitTimerRef.current = window.setTimeout(() => {
        const nextTarget = pendingTargetRef.current
        const nextCount = pendingCountRef.current
        if (nextTarget) {
          dispatch({ type: 'finish-exit', current: nextTarget, count: nextCount })
          enterTimerRef.current = window.setTimeout(() => {
            dispatch({ type: 'finish-enter' })
            enterTimerRef.current = null
          }, ENTER_DURATION_MS)
        } else {
          dispatch({ type: 'finish-exit' })
        }
        exitTimerRef.current = null
      }, EXIT_DURATION_MS)
      return
    }

    if (!panelState.renderedCurrent || !targetCurrent) {
      return
    }

    if (panelState.renderedCurrent.id === targetCurrent.id) {
      clearExitTimer()
      dispatch({ type: 'refresh-current', current: targetCurrent, count: targetCount })
      if (panelState.visibility === 'exiting' || panelState.visibility === 'hidden') {
        clearEnterTimer()
        dispatch({ type: 'start-enter', current: targetCurrent, count: targetCount })
        enterTimerRef.current = window.setTimeout(() => {
          dispatch({ type: 'finish-enter' })
          enterTimerRef.current = null
        }, ENTER_DURATION_MS)
      }
      return
    }

    clearEnterTimer()
    if (panelState.visibility === 'exiting') {
      return
    }
    clearExitTimer()
    dispatch({ type: 'start-exit' })
    exitTimerRef.current = window.setTimeout(() => {
      const nextTarget = pendingTargetRef.current
      const nextCount = pendingCountRef.current
      if (nextTarget) {
        dispatch({ type: 'finish-exit', current: nextTarget, count: nextCount })
        enterTimerRef.current = window.setTimeout(() => {
          dispatch({ type: 'finish-enter' })
          enterTimerRef.current = null
        }, ENTER_DURATION_MS)
      } else {
        dispatch({ type: 'finish-exit' })
      }
      exitTimerRef.current = null
    }, EXIT_DURATION_MS)
  }, [panelState.renderedCurrent, panelState.visibility, targetCurrent, targetCount])

  const renderedCurrent = panelState.renderedCurrent
  const renderedCount = panelState.renderedCount
  const visibility = panelState.visibility
  const decision = panelState.interactionId === renderedCurrent?.id ? panelState.decision : ''
  const draftAnswers = useMemo(
    () => (panelState.interactionId === renderedCurrent?.id ? panelState.draftAnswers : {}),
    [panelState.draftAnswers, panelState.interactionId, renderedCurrent?.id],
  )
  const questions = renderedCurrent?.user_input?.questions ?? EMPTY_QUESTIONS
  const answers = useMemo(() => buildAnswers(questions, draftAnswers), [draftAnswers, questions])

  if (!renderedCurrent || renderedCount === 0 || visibility === 'hidden') {
    return null
  }

  const requiresExplicitSubmit =
    renderedCurrent.kind === 'user_input' && questions.some((question) => questionHasTextInput(question))
  const valid =
    renderedCurrent.kind === 'approval'
      ? !!decision
      : questions.length > 0 && questions.every((question) => (answers[question.id]?.[0] ?? '').trim().length > 0)
  const moreQueued = Math.max(0, renderedCount - 1)
  const approvalGroups =
    renderedCurrent.kind === 'approval'
      ? approvalDecisionGroups(renderedCurrent.approval?.decisions ?? [])
      : null
  const interactionLocked = isSubmitting || visibility !== 'visible'

  const respondToRenderedCurrent = (payload: Omit<InterruptResponsePayload, 'interruptId'>) => {
    if (interactionLocked) {
      return
    }
    onRespond({ interruptId: renderedCurrent.id, ...payload })
  }

  return (
    <section
      className="sticky top-[4.75rem] z-20 border-b border-white/10 bg-[rgba(9,12,16,0.88)] backdrop-blur-2xl lg:top-[4.6rem]"
      data-testid="global-interrupt-panel"
      data-visibility={visibility}
    >
      <div className="mx-auto w-full max-w-[1600px] px-[var(--shell-padding)] py-4">
        <div
          className={cn(
            'interrupt-panel-shell rounded-[calc(var(--panel-radius)+0.2rem)] border border-white/10 bg-[linear-gradient(180deg,rgba(255,255,255,.055),rgba(255,255,255,.025))] p-[var(--panel-padding)] shadow-[0_24px_80px_rgba(0,0,0,.34)]',
            visibility === 'entering' && 'interrupt-panel-shell-enter',
            visibility === 'exiting' && 'interrupt-panel-shell-exit',
          )}
        >
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="min-w-0 space-y-3">
              <div className="flex flex-wrap items-center gap-2">
                <Badge className="border-amber-400/25 bg-amber-400/12 text-amber-100">
                  {renderedCount} waiting
                </Badge>
                {renderedCurrent.collaboration_mode === 'plan' ? (
                  <Badge className="border-sky-400/25 bg-sky-400/12 text-sky-100">Plan turn</Badge>
                ) : null}
                <Badge className="border-white/10 bg-white/5 text-white">
                  {renderedCurrent.issue_identifier || 'Agent'}
                </Badge>
                {renderedCurrent.phase ? (
                  <Badge className="border-white/10 bg-white/5 text-white">
                    {toTitleCase(renderedCurrent.phase)}
                  </Badge>
                ) : null}
                {renderedCurrent.attempt ? (
                  <Badge className="border-white/10 bg-white/5 text-white">
                    Attempt {renderedCurrent.attempt}
                  </Badge>
                ) : null}
              </div>

              <div className="min-w-0">
                <p className="truncate text-base font-medium text-white">
                  {renderedCurrent.issue_title || renderedCurrent.issue_identifier || 'Running agent'}
                </p>
                <p className="mt-1 text-sm leading-6 text-[var(--muted-foreground)]">
                  {interruptSummary(renderedCurrent)}
                </p>
                <p className="mt-2 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  Last activity {formatRelativeTimeCompact(renderedCurrent.last_activity_at || renderedCurrent.requested_at)}
                </p>
              </div>
            </div>

            {moreQueued > 0 ? (
              <p className="pt-1 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                {moreQueued} more queued
              </p>
            ) : null}
          </div>

          <form
            className="mt-4 grid gap-4 rounded-[var(--panel-radius)] border border-white/8 bg-black/25 p-[var(--panel-padding)]"
            onSubmit={(event) => {
              event.preventDefault()
              if (!valid || interactionLocked) {
                return
              }
              if (renderedCurrent.kind === 'approval') {
                const selectedDecision = renderedCurrent.approval?.decisions.find((option) => option.value === decision)
                if (!selectedDecision) {
                  return
                }
                if (selectedDecision.decision_payload) {
                  respondToRenderedCurrent({ decision_payload: selectedDecision.decision_payload })
                  return
                }
                respondToRenderedCurrent({ decision: selectedDecision.value })
                return
              }
              respondToRenderedCurrent({ answers })
            }}
          >
            {renderedCurrent.kind === 'approval' ? (
              <>
                <div className="grid gap-4">
                  <div className="space-y-2">
                    <p className="text-lg font-medium text-white">{approvalPrompt(renderedCurrent)}</p>
                    {renderedCurrent.approval?.reason ? (
                      <p className="max-w-4xl text-sm leading-6 text-[var(--muted-foreground)]">
                        {renderedCurrent.approval.reason}
                      </p>
                    ) : null}
                  </div>

                  <div className="rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/12 bg-[linear-gradient(180deg,rgba(255,255,255,.03),rgba(0,0,0,.18))] px-4 py-4">
                    <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                      Requested command
                    </p>
                    <code className="mt-3 block overflow-x-auto whitespace-pre-wrap break-all rounded-[calc(var(--panel-radius)-0.45rem)] border border-white/10 bg-black/35 px-4 py-3 font-mono text-[0.96rem] leading-7 text-white">
                      {renderedCurrent.approval?.command || renderedCurrent.approval?.reason || 'Operator approval required.'}
                    </code>
                  </div>

                  {renderedCurrent.approval?.cwd ? (
                    <div className="flex flex-wrap items-center gap-3 rounded-[calc(var(--panel-radius)-0.3rem)] border border-white/8 bg-white/[0.03] px-4 py-3">
                      <span className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                        Working directory
                      </span>
                      <code className="truncate rounded-full border border-white/10 bg-black/25 px-3 py-1.5 font-mono text-xs text-white">
                        {renderedCurrent.approval.cwd}
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
                              disabled={interactionLocked}
                              type="button"
                              onClick={() => {
                                if (interactionLocked) {
                                  return
                                }
                                dispatch({
                                  type: 'select-decision',
                                  interruptId: renderedCurrent.id,
                                  decision: option.value,
                                })
                                if (option.decision_payload) {
                                  respondToRenderedCurrent({ decision_payload: option.decision_payload })
                                  return
                                }
                                respondToRenderedCurrent({ decision: option.value })
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
                              disabled={interactionLocked}
                              type="button"
                              onClick={() => {
                                if (interactionLocked) {
                                  return
                                }
                                dispatch({
                                  type: 'select-decision',
                                  interruptId: renderedCurrent.id,
                                  decision: option.value,
                                })
                                if (option.decision_payload) {
                                  respondToRenderedCurrent({ decision_payload: option.decision_payload })
                                  return
                                }
                                respondToRenderedCurrent({ decision: option.value })
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
                            disabled={interactionLocked}
                            type="button"
                            onClick={() => {
                              if (interactionLocked) {
                                return
                              }
                              dispatch({
                                type: 'select-decision',
                                interruptId: renderedCurrent.id,
                                decision: option.value,
                              })
                              if (option.decision_payload) {
                                respondToRenderedCurrent({ decision_payload: option.decision_payload })
                                return
                              }
                              respondToRenderedCurrent({ decision: option.value })
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
                                disabled={interactionLocked}
                                type="button"
                                onClick={() => {
                                  if (interactionLocked) {
                                    return
                                  }
                                  const nextDraftAnswers = {
                                    ...draftAnswers,
                                    [question.id]: option.label,
                                  }
                                  dispatch({
                                    type: 'set-draft-answer',
                                    interruptId: renderedCurrent.id,
                                    questionId: question.id,
                                    value: option.label,
                                  })
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
                                    respondToRenderedCurrent({ answers: nextAnswers })
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
                          disabled={interactionLocked}
                          placeholder={question.is_secret ? 'Enter response' : 'Type response'}
                          type={question.is_secret ? 'password' : 'text'}
                          value={draftAnswers[question.id] ?? ''}
                          onChange={(event) =>
                            dispatch({
                              type: 'set-draft-answer',
                              interruptId: renderedCurrent.id,
                              questionId: question.id,
                              value: event.target.value,
                            })
                          }
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
                    valid && !interactionLocked
                      ? 'border-[var(--accent)]/45 bg-[linear-gradient(135deg,rgba(196,255,87,.24),rgba(255,255,255,.06))] text-white hover:border-[var(--accent)]/60'
                      : 'border-white/10 bg-white/5 text-white/45',
                  )}
                  disabled={!valid || interactionLocked}
                  type="submit"
                >
                  {isSubmitting ? 'Submitting...' : 'Submit response'}
                </button>
              </div>
            ) : null}
          </form>
        </div>
      </div>
    </section>
  )
}
