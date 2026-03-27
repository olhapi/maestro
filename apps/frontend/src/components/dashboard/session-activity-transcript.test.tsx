import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { SessionActivityTranscript } from '@/components/dashboard/session-activity-transcript'
import type { ActivityEntry, ActivityGroup } from '@/lib/types'
import { formatDateTime } from '@/lib/utils'

function makeCommandEntry(overrides: Partial<ActivityEntry> = {}): ActivityEntry {
  return {
    id: 'attempt-1-command-1',
    kind: 'command',
    title: 'Command output',
    summary: 'npm run dev',
    detail: '$ npm run dev\ncwd: /repo/apps/frontend\n\nStarting vite dev server',
    expandable: true,
    tone: 'default',
    item_type: 'commandExecution',
    status: 'in_progress',
    ...overrides,
  }
}

function makeGroups(entries: ActivityEntry[]): ActivityGroup[] {
  return [
    {
      attempt: 1,
      phase: 'implementation',
      status: 'active',
      entries,
    },
  ]
}

const originalClipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
const originalExecCommandDescriptor = Object.getOwnPropertyDescriptor(document, 'execCommand')
const originalScrollHeightDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollHeight')
const originalClientHeightDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientHeight')
const originalScrollTopDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollTop')

type ScrollMetrics = {
  scrollHeight: number
  clientHeight: number
  scrollTop: number
}

function mockClipboard(writeText = vi.fn().mockResolvedValue(undefined)) {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText,
    },
  })

  return writeText
}

function restoreClipboard() {
  if (originalClipboardDescriptor) {
    Object.defineProperty(navigator, 'clipboard', originalClipboardDescriptor)
    return
  }

  delete (navigator as typeof navigator & { clipboard?: unknown }).clipboard
}

function mockExecCommand(result = true) {
  const execCommand = vi.fn(() => result)

  Object.defineProperty(document, 'execCommand', {
    configurable: true,
    value: execCommand,
  })

  return execCommand
}

function mockScrollMetrics(metrics: ScrollMetrics) {
  Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
    configurable: true,
    get: () => metrics.scrollHeight,
  })
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get: () => metrics.clientHeight,
  })
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get: () => metrics.scrollTop,
    set: (value: number) => {
      metrics.scrollTop = value
    },
  })

  return metrics
}

function restoreExecCommand() {
  if (originalExecCommandDescriptor) {
    Object.defineProperty(document, 'execCommand', originalExecCommandDescriptor)
    return
  }

  delete (document as typeof document & { execCommand?: unknown }).execCommand
}

function restoreScrollMetrics() {
  if (originalScrollHeightDescriptor) {
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', originalScrollHeightDescriptor)
  } else {
    delete (HTMLElement.prototype as typeof HTMLElement.prototype & { scrollHeight?: unknown }).scrollHeight
  }

  if (originalClientHeightDescriptor) {
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', originalClientHeightDescriptor)
  } else {
    delete (HTMLElement.prototype as typeof HTMLElement.prototype & { clientHeight?: unknown }).clientHeight
  }

  if (originalScrollTopDescriptor) {
    Object.defineProperty(HTMLElement.prototype, 'scrollTop', originalScrollTopDescriptor)
  } else {
    delete (HTMLElement.prototype as typeof HTMLElement.prototype & { scrollTop?: unknown }).scrollTop
  }
}

