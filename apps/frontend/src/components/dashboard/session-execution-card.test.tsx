import { fireEvent, render, screen } from '@testing-library/react'
import { vi } from 'vitest'

import { SessionExecutionCard } from '@/components/dashboard/session-execution-card'
import type { ActivityEntry, IssueExecutionDetail, RuntimeEvent } from '@/lib/types'

function makeDebugEntry(overrides: Partial<ActivityEntry> = {}): ActivityEntry {
  return {
    id: 'debug-entry-1',
    kind: 'secondary',
    title: 'Secondary signal',
    summary: 'Captured background execution output',
    expandable: false,
    item_type: 'secondaryItem',
    ...overrides,
  }
}

function makeRuntimeEvent(overrides: Partial<RuntimeEvent> = {}): RuntimeEvent {
  return {
    seq: 1,
    kind: 'run_started',
    phase: 'implementation',
    attempt: 2,
    ts: '2026-03-10T12:00:01Z',
    payload: {},
    ...overrides,
  }
}

function makeExecutionDetail(overrides: Partial<IssueExecutionDetail> = {}): IssueExecutionDetail {
  return {
    issue_id: 'issue-1',
    identifier: 'ISS-1',
    active: false,
    phase: 'implementation',
    attempt_number: 2,
    retry_state: 'none',
    session_source: 'persisted',
    session: {
      session_id: 'session-1',
      thread_id: 'thread-1',
      turn_id: 'turn-1',
      last_event: 'turn.completed',
      last_timestamp: '2026-03-10T12:00:00Z',
      input_tokens: 12,
      output_tokens: 18,
      total_tokens: 30,
      events_processed: 2,
      turns_started: 2,
      turns_completed: 2,
      terminal: false,
    },
    runtime_events: [],
    activity_groups: [],
    debug_activity_groups: [],
    agent_commands: [],
    ...overrides,
  }
}

describe('SessionExecutionCard', () => {
  it('caps the debug signals panel height and makes it scrollable', () => {
    render(
      <SessionExecutionCard
        execution={makeExecutionDetail({
          debug_activity_groups: [
            {
              attempt: 2,
              phase: 'implementation',
              status: 'active',
              entries: [makeDebugEntry()],
            },
          ],
          runtime_events: [makeRuntimeEvent()],
        })}
        issueTotalTokens={120}
      />,
    )

    const scrollContainer = screen.getByTestId('debug-signals-scroll')
    expect(scrollContainer).toHaveClass('max-h-[520px]')
    expect(scrollContainer).toHaveClass('overflow-y-auto')
  })

  it('renders the pending plan approval card and triggers approval', () => {
    const onApprovePlan = vi.fn()

    render(
      <SessionExecutionCard
        execution={makeExecutionDetail({
          plan_approval: {
            markdown: 'Review the **plan** before execution.\n\nSee [details](https://example.com).',
            requested_at: '2026-03-18T12:00:00Z',
            attempt: 2,
          },
        })}
        issueTotalTokens={120}
        onApprovePlan={onApprovePlan}
      />,
    )

    expect(screen.getByText('Plan ready for approval')).toBeInTheDocument()
    expect(screen.getByText('plan', { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'details' })).toHaveAttribute('href', 'https://example.com')

    fireEvent.click(screen.getByRole('button', { name: /approve plan and continue/i }))

    expect(onApprovePlan).toHaveBeenCalledTimes(1)
  })
})
