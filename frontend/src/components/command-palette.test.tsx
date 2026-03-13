import { screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { CommandPalette } from '@/components/command-palette'
import { makeBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

const navigate = vi.fn()

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
}))

vi.mock('@/lib/api', () => ({
  api: {
    bootstrap: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

describe('CommandPalette', () => {
  beforeAll(() => {
    vi.stubGlobal('ResizeObserver', ResizeObserverMock)
    window.HTMLElement.prototype.scrollIntoView = vi.fn()
  })

  afterAll(() => {
    vi.unstubAllGlobals()
  })

  it('focuses the search input when the palette opens', async () => {
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse())

    const onOpenChange = vi.fn()
    renderWithQueryClient(<CommandPalette open={true} onOpenChange={onOpenChange} />)

    await waitFor(() => {
      expect(
        screen.getByPlaceholderText('Search issues, projects, sessions, or actions...'),
      ).toHaveFocus()
    })
  })
})