describe('SessionActivityTranscript', () => {
  afterEach(() => {
    vi.useRealTimers()
    restoreClipboard()
    restoreExecCommand()
    restoreScrollMetrics()
  })

  it('renders the transcript inside a scroll container with a fixed-width toggle', () => {
    render(
      <SessionActivityTranscript
        groups={makeGroups([
          {
            id: 'attempt-1-agent-1',
            kind: 'agent',
            title: 'Agent update',
            summary: 'Planning the fix',
            expandable: false,
            phase: 'commentary',
            tone: 'default',
          },
          makeCommandEntry(),
        ])}
      />,
    )

    const scrollContainer = screen.getByTestId('activity-log-scroll')
    expect(scrollContainer).toHaveClass('max-h-[520px]')

    const toggle = within(scrollContainer).getByRole('button', { name: /expand/i })
    expect(toggle).toHaveClass('w-20')

    fireEvent.click(toggle)

    expect(toggle).toHaveClass('w-20')
    expect(toggle).toHaveTextContent('Collapse')
  })

  it('copies the loaded transcript groups with the native clipboard API', async () => {
    vi.useFakeTimers()
    const writeText = mockClipboard()
    const groups = makeGroups([
      {
        id: 'attempt-1-agent-1',
        kind: 'agent',
        title: 'Agent update',
        summary: 'Planning the fix',
        expandable: false,
        phase: 'commentary',
        tone: 'default',
      },
      makeCommandEntry(),
    ])

    render(<SessionActivityTranscript groups={groups} />)

    const button = screen.getByRole('button', { name: /copy all/i })
    expect(button).toBeEnabled()

    fireEvent.click(button)

    expect(writeText).toHaveBeenCalledWith(JSON.stringify(groups, null, 2))

    await act(async () => {
      await Promise.resolve()
    })

    expect(screen.getByRole('button', { name: /copied/i })).toBeInTheDocument()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1400)
    })

    expect(screen.getByRole('button', { name: /copy all/i })).toBeInTheDocument()
  })

  it('falls back to execCommand when the clipboard API is unavailable', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    })
    const execCommand = mockExecCommand(true)

    render(<SessionActivityTranscript groups={makeGroups([makeCommandEntry()])} />)

    const button = screen.getByRole('button', { name: /copy all/i })
    expect(button).toBeEnabled()

    fireEvent.click(button)

    expect(execCommand).toHaveBeenCalledWith('copy')

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /copied/i })).toBeInTheDocument()
    })
  })

  it('falls back to execCommand when the clipboard API rejects', async () => {
    const writeText = mockClipboard(vi.fn().mockRejectedValue(new Error('permission denied')))
    const execCommand = mockExecCommand(true)
    const groups = makeGroups([makeCommandEntry()])

    render(<SessionActivityTranscript groups={groups} />)

    fireEvent.click(screen.getByRole('button', { name: /copy all/i }))

    expect(writeText).toHaveBeenCalledWith(JSON.stringify(groups, null, 2))

    await waitFor(() => {
      expect(execCommand).toHaveBeenCalledWith('copy')
    })

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /copied/i })).toBeInTheDocument()
    })
  })

  it('renders markdown-formatted activity summaries', () => {
    render(
      <SessionActivityTranscript
        groups={makeGroups([
          {
            id: 'attempt-1-agent-1',
            kind: 'agent',
            title: 'Agent update',
            summary: 'Review the **plan** in [docs](https://example.com)\n\n- First item\n- Second item',
            expandable: false,
            phase: 'commentary',
            tone: 'default',
          },
        ])}
      />,
    )

    expect(screen.getByText('plan', { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'docs' })).toHaveAttribute('href', 'https://example.com')
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
  })

  it('renders proposed plans in a dedicated final-answer callout', () => {
    render(
      <SessionActivityTranscript
        groups={makeGroups([
          {
            id: 'attempt-1-agent-final',
            kind: 'agent',
            title: 'Final answer',
            summary:
              '<proposed_plan>\nReview the **plan** in [docs](https://example.com)\n\n- First item\n- Second item\n</proposed_plan>',
            expandable: false,
            phase: 'final_answer',
            tone: 'success',
          },
        ])}
      />,
    )

    expect(screen.queryByText(/<proposed_plan>/i)).not.toBeInTheDocument()

    const callout = screen.getByTestId('proposed-plan-callout')
    expect(callout).toHaveClass('border-sky-400/25')
    expect(callout).toHaveClass('bg-sky-400/10')
    expect(screen.getByText('Proposed plan')).toBeInTheDocument()
    expect(screen.getByText('plan', { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'docs' })).toHaveAttribute('href', 'https://example.com')
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
  })

  it('renders inline timestamps, clamps verbose summaries, and keeps the layout contained', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-03-10T12:00:00Z'))

    const startedAt = '2026-03-10T11:30:00Z'
    const completedAt = '2026-03-10T11:58:00Z'

    render(
      <SessionActivityTranscript
        groups={makeGroups([
          makeCommandEntry({
            started_at: startedAt,
            completed_at: completedAt,
            summary:
              'This is an exceptionally long activity summary that should stay compact inside the transcript so it does not widen the center column or push nearby panels sideways.',
            detail:
              '$ npm run dev -- --filter=frontend\nfirst detail chunk with-an-exceptionally-long-token-that-should-wrap',
          }),
        ])}
      />,
    )

    const activityLog = screen.getByTestId('activity-log')
    expect(activityLog).toHaveClass('overflow-x-hidden')

    const scrollContainer = screen.getByTestId('activity-log-scroll')
    expect(scrollContainer).toHaveClass('overflow-x-hidden')

    const title = screen.getByText('Command output')
    const row = title.closest('article')
    expect(row).toHaveClass('min-w-0')
    expect(row).toHaveClass('overflow-x-hidden')

    const markerRow = title.parentElement?.parentElement?.parentElement
    expect(markerRow).toHaveClass('items-center')

    const titleRow = title.parentElement
    expect(titleRow).toHaveClass('flex-wrap')
    expect(titleRow).toHaveClass('min-w-0')

    const timestamp = within(titleRow as HTMLElement).getByText(formatDateTime(completedAt))
    expect(timestamp).toHaveAttribute('dateTime', completedAt)
    expect(timestamp).toHaveAttribute('title', formatDateTime(completedAt))
    expect(within(titleRow as HTMLElement).queryByText('2m ago')).not.toBeInTheDocument()

    const summary = screen.getByText(/exceptionally long activity summary/i)
    expect(summary.closest('div')).toHaveClass('line-clamp-3')

    fireEvent.click(screen.getByRole('button', { name: /expand/i }))

    const detail = screen.getByText((content, element) => element?.tagName === 'PRE' && content.includes('with-an-exceptionally-long-token'))
    expect(detail).toHaveClass('whitespace-pre-wrap', 'break-words')
    expect(detail).not.toHaveClass('overflow-x-auto')
  })

  it('keeps an expanded command row open when the same row updates in place', () => {
    const { rerender } = render(
      <SessionActivityTranscript
        groups={makeGroups([
          makeCommandEntry({
            summary: 'Initial summary',
            detail: '$ npm run dev\nfirst detail chunk',
          }),
        ])}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /expand/i }))
    expect(screen.getByText(/first detail chunk/i)).toBeInTheDocument()

    rerender(
      <SessionActivityTranscript
        groups={makeGroups([
          makeCommandEntry({
            summary: 'Updated summary',
            detail: '$ npm run dev\nsecond detail chunk',
          }),
        ])}
      />,
    )

    expect(screen.getByRole('button', { name: /collapse/i })).toBeInTheDocument()
    expect(screen.getByText(/second detail chunk/i)).toBeInTheDocument()
    expect(screen.queryByText(/first detail chunk/i)).not.toBeInTheDocument()
  })

  it('scrolls the activity log to the bottom on the initial render', () => {
    mockScrollMetrics({
      scrollHeight: 480,
      clientHeight: 200,
      scrollTop: 0,
    })

    render(
      <SessionActivityTranscript
        groups={makeGroups([
          makeCommandEntry({
            summary: 'Initial summary',
            detail: '$ npm run dev\nfirst detail chunk',
            started_at: '2026-03-10T11:30:00Z',
            completed_at: '2026-03-10T11:58:00Z',
          }),
        ])}
      />,
    )

    const scrollContainer = screen.getByTestId('activity-log-scroll')
    expect(scrollContainer.scrollTop).toBe(280)
  })

  it('keeps the current position when the user scrolls up and resumes once they return to the bottom', () => {
    const scrollMetrics = mockScrollMetrics({
      scrollHeight: 480,
      clientHeight: 200,
      scrollTop: 0,
    })

    const { rerender } = render(
      <SessionActivityTranscript
        groups={makeGroups([
          makeCommandEntry({
            summary: 'Initial summary',
            detail: '$ npm run dev\nfirst detail chunk',
            started_at: '2026-03-10T11:30:00Z',
            completed_at: '2026-03-10T11:58:00Z',
          }),
        ])}
      />,
    )

    const scrollContainer = screen.getByTestId('activity-log-scroll')
    expect(scrollContainer.scrollTop).toBe(280)

    scrollMetrics.scrollTop = 140
    fireEvent.scroll(scrollContainer)

    scrollMetrics.scrollHeight = 520

    rerender(
      <SessionActivityTranscript
        groups={makeGroups([
          makeCommandEntry({
            summary: 'Updated summary',
            detail: '$ npm run dev\nsecond detail chunk',
            started_at: '2026-03-10T11:30:00Z',
            completed_at: '2026-03-10T11:59:00Z',
          }),
        ])}
      />,
    )

    expect(scrollContainer.scrollTop).toBe(140)

    scrollMetrics.scrollTop = 320
    fireEvent.scroll(scrollContainer)
    scrollMetrics.scrollHeight = 560

    rerender(
      <SessionActivityTranscript
        groups={makeGroups([
          makeCommandEntry({
            summary: 'Newest summary',
            detail: '$ npm run dev\nthird detail chunk',
            started_at: '2026-03-10T11:30:00Z',
            completed_at: '2026-03-10T12:00:00Z',
          }),
        ])}
      />,
    )

    expect(scrollContainer.scrollTop).toBe(360)
  })

  it('resets the pinned state when the transcript empties', () => {
    const scrollMetrics = mockScrollMetrics({
      scrollHeight: 480,
      clientHeight: 200,
      scrollTop: 0,
    })

    const { rerender } = render(
      <SessionActivityTranscript
        groups={makeGroups([
          makeCommandEntry({
            summary: 'Initial summary',
            detail: '$ npm run dev\nfirst detail chunk',
          }),
        ])}
      />,
    )

    const scrollContainer = screen.getByTestId('activity-log-scroll')
    expect(scrollContainer.scrollTop).toBe(280)

    scrollMetrics.scrollTop = 140
    fireEvent.scroll(scrollContainer)

    rerender(<SessionActivityTranscript groups={[]} />)

    scrollMetrics.scrollHeight = 520
    scrollMetrics.scrollTop = 0

    rerender(
      <SessionActivityTranscript
        groups={makeGroups([
          makeCommandEntry({
            summary: 'Fresh summary',
            detail: '$ npm run dev\nfresh detail chunk',
          }),
        ])}
      />,
    )

    expect(screen.getByTestId('activity-log-scroll').scrollTop).toBe(320)
  })
})
