import { useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, Layers3, Plus } from 'lucide-react'
import { toast } from 'sonner'

import { IssueCard } from '@/components/dashboard/issue-card'
import { PageHeader } from '@/components/dashboard/page-header'
import { IssuePreviewSheet } from '@/components/dashboard/issue-preview-sheet'
import { IssueDialog } from '@/components/forms'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { api } from '@/lib/api'
import { getStateMeta, groupIssuesByState, issueStatesFor } from '@/lib/dashboard'
import { summaryActiveCount, summaryDoneCount } from '@/lib/projects'
import { appRoutes } from '@/lib/routes'
import type { IssueDetail, IssueState, IssueSummary } from '@/lib/types'
import { formatRelativeTime } from '@/lib/utils'

function StatCard({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <Card>
      <CardContent>
        <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{label}</p>
        <p className="mt-2.5 font-display text-[length:var(--metric-value-size)] leading-none text-white">{value}</p>
        <p className="mt-2 text-sm text-[var(--muted-foreground)]">{detail}</p>
      </CardContent>
    </Card>
  )
}

export function EpicDetailPage() {
  const { epicId } = useParams({ from: '/epics/$epicId' })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [issueDialogOpen, setIssueDialogOpen] = useState(false)
  const [previewIssue, setPreviewIssue] = useState<IssueSummary>()

  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: api.bootstrap })
  const epic = useQuery({ queryKey: ['epic', epicId], queryFn: () => api.getEpic(epicId) })

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['bootstrap'] }),
      queryClient.invalidateQueries({ queryKey: ['epic', epicId] }),
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

  const laneStates = useMemo(() => issueStatesFor(epic.data?.issues.items ?? []), [epic.data?.issues.items])
  const groupedIssues = useMemo(() => groupIssuesByState(epic.data?.issues.items ?? [], laneStates), [epic.data?.issues.items, laneStates])

  if (!bootstrap.data || !epic.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />
  }

  return (
    <div className="grid gap-[var(--section-gap)]">
      <PageHeader
        eyebrow="Epic hub"
        title={epic.data.epic.name}
        description={epic.data.epic.description || 'No epic description yet.'}
        crumbs={[
          { label: 'Projects', to: appRoutes.projects },
          epic.data.project ? { label: epic.data.project.name, to: appRoutes.projectDetail, params: { projectId: epic.data.project.id } } : { label: 'Project' },
          { label: epic.data.epic.name },
        ]}
        actions={
          <>
            <Button variant="secondary" onClick={() => setIssueDialogOpen(true)}>
              <Plus className="size-4" />
              New issue
            </Button>
            {epic.data.project ? (
              <Button onClick={() => void navigate({ to: appRoutes.projectDetail, params: { projectId: epic.data.project!.id } })}>
                Open project
              </Button>
            ) : null}
          </>
        }
        stats={
          <>
            <StatCard label="Issues" value={String(epic.data.issues.items.length)} detail="All work attached to this epic." />
            <StatCard label="Active" value={String(summaryActiveCount(epic.data.epic))} detail="Issues currently in execution states." />
            <StatCard label="Done" value={String(summaryDoneCount(epic.data.epic))} detail="Completed work inside this epic." />
            <StatCard label="Siblings" value={String(epic.data.sibling_epics.length - 1)} detail="Other epics inside the same project." />
          </>
        }
      />

      <div className="grid gap-[var(--section-gap)] lg:grid-cols-[1fr_.9fr]">
        <Card>
          <CardContent>
            <Badge>Recent work</Badge>
            <h2 className="mt-4 text-2xl font-semibold text-white">What changed in this epic</h2>
            <div className="mt-4 grid gap-2.5">
              {epic.data.issues.items.slice(0, 5).map((issue) => (
                <button key={issue.id} className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.04] p-3.5 text-left transition hover:bg-white/[0.07]" onClick={() => setPreviewIssue(issue)}>
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <p className="font-medium text-white">{issue.identifier}</p>
                      <p className="mt-1 text-sm text-[var(--muted-foreground)]">{issue.title}</p>
                    </div>
                    <Badge className="border-white/10 bg-white/5 text-white">{getStateMeta(issue.state).label}</Badge>
                  </div>
                  <p className="mt-3 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{formatRelativeTime(issue.updated_at)}</p>
                </button>
              ))}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardContent>
            <Badge>Sibling epics</Badge>
            <h2 className="mt-4 text-2xl font-semibold text-white">Adjacent delivery arcs</h2>
            <div className="mt-4 grid gap-2.5">
              {epic.data.sibling_epics
                .filter((sibling) => sibling.id !== epic.data.epic.id)
                .map((sibling) => (
                  <Link key={sibling.id} className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.04] p-3.5 transition hover:bg-white/[0.07]" params={{ epicId: sibling.id }} to={appRoutes.epicDetail}>
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <p className="font-medium text-white">{sibling.name}</p>
                        <p className="mt-1 text-sm text-[var(--muted-foreground)]">{sibling.description || 'No description yet.'}</p>
                      </div>
                      <Badge>{summaryActiveCount(sibling)} active</Badge>
                    </div>
                  </Link>
                ))}
              {epic.data.sibling_epics.filter((sibling) => sibling.id !== epic.data.epic.id).length === 0 ? (
                <div className="rounded-[1.25rem] border border-white/8 bg-white/[0.04] p-6 text-sm text-[var(--muted-foreground)]">
                  This is the only epic in the project right now.
                </div>
              ) : null}
            </div>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardContent>
          <div className="flex items-center justify-between gap-3">
            <div>
              <Badge>Epic lanes</Badge>
              <h2 className="mt-4 text-2xl font-semibold text-white">State of work across the epic</h2>
            </div>
            <Link className="inline-flex items-center gap-2 text-sm text-[var(--accent)]" to={appRoutes.work}>
              Open full board
              <ArrowRight className="size-4" />
            </Link>
          </div>
          <div className="mt-4 grid gap-3 lg:grid-cols-3">
            {laneStates.map((state) => (
              <div key={state} className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-white/[0.03] p-3.5">
                <div>
                  <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{getStateMeta(state).label}</p>
                  <p className="mt-2 text-2xl font-semibold text-white">{groupedIssues[state].length}</p>
                </div>
                <div className="mt-4 grid gap-3">
                  {groupedIssues[state].length > 0 ? (
                    groupedIssues[state].map((issue) => <IssueCard key={issue.id} issue={issue} bootstrap={bootstrap.data} compact onOpen={setPreviewIssue} onStateChange={(item, nextState) => stateMutation.mutate({ identifier: item.identifier, nextState })} />)
                  ) : (
                    <div className="flex min-h-[180px] flex-col items-center justify-center rounded-[1.25rem] border border-dashed border-white/10 text-center text-sm text-[var(--muted-foreground)]">
                      <Layers3 className="size-5 text-[var(--accent)]" />
                      <p className="mt-3">No issues in {getStateMeta(state).label.toLowerCase()}.</p>
                    </div>
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
        initial={{ epic_id: epicId, project_id: epic.data.project?.id } as Partial<IssueDetail>}
        projects={bootstrap.data.projects}
        epics={bootstrap.data.epics.filter((candidate) => candidate.project_id === epic.data.project?.id)}
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
        onStateChange={async (identifier, nextState) => {
          await stateMutation.mutateAsync({ identifier, nextState })
        }}
      />
    </div>
  )
}
