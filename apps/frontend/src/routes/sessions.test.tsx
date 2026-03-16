import type { ReactNode } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { SessionsPage } from '@/routes/sessions'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    children,
    className,
    params,
    to,
  }: {
    children: ReactNode
    className?: string
    params?: { identifier?: string }
    to: string
  }) => (
    <a className={className} data-identifier={params?.identifier} data-to={to}>
      {children}
    </a>
  ),
}))

vi.mock('@/lib/api', () => ({
  api: {
    listSessions: vi.fn(),
    listRuntimeEvents: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

function makeSessionsResponse() {
  return {
    sessions: {
      'RUN-LIVE-A': {
        issue_id: 'issue-live-a',
        issue_identifier: 'RUN-LIVE-A',
        session_id: 'thread-live-a-turn-live-a',
        thread_id: 'thread-live-a',
        turn_id: 'turn-live-a',
        last_event: 'turn.started',
        last_timestamp: '2026-03-10T11:59:30Z',
        last_message: 'Applying migration changes after approval review',
        input_tokens: 10,
        output_tokens: 20,
        total_tokens: 30,
        events_processed: 6,
        turns_started: 4,
        turns_completed: 3,
        terminal: false,
        history: [],
      },
      'RUN-LIVE-Z': {
        issue_id: 'issue-live-z',
        issue_identifier: 'RUN-LIVE-Z',
        session_id: 'thread-live-z-turn-live-z',
        thread_id: 'thread-live-z',
        turn_id: 'turn-live-z',
        last_event: 'turn.started',
        last_timestamp: '2026-03-10T11:58:30Z',
        last_message: 'Checking follow-up changes',
        input_tokens: 8,
        output_tokens: 10,
        total_tokens: 18,
        events_processed: 3,
        turns_started: 2,
        turns_completed: 1,
        terminal: false,
        history: [],
      },
    },
    entries: [
      {
        issue_id: 'issue-live-a',
        issue_identifier: 'RUN-LIVE-A',
        issue_title: 'Alpha issue',
        source: 'live',
        active: true,
        status: 'waiting',
        pending_interrupt: {
          id: 'interrupt-1',
          kind: 'approval',
          requested_at: '2026-03-10T11:59:30Z',
          last_activity_at: '2026-03-10T11:59:30Z',
          last_activity: 'Applying migration changes after approval review',
          collaboration_mode: 'plan',
          approval: {
            decisions: [{ value: 'approved', label: 'Approve once' }],
          },
        },
        phase: 'implementation',
        attempt: 2,
        run_kind: 'run_started',
        updated_at: '2026-03-10T11:59:30Z',
        last_event: 'turn.started',
        last_message: 'Applying migration changes after approval review',
        total_tokens: 30,
        events_processed: 6,
        turns_started: 4,
        turns_completed: 3,
        terminal: false,
        history: [],
        error: '',
      },
      {
        issue_id: 'issue-live-z',
        issue_identifier: 'RUN-LIVE-Z',
        issue_title: 'Zulu issue',
        source: 'live',
        active: true,
        status: 'active',
        phase: 'implementation',
        attempt: 1,
        run_kind: 'run_started',
        updated_at: '2026-03-10T11:58:30Z',
        last_event: 'turn.started',
        last_message: 'Checking follow-up changes',
        total_tokens: 18,
        events_processed: 3,
        turns_started: 2,
        turns_completed: 1,
        terminal: false,
        history: [],
        error: '',
      },
      {
        issue_id: 'issue-paused',
        issue_identifier: 'RUN-PAUSED',
        issue_title: 'Bravo issue',
        source: 'persisted',
        active: false,
        status: 'paused',
        phase: 'review',
        attempt: 3,
        run_kind: 'retry_paused',
        failure_class: 'stall_timeout',
        updated_at: '2026-03-10T11:55:00Z',
        last_event: 'run.failed',
        last_message: 'Paused after repeated failures',
        total_tokens: 48,
        events_processed: 8,
        turns_started: 3,
        turns_completed: 2,
        terminal: false,
        terminal_reason: '',
        history: [],
        error: 'stall_timeout',
      },
    ],
  }
}

describe('SessionsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders a compact sessions overview with issue titles and open-issue links', async () => {
    vi.mocked(api.listSessions).mockResolvedValue(makeSessionsResponse())
    vi.mocked(api.listRuntimeEvents).mockResolvedValue({ events: [] })

    renderWithQueryClient(<SessionsPage />)

    await waitFor(() => {
      expect(screen.getByText('Run transparency')).toBeInTheDocument()
    })

    expect(screen.getByText('Alpha issue')).toBeInTheDocument()
    expect(screen.getByText('Zulu issue')).toBeInTheDocument()
    expect(screen.getByText('Bravo issue')).toBeInTheDocument()
    expect(screen.getByText('Plan turn')).toBeInTheDocument()
    expect(screen.queryByText('Show details')).not.toBeInTheDocument()
    expect(screen.queryByText('Recent session history')).not.toBeInTheDocument()

    const links = screen.getAllByText('Open issue')
    expect(links).toHaveLength(3)
    expect(links[0]).toHaveAttribute('data-to', '/issues/$identifier')
    expect(links[0]).toHaveAttribute('data-identifier', 'RUN-LIVE-A')
  })

  it('truncates the short session summary to two lines with css', async () => {
    vi.mocked(api.listSessions).mockResolvedValue(makeSessionsResponse())
    vi.mocked(api.listRuntimeEvents).mockResolvedValue({ events: [] })

    renderWithQueryClient(<SessionsPage />)

    await waitFor(() => {
      expect(screen.getByTestId('session-summary-RUN-LIVE-A')).toBeInTheDocument()
    })

    expect(screen.getByTestId('session-summary-RUN-LIVE-A')).toHaveClass('line-clamp-2')
  })

  it('marks stale live sessions as quiet', async () => {
    vi.mocked(api.listSessions).mockResolvedValue({
      ...makeSessionsResponse(),
      entries: [
        {
          ...makeSessionsResponse().entries[0],
          updated_at: '2026-03-09T10:00:00Z',
        },
      ],
    })
    vi.mocked(api.listRuntimeEvents).mockResolvedValue({ events: [] })

    renderWithQueryClient(<SessionsPage />)

    await waitFor(() => {
      expect(screen.getByText('Quiet')).toBeInTheDocument()
    })
  })

  it('shows an empty state when there are no live or recent runs', async () => {
    vi.mocked(api.listSessions).mockResolvedValue({ sessions: {}, entries: [] })
    vi.mocked(api.listRuntimeEvents).mockResolvedValue({ events: [] })

    renderWithQueryClient(<SessionsPage />)

    await waitFor(() => {
      expect(screen.getByText('No live or recent runs are available.')).toBeInTheDocument()
    })
  })
})
