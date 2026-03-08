import { useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, Plus } from 'lucide-react'
import { toast } from 'sonner'

import { IssueCard } from '@/components/dashboard/issue-card'
import { PageHeader } from '@/components/dashboard/page-header'
import { IssuePreviewSheet } from '@/components/dashboard/issue-preview-sheet'
import { IssueDialog } from '@/components/forms'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty'
import { api } from '@/lib/api'
import { groupIssuesByState, issueStates, stateMeta } from '@/lib/dashboard'
import { appRoutes } from '@/lib/routes'
import type { IssueDetail, IssueState, IssueSummary } from '@/lib/types'
import { formatRelativeTime } from '@/lib/utils'

function ProjectStat({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <div className="min-w-0 border-r border-white/8 px-4 py-3 last:border-r-0">
      <p className="text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{label}</p>
      <p className="mt-2 font-display text-3xl text-white">{value}</p>
      <p className="mt-2 text-xs leading-5 text-[var(--muted-foreground)]">{detail}</p>
    </div>
  )
}

export function ProjectDetailPage() {
  const { projectId } = useParams({ from: '/projects/$projectId' })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
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

  const groupedIssues = useMemo(() => groupIssuesByState(project.data?.issues.items ?? []), [project.data?.issues.items])

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
            <Button variant="secondary" onClick={() => setIssueDialogOpen(true)}>
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
        statsClassName="overflow-hidden rounded-[1.5rem] border border-white/10 bg-white/[0.04] xl:grid-cols-4 xl:gap-0"
      />

      <div className="grid gap-5 xl:grid-cols-[1.1fr_.9fr]">
        <Card>
          <CardContent className="p-5">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h2 className="text-2xl font-semibold text-white">Epics driving this project</h2>
              </div>
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

        <Card>
          <CardContent className="p-5">
          <div className="flex items-center justify-between gap-3">
            <div>
              <h2 className="text-2xl font-semibold text-white">Project work mapped by state</h2>
            </div>
            <Link className="inline-flex items-center gap-2 text-sm text-[var(--accent)]" to={appRoutes.work}>
              Open full board
              <ArrowRight className="size-4" />
            </Link>
          </div>
          <div className="mt-5 grid gap-4 xl:grid-cols-3">
            {issueStates.map((state) => (
              <div key={state} className="rounded-[1.5rem] border border-white/8 bg-white/[0.03] p-4">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{stateMeta[state].label}</p>
                    <p className="mt-2 text-2xl font-semibold text-white">{groupedIssues[state].length}</p>
                  </div>
                </div>
                <div className="mt-4 grid gap-3">
                  {groupedIssues[state].length > 0 ? (
                    groupedIssues[state].map((issue) => <IssueCard key={issue.id} issue={issue} bootstrap={bootstrap.data} compact onOpen={setPreviewIssue} onStateChange={(item, nextState) => stateMutation.mutate({ identifier: item.identifier, nextState })} />)
                  ) : (
                    <Empty className="min-h-[180px]">
                      <EmptyHeader>
                        <EmptyMedia variant="icon">
                          <Plus />
                        </EmptyMedia>
                        <EmptyTitle>No issues in {stateMeta[state].label.toLowerCase()}</EmptyTitle>
                        <EmptyDescription>Move work here from the main board or create a fresh issue for this project.</EmptyDescription>
                      </EmptyHeader>
                    </Empty>
                  )}
                </div>
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      <IssueDialog
        open={issueDialogOpen}
        onOpenChange={setIssueDialogOpen}
        initial={{ project_id: projectId } as Partial<IssueDetail>}
        projects={bootstrap.data.projects}
        epics={bootstrap.data.epics.filter((epic) => epic.project_id === projectId)}
        onSubmit={async (body) => {
          await api.createIssue(body)
          toast.success('Issue created')
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
