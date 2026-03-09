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
})
