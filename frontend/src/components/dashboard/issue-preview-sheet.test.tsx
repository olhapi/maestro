import { fireEvent, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { IssuePreviewSheet } from '@/components/dashboard/issue-preview-sheet'
import { makeBootstrapResponse, makeIssueDetail, makeIssueSummary } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

const navigate = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
}))

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
  },
}))

vi.mock('@/lib/api', () => ({
  api: {
    getIssue: vi.fn(),
    retryIssue: vi.fn(),
    setIssueBlockers: vi.fn(),
    updateIssue: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

describe('IssuePreviewSheet', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('loads detail and triggers retry action', async () => {
    const bootstrap = makeBootstrapResponse()
    const summary = makeIssueSummary()
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())
    vi.mocked(api.retryIssue).mockResolvedValue({ status: 'queued_now' })

    const onInvalidate = vi.fn().mockResolvedValue(undefined)

    renderWithQueryClient(
      <IssuePreviewSheet
        issue={summary}
        bootstrap={bootstrap}
        open
        onOpenChange={vi.fn()}
        onInvalidate={onInvalidate}
      />,
    )

    await waitFor(() => {
      expect(screen.getByText('Investigate retries')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByText('Retry now'))

    await waitFor(() => {
      expect(api.retryIssue).toHaveBeenCalledWith(summary.identifier)
      expect(onInvalidate).toHaveBeenCalled()
    })
  })

  it('shows paused execution status from bootstrap data', async () => {
    const summary = makeIssueSummary()
    const bootstrap = makeBootstrapResponse({
      overview: {
        ...makeBootstrapResponse().overview,
        snapshot: {
          ...makeBootstrapResponse().overview.snapshot,
          paused: [
            {
              issue_id: summary.id,
              identifier: summary.identifier,
              phase: 'implementation',
              attempt: 3,
              paused_at: '2026-03-09T12:05:00Z',
              error: 'stall_timeout',
              consecutive_failures: 3,
              pause_threshold: 3,
            },
          ],
        },
      },
    })
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())

    renderWithQueryClient(
      <IssuePreviewSheet
        issue={summary}
        bootstrap={bootstrap}
        open
        onOpenChange={vi.fn()}
        onInvalidate={vi.fn().mockResolvedValue(undefined)}
      />,
    )

    await waitFor(() => {
      expect(screen.getAllByText('Paused').length).toBeGreaterThan(0)
    })

    expect(screen.getByText(/auto-retries paused after 3 interrupted runs/i)).toBeInTheDocument()
  })
})
