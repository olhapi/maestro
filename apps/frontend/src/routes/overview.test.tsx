import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

import { TooltipProvider } from '@/components/ui/tooltip'
import { OverviewPage } from '@/routes/overview'
import { makeBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'
import { formatCompactNumber } from '@/lib/utils'

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
  Line: ({
    dataKey,
    name,
    stroke,
    strokeOpacity,
    strokeWidth,
  }: {
    dataKey?: string
    name?: string
    stroke?: string
    strokeOpacity?: number
    strokeWidth?: number
  }) => (
    <path
      data-testid={dataKey ? `overview-line-${dataKey}` : 'overview-line'}
      data-name={name}
      data-stroke={stroke}
      data-stroke-opacity={String(strokeOpacity ?? 1)}
      data-stroke-width={String(strokeWidth ?? '')}
    />
  ),
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

afterEach(() => {
  vi.useRealTimers()
})

function renderOverviewWithBootstrapData(bootstrap = makeBootstrapResponse()) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        staleTime: Number.POSITIVE_INFINITY,
      },
    },
  })
  queryClient.setQueryData(['bootstrap'], bootstrap)

  const rendered = render(
    <QueryClientProvider client={queryClient}>
      <TooltipProvider delayDuration={0}>
        <OverviewPage />
      </TooltipProvider>
    </QueryClientProvider>,
  )

  return { queryClient, ...rendered }
}

function snapshotRefreshDetail() {
  return screen.getByText((_, element) => {
    return element?.tagName === 'P' && element.textContent?.startsWith('Current running sessions only. Snapshot refreshed ') === true
  })
}

function getOverviewListPanel(title: string) {
  const heading = screen.getByRole('heading', { name: title })
  const card = heading.closest('[class*="max-h-[520px]"]')

  if (!(card instanceof HTMLElement)) {
    throw new Error(`Unable to find overview card for ${title}`)
  }

  const content = card.children[1]

  if (!(content instanceof HTMLElement)) {
    throw new Error(`Unable to find overview card body for ${title}`)
  }

  return { card, content }
}

