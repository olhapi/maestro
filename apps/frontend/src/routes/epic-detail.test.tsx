import type { AnchorHTMLAttributes, ReactNode } from 'react'
import { screen, waitFor, within } from '@testing-library/react'
import { vi } from 'vitest'

import { EpicDetailPage } from '@/routes/epic-detail'
import { makeBootstrapResponse, makeIssueSummary } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    children,
    to,
    ...props
  }: {
    children: ReactNode
    to?: string
    params?: Record<string, string>
  } & AnchorHTMLAttributes<HTMLAnchorElement>) => (
    <a href={to ?? '#'} {...props}>
      {children}
    </a>
  ),
  useNavigate: () => vi.fn(),
  useParams: () => ({ epicId: 'epic-1' }),
}))

vi.mock('@/components/dashboard/issue-card', () => ({
  IssueCard: ({ issue }: { issue: { identifier: string } }) => <div>{issue.identifier}</div>,
}))

vi.mock('@/components/dashboard/issue-preview-sheet', () => ({
  IssuePreviewSheet: () => null,
}))

vi.mock('@/components/forms', () => ({
  IssueDialog: () => null,
}))

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
  },
}))

vi.mock('@/lib/api', () => ({
  api: {
    bootstrap: vi.fn(),
    getEpic: vi.fn(),
    setIssueState: vi.fn(),
    createIssue: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

function makeEpicDetailResponse() {
  const bootstrap = makeBootstrapResponse()
  return {
    epic: bootstrap.epics[0],
    project: bootstrap.projects[0],
    sibling_epics: [
      bootstrap.epics[0],
      {
        ...bootstrap.epics[0],
        id: 'epic-2',
        name: 'Adjacent epic',
        counts: {
          backlog: 0,
          ready: 0,
          in_progress: 0,
          in_review: 0,
          done: 0,
          cancelled: 0,
        },
      },
    ],
    issues: bootstrap.issues,
  }
}

describe('EpicDetailPage', () => {
  it('renders the renamed epic detail sections and summary badges', async () => {
    vi.mocked(api.bootstrap).mockResolvedValue(makeBootstrapResponse())
    vi.mocked(api.getEpic).mockResolvedValue(makeEpicDetailResponse())

    renderWithQueryClient(<EpicDetailPage />)

    await waitFor(() => {
      expect(screen.getByText('What changed in this epic')).toBeInTheDocument()
    })

    expect(screen.queryByText('Recent work')).not.toBeInTheDocument()
    expect(screen.queryByText('Sibling epics')).not.toBeInTheDocument()
    expect(screen.queryByText('Epic lanes')).not.toBeInTheDocument()
    expect(screen.getByText('Issues')).toBeInTheDocument()
    expect(screen.getByText('Adjacent delivery arcs')).toBeInTheDocument()
    expect(screen.getByText('State of work across the epic')).toBeInTheDocument()
    expect(screen.getByText(/0 active/i)).toBeInTheDocument()
  })

  it('shows the full epic issue total and a placeholder when a lane has no loaded cards', async () => {
    const bootstrap = makeBootstrapResponse()
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)
    vi.mocked(api.getEpic).mockResolvedValue({
      epic: {
        ...bootstrap.epics[0],
        total_count: 19,
        counts: {
          backlog: 0,
          ready: 1,
          in_progress: 1,
          in_review: 17,
          done: 0,
          cancelled: 0,
        },
      },
      project: bootstrap.projects[0],
      sibling_epics: bootstrap.epics,
      issues: {
        items: [
          makeIssueSummary({ id: 'issue-1', identifier: 'ISS-1', state: 'ready' }),
          makeIssueSummary({
            id: 'issue-2',
            identifier: 'ISS-2',
            state: 'in_progress',
          }),
        ],
        total: 2,
        limit: 200,
        offset: 0,
      },
    })

    renderWithQueryClient(<EpicDetailPage />)

    await waitFor(() => {
      expect(screen.getByText('State of work across the epic')).toBeInTheDocument()
    })

    const issuesStat = screen.getByText('Issues').closest('div')
    expect(issuesStat).not.toBeNull()
    expect(within(issuesStat as HTMLElement).getByText('19')).toBeInTheDocument()

    const inReviewLabel = screen.getByText('In Review')
    const inReviewCard = inReviewLabel.closest('div')
    expect(inReviewCard).not.toBeNull()
    expect(within(inReviewCard as HTMLElement).getByText('17')).toBeInTheDocument()
    expect(screen.getByText('No loaded issues on this page.')).toBeInTheDocument()
    expect(screen.queryByText(/No issues in in review/i)).not.toBeInTheDocument()
  })
})
