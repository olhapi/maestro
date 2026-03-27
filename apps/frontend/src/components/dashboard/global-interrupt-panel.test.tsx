import { fireEvent, render, screen } from '@testing-library/react'
import { vi } from 'vitest'

import { GlobalInterruptPanel } from '@/components/dashboard/global-interrupt-panel'

function makeApprovalInterrupt() {
  return {
    id: 'interrupt-approval',
    kind: 'approval' as const,
    issue_identifier: 'ISS-1',
    issue_title: 'Review migrations',
    phase: 'review',
    attempt: 1,
    requested_at: '2026-03-16T10:00:00Z',
    approval: {
      command: 'ssh-add --apple-use-keychain ~/.ssh/id_rsa ~/.ssh/squirro.key',
      cwd: '/Users/olhapi-work',
      reason: 'Add SSH keys to macOS keychain agent',
      decisions: [
        {
          value: 'approved_once',
          label: 'Approve once',
          description: 'Run the tool and continue.',
        },
        {
          value: 'approved_for_session',
          label: 'Approve for session',
          description: 'Allow similar requests for the rest of the session.',
        },
        {
          value: 'denied',
          label: 'Deny',
          description: 'Reject the request and let the agent continue the turn.',
        },
      ],
    },
  }
}

function makePlanApprovalInterrupt(overrides: { requestedAt?: string; markdown?: string } = {}) {
  const approval = makeApprovalInterrupt()
  return {
    ...approval,
    id: 'interrupt-plan-approval',
    requested_at: overrides.requestedAt ?? approval.requested_at,
    approval: {
      ...approval.approval,
      markdown:
        overrides.markdown ??
        'Review the proposed plan before execution.\n\n- Tighten the rollout\n- Add a rollback check',
    },
  }
}

