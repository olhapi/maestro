import { useDeferredValue, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { List, Pencil, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { KanbanBoard } from '@/components/dashboard/kanban-board'
import { PageHeader } from '@/components/dashboard/page-header'
import { IssuePreviewSheet } from '@/components/dashboard/issue-preview-sheet'
import { IssueDialog } from '@/components/forms'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { api } from '@/lib/api'
import { issueStates, stateMeta } from '@/lib/dashboard'
import { appRoutes } from '@/lib/routes'
import type { BootstrapResponse, IssueDetail, IssueState, IssueSummary } from '@/lib/types'
import { formatRelativeTime } from '@/lib/utils'

function StatCard({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <Card className="bg-white/[0.04]">
      <CardContent className="p-5">
        <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{label}</p>
        <p className="mt-3 font-display text-4xl font-semibold text-white">{value}</p>
        <p className="mt-2 text-sm text-[var(--muted-foreground)]">{detail}</p>
      </CardContent>
    </Card>
  )
}

export function WorkPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const deferredSearch = useDeferredValue(search)
  const [state, setState] = useState('')
  const [sort, setSort] = useState('updated_desc')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<IssueDetail | undefined>()
  const [composerDefaults, setComposerDefaults] = useState<Partial<IssueDetail>>({
    state: 'backlog',
  })
  const [previewIssue, setPreviewIssue] = useState<IssueSummary>()

  const issuesKey = ['issues', deferredSearch, state, sort] as const
  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: api.bootstrap })
  const issues = useQuery({
    queryKey: issuesKey,
    queryFn: () => api.listIssues({ search: deferredSearch, state, sort, limit: 200 }),
  })

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['issues'] }),
      queryClient.invalidateQueries({ queryKey: ['bootstrap'] }),
    ])
  }

  const patchIssueState = (payload: { identifier: string; nextState: IssueState }) => {
    const cached = queryClient.getQueryData<{ items: IssueSummary[]; total: number; limit: number; offset: number }>(issuesKey)
    const nextItems = cached?.items.map((item) =>
      item.identifier === payload.identifier ? { ...item, state: payload.nextState, updated_at: new Date().toISOString() } : item,
    )
    if (cached && nextItems) {
      queryClient.setQueryData(issuesKey, { ...cached, items: nextItems })
    }
    const cachedBootstrap = queryClient.getQueryData<BootstrapResponse>(['bootstrap'])
    if (cachedBootstrap) {
      queryClient.setQueryData(['bootstrap'], {
        ...cachedBootstrap,
        issues: {
          ...cachedBootstrap.issues,
          items: cachedBootstrap.issues.items.map((item) =>
            item.identifier === payload.identifier ? { ...item, state: payload.nextState, updated_at: new Date().toISOString() } : item,
          ),
        },
      })
    }
    return { cached, cachedBootstrap }
  }

  const stateMutation = useMutation({
    mutationFn: ({ identifier, nextState }: { identifier: string; nextState: IssueState }) =>
      api.setIssueState(identifier, nextState),
    onMutate: async (payload) => patchIssueState(payload),
    onError: (_error, _vars, context) => {
      if (context?.cached) queryClient.setQueryData(issuesKey, context.cached)
      if (context?.cachedBootstrap) queryClient.setQueryData(['bootstrap'], context.cachedBootstrap)
      toast.error('Unable to move issue')
    },
    onSuccess: async () => {
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

  const metrics = useMemo(() => {
    const data = bootstrap.data?.overview.board
    return {
      active: (data?.ready ?? 0) + (data?.in_progress ?? 0) + (data?.in_review ?? 0),
      done: data?.done ?? 0,
      backlog: data?.backlog ?? 0,
      live: bootstrap.data?.overview.snapshot.running.length ?? 0,
    }
  }, [bootstrap.data])

  if (!bootstrap.data || !issues.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />
  }

  return (
    <div className="grid gap-5">
      <PageHeader
        title="Coordinate delivery without leaving the board"
        description="This surface is now optimized for live triage: drag work between lanes, inspect execution context in-place, and dive into full issue pages only when needed."
        actions={
          <>
            <Button
              variant="secondary"
              onClick={() => {
                setEditing(undefined)
                setComposerDefaults({
                  state: 'backlog',
                  project_id: bootstrap.data?.projects[0]?.id,
                })
                setDialogOpen(true)
              }}
            >
              <Plus className="size-4" />
              Create issue
            </Button>
            <Button onClick={() => void navigate({ to: appRoutes.projects })}>Open portfolio</Button>
          </>
        }
        stats={
          <>
            <StatCard label="Active work" value={String(metrics.active)} detail="Ready, in progress, and in review across the portfolio." />
            <StatCard label="Backlog" value={String(metrics.backlog)} detail="Planned work not yet routed into execution." />
            <StatCard label="Completed" value={String(metrics.done)} detail="Issues already closed out successfully." />
            <StatCard label="Live sessions" value={String(metrics.live)} detail="Issues currently attached to a running workspace." />
          </>
        }
      />

      <Card>
        <CardHeader className="flex-col gap-4 lg:flex-row lg:items-center">
          <div>
            <CardTitle>Filter the board without losing spatial context</CardTitle>
          </div>
          <div className="grid w-full gap-3 lg:grid-cols-[1.4fr_repeat(2,minmax(0,220px))]">
            <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search by identifier, title, or description" />
            <Select value={state} onChange={(event) => setState(event.target.value)}>
              <option value="">All states</option>
              {issueStates.map((value) => (
                <option key={value} value={value}>
                  {stateMeta[value].label}
                </option>
              ))}
            </Select>
            <Select value={sort} onChange={(event) => setSort(event.target.value)}>
              <option value="updated_desc">Recently updated</option>
              <option value="priority_asc">Highest priority</option>
              <option value="identifier_asc">Identifier A-Z</option>
              <option value="state_asc">State grouping</option>
            </Select>
          </div>
        </CardHeader>
      </Card>

      <Tabs defaultValue="board" className="grid gap-4">
        <TabsList>
          <TabsTrigger value="board">Board</TabsTrigger>
          <TabsTrigger value="list">List</TabsTrigger>
        </TabsList>

        <TabsContent value="board" className="m-0">
          <KanbanBoard
            items={issues.data.items}
            bootstrap={bootstrap.data}
            onOpenIssue={setPreviewIssue}
            onMoveIssue={(issue, nextState) => stateMutation.mutate({ identifier: issue.identifier, nextState })}
            onCreateIssue={(nextState) => {
              setEditing(undefined)
              setComposerDefaults({
                state: nextState ?? 'backlog',
                project_id: bootstrap.data?.projects[0]?.id,
              })
              setDialogOpen(true)
            }}
          />
        </TabsContent>

        <TabsContent value="list" className="m-0">
          <Card>
            <CardContent className="overflow-x-auto pt-5">
              <table className="w-full min-w-[960px] text-left text-sm">
                <thead className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  <tr>
                    <th className="pb-4">Issue</th>
                    <th className="pb-4">State</th>
                    <th className="pb-4">Project</th>
                    <th className="pb-4">Epic</th>
                    <th className="pb-4">Updated</th>
                    <th className="pb-4 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {issues.data.items.map((issue) => (
                    <tr key={issue.id} className="border-t border-white/6">
                      <td className="py-4">
                        <button className="text-left" onClick={() => setPreviewIssue(issue)}>
                          <p className="font-medium text-white">{issue.identifier}</p>
                          <p className="max-w-[420px] text-sm text-[var(--muted-foreground)]">{issue.title}</p>
                        </button>
                      </td>
                      <td className="py-4">
                        <Badge className="border-white/10 bg-white/5 text-white">{stateMeta[issue.state].label}</Badge>
                      </td>
                      <td className="py-4 text-[var(--muted-foreground)]">
                        {issue.project_id ? (
                          <Link params={{ projectId: issue.project_id }} to={appRoutes.projectDetail}>
                            {issue.project_name || 'Unassigned'}
                          </Link>
                        ) : (
                          'Unassigned'
                        )}
                      </td>
                      <td className="py-4 text-[var(--muted-foreground)]">
                        {issue.epic_id ? (
                          <Link params={{ epicId: issue.epic_id }} to={appRoutes.epicDetail}>
                            {issue.epic_name || 'None'}
                          </Link>
                        ) : (
                          'None'
                        )}
                      </td>
                      <td className="py-4 text-[var(--muted-foreground)]">{formatRelativeTime(issue.updated_at)}</td>
                      <td className="py-4">
                        <div className="flex justify-end gap-2">
                          <Button variant="ghost" size="icon" onClick={() => setPreviewIssue(issue)}>
                            <List className="size-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={async () => {
                              const detail = await api.getIssue(issue.identifier)
                              setEditing(detail)
                              setDialogOpen(true)
                            }}
                          >
                            <Pencil className="size-4" />
                          </Button>
                          <Button variant="ghost" size="icon" onClick={() => deleteMutation.mutate(issue.identifier)}>
                            <Trash2 className="size-4" />
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      <IssueDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        initial={editing ?? composerDefaults}
        projects={bootstrap.data.projects}
        epics={bootstrap.data.epics}
        onSubmit={async (body) => {
          if (editing) {
            await api.updateIssue(editing.identifier, body)
            toast.success('Issue updated')
          } else {
            await api.createIssue(body)
            toast.success('Issue created')
          }
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
