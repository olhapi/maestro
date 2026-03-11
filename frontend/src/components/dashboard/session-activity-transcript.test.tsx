import { fireEvent, render, screen, within } from '@testing-library/react'

import { SessionActivityTranscript } from '@/components/dashboard/session-activity-transcript'
import type { SessionDisplayHistoryEntry } from '@/lib/types'

function makeCommandEntry(overrides: Partial<SessionDisplayHistoryEntry> = {}): SessionDisplayHistoryEntry {
  return {
    id: 'session-command-call-1',
    kind: 'command',
    title: 'Command output',
    summary: 'Starting vite dev server',
    detail: '$ npm run dev\ncwd: /repo/apps/frontend\n\nStarting vite dev server',
    expandable: true,
    tone: 'default',
    event_type: 'exec_command_output_delta',
    command: 'npm run dev',
    command_state: 'output',
    ...overrides,
  }
}

describe('SessionActivityTranscript', () => {
  it('renders the transcript inside a scroll container with a fixed-width toggle', () => {
    render(
      <SessionActivityTranscript
        entries={[
          {
            id: 'session-agent-item-1',
            kind: 'agent',
            title: 'Agent update',
            summary: 'Planning the fix',
            expandable: false,
            phase: 'commentary',
            tone: 'default',
            event_type: 'item.completed',
          },
          makeCommandEntry(),
        ]}
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

  it('keeps an expanded command row open when the same row updates in place', () => {
    const { rerender } = render(
      <SessionActivityTranscript
        entries={[
          makeCommandEntry({
            summary: 'Initial summary',
            detail: '$ npm run dev\nfirst detail chunk',
          }),
        ]}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /expand/i }))
    expect(screen.getByText(/first detail chunk/i)).toBeInTheDocument()

    rerender(
      <SessionActivityTranscript
        entries={[
          makeCommandEntry({
            summary: 'Updated summary',
            detail: '$ npm run dev\nsecond detail chunk',
          }),
        ]}
      />,
    )

    expect(screen.getByRole('button', { name: /collapse/i })).toBeInTheDocument()
    expect(screen.getByText(/second detail chunk/i)).toBeInTheDocument()
    expect(screen.queryByText(/first detail chunk/i)).not.toBeInTheDocument()
  })
})
