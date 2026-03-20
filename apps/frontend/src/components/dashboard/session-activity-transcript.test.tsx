import { fireEvent, render, screen, within } from '@testing-library/react'

import { SessionActivityTranscript } from '@/components/dashboard/session-activity-transcript'
import type { ActivityEntry, ActivityGroup } from '@/lib/types'

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

describe('SessionActivityTranscript', () => {
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

  it('renders the status marker inline and centered with the entry title row', () => {
    render(<SessionActivityTranscript groups={makeGroups([makeCommandEntry()])} />)

    const titleRow = screen.getByText('Command output').parentElement
    expect(titleRow).toHaveClass('flex')
    expect(titleRow).toHaveClass('items-center')
    expect(titleRow?.querySelector('span')).toHaveClass('size-1.5')
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

  it('scrolls the activity log to the bottom when activity updates arrive', () => {
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
    Object.defineProperty(scrollContainer, 'scrollHeight', {
      configurable: true,
      get: () => 480,
    })
    Object.defineProperty(scrollContainer, 'scrollTop', {
      configurable: true,
      writable: true,
      value: 0,
    })

    rerender(
      <SessionActivityTranscript
        groups={makeGroups([
          makeCommandEntry({
            summary: 'Updated summary',
            detail: '$ npm run dev\nsecond detail chunk',
          }),
          {
            id: 'attempt-1-agent-2',
            kind: 'agent',
            title: 'Agent update',
            summary: 'Newer message at the bottom',
            expandable: false,
            phase: 'commentary',
            tone: 'default',
          },
        ])}
      />,
    )

    expect(scrollContainer.scrollTop).toBe(480)
  })
})
