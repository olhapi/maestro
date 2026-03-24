import type { AnchorHTMLAttributes, ReactNode } from 'react'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { AppShell } from '@/components/app-shell'
import { makeBootstrapResponse, makeWorkBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

const invalidateSocket = vi.fn()
let pathname = '/work'
const initialInnerWidth = window.innerWidth
const audioContextInstances: MockAudioContext[] = []

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
    dashboardRefreshCoalesceMs: () => 0,
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
    audioContextInstances.length = 0
    MockAudioContext.nextResumeError = null
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

  it('renders navigation and reacts to live updates', async () => {
    mockDashboardData()
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [] })

    const { queryClient } = renderWithQueryClient(<AppShell />)
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    await waitFor(() => {
      expect(screen.getAllByText('Maestro').length).toBeGreaterThan(0)
    })

    expect(screen.getAllByRole('link', { name: 'Maestro' })[0]).toHaveAttribute('href', '/')
    expect(screen.getAllByRole('link', { name: 'Sessions' }).length).toBeGreaterThan(0)
    expect(screen.getByText(/^\d+s ago$/)).toBeInTheDocument()
    expect(document.title).toContain('Work')

    fireEvent.click(screen.getAllByRole('button', { name: 'Search issues, projects, sessions, and actions' })[0])
    expect(screen.getByTestId('command-palette')).toHaveTextContent('open')

    await act(async () => {
      await invalidateSocket()
    })
    await waitFor(() => {
      expect(invalidateQueries).toHaveBeenCalledTimes(3)
    }, { timeout: 2000 })

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

    expect(screen.getByText('2 waiting')).toBeInTheDocument()
    expect(screen.getByText('Plan turn')).toBeInTheDocument()
    expect(screen.getByText('Project dispatch blocked')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Acknowledge' }).length).toBeGreaterThan(0)
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
    expect(audioContextInstances[0]?.createOscillator).toHaveBeenCalledTimes(1)
    expect(audioContextInstances[0]?.createGain).toHaveBeenCalledTimes(1)
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
