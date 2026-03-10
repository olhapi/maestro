import { useEffect, useMemo, useState } from 'react'
import { Link, Outlet, useRouterState } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Command, FolderKanban, LayoutDashboard, ListTodo, MonitorPlay, RefreshCw, RotateCcw } from 'lucide-react'

import { CommandPalette } from '@/components/command-palette'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { appRoutes, isProjectsPath } from '@/lib/routes'
import { connectDashboardSocket } from '@/lib/live'
import { cn, formatRelativeTime } from '@/lib/utils'

const nav = [
  { label: 'Overview', to: appRoutes.overview, icon: LayoutDashboard, match: (pathname: string) => pathname === appRoutes.overview },
  { label: 'Work', to: appRoutes.work, icon: ListTodo, match: (pathname: string) => pathname === appRoutes.work || pathname.startsWith('/issues/') },
  { label: 'Projects', to: appRoutes.projects, icon: FolderKanban, match: isProjectsPath },
  { label: 'Sessions', to: appRoutes.sessions, icon: MonitorPlay, match: (pathname: string) => pathname === appRoutes.sessions || pathname.startsWith('/sessions/') },
]

const APP_TITLE = 'Maestro Control Center'

function getPageTitle(pathname: string) {
  if (pathname === appRoutes.overview) return 'Overview'
  if (pathname === appRoutes.work) return 'Work'
  if (pathname === appRoutes.projects) return 'Projects'
  if (pathname.startsWith('/projects/')) return 'Project'
  if (pathname.startsWith('/epics/')) return 'Epic'
  if (pathname.startsWith('/issues/')) return 'Issue'
  if (pathname === appRoutes.sessions || pathname.startsWith('/sessions/')) return 'Sessions'
  return ''
}

export function AppShell() {
  const { location } = useRouterState()
  const queryClient = useQueryClient()
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [lastRefresh, setLastRefresh] = useState<string>(new Date().toISOString())
  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: api.bootstrap })

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

  const activePath = useMemo(() => location.pathname || appRoutes.overview, [location.pathname])

  useEffect(() => {
    const pageTitle = getPageTitle(activePath)
    document.title = pageTitle ? `${pageTitle} · ${APP_TITLE}` : APP_TITLE
  }, [activePath])

  return (
    <div className="min-h-screen bg-[var(--page)] text-white">
      <div className="pointer-events-none fixed inset-0 bg-[radial-gradient(circle_at_top_left,rgba(196,255,87,.12),transparent_24%),radial-gradient(circle_at_top_right,rgba(83,217,255,.1),transparent_26%),linear-gradient(180deg,rgba(255,255,255,.05),transparent_40%)]" />
      <div className="relative grid min-h-screen lg:grid-cols-[280px_1fr]">
        <aside className="border-r border-white/8 bg-black/25 p-5 backdrop-blur-2xl">
          <div>
            <h1 className="font-display text-2xl font-semibold">{APP_TITLE}</h1>
          </div>

          <div className="mt-8 space-y-2">
            {nav.map((item) => {
              const Icon = item.icon
              const active = item.match(activePath)
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

          <div className="mt-8 grid gap-3 rounded-[1.75rem] border border-white/10 bg-white/5 p-4">
            <div className="flex items-center justify-between">
              <Badge>Live link</Badge>
              <span className="text-xs uppercase tracking-[0.18em] text-[var(--muted-foreground)]">now</span>
            </div>
            <div className="grid gap-2 text-sm">
              <div className="flex items-center justify-between">
                <span className="text-[var(--muted-foreground)]">Active runs</span>
                <span className="text-white">{bootstrap.data?.overview.snapshot.running.length ?? 0}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-[var(--muted-foreground)]">Queued retries</span>
                <span className="text-white">{bootstrap.data?.overview.snapshot.retrying.length ?? 0}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-[var(--muted-foreground)]">Last signal</span>
                <span className="text-white">{formatRelativeTime(lastRefresh)}</span>
              </div>
            </div>
          </div>
        </aside>

        <main className="relative flex min-h-screen flex-col">
          <header className="flex flex-wrap items-center justify-between gap-4 border-b border-white/8 px-5 py-4 backdrop-blur-xl">
            <div className="flex flex-wrap items-center gap-3 text-sm text-[var(--muted-foreground)]">
              <span className="inline-flex items-center gap-2">
                <Activity className="size-4 text-lime-300" />
                {bootstrap.data?.overview.snapshot.running.length ?? 0} running
              </span>
              <span className="inline-flex items-center gap-2">
                <RotateCcw className="size-4 text-amber-300" />
                {bootstrap.data?.overview.snapshot.retrying.length ?? 0} retries
              </span>
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
