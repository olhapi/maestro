import { useState } from 'react'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { toast } from 'sonner'

import { KanbanBoard } from '@/components/dashboard/kanban-board'
import { PageHeader } from '@/components/dashboard/page-header'
import { IssuePreviewSheet } from '@/components/dashboard/issue-preview-sheet'
import { EpicDialog, IssueDialog } from '@/components/forms'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { api } from '@/lib/api'
import { stateMeta } from '@/lib/dashboard'
import { appRoutes } from '@/lib/routes'
import type { IssueDetail, IssueState, IssueSummary } from '@/lib/types'
import { formatRelativeTime } from '@/lib/utils'

function ProjectStat({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <div className="min-w-0 border-r border-white/8 px-3 py-2.5 last:border-r-0">
      <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{label}</p>
      <p className="mt-1.5 font-display text-2xl text-white">{value}</p>
      <p className="mt-1.5 text-xs leading-4 text-[var(--muted-foreground)] md:line-clamp-2">{detail}</p>
    </div>
  )
}

export function ProjectDetailPage() {
  const { projectId } = useParams({ from: '/projects/$projectId' })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [epicDialogOpen, setEpicDialogOpen] = useState(false)
  const [issueDialogInitial, setIssueDialogInitial] = useState<Partial<IssueDetail>>({ project_id: projectId, state: 'backlog' })
  const [issueDialogOpen, setIssueDialogOpen] = useState(false)
  const [previewIssue, setPreviewIssue] = useState<IssueSummary>()

  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: api.bootstrap })
  const project = useQuery({ queryKey: ['project', projectId], queryFn: () => api.getProject(projectId) })

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['bootstrap'] }),
      queryClient.invalidateQueries({ queryKey: ['project', projectId] }),
      queryClient.invalidateQueries({ queryKey: ['issues'] }),
      queryClient.invalidateQueries({ queryKey: ['projects'] }),
      queryClient.invalidateQueries({ queryKey: ['epics'] }),
    ])
  }

  const stateMutation = useMutation({
    mutationFn: ({ identifier, nextState }: { identifier: string; nextState: IssueState }) => api.setIssueState(identifier, nextState),
    onSuccess: async () => {
      toast.success('Issue moved')
      await invalidate()
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (identifier: string) => api.deleteIssue(identifier),
    onSuccess: async () => {
      toast.success('Issue deleted')
      setPreviewIssue(undefined)
      await invalidate()
    },
  })

  if (!bootstrap.data || !project.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />
  }

  const totalIssues = project.data.issues.items.length

  return (
    <div className="grid gap-5">
      <PageHeader
        title={project.data.project.name}
        description={project.data.project.description || 'No project description yet.'}
        crumbs={[
          { label: 'Projects', to: appRoutes.projects },
          { label: project.data.project.name },
        ]}
        actions={
          <>
            <Button
              variant="secondary"
              onClick={() => {
                setIssueDialogInitial({ project_id: projectId, state: 'backlog' })
                setIssueDialogOpen(true)
              }}
            >
              <Plus className="size-4" />
              New issue
            </Button>
            <Button onClick={() => void navigate({ to: appRoutes.work })}>
              Open work board
            </Button>
          </>
        }
        stats={
          <>
            <ProjectStat label="Issues" value={String(totalIssues)} detail="All work currently attached to this project." />
            <ProjectStat label="Active" value={String(project.data.project.counts.ready + project.data.project.counts.in_progress + project.data.project.counts.in_review)} detail="Issues currently in an execution state." />
            <ProjectStat label="Epics" value={String(project.data.epics.length)} detail="Delivery arcs scoped to this project." />
            <ProjectStat label="Completed" value={String(project.data.project.counts.done)} detail="Closed out work items." />
          </>
        }
        statsClassName="overflow-hidden rounded-[1.5rem] border border-white/10 bg-white/[0.04] md:grid-cols-4 md:gap-0"
      />

      <Card>
        <CardContent className="flex flex-wrap items-center justify-between gap-4 p-5">
          <div className="min-w-0">
            <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Repo binding</p>
            <p className="mt-2 truncate text-sm text-white">{project.data.project.repo_path || 'No repo path configured yet.'}</p>
            <p className="mt-1 text-xs text-[var(--muted-foreground)]">{project.data.project.workflow_path || 'Workflow defaults to <repo>/WORKFLOW.md.'}</p>
          </div>
          <Badge className={project.data.project.orchestration_ready ? 'border-lime-400/30 bg-lime-400/10 text-lime-200' : 'border-amber-400/30 bg-amber-400/10 text-amber-200'}>
            {project.data.project.orchestration_ready ? 'Orchestration ready' : 'Needs repo or workflow setup'}
          </Badge>
        </CardContent>
      </Card>

      <div className="grid gap-5 xl:grid-cols-[1.1fr_.9fr]">
        <Card>
          <CardContent className="p-5">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h2 className="text-2xl font-semibold text-white">Epics driving this project</h2>
              </div>
              <Button
                variant="secondary"
                size="icon"
                className="border-white/12 bg-white/6 text-white hover:bg-white/10"
                aria-label="Create epic"
                title="Create epic"
                onClick={() => setEpicDialogOpen(true)}
              >
                <Plus className="size-4 shrink-0 text-[var(--accent)]" />
              </Button>
            </div>
            <div className="mt-5 grid gap-3">
              {project.data.epics.map((epic) => (
                <div key={epic.id} className="rounded-[1.5rem] border border-white/8 bg-white/[0.04] p-4">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div>
                      <p className="text-lg font-semibold text-white">
                        <Link params={{ epicId: epic.id }} to={appRoutes.epicDetail}>
                          {epic.name}
                        </Link>
                      </p>
                      <p className="mt-2 text-sm leading-6 text-[var(--muted-foreground)]">{epic.description || 'No epic description yet.'}</p>
                    </div>
                    <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-[var(--muted-foreground)]">
                      {epic.counts.ready + epic.counts.in_progress + epic.counts.in_review} active
                    </span>
                  </div>
                  <div className="mt-4 grid grid-cols-4 gap-2 text-xs text-[var(--muted-foreground)]">
                    <div className="rounded-xl border border-white/8 bg-black/20 p-3">Backlog {epic.counts.backlog}</div>
                    <div className="rounded-xl border border-white/8 bg-black/20 p-3">Ready {epic.counts.ready}</div>
                    <div className="rounded-xl border border-white/8 bg-black/20 p-3">In progress {epic.counts.in_progress}</div>
                    <div className="rounded-xl border border-white/8 bg-black/20 p-3">Review {epic.counts.in_review}</div>
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardContent className="p-5">
            <h2 className="text-2xl font-semibold text-white">What changed most recently</h2>
            <div className="mt-5 grid gap-3">
              {project.data.issues.items.slice(0, 5).map((issue) => (
                <button key={issue.id} className="rounded-[1.25rem] border border-white/8 bg-white/[0.04] p-4 text-left transition hover:bg-white/[0.07]" onClick={() => setPreviewIssue(issue)}>
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <p className="font-medium text-white">{issue.identifier}</p>
                      <p className="mt-1 text-sm text-[var(--muted-foreground)]">{issue.title}</p>
                    </div>
                    <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-[var(--muted-foreground)]">{stateMeta[issue.state].label}</span>
                  </div>
                  <p className="mt-3 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{formatRelativeTime(issue.updated_at)}</p>
                </button>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>

      <KanbanBoard
        items={project.data.issues.items}
        bootstrap={bootstrap.data}
        onOpenIssue={setPreviewIssue}
        onMoveIssue={(issue, nextState) => stateMutation.mutate({ identifier: issue.identifier, nextState })}
        onCreateIssue={(nextState) => {
          setIssueDialogInitial({ project_id: projectId, state: nextState ?? 'backlog' })
          setIssueDialogOpen(true)
        }}
      />

      <IssueDialog
        open={issueDialogOpen}
        onOpenChange={setIssueDialogOpen}
        initial={issueDialogInitial}
        projects={bootstrap.data.projects}
        epics={bootstrap.data.epics.filter((epic) => epic.project_id === projectId)}
        onSubmit={async (body) => {
          await api.createIssue(body)
          toast.success('Issue created')
          await invalidate()
        }}
      />

      <EpicDialog
        open={epicDialogOpen}
        onOpenChange={setEpicDialogOpen}
        initial={{ project_id: projectId }}
        projects={[project.data.project]}
        onSubmit={async (body) => {
          await api.createEpic(body)
          toast.success('Epic created')
          await invalidate()
        }}
      />

      <IssuePreviewSheet
        issue={previewIssue}
        bootstrap={bootstrap.data}
        open={Boolean(previewIssue)}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setPreviewIssue(undefined)
        }}
        onInvalidate={invalidate}
        onDelete={async (identifier) => {
          await deleteMutation.mutateAsync(identifier)
        }}
        onStateChange={async (identifier, nextState) => {
          await stateMutation.mutateAsync({ identifier, nextState })
        }}
      />
    </div>
  )
}
