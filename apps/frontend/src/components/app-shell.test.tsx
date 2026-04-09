import type { AnchorHTMLAttributes, ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { AppShell } from '@/components/app-shell'
import { TooltipProvider } from '@/components/ui/tooltip'
import { makeBootstrapResponse, makeWorkBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

const invalidateSocket = vi.fn()
let pathname = '/work'
const initialInnerWidth = window.innerWidth
const audioContextInstances: MockAudioContext[] = []
const dashboardRefreshCoalesceMs = vi.hoisted(() => vi.fn(() => 0))

class MockAudioParam {
  setValueAtTime = vi.fn()
  linearRampToValueAtTime = vi.fn()
  exponentialRampToValueAtTime = vi.fn()
}

class MockOscillatorNode {
  type = 'sine'
  frequency = new MockAudioParam()
  connect = vi.fn()
  start = vi.fn()
  stop = vi.fn()
  emitEnded = vi.fn()
  addEventListener = vi.fn((event: string, listener: () => void) => {
    if (event === 'ended') {
      this.emitEnded.mockImplementation(listener)
    }
  })
}

class MockGainNode {
  gain = new MockAudioParam()
  connect = vi.fn()
}

class MockAudioContext {
  static nextResumeError: Error | null = null
  currentTime = 0
  destination = {}
  oscillator = new MockOscillatorNode()
  gainNode = new MockGainNode()
  createOscillator = vi.fn(() => this.oscillator)
  createGain = vi.fn(() => this.gainNode)
  resume = vi.fn(() => {
    const error = MockAudioContext.nextResumeError
    MockAudioContext.nextResumeError = null
    return error ? Promise.reject(error) : Promise.resolve(undefined)
  })
  close = vi.fn().mockResolvedValue(undefined)

  constructor() {
    audioContextInstances.push(this)
  }
}

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    children,
    to,
    ...props
  }: {
    children: ReactNode
    to?: string
    params?: Record<string, string>
  } & AnchorHTMLAttributes<HTMLAnchorElement>) => (
    <a href={to ?? '#'} {...props}>
      {children}
    </a>
  ),
  Outlet: () => <div data-testid="outlet">Outlet</div>,
  useRouterState: () => ({ location: { pathname } }),
}))

vi.mock('@/components/command-palette', () => ({
  CommandPalette: ({ open }: { open: boolean }) => <div data-testid="command-palette">{open ? 'open' : 'closed'}</div>,
}))

vi.mock('@/lib/query-refresh', async () => {
  const actual = await vi.importActual<typeof import('@/lib/query-refresh')>('@/lib/query-refresh')
  return {
    ...actual,
    dashboardRefreshCoalesceMs,
  }
})

vi.mock('@/lib/live', () => ({
  connectDashboardSocket: ({ onInvalidate }: { onInvalidate: () => void }) => {
    invalidateSocket.mockImplementation(onInvalidate)
    return {
      reconnect: vi.fn(),
      disconnect: vi.fn(),
    }
  },
}))

vi.mock('@/lib/api', () => ({
  api: {
    bootstrap: vi.fn(),
    workBootstrap: vi.fn(),
    listInterrupts: vi.fn(),
    acknowledgeInterrupt: vi.fn(),
    respondToInterrupt: vi.fn(),
    requestIssuePlanRevision: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

function makeApprovalInterrupt(id = 'interrupt-1') {
  return {
    id,
    kind: 'approval' as const,
    issue_identifier: 'ISS-1',
    issue_title: 'Review migrations',
    phase: 'implementation',
    attempt: 1,
    requested_at: '2026-03-16T10:00:00Z',
    last_activity_at: '2026-03-16T10:00:00Z',
    last_activity: 'gh pr view',
    collaboration_mode: 'plan' as const,
    approval: {
      command: 'gh pr view',
      decisions: [{ value: 'approved', label: 'Approve once' }],
    },
  }
}

function makePlanApprovalInterrupt(id = 'interrupt-1') {
  const base = makeApprovalInterrupt(id)
  return {
    ...base,
    approval: {
      ...base.approval,
      markdown: 'Review the proposed plan before execution.\n\n- Tighten the rollout\n- Add a rollback check',
    },
  }
}

function makeAlertInterrupt(id = 'alert-1') {
  return {
    id,
    kind: 'alert' as const,
    issue_identifier: 'ISS-9',
    issue_title: 'Blocked issue',
    project_id: 'project-1',
    project_name: 'Platform',
    requested_at: '2026-03-16T10:05:00Z',
    last_activity_at: '2026-03-16T10:05:00Z',
    last_activity: 'Project repo is outside the current server scope (/repo/current)',
    actions: [{ kind: 'acknowledge' as const, label: 'Acknowledge' }],
    alert: {
      code: 'project_dispatch_blocked',
      severity: 'error' as const,
      title: 'Project dispatch blocked',
      message: 'Project repo is outside the current server scope (/repo/current)',
      detail: 'Blocked issue is waiting for execution until the project scope mismatch is fixed.',
    },
  }
}

function makeSecondApprovalInterrupt(id = 'interrupt-2') {
  return {
    ...makeApprovalInterrupt(id),
    issue_identifier: 'ISS-2',
    issue_title: 'Approve deployment',
    requested_at: '2026-03-16T10:02:00Z',
    last_activity_at: '2026-03-16T10:02:00Z',
    last_activity: 'deploy production',
    approval: {
      command: 'deploy production',
      decisions: [{ value: 'approved', label: 'Approve once' }],
    },
  }
}

function renderAppShellWithSeededData(entries: Array<readonly [readonly unknown[], unknown]>) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: Number.POSITIVE_INFINITY,
      },
    },
  })
  for (const [queryKey, value] of entries) {
    queryClient.setQueryData(queryKey, value)
  }

  const rendered = renderWithProviders(queryClient)

  return {
    queryClient,
    ...rendered,
  }
}

