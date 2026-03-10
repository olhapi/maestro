import { fireEvent, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { SessionsPage } from '@/routes/sessions'
import { renderWithQueryClient } from '@/test/test-utils'

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
      'RUN-LIVE': {
        issue_id: 'issue-live',
        issue_identifier: 'RUN-LIVE',
        session_id: 'thread-live-turn-live',
        thread_id: 'thread-live',
        turn_id: 'turn-live',
        last_event: 'turn.started',
        last_timestamp: '2026-03-10T11:59:30Z',
        last_message: 'Applying migration',
        input_tokens: 10,
        output_tokens: 20,
        total_tokens: 30,
        events_processed: 6,
        turns_started: 4,
        turns_completed: 3,
        terminal: false,
        history: [{ type: 'turn.started', thread_id: 'thread-live', turn_id: 'turn-live', input_tokens: 0, output_tokens: 0, total_tokens: 30, message: 'Applying migration' }],
      },
    },
    entries: [
      {
        issue_id: 'issue-live',
        issue_identifier: 'RUN-LIVE',
        source: 'live',
        active: true,
        status: 'active',
        phase: 'implementation',
        attempt: 2,
        run_kind: 'run_started',
        failure_class: '',
        updated_at: '2026-03-10T11:59:30Z',
        last_event: 'turn.started',
        last_message: 'Applying migration',
        total_tokens: 30,
        events_processed: 6,
        turns_started: 4,
        turns_completed: 3,
        terminal: false,
        terminal_reason: '',
        history: [{ type: 'turn.started', thread_id: 'thread-live', turn_id: 'turn-live', input_tokens: 0, output_tokens: 0, total_tokens: 30, message: 'Applying migration' }],
        error: '',
      },
      {
        issue_id: 'issue-paused',
        issue_identifier: 'RUN-PAUSED',
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
        history: [{ type: 'run.failed', thread_id: 'thread-paused', turn_id: 'turn-paused', input_tokens: 0, output_tokens: 0, total_tokens: 48, message: 'Paused after repeated failures' }],
        error: 'stall_timeout',
      },
    ],
  }
}

describe('SessionsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders mixed live and recent persisted entries in the provided order', async () => {
    vi.mocked(api.listSessions).mockResolvedValue(makeSessionsResponse())
    vi.mocked(api.listRuntimeEvents).mockResolvedValue({ events: [] })

    renderWithQueryClient(<SessionsPage />)

    await waitFor(() => {
      expect(screen.getByText('Run transparency')).toBeInTheDocument()
    })

    const identifiers = screen.getAllByText(/RUN-/).map((node) => node.textContent)
    expect(identifiers).toEqual(['RUN-LIVE', 'RUN-PAUSED'])
  })

  it('keeps cards compact by default and reveals session history on expand', async () => {
    vi.mocked(api.listSessions).mockResolvedValue(makeSessionsResponse())
    vi.mocked(api.listRuntimeEvents).mockResolvedValue({ events: [] })

    renderWithQueryClient(<SessionsPage />)

    await waitFor(() => {
      expect(screen.getAllByText('Show details').length).toBeGreaterThan(0)
    })

    expect(screen.queryByText('Recent session history')).not.toBeInTheDocument()

    fireEvent.click(screen.getAllByText('Show details')[0])

    expect(screen.getByText('Recent session history')).toBeInTheDocument()
    expect(screen.getByText('turn.started')).toBeInTheDocument()
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
