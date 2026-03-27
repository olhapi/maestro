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
              entries: [
                makeDebugEntry({
                  detail:
                    '$ npm run dev -- --filter=frontend\nbackground output with-an-exceptionally-long-token-that-should-wrap',
                }),
              ],
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

    const debugDetail = screen.getByText((content, element) => element?.tagName === 'PRE' && content.includes('with-an-exceptionally-long-token'))
    expect(debugDetail).toHaveClass('whitespace-pre-wrap', 'break-words')
    expect(debugDetail).not.toHaveClass('overflow-x-auto')
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
    expect(screen.queryByPlaceholderText(/explain what should change in the plan/i)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /approve plan/i }))

    expect(onApprovePlan).toHaveBeenCalledWith(undefined)
  })

  it('forwards revision notes and approval notes from the plan approval card', () => {
    const onApprovePlan = vi.fn()
    const onRequestPlanRevision = vi.fn()

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
        onRequestPlanRevision={onRequestPlanRevision}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /add note/i }))
    fireEvent.change(screen.getByPlaceholderText(/explain what should change in the plan/i), {
      target: { value: 'Tighten the rollout steps.' },
    })

    fireEvent.click(screen.getByRole('button', { name: /request changes/i }))

    expect(onRequestPlanRevision).toHaveBeenCalledWith('Tighten the rollout steps.')

    fireEvent.click(screen.getByRole('button', { name: /approve plan/i }))

    expect(onApprovePlan).toHaveBeenCalledWith('Tighten the rollout steps.')
  })

  it('can collapse and restore a drafted plan note without clearing it', () => {
    render(
      <SessionExecutionCard
        execution={makeExecutionDetail({
          plan_approval: {
            markdown: 'Review the **plan** before execution.',
            requested_at: '2026-03-18T12:00:00Z',
            attempt: 2,
          },
        })}
        issueTotalTokens={120}
        onApprovePlan={() => {}}
        onRequestPlanRevision={() => {}}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /add note/i }))

    const noteInput = screen.getByPlaceholderText(/explain what should change in the plan/i)
    fireEvent.change(noteInput, {
      target: { value: 'Keep the rollout small.' },
    })

    fireEvent.click(screen.getByRole('button', { name: /hide note/i }))

    expect(screen.queryByPlaceholderText(/explain what should change in the plan/i)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /edit note/i }))

    expect(screen.getByPlaceholderText(/explain what should change in the plan/i)).toHaveValue(
      'Keep the rollout small.',
    )
  })

  it('disables approval when the approve callback is not provided', () => {
    render(
      <SessionExecutionCard
        execution={makeExecutionDetail({
          plan_approval: {
            markdown: 'Review the **plan** before execution.',
            requested_at: '2026-03-18T12:00:00Z',
            attempt: 2,
          },
        })}
        issueTotalTokens={120}
        onRequestPlanRevision={() => {}}
      />,
    )

    expect(screen.getByRole('button', { name: /approve plan/i })).toBeDisabled()
  })

  it('reveals the note composer when request changes is chosen without a note', () => {
    render(
      <SessionExecutionCard
        execution={makeExecutionDetail({
          plan_approval: {
            markdown: 'Review the **plan** before execution.',
            requested_at: '2026-03-18T12:00:00Z',
            attempt: 2,
          },
        })}
        issueTotalTokens={120}
        onRequestPlanRevision={() => {}}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /request changes/i }))

    expect(screen.getByPlaceholderText(/explain what should change in the plan/i)).toHaveFocus()
  })

  it('clears stale revision notes when a new plan approval arrives', () => {
    const onApprovePlan = vi.fn()
    const { rerender } = render(
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

    fireEvent.click(screen.getByRole('button', { name: /add note/i }))
    fireEvent.change(screen.getByPlaceholderText(/explain what should change in the plan/i), {
      target: { value: 'Reduce the rollout size and add a rollback check.' },
    })
    expect(screen.getByPlaceholderText(/explain what should change in the plan/i)).toHaveValue(
      'Reduce the rollout size and add a rollback check.',
    )

    rerender(
      <SessionExecutionCard
        execution={makeExecutionDetail({
          plan_approval: {
            markdown: 'Review the **updated** plan before execution.\n\nSee [details](https://example.com).',
            requested_at: '2026-03-18T13:00:00Z',
            attempt: 3,
          },
        })}
        issueTotalTokens={120}
        onApprovePlan={onApprovePlan}
      />,
    )

    expect(screen.queryByPlaceholderText(/explain what should change in the plan/i)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /add note/i }))
    expect(screen.getByPlaceholderText(/explain what should change in the plan/i)).toHaveValue('')

    fireEvent.click(screen.getByRole('button', { name: /approve plan/i }))

    expect(onApprovePlan).toHaveBeenCalledWith(undefined)
  })

  it('renders a workspace recovery banner for bootstrap blockers', () => {
    render(
      <SessionExecutionCard
        execution={makeExecutionDetail({
          failure_class: 'workspace_bootstrap',
          current_error: 'workspace recovery required: Git blocked the branch switch while rebasing',
          workspace_recovery: {
            status: 'recovering',
            message: 'Workspace recovery note:\n\n- Maestro found an active Git rebase in this workspace.',
          },
        })}
        issueTotalTokens={120}
      />,
    )

    expect(screen.getByText('Bootstrap blocked')).toBeInTheDocument()
    expect(screen.getByText('Workspace recovery in progress')).toBeInTheDocument()
    expect(screen.getByText(/Workspace recovery note:/)).toBeInTheDocument()
    expect(screen.getByText(/Retry once the workspace is clean/i)).toBeInTheDocument()
  })

  it('renders issue-level maestro alerts as execution blockers', () => {
    render(
      <SessionExecutionCard
        execution={makeExecutionDetail({
          pending_interrupt: {
            id: 'alert-1',
            kind: 'alert',
            issue_identifier: 'ISS-1',
            issue_title: 'Blocked issue',
            project_id: 'project-1',
            project_name: 'Platform',
            requested_at: '2026-03-16T12:00:00Z',
            alert: {
              code: 'project_dispatch_blocked',
              severity: 'error',
              title: 'Project dispatch blocked',
              message: 'Project repo is outside the current server scope (/repo/current)',
              detail: 'Blocked issue is waiting for execution until the project scope mismatch is fixed.',
            },
          },
        })}
        issueTotalTokens={120}
      />,
    )

    expect(screen.getAllByText('Blocked').length).toBeGreaterThan(0)
    expect(screen.getByText('Project dispatch blocked')).toBeInTheDocument()
    expect(screen.getByText('Project repo is outside the current server scope (/repo/current)')).toBeInTheDocument()
    expect(screen.getByText('Blocked issue is waiting for execution until the project scope mismatch is fixed.')).toBeInTheDocument()
  })
})
