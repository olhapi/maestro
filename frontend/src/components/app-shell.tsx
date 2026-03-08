import { useEffect, useMemo, useState } from 'react'
import { Link, Outlet, useRouterState } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { BellDot, Command, FolderKanban, LayoutDashboard, ListTodo, MonitorPlay, RefreshCw } from 'lucide-react'

import { CommandPalette } from '@/components/command-palette'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { connectDashboardSocket } from '@/lib/live'
import { cn, formatRelativeTime } from '@/lib/utils'

const nav = [
  { label: 'Overview', to: '/dashboard', icon: LayoutDashboard },
  { label: 'Work', to: '/dashboard/work', icon: ListTodo },
  { label: 'Projects', to: '/dashboard/projects', icon: FolderKanban },
  { label: 'Sessions', to: '/dashboard/sessions', icon: MonitorPlay },
]

export function AppShell() {
  const { location } = useRouterState()
  const queryClient = useQueryClient()
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [lastRefresh, setLastRefresh] = useState<string>(new Date().toISOString())

  useEffect(() => {
    return connectDashboardSocket(() => {
      setLastRefresh(new Date().toISOString())
      void queryClient.invalidateQueries()
    })
  }, [queryClient])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setPaletteOpen((open) => !open)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  const activePath = useMemo(() => location.pathname || '/dashboard', [location.pathname])

  return (
    <div className="min-h-screen bg-[var(--page)] text-white">
      <div className="pointer-events-none fixed inset-0 bg-[radial-gradient(circle_at_top_left,rgba(196,255,87,.12),transparent_24%),radial-gradient(circle_at_top_right,rgba(83,217,255,.1),transparent_26%),linear-gradient(180deg,rgba(255,255,255,.05),transparent_40%)]" />
      <div className="relative grid min-h-screen lg:grid-cols-[280px_1fr]">
        <aside className="border-r border-white/8 bg-black/25 p-5 backdrop-blur-2xl">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-xs uppercase tracking-[0.28em] text-[var(--muted-foreground)]">Symphony</p>
              <h1 className="mt-2 font-display text-2xl font-semibold">Mission Control</h1>
            </div>
            <div className="rounded-2xl border border-white/10 bg-white/5 p-3">
              <BellDot className="size-5 text-[var(--accent)]" />
            </div>
          </div>
          <div className="mt-8 space-y-2">
            {nav.map((item) => {
              const Icon = item.icon
              const active = activePath === item.to
              return (
                <Link
                  key={item.to}
                  to={item.to}
                  className={cn(
                    'flex items-center gap-3 rounded-2xl border px-4 py-3 text-sm transition',
                    active
                      ? 'border-[var(--accent)]/50 bg-[linear-gradient(135deg,rgba(196,255,87,.18),rgba(255,255,255,.06))] text-white'
                      : 'border-transparent bg-transparent text-[var(--muted-foreground)] hover:border-white/8 hover:bg-white/4 hover:text-white',
                  )}
                >
                  <Icon className="size-4" />
                  {item.label}
                </Link>
              )
            })}
          </div>
          <div className="mt-8 rounded-[1.75rem] border border-white/10 bg-white/5 p-4">
            <Badge>Live link</Badge>
            <p className="mt-3 text-sm leading-6 text-[var(--muted-foreground)]">
              WebSocket invalidation is active. The dashboard automatically refetches when orchestration state changes.
            </p>
            <p className="mt-3 text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">Last signal</p>
            <p className="mt-1 text-sm text-white">{formatRelativeTime(lastRefresh)}</p>
          </div>
        </aside>

        <main className="relative flex min-h-screen flex-col">
          <header className="flex flex-wrap items-center justify-between gap-4 border-b border-white/8 px-5 py-4 backdrop-blur-xl">
            <div>
              <p className="text-xs uppercase tracking-[0.22em] text-[var(--muted-foreground)]">Operator dashboard</p>
              <h2 className="font-display text-3xl font-semibold">Dense, live, and controllable</h2>
            </div>
            <div className="flex items-center gap-3">
              <Button variant="secondary" onClick={() => void queryClient.invalidateQueries()}>
                <RefreshCw className="size-4" />
                Refresh
              </Button>
              <Button variant="secondary" onClick={() => setPaletteOpen(true)}>
                <Command className="size-4" />
                Command Palette
              </Button>
            </div>
          </header>
          <div className="flex-1 p-5">
            <Outlet />
          </div>
        </main>
      </div>
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </div>
  )
}
