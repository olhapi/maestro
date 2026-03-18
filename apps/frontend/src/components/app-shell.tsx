import { startTransition, useEffect, useEffectEvent, useMemo, useRef, useState } from 'react'
import { Link, Outlet, useRouterState } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, FolderKanban, LayoutDashboard, ListTodo, MonitorPlay, RotateCcw, Search } from 'lucide-react'

import { CommandPalette } from '@/components/command-palette'
import { GlobalInterruptPanel } from '@/components/dashboard/global-interrupt-panel'
import { Badge } from '@/components/ui/badge'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useIsMobileLayout } from '@/hooks/use-is-mobile-layout'
import { api } from '@/lib/api'
import { appRoutes, isProjectsPath } from '@/lib/routes'
import { connectDashboardSocket } from '@/lib/live'
import { refreshDashboardQueries } from '@/lib/query-refresh'
import { cn, formatRelativeTimeCompact } from '@/lib/utils'

const nav = [
  { label: 'Overview', to: appRoutes.overview, icon: LayoutDashboard, match: (pathname: string) => pathname === appRoutes.overview },
  { label: 'Work', to: appRoutes.work, icon: ListTodo, match: (pathname: string) => pathname === appRoutes.work || pathname.startsWith('/issues/') },
  { label: 'Projects', to: appRoutes.projects, icon: FolderKanban, match: isProjectsPath },
  { label: 'Sessions', to: appRoutes.sessions, icon: MonitorPlay, match: (pathname: string) => pathname === appRoutes.sessions || pathname.startsWith('/sessions/') },
]

const APP_TITLE = 'Maestro Control Center'
const SIDEBAR_TITLE = 'Maestro'
const brandLinkClass = 'rounded-[calc(var(--panel-radius)-0.125rem)] outline-none transition hover:text-white focus-visible:ring-2 focus-visible:ring-[var(--accent)]/60'
type AudioContextConstructor = typeof AudioContext
type InterruptAudioWindow = Window & typeof globalThis & { webkitAudioContext?: AudioContextConstructor }

