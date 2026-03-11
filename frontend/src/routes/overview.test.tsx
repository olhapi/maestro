import type { ReactNode } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { OverviewPage } from '@/routes/overview'
import { makeBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    children,
    params,
  }: {
    children: ReactNode
    params?: { identifier?: string }
  }) => <a href={params?.identifier ? `/issues/${params.identifier}` : '#'}>{children}</a>,
}))

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
    expect(screen.getAllByRole('link', { name: /ISS-1/i })).toHaveLength(2)
  })
})
