import type { ReactNode } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { WorkPage } from '@/routes/work'
import { makeBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
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
    vi.mocked(api.listIssues).mockResolvedValue({
      items: bootstrap.issues.items,
      total: bootstrap.issues.total,
      limit: 200,
      offset: 0,
    })

    renderWithQueryClient(<WorkPage />)

    await waitFor(() => {
      expect(screen.getByText('Coordinate delivery without leaving the board')).toBeInTheDocument()
    })

    expect(screen.getByText('Investigate retries')).toBeInTheDocument()
    expect(screen.getByText('Active work')).toBeInTheDocument()
    expect(screen.getByText('Create issue')).toBeInTheDocument()
  })
})
