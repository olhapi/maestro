import { useId, useState } from 'react'
import { ChevronDown, ChevronUp, Plus } from 'lucide-react'

import { IssueCard } from '@/components/dashboard/issue-card'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import type { DashboardWorkSource, IssueState, IssueSummary } from '@/lib/types'
import { getSessionForIssue, getStateMeta, issueStatesFor } from '@/lib/dashboard'
import { cn } from '@/lib/utils'

type KanbanBoardMode = 'board' | 'grouped'

type KanbanLane = {
  blocked: number
  items: IssueSummary[]
  live: number
  state: IssueState
}

function stateRowBodyId(boardId: string, state: string) {
  return `${boardId}-${state.replace(/[^a-zA-Z0-9_-]+/g, '-')}`
}

function GroupedKanbanBoard({
  lanes,
  bootstrap,
  onOpenIssue,
  onMoveIssue,
  onCreateIssue,
}: {
  lanes: KanbanLane[]
  bootstrap?: DashboardWorkSource
  onOpenIssue: (issue: IssueSummary) => void
  onMoveIssue: (issue: IssueSummary, nextState: IssueState) => void
  onCreateIssue?: (state?: IssueState) => void
}) {
  const [collapsedRows, setCollapsedRows] = useState<Record<string, boolean>>({})
  const groupedBoardId = useId()

  return (
    <div className="grid gap-[var(--section-gap)]">
      {lanes.map((lane) => {
        const meta = getStateMeta(lane.state)
        const collapsed = collapsedRows[lane.state] ?? false
        const bodyId = stateRowBodyId(groupedBoardId, lane.state)

        return (
          <div
            key={lane.state}
            className="overflow-hidden rounded-[var(--panel-radius)] border border-white/10 bg-[linear-gradient(180deg,rgba(255,255,255,.05),rgba(255,255,255,.02))]"
          >
            <div className={cn('border-b border-white/8 bg-gradient-to-br p-[var(--panel-padding)]', meta.boardTint)}>
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className={cn('text-xs uppercase tracking-[0.22em]', meta.accent)}>{meta.label}</p>
                  <div className="mt-2 flex flex-wrap items-end gap-3">
                    <p className="text-2xl font-semibold leading-none text-white">{lane.items.length}</p>
                    <div className="flex flex-wrap gap-2 text-xs text-[var(--muted-foreground)]">
                      <span>{lane.blocked} blocked</span>
                      <span>{lane.live} live</span>
                    </div>
                  </div>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    aria-controls={bodyId}
                    aria-expanded={!collapsed}
                    aria-label={`${collapsed ? 'Expand' : 'Collapse'} ${meta.label} status row`}
                    className="rounded-full border-white/12 bg-white/6 text-white hover:bg-white/10"
                    onClick={() => {
                      setCollapsedRows((current) => ({
                        ...current,
                        [lane.state]: !current[lane.state],
                      }))
                    }}
                    size="sm"
                    variant="secondary"
                  >
                    {collapsed ? <ChevronDown className="size-4" /> : <ChevronUp className="size-4" />}
                    <span>{collapsed ? 'Expand' : 'Collapse'}</span>
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    className="rounded-full border-white/12 bg-white/6 text-white hover:bg-white/10"
                    onClick={() => onCreateIssue?.(lane.state)}
                  >
                    <Plus className="size-4" />
                    New
                  </Button>
                </div>
              </div>
            </div>

            <div hidden={collapsed} id={bodyId} className="grid gap-2.5 p-[var(--panel-padding)]">
              {!collapsed && (lane.items.length > 0 ? (
                lane.items.map((issue) => (
                  <IssueCard
                    key={issue.id}
                    issue={issue}
                    bootstrap={bootstrap}
                    compact
                    onOpen={onOpenIssue}
                    onStateChange={onMoveIssue}
                  />
                ))
              ) : (
                <button
                  className="flex min-h-28 items-center justify-center rounded-[calc(var(--panel-radius)-0.125rem)] border border-dashed border-white/10 bg-transparent px-4 py-5 text-sm text-[var(--muted-foreground)] transition hover:border-white/20 hover:text-white"
                  onClick={() => onCreateIssue?.(lane.state)}
                >
                  Add the next issue
                </button>
              ))}
            </div>
          </div>
        )
      })}
    </div>
  )
}

