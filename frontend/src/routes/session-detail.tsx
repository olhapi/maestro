import { Link, useParams } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'

import { PageHeader } from '@/components/dashboard/page-header'
import { SessionExecutionCard } from '@/components/dashboard/session-execution-card'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { api } from '@/lib/api'
import { getStateMeta } from '@/lib/dashboard'
import { appRoutes } from '@/lib/routes'
import { formatDateTime, formatRelativeTime } from '@/lib/utils'

export function SessionDetailPage() {
  const { identifier } = useParams({ from: '/sessions/$identifier' })

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

  if (!issue.data || !execution.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />
  }

  return (
    <div className="grid gap-[var(--section-gap)]">
      <PageHeader
        title={issue.data.title}
        description={issue.data.description || 'No description provided.'}
        descriptionClassName="overflow-hidden [display:-webkit-box] [-webkit-box-orient:vertical] [-webkit-line-clamp:2] [overflow-wrap:anywhere] break-all"
        crumbs={[
          { label: 'Sessions', to: appRoutes.sessions },
          { label: issue.data.identifier },
        ]}
        actions={
          <Link
            className="inline-flex h-10 items-center justify-center rounded-xl border border-white/10 bg-white/5 px-4 py-2 text-sm font-medium text-white transition duration-200 hover:bg-white/10"
            params={{ identifier }}
            to={appRoutes.issueDetail}
          >
            Open issue
          </Link>
        }
      />

      <div className="flex flex-wrap gap-2">
        <Badge>{issue.data.identifier}</Badge>
        <Badge className="border-white/10 bg-white/5 text-white">{getStateMeta(issue.data.state).label}</Badge>
        <Badge className="border-white/10 bg-white/5 text-white">
          {execution.data.active ? 'Live session' : 'Persisted session'}
        </Badge>
        {issue.data.project_name ? <Badge className="border-white/10 bg-white/5 text-white">{issue.data.project_name}</Badge> : null}
      </div>

      <div className="grid items-start gap-[var(--section-gap)] lg:grid-cols-[1.2fr_.8fr]">
        <SessionExecutionCard
          execution={execution.data}
          issueTotalTokens={issue.data.total_tokens_spent}
          title="Session detail"
          pausedActionHint="Open the issue page to retry after checking the workspace or runtime conditions."
        />

        <div className="grid gap-[var(--section-gap)]">
          <Card>
            <CardContent className="grid gap-3 pt-[var(--panel-padding)] sm:grid-cols-2 lg:grid-cols-1">
              <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 px-3.5 py-3">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Updated</p>
                <p className="mt-3 text-white">{formatRelativeTime(issue.data.updated_at)}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">{formatDateTime(issue.data.updated_at)}</p>
              </div>
              <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 px-3.5 py-3">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Workspace</p>
                <p className="mt-3 break-all text-white">{issue.data.workspace_path || 'Not created yet'}</p>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">Runs: {issue.data.workspace_run_count}</p>
              </div>
              <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 px-3.5 py-3 sm:col-span-2 lg:col-span-1">
                <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Branch / PR</p>
                <p className="mt-3 text-white">{issue.data.branch_name || 'No branch linked'}</p>
                <p className="mt-2 break-all text-sm text-[var(--muted-foreground)]">{issue.data.pr_url || 'No pull request linked'}</p>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardContent className="pt-3.5 pb-3.5">
              <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Description</p>
              <p className="mt-3 whitespace-pre-wrap text-sm leading-6 text-[var(--muted-foreground)]">
                {issue.data.description || 'No description provided.'}
              </p>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  )
}