function playInterruptNotification() {
  if (typeof window === 'undefined') {
    return
  }
  const AudioContextImpl = (window as InterruptAudioWindow).AudioContext ?? (window as InterruptAudioWindow).webkitAudioContext
  if (!AudioContextImpl) {
    return
  }

  try {
    const context = new AudioContextImpl()
    const now = context.currentTime
    const oscillator = context.createOscillator()
    const gain = context.createGain()
    let closed = false
    const closeContext = () => {
      if (closed) {
        return
      }
      closed = true
      void context.close().catch(() => {})
    }

    oscillator.type = 'triangle'
    oscillator.frequency.setValueAtTime(880, now)
    oscillator.frequency.linearRampToValueAtTime(1174.66, now + 0.12)
    gain.gain.setValueAtTime(0.0001, now)
    gain.gain.exponentialRampToValueAtTime(0.18, now + 0.02)
    gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.32)

    oscillator.connect(gain)
    gain.connect(context.destination)

    void context.resume().catch(() => {
      closeContext()
    })
    oscillator.start(now)
    oscillator.stop(now + 0.32)
    oscillator.addEventListener(
      'ended',
      () => {
        closeContext()
      },
      { once: true },
    )
  } catch {
    // Ignore audio failures so interrupts still render even when autoplay is blocked.
  }
}

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
  const isMobileLayout = useIsMobileLayout()
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [lastRefresh, setLastRefresh] = useState<string>(new Date().toISOString())
  const [hiddenInterruptId, setHiddenInterruptId] = useState<string | null>(null)
  const lastObservedInterruptId = useRef<string | null | undefined>(undefined)
  const bootstrap = useQuery({ queryKey: ['bootstrap'], queryFn: api.bootstrap })
  const interrupts = useQuery({ queryKey: ['interrupts'], queryFn: api.listInterrupts })
  const activePath = useMemo(() => location.pathname || appRoutes.overview, [location.pathname])
  const respondToInterrupt = useMutation({
    mutationFn: ({
      id,
      body,
    }: {
      id: string
      body: {
        decision?: string
        decision_payload?: Record<string, unknown>
        answers?: Record<string, string[]>
      }
    }) =>
      api.respondToInterrupt(id, body),
    onMutate: ({ id }) => {
      startTransition(() => {
        setHiddenInterruptId(id)
      })
    },
    onError: () => {
      setHiddenInterruptId(null)
    },
    onSettled: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['interrupts'], refetchType: 'active' }),
        queryClient.invalidateQueries({ queryKey: ['sessions'], refetchType: 'active' }),
        queryClient.invalidateQueries({ queryKey: ['issue-execution'], refetchType: 'active' }),
      ])
    },
  })

  const handleSocketSignal = useEffectEvent(() => {
    setLastRefresh(new Date().toISOString())
  })

  const handleSocketInvalidate = useEffectEvent(() => {
    handleSocketSignal()
    void Promise.all([
      queryClient.invalidateQueries({
        queryKey: ['interrupts'],
        refetchType: 'active',
      }),
      refreshDashboardQueries(queryClient, activePath),
    ])
  })

  useEffect(() => {
    const socket = connectDashboardSocket({
      onInvalidate: handleSocketInvalidate,
      onSignal: handleSocketSignal,
    })
    return () => {
      socket.disconnect()
    }
  }, [])

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

  const pageTitle = getPageTitle(activePath) || SIDEBAR_TITLE
  const runningCount = bootstrap.data?.overview.snapshot.running.length ?? 0
  const retryCount = bootstrap.data?.overview.snapshot.retrying.length ?? 0
  const currentInterruptId = interrupts.data?.current?.id ?? null
  const effectiveHiddenInterruptId = interrupts.data?.current?.id === hiddenInterruptId ? hiddenInterruptId : null

  useEffect(() => {
    const nextTitle = getPageTitle(activePath)
    document.title = nextTitle ? `${nextTitle} · ${APP_TITLE}` : APP_TITLE
  }, [activePath])

  useEffect(() => {
    if (!interrupts.isFetched) {
      return
    }
    if (lastObservedInterruptId.current === undefined) {
      lastObservedInterruptId.current = currentInterruptId
      return
    }
    if (!currentInterruptId) {
      lastObservedInterruptId.current = null
      return
    }
    if (lastObservedInterruptId.current === currentInterruptId) {
      return
    }
    lastObservedInterruptId.current = currentInterruptId
    playInterruptNotification()
  }, [currentInterruptId, interrupts.isFetched])

  return (
    <div className="min-h-screen overflow-x-clip bg-[var(--page)] text-white">
      <div className="pointer-events-none fixed inset-0 bg-[radial-gradient(circle_at_top_left,rgba(196,255,87,.12),transparent_24%),radial-gradient(circle_at_top_right,rgba(83,217,255,.1),transparent_26%),linear-gradient(180deg,rgba(255,255,255,.05),transparent_40%)]" />
      <div className="relative min-h-screen lg:grid lg:grid-cols-[var(--shell-sidebar-width)_1fr]">
        <aside className="hidden border-b border-white/8 bg-black/25 p-[var(--shell-padding)] backdrop-blur-2xl lg:flex lg:min-h-screen lg:flex-col lg:border-b-0 lg:border-r">
          <Link
            to={appRoutes.overview}
            className={cn('flex items-center gap-3 text-white lg:max-[1440px]:justify-center', brandLinkClass)}
          >
            <div className="hidden size-11 items-center justify-center rounded-[calc(var(--panel-radius)-0.125rem)] border border-white/10 bg-white/5 font-display text-sm font-semibold tracking-[0.24em] text-[var(--accent)] lg:max-[1440px]:flex">
              MC
            </div>
            <div className="lg:max-[1440px]:hidden">
              <h1 className="font-display text-[1.65rem] font-semibold leading-none">{SIDEBAR_TITLE}</h1>
            </div>
          </Link>

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

        <div className="relative flex min-h-screen min-w-0 flex-col">
          <header className="sticky top-0 z-30 border-b border-white/8 bg-black/30 backdrop-blur-xl lg:hidden">
            <div className="flex items-start justify-between gap-3 px-[var(--shell-padding)] py-3">
              <div className="min-w-0">
                <Link to={appRoutes.overview} className={cn('inline-flex text-[11px] uppercase tracking-[0.18em] text-[var(--muted-foreground)]', brandLinkClass)}>
                  {SIDEBAR_TITLE}
                </Link>
                <h1 className="mt-1 truncate font-display text-lg font-semibold leading-none text-white">{pageTitle}</h1>
              </div>
              <div className="flex items-center gap-2">
                <button
                  aria-label="Search issues, projects, sessions, and actions"
                  className="inline-flex h-9 shrink-0 items-center gap-2 rounded-xl border border-white/10 bg-white/5 px-3 text-sm text-[var(--muted-foreground)] transition hover:border-white/14 hover:bg-white/8 hover:text-white"
                  title="Open command palette"
                  type="button"
                  onClick={() => setPaletteOpen(true)}
                >
                  <Search className="size-4 shrink-0" />
                  <span className="hidden sm:inline">Search</span>
                  <span className="inline-flex items-center rounded-md border border-white/10 bg-black/20 px-1.5 py-0.5 text-[10px] font-medium text-white/70">
                    K
                  </span>
                </button>
              </div>
            </div>
            <div className="flex flex-wrap gap-2 border-t border-white/6 px-[var(--shell-padding)] pb-3 pt-2">
              <Badge className="border-lime-400/20 bg-lime-400/10 text-lime-100">{runningCount} running</Badge>
              <Badge className="border-amber-400/20 bg-amber-400/10 text-amber-100">{retryCount} retries</Badge>
              <Badge className="border-white/10 bg-white/5 text-white">Updated {formatRelativeTimeCompact(lastRefresh)}</Badge>
            </div>
          </header>

          <header className="sticky top-0 z-30 hidden flex-wrap items-center justify-between gap-3 border-b border-white/8 bg-black/10 px-[var(--shell-padding)] py-3 backdrop-blur-xl lg:flex">
            <div className="flex flex-wrap items-center gap-3 text-sm text-[var(--muted-foreground)]">
              <span className="inline-flex items-center gap-2">
                <Activity className="size-4 text-lime-300" />
                {runningCount} running
              </span>
              <span className="inline-flex items-center gap-2">
                <RotateCcw className="size-4 text-amber-300" />
                {retryCount} retries
              </span>
            </div>
            <div className="flex items-center gap-2">
              <button
                aria-label="Search issues, projects, sessions, and actions"
                className="group inline-flex h-11 min-w-[22rem] items-center justify-between gap-3 rounded-2xl border border-white/10 bg-white/5 px-4 text-sm text-[var(--muted-foreground)] transition hover:border-white/14 hover:bg-white/8 hover:text-white"
                title="Open command palette"
                type="button"
                onClick={() => setPaletteOpen(true)}
              >
                <span className="flex min-w-0 items-center gap-3">
                  <Search className="size-4 shrink-0 text-[var(--muted-foreground)] transition group-hover:text-white" />
                  <span className="truncate">Search issues, projects, sessions, actions</span>
                </span>
                <span className="inline-flex shrink-0 items-center rounded-lg border border-white/10 bg-black/20 px-2.5 py-1 text-[11px] font-medium text-white/70">
                  Command + K
                </span>
              </button>
            </div>
          </header>
          <GlobalInterruptPanel
            count={interrupts.data?.count ?? 0}
            current={interrupts.data?.current}
            hiddenCurrentId={effectiveHiddenInterruptId}
            isSubmitting={respondToInterrupt.isPending}
            onRespond={(body) => {
              const current = interrupts.data?.current
              if (!current) {
                return
              }
              respondToInterrupt.mutate({ id: current.id, body })
            }}
          />
          <div
            className={cn(
              'flex-1 min-w-0 p-[var(--shell-padding)]',
              isMobileLayout ? 'pb-[calc(var(--mobile-nav-height)+var(--shell-padding)+env(safe-area-inset-bottom))]' : '',
            )}
          >
            <Outlet />
          </div>
        </div>
      </div>
      <nav className="fixed inset-x-0 bottom-0 z-40 border-t border-white/8 bg-[rgba(8,9,12,0.96)] px-[var(--shell-padding)] pb-[calc(env(safe-area-inset-bottom)+0.5rem)] pt-2 backdrop-blur-2xl lg:hidden">
        <div className="grid grid-cols-4 gap-2">
          {nav.map((item) => {
            const Icon = item.icon
            const active = item.match(activePath)
            return (
              <Link
                key={item.to}
                aria-label={item.label}
                className={cn(
                  'flex min-w-0 flex-col items-center gap-1 rounded-[calc(var(--panel-radius)-0.25rem)] border px-2 py-2 text-xs transition',
                  active
                    ? 'border-[var(--accent)]/40 bg-[linear-gradient(135deg,rgba(196,255,87,.16),rgba(255,255,255,.05))] text-white'
                    : 'border-transparent text-[var(--muted-foreground)] hover:border-white/8 hover:bg-white/4 hover:text-white',
                )}
                title={item.label}
                to={item.to}
              >
                <Icon className="size-4 shrink-0" />
                <span className="truncate text-[11px]">{item.label}</span>
              </Link>
            )
          })}
        </div>
      </nav>
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </div>
  )
}
