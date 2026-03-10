import type { ReactNode } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { SessionDetailPage } from '@/routes/session-detail'
import { makeIssueDetail } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
  useParams: () => ({ identifier: 'ISS-1' }),
}))

vi.mock('@/lib/api', () => ({
  api: {
    getIssue: vi.fn(),
    getIssueExecution: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

describe('SessionDetailPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows live execution details and links back to the issue page', async () => {
    const issue = makeIssueDetail({ state: 'in_progress' })
    vi.mocked(api.getIssue).mockResolvedValue(issue)
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: true,
      phase: 'implementation',
      attempt_number: 2,
      retry_state: 'active',
      session_source: 'live',
      session: {
        issue_id: issue.id,
        issue_identifier: issue.identifier,
        session_id: 'thread-live-turn-live',
        thread_id: 'thread-live',
        turn_id: 'turn-live',
        last_event: 'turn.started',
        last_timestamp: '2026-03-10T12:00:00Z',
        last_message: 'Applying changes',
        input_tokens: 0,
        output_tokens: 0,
        total_tokens: 30,
        events_processed: 1,
        turns_started: 2,
        turns_completed: 1,
        terminal: false,
        history: [{ type: 'turn.started', thread_id: 'thread-live', turn_id: 'turn-live', total_tokens: 30, message: 'Applying changes' }],
      },
      runtime_events: [],
    })

    renderWithQueryClient(<SessionDetailPage />)

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: issue.title })).toBeInTheDocument()
    })

    expect(screen.getByText(issue.title)).toBeInTheDocument()
    expect(screen.getByText('Open issue')).toBeInTheDocument()
    expect(screen.getByText('Live session')).toBeInTheDocument()
    expect(screen.getAllByText('Applying changes').length).toBeGreaterThan(0)
  })

  it('shows persisted paused execution context', async () => {
    const issue = makeIssueDetail({ state: 'in_progress' })
    vi.mocked(api.getIssue).mockResolvedValue(issue)
    vi.mocked(api.getIssueExecution).mockResolvedValue({
      issue_id: issue.id,
      identifier: issue.identifier,
      active: false,
      phase: 'implementation',
      attempt_number: 3,
      failure_class: 'stall_timeout',
      current_error: 'stall_timeout',
      retry_state: 'paused',
      paused_at: '2026-03-10T12:05:00Z',
      pause_reason: 'stall_timeout',
      consecutive_failures: 3,
      pause_threshold: 3,
      session_source: 'persisted',
      session: {
        issue_id: issue.id,
        issue_identifier: issue.identifier,
        session_id: 'thread-paused-turn-paused',
        thread_id: 'thread-paused',
        turn_id: 'turn-paused',
        last_event: 'item.started',
        last_timestamp: '2026-03-10T12:05:00Z',
        last_message: 'Paused after repeated failures',
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

    renderWithQueryClient(<SessionDetailPage />)

    await waitFor(() => {
      expect(screen.getAllByText('Paused').length).toBeGreaterThan(0)
    })

    expect(screen.getByText(/Open the issue page to retry/i)).toBeInTheDocument()
    expect(screen.getByText('Persisted session')).toBeInTheDocument()
    expect(screen.getByText(/stopped retrying after 3 interrupted runs/i)).toBeInTheDocument()
  })
})
