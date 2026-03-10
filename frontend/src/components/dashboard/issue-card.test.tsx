import { fireEvent, screen } from '@testing-library/react'
import { vi } from 'vitest'

import { IssueCard } from '@/components/dashboard/issue-card'
import { makeBootstrapResponse, makeIssueSummary } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

describe('IssueCard', () => {
  it('renders a paused badge when retries are paused for an issue', () => {
    const issue = makeIssueSummary()
    const bootstrap = makeBootstrapResponse({
      overview: {
        ...makeBootstrapResponse().overview,
        snapshot: {
          ...makeBootstrapResponse().overview.snapshot,
          paused: [
            {
              issue_id: issue.id,
              identifier: issue.identifier,
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
    const onOpen = vi.fn()

    renderWithQueryClient(<IssueCard issue={issue} bootstrap={bootstrap} onOpen={onOpen} />)

    expect(screen.getByText('Paused')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button'))
    expect(onOpen).toHaveBeenCalledWith(issue)
  })
})
