import { useQuery } from '@tanstack/react-query'
import { Activity, FolderKanban, RotateCcw, Rocket, TimerReset } from 'lucide-react'
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { ScrollArea } from '@/components/ui/scroll-area'
import { api } from '@/lib/api'
import type { RuntimeEvent } from '@/lib/types'
import { formatNumber, formatRelativeTime, toTitleCase } from '@/lib/utils'

function Metric({
  label,
  value,
  detail,
  icon: Icon,
}: {
  label: string
  value: string
  detail: string
  icon: React.ComponentType<{ className?: string }>
}) {
  return (
    <Card className="bg-[linear-gradient(180deg,rgba(255,255,255,.08),rgba(255,255,255,.03))]">
      <CardContent className="pt-5">
        <div className="flex items-start justify-between">
          <div>
            <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{label}</p>
            <p className="mt-3 font-display text-4xl font-semibold">{value}</p>
            <p className="mt-2 text-sm text-[var(--muted-foreground)]">{detail}</p>
          </div>
          <div className="rounded-2xl border border-white/10 bg-white/5 p-3">
            <Icon className="size-5 text-[var(--accent)]" />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function EventRail({ events }: { events: RuntimeEvent[] }) {
  return (
    <Card>
      <CardHeader>
        <div>
          <Badge>Recent signals</Badge>
          <CardTitle className="mt-4">Event rail</CardTitle>
          <CardDescription>Most recent orchestration events persisted for operator triage.</CardDescription>
        </div>
      </CardHeader>
      <CardContent>
        <ScrollArea className="h-[320px] md:h-[420px]">
          <div className="space-y-3 pr-4">
            {events.map((event) => (
              <div key={event.seq} className="rounded-2xl border border-white/8 bg-black/20 p-4">
                <div className="flex items-center justify-between gap-3">
                  <p className="text-sm font-medium text-white">{toTitleCase(event.kind)}</p>
                  <span className="text-xs text-[var(--muted-foreground)]">{formatRelativeTime(event.ts)}</span>
                </div>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                  {event.identifier ? `${event.identifier} · ` : ''}
                  {event.error || event.title || 'Runtime signal captured'}
                </p>
              </div>
            ))}
          </div>
        </ScrollArea>
      </CardContent>
    </Card>
  )
}

export function OverviewPage() {
  const { data, isLoading } = useQuery({ queryKey: ['bootstrap'], queryFn: api.bootstrap })

  if (isLoading || !data) {
    return <div className="grid gap-4 lg:grid-cols-4">{Array.from({ length: 4 }).map((_, index) => <Card key={index} className="h-36 animate-pulse bg-white/5" />)}</div>
  }

  const snapshot = data.overview.snapshot
  const status = data.overview.status
  const activeRuns = Number(status.active_runs ?? snapshot.running.length)
  const retryQueue = Number(status.retry_queue_count ?? snapshot.retrying.length)
  const recentEvents = data.overview.recent_events.slice(-8).reverse()

  return (
    <div className="grid gap-5">
      <section className="grid gap-4 lg:grid-cols-4">
        <Metric label="Running agents" value={formatNumber(activeRuns)} detail="Live execution slots currently occupied." icon={Rocket} />
        <Metric label="Retry pressure" value={formatNumber(retryQueue)} detail="Queued retries waiting to re-enter execution." icon={RotateCcw} />
        <Metric label="Total issues" value={formatNumber(data.overview.issue_count)} detail="Current tracked work across the portfolio." icon={FolderKanban} />
        <Metric
          label="Token burn"
          value={formatNumber(snapshot.codex_totals.total_tokens)}
          detail={`Last snapshot refreshed ${formatRelativeTime(data.generated_at)}.`}
          icon={Activity}
        />
      </section>

      <section>
        <Card>
          <CardHeader>
            <div>
              <Badge>24h throughput</Badge>
              <CardTitle className="mt-4">Execution tempo</CardTitle>
              <CardDescription>Run starts, completions, failures, retries, and token burn across the last 24 hours.</CardDescription>
            </div>
          </CardHeader>
          <CardContent className="min-w-0">
            <ResponsiveContainer width="100%" height={320}>
              <AreaChart data={data.overview.series}>
                <defs>
                  <linearGradient id="runsCompleted" x1="0" x2="0" y1="0" y2="1">
                    <stop offset="5%" stopColor="#c4ff57" stopOpacity={0.7} />
                    <stop offset="95%" stopColor="#c4ff57" stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id="retries" x1="0" x2="0" y1="0" y2="1">
                    <stop offset="5%" stopColor="#53d9ff" stopOpacity={0.6} />
                    <stop offset="95%" stopColor="#53d9ff" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke="rgba(255,255,255,.08)" vertical={false} />
                <XAxis dataKey="bucket" stroke="rgba(255,255,255,.45)" tickLine={false} axisLine={false} />
                <YAxis stroke="rgba(255,255,255,.45)" tickLine={false} axisLine={false} />
                <Tooltip contentStyle={{ background: '#101114', border: '1px solid rgba(255,255,255,.08)', borderRadius: 16 }} />
                <Area type="monotone" dataKey="runs_completed" stroke="#c4ff57" fill="url(#runsCompleted)" strokeWidth={2} />
                <Area type="monotone" dataKey="retries" stroke="#53d9ff" fill="url(#retries)" strokeWidth={2} />
              </AreaChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>
      </section>

      <section className="grid gap-5 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Active runs</CardTitle>
            </div>
          </CardHeader>
          <CardContent className="space-y-3">
            {snapshot.running.length === 0 ? (
              <p className="text-sm text-[var(--muted-foreground)]">No agents are currently running.</p>
            ) : (
              snapshot.running.map((entry) => (
                <div key={entry.issue_id} className="rounded-2xl border border-white/8 bg-black/20 p-4">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <p className="font-medium text-white">{entry.identifier}</p>
                      <p className="text-sm text-[var(--muted-foreground)]">{entry.last_message || 'Waiting for next event'}</p>
                    </div>
                    <Badge>{formatNumber(entry.tokens.total_tokens)} tokens</Badge>
                  </div>
                </div>
              ))
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div>
              <CardTitle>Pending retries</CardTitle>
            </div>
          </CardHeader>
          <CardContent className="space-y-3">
            {snapshot.retrying.length === 0 ? (
              <p className="text-sm text-[var(--muted-foreground)]">Retry queue is empty.</p>
            ) : (
              snapshot.retrying.map((entry) => (
                <div key={entry.issue_id} className="rounded-2xl border border-white/8 bg-black/20 p-4">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <p className="font-medium text-white">{entry.identifier}</p>
                      <p className="text-sm text-[var(--muted-foreground)]">{entry.error || 'Awaiting retry'}</p>
                    </div>
                    <Badge>{formatRelativeTime(entry.due_at)}</Badge>
                  </div>
                </div>
              ))
            )}
          </CardContent>
        </Card>
      </section>

      <section className="grid gap-4 lg:grid-cols-4">
        {Object.entries(data.overview.board).map(([key, value]) => (
          <Card key={key} className="overflow-hidden">
            <CardContent className="pt-5">
              <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{key.replaceAll('_', ' ')}</p>
              <p className="mt-3 font-display text-3xl font-semibold">{value}</p>
              <div className="mt-4 flex items-center gap-2 text-xs text-[var(--muted-foreground)]">
                <TimerReset className="size-3.5" />
                state load
              </div>
            </CardContent>
          </Card>
        ))}
      </section>

      <section>
        <EventRail events={recentEvents} />
      </section>
    </div>
  )
}
