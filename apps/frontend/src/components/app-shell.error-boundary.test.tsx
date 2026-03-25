import type { AnchorHTMLAttributes, ReactNode } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AppShell } from '@/components/app-shell'
import { makeWorkBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

let pathname = '/work'
let shouldThrowCommandPalette = false
let shouldThrowInterruptPanel = false

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
  CommandPalette: ({ open }: { open: boolean }) => {
    if (open && shouldThrowCommandPalette) {
      throw new Error('palette crashed')
    }

    return <div data-testid="command-palette">{open ? 'open' : 'closed'}</div>
  },
}))

vi.mock('@/components/dashboard/global-interrupt-panel', () => ({
  GlobalInterruptPanel: ({ items }: { items: Array<unknown> }) => {
    if (items.length > 0 && shouldThrowInterruptPanel) {
      throw new Error('interrupt panel crashed')
    }

    return <div data-testid="interrupt-panel">{items.length > 0 ? 'Interrupt panel ready' : 'No interrupts'}</div>
  },
}))

vi.mock('@/lib/live', () => ({
  connectDashboardSocket: () => ({
    reconnect: vi.fn(),
    disconnect: vi.fn(),
  }),
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

describe('AppShell error boundaries', () => {
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    pathname = '/work'
    shouldThrowCommandPalette = false
    shouldThrowInterruptPanel = false
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.clearAllMocks()
  })

  afterEach(() => {
    consoleErrorSpy.mockRestore()
  })

  it('contains command palette crashes and reloads the palette subtree', async () => {
    vi.mocked(api.workBootstrap).mockResolvedValue(makeWorkBootstrapResponse())
    vi.mocked(api.listInterrupts).mockResolvedValue({ items: [] })
    shouldThrowCommandPalette = true

    renderWithQueryClient(<AppShell />)

    await waitFor(() => {
      expect(screen.getAllByText('Maestro').length).toBeGreaterThan(0)
    })

    fireEvent.click(screen.getAllByRole('button', { name: 'Search issues, projects, sessions, and actions' })[0])

    const reloadButton = await screen.findByRole('button', { name: /reload command palette/i })
    expect(screen.getByTestId('outlet')).toBeInTheDocument()

    shouldThrowCommandPalette = false
    fireEvent.click(reloadButton)

    expect(await screen.findByTestId('command-palette')).toHaveTextContent('open')
    expect(screen.getByTestId('outlet')).toBeInTheDocument()
  })

  it('contains interrupt panel crashes and reloads the panel subtree', async () => {
    vi.mocked(api.workBootstrap).mockResolvedValue(makeWorkBootstrapResponse())
    vi.mocked(api.listInterrupts).mockResolvedValue({
      items: [{
        id: 'interrupt-1',
        kind: 'approval',
        issue_identifier: 'ISS-1',
        issue_title: 'Review migrations',
        requested_at: '2026-03-16T10:00:00Z',
        approval: {
          decisions: [{ value: 'approved', label: 'Approve once' }],
        },
      }],
    })
    shouldThrowInterruptPanel = true

    renderWithQueryClient(<AppShell />)

    const reloadButton = await screen.findByRole('button', { name: /reload interrupt panel/i })
    expect(screen.getByTestId('outlet')).toBeInTheDocument()

    shouldThrowInterruptPanel = false
    fireEvent.click(reloadButton)

    expect(await screen.findByTestId('interrupt-panel')).toHaveTextContent('Interrupt panel ready')
    expect(screen.getByTestId('outlet')).toBeInTheDocument()
  })
})
