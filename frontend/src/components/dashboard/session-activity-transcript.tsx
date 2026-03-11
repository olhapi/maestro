import { useState } from 'react'
import { ChevronDown, ChevronUp } from 'lucide-react'

import type { SessionDisplayHistoryEntry } from '@/lib/types'

function normalizeText(value?: string) {
  return (value ?? '').replace(/\s+/g, ' ').trim().toLowerCase()
}

function visibleEntries(entries: SessionDisplayHistoryEntry[]) {
  return entries.filter((entry) => entry.kind === 'agent' || entry.kind === 'command')
}

function isFinalAnswer(entry: SessionDisplayHistoryEntry) {
  return entry.kind === 'agent' && entry.phase === 'final_answer'
}

function rowMarkerClass(entry: SessionDisplayHistoryEntry) {
  if (isFinalAnswer(entry)) {
    return 'bg-emerald-300 shadow-[0_0_12px_rgba(110,231,183,0.6)]'
  }
  if (entry.kind === 'command') {
    return entry.tone === 'error' ? 'bg-rose-300' : 'bg-white/45'
  }
  return 'bg-sky-300/80'
}

function commandLeadIn(state?: SessionDisplayHistoryEntry['command_state']) {
  switch (state) {
    case 'completed':
      return 'Background terminal finished with'
    case 'failed':
      return 'Background terminal failed with'
    case 'started':
      return 'Background terminal started with'
    default:
      return 'Background terminal streamed output from'
  }
}

function commandText(entry: SessionDisplayHistoryEntry) {
  return entry.command?.trim() || entry.summary.trim() || 'command'
}

function commandSecondarySummary(entry: SessionDisplayHistoryEntry) {
  const summary = entry.summary.trim()
  if (!summary) {
    return ''
  }
  if (normalizeText(summary) === normalizeText(entry.command)) {
    return ''
  }
  return summary
}

function agentBody(entry: SessionDisplayHistoryEntry) {
  return entry.summary.trim() || entry.title.trim() || 'Agent update'
}

function renderCompactCommand(entry: SessionDisplayHistoryEntry) {
  const summary = commandSecondarySummary(entry)

  return (
    <div className="min-w-0">
      <p className="break-all [overflow-wrap:anywhere] text-sm leading-6 text-[var(--muted-foreground)]">
        {commandLeadIn(entry.command_state)}{' '}
        <code className="rounded bg-white/[0.06] px-1 py-0.5 font-mono text-[11px] text-white break-all [overflow-wrap:anywhere]">
          {commandText(entry)}
        </code>
      </p>
      {summary ? (
        <p className="mt-1 whitespace-pre-wrap break-all [overflow-wrap:anywhere] text-sm leading-6 text-white/80">
          {summary}
        </p>
      ) : null}
    </div>
  )
}

function renderAgentUpdate(entry: SessionDisplayHistoryEntry) {
  const finalAnswer = isFinalAnswer(entry)

  return (
    <div className="min-w-0">
      {finalAnswer ? (
        <p className="text-[11px] font-medium uppercase tracking-[0.18em] text-emerald-200/85">
          Final answer
        </p>
      ) : null}
      <p
        className={`whitespace-pre-wrap break-all [overflow-wrap:anywhere] ${
          finalAnswer
            ? 'mt-1.5 text-base font-medium leading-7 text-white'
            : 'text-[15px] leading-6 text-white/92'
        }`}
      >
        {agentBody(entry)}
      </p>
    </div>
  )
}

export function SessionActivityTranscript({
  entries,
  emptyMessage = 'No visible activity captured for this issue yet.',
}: {
  entries: SessionDisplayHistoryEntry[]
  emptyMessage?: string
}) {
  const transcriptEntries = visibleEntries(entries)
  const [expandedRows, setExpandedRows] = useState<Record<string, boolean>>({})

  const toggleHistoryRow = (rowKey: string) => {
    setExpandedRows((current) => ({ ...current, [rowKey]: !current[rowKey] }))
  }

  return (
    <section
      className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5"
      data-testid="activity-log"
    >
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm font-medium text-white">Activity log</p>
        <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
          {transcriptEntries.length} updates
        </span>
      </div>

      <div className="mt-4 space-y-4">
        {transcriptEntries.length === 0 ? (
          <p className="text-sm text-[var(--muted-foreground)]">{emptyMessage}</p>
        ) : (
          transcriptEntries.map((entry, index) => {
            const rowKey = `${entry.id}-${index}`
            const expanded = expandedRows[rowKey] ?? false

            return (
              <article key={rowKey} className="relative pl-4">
                <span
                  className={`absolute top-2.5 left-0 block size-1.5 rounded-full ${rowMarkerClass(entry)}`}
                />

                <div className="flex items-start gap-3">
                  <div className="min-w-0 flex-1">
                    {entry.kind === 'command'
                      ? renderCompactCommand(entry)
                      : renderAgentUpdate(entry)}

                    {entry.detail && expanded ? (
                      <pre className="mt-3 overflow-x-auto whitespace-pre-wrap break-all [overflow-wrap:anywhere] rounded-md border border-white/10 bg-black/35 p-2.5 text-xs leading-5 text-white/88">
                        {entry.detail}
                      </pre>
                    ) : null}
                  </div>

                  {entry.kind === 'command' && entry.expandable ? (
                    <button
                      className="inline-flex h-6 shrink-0 items-center gap-0.5 self-start rounded-sm border border-white/10 bg-white/[0.04] px-1.5 text-[10px] leading-none text-[var(--muted-foreground)] transition hover:bg-white/[0.08] hover:text-white"
                      onClick={() => toggleHistoryRow(rowKey)}
                      type="button"
                    >
                      {expanded ? 'Collapse' : 'Expand'}
                      {expanded ? <ChevronUp className="size-2.5" /> : <ChevronDown className="size-2.5" />}
                    </button>
                  ) : null}
                </div>
              </article>
            )
          })
        )}
      </div>
    </section>
  )
}
