import { useEffect, useMemo, useRef } from 'react'

import { Button } from '@/components/ui/button'
import { MarkdownText } from '@/components/ui/markdown'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'

type PlanSectionKey = 'summary' | 'questions' | 'assumptions' | 'plan' | 'tests'
type PlanBucketKey = PlanSectionKey | 'intro'

export type ParsedPlanApprovalMarkdown = {
  intro: string
  summary: string
  questions: string
  assumptions: string
  plan: string
  tests: string
  hasStructuredSections: boolean
}

export type PlanApprovalExtraAction = {
  key: string
  label: string
  onClick: () => void
  disabled?: boolean
  variant?: 'secondary' | 'destructive' | 'ghost'
}

const sectionPattern =
  /^\s*(?:#{1,6}\s*)?(summary|questions|assumptions|plan|key changes|implementation changes|test plan|tests)\s*:?\s*$/i

function normalizeSectionKey(raw: string): PlanSectionKey | null {
  const normalized = raw.trim().toLowerCase()

  switch (normalized) {
    case 'summary':
      return 'summary'
    case 'questions':
      return 'questions'
    case 'assumptions':
      return 'assumptions'
    case 'plan':
    case 'key changes':
    case 'implementation changes':
      return 'plan'
    case 'test plan':
    case 'tests':
      return 'tests'
    default:
      return null
  }
}

function trimBucket(value: string[]) {
  return value.join('\n').trim()
}

function isCodeFenceLine(line: string) {
  return /^\s*(?:```|~~~)/.test(line)
}

function parsePlanApprovalMarkdown(markdown: string): ParsedPlanApprovalMarkdown {
  const buckets: Record<PlanBucketKey, string[]> = {
    intro: [],
    summary: [],
    questions: [],
    assumptions: [],
    plan: [],
    tests: [],
  }

  let currentBucket: PlanBucketKey = 'intro'
  let hasStructuredSections = false
  let inCodeBlock = false

  for (const line of markdown.split(/\r?\n/)) {
    if (isCodeFenceLine(line)) {
      buckets[currentBucket].push(line)
      inCodeBlock = !inCodeBlock
      continue
    }

    if (inCodeBlock) {
      buckets[currentBucket].push(line)
      continue
    }

    const match = line.match(sectionPattern)
    const nextBucket = match ? normalizeSectionKey(match[1]) : null

    if (nextBucket) {
      currentBucket = nextBucket
      hasStructuredSections = true
      continue
    }

    buckets[currentBucket].push(line)
  }

  return {
    intro: trimBucket(buckets.intro),
    summary: trimBucket(buckets.summary),
    questions: trimBucket(buckets.questions),
    assumptions: trimBucket(buckets.assumptions),
    plan: trimBucket(buckets.plan),
    tests: trimBucket(buckets.tests),
    hasStructuredSections,
  }
}

function PlanSection({
  content,
  eyebrow,
  title,
  tone = 'default',
}: {
  content: string
  eyebrow: string
  title?: string
  tone?: 'default' | 'questions' | 'assumptions'
}) {
  if (!content.trim()) {
    return null
  }

  return (
    <section
      className={cn(
        'rounded-[calc(var(--panel-radius)-0.2rem)] border p-5',
        tone === 'questions' && 'border-sky-400/25 bg-sky-400/8 text-white',
        tone === 'assumptions' && 'border-white/8 bg-black/20 text-white/90',
        tone === 'default' && 'border-white/10 bg-white/[0.04] text-white',
      )}
    >
      <div className="mb-3 space-y-1.5">
        <p className="text-xs font-medium uppercase tracking-[0.12em] text-white/55">{eyebrow}</p>
        {title ? <h3 className="text-lg font-semibold leading-7 text-white">{title}</h3> : null}
      </div>
      <MarkdownText className="space-y-3 text-[15px] leading-7 text-inherit" content={content} />
    </section>
  )
}

export function PlanApprovalDocument({
  markdown,
  className,
}: {
  markdown: string
  className?: string
}) {
  const parsed = useMemo(() => parsePlanApprovalMarkdown(markdown), [markdown])

  if (!markdown.trim()) {
    return null
  }

  if (!parsed.hasStructuredSections) {
    return (
      <div className={className}>
        <PlanSection content={markdown} eyebrow="Proposed plan" tone="default" />
      </div>
    )
  }

  return (
    <div className={cn('grid gap-4', className)}>
      {parsed.intro ? (
        <div className="rounded-[calc(var(--panel-radius)-0.2rem)] border border-white/8 bg-black/20 p-5 text-[15px] leading-7 text-white/88">
          <MarkdownText className="space-y-3 text-inherit" content={parsed.intro} />
        </div>
      ) : null}
      <PlanSection
        content={parsed.summary}
        eyebrow="Summary"
        title="Review summary"
        tone="default"
      />
      <PlanSection
        content={parsed.questions}
        eyebrow="Questions"
        title="Questions to resolve"
        tone="questions"
      />
      <PlanSection
        content={parsed.assumptions}
        eyebrow="Assumptions"
        title="Assumptions in scope"
        tone="assumptions"
      />
      <PlanSection
        content={parsed.plan}
        eyebrow="Plan"
        title="Implementation plan"
        tone="default"
      />
      <PlanSection
        content={parsed.tests}
        eyebrow="Validation"
        title="Test plan"
        tone="default"
      />
    </div>
  )
}

export function PlanApprovalActionBar({
  approveLabel,
  approveDisabled = false,
  requestChangesDisabled = false,
  note,
  noteDescription = 'Add optional steering notes. A note becomes required if you request changes.',
  noteLabel = 'Review note',
  notePlaceholder = 'Explain what should change in the plan...',
  noteRequired = false,
  noteVisible,
  onApprove,
  onNoteChange,
  onRequestChanges,
  onToggleNote,
  extraActions = [],
  className,
}: {
  approveLabel: string
  approveDisabled?: boolean
  requestChangesDisabled?: boolean
  note: string
  noteDescription?: string
  noteLabel?: string
  notePlaceholder?: string
  noteRequired?: boolean
  noteVisible: boolean
  onApprove: () => void
  onNoteChange: (value: string) => void
  onRequestChanges: () => void
  onToggleNote: () => void
  extraActions?: PlanApprovalExtraAction[]
  className?: string
}) {
  const noteRef = useRef<HTMLTextAreaElement | null>(null)
  const previousNoteVisible = useRef(noteVisible)
  const noteToggleLabel = noteVisible ? 'Hide note' : note.trim().length > 0 ? 'Edit note' : 'Add note'

  useEffect(() => {
    if (noteVisible && !previousNoteVisible.current) {
      noteRef.current?.focus()
    }
    previousNoteVisible.current = noteVisible
  }, [noteVisible])

  return (
    <div className={cn('grid gap-4', className)}>
      {noteVisible ? (
        <label
          className={cn(
            'grid gap-2 rounded-[calc(var(--panel-radius)-0.2rem)] border p-4',
            noteRequired ? 'border-amber-400/30 bg-amber-400/8' : 'border-white/8 bg-white/[0.03]',
          )}
        >
          <div className="space-y-1">
            <p className="text-sm font-medium text-white">{noteLabel}</p>
            <p className={cn('text-sm leading-6', noteRequired ? 'text-amber-100/90' : 'text-[var(--muted-foreground)]')}>
              {noteRequired ? 'A note is required to request changes.' : noteDescription}
            </p>
          </div>
          <Textarea
            ref={noteRef}
            className={cn(noteRequired ? 'border-amber-400/30 focus:border-amber-300' : '')}
            placeholder={notePlaceholder}
            value={note}
            onChange={(event) => {
              onNoteChange(event.target.value)
            }}
          />
        </label>
      ) : null}

      <div className="flex flex-wrap items-center gap-3">
        <Button className="h-11 rounded-2xl px-5" disabled={approveDisabled} type="button" onClick={onApprove}>
          {approveLabel}
        </Button>
        <Button
          className="h-11 rounded-2xl px-5"
          disabled={requestChangesDisabled}
          type="button"
          variant="secondary"
          onClick={onRequestChanges}
        >
          Request changes
        </Button>
        <Button className="h-11 rounded-2xl px-3" type="button" variant="ghost" onClick={onToggleNote}>
          {noteToggleLabel}
        </Button>
      </div>

      {extraActions.length > 0 ? (
        <div className="flex flex-wrap items-center gap-3 border-t border-white/8 pt-3">
          {extraActions.map((action) => (
            <Button
              key={action.key}
              className="h-10 rounded-2xl px-4"
              disabled={action.disabled}
              type="button"
              variant={action.variant ?? 'secondary'}
              onClick={action.onClick}
            >
              {action.label}
            </Button>
          ))}
        </div>
      ) : null}
    </div>
  )
}
