import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { ArrowLeft, RotateCcw, Save, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { IssueDialog } from '@/components/forms'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { formatRelativeTime, formatNumber, toTitleCase } from '@/lib/utils'

export function IssueDetailPage() {
  const { identifier } = useParams({ from: '/dashboard/issues/$identifier' })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [editOpen, setEditOpen] = useState(false)
  const [blockersValue, setBlockersValue] = useState('')

  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: api.bootstrap })
  const issue = useQuery({
    queryKey: ['issue', identifier],
    queryFn: () => api.getIssue(identifier),
  })

  useEffect(() => {
    setBlockersValue(issue.data?.blocked_by?.join(', ') ?? '')
  }, [issue.data?.blocked_by])

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['bootstrap'] }),
      queryClient.invalidateQueries({ queryKey: ['issues'] }),
      queryClient.invalidateQueries({ queryKey: ['issue', identifier] }),
    ])
  }

  const retryMutation = useMutation({
    mutationFn: () => api.retryIssue(identifier),
    onSuccess: async () => {
      toast.success('Retry requested')
      await invalidate()
    },
  })

  const deleteMutation = useMutation({
    mutationFn: () => api.deleteIssue(identifier),
    onSuccess: async () => {
      toast.success('Issue deleted')
      await invalidate()
      void navigate({ to: '/dashboard/work' })
    },
  })

  if (!bootstrap.data || !issue.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />
  }

  const session = bootstrap.data.sessions.sessions[issue.data.id]

  return (
    <div className="grid gap-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Button variant="secondary" onClick={() => void navigate({ to: '/dashboard/work' })}>
          <ArrowLeft className="size-4" />
          Back to work
        </Button>
        <div className="flex gap-3">
          <Button variant="secondary" onClick={() => setEditOpen(true)}>
            Edit issue
          </Button>
          <Button variant="secondary" onClick={() => retryMutation.mutate()}>
            <RotateCcw className="size-4" />
            Retry now
          </Button>
          <Button variant="destructive" onClick={() => deleteMutation.mutate()}>
            <Trash2 className="size-4" />
            Delete
          </Button>
        </div>
      </div>

      <div className="grid gap-5 xl:grid-cols-[1.25fr_.75fr]">
        <Card>
          <CardHeader>
            <div>
              <div className="flex flex-wrap items-center gap-2">
                <Badge>{issue.data.identifier}</Badge>
                <Badge>{toTitleCase(issue.data.state)}</Badge>
                <Badge>{issue.data.project_name || 'No project'}</Badge>
                {issue.data.epic_name ? <Badge>{issue.data.epic_name}</Badge> : null}
              </div>
              <CardTitle className="mt-4 text-3xl">{issue.data.title}</CardTitle>
              <CardDescription className="mt-3">
                Updated {formatRelativeTime(issue.data.updated_at)} · Priority {issue.data.priority}
              </CardDescription>
            </div>
          </CardHeader>
          <CardContent className="space-y-6">
            <div className="rounded-[1.75rem] border border-white/8 bg-black/20 p-5">
              <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Description</p>
              <p className="mt-3 whitespace-pre-wrap text-sm leading-7 text-slate-200">{issue.data.description || 'No description provided.'}</p>
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <div className="rounded-[1.75rem] border border-white/8 bg-black/20 p-5">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Workspace</p>
                <p className="mt-3 break-all text-sm text-white">{issue.data.workspace_path || 'Not created yet'}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">Runs: {formatNumber(issue.data.workspace_run_count)}</p>
              </div>
              <div className="rounded-[1.75rem] border border-white/8 bg-black/20 p-5">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">PR & branch</p>
                <p className="mt-3 text-sm text-white">{issue.data.branch_name || 'No branch linked'}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">{issue.data.pr_url || 'No pull request linked'}</p>
              </div>
            </div>
          </CardContent>
        </Card>

        <div className="grid gap-5">
          <Card>
            <CardHeader>
              <div>
                <Badge>Operator controls</Badge>
                <CardTitle className="mt-4">Live adjustments</CardTitle>
              </div>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="grid gap-2">
                <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">State</span>
                <Select
                  value={issue.data.state}
                  onChange={async (event) => {
                    await api.setIssueState(identifier, event.target.value)
                    toast.success('State updated')
                    await invalidate()
                  }}
                >
                  {['backlog', 'ready', 'in_progress', 'in_review', 'done', 'cancelled'].map((value) => (
                    <option key={value} value={value}>
                      {toTitleCase(value)}
                    </option>
                  ))}
                </Select>
              </div>
              <div className="grid gap-2">
                <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Blockers</span>
                <Textarea value={blockersValue} onChange={(event) => setBlockersValue(event.target.value)} />
                <Button
                  variant="secondary"
                  onClick={async () => {
                    await api.setIssueBlockers(
                      identifier,
                      blockersValue
                        .split(',')
                        .map((value) => value.trim())
                        .filter(Boolean),
                    )
                    toast.success('Blockers updated')
                    await invalidate()
                  }}
                >
                  <Save className="size-4" />
                  Save blockers
                </Button>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <div>
                <Badge>Session telemetry</Badge>
                <CardTitle className="mt-4">Execution context</CardTitle>
              </div>
            </CardHeader>
            <CardContent className="space-y-3">
              {session ? (
                <>
                  <div className="rounded-2xl border border-white/8 bg-black/20 p-4">
                    <p className="text-sm text-[var(--muted-foreground)]">Last event</p>
                    <p className="mt-2 font-medium text-white">{session.last_event || 'n/a'}</p>
                    <p className="mt-2 text-sm text-[var(--muted-foreground)]">{session.last_message || 'No message'}</p>
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div className="rounded-2xl border border-white/8 bg-black/20 p-4">
                      <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Turns</p>
                      <p className="mt-2 font-display text-3xl">{session.turns_started}</p>
                    </div>
                    <div className="rounded-2xl border border-white/8 bg-black/20 p-4">
                      <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Tokens</p>
                      <p className="mt-2 font-display text-3xl">{formatNumber(session.total_tokens)}</p>
                    </div>
                  </div>
                </>
              ) : (
                <p className="text-sm text-[var(--muted-foreground)]">No live session currently attached to this issue.</p>
              )}
            </CardContent>
          </Card>
        </div>
      </div>

      <IssueDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        initial={issue.data}
        projects={bootstrap.data.projects}
        epics={bootstrap.data.epics}
        onSubmit={async (body) => {
          await api.updateIssue(identifier, body)
          toast.success('Issue updated')
          await invalidate()
        }}
      />
    </div>
  )
}