describe('OverviewPage', () => {
  it('renders overview metrics from bootstrap data and keeps the snapshot age ticking', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-03-11T10:00:00Z'))

    const refreshedAt = '2026-03-11T09:59:59Z'
    const bootstrap = makeBootstrapResponse()
    bootstrap.generated_at = refreshedAt
    bootstrap.overview.snapshot.generated_at = refreshedAt
    const burnTotal = bootstrap.overview.series.reduce((total, point) => total + (point.tokens ?? 0), 0)

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)

    const { queryClient } = renderOverviewWithBootstrapData(bootstrap)

    expect(screen.getByText('Execution health')).toBeInTheDocument()
    expect(screen.getByText('Retry pressure')).toBeInTheDocument()
    expect(screen.getByText('Live token load')).toBeInTheDocument()
    expect(screen.getByText(/runs started, completions, failures, and retries across the last 24 hours/i)).toBeInTheDocument()
    expect(screen.getByText(/hourly burn from final run totals, not live snapshot totals/i)).toBeInTheDocument()
    expect(screen.getByText(new RegExp(`24h burn\\s+·\\s+${formatCompactNumber(burnTotal)}\\s+total`, 'i'))).toBeInTheDocument()
    expect(screen.queryByText(new RegExp(`^${formatCompactNumber(burnTotal)}\\s+total$`, 'i'))).not.toBeInTheDocument()
    expect(screen.getByText('Runs started')).toBeInTheDocument()
    expect(screen.getByText('Runs completed')).toBeInTheDocument()
    expect(screen.getByText('Runs failed')).toBeInTheDocument()
    expect(screen.getByText('Retries')).toBeInTheDocument()
    expect(screen.getByText('Execution health').closest('section') as HTMLElement).toHaveClass('lg:items-stretch')
    expect(screen.getAllByRole('link', { name: /ISS-1/i })).toHaveLength(2)
    expect(screen.getAllByTestId('overview-chart')).toHaveLength(2)
    expect(ResponsiveContainer.mock.calls.some(([props]) => props.height === '100%')).toBe(true)
    expect(snapshotRefreshDetail()).toHaveTextContent('Current running sessions only. Snapshot refreshed 1s ago.')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000)
    })

    expect(snapshotRefreshDetail()).toHaveTextContent('Current running sessions only. Snapshot refreshed 3s ago.')

    const refreshedBootstrap = makeBootstrapResponse()
    refreshedBootstrap.generated_at = new Date(Date.now()).toISOString()
    refreshedBootstrap.overview.snapshot.generated_at = refreshedBootstrap.generated_at

    await act(async () => {
      queryClient.setQueryData(['bootstrap'], refreshedBootstrap)
      await vi.advanceTimersByTimeAsync(0)
    })

    expect(snapshotRefreshDetail()).toHaveTextContent('Current running sessions only. Snapshot refreshed 0s ago.')
  })

  it('stacks active run rows on mobile to keep badges in view', async () => {
    const bootstrap = makeBootstrapResponse()
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)

    renderWithQueryClient(<OverviewPage />)

    await waitFor(() => {
      expect(screen.getByText('Active runs')).toBeInTheDocument()
    })

    const activeRunLink = screen.getAllByRole('link', { name: /ISS-1/i })[0]
    const row = activeRunLink.querySelector('div')

    expect(row).toHaveClass('flex', 'min-w-0', 'flex-col', 'gap-2')
    expect(row).toHaveClass('sm:flex-row', 'sm:items-start', 'sm:justify-between')
    expect(within(row!).getByText('ISS-1')).toHaveClass('break-words', '[overflow-wrap:anywhere]')
  })

  it('caps active runs and pending retries with the same scrollable panel height', async () => {
    const bootstrap = makeBootstrapResponse()
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)

    renderWithQueryClient(<OverviewPage />)

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Active runs' })).toBeInTheDocument()
      expect(screen.getByRole('heading', { name: 'Pending retries' })).toBeInTheDocument()
    })

    const activeRunsPanel = getOverviewListPanel('Active runs')
    const pendingRetriesPanel = getOverviewListPanel('Pending retries')

    expect(activeRunsPanel.card).toHaveClass('flex', 'max-h-[520px]', 'flex-col', 'overflow-hidden')
    expect(pendingRetriesPanel.card).toHaveClass('flex', 'max-h-[520px]', 'flex-col', 'overflow-hidden')

    expect(activeRunsPanel.content).toHaveClass('min-h-0', 'flex-1', 'space-y-2.5', 'overflow-y-auto', 'pr-1')
    expect(pendingRetriesPanel.content).toHaveClass('min-h-0', 'flex-1', 'space-y-2.5', 'overflow-y-auto', 'pr-1')
  })

  it('highlights the matching execution line while its legend chip is hovered', async () => {
    const bootstrap = makeBootstrapResponse()
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)

    renderWithQueryClient(<OverviewPage />)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Runs started' })).toBeInTheDocument()
    })

    const runsStartedLegend = screen.getByRole('button', { name: 'Runs started' })
    const runsStartedLine = screen.getByTestId('overview-line-runs_started')
    const runsCompletedLine = screen.getByTestId('overview-line-runs_completed')

    expect(runsStartedLegend).toHaveAttribute('data-active', 'false')
    expect(runsStartedLine).toHaveAttribute('data-stroke-width', '2.5')
    expect(runsCompletedLine).toHaveAttribute('data-stroke-opacity', '1')

    fireEvent.mouseEnter(runsStartedLegend)

    expect(runsStartedLegend).toHaveAttribute('data-active', 'true')
    expect(runsStartedLine).toHaveAttribute('data-stroke-width', '3.5')
    expect(runsStartedLine).toHaveAttribute('data-stroke-opacity', '1')
    expect(runsCompletedLine).toHaveAttribute('data-stroke-opacity', '0.28')

    fireEvent.mouseLeave(runsStartedLegend)

    expect(runsStartedLegend).toHaveAttribute('data-active', 'false')
    expect(runsStartedLine).toHaveAttribute('data-stroke-width', '2.5')
    expect(runsCompletedLine).toHaveAttribute('data-stroke-opacity', '1')
  })
})
