import type { ReactNode } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { WorkPage } from '@/routes/work'
import { makeBootstrapResponse, makeIssueDetail } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

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

    fireEvent.change(screen.getByLabelText(/filter by project/i), { target: { value: 'project-1' } })

    await waitFor(() => {
      expect(api.listIssues).toHaveBeenLastCalledWith({
        search: '',
        project_id: 'project-1',
        state: '',
        sort: 'priority_asc',
        limit: 200,
      })
    })
  })
})
