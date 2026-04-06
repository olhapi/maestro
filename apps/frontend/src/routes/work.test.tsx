import { useState, type ComponentPropsWithoutRef, type ReactNode } from 'react'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { vi } from 'vitest'

import { GlobalDashboardProvider } from '@/components/dashboard/global-dashboard-context'
import { WorkPage } from '@/routes/work'
import { makeBootstrapResponse, makeIssueDetail, makeIssueSummary, makeWorkBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient, selectOption } from '@/test/test-utils'

const initialInnerWidth = window.innerWidth

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    children,
    params,
    to,
    ...props
  }: {
    children: ReactNode
    params?: { identifier?: string }
    to?: string
  } & ComponentPropsWithoutRef<'a'>) => (
    <a
      href={
        typeof to === 'string'
          ? to
          : params?.identifier
            ? `/issues/${params.identifier}`
            : '#'
      }
      {...props}
    >
      {children}
    </a>
  ),
  useNavigate: () => vi.fn(),
}))

vi.mock('@/lib/api', () => ({
  api: {
    bootstrap: vi.fn(),
    workBootstrap: vi.fn(),
    listIssues: vi.fn(),
    setIssueState: vi.fn(),
    deleteIssue: vi.fn(),
    getIssue: vi.fn(),
    retryIssue: vi.fn(),
    setIssueBlockers: vi.fn(),
    updateIssue: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

function makeDoneIssues(count: number) {
  return Array.from({ length: count }, (_, index) =>
    makeIssueSummary({
      id: `done-${index + 1}`,
      identifier: `DONE-${String(index + 1).padStart(2, '0')}`,
      title: `Done task ${index + 1}`,
      state: 'done',
      priority: 2,
      updated_at: `2026-03-${String((index % 28) + 1).padStart(2, '0')}T11:00:00Z`,
    }),
  )
}

function sortIssuesForTest(issues: IssueSummary[], sort: string) {
  const sorted = [...issues]
  sorted.sort((left, right) => {
    switch (sort) {
      case 'identifier_asc':
        return left.identifier.localeCompare(right.identifier)
      case 'priority_asc':
        if (left.priority !== right.priority) {
          return left.priority - right.priority
        }
        return new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime()
      case 'state_asc': {
        const stateDelta = left.state.localeCompare(right.state)
        if (stateDelta !== 0) {
          return stateDelta
        }
        if (left.priority !== right.priority) {
          return left.priority - right.priority
        }
        return new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime()
      }
      case 'updated_desc':
      default: {
        const updatedDelta =
          new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime()
        if (updatedDelta !== 0) {
          return updatedDelta
        }
        return new Date(right.created_at).getTime() - new Date(left.created_at).getTime()
      }
    }
  })
  return sorted
}

describe('WorkPage', () => {
  const mockWorkBootstrap = (bootstrap = makeBootstrapResponse()) => {
    vi.mocked(api.workBootstrap).mockResolvedValue(makeWorkBootstrapResponse({
      generated_at: bootstrap.generated_at,
      overview: {
        board: bootstrap.overview.board,
        snapshot: {
          running: bootstrap.overview.snapshot.running,
          retrying: bootstrap.overview.snapshot.retrying,
          paused: bootstrap.overview.snapshot.paused,
        },
      },
      projects: bootstrap.projects,
      epics: bootstrap.epics,
      issues: bootstrap.issues,
      sessions: bootstrap.sessions,
    }))
  }

  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      writable: true,
      value: initialInnerWidth,
    })
    window.dispatchEvent(new Event('resize'))
  })

  it('renders board data from bootstrap and issues queries', async () => {
    const bootstrap = makeBootstrapResponse()
    mockWorkBootstrap(bootstrap)
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())
    vi.mocked(api.listIssues).mockResolvedValue({
      items: bootstrap.issues.items,
      total: bootstrap.issues.total,
      limit: 200,
      offset: 0,
    })

    renderWithQueryClient(<WorkPage />)

    await waitFor(() => {
      expect(screen.getByText('Coordinate work on one board')).toBeInTheDocument()
    })

    expect(screen.getByRole('heading', { name: 'Coordinate work on one board' })).toHaveClass('w-full')
    expect(screen.getByText('This surface is now optimized for live triage: drag work between lanes, inspect execution context in-place, and dive into full issue pages only when needed.')).toHaveClass('max-w-none')
    expect(screen.getByText('Investigate retries')).toBeInTheDocument()
    expect(screen.getByText('Active work')).toBeInTheDocument()
    expect(screen.getByText('1 live')).toBeInTheDocument()
    expect(screen.queryByText('Create issue')).not.toBeInTheDocument()
    expect(screen.getByText('Triage, route, and monitor work in one surface')).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'Board view' })).toHaveAttribute('data-state', 'on')
    expect(screen.queryByRole('button', { name: /collapse backlog status row/i })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('radio', { name: 'List view' }))
    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: 'Issue' })).toBeInTheDocument()
    })
    expect(screen.getByText('Triage, route, and monitor work in one surface')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /iss-1.*investigate retries/i }))

    await waitFor(() => {
      expect(screen.getByText('turn.started')).toBeInTheDocument()
    })
  })

  it('limits the done lane in board view without affecting list view results', async () => {
    const base = makeBootstrapResponse()
    const doneIssues = makeDoneIssues(35)
    const bootstrap = {
      ...base,
      overview: {
        ...base.overview,
        board: {
          ...base.overview.board,
          ready: 0,
          in_progress: 0,
          done: doneIssues.length,
        },
      },
      issues: {
        items: doneIssues,
        total: 114,
        limit: 200,
        offset: 0,
        counts: {
          backlog: 0,
          ready: 0,
          in_progress: 0,
          in_review: 0,
          done: 114,
          cancelled: 0,
        },
      },
    }

    mockWorkBootstrap(bootstrap)
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())
    vi.mocked(api.listIssues).mockResolvedValue({
      items: doneIssues,
      total: 114,
      limit: 200,
      offset: 0,
      counts: {
        backlog: 0,
        ready: 0,
        in_progress: 0,
        in_review: 0,
        done: 114,
        cancelled: 0,
      },
    })

    renderWithQueryClient(<WorkPage />)

    await waitFor(() => {
      expect(screen.getByText('Coordinate work on one board')).toBeInTheDocument()
    })

    expect(screen.getByText('114')).toBeInTheDocument()
    expect(screen.getAllByText(/Done task \d+/)).toHaveLength(30)
    expect(screen.getByText('Showing 30 of 35')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Load 5 more' })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('radio', { name: 'List view' }))

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: 'Issue' })).toBeInTheDocument()
    })

    expect(screen.queryByRole('button', { name: 'Load 5 more' })).not.toBeInTheDocument()
    expect(screen.getAllByText(/Done task \d+/)).toHaveLength(35)
  })

  it('filters issues by project from the work toolbar', async () => {
    const bootstrap = makeBootstrapResponse()
    mockWorkBootstrap(bootstrap)
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())
    vi.mocked(api.listIssues).mockResolvedValue({
      items: bootstrap.issues.items,
      total: bootstrap.issues.total,
      limit: 200,
      offset: 0,
    })

    renderWithQueryClient(<WorkPage />)

    await waitFor(() => {
      expect(screen.getByText('Coordinate work on one board')).toBeInTheDocument()
    })

    await selectOption(/filter by project/i, /platform/i)

    await waitFor(() => {
      expect(api.listIssues).toHaveBeenLastCalledWith({
        search: '',
        project_id: 'project-1',
        state: '',
        issue_type: '',
        sort: 'priority_asc',
        limit: 200,
      })
    })
  })

  it('filters issues by type from the work toolbar', async () => {
    const bootstrap = makeBootstrapResponse({
      issues: {
        ...makeBootstrapResponse().issues,
        items: [
          makeIssueSummary(),
          makeIssueSummary({
            id: 'issue-2',
            identifier: 'ISS-2',
            title: 'Nightly sync',
            issue_type: 'recurring',
          }),
        ],
      },
    })
    mockWorkBootstrap(bootstrap)
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail({ issue_type: 'recurring', cron: '*/15 * * * *', enabled: true }))
    vi.mocked(api.listIssues).mockResolvedValue({
      items: bootstrap.issues.items,
      total: bootstrap.issues.total,
      limit: 200,
      offset: 0,
    })

    renderWithQueryClient(<WorkPage />)

    await waitFor(() => {
      expect(screen.getByText('Coordinate work on one board')).toBeInTheDocument()
    })

    await selectOption(/filter by issue type/i, /recurring/i)

    await waitFor(() => {
      expect(api.listIssues).toHaveBeenLastCalledWith({
        search: '',
        project_id: '',
        state: '',
        issue_type: 'recurring',
        sort: 'priority_asc',
        limit: 200,
      })
    })
  })

  it('sorts the work list from header clicks and keeps the chosen sort after remount', async () => {
    const bootstrap = makeBootstrapResponse({
      issues: {
        ...makeBootstrapResponse().issues,
        items: [
          makeIssueSummary({
            id: 'issue-1',
            identifier: 'ISS-2',
            title: 'Investigate retries',
            priority: 1,
            updated_at: '2026-03-09T10:00:00Z',
          }),
          makeIssueSummary({
            id: 'issue-2',
            identifier: 'ISS-1',
            title: 'Triage release',
            priority: 5,
            updated_at: '2026-03-09T11:00:00Z',
          }),
        ],
        total: 2,
      },
    })
    mockWorkBootstrap(bootstrap)
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())
    vi.mocked(api.listIssues).mockImplementation(async (filters) => ({
      items: sortIssuesForTest(bootstrap.issues.items, filters.sort || 'priority_asc'),
      total: bootstrap.issues.total,
      limit: 200,
      offset: 0,
    }))

    function Harness() {
      const [visible, setVisible] = useState(true)

      return (
        <GlobalDashboardProvider>
          <button onClick={() => setVisible((current) => !current)} type="button">
            Toggle work page
          </button>
          {visible ? <WorkPage /> : null}
        </GlobalDashboardProvider>
      )
    }

    renderWithQueryClient(<Harness />)

    await waitFor(() => {
      expect(screen.getByText('Coordinate work on one board')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('radio', { name: 'List view' }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Sort by Issue' })).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: 'Sort by Priority' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Sort by Project' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Sort by Epic' })).toBeInTheDocument()

    await waitFor(() => {
      const issueButtons = within(screen.getByRole('table')).getAllByRole('button', {
        name: /ISS-/,
      })
      expect(issueButtons[0]).toHaveTextContent('ISS-2')
      expect(issueButtons[1]).toHaveTextContent('ISS-1')
    })

    fireEvent.click(screen.getByRole('button', { name: 'Sort by Issue' }))

    await waitFor(() => {
      expect(api.listIssues).toHaveBeenLastCalledWith({
        search: '',
        project_id: '',
        state: '',
        issue_type: '',
        sort: 'identifier_asc',
        limit: 200,
      })
    })

    await waitFor(() => {
      const issueButtons = within(screen.getByRole('table')).getAllByRole('button', {
        name: /ISS-/,
      })
      expect(issueButtons[0]).toHaveTextContent('ISS-1')
      expect(issueButtons[1]).toHaveTextContent('ISS-2')
    })

    fireEvent.click(screen.getByRole('button', { name: 'Toggle work page' }))
    expect(screen.queryByText('Coordinate work on one board')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Toggle work page' }))

    await waitFor(() => {
      expect(api.listIssues).toHaveBeenLastCalledWith({
        search: '',
        project_id: '',
        state: '',
        issue_type: '',
        sort: 'identifier_asc',
        limit: 200,
      })
    })
  })

  it('renders live, blocked, and priority badges with truncated list cells', async () => {
    const bootstrap = makeBootstrapResponse({
      issues: {
        ...makeBootstrapResponse().issues,
        items: [
          makeIssueSummary({
            title: 'Implement an exceptionally long issue title that should be truncated in the list view',
            priority: 1,
            project_name: 'Platform with a very long project name that should not overflow the table',
            epic_name: 'Observability with a very long epic name that should truncate cleanly',
            is_blocked: true,
          }),
        ],
        total: 1,
      },
    })
    mockWorkBootstrap(bootstrap)
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())
    vi.mocked(api.listIssues).mockResolvedValue({
      items: bootstrap.issues.items,
      total: bootstrap.issues.total,
      limit: 200,
      offset: 0,
    })

    renderWithQueryClient(<WorkPage />)

    await waitFor(() => {
      expect(screen.getByText('Coordinate work on one board')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('radio', { name: 'List view' }))

    const table = await screen.findByRole('table')
    const issueButton = within(table).getByRole('button', {
      name: /ISS-1/i,
    })
    expect(issueButton).toHaveTextContent('Live')
    expect(issueButton).toHaveTextContent('Blocked')

    const title = within(issueButton).getByText(
      'Implement an exceptionally long issue title that should be truncated in the list view',
    )
    expect(title).toHaveClass('truncate')

    expect(within(table).getByRole('link', {
      name: 'Platform with a very long project name that should not overflow the table',
    })).toHaveClass('truncate')
    expect(within(table).getByRole('link', {
      name: 'Observability with a very long epic name that should truncate cleanly',
    })).toHaveClass('truncate')
    expect(within(table).getByLabelText('Priority 1')).toBeInTheDocument()
  })

  it('uses the grouped mobile board without exposing the desktop view toggle', async () => {
    try {
      Object.defineProperty(window, 'innerWidth', {
        configurable: true,
        writable: true,
        value: 390,
      })
      await act(async () => {
        window.dispatchEvent(new Event('resize'))
      })

      const bootstrap = makeBootstrapResponse()
      mockWorkBootstrap(bootstrap)
      vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())
      vi.mocked(api.listIssues).mockResolvedValue({
        items: bootstrap.issues.items,
        total: bootstrap.issues.total,
        limit: 200,
        offset: 0,
      })

      renderWithQueryClient(<WorkPage />)

      await waitFor(() => {
        expect(screen.getByText('Review work state by state')).toBeInTheDocument()
      })

      expect(
        screen.queryByText('Ready, in progress, and in review across the portfolio.'),
      ).not.toBeInTheDocument()
      expect(screen.queryByText('Planned work not yet routed into execution.')).not.toBeInTheDocument()
      expect(screen.queryByText('Issues already closed out successfully.')).not.toBeInTheDocument()
      expect(screen.queryByText('Issues currently attached to a running workspace.')).not.toBeInTheDocument()
      expect(screen.queryByRole('radio', { name: 'Board view' })).not.toBeInTheDocument()
      expect(screen.getByRole('button', { name: /collapse backlog status row/i })).toHaveAttribute(
        'aria-expanded',
        'true',
      )
      expect(screen.getByRole('button', { name: /collapse ready status row/i })).toHaveAttribute(
        'aria-expanded',
        'true',
      )
      expect(screen.getAllByRole('button', { name: 'New' }).length).toBeGreaterThan(0)
    } finally {
      Object.defineProperty(window, 'innerWidth', {
        configurable: true,
        writable: true,
        value: initialInnerWidth,
      })
      await act(async () => {
        window.dispatchEvent(new Event('resize'))
      })
    }
  })

  it('confirms issue deletion from the list view before calling the API', async () => {
    const bootstrap = makeBootstrapResponse()
    mockWorkBootstrap(bootstrap)
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())
    vi.mocked(api.listIssues).mockResolvedValue({
      items: bootstrap.issues.items,
      total: bootstrap.issues.total,
      limit: 200,
      offset: 0,
    })
    vi.mocked(api.deleteIssue).mockResolvedValue({ deleted: true })

    renderWithQueryClient(<WorkPage />)

    await waitFor(() => {
      expect(screen.getByText('Coordinate work on one board')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('radio', { name: 'List view' }))

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: 'Issue' })).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /delete issue/i }))
    expect(api.deleteIssue).not.toHaveBeenCalled()

    const confirmDialog = await screen.findByRole('dialog', {
      name: /delete iss-1\?/i,
    })
    fireEvent.click(
      within(confirmDialog).getByRole('button', { name: /delete issue/i }),
    )

    await waitFor(() => {
      expect(api.deleteIssue).toHaveBeenCalledWith('ISS-1')
    })
  })
})
