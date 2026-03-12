import { useLayoutEffect, useRef, useState } from 'react'
import { ChevronDown, ChevronUp } from 'lucide-react'

import type { ActivityGroup, ActivityEntry } from '@/lib/types'
import { toTitleCase } from '@/lib/utils'

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

  return `text-[11px] font-medium uppercase tracking-[0.18em] ${headingTone}`
}

function entrySummaryClass(entry: ActivityEntry) {
  if (entry.kind === 'status') {
    return 'mt-1 whitespace-pre-wrap break-all [overflow-wrap:anywhere] text-sm leading-6 text-white/82'
  }

  return 'mt-1.5 whitespace-pre-wrap break-all [overflow-wrap:anywhere] text-[15px] leading-6 text-white/92'
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

export function SessionActivityTranscript({
  groups,
  emptyMessage = 'No visible activity captured for this issue yet.',
}: {
  groups: ActivityGroup[]
  emptyMessage?: string
}) {
  const [expandedRows, setExpandedRows] = useState<Record<string, boolean>>({})
  const scrollContainerRef = useRef<HTMLDivElement | null>(null)
  const totalEntries = groups.reduce((sum, group) => sum + group.entries.length, 0)
  // Track transcript growth without copying the full command output into a dependency key.
  const scrollVersion = groups
    .map((group) =>
      [
        group.attempt,
        group.phase ?? '',
        group.status ?? '',
        ...group.entries.map((entry) =>
          `${entry.id}:${entry.status ?? ''}:${entry.summary.length}:${entry.detail?.length ?? 0}`),
      ].join('|'),
    )
    .join('||')

  const toggleHistoryRow = (rowKey: string) => {
    setExpandedRows((current) => ({ ...current, [rowKey]: !current[rowKey] }))
  }

  useLayoutEffect(() => {
    const node = scrollContainerRef.current
    if (!node || totalEntries === 0) {
      return
    }
    node.scrollTop = node.scrollHeight
  }, [scrollVersion, totalEntries])

  return (
    <section
      className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5"
      data-testid="activity-log"
    >
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm font-medium text-white">Activity log</p>
        <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
          {totalEntries} updates
        </span>
      </div>

      {totalEntries === 0 ? (
        <p className="mt-4 text-sm text-[var(--muted-foreground)]">{emptyMessage}</p>
      ) : (
        <div
          className="mt-4 max-h-[520px] overflow-y-auto pr-1"
          data-testid="activity-log-scroll"
          ref={scrollContainerRef}
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

                    return (
                      <article key={entry.id} className="flex items-start gap-3">
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-2.5">
                            <span className={`block size-1.5 shrink-0 rounded-full ${rowMarkerClass(entry)}`} />
                            <p className={entryHeadingClass(entry)}>{entry.title}</p>
                          </div>

                          <div className="pl-4">
                            <p className={entrySummaryClass(entry)}>{entry.summary}</p>

                            {entry.detail && expanded ? (
                              <pre className="mt-3 overflow-x-auto whitespace-pre-wrap break-all [overflow-wrap:anywhere] rounded-md border border-white/10 bg-black/35 p-2.5 text-xs leading-5 text-white/88">
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
