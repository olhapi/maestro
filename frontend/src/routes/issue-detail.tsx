import { useState } from 'react'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RotateCcw, Save, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { PageHeader } from '@/components/dashboard/page-header'
import { IssueDialog } from '@/components/forms'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { appRoutes } from '@/lib/routes'
import { getStateMeta, issueStatesFor } from '@/lib/dashboard'
import type { IssueState } from '@/lib/types'
import { formatDateTime, formatNumber, formatRelativeTime, toTitleCase } from '@/lib/utils'

export function IssueDetailPage() {
  const { identifier } = useParams({ from: '/issues/$identifier' })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [editOpen, setEditOpen] = useState(false)
  const [blockersDraft, setBlockersDraft] = useState<string | null>(null)

  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: api.bootstrap })
  const issue = useQuery({
    queryKey: ['issue', identifier],
    queryFn: () => api.getIssue(identifier),
  })
  const execution = useQuery({
    queryKey: ['issue-execution', identifier],
    queryFn: () => api.getIssueExecution(identifier),
    refetchInterval: (query) => {
      if (query.state.data?.active) {
        return 1500
      }
      if (query.state.data?.retry_state === 'scheduled') {
        return 5000
      }
      return false
    },
    refetchIntervalInBackground: true,
  })

  const invalidate = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['bootstrap'] }),
      queryClient.invalidateQueries({ queryKey: ['issues'] }),
      queryClient.invalidateQueries({ queryKey: ['issue', identifier] }),
      queryClient.invalidateQueries({ queryKey: ['issue-execution', identifier] }),
      queryClient.invalidateQueries({ queryKey: ['project'] }),
      queryClient.invalidateQueries({ queryKey: ['epic'] }),
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
      void navigate({ to: appRoutes.work })
    },
  })

  if (!bootstrap.data || !issue.data || !execution.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />
  }

  const blockersValue = blockersDraft ?? issue.data.blocked_by?.join(', ') ?? ''
  const session = execution.data.session
  const sessionHistory = session?.history?.slice(-8) ?? []
  const runtimeEvents = execution.data.runtime_events.slice(-8)
  const availableStates = issueStatesFor(bootstrap.data.issues.items, [issue.data.state])
  const sessionStatusLabel = execution.data.failure_class === 'run_interrupted'
    ? 'Interrupted'
    : execution.data.active
      ? 'Active session'
      : 'Idle'
  const sessionHeadline = execution.data.failure_class === 'run_interrupted'
    ? 'Last run interrupted'
    : session?.last_event || 'No app-server session recorded'
  const sessionMessage = (() => {
    if (execution.data.session_source === 'persisted' && session?.last_timestamp) {
      return `Last session update ${formatRelativeTime(session.last_timestamp)}`
    }
    if (execution.data.failure_class === 'run_interrupted') {
      return 'The last known execution ended without a live completion signal.'
    }
    return session?.last_message || 'No message'
  })()

  return (
    <div className="grid gap-5">
      <PageHeader
        eyebrow="Issue detail"
        title={issue.data.title}
        description={issue.data.description || 'No description provided.'}
        crumbs={[
          { label: 'Work', to: appRoutes.work },
          issue.data.project_id && issue.data.project_name ? { label: issue.data.project_name, to: appRoutes.projectDetail, params: { projectId: issue.data.project_id } } : { label: 'Issue' },
          issue.data.epic_id && issue.data.epic_name ? { label: issue.data.epic_name, to: appRoutes.epicDetail, params: { epicId: issue.data.epic_id } } : { label: issue.data.identifier },
          { label: issue.data.identifier },
        ]}
        actions={
          <>
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
          </>
        }
      />

      <div className="flex flex-wrap gap-2">
        <Badge>{issue.data.identifier}</Badge>
        <Badge className="border-white/10 bg-white/5 text-white">{getStateMeta(issue.data.state).label}</Badge>
        {issue.data.project_name ? (
          <Badge className="border-white/10 bg-white/5 text-white">
            <Link params={{ projectId: issue.data.project_id! }} to={appRoutes.projectDetail}>
              {issue.data.project_name}
            </Link>
          </Badge>
        ) : null}
        {issue.data.epic_name ? (
          <Badge className="border-white/10 bg-white/5 text-white">
            <Link params={{ epicId: issue.data.epic_id! }} to={appRoutes.epicDetail}>
              {issue.data.epic_name}
            </Link>
          </Badge>
        ) : null}
      </div>

      <div className="grid gap-5 xl:grid-cols-[1.2fr_.8fr]">
        <div className="grid gap-5">
          <Card>
            <CardContent className="grid gap-4 p-5 md:grid-cols-3">
              <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Updated</p>
                <p className="mt-3 text-white">{formatRelativeTime(issue.data.updated_at)}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">{formatDateTime(issue.data.updated_at)}</p>
              </div>
              <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Workspace</p>
                <p className="mt-3 break-all text-white">{issue.data.workspace_path || 'Not created yet'}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">Runs: {formatNumber(issue.data.workspace_run_count)}</p>
              </div>
              <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Branch / PR</p>
                <p className="mt-3 text-white">{issue.data.branch_name || 'No branch linked'}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">{issue.data.pr_url || 'No pull request linked'}</p>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Description</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="whitespace-pre-wrap text-sm leading-7 text-[var(--muted-foreground)]">{issue.data.description || 'No description provided.'}</p>
            </CardContent>
          </Card>
        </div>

        <div className="grid gap-5">
          <Card>
            <CardHeader>
              <CardTitle>Live adjustments</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="grid gap-2">
                <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">State</span>
                <Select
                  value={issue.data.state}
                  onChange={async (event) => {
                    await api.setIssueState(identifier, event.target.value as IssueState)
                    toast.success('State updated')
                    await invalidate()
                  }}
                >
                  {availableStates.map((value) => (
                    <option key={value} value={value}>
                      {getStateMeta(value).label}
                    </option>
                  ))}
                </Select>
              </div>

              <div className="grid gap-2">
                <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Blockers</span>
                <Textarea value={blockersValue} onChange={(event) => setBlockersDraft(event.target.value)} className="min-h-[120px]" />
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
                    setBlockersDraft(null)
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
              <CardTitle>Execution triage</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="flex flex-wrap gap-2">
                <Badge className="border-white/10 bg-white/5 text-white">{sessionStatusLabel}</Badge>
                <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(execution.data.retry_state)}</Badge>
                <Badge className="border-white/10 bg-white/5 text-white">Attempt {execution.data.attempt_number || 0}</Badge>
                <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(execution.data.phase || 'implementation')}</Badge>
                {execution.data.failure_class ? (
                  <Badge className="border-rose-400/20 bg-rose-400/10 text-rose-100">{toTitleCase(execution.data.failure_class)}</Badge>
                ) : null}
                {execution.data.next_retry_at ? (
                  <Badge className="border-amber-400/20 bg-amber-400/10 text-amber-100">
                    Retry {formatRelativeTime(execution.data.next_retry_at)}
                  </Badge>
                ) : null}
              </div>

              {execution.data.current_error ? (
                <div className="rounded-[1.5rem] border border-rose-400/15 bg-rose-400/10 p-4 text-sm text-rose-100">
                  <p className="text-xs uppercase tracking-[0.18em] text-rose-200/80">Current error</p>
                  <p className="mt-2 whitespace-pre-wrap break-words">{execution.data.current_error}</p>
                </div>
              ) : null}

              <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <p className="text-sm text-[var(--muted-foreground)]">Session snapshot</p>
                    <p className="mt-2 font-medium text-white">{sessionHeadline}</p>
                    <p className="mt-2 text-sm text-[var(--muted-foreground)]">{sessionMessage}</p>
                  </div>
                  <Badge className="border-white/10 bg-white/5 text-white">{toTitleCase(execution.data.session_source)}</Badge>
                </div>
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                  <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Turns</p>
                  <p className="mt-2 font-display text-3xl text-white">{session?.turns_started ?? 0}</p>
                  <p className="mt-2 text-sm text-[var(--muted-foreground)]">Completed: {formatNumber(session?.turns_completed)}</p>
                </div>
                <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                  <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Tokens</p>
                  <p className="mt-2 font-display text-3xl text-white">{formatNumber(session?.total_tokens)}</p>
                  <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                    Updated: {session ? formatDateTime(session.last_timestamp) : 'n/a'}
                  </p>
                </div>
              </div>

              <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                <div className="flex items-center justify-between gap-3">
                  <p className="text-sm font-medium text-white">Recent session history</p>
                  <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                    {sessionHistory.length} events
                  </span>
                </div>
                <div className="mt-4 space-y-3">
                  {sessionHistory.length === 0 ? (
                    <p className="text-sm text-[var(--muted-foreground)]">No session history captured for this issue yet.</p>
                  ) : (
                    sessionHistory.map((event, index) => (
                      <div key={`${event.type}-${event.turn_id || index}`} className="rounded-2xl border border-white/8 bg-white/[0.03] p-3">
                        <div className="flex items-center justify-between gap-3">
                          <p className="text-sm font-medium text-white">{event.type}</p>
                          <span className="text-xs text-[var(--muted-foreground)]">{formatNumber(event.total_tokens)} tokens</span>
                        </div>
                        <p className="mt-2 text-sm text-[var(--muted-foreground)]">{event.message || 'No message'}</p>
                      </div>
                    ))
                  )}
                </div>
              </div>

              <div className="rounded-[1.5rem] border border-white/8 bg-black/20 p-4">
                <div className="flex items-center justify-between gap-3">
                  <p className="text-sm font-medium text-white">Runtime events</p>
                  <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">
                    {execution.data.runtime_events.length} tracked
                  </span>
                </div>
                <div className="mt-4 space-y-3">
                  {runtimeEvents.length === 0 ? (
                    <p className="text-sm text-[var(--muted-foreground)]">No persisted runtime events for this issue yet.</p>
                  ) : (
                    runtimeEvents.map((event) => (
                      <div key={event.seq} className="rounded-2xl border border-white/8 bg-white/[0.03] p-3">
                        <div className="flex items-center justify-between gap-3">
                          <p className="text-sm font-medium text-white">{toTitleCase(event.kind)}</p>
                          <span className="text-xs text-[var(--muted-foreground)]">{formatDateTime(event.ts)}</span>
                        </div>
                        <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                          {[event.phase && toTitleCase(event.phase), event.attempt ? `Attempt ${event.attempt}` : '', event.delay_type && toTitleCase(event.delay_type)]
                            .filter(Boolean)
                            .join(' · ') || 'Execution signal'}
                        </p>
                        {event.error ? <p className="mt-2 text-sm text-rose-100">{event.error}</p> : null}
                      </div>
                    ))
                  )}
                </div>
              </div>
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
