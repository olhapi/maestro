import type { ReactNode } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { OverviewPage } from '@/routes/overview'
import { makeBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

const { ResponsiveContainer } = vi.hoisted(() => ({
  ResponsiveContainer: vi.fn(({ children }: { children: ReactNode }) => {
    return (
      <svg data-testid="overview-chart" role="img" aria-label="Overview chart">
        {children}
      </svg>
    )
  }),
}))

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    children,
    params,
  }: {
    children: ReactNode
    params?: { identifier?: string }
  }) => <a href={params?.identifier ? `/issues/${params.identifier}` : '#'}>{children}</a>,
}))

vi.mock('recharts', () => ({
  ResponsiveContainer,
  LineChart: ({ children }: { children: ReactNode }) => <>{children}</>,
  AreaChart: ({ children }: { children: ReactNode }) => <>{children}</>,
  Line: () => <path />,
  Area: () => <path />,
  CartesianGrid: () => <g />,
  Tooltip: () => null,
  XAxis: () => <g />,
  YAxis: () => <g />,
}))

vi.mock('@/lib/api', () => ({
  api: {
    bootstrap: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

describe('OverviewPage', () => {
  it('renders overview metrics from bootstrap data', async () => {
    const refreshedAt = new Date(Date.now() - 1000).toISOString()
    const bootstrap = makeBootstrapResponse()
    bootstrap.generated_at = refreshedAt
    bootstrap.overview.snapshot.generated_at = refreshedAt

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)

    renderWithQueryClient(<OverviewPage />)

    await waitFor(() => {
      expect(screen.getByText('Running agents')).toBeInTheDocument()
    })

    expect(screen.getByText('Execution health')).toBeInTheDocument()
    expect(screen.getByText('Retry pressure')).toBeInTheDocument()
    expect(screen.getByText('Live token load')).toBeInTheDocument()
    expect(screen.getByText(/runs started, completions, failures, and retries across the last 24 hours/i)).toBeInTheDocument()
    expect(screen.getByText(/hourly burn from final run totals, not live snapshot totals/i)).toBeInTheDocument()
    expect(screen.getByText('Runs started')).toBeInTheDocument()
    expect(screen.getByText('Runs completed')).toBeInTheDocument()
    expect(screen.getByText('Runs failed')).toBeInTheDocument()
    expect(screen.getByText('Retries')).toBeInTheDocument()
    expect(screen.getByText('Execution health').closest('section') as HTMLElement).toHaveClass('lg:items-stretch')
    expect(screen.getAllByRole('link', { name: /ISS-1/i })).toHaveLength(2)
    expect(screen.getAllByTestId('overview-chart')).toHaveLength(2)
    expect(ResponsiveContainer.mock.calls.some(([props]) => props.height === '100%')).toBe(true)
    expect(screen.getByText(/Current running sessions only\. Snapshot refreshed \d+s ago\./)).toBeInTheDocument()
  })
})
