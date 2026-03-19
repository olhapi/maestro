import type { AnchorHTMLAttributes, ReactNode } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { EpicDetailPage } from '@/routes/epic-detail'
import { makeBootstrapResponse } from '@/test/fixtures'
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
})
