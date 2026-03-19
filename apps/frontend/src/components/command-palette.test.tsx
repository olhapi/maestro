import { fireEvent, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { CommandPalette } from '@/components/command-palette'
import { makeWorkBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

const navigate = vi.fn()
let pathname = '/work'

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  useRouterState: () => ({ location: { pathname } }),
}))

vi.mock('@/lib/api', () => ({
  api: {
    bootstrap: vi.fn(),
    workBootstrap: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

describe('CommandPalette', () => {
  beforeEach(() => {
    pathname = '/work'
  })

  beforeAll(() => {
    vi.stubGlobal('ResizeObserver', ResizeObserverMock)
    window.HTMLElement.prototype.scrollIntoView = vi.fn()
  })

  afterAll(() => {
    vi.unstubAllGlobals()
  })

  it('focuses the search input when the palette opens', async () => {
    vi.mocked(api.workBootstrap).mockResolvedValue(makeWorkBootstrapResponse())

    const onOpenChange = vi.fn()
    renderWithQueryClient(<CommandPalette open={true} onOpenChange={onOpenChange} />)

    await waitFor(() => {
      expect(
        screen.getByPlaceholderText('Search issues, projects, sessions, or actions...'),
      ).toHaveFocus()
    })
  })

  it('refreshes only the visible route data from the action list', async () => {
    vi.mocked(api.workBootstrap).mockResolvedValue(makeWorkBootstrapResponse())

    const onOpenChange = vi.fn()
    const { queryClient } = renderWithQueryClient(<CommandPalette open={true} onOpenChange={onOpenChange} />)
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    fireEvent.click(screen.getByText('Refresh data'))

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
    expect(invalidateQueries).toHaveBeenCalledTimes(2)
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['work-bootstrap'],
      refetchType: 'active',
    })
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['issues'],
      refetchType: 'active',
    })
  })
})