function renderWithProviders(queryClient: QueryClient) {
  return render(
    <QueryClientProvider client={queryClient}>
      <TooltipProvider delayDuration={0}>
        <AppShell />
      </TooltipProvider>
    </QueryClientProvider>,
  )
}

describe('AppShell', () => {
  const mockDashboardData = (bootstrap = makeBootstrapResponse()) => {
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)
    vi.mocked(api.workBootstrap).mockResolvedValue(makeWorkBootstrapResponse({
      generated_at: bootstrap.generated_at,
      overview: {
        board: bootstrap.overview.board,
        snapshot: {
          running: bootstrap.overview.snapshot.running,
          retrying: bootstrap.overview.snapshot.retrying,
          paused: bootstrap.overview.snapshot.paused,
        },
      },
      projects: bootstrap.projects,
      epics: bootstrap.epics,
      issues: bootstrap.issues,
      sessions: bootstrap.sessions,
    }))
  }

  beforeEach(() => {
    pathname = '/work'
    invalidateSocket.mockReset()
    dashboardRefreshCoalesceMs.mockReset()
    dashboardRefreshCoalesceMs.mockReturnValue(0)
    audioContextInstances.length = 0
    MockAudioContext.nextResumeError = null
    window.localStorage.clear()
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      writable: true,
      value: initialInnerWidth,
    })
    Object.defineProperty(window, 'AudioContext', {
      configurable: true,
      writable: true,
      value: MockAudioContext,
    })
    window.dispatchEvent(new Event('resize'))
    vi.useRealTimers()
  })

  it('renders navigation, keeps refresh ages moving, and reacts to live updates', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-03-11T10:00:00Z'))
    const bootstrap = makeBootstrapResponse()
    const workBootstrap = makeWorkBootstrapResponse({
      generated_at: bootstrap.generated_at,
      overview: {
        board: bootstrap.overview.board,
        snapshot: {
          running: bootstrap.overview.snapshot.running,
          retrying: bootstrap.overview.snapshot.retrying,
          paused: bootstrap.overview.snapshot.paused,
        },
      },
      projects: bootstrap.projects,
      epics: bootstrap.epics,
      issues: bootstrap.issues,
      sessions: bootstrap.sessions,
    })

    mockDashboardData(bootstrap)
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [] })

    const { queryClient } = renderAppShellWithSeededData([
      [['work-bootstrap'], workBootstrap] as const,
      [['interrupts'], { items: [] }] as const,
    ])
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    expect(screen.getAllByRole('link', { name: 'Maestro' })[0]).toHaveAttribute('href', '/')
    expect(screen.getAllByRole('link', { name: 'Sessions' }).length).toBeGreaterThan(0)
    expect(screen.getByText('Updated 0s ago')).toBeInTheDocument()
    expect(screen.getByText('Last signal').nextElementSibling).toHaveTextContent('0s ago')
    expect(document.title).toContain('Work')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000)
    })

    expect(screen.getByText('Updated 2s ago')).toBeInTheDocument()
    expect(screen.getByText('Last signal').nextElementSibling).toHaveTextContent('2s ago')

    fireEvent.click(screen.getAllByRole('button', { name: 'Search issues, projects, sessions, and actions' })[0])
    expect(screen.getByTestId('command-palette')).toHaveTextContent('open')

    await act(async () => {
      await invalidateSocket()
    })

    expect(invalidateQueries).toHaveBeenCalledTimes(3)
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['interrupts'],
      refetchType: 'active',
    })
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['work-bootstrap'],
      refetchType: 'active',
    })
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['issues'],
      refetchType: 'active',
    })
    expect(screen.getByText('Updated 0s ago')).toBeInTheDocument()
    expect(screen.getByText('Last signal').nextElementSibling).toHaveTextContent('0s ago')
  })

  it('refreshes immediately when the mobile freshness chip is clicked', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-03-11T10:00:00Z'))
    dashboardRefreshCoalesceMs.mockReturnValue(750)
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      writable: true,
      value: 390,
    })
    window.dispatchEvent(new Event('resize'))

    const bootstrap = makeBootstrapResponse()
    const workBootstrap = makeWorkBootstrapResponse({
      generated_at: bootstrap.generated_at,
      overview: {
        board: bootstrap.overview.board,
        snapshot: {
          running: bootstrap.overview.snapshot.running,
          retrying: bootstrap.overview.snapshot.retrying,
          paused: bootstrap.overview.snapshot.paused,
        },
      },
      projects: bootstrap.projects,
      epics: bootstrap.epics,
      issues: bootstrap.issues,
      sessions: bootstrap.sessions,
    })

    mockDashboardData(bootstrap)
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [] })

    const { queryClient } = renderAppShellWithSeededData([
      [['work-bootstrap'], workBootstrap] as const,
      [['interrupts'], { items: [] }] as const,
    ])
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000)
    })

    expect(screen.getByText('Updated 2s ago')).toBeInTheDocument()

    await act(async () => {
      await invalidateSocket()
    })

    expect(invalidateQueries).toHaveBeenCalledTimes(0)

    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', {
          name: /refresh dashboard now/i,
        }),
      )
    })

    expect(invalidateQueries).toHaveBeenCalledTimes(3)
    expect(screen.getByText('Updated 0s ago')).toBeInTheDocument()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(750)
    })

    expect(invalidateQueries).toHaveBeenCalledTimes(3)
    expect(screen.getByText('Updated 0s ago')).toBeInTheDocument()
  })

  it('keeps the sessions nav state and title on nested session routes', async () => {
    pathname = '/sessions/ISS-1'
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [] })

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByRole('link', { name: 'Sessions' }).length).toBeGreaterThan(0)
    })

    expect(document.title).toContain('Sessions')
  })

  it('invalidates only the active issue detail queries on nested issue routes', async () => {
    pathname = '/issues/ISS-42'
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [] })

    const { queryClient } = renderWithQueryClient(<AppShell />)
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    await waitFor(() => {
      expect(screen.getByTestId('outlet')).toBeInTheDocument()
    })

    await act(async () => {
      await invalidateSocket()
    })

    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['issue', 'ISS-42'],
      refetchType: 'active',
    })
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['issue-execution', 'ISS-42'],
      refetchType: 'active',
    })
  })

  it('switches to the compact mobile shell below the desktop breakpoint', async () => {
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      writable: true,
      value: 390,
    })
    window.dispatchEvent(new Event('resize'))
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [] })

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(
        screen.getAllByRole('button', { name: 'Search issues, projects, sessions, and actions' }).length,
      ).toBeGreaterThan(0)
    })

    expect(screen.getByText(/Updated .*ago/)).toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: 'Maestro' })[0]).toHaveAttribute('href', '/')
    expect(screen.getAllByRole('link', { name: 'Overview' }).length).toBeGreaterThan(0)
    expect(screen.getAllByRole('link', { name: 'Projects' }).length).toBeGreaterThan(0)
  })

  it('renders the shared interrupt feed and spotlights actionable items before alerts', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({
      items: [makeAlertInterrupt(), makeApprovalInterrupt()],
    })

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByText('Review migrations').length).toBeGreaterThan(0)
    })

    expect(screen.getAllByText('2 waiting').length).toBeGreaterThan(0)
    expect(screen.getByText('Plan turn')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /queue \(2\)/i })).toBeInTheDocument()
    expect(screen.getByText('Allow the agent to run this command?')).toBeInTheDocument()
  })

  it('shows a launcher for the active waiting interrupt and toggles the full-screen dialog from the header', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({
      items: [makeAlertInterrupt(), makeApprovalInterrupt()],
    })

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByText('Review migrations').length).toBeGreaterThan(0)
    })

    expect(screen.getByText('Plan turn')).toBeInTheDocument()
    expect(screen.getByTestId('global-interrupt-panel')).toBeInTheDocument()
    expect(
      screen.getAllByRole('button', {
        name: /hide waiting input dialog: approval · iss-1/i,
        hidden: true,
      }),
    ).toHaveLength(2)

    fireEvent.click(
      screen.getAllByRole('button', {
        name: /hide waiting input dialog: approval · iss-1/i,
        hidden: true,
      })[0]!,
    )

    await waitFor(() => {
      expect(screen.queryByTestId('global-interrupt-panel')).not.toBeInTheDocument()
    })
    await waitFor(() => {
      expect(window.localStorage.getItem('maestro.interrupt-panel-hidden')).toBe('true')
    })
    expect(
      screen.getAllByRole('button', {
        name: /show waiting input dialog: approval · iss-1/i,
        hidden: true,
      }),
    ).toHaveLength(2)

    fireEvent.click(
      screen.getAllByRole('button', {
        name: /show waiting input dialog: approval · iss-1/i,
        hidden: true,
      })[0]!,
    )

    await waitFor(() => {
      expect(screen.getByTestId('global-interrupt-panel')).toBeInTheDocument()
    })
    await waitFor(() => {
      expect(window.localStorage.getItem('maestro.interrupt-panel-hidden')).toBe('false')
    })
  })

  it('restores a dismissed dialog from localStorage and keeps the header launcher visible', async () => {
    window.localStorage.setItem('maestro.interrupt-panel-hidden', 'true')
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [makeApprovalInterrupt()] })

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(
        screen.getAllByRole('button', {
          name: /show waiting input dialog: approval · iss-1/i,
          hidden: true,
        }),
      ).toHaveLength(2)
    })

    expect(screen.queryByTestId('global-interrupt-panel')).not.toBeInTheDocument()
    expect(
      screen.getAllByRole('button', {
        name: /show waiting input dialog: approval · iss-1/i,
        hidden: true,
      }),
    ).toHaveLength(2)

    fireEvent.click(
      screen.getAllByRole('button', {
        name: /show waiting input dialog: approval · iss-1/i,
        hidden: true,
      })[0]!,
    )

    await waitFor(() => {
      expect(screen.getByTestId('global-interrupt-panel')).toBeInTheDocument()
    })
    await waitFor(() => {
      expect(window.localStorage.getItem('maestro.interrupt-panel-hidden')).toBe('false')
    })
  })

  it('plays one audio notification only when a spotlight interrupt appears after initial load', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts)
      .mockResolvedValueOnce({ items: [] })
      .mockResolvedValue({ items: [makeApprovalInterrupt()] })

    const { queryClient } = renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(queryClient.getQueryState(['interrupts'])?.status).toBe('success')
    })

    expect(audioContextInstances).toHaveLength(0)

    await act(async () => {
      await invalidateSocket()
    })

    await waitFor(() => {
      expect(screen.getAllByText('Review migrations').length).toBeGreaterThan(0)
    }, { timeout: 2000 })

    expect(audioContextInstances).toHaveLength(1)

    const [context] = audioContextInstances

    expect(context?.createOscillator).toHaveBeenCalledTimes(1)
    expect(context?.createGain).toHaveBeenCalledTimes(1)
    expect(context?.oscillator.type).toBe('triangle')
    expect(context?.oscillator.frequency.setValueAtTime).toHaveBeenNthCalledWith(1, 783.99, 0)
    expect(context?.oscillator.frequency.setValueAtTime).toHaveBeenNthCalledWith(2, 1046.5, 0.085)
    expect(context?.oscillator.frequency.linearRampToValueAtTime).not.toHaveBeenCalled()
    expect(context?.gainNode.gain.setValueAtTime).toHaveBeenCalledWith(0.0001, 0)
    expect(context?.gainNode.gain.linearRampToValueAtTime).toHaveBeenNthCalledWith(1, 0.08, 0.012)
    expect(context?.gainNode.gain.linearRampToValueAtTime).toHaveBeenNthCalledWith(2, 0.03, 0.08)
    expect(context?.gainNode.gain.linearRampToValueAtTime).toHaveBeenNthCalledWith(3, 0.085, 0.095)
    expect(context?.gainNode.gain.linearRampToValueAtTime).toHaveBeenNthCalledWith(4, 0.0001, 0.29)
    expect(context?.gainNode.gain.exponentialRampToValueAtTime).not.toHaveBeenCalled()
    expect(context?.oscillator.start).toHaveBeenCalledWith(0)
    expect(context?.oscillator.stop).toHaveBeenCalledWith(0.3)
  })

  it('does not replay the audio notification for the same spotlight interrupt id', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [makeApprovalInterrupt()] })

    const { queryClient } = renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByText('Review migrations').length).toBeGreaterThan(0)
    })

    await waitFor(() => {
      expect(queryClient.getQueryState(['interrupts'])?.status).toBe('success')
    })

    expect(audioContextInstances).toHaveLength(0)
    const interruptFetchCount = vi.mocked(api.listInterrupts).mock.calls.length

    await act(async () => {
      await invalidateSocket()
    })

    await waitFor(() => {
      expect(vi.mocked(api.listInterrupts).mock.calls.length).toBeGreaterThan(interruptFetchCount)
    }, { timeout: 2000 })

    expect(audioContextInstances).toHaveLength(0)
  })

  it('does not play audio when optimistic hiding reveals an already-queued interrupt', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts)
      .mockResolvedValueOnce({
        items: [makeApprovalInterrupt('interrupt-1'), makeSecondApprovalInterrupt('interrupt-2')],
      })
      .mockResolvedValue({
        items: [makeSecondApprovalInterrupt('interrupt-2')],
      })

    let resolveResponse: ((value: { id: string; status: string }) => void) | undefined
    vi.mocked(api.respondToInterrupt).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveResponse = resolve
        }),
    )

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByText('Review migrations').length).toBeGreaterThan(0)
    })

    fireEvent.click(screen.getByRole('button', { name: /approve once/i }))

    await waitFor(() => {
      expect(api.respondToInterrupt).toHaveBeenCalledWith('interrupt-1', { decision: 'approved' })
    })
    await waitFor(() => {
      expect(screen.getAllByText('Approve deployment').length).toBeGreaterThan(0)
    })
    expect(audioContextInstances).toHaveLength(0)

    await act(async () => {
      resolveResponse?.({ id: 'interrupt-1', status: 'ok' })
    })

    await waitFor(() => {
      expect(vi.mocked(api.listInterrupts).mock.calls.length).toBeGreaterThan(1)
    })
    await waitFor(() => {
      expect(screen.getAllByText('Approve deployment').length).toBeGreaterThan(0)
    })
    expect(audioContextInstances).toHaveLength(0)
  })

  it('keeps later queued approvals disabled until the head response settles', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({
      items: [makeApprovalInterrupt('interrupt-1'), makeSecondApprovalInterrupt('interrupt-2')],
    })

    let resolveResponse: ((value: { id: string; status: string }) => void) | undefined
    vi.mocked(api.respondToInterrupt).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveResponse = resolve
        }),
    )

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByText('Review migrations').length).toBeGreaterThan(0)
    })

    fireEvent.click(screen.getByRole('button', { name: /approve once/i }))

    await waitFor(() => {
      expect(api.respondToInterrupt).toHaveBeenCalledWith('interrupt-1', { decision: 'approved' })
    })
    await waitFor(() => {
      expect(screen.getAllByText('Approve deployment').length).toBeGreaterThan(0)
    })
    expect(screen.getByRole('button', { name: /approve once/i })).toBeDisabled()

    await act(async () => {
      resolveResponse?.({ id: 'interrupt-1', status: 'ok' })
    })
  })

  it('closes the audio context when resume rejects before playback starts', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts)
      .mockResolvedValueOnce({ items: [] })
      .mockResolvedValue({ items: [makeApprovalInterrupt()] })
    MockAudioContext.nextResumeError = new Error('blocked')

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(vi.mocked(api.listInterrupts).mock.calls.length).toBeGreaterThan(0)
    })
    await waitFor(() => {
      expect(invalidateSocket.getMockImplementation()).toBeTypeOf('function')
    })

    await act(async () => {
      await invalidateSocket()
    })

    await waitFor(() => {
      expect(audioContextInstances).toHaveLength(1)
    }, { timeout: 2000 })

    const [context] = audioContextInstances

    await waitFor(() => {
      expect(context.close).toHaveBeenCalledTimes(1)
    })
  })

  it('optimistically hides a responded interrupt while the response is in flight', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [makeApprovalInterrupt()] })

    let resolveResponse: ((value: { id: string; status: string }) => void) | undefined
    vi.mocked(api.respondToInterrupt).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveResponse = resolve
        }),
    )

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByText('Review migrations').length).toBeGreaterThan(0)
    })

    fireEvent.click(screen.getByRole('button', { name: /approve once/i }))

    await waitFor(() => {
      expect(api.respondToInterrupt).toHaveBeenCalledWith('interrupt-1', { decision: 'approved' })
    })
    expect(screen.queryByTestId('global-interrupt-panel')).not.toBeInTheDocument()

    const interruptFetchCount = vi.mocked(api.listInterrupts).mock.calls.length
    await act(async () => {
      resolveResponse?.({ id: 'interrupt-1', status: 'ok' })
    })
    await waitFor(() => {
      expect(vi.mocked(api.listInterrupts).mock.calls.length).toBeGreaterThan(interruptFetchCount)
    })
    expect(screen.queryByTestId('global-interrupt-panel')).not.toBeInTheDocument()
  })

  it('keeps note-only approval responses visible while the request is in flight', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [makeApprovalInterrupt()] })

    let resolveResponse: ((value: { id: string; status: string }) => void) | undefined
    vi.mocked(api.respondToInterrupt).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveResponse = resolve
        }),
    )

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByText('Review migrations').length).toBeGreaterThan(0)
    })

    expect(screen.queryByPlaceholderText(/add steering notes for the next turn/i)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /add steering note/i }))
    fireEvent.change(screen.getByPlaceholderText(/add steering notes for the next turn/i), {
      target: { value: 'Keep the rollout in a smaller batch.' },
    })
    fireEvent.click(screen.getByRole('button', { name: /send note/i }))

    await waitFor(() => {
      expect(api.respondToInterrupt).toHaveBeenCalledWith('interrupt-1', {
        note: 'Keep the rollout in a smaller batch.',
      })
    })
    expect(screen.getByTestId('global-interrupt-panel')).toBeInTheDocument()

    await act(async () => {
      resolveResponse?.({ id: 'interrupt-1', status: 'ok' })
    })
    expect(screen.getByTestId('global-interrupt-panel')).toBeInTheDocument()
  })

  it('hides a plan approval interrupt after requesting changes and the refreshed list clears it', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts)
      .mockResolvedValueOnce({ items: [makePlanApprovalInterrupt()] })
      .mockResolvedValue({ items: [] })

    let resolveRevision: ((value: { ok: boolean }) => void) | undefined
    vi.mocked(api.requestIssuePlanRevision).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveRevision = resolve
        }),
    )

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByText('Review migrations').length).toBeGreaterThan(0)
    })

    fireEvent.click(screen.getByRole('button', { name: /add steering note/i }))
    fireEvent.change(screen.getByPlaceholderText(/explain what should change in the plan/i), {
      target: { value: 'Tighten the rollout and keep the rollback explicit.' },
    })
    fireEvent.click(screen.getByRole('button', { name: /request changes/i }))

    await waitFor(() => {
      expect(api.requestIssuePlanRevision).toHaveBeenCalledWith(
        'ISS-1',
        'Tighten the rollout and keep the rollback explicit.',
      )
    })

    await act(async () => {
      resolveRevision?.({ ok: true })
    })

    await waitFor(() => {
      expect(screen.queryByTestId('global-interrupt-panel')).not.toBeInTheDocument()
    })
  })

  it('optimistically hides an acknowledged alert while the request is in flight', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [makeAlertInterrupt()] })

    let resolveAcknowledge: ((value: { id: string; status: string }) => void) | undefined
    vi.mocked(api.acknowledgeInterrupt).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveAcknowledge = resolve
        }),
    )

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByText('Project dispatch blocked').length).toBeGreaterThan(0)
    })

    fireEvent.click(screen.getAllByRole('button', { name: 'Acknowledge' })[0]!)

    await waitFor(() => {
      expect(api.acknowledgeInterrupt).toHaveBeenCalledWith('alert-1')
    })
    expect(screen.queryByTestId('global-interrupt-panel')).not.toBeInTheDocument()

    const interruptFetchCount = vi.mocked(api.listInterrupts).mock.calls.length
    await act(async () => {
      resolveAcknowledge?.({ id: 'alert-1', status: 'ok' })
    })
    await waitFor(() => {
      expect(vi.mocked(api.listInterrupts).mock.calls.length).toBeGreaterThan(interruptFetchCount)
    })
    expect(screen.queryByTestId('global-interrupt-panel')).not.toBeInTheDocument()
  })
})
