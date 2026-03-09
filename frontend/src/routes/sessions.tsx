import { useQuery } from '@tanstack/react-query'

import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { api } from '@/lib/api'
import { formatNumber, formatRelativeTime } from '@/lib/utils'

export function SessionsPage() {
  const sessions = useQuery({ queryKey: ['sessions'], queryFn: api.listSessions })
  const events = useQuery({ queryKey: ['runtime-events'], queryFn: api.listRuntimeEvents })

  if (!sessions.data || !events.data) {
    return <Card className="h-[420px] animate-pulse bg-white/5" />
  }

  const entries = Object.entries(sessions.data.sessions)

  return (
    <div className="grid gap-5">
      <div>
        <h3 className="font-display text-4xl font-semibold">Threads, turns, and event traces</h3>
      </div>
      <div className="grid gap-5 xl:grid-cols-[1.2fr_.8fr]">
        <Card>
          <CardHeader>
            <div>
              <CardTitle>Live app sessions</CardTitle>
              <CardDescription>Operational snapshots direct from the running app-server sessions.</CardDescription>
            </div>
          </CardHeader>
          <CardContent className="grid gap-3">
            {entries.length === 0 ? (
              <p className="text-sm text-[var(--muted-foreground)]">No sessions are currently active.</p>
            ) : (
              entries.map(([issueIdentifier, session]) => (
                <div key={issueIdentifier} className="rounded-[1.75rem] border border-white/8 bg-black/20 p-5">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <p className="font-medium text-white">{session.issue_identifier || issueIdentifier}</p>
                      {session.issue_id ? <p className="mt-1 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">{session.issue_id}</p> : null}
                      <p className="mt-1 text-sm text-[var(--muted-foreground)]">{session.last_message || session.last_event || 'No event message yet.'}</p>
                    </div>
                    <div className="flex gap-2">
                      <Badge>{session.last_event || 'idle'}</Badge>
                      <Badge>{formatNumber(session.total_tokens)} tokens</Badge>
                    </div>
                  </div>
                  <div className="mt-4 grid grid-cols-3 gap-3 text-sm">
                    <div className="rounded-2xl border border-white/8 bg-white/4 p-3 text-[var(--muted-foreground)]">
                      <p className="text-xs uppercase tracking-[0.18em]">Turns started</p>
                      <p className="mt-2 text-xl text-white">{session.turns_started}</p>
                    </div>
                    <div className="rounded-2xl border border-white/8 bg-white/4 p-3 text-[var(--muted-foreground)]">
                      <p className="text-xs uppercase tracking-[0.18em]">Events</p>
                      <p className="mt-2 text-xl text-white">{session.events_processed}</p>
                    </div>
                    <div className="rounded-2xl border border-white/8 bg-white/4 p-3 text-[var(--muted-foreground)]">
                      <p className="text-xs uppercase tracking-[0.18em]">Updated</p>
                      <p className="mt-2 text-xl text-white">{formatRelativeTime(session.last_timestamp)}</p>
                    </div>
                  </div>
                </div>
              ))
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div>
              <CardTitle>Recent runtime events</CardTitle>
              <CardDescription>Persisted event tape for quick debugging and failure forensics.</CardDescription>
            </div>
          </CardHeader>
          <CardContent className="space-y-3">
            {events.data.events.slice(-12).reverse().map((event) => (
              <div key={event.seq} className="rounded-2xl border border-white/8 bg-black/20 p-4">
                <div className="flex items-center justify-between gap-3">
                  <p className="text-sm font-medium text-white">{event.kind}</p>
                  <span className="text-xs text-[var(--muted-foreground)]">{formatRelativeTime(event.ts)}</span>
                </div>
                <p className="mt-2 text-sm text-[var(--muted-foreground)]">
                  {event.identifier ? `${event.identifier} · ` : ''}
                  {event.error || event.title || 'Runtime signal'}
                </p>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
