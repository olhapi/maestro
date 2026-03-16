import type { AnchorHTMLAttributes, ReactNode } from 'react'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { AppShell } from '@/components/app-shell'
import { makeBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

const invalidateSocket = vi.fn()
let pathname = '/work'
const initialInnerWidth = window.innerWidth

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

vi.mock('@/lib/live', () => ({
  connectDashboardSocket: (onInvalidate: () => void) => {
    invalidateSocket.mockImplementation(onInvalidate)
    return () => {}
  },
}))

vi.mock('@/lib/api', () => ({
  api: {
    bootstrap: vi.fn(),
    listInterrupts: vi.fn(),
    respondToInterrupt: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

describe('AppShell', () => {
  beforeEach(() => {
    pathname = '/work'
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      writable: true,
      value: initialInnerWidth,
    })
    window.dispatchEvent(new Event('resize'))
  })

  it('renders navigation and reacts to live updates', async () => {
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse())
    vi.mocked(api.listInterrupts).mockResolvedValue({ count: 0 })

    const { queryClient } = renderWithQueryClient(<AppShell />)
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    await waitFor(() => {
      expect(screen.getAllByText('Maestro').length).toBeGreaterThan(0)
    })

    expect(screen.getAllByRole('link', { name: 'Maestro' })[0]).toHaveAttribute('href', '/')
    const workLink = screen.getAllByRole('link', { name: 'Work' })[0]
    expect(workLink).toHaveAttribute('aria-label', 'Work')
    expect(screen.getAllByRole('link', { name: 'Sessions' }).length).toBeGreaterThan(0)
    expect(screen.getByText(/^\d+s ago$/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Refresh' })).not.toBeInTheDocument()
    expect(document.title).toContain('Work')

    fireEvent.click(screen.getAllByRole('button', { name: 'Search issues, projects, sessions, and actions' })[0])
    expect(screen.getByTestId('command-palette')).toHaveTextContent('open')

    await act(async () => {
      await invalidateSocket()
    })
    await waitFor(() => {
      expect(screen.getAllByText('1 running').length).toBeGreaterThan(0)
    })

    expect(invalidateQueries).toHaveBeenCalledTimes(3)
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['interrupts'],
      refetchType: 'active',
    })
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['bootstrap'],
      refetchType: 'active',
    })
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['issues'],
      refetchType: 'active',
    })
  })

  it('keeps the sessions nav state and title on nested session routes', async () => {
    pathname = '/sessions/ISS-1'
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse())
    vi.mocked(api.listInterrupts).mockResolvedValue({ count: 0 })

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByRole('link', { name: 'Sessions' }).length).toBeGreaterThan(0)
    })

    expect(document.title).toContain('Sessions')
  })

  it('switches to the compact mobile shell below the desktop breakpoint', async () => {
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      writable: true,
      value: 390,
    })
    window.dispatchEvent(new Event('resize'))
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse())
    vi.mocked(api.listInterrupts).mockResolvedValue({ count: 0 })

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(
        screen.getAllByRole('button', { name: 'Search issues, projects, sessions, and actions' }).length,
      ).toBeGreaterThan(0)
    })

    expect(screen.queryByRole('button', { name: 'Refresh' })).not.toBeInTheDocument()
    expect(screen.getByText(/Updated .*ago/)).toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: 'Maestro' })[0]).toHaveAttribute('href', '/')
    expect(screen.getAllByRole('link', { name: 'Overview' }).length).toBeGreaterThan(0)
    expect(screen.getAllByRole('link', { name: 'Projects' }).length).toBeGreaterThan(0)
  })

  it('renders the global interrupt panel for the first waiting interaction', async () => {
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse())
    vi.mocked(api.listInterrupts).mockResolvedValue({
      count: 2,
      current: {
        id: 'interrupt-1',
        kind: 'approval',
        issue_identifier: 'ISS-1',
        issue_title: 'Review migrations',
        phase: 'implementation',
        attempt: 1,
        requested_at: '2026-03-16T10:00:00Z',
        last_activity_at: '2026-03-16T10:00:00Z',
        last_activity: 'gh pr view',
        collaboration_mode: 'plan',
        approval: {
          decisions: [
            { value: 'approved', label: 'Approve once' },
            { value: 'approved_for_session', label: 'Approve for session' },
          ],
        },
      },
    })

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getByText('Review migrations')).toBeInTheDocument()
    })

    expect(screen.getByText('2 waiting')).toBeInTheDocument()
    expect(screen.getByText('Plan turn')).toBeInTheDocument()
    expect(screen.getByText('1 more queued')).toBeInTheDocument()
  })
})
