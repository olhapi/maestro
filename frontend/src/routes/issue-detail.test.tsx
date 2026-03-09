import type { ReactNode } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { IssueDetailPage } from '@/routes/issue-detail'
import { makeBootstrapResponse, makeIssueDetail } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
  useNavigate: () => vi.fn(),
  useParams: () => ({ identifier: 'ISS-1' }),
}))

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

vi.mock('@/lib/api', () => ({
  api: {
    bootstrap: vi.fn(),
    getIssue: vi.fn(),
    getIssueExecution: vi.fn(),
    retryIssue: vi.fn(),
    deleteIssue: vi.fn(),
    updateIssue: vi.fn(),
    setIssueState: vi.fn(),
    setIssueBlockers: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

describe('IssueDetailPage', () => {
  it('shows interrupted persisted session details instead of an idle no-session view', async () => {
    const bootstrap = makeBootstrapResponse()
    const issue = makeIssueDetail({ state: 'in_progress' })
    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)
    vi.mocked(api.getIssue).mockResolvedValue(issue)
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: 'implementation',
      attempt_number: 2,
      failure_class: 'run_interrupted',
      current_error: 'run_interrupted',
      retry_state: 'none',
      session_source: 'persisted',
      session: {
        issue_id: issue.id,
        issue_identifier: issue.identifier,
        session_id: 'thread-stale-turn-stale',
        thread_id: 'thread-stale',
        turn_id: 'turn-stale',
        last_event: 'turn.started',
        last_timestamp: '2026-03-09T12:00:00Z',
        last_message: '',
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 0,
        events_processed: 1,
        turns_started: 1,
        turns_completed: 0,
        terminal: false,
        history: [],
      },
      runtime_events: [],
    })

    renderWithQueryClient(<IssueDetailPage />)

    await waitFor(() => {
      expect(screen.getByText('Last run interrupted')).toBeInTheDocument()
    })

    expect(screen.getByText('Interrupted')).toBeInTheDocument()
    expect(screen.getByText(/Last session update/i)).toBeInTheDocument()
    expect(screen.getByText('Persisted')).toBeInTheDocument()
  })
})
