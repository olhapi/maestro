import { useEffect, useMemo, useState } from 'react'
import { Link, Outlet, useRouterState } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Command, FolderKanban, LayoutDashboard, ListTodo, MonitorPlay, RefreshCw, RotateCcw } from 'lucide-react'

import { CommandPalette } from '@/components/command-palette'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { api } from '@/lib/api'
import { appRoutes, isProjectsPath } from '@/lib/routes'
import { connectDashboardSocket } from '@/lib/live'
import { cn, formatRelativeTimeCompact } from '@/lib/utils'

const nav = [
  { label: 'Overview', to: appRoutes.overview, icon: LayoutDashboard, match: (pathname: string) => pathname === appRoutes.overview },
  { label: 'Work', to: appRoutes.work, icon: ListTodo, match: (pathname: string) => pathname === appRoutes.work || pathname.startsWith('/issues/') },
  { label: 'Projects', to: appRoutes.projects, icon: FolderKanban, match: isProjectsPath },
  { label: 'Sessions', to: appRoutes.sessions, icon: MonitorPlay, match: (pathname: string) => pathname === appRoutes.sessions || pathname.startsWith('/sessions/') },
]

const APP_TITLE = 'Maestro Control Center'
const SIDEBAR_TITLE = 'Maestro'

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
      <div className="relative grid min-h-screen lg:grid-cols-[var(--shell-sidebar-width)_1fr]">
        <aside className="border-b border-white/8 bg-black/25 p-[var(--shell-padding)] backdrop-blur-2xl lg:border-b-0 lg:border-r">
          <div className="flex items-center gap-3 lg:max-[1440px]:justify-center">
            <div className="hidden size-11 items-center justify-center rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/10 bg-white/5 font-display text-sm font-semibold tracking-[0.24em] text-[var(--accent)] lg:max-[1440px]:flex">
              MC
            </div>
            <div className="lg:max-[1440px]:hidden">
              <h1 className="font-display text-[1.65rem] font-semibold leading-none">{SIDEBAR_TITLE}</h1>
            </div>
          </div>

          <div className="mt-6 grid gap-2">
            {nav.map((item) => {
              const Icon = item.icon
              const active = item.match(activePath)
              return (
                <Tooltip key={item.to}>
                  <TooltipTrigger asChild>
                    <Link
                      to={item.to}
                      aria-label={item.label}
                      title={item.label}
                      className={cn(
                        'flex items-center gap-3 rounded-[calc(var(--panel-radius)-0.125rem)] border px-3.5 py-2.5 text-sm transition lg:max-[1440px]:justify-center lg:max-[1440px]:px-0 lg:max-[1440px]:py-3',
                        active
                          ? 'border-[var(--accent)]/50 bg-[linear-gradient(135deg,rgba(196,255,87,.18),rgba(255,255,255,.06))] text-white'
                          : 'border-transparent bg-transparent text-[var(--muted-foreground)] hover:border-white/8 hover:bg-white/4 hover:text-white',
                      )}
                    >
                      <Icon className="size-4 shrink-0" />
                      <span className="lg:max-[1440px]:sr-only">{item.label}</span>
                    </Link>
                  </TooltipTrigger>
                  <TooltipContent side="right" className="hidden lg:max-[1440px]:block">
                    {item.label}
                  </TooltipContent>
                </Tooltip>
              )
            })}
          </div>

          <div className="mt-6 grid gap-3 rounded-[var(--panel-radius)] border border-white/10 bg-white/5 p-[var(--panel-padding)] lg:max-[1440px]:hidden">
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
                <span className="text-white">{formatRelativeTimeCompact(lastRefresh)}</span>
              </div>
            </div>
          </div>
        </aside>

        <main className="relative flex min-h-screen flex-col">
          <header className="flex flex-wrap items-center justify-between gap-3 border-b border-white/8 px-[var(--shell-padding)] py-3 backdrop-blur-xl">
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
            <div className="flex items-center gap-2">
              <Button variant="secondary" size="sm" onClick={() => void queryClient.invalidateQueries()}>
                <RefreshCw className="size-4" />
                Refresh
              </Button>
              <Button variant="secondary" size="sm" onClick={() => setPaletteOpen(true)}>
                <Command className="size-4" />
                Command Palette
              </Button>
            </div>
          </header>
          <div className="flex-1 p-[var(--shell-padding)]">
            <Outlet />
          </div>
        </main>
      </div>
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </div>
  )
}