export function KanbanBoard({
  items,
  bootstrap,
  onOpenIssue,
  onMoveIssue,
  onCreateIssue,
  mode = 'board',
}: {
  items: IssueSummary[]
  bootstrap?: DashboardWorkSource
  onOpenIssue: (issue: IssueSummary) => void
  onMoveIssue: (issue: IssueSummary, nextState: IssueState) => void
  onCreateIssue?: (state?: IssueState) => void
  mode?: KanbanBoardMode
}) {
  const [dragged, setDragged] = useState<IssueSummary | null>(null)
  const [dropState, setDropState] = useState<IssueState | null>(null)

  const lanes = issueStatesFor(items).map((state) => {
    const laneItems = items.filter((item) => item.state === state)
    return {
      state,
      items: laneItems,
      blocked: laneItems.filter((item) => item.is_blocked).length,
      live: laneItems.filter((item) => getSessionForIssue(bootstrap, item.id, item.identifier)).length,
    }
  })

  if (mode === 'grouped') {
    return (
      <GroupedKanbanBoard
        bootstrap={bootstrap}
        lanes={lanes}
        onCreateIssue={onCreateIssue}
        onMoveIssue={onMoveIssue}
        onOpenIssue={onOpenIssue}
      />
    )
  }

  return (
    <div className="grid">
      <ScrollArea className="rounded-[var(--panel-radius)] border border-white/10 bg-white/[0.03]">
        <div className="flex min-w-max gap-[var(--section-gap)] p-[var(--panel-padding)]">
          {lanes.map((lane) => {
            const meta = getStateMeta(lane.state)
            const activeDrop = dropState === lane.state
            return (
              <div
                key={lane.state}
                className={cn(
                  'flex min-h-[62vh] w-[var(--board-lane-width)] shrink-0 flex-col rounded-[calc(var(--panel-radius)+0.125rem)] border border-white/10 bg-[linear-gradient(180deg,rgba(255,255,255,.05),rgba(255,255,255,.02))] transition lg:max-[1440px]:min-h-[58vh]',
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
                <div
                  className={cn(
                    'sticky top-0 z-10 rounded-t-[calc(var(--panel-radius)+0.125rem)] border-b border-white/8 bg-gradient-to-br p-[var(--panel-padding)] backdrop-blur-xl',
                    meta.boardTint,
                  )}
                >
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <p className={cn('text-xs uppercase tracking-[0.22em]', meta.accent)}>{meta.label}</p>
                      <p className="mt-1.5 text-[1.65rem] font-semibold leading-none text-white">{lane.items.length}</p>
                    </div>
                    <Button
                      variant="secondary"
                      size="sm"
                      className="rounded-full border-white/12 bg-white/6 px-2.5 text-white hover:bg-white/10"
                      onClick={() => onCreateIssue?.(lane.state)}
                    >
                      <Plus className="size-4" />
                      New
                    </Button>
                  </div>
                  <div className="mt-2.5 flex flex-wrap gap-2 text-xs text-[var(--muted-foreground)]">
                    <span>{lane.blocked} blocked</span>
                    <span>{lane.live} live</span>
                  </div>
                </div>

                <div className="flex flex-1 flex-col gap-2.5 p-2.5">
                  {lane.items.map((issue) => (
                    <div
                      key={issue.id}
                      draggable
                      className="cursor-grab active:cursor-grabbing"
                      onDragStart={() => setDragged(issue)}
                      onDragEnd={() => {
                        setDragged(null)
                        setDropState(null)
                      }}
                    >
                      <IssueCard
                        issue={issue}
                        bootstrap={bootstrap}
                        compact
                        className="cursor-grab active:cursor-grabbing"
                        onOpen={onOpenIssue}
                        onStateChange={onMoveIssue}
                      />
                    </div>
                  ))}
                  {lane.items.length === 0 ? (
                    <button
                      className="flex flex-1 items-center justify-center rounded-[calc(var(--panel-radius)-0.125rem)] border border-dashed border-white/10 bg-transparent px-4 py-5 text-sm text-[var(--muted-foreground)] transition hover:border-white/20 hover:text-white"
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
