import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Activity, FolderKanban, RotateCcw, Rocket } from 'lucide-react'
import { Area, AreaChart, CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { ComponentErrorBoundary } from '@/components/ui/component-error-boundary'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { api } from '@/lib/api'
import { appRoutes } from '@/lib/routes'
import type { BootstrapResponse } from '@/lib/types'
import { cn, formatCompactNumber, formatNumber, formatRelativeTime, formatRelativeTimeCompact } from '@/lib/utils'

type SeriesPoint = BootstrapResponse['overview']['series'][number]

type ChartSeries = {
  key: keyof SeriesPoint
  label: string
  color: string
}

const executionHealthSeries: ChartSeries[] = [
  { key: 'runs_started', label: 'Runs started', color: '#53d9ff' },
  { key: 'runs_completed', label: 'Runs completed', color: '#c4ff57' },
  { key: 'runs_failed', label: 'Runs failed', color: '#ff6d8f' },
  { key: 'retries', label: 'Retries', color: '#f3b84c' },
]

const tokenBurnSeries: ChartSeries[] = [{ key: 'tokens', label: 'Token burn', color: '#53d9ff' }]

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
      <CardContent className="pt-[var(--panel-padding)]">
        <div className="flex items-start justify-between">
          <div>
            <p className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{label}</p>
            <p className="mt-2.5 font-display text-[length:var(--metric-value-size)] font-semibold leading-none">{value}</p>
            <p className="mt-2 text-sm text-[var(--muted-foreground)]">{detail}</p>
          </div>
          <div className="rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/10 bg-white/5 p-2.5">
            <Icon className="size-5 text-[var(--accent)]" />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function SeriesLegend({ series }: { series: readonly ChartSeries[] }) {
  return (
    <div className="flex flex-wrap gap-2.5">
      {series.map((entry) => (
        <div
          key={entry.key}
          className="inline-flex items-center gap-2 rounded-full border border-white/8 bg-white/5 px-3 py-1 text-xs text-[var(--muted-foreground)]"
        >
          <span className="size-2.5 rounded-full" style={{ backgroundColor: entry.color }} />
          <span>{entry.label}</span>
        </div>
      ))}
    </div>
  )
}

function ChartTooltip({
  active,
  label,
  payload,
  series,
  valueFormatter,
}: {
  active?: boolean
  label?: string | number
  payload?: Array<{ name?: string; value?: unknown; color?: string; dataKey?: string }>
  series: readonly ChartSeries[]
  valueFormatter: (value: number) => string
}) {
  if (!active || !payload?.length) {
    return null
  }

  const payloadByName = new Map(
    payload.map((entry) => [String(entry.name ?? entry.dataKey ?? ''), entry] as const),
  )

  return (
    <div className="rounded-2xl border border-white/10 bg-[#101114] px-3 py-2.5 shadow-2xl shadow-black/50">
      <p className="text-[0.68rem] uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{label}</p>
      <div className="mt-2 space-y-1.5">
        {series.map((entry) => {
          const payloadEntry = payloadByName.get(entry.label) ?? payloadByName.get(String(entry.key))
          if (!payloadEntry) {
            return null
          }

          return (
            <div key={entry.key} className="flex items-center justify-between gap-4 text-sm">
              <div className="flex items-center gap-2">
                <span className="size-2.5 rounded-full" style={{ backgroundColor: entry.color }} />
                <span className="text-white/90">{entry.label}</span>
              </div>
              <span className="font-medium text-white">
                {valueFormatter(Number(payloadEntry.value ?? 0))}
              </span>
            </div>
          )
        })}
      </div>
    </div>
  )
}

function ExecutionHealthChart({ series }: { series: SeriesPoint[] }) {
  return (
    <ResponsiveContainer width="100%" height={320}>
      <LineChart data={series} margin={{ top: 8, right: 8, bottom: 0, left: -16 }}>
        <CartesianGrid stroke="rgba(255,255,255,.08)" vertical={false} />
        <XAxis dataKey="bucket" stroke="rgba(255,255,255,.45)" tickLine={false} axisLine={false} minTickGap={20} />
        <YAxis
          allowDecimals={false}
          stroke="rgba(255,255,255,.45)"
          tickLine={false}
          axisLine={false}
          tickFormatter={(value) => formatNumber(Number(value))}
        />
        <Tooltip
          content={<ChartTooltip series={executionHealthSeries} valueFormatter={(value) => formatNumber(value)} />}
          cursor={{ stroke: 'rgba(255,255,255,.12)' }}
        />
        {executionHealthSeries.map((entry) => (
          <Line
            key={entry.key}
            dataKey={entry.key}
            name={entry.label}
            dot={false}
            activeDot={{ r: 4 }}
            stroke={entry.color}
            strokeLinecap="round"
            strokeWidth={2.5}
            type="monotone"
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
  )
}

function TokenBurnChart({ series }: { series: SeriesPoint[] }) {
  return (
    <ResponsiveContainer width="100%" height="100%">
      <AreaChart data={series} margin={{ top: 8, right: 8, bottom: 0, left: -16 }}>
        <defs>
          <linearGradient id="tokenBurn" x1="0" x2="0" y1="0" y2="1">
            <stop offset="5%" stopColor="#53d9ff" stopOpacity={0.6} />
            <stop offset="95%" stopColor="#53d9ff" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid stroke="rgba(255,255,255,.08)" vertical={false} />
        <XAxis dataKey="bucket" stroke="rgba(255,255,255,.45)" tickLine={false} axisLine={false} minTickGap={20} />
        <YAxis
          allowDecimals={false}
          stroke="rgba(255,255,255,.45)"
          tickLine={false}
          axisLine={false}
          tickFormatter={(value) => formatCompactNumber(Number(value))}
        />
        <Tooltip
          content={<ChartTooltip series={tokenBurnSeries} valueFormatter={(value) => formatCompactNumber(value)} />}
          cursor={{ stroke: 'rgba(255,255,255,.12)' }}
        />
        <Area
          dataKey="tokens"
          name="Token burn"
          stroke="#53d9ff"
          strokeWidth={2.5}
          fill="url(#tokenBurn)"
          fillOpacity={1}
          type="monotone"
          dot={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  )
}

function totalTokenBurn(series: SeriesPoint[]) {
  return series.reduce((total, point) => total + (point.tokens ?? 0), 0)
}

function OverviewTrendCard({
  badge,
  title,
  description,
  legend,
  action,
  boundaryLabel,
  minHeight,
  chartClassName,
  resetKey,
  children,
}: {
  badge: string
  title: string
  description: string
  legend?: readonly ChartSeries[]
  action?: ReactNode
  boundaryLabel: string
  minHeight: string
  chartClassName: string
  resetKey: string
  children: ReactNode
}) {
  return (
    <Card className="flex h-full flex-col overflow-hidden">
      <CardHeader className="flex-col items-start gap-4 md:flex-row md:items-start md:justify-between">
        <div className="min-w-0">
          <Badge>{badge}</Badge>
          <CardTitle className="mt-4">{title}</CardTitle>
          <CardDescription className="mt-2">{description}</CardDescription>
          {legend ? <div className="mt-4"><SeriesLegend series={legend} /></div> : null}
        </div>
        {action ? <div className="shrink-0">{action}</div> : null}
      </CardHeader>
      <CardContent className="flex min-h-0 flex-1 flex-col pt-0">
        <ComponentErrorBoundary className={minHeight} label={boundaryLabel} resetKeys={[resetKey]} scope="widget">
          <div className={cn('min-h-0', chartClassName)}>{children}</div>
        </ComponentErrorBoundary>
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
  const burnTotal = totalTokenBurn(data.overview.series)

  return (
    <div className="grid gap-[var(--section-gap)]">
      <section className="grid gap-3 md:grid-cols-2 lg:grid-cols-4">
        <Metric label="Running agents" value={formatNumber(activeRuns)} detail="Live execution slots currently occupied." icon={Rocket} />
        <Metric label="Retry pressure" value={formatNumber(retryQueue)} detail="Queued retries waiting to re-enter execution." icon={RotateCcw} />
        <Metric label="Total issues" value={formatNumber(data.overview.issue_count)} detail="Current tracked work across the portfolio." icon={FolderKanban} />
        <Metric
          label="Live token load"
          value={formatCompactNumber(snapshot.codex_totals.total_tokens)}
          detail={`Current running sessions only. Snapshot refreshed ${formatRelativeTimeCompact(data.generated_at)}.`}
          icon={Activity}
        />
      </section>

      <section className="grid gap-[var(--section-gap)] lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)] lg:items-stretch">
        <OverviewTrendCard
          badge="24h execution"
          title="Execution health"
          description="Runs started, completions, failures, and retries across the last 24 hours."
          legend={executionHealthSeries}
          boundaryLabel="overview execution health chart"
          minHeight="min-h-[340px]"
          chartClassName="h-[320px]"
          resetKey={data.generated_at}
        >
          <ExecutionHealthChart series={data.overview.series} />
        </OverviewTrendCard>

        <OverviewTrendCard
          badge="24h burn"
          title="Token burn"
          description="Hourly burn from final run totals, not live snapshot totals."
          action={<Badge className="border-white/10 bg-white/5 text-white/90">{formatCompactNumber(burnTotal)} total</Badge>}
          boundaryLabel="overview token burn chart"
          minHeight="min-h-[260px]"
          chartClassName="h-[220px] lg:h-full"
          resetKey={data.generated_at}
        >
          <TokenBurnChart series={data.overview.series} />
        </OverviewTrendCard>
      </section>

      <section className="grid gap-[var(--section-gap)] lg:grid-cols-2">
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Active runs</CardTitle>
            </div>
          </CardHeader>
          <CardContent className="space-y-2.5">
            {snapshot.running.length === 0 ? (
              <p className="text-sm text-[var(--muted-foreground)]">No agents are currently running.</p>
            ) : (
              snapshot.running.map((entry) => (
                <Link
                  key={entry.issue_id}
                  className="block rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5 transition hover:border-white/16 hover:bg-white/[0.05]"
                  params={{ identifier: entry.identifier }}
                  to={appRoutes.issueDetail}
                >
                  <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                    <div className="min-w-0">
                      <p className="min-w-0 break-words [overflow-wrap:anywhere] font-medium text-white">{entry.identifier}</p>
                      <p className="min-w-0 break-words [overflow-wrap:anywhere] text-sm text-[var(--muted-foreground)]">{entry.last_message || 'Waiting for next event'}</p>
                    </div>
                    <Badge className="self-start shrink-0">{formatCompactNumber(entry.tokens.total_tokens)} tokens</Badge>
                  </div>
                </Link>
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
          <CardContent className="space-y-2.5">
            {snapshot.retrying.length === 0 ? (
              <p className="text-sm text-[var(--muted-foreground)]">Retry queue is empty.</p>
            ) : (
              snapshot.retrying.map((entry) => (
                <Link
                  key={entry.issue_id}
                  className="block rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/8 bg-black/20 p-3.5 transition hover:border-white/16 hover:bg-white/[0.05]"
                  params={{ identifier: entry.identifier }}
                  to={appRoutes.issueDetail}
                >
                  <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                    <div className="min-w-0">
                      <p className="min-w-0 break-words [overflow-wrap:anywhere] font-medium text-white">{entry.identifier}</p>
                      <p className="min-w-0 break-words [overflow-wrap:anywhere] text-sm text-[var(--muted-foreground)]">{entry.error || 'Awaiting retry'}</p>
                    </div>
                    <Badge className="self-start shrink-0">{formatRelativeTime(entry.due_at)}</Badge>
                  </div>
                </Link>
              ))
            )}
          </CardContent>
        </Card>
      </section>

      <section className="grid gap-2.5 sm:grid-cols-2 md:grid-cols-3 xl:grid-cols-6">
        {Object.entries(data.overview.board).map(([key, value]) => (
          <Card key={key} className="overflow-hidden">
            <CardContent className="px-[var(--panel-padding-tight)] pb-[var(--panel-padding-tight)] pt-[var(--panel-padding-tight)]">
              <p className="text-[0.68rem] uppercase tracking-[0.14em] text-[var(--muted-foreground)]">{key.replaceAll('_', ' ')}</p>
              <p className="mt-2 font-display text-[calc(var(--metric-value-size)-0.5rem)] font-semibold leading-none">{value}</p>
            </CardContent>
          </Card>
        ))}
      </section>
    </div>
  )
}
