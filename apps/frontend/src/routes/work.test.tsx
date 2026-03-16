import type { ReactNode } from 'react'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { vi } from 'vitest'

import { WorkPage } from '@/routes/work'
import { makeBootstrapResponse, makeIssueDetail, makeIssueSummary } from '@/test/fixtures'
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
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)
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

    expect(screen.getByText('Investigate retries')).toBeInTheDocument()
    expect(screen.getByText('Active work')).toBeInTheDocument()
    expect(screen.getByText('1 live')).toBeInTheDocument()
    expect(screen.queryByText('Create issue')).not.toBeInTheDocument()
    expect(screen.getByText('Triage, route, and monitor work in one surface')).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'Board view' })).toHaveAttribute('data-state', 'on')
    expect(screen.getByRole('link', { name: /investigate retries/i })).toHaveAttribute('href', '/issues/ISS-1')

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
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)
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
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)
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

  it('uses the grouped mobile board without exposing the desktop view toggle', async () => {
    Object.defineProperty(window, 'innerWidth', {
      configurable: true,
      writable: true,
      value: 390,
    })
    window.dispatchEvent(new Event('resize'))

    const bootstrap = makeBootstrapResponse()
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)
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
    expect(screen.getAllByText('Backlog').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Ready').length).toBeGreaterThan(0)
    expect(screen.getAllByRole('button', { name: 'New' }).length).toBeGreaterThan(0)
  })

  it('confirms issue deletion from the list view before calling the API', async () => {
    const bootstrap = makeBootstrapResponse()
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)
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
