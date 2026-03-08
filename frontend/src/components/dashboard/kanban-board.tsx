import { useState } from 'react'
import { Activity, AlertTriangle, ArrowRightLeft, Plus } from 'lucide-react'

import { IssueCard } from '@/components/dashboard/issue-card'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import type { BootstrapResponse, IssueState, IssueSummary } from '@/lib/types'
import { issueStates, stateMeta } from '@/lib/dashboard'
import { cn } from '@/lib/utils'

export function KanbanBoard({
  items,
  bootstrap,
  onOpenIssue,
  onMoveIssue,
  onCreateIssue,
}: {
  items: IssueSummary[]
  bootstrap?: BootstrapResponse
  onOpenIssue: (issue: IssueSummary) => void
  onMoveIssue: (issue: IssueSummary, nextState: IssueState) => void
  onCreateIssue?: (state?: IssueState) => void
}) {
  const [dragged, setDragged] = useState<IssueSummary | null>(null)
  const [dropState, setDropState] = useState<IssueState | null>(null)

  const lanes = issueStates.map((state) => {
    const laneItems = items.filter((item) => item.state === state)
    return {
      state,
      items: laneItems,
      blocked: laneItems.filter((item) => item.is_blocked).length,
      live: laneItems.filter((item) => bootstrap?.sessions.sessions[item.id]).length,
    }
  })

  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-[1.75rem] border border-white/10 bg-white/[0.04] p-4">
        <div>
          <p className="text-xs uppercase tracking-[0.22em] text-[var(--muted-foreground)]">Execution board</p>
          <h2 className="mt-2 text-xl font-semibold text-white">Triage, route, and monitor work in one surface</h2>
        </div>
        <div className="flex flex-wrap items-center gap-3 text-sm text-[var(--muted-foreground)]">
          <span className="inline-flex items-center gap-2">
            <Activity className="size-4 text-lime-300" />
            {bootstrap?.overview.snapshot.running.length ?? 0} active runs
          </span>
          <span className="inline-flex items-center gap-2">
            <AlertTriangle className="size-4 text-amber-300" />
            {bootstrap?.overview.snapshot.retrying.length ?? 0} retries queued
          </span>
          <span className="inline-flex items-center gap-2">
            <ArrowRightLeft className="size-4 text-cyan-300" />
            Drag cards between lanes
          </span>
        </div>
      </div>

      <ScrollArea className="rounded-[1.75rem] border border-white/10 bg-white/[0.03]">
        <div className="flex min-w-max gap-4 p-4">
          {lanes.map((lane) => {
            const meta = stateMeta[lane.state]
            const activeDrop = dropState === lane.state
            return (
              <div
                key={lane.state}
                className={cn(
                  'flex min-h-[68vh] w-[320px] shrink-0 flex-col rounded-[1.5rem] border border-white/10 bg-[linear-gradient(180deg,rgba(255,255,255,.05),rgba(255,255,255,.02))] transition',
                  activeDrop && 'border-[var(--accent)]/40 bg-white/[0.06]',
                )}
                onDragOver={(event) => {
                  event.preventDefault()
                  setDropState(lane.state)
                }}
                onDragLeave={() => setDropState((current) => (current === lane.state ? null : current))}
                onDrop={(event) => {
                  event.preventDefault()
                  setDropState(null)
                  if (dragged && dragged.state !== lane.state) {
                    onMoveIssue(dragged, lane.state)
                  }
                  setDragged(null)
                }}
              >
                <div className={cn('sticky top-0 z-10 rounded-t-[1.5rem] border-b border-white/8 bg-gradient-to-br p-4 backdrop-blur-xl', meta.boardTint)}>
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <p className={cn('text-xs uppercase tracking-[0.22em]', meta.accent)}>{meta.label}</p>
                      <p className="mt-2 text-2xl font-semibold text-white">{lane.items.length}</p>
                    </div>
                    <Button
                      variant="secondary"
                      size="sm"
                      className="rounded-full border-white/12 bg-white/6 px-3 text-white hover:bg-white/10"
                      onClick={() => onCreateIssue?.(lane.state)}
                    >
                      <Plus className="size-4" />
                      New
                    </Button>
                  </div>
                  <div className="mt-3 flex flex-wrap gap-2 text-xs text-[var(--muted-foreground)]">
                    <span>{lane.blocked} blocked</span>
                    <span>{lane.live} live</span>
                  </div>
                </div>

                <div className="flex flex-1 flex-col gap-3 p-3">
                  {lane.items.map((issue) => (
                    <div
                      key={issue.id}
                      draggable
                      onDragStart={() => setDragged(issue)}
                      onDragEnd={() => {
                        setDragged(null)
                        setDropState(null)
                      }}
                    >
                      <IssueCard issue={issue} bootstrap={bootstrap} compact onOpen={onOpenIssue} onStateChange={onMoveIssue} />
                    </div>
                  ))}
                  {lane.items.length === 0 ? (
                    <button
                      className="flex flex-1 items-center justify-center rounded-[1.25rem] border border-dashed border-white/10 bg-transparent px-4 py-6 text-sm text-[var(--muted-foreground)] transition hover:border-white/20 hover:text-white"
                      onClick={() => onCreateIssue?.(lane.state)}
                    >
                      Add the next issue
                    </button>
                  ) : null}
                </div>
              </div>
            )
          })}
        </div>
      </ScrollArea>
    </div>
  )
}
