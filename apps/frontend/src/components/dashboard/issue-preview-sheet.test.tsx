import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
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
    error: vi.fn(),
  },
}))

vi.mock('@/lib/api', () => ({
  api: {
    getIssue: vi.fn(),
    listIssues: vi.fn(),
    retryIssue: vi.fn(),
    runIssueNow: vi.fn(),
    setIssueBlockers: vi.fn(),
    updateIssue: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((nextResolve) => {
    resolve = nextResolve
  })

  return { promise, resolve }
}

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

    expect(screen.getByText('turn.started')).toBeInTheDocument()
    expect(screen.queryByText('No live session')).not.toBeInTheDocument()

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

    expect(screen.getByText(/auto-retries paused after 3 stalled runs/i)).toBeInTheDocument()
  })

  it('shows recurring schedule details and triggers run-now', async () => {
    const bootstrap = makeBootstrapResponse()
    const summary = makeIssueSummary({ issue_type: 'recurring', next_run_at: '2026-03-09T12:30:00Z' })
    vi.mocked(api.getIssue).mockResolvedValue(
      makeIssueDetail({
        issue_type: 'recurring',
        cron: '*/15 * * * *',
        enabled: true,
        next_run_at: '2026-03-09T12:30:00Z',
      }),
    )
    vi.mocked(api.runIssueNow).mockResolvedValue({ status: 'queued_now' })

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
      expect(screen.getByText('Recurring')).toBeInTheDocument()
    })

    expect(screen.getByText(/next scheduled run/i)).toBeInTheDocument()

    fireEvent.click(screen.getByText('Run now'))

    await waitFor(() => {
      expect(api.runIssueNow).toHaveBeenCalledWith(summary.identifier)
      expect(onInvalidate).toHaveBeenCalled()
    })
  })

  it('renders edit, retry, and delete in a single icon action row', async () => {
    const bootstrap = makeBootstrapResponse()
    const summary = makeIssueSummary()
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())

    renderWithQueryClient(
      <IssuePreviewSheet
        issue={summary}
        bootstrap={bootstrap}
        open
        onOpenChange={vi.fn()}
        onInvalidate={vi.fn().mockResolvedValue(undefined)}
        onDelete={vi.fn().mockResolvedValue(undefined)}
      />,
    )

    await waitFor(() => {
      expect(screen.getByText('Investigate retries')).toBeInTheDocument()
    })

    const actionRow = screen.getByTestId('issue-preview-actions-row')
    const editButton = within(actionRow).getByRole('button', {
      name: /edit issue/i,
    })
    const retryButton = within(actionRow).getByRole('button', {
      name: /retry now/i,
    })
    const deleteButton = within(actionRow).getByRole('button', {
      name: /delete/i,
    })

    expect(within(actionRow).getAllByRole('button')).toHaveLength(3)
    expect(editButton.querySelector('svg')).not.toBeNull()
    expect(retryButton.querySelector('svg')).not.toBeNull()
    expect(deleteButton.querySelector('svg')).not.toBeNull()
  })

  it('shows assigned agent metadata when issue details load', async () => {
    const bootstrap = makeBootstrapResponse()
    const summary = makeIssueSummary()
    vi.mocked(api.getIssue).mockResolvedValue(
      makeIssueDetail({
        agent_name: 'marketing',
        agent_prompt: 'Review the hero messaging before implementation.',
      }),
    )

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
      expect(screen.getByText('Assigned agent')).toBeInTheDocument()
    })

    expect(screen.getByText('marketing')).toBeInTheDocument()
    expect(screen.getByText('Review the hero messaging before implementation.')).toBeInTheDocument()
  })

  it('confirms deletion before calling the preview delete handler', async () => {
    const bootstrap = makeBootstrapResponse()
    const summary = makeIssueSummary()
    const onDelete = vi.fn().mockResolvedValue(undefined)
    const onOpenChange = vi.fn()
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())

    renderWithQueryClient(
      <IssuePreviewSheet
        issue={summary}
        bootstrap={bootstrap}
        open
        onOpenChange={onOpenChange}
        onInvalidate={vi.fn().mockResolvedValue(undefined)}
        onDelete={onDelete}
      />,
    )

    await waitFor(() => {
      expect(screen.getByText('Investigate retries')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /^delete$/i }))
    expect(onDelete).not.toHaveBeenCalled()

    const confirmDialog = await screen.findByRole('dialog', {
      name: /delete iss-1\?/i,
    })
    fireEvent.click(
      within(confirmDialog).getByRole('button', { name: /delete issue/i }),
    )

    await waitFor(() => {
      expect(onDelete).toHaveBeenCalledWith(summary.identifier)
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('keeps actions scoped to the currently selected issue while detail loads', async () => {
    const bootstrap = makeBootstrapResponse()
    const firstSummary = makeIssueSummary({
      id: 'issue-1',
      identifier: 'ISS-1',
      title: 'First issue',
    })
    const secondSummary = makeIssueSummary({
      id: 'issue-2',
      identifier: 'ISS-2',
      title: 'Second issue',
    })
    const firstLoad = deferred<ReturnType<typeof makeIssueDetail>>()
    const secondLoad = deferred<ReturnType<typeof makeIssueDetail>>()
    vi.mocked(api.getIssue)
      .mockReturnValueOnce(firstLoad.promise)
      .mockReturnValueOnce(secondLoad.promise)
    vi.mocked(api.retryIssue).mockResolvedValue({ status: 'queued_now' })

    const onInvalidate = vi.fn().mockResolvedValue(undefined)
    const { rerender } = renderWithQueryClient(
      <IssuePreviewSheet
        issue={firstSummary}
        bootstrap={bootstrap}
        open
        onOpenChange={vi.fn()}
        onInvalidate={onInvalidate}
      />,
    )

    rerender(
      <IssuePreviewSheet
        issue={secondSummary}
        bootstrap={bootstrap}
        open
        onOpenChange={vi.fn()}
        onInvalidate={onInvalidate}
      />,
    )

    expect(screen.getByText('Second issue')).toBeInTheDocument()

    await act(async () => {
      firstLoad.resolve(
        makeIssueDetail({
          id: firstSummary.id,
          identifier: firstSummary.identifier,
          title: 'First issue detail',
        }),
      )
      await firstLoad.promise
    })

    expect(screen.queryByText('First issue detail')).not.toBeInTheDocument()
    expect(screen.getByText('Second issue')).toBeInTheDocument()

    fireEvent.click(screen.getByText('Retry now'))

    await waitFor(() => {
      expect(api.retryIssue).toHaveBeenCalledWith(secondSummary.identifier)
    })

    await act(async () => {
      secondLoad.resolve(
        makeIssueDetail({
          id: secondSummary.id,
          identifier: secondSummary.identifier,
          title: 'Second issue detail',
        }),
      )
      await secondLoad.promise
    })

    await waitFor(() => {
      expect(screen.getByText('Second issue detail')).toBeInTheDocument()
    })
  })

  it('shows an error toast when saving invalid blockers fails', async () => {
    const bootstrap = makeBootstrapResponse()
    const summary = makeIssueSummary()
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail())
    vi.mocked(api.setIssueBlockers).mockRejectedValue(new Error('unknown blocker'))

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
      expect(screen.getByRole('button', { name: /save blockers/i })).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /save blockers/i }))

    const { toast } = await import('sonner')
    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Unable to update blockers: unknown blocker')
    })
  })
})
