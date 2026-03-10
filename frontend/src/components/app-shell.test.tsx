import type { AnchorHTMLAttributes, ReactNode } from 'react'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { AppShell } from '@/components/app-shell'
import { makeBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

const invalidateSocket = vi.fn()
let pathname = '/work'

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
  },
}))

const { api } = await import('@/lib/api')

describe('AppShell', () => {
  beforeEach(() => {
    pathname = '/work'
  })

  it('renders navigation and reacts to refresh controls', async () => {
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse())

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getByText('Maestro Control Center')).toBeInTheDocument()
    })

    const workLink = screen.getByRole('link', { name: 'Work' })
    expect(workLink).toHaveAttribute('aria-label', 'Work')
    expect(workLink.className).toContain('lg:max-[1440px]:justify-center')
    expect(screen.getByRole('link', { name: 'Sessions' })).toBeInTheDocument()
    expect(document.title).toContain('Work')

    fireEvent.click(screen.getByText('Command Palette'))
    expect(screen.getByTestId('command-palette')).toHaveTextContent('open')

    await act(async () => {
      invalidateSocket()
    })
    await waitFor(() => {
      expect(screen.getByText('1 running')).toBeInTheDocument()
    })
  })

  it('keeps the sessions nav state and title on nested session routes', async () => {
    pathname = '/sessions/ISS-1'
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse())

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getByRole('link', { name: 'Sessions' })).toBeInTheDocument()
    })

    expect(document.title).toContain('Sessions')
  })
})
