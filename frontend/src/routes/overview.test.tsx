import { screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { OverviewPage } from '@/routes/overview'
import { makeBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@/lib/api', () => ({
  api: {
    bootstrap: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

describe('OverviewPage', () => {
  it('renders overview metrics from bootstrap data', async () => {
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse())

    renderWithQueryClient(<OverviewPage />)

    await waitFor(() => {
      expect(screen.getByText('Running agents')).toBeInTheDocument()
    })

    expect(screen.getByText('Event rail')).toBeInTheDocument()
    expect(screen.getByText('Execution tempo')).toBeInTheDocument()
    expect(screen.getByText('Retry pressure')).toBeInTheDocument()
  })
})
