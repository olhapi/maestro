import { useEffect, useLayoutEffect, useRef, useState, type UIEvent } from 'react'
import { Check, ChevronDown, ChevronUp, Copy } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { MarkdownText, wrappedOutputClassName } from '@/components/ui/markdown'
import type { ActivityEntry, ActivityGroup } from '@/lib/types'
import { formatDateTime, toTitleCase } from '@/lib/utils'

const autoScrollBottomThreshold = 12

function entryTimestamp(entry: ActivityEntry) {
  return entry.completed_at ?? entry.started_at
}

function rowMarkerClass(entry: ActivityEntry) {
  if (entry.kind === 'agent') {
    return entry.phase === 'final_answer'
      ? 'bg-emerald-300 shadow-[0_0_12px_rgba(110,231,183,0.6)]'
      : 'bg-sky-300/80'
  }
  if (entry.kind === 'command') {
    return entry.tone === 'error' ? 'bg-rose-300' : entry.tone === 'success' ? 'bg-emerald-300' : 'bg-white/45'
  }
  return entry.tone === 'error' ? 'bg-rose-300' : entry.tone === 'success' ? 'bg-emerald-300' : 'bg-amber-200'
}

function entryHeadingClass(entry: ActivityEntry) {
  const headingTone =
    entry.kind === 'agent' && entry.phase === 'final_answer'
      ? 'text-emerald-200/85'
      : 'text-[var(--muted-foreground)]'

  return `min-w-0 break-words [overflow-wrap:anywhere] text-[11px] font-medium uppercase tracking-[0.18em] ${headingTone}`
}

function entrySummaryClass(entry: ActivityEntry) {
  if (entry.kind === 'status') {
    return 'mt-1 min-w-0 line-clamp-3 whitespace-pre-wrap break-words [overflow-wrap:anywhere] text-sm leading-6 text-white/82'
  }

  return 'mt-1.5 min-w-0 line-clamp-3 whitespace-pre-wrap break-words [overflow-wrap:anywhere] text-[15px] leading-6 text-white/92'
}

function groupLabel(group: ActivityGroup) {
  const labels = [`Attempt ${group.attempt}`]
  if (group.phase) {
    labels.push(toTitleCase(group.phase))
  }
  if (group.status) {
    labels.push(toTitleCase(group.status))
  }
  return labels.join(' · ')
}

function extractProposedPlanMarkdown(value: string) {
  const match = value.match(/<proposed_plan>\s*([\s\S]*?)\s*<\/proposed_plan>/i)
  if (!match) {
    return ''
  }

  return match[1].trim()
}

function isScrolledToBottom(node: HTMLDivElement) {
  return node.scrollHeight - node.scrollTop - node.clientHeight <= autoScrollBottomThreshold
}

function scrollToBottom(node: HTMLDivElement) {
  node.scrollTop = Math.max(0, node.scrollHeight - node.clientHeight)
}

async function copyText(value: string) {
  if (typeof navigator !== 'undefined' && typeof navigator.clipboard?.writeText === 'function') {
    try {
      await navigator.clipboard.writeText(value)
      return true
    } catch {
      // Fall through to the legacy copy path below.
    }
  }

  if (typeof document === 'undefined' || !document.body || typeof document.execCommand !== 'function') {
    return false
  }

  const textarea = document.createElement('textarea')
  const activeElement = document.activeElement instanceof HTMLElement ? document.activeElement : null

  textarea.value = value
  textarea.readOnly = true
  textarea.setAttribute('aria-hidden', 'true')
  textarea.style.position = 'fixed'
  textarea.style.top = '0'
  textarea.style.left = '-9999px'
  textarea.style.opacity = '0'
  textarea.style.pointerEvents = 'none'
  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  textarea.setSelectionRange(0, textarea.value.length)

  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    textarea.remove()
    activeElement?.focus()
  }
}

