import { useEffect, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { ExternalLink, GitBranch, RotateCcw, Save, Trash2, Workflow } from 'lucide-react'
import { toast } from 'sonner'

import { IssueDialog } from '@/components/forms'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Select } from '@/components/ui/select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { getRetryForIssue, getSessionForIssue, stateMeta } from '@/lib/dashboard'
import { appRoutes } from '@/lib/routes'
import type { BootstrapResponse, IssueDetail, IssueState, IssueSummary } from '@/lib/types'
import { formatDateTime, formatNumber, formatRelativeTime } from '@/lib/utils'

export function IssuePreviewSheet({
  issue,
  bootstrap,
  open,
  onOpenChange,
  onInvalidate,
  onDelete,
  onStateChange,
}: {
  issue?: IssueSummary
  bootstrap?: BootstrapResponse
  open: boolean
  onOpenChange: (open: boolean) => void
  onInvalidate: () => Promise<void>
  onDelete?: (identifier: string) => Promise<void>
  onStateChange?: (identifier: string, state: IssueState) => Promise<void>
}) {
  const [detail, setDetail] = useState<IssueDetail>()
  const [blockersValue, setBlockersValue] = useState('')
  const [editOpen, setEditOpen] = useState(false)
  const navigate = useNavigate()

  useEffect(() => {
    if (!issue || !open) return
    void api
      .getIssue(issue.identifier)
      .then((next) => {
        setDetail(next)
        setBlockersValue(next.blocked_by?.join(', ') ?? '')
      })
  }, [issue, open])

  const activeIssue = detail ?? issue
  const session = activeIssue ? getSessionForIssue(bootstrap, activeIssue.id) : undefined
  const retry = activeIssue ? getRetryForIssue(bootstrap, activeIssue.id) : undefined

  if (!activeIssue) return null

  return (
    <>
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent className="w-[min(580px,calc(100vw-24px))]">
          <SheetHeader>
            <div className="flex items-start justify-between gap-4 pr-10">
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <Badge>{activeIssue.identifier}</Badge>
                  <Badge className="border-white/12 bg-white/5 text-white">{stateMeta[activeIssue.state].label}</Badge>
                  {activeIssue.project_name ? <Badge className="border-white/12 bg-white/5 text-white">{activeIssue.project_name}</Badge> : null}
                  {activeIssue.epic_name ? <Badge className="border-white/12 bg-white/5 text-white">{activeIssue.epic_name}</Badge> : null}
                </div>
                <SheetTitle className="mt-4 text-2xl">{activeIssue.title}</SheetTitle>
                <SheetDescription>
                  Updated {formatRelativeTime(activeIssue.updated_at)} · Priority {activeIssue.priority}
                </SheetDescription>
              </div>
            </div>
          </SheetHeader>

          <div className="flex-1 space-y-5 overflow-y-auto px-6 py-5">
            <div className="rounded-[1.5rem] border border-white/8 bg-white/[0.04] p-4">
              <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Description</p>
              <p className="mt-3 whitespace-pre-wrap text-sm leading-7 text-[var(--muted-foreground)]">
                {activeIssue.description || 'No description provided.'}
              </p>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <div className="rounded-[1.5rem] border border-white/8 bg-white/[0.04] p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Workspace</p>
                <p className="mt-3 break-all text-sm text-white">{activeIssue.workspace_path || 'Not created yet'}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">{formatNumber(activeIssue.workspace_run_count)} runs</p>
              </div>
              <div className="rounded-[1.5rem] border border-white/8 bg-white/[0.04] p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Execution</p>
                <div className="mt-3 grid gap-2 text-sm text-[var(--muted-foreground)]">
                  <span className="inline-flex items-center gap-2 text-white">
                    <Workflow className="size-4 text-lime-300" />
                    {session ? session.last_event || 'Live session' : 'No live session'}
                  </span>
                  {retry ? <span>Retry at {formatDateTime(retry.due_at)}</span> : <span>No retry scheduled</span>}
                </div>
              </div>
            </div>

            <div className="grid gap-4 rounded-[1.5rem] border border-white/8 bg-white/[0.04] p-4">
              <div className="grid gap-2">
                <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">State</span>
                <Select
                  value={activeIssue.state}
                  onChange={async (event) => {
                    if (!onStateChange) return
                    await onStateChange(activeIssue.identifier, event.target.value as IssueState)
                    const next = await api.getIssue(activeIssue.identifier)
                    setDetail(next)
                  }}
                >
                  {(['backlog', 'ready', 'in_progress', 'in_review', 'done', 'cancelled'] as IssueState[]).map((state) => (
                    <option key={state} value={state}>
                      {stateMeta[state].label}
                    </option>
                  ))}
                </Select>
              </div>

              <div className="grid gap-2">
                <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Blockers</span>
                <Textarea value={blockersValue} onChange={(event) => setBlockersValue(event.target.value)} className="min-h-[96px]" />
                <Button
                  variant="secondary"
                  className="justify-center"
                  onClick={async () => {
                    await api.setIssueBlockers(
                      activeIssue.identifier,
                      blockersValue
                        .split(',')
                        .map((value) => value.trim())
                        .filter(Boolean),
                    )
                    toast.success('Blockers updated')
                    await onInvalidate()
                    const next = await api.getIssue(activeIssue.identifier)
                    setDetail(next)
                  }}
                >
                  <Save className="size-4" />
                  Save blockers
                </Button>
              </div>
            </div>

            <div className="grid gap-3 rounded-[1.5rem] border border-white/8 bg-white/[0.04] p-4 text-sm">
              <div className="flex items-center gap-2 text-white">
                <GitBranch className="size-4 text-[var(--accent)]" />
                {activeIssue.branch_name || 'No branch linked'}
              </div>
              <div className="text-[var(--muted-foreground)]">{activeIssue.pr_url || 'No pull request linked'}</div>
              {activeIssue.pr_url ? (
                <a className="inline-flex items-center gap-2 text-sm text-[var(--accent)]" href={activeIssue.pr_url} rel="noreferrer" target="_blank">
                  Open PR
                  <ExternalLink className="size-4" />
                </a>
              ) : null}
            </div>
          </div>

          <SheetFooter className="flex flex-wrap items-center justify-between gap-3">
            <Button variant="secondary" onClick={() => setEditOpen(true)}>
              Edit issue
            </Button>
            <div className="flex flex-wrap gap-3">
              <Button variant="secondary" onClick={() => void api.retryIssue(activeIssue.identifier).then(onInvalidate)}>
                <RotateCcw className="size-4" />
                Retry now
              </Button>
              {onDelete ? (
                <Button
                  variant="destructive"
                  onClick={async () => {
                    await onDelete(activeIssue.identifier)
                    onOpenChange(false)
                  }}
                >
                  <Trash2 className="size-4" />
                  Delete
                </Button>
              ) : null}
              <Button
                variant="secondary"
                onClick={() => {
                  onOpenChange(false)
                  void navigate({ to: appRoutes.issueDetail, params: { identifier: activeIssue.identifier } })
                }}
              >
                Full page
              </Button>
            </div>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      {bootstrap ? (
        <IssueDialog
          open={editOpen}
          onOpenChange={setEditOpen}
          initial={detail}
          projects={bootstrap.projects}
          epics={bootstrap.epics}
          onSubmit={async (body) => {
            await api.updateIssue(activeIssue.identifier, body)
            toast.success('Issue updated')
            await onInvalidate()
            const next = await api.getIssue(activeIssue.identifier)
            setDetail(next)
          }}
        />
      ) : null}
    </>
  )
}
