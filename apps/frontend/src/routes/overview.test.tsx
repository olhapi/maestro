import type { ReactNode } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { OverviewPage } from '@/routes/overview'
import { makeBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

let shouldThrowChart = false

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
  ResponsiveContainer: ({ children }: { children: ReactNode }) => {
    if (shouldThrowChart) {
      throw new Error('chart crashed')
    }

    return (
      <svg data-testid="overview-chart" role="img" aria-label="Overview throughput chart">
        {children}
      </svg>
    )
  },
  AreaChart: ({ children }: { children: ReactNode }) => <>{children}</>,
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
  afterEach(() => {
    shouldThrowChart = false
  })

  it('renders overview metrics from bootstrap data', async () => {
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse())

    renderWithQueryClient(<OverviewPage />)

    await waitFor(() => {
      expect(screen.getByText('Running agents')).toBeInTheDocument()
    })

    expect(screen.getByText('Execution tempo')).toBeInTheDocument()
    expect(screen.queryByText('Recent signals')).not.toBeInTheDocument()
    expect(screen.getByText('Retry pressure')).toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: /ISS-1/i })).toHaveLength(2)
  })

  it('contains throughput chart crashes and recovers the chart widget', async () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    shouldThrowChart = true
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse())

    renderWithQueryClient(<OverviewPage />)

    const reloadButton = await screen.findByRole('button', { name: /reload overview throughput chart/i })
    expect(screen.getByText('Running agents')).toBeInTheDocument()

    shouldThrowChart = false
    fireEvent.click(reloadButton)

    await waitFor(() => {
      expect(screen.getByTestId('overview-chart')).toBeInTheDocument()
    })
    expect(screen.getByText('Running agents')).toBeInTheDocument()

    consoleErrorSpy.mockRestore()
  })
})