export function SessionActivityTranscript({
  groups,
  emptyMessage = 'No visible activity captured for this issue yet.',
}: {
  groups: ActivityGroup[]
  emptyMessage?: string
}) {
  const [expandedRows, setExpandedRows] = useState<Record<string, boolean>>({})
  const [copied, setCopied] = useState(false)
  const scrollContainerRef = useRef<HTMLDivElement | null>(null)
  const pinnedToBottomRef = useRef(true)
  const totalEntries = groups.reduce((sum, group) => sum + group.entries.length, 0)
  // Track transcript growth without copying the full command output into a dependency key.
  const scrollVersion = groups
    .map((group) =>
      [
        group.attempt,
        group.phase ?? '',
        group.status ?? '',
        ...group.entries.map((entry) =>
          `${entry.id}:${entry.status ?? ''}:${entry.summary.length}:${entry.detail?.length ?? 0}:${entry.started_at ?? ''}:${entry.completed_at ?? ''}`),
      ].join('|'),
    )
    .join('||')

  useEffect(() => {
    if (!copied) {
      return undefined
    }

    const handle = window.setTimeout(() => {
      setCopied(false)
    }, 1400)

    return () => window.clearTimeout(handle)
  }, [copied])

  const toggleHistoryRow = (rowKey: string) => {
    setExpandedRows((current) => ({ ...current, [rowKey]: !current[rowKey] }))
  }

  const copyAll = async () => {
    const copiedText = await copyText(JSON.stringify(groups, null, 2))
    if (copiedText) {
      setCopied(true)
    }
  }

  useLayoutEffect(() => {
    if (totalEntries === 0) {
      pinnedToBottomRef.current = true
      return
    }

    const node = scrollContainerRef.current
    if (!node) {
      return
    }

    const shouldStickToBottom = pinnedToBottomRef.current || isScrolledToBottom(node)
    pinnedToBottomRef.current = shouldStickToBottom

    if (shouldStickToBottom) {
      scrollToBottom(node)
    }
  }, [scrollVersion, totalEntries])

  return (
    <section
      className="min-w-0 overflow-x-hidden rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5"
      data-testid="activity-log"
    >
      <div className="flex min-w-0 items-center justify-between gap-3">
        <p className="text-sm font-medium text-white">Activity log</p>
        <div className="flex items-center gap-2">
          <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
            {totalEntries} updates
          </span>
          <Button
            className="h-8 rounded-full px-3 text-xs"
            onClick={() => {
              void copyAll()
            }}
            size="sm"
            type="button"
            variant="secondary"
          >
            {copied ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
            <span>{copied ? 'Copied' : 'Copy all'}</span>
          </Button>
        </div>
      </div>

      {totalEntries === 0 ? (
        <p className="mt-4 text-sm text-[var(--muted-foreground)]">{emptyMessage}</p>
      ) : (
        <div
          className="mt-4 max-h-[520px] overflow-x-hidden overflow-y-auto pr-1"
          data-testid="activity-log-scroll"
          ref={scrollContainerRef}
          onScroll={(event: UIEvent<HTMLDivElement>) => {
            pinnedToBottomRef.current = isScrolledToBottom(event.currentTarget)
          }}
        >
          <div className="space-y-6">
            {groups.map((group) => (
              <section key={`attempt-${group.attempt}`} className="space-y-4">
                <div className="flex items-center justify-between gap-3 border-b border-white/8 pb-2">
                  <p className="text-xs font-medium uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                    {groupLabel(group)}
                  </p>
                  <span className="text-xs text-[var(--muted-foreground)]">
                    {group.entries.length} entries
                  </span>
                </div>

                <div className="space-y-4">
                  {group.entries.map((entry) => {
                    const expanded = expandedRows[entry.id] ?? false
                    const timestamp = entryTimestamp(entry)
                    const proposedPlanMarkdown =
                      entry.kind === 'agent' && entry.phase === 'final_answer'
                        ? extractProposedPlanMarkdown(entry.summary)
                        : ''
                    const showProposedPlan = proposedPlanMarkdown.length > 0

                    return (
                      <article key={entry.id} className="flex min-w-0 items-start gap-3 overflow-x-hidden">
                        <div className="min-w-0 flex-1">
                          <div className="flex min-w-0 items-center gap-2.5">
                            <span className={`block size-1.5 shrink-0 rounded-full ${rowMarkerClass(entry)}`} />
                            <div className="min-w-0 flex-1">
                              <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
                                <p className={entryHeadingClass(entry)}>{entry.title}</p>
                                {timestamp ? (
                                  <time
                                    className="shrink-0 whitespace-nowrap text-[11px] leading-4 text-[var(--muted-foreground)]"
                                    dateTime={timestamp}
                                    title={formatDateTime(timestamp)}
                                  >
                                    {formatDateTime(timestamp)}
                                  </time>
                                ) : null}
                              </div>
                            </div>
                          </div>

                          <div className="pl-4">
                            {showProposedPlan ? (
                              <div
                                className="rounded-md border border-sky-400/25 bg-sky-400/10 p-3 text-sm leading-6 text-sky-50/92"
                                data-testid="proposed-plan-callout"
                              >
                                <p className="mb-2 text-[10px] font-medium uppercase tracking-[0.18em] text-sky-100/80">
                                  Proposed plan
                                </p>
                                <MarkdownText content={proposedPlanMarkdown} />
                              </div>
                            ) : (
                              <MarkdownText className={entrySummaryClass(entry)} content={entry.summary} />
                            )}

                            {entry.detail && expanded ? (
                              <pre className={`${wrappedOutputClassName} mt-3 rounded-md border border-white/10 bg-black/35 p-2.5 text-xs leading-5 text-white/88`}>
                                {entry.detail}
                              </pre>
                            ) : null}
                          </div>
                        </div>

                        {entry.expandable ? (
                          <button
                            className="inline-flex h-6 w-20 shrink-0 items-center justify-center gap-1 self-start rounded-sm border border-white/10 bg-white/[0.04] px-1.5 text-[10px] leading-none text-[var(--muted-foreground)] transition hover:bg-white/[0.08] hover:text-white"
                            onClick={() => toggleHistoryRow(entry.id)}
                            type="button"
                          >
                            {expanded ? 'Collapse' : 'Expand'}
                            {expanded ? <ChevronUp className="size-2.5" /> : <ChevronDown className="size-2.5" />}
                          </button>
                        ) : null}
                      </article>
                    )
                  })}
                </div>
              </section>
            ))}
          </div>
        </div>
      )}
    </section>
  )
}
