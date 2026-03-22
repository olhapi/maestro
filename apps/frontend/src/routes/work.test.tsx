import { useState, type ReactNode } from 'react'
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
  }: {
    children: ReactNode
    params?: { identifier?: string }
  }) => <a href={params?.identifier ? `/issues/${params.identifier}` : '#'}>{children}</a>,
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

    fireEvent.click(screen.getByRole('button', { name: /iss-1 investigate retries/i }))

    await waitFor(() => {
      expect(screen.getByText('turn.started')).toBeInTheDocument()
    })
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

  it('retains selected filters after the page remounts', async () => {
    const bootstrap = makeBootstrapResponse({
      projects: [
        makeBootstrapResponse().projects[0],
        {
          ...makeBootstrapResponse().projects[0],
          id: 'project-2',
          name: 'Operations',
        },
      ],
      issues: {
        ...makeBootstrapResponse().issues,
        items: [
          makeIssueSummary(),
          makeIssueSummary({
            id: 'issue-2',
            identifier: 'ISS-2',
            title: 'Nightly sync',
            issue_type: 'recurring',
            project_id: 'project-2',
            project_name: 'Operations',
            state: 'in_progress',
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

    await selectOption(/filter by project/i, /operations/i)
    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: /filter by state/i })).toBeInTheDocument()
    })
    await selectOption(/filter by state/i, /in progress/i)
    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: /filter by issue type/i })).toBeInTheDocument()
    })
    await selectOption(/filter by issue type/i, /recurring/i)
    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: /sort issues/i })).toBeInTheDocument()
    })
    await selectOption(/sort issues/i, /recently updated/i)

    fireEvent.click(screen.getByRole('button', { name: 'Toggle work page' }))
    expect(screen.queryByText('Coordinate work on one board')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Toggle work page' }))

    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: /filter by project/i })).toBeInTheDocument()
    })

    expect(screen.getByRole('combobox', { name: /filter by project/i })).toHaveTextContent('Operations')
    expect(screen.getByRole('combobox', { name: /filter by state/i })).toHaveTextContent('In Progress')
    expect(screen.getByRole('combobox', { name: /filter by issue type/i })).toHaveTextContent('Recurring')
    expect(screen.getByRole('combobox', { name: /sort issues/i })).toHaveTextContent('Recently updated')

    await waitFor(() => {
      expect(api.listIssues).toHaveBeenLastCalledWith({
        search: '',
        project_id: 'project-2',
        state: 'in_progress',
        issue_type: 'recurring',
        sort: 'updated_desc',
        limit: 200,
      })
    })
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
