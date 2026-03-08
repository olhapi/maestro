import { useDeferredValue, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { IssueDialog } from '@/components/forms'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { api } from '@/lib/api'
import type { IssueDetail, IssueSummary } from '@/lib/types'
import { formatRelativeTime, toTitleCase } from '@/lib/utils'

const states = ['backlog', 'ready', 'in_progress', 'in_review', 'done', 'cancelled']

export function WorkPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const deferredSearch = useDeferredValue(search)
  const [state, setState] = useState('')
  const [sort, setSort] = useState('updated_desc')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<IssueDetail | undefined>()

  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: api.bootstrap })
  const issues = useQuery({
    queryKey: ['issues', deferredSearch, state, sort],
    queryFn: () => api.listIssues({ search: deferredSearch, state, sort, limit: 200 }),
  })

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['issues'] }),
      queryClient.invalidateQueries({ queryKey: ['bootstrap'] }),
    ])
  }

  const deleteMutation = useMutation({
    mutationFn: (identifier: string) => api.deleteIssue(identifier),
    onSuccess: async () => {
      toast.success('Issue deleted')
      await invalidate()
    },
  })

  const stateMutation = useMutation({
    mutationFn: ({ identifier, nextState }: { identifier: string; nextState: string }) =>
      api.setIssueState(identifier, nextState),
    onSuccess: async () => {
      await invalidate()
    },
  })

  const board = useMemo(() => {
    const groups = new Map<string, IssueSummary[]>()
    states.forEach((value) => groups.set(value, []))
    for (const issue of issues.data?.items ?? []) {
      groups.get(issue.state)?.push(issue)
    }
    return groups
  }, [issues.data])

  if (!bootstrap.data || !issues.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />
  }

  return (
    <div className="grid gap-5">
      <Card>
        <CardHeader>
          <div>
            <Badge>Work explorer</Badge>
            <CardTitle className="mt-4">High-control issue surface</CardTitle>
            <CardDescription>Filter, edit, triage, and route every issue without leaving the operator dashboard.</CardDescription>
          </div>
          <Button
            onClick={() => {
              setEditing(undefined)
              setDialogOpen(true)
            }}
          >
            <Plus className="size-4" />
            Create issue
          </Button>
        </CardHeader>
        <CardContent className="grid gap-3 lg:grid-cols-[1.4fr_repeat(2,minmax(0,220px))]">
          <Input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search by identifier, title, or description" />
          <Select value={state} onChange={(event) => setState(event.target.value)}>
            <option value="">All states</option>
            {states.map((value) => (
              <option key={value} value={value}>
                {toTitleCase(value)}
              </option>
            ))}
          </Select>
          <Select value={sort} onChange={(event) => setSort(event.target.value)}>
            <option value="updated_desc">Recently updated</option>
            <option value="priority_asc">Highest priority</option>
            <option value="identifier_asc">Identifier A-Z</option>
            <option value="state_asc">State grouping</option>
          </Select>
        </CardContent>
      </Card>

      <Tabs defaultValue="list" className="grid gap-4">
        <TabsList>
          <TabsTrigger value="list">List</TabsTrigger>
          <TabsTrigger value="board">Board</TabsTrigger>
        </TabsList>
        <TabsContent value="list">
          <Card>
            <CardContent className="overflow-x-auto pt-5">
              <table className="w-full min-w-[940px] text-left text-sm">
                <thead className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                  <tr>
                    <th className="pb-4">Issue</th>
                    <th className="pb-4">State</th>
                    <th className="pb-4">Project</th>
                    <th className="pb-4">Epic</th>
                    <th className="pb-4">Priority</th>
                    <th className="pb-4">Updated</th>
                    <th className="pb-4 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {issues.data.items.map((issue) => (
                    <tr key={issue.id} className="border-t border-white/6">
                      <td className="py-4">
                        <button className="text-left" onClick={() => void navigate({ to: '/dashboard/issues/$identifier', params: { identifier: issue.identifier } })}>
                          <p className="font-medium text-white">{issue.identifier}</p>
                          <p className="max-w-[340px] text-sm text-[var(--muted-foreground)]">{issue.title}</p>
                        </button>
                      </td>
                      <td className="py-4">
                        <Select
                          value={issue.state}
                          className="min-w-[160px]"
                          onChange={(event) => stateMutation.mutate({ identifier: issue.identifier, nextState: event.target.value })}
                        >
                          {states.map((value) => (
                            <option key={value} value={value}>
                              {toTitleCase(value)}
                            </option>
                          ))}
                        </Select>
                      </td>
                      <td className="py-4 text-[var(--muted-foreground)]">{issue.project_name || 'Unassigned'}</td>
                      <td className="py-4 text-[var(--muted-foreground)]">{issue.epic_name || 'None'}</td>
                      <td className="py-4">{issue.priority}</td>
                      <td className="py-4 text-[var(--muted-foreground)]">{formatRelativeTime(issue.updated_at)}</td>
                      <td className="py-4">
                        <div className="flex justify-end gap-2">
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
        <TabsContent value="board">
          <div className="grid gap-4 xl:grid-cols-3">
            {states.map((value) => (
              <Card key={value}>
                <CardHeader>
                  <div>
                    <Badge>{toTitleCase(value)}</Badge>
                    <CardTitle className="mt-4">{board.get(value)?.length ?? 0} issues</CardTitle>
                  </div>
                </CardHeader>
                <CardContent className="space-y-3">
                  {(board.get(value) ?? []).map((issue) => (
                    <button
                      key={issue.id}
                      className="block w-full rounded-2xl border border-white/8 bg-black/20 p-4 text-left"
                      onClick={() => void navigate({ to: '/dashboard/issues/$identifier', params: { identifier: issue.identifier } })}
                    >
                      <p className="font-medium text-white">{issue.identifier}</p>
                      <p className="mt-1 text-sm text-[var(--muted-foreground)]">{issue.title}</p>
                    </button>
                  ))}
                </CardContent>
              </Card>
            ))}
          </div>
        </TabsContent>
      </Tabs>

      <IssueDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        initial={editing}
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
    </div>
  )
}
