import { forwardRef, type ComponentPropsWithoutRef } from 'react'
import { act, fireEvent, screen } from '@testing-library/react'
import { vi } from 'vitest'

import { IssueCard } from '@/components/dashboard/issue-card'
import { makeBootstrapResponse, makeIssueSummary } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@tanstack/react-router', () => ({
  Link: forwardRef<
    HTMLAnchorElement,
    ComponentPropsWithoutRef<'a'> & {
      params?: { identifier?: string }
    }
  >(({ children, className, params, ...props }, ref) => (
    <a
      ref={ref}
      className={className}
      href={params?.identifier ? `/issues/${params.identifier}` : '#'}
      {...props}
    >
      {children}
    </a>
  )),
}))

describe('IssueCard', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('renders a live badge when bootstrap sessions are keyed by issue identifier', () => {
    const issue = makeIssueSummary()

    renderWithQueryClient(<IssueCard issue={issue} bootstrap={makeBootstrapResponse()} onOpen={vi.fn()} />)

    expect(screen.getByText('Live')).toBeInTheDocument()
  })

  it('renders a paused badge when retries are paused for an issue', () => {
    const issue = makeIssueSummary()
    const onOpen = vi.fn()
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
    renderWithQueryClient(<IssueCard issue={issue} bootstrap={bootstrap} onOpen={onOpen} />)

    expect(screen.getByText('Paused')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /investigate retries/i }))
    expect(onOpen).toHaveBeenCalledWith(issue)
  })

  it('keeps key metadata visible in compact mode', () => {
    const issue = makeIssueSummary({
      branch_name: 'feature/retries',
      total_tokens_spent: 144,
      workspace_run_count: 3,
    })

    renderWithQueryClient(<IssueCard issue={issue} compact onOpen={vi.fn()} />)

    expect(screen.getByText('feature/retries')).toBeInTheDocument()
    expect(screen.getByText('3 runs')).toBeInTheDocument()
    expect(screen.getByText('144 tokens')).toBeInTheDocument()
  })

  it('renders issue descriptions as markdown without nesting links inside the button', () => {
    renderWithQueryClient(
      <IssueCard
        issue={makeIssueSummary({
          description: 'Review the **retry window** and [docs](https://example.com).',
        })}
        onOpen={vi.fn()}
      />,
    )

    expect(screen.getByText('retry window', { selector: 'strong' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'docs' })).not.toBeInTheDocument()
  })

  it('allows draggable surfaces to override the card cursor affordance', () => {
    renderWithQueryClient(<IssueCard issue={makeIssueSummary()} className="cursor-grab active:cursor-grabbing" onOpen={vi.fn()} />)

    expect(screen.getByRole('button', { name: /investigate retries/i })).toHaveClass('cursor-grab', 'active:cursor-grabbing')
  })

  it('opens the quick preview from the primary card action', () => {
    const issue = makeIssueSummary()
    const onOpen = vi.fn()
    renderWithQueryClient(<IssueCard issue={issue} onOpen={onOpen} />)

    fireEvent.click(screen.getByRole('button', { name: /investigate retries/i }))
    expect(onOpen).toHaveBeenCalledWith(issue)
  })

  it('renders automation metadata when the issue is scheduled', () => {
    renderWithQueryClient(
      <IssueCard
        issue={makeIssueSummary({
          issue_type: 'recurring',
          next_run_at: '2026-03-09T12:30:00Z',
        })}
        onOpen={vi.fn()}
      />,
    )

    expect(screen.getByText('Automation')).toBeInTheDocument()
    expect(screen.getByText(/next/i)).toBeInTheDocument()
  })

  it('shows the hover card preview when the issue is hovered', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-03-09T12:00:00Z'))

    const issue = makeIssueSummary({
      issue_type: 'recurring',
      next_run_at: '2026-03-09T12:30:00Z',
      labels: ['api', 'automation'],
      is_blocked: true,
      blocked_by: ['ISS-9'],
      workspace_last_run: '2026-03-09T11:45:00Z',
      pr_url: 'https://example.com/pr/7',
    })

    renderWithQueryClient(<IssueCard issue={issue} bootstrap={makeBootstrapResponse()} onOpen={vi.fn()} />)

    const trigger = screen.getByRole('button', { name: /investigate retries/i })
    await act(async () => {
      fireEvent.pointerEnter(trigger, { pointerType: 'mouse' })
      fireEvent.mouseEnter(trigger)
      await vi.advanceTimersByTimeAsync(150)
    })

    expect(screen.getByText('Retry scheduled in 5 minutes')).toBeInTheDocument()
    expect(screen.getByText('Reason: Approval Required')).toBeInTheDocument()
    expect(screen.getByText('Blocked by ISS-9')).toBeInTheDocument()
    expect(screen.getByText('Live session · Turn Started')).toBeInTheDocument()
    expect(screen.getByText('/tmp/workspaces/ISS-1')).toBeInTheDocument()
    expect(screen.getByText('Last run 15 minutes ago')).toBeInTheDocument()
    expect(screen.getByText('api')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open linked PR' })).toHaveAttribute('href', 'https://example.com/pr/7')
  })
})
