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

  it('shows automation schedule details and triggers run-now', async () => {
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
      expect(screen.getByText('Automation')).toBeInTheDocument()
    })

    expect(screen.getByText(/next automation run/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /project automations/i })).toBeInTheDocument()

    fireEvent.click(screen.getByText('Run now'))

    await waitFor(() => {
      expect(api.runIssueNow).toHaveBeenCalledWith(summary.identifier)
      expect(onInvalidate).toHaveBeenCalled()
    })
  })

  it('renders full page, edit, retry, and delete in a single icon action row', async () => {
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
    const fullPageButton = within(actionRow).getByRole('button', {
      name: /full page/i,
    })
    const editButton = within(actionRow).getByRole('button', {
      name: /edit issue/i,
    })
    const retryButton = within(actionRow).getByRole('button', {
      name: /retry now/i,
    })
    const deleteButton = within(actionRow).getByRole('button', {
      name: /delete/i,
    })

    expect(within(actionRow).getAllByRole('button')).toHaveLength(4)
    expect(within(actionRow).getAllByRole('button').map((button) => button.textContent)).toEqual([
      'Full page',
      'Edit issue',
      'Retry now',
      'Delete',
    ])
    expect(fullPageButton.querySelector('svg')).not.toBeNull()
    expect(editButton.querySelector('svg')).not.toBeNull()
    expect(retryButton.querySelector('svg')).not.toBeNull()
    expect(deleteButton.querySelector('svg')).not.toBeNull()
  })

  it('keeps issue metadata scrollable inside the preview sheet', async () => {
    const bootstrap = makeBootstrapResponse()
    const summary = makeIssueSummary()
    vi.mocked(api.getIssue).mockResolvedValue(
      makeIssueDetail({
        agent_name: 'marketing',
        agent_prompt: 'Review the hero messaging before implementation.',
        workspace_path: '/home/olhapi/projects/mvphotography/workspaces/issues/2026/03/29/a-very-long-workspace-path-that-should-scroll',
        branch_name: 'codex/ISS-1-with-an-exceptionally-long-branch-name-that-should-scroll',
        pr_url: 'https://example.com/pr/99/with/an/exceptionally/long/path/that/should/scroll',
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
      expect(screen.getByText('Branch / PR')).toBeInTheDocument()
    })

    const workspaceCard = screen.getByText('Workspace').closest('div')
    expect(workspaceCard).toHaveClass('min-w-0')
    expect(within(workspaceCard!).getByText(
      '/home/olhapi/projects/mvphotography/workspaces/issues/2026/03/29/a-very-long-workspace-path-that-should-scroll',
    )).toHaveClass('max-w-full', 'overflow-x-auto', 'whitespace-nowrap')

    const branchCard = screen.getByText('Branch / PR').closest('div')
    expect(branchCard).toHaveClass('min-w-0')
    expect(within(branchCard!).getByText(
      'codex/ISS-1-with-an-exceptionally-long-branch-name-that-should-scroll',
    )).toHaveClass('max-w-full', 'overflow-x-auto', 'whitespace-nowrap')
    expect(within(branchCard!).getByText(
      'https://example.com/pr/99/with/an/exceptionally/long/path/that/should/scroll',
    )).toHaveClass('max-w-full', 'overflow-x-auto', 'whitespace-nowrap')

    expect(screen.queryByText('Assigned agent')).not.toBeInTheDocument()
  })

  it('keeps the workspace path on a single scrollable line', async () => {
    const bootstrap = makeBootstrapResponse()
    const summary = makeIssueSummary({
      workspace_path: '/home/olhapi/projects/mvphotography/workspaces/issues/2026/03/29/a-very-long-workspace-path',
    })
    vi.mocked(api.getIssue).mockResolvedValue(makeIssueDetail(summary))

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
      expect(screen.getByText('Workspace')).toBeInTheDocument()
    })

    const workspaceCard = screen.getByText('Workspace').closest('div')
    expect(workspaceCard).toHaveClass('min-w-0')

    const workspacePath = within(workspaceCard!).getByText(summary.workspace_path)
    expect(workspacePath).toHaveClass('max-w-full', 'overflow-x-auto', 'whitespace-nowrap')
  })

  it('renders markdown in the issue description', async () => {
    const bootstrap = makeBootstrapResponse()
    const summary = makeIssueSummary()
    vi.mocked(api.getIssue).mockResolvedValue(
      makeIssueDetail({
        description: 'Review the **retry window** before merge.\n\nSee [details](https://example.com).',
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
      expect(screen.getByText('Investigate retries')).toBeInTheDocument()
    })

    expect(screen.getByText('retry window', { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'details' })).toHaveAttribute(
      'href',
      'https://example.com',
    )
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