describe('GlobalInterruptPanel', () => {
  it('keeps the approval queue and detail panes top-aligned instead of stretching the selected card', () => {
    const { container } = render(
      <GlobalInterruptPanel
        items={[makeApprovalInterrupt()]}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onRespond={() => {}}
      />,
    )

    const layout = container.querySelector('[data-testid="global-interrupt-panel"] > div > div > div.mt-4.grid')
    if (!layout) {
      throw new Error('approval layout not found')
    }
    expect(layout).toHaveClass('items-start')

    const [queuePane, detailPane] = Array.from(layout.children)
    if (!queuePane || !detailPane) {
      throw new Error('approval layout panes not found')
    }
    expect(queuePane).toHaveClass('min-w-0', 'items-start')
    expect(detailPane).toHaveClass('min-w-0', 'self-start')
  })

  it('renders the richer approval structure and auto-submits plain approval decisions', () => {
    const onRespond = vi.fn()

    render(
      <GlobalInterruptPanel
        items={[makeApprovalInterrupt()]}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onRespond={onRespond}
      />,
    )

    expect(screen.getByText('Allow the agent to run this command?')).toBeInTheDocument()
    expect(screen.getByText('Requested command')).toBeInTheDocument()
    expect(screen.getByText('Add SSH keys to macOS keychain agent')).toBeInTheDocument()
    expect(screen.getByText('Working directory')).toBeInTheDocument()
    expect(screen.getByText('/Users/olhapi-work')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /submit response/i })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /approve once/i }))

    expect(onRespond).toHaveBeenCalledWith({ interruptId: 'interrupt-approval', decision: 'approved_once' })
  })

  it('renders a steering note field for approvals and forwards note-only responses', () => {
    const onRespond = vi.fn()

    render(
      <GlobalInterruptPanel
        items={[makeApprovalInterrupt()]}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onRespond={onRespond}
      />,
    )

    expect(screen.getByText('Agent note')).toBeInTheDocument()

    fireEvent.change(screen.getByPlaceholderText(/add steering notes for the next turn/i), {
      target: { value: 'Steer the agent toward a smaller rollout.' },
    })

    fireEvent.click(screen.getByRole('button', { name: /send note/i }))

    expect(onRespond).toHaveBeenCalledWith({
      interruptId: 'interrupt-approval',
      note: 'Steer the agent toward a smaller rollout.',
    })
  })

  it('renders plan approvals with inline markdown and forwards revision notes', () => {
    const onRespond = vi.fn()

    render(
      <GlobalInterruptPanel
        items={[makePlanApprovalInterrupt()]}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onRespond={onRespond}
      />,
    )

    expect(screen.getAllByText('Plan approval').length).toBeGreaterThan(1)
    expect(screen.getByText('Proposed plan')).toBeInTheDocument()
    expect(screen.getByText(/tighten the rollout/i)).toBeInTheDocument()
    expect(screen.getByText(/add a rollback check/i)).toBeInTheDocument()

    fireEvent.change(screen.getByPlaceholderText(/explain what should change in the plan/i), {
      target: { value: 'Make the rollout smaller and add a rollback guard.' },
    })

    fireEvent.click(screen.getByRole('button', { name: /request revision/i }))

    expect(onRespond).toHaveBeenCalledWith({
      interruptId: 'interrupt-plan-approval',
      note: 'Make the rollout smaller and add a rollback guard.',
    })

    fireEvent.click(screen.getByRole('button', { name: /approve once/i }))

    expect(onRespond).toHaveBeenCalledWith({
      interruptId: 'interrupt-plan-approval',
      decision: 'approved_once',
      note: 'Make the rollout smaller and add a rollback guard.',
    })
  })

  it('clears stale plan approval notes when the approval request changes', () => {
    const onRespond = vi.fn()
    const { rerender } = render(
      <GlobalInterruptPanel
        items={[makePlanApprovalInterrupt()]}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onRespond={onRespond}
      />,
    )

    fireEvent.change(screen.getByPlaceholderText(/explain what should change in the plan/i), {
      target: { value: 'Reduce the rollout size and add a rollback check.' },
    })
    expect(screen.getByPlaceholderText(/explain what should change in the plan/i)).toHaveValue(
      'Reduce the rollout size and add a rollback check.',
    )

    rerender(
      <GlobalInterruptPanel
        items={[makePlanApprovalInterrupt({
          requestedAt: '2026-03-16T10:15:00Z',
          markdown: 'Review the updated plan before execution.\n\n- Tighten the rollout further',
        })]}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onRespond={onRespond}
      />,
    )

    expect(screen.getByPlaceholderText(/explain what should change in the plan/i)).toHaveValue('')

    fireEvent.click(screen.getByRole('button', { name: /approve once/i }))

    expect(onRespond).toHaveBeenCalledWith({
      interruptId: 'interrupt-plan-approval',
      decision: 'approved_once',
    })
  })

  it('uses decision payload when approval options provide structured responses', () => {
    const onRespond = vi.fn()

    render(
      <GlobalInterruptPanel
        items={[{
          id: 'interrupt-approval-structured',
          kind: 'approval',
          issue_identifier: 'ISS-4',
          issue_title: 'Review network access',
          requested_at: '2026-03-16T10:00:00Z',
          approval: {
            command: 'curl https://api.github.com',
            decisions: [
              {
                value: 'network_policy_allow_api_github_com',
                label: 'Persist allow rule',
                description: 'Allow this host for future requests and keep the turn going.',
                decision_payload: {
                  applyNetworkPolicyAmendment: {
                    network_policy_amendment: {
                      action: 'allow',
                      host: 'api.github.com',
                    },
                  },
                },
              },
            ],
          },
        }]}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onRespond={onRespond}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /persist allow rule/i }))

    expect(onRespond).toHaveBeenCalledWith({
      interruptId: 'interrupt-approval-structured',
      decision_payload: {
        applyNetworkPolicyAmendment: {
          network_policy_amendment: {
            action: 'allow',
            host: 'api.github.com',
          },
        },
      },
    })
  })

  it('auto-submits option-only user input without rendering a submit button', () => {
    const onRespond = vi.fn()

    render(
      <GlobalInterruptPanel
        items={[{
          id: 'interrupt-options',
          kind: 'user_input',
          issue_identifier: 'ISS-2',
          issue_title: 'Choose environment',
          requested_at: '2026-03-16T10:00:00Z',
          user_input: {
            questions: [
              {
                id: 'environment',
                question: 'Which environment should I use?',
                options: [{ label: 'Staging' }, { label: 'Production' }],
              },
            ],
          },
        }]}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onRespond={onRespond}
      />,
    )

    expect(screen.queryByRole('button', { name: /submit response/i })).not.toBeInTheDocument()

    fireEvent.click(screen.getByText('Staging').closest('button')!)

    expect(onRespond).toHaveBeenCalledWith({
      interruptId: 'interrupt-options',
      answers: {
        environment: ['Staging'],
      },
    })
  })

  it('keeps the submit button when user input includes an other-answer text input', () => {
    const onRespond = vi.fn()

    render(
      <GlobalInterruptPanel
        items={[{
          id: 'interrupt-other',
          kind: 'user_input',
          issue_identifier: 'ISS-3',
          issue_title: 'Choose action',
          requested_at: '2026-03-16T10:00:00Z',
          user_input: {
            questions: [
              {
                id: 'action',
                question: 'How should I proceed?',
                options: [{ label: 'Use default' }, { label: 'Skip' }],
                is_other: true,
              },
            ],
          },
        }]}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onRespond={onRespond}
      />,
    )

    const submitButton = screen.getByRole('button', { name: /submit response/i })
    expect(submitButton).toBeDisabled()

    fireEvent.click(screen.getByText('Use default').closest('button')!)

    expect(onRespond).not.toHaveBeenCalled()
    expect(submitButton).toBeEnabled()
  })

  it('defaults the detail pane to the first actionable interrupt when alerts are also queued', () => {
    render(
      <GlobalInterruptPanel
        items={[
          {
            id: 'alert-project-dispatch-1',
            kind: 'alert',
            issue_identifier: 'ISS-9',
            issue_title: 'Blocked issue',
            project_id: 'project-1',
            project_name: 'Platform',
            requested_at: '2026-03-16T10:00:00Z',
            actions: [{ kind: 'acknowledge', label: 'Acknowledge' }],
            alert: {
              code: 'project_dispatch_blocked',
              severity: 'error',
              title: 'Project dispatch blocked',
              message: 'Project repo is outside the current server scope (/repo/current)',
            },
          },
          makeApprovalInterrupt(),
        ]}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onRespond={() => {}}
      />,
    )

    expect(screen.getAllByText('Review migrations').length).toBeGreaterThan(0)
    expect(screen.getByText('2 waiting')).toBeInTheDocument()
    expect(screen.getAllByText('Project dispatch blocked').length).toBeGreaterThan(0)
    expect(screen.getAllByRole('button', { name: 'Acknowledge' }).length).toBeGreaterThan(0)
  })

  it('keeps later queued approvals read-only until they reach the front of the queue', () => {
    const onRespond = vi.fn()

    render(
      <GlobalInterruptPanel
        items={[
          makeApprovalInterrupt(),
          {
            ...makeApprovalInterrupt(),
            id: 'interrupt-approval-2',
            issue_identifier: 'ISS-2',
            issue_title: 'Approve deployment',
            approval: {
              command: 'deploy production',
              decisions: [{ value: 'approved_once', label: 'Approve once' }],
            },
          },
        ]}
        respondableInterruptId="interrupt-approval"
        isSubmitting={false}
        onAcknowledge={() => {}}
        onRespond={onRespond}
      />,
    )

    fireEvent.click(screen.getByText('Approve deployment').closest('button')!)

    expect(screen.getByText(/an earlier interrupt is still pending/i)).toBeInTheDocument()

    const approveButton = screen.getByRole('button', { name: /approve once/i })
    expect(approveButton).toBeDisabled()

    fireEvent.click(approveButton)

    expect(onRespond).not.toHaveBeenCalled()
  })

  it('renders alert actions and deep links for issue-level maestro blockers', () => {
    const onAcknowledge = vi.fn()

    render(
      <GlobalInterruptPanel
        items={[{
          id: 'alert-project-dispatch-2',
          kind: 'alert',
          issue_identifier: 'ISS-7',
          issue_title: 'Blocked issue',
          project_id: 'project-1',
          project_name: 'Platform',
          requested_at: '2026-03-16T10:00:00Z',
          actions: [{ kind: 'acknowledge', label: 'Acknowledge' }],
          alert: {
            code: 'project_dispatch_blocked',
            severity: 'error',
            title: 'Project dispatch blocked',
            message: 'Project repo is outside the current server scope (/repo/current)',
            detail: 'Blocked issue is waiting for execution until the project scope mismatch is fixed.',
          },
        }]}
        isSubmitting={false}
        onAcknowledge={onAcknowledge}
        onRespond={() => {}}
      />,
    )

    expect(screen.getAllByText('Project dispatch blocked').length).toBeGreaterThan(0)
    expect(screen.getByText('Blocked issue is waiting for execution until the project scope mismatch is fixed.')).toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: 'Open issue' })[0]).toHaveAttribute('href', '/issues/ISS-7')
    expect(screen.getAllByRole('link', { name: 'Open project' })[0]).toHaveAttribute('href', '/projects/project-1')

    fireEvent.click(screen.getAllByRole('button', { name: 'Acknowledge' })[0]!)

    expect(onAcknowledge).toHaveBeenCalledWith('alert-project-dispatch-2')
  })
})
