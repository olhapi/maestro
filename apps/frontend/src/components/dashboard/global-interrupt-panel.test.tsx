import { fireEvent, render, screen } from '@testing-library/react'
import type { ComponentProps } from 'react'
import { vi } from 'vitest'

import { GlobalInterruptPanel } from '@/components/dashboard/global-interrupt-panel'
import type { PendingInterrupt } from '@/lib/types'

function makeApprovalInterrupt(overrides: { command?: string } = {}) {
  return {
    id: 'interrupt-approval',
    kind: 'approval' as const,
    issue_identifier: 'ISS-1',
    issue_title: 'Review migrations',
    phase: 'review',
    attempt: 1,
    requested_at: '2026-03-16T10:00:00Z',
    approval: {
      command: overrides.command ?? 'ssh-add --apple-use-keychain ~/.ssh/id_rsa ~/.ssh/squirro.key',
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

type GlobalInterruptPanelProps = ComponentProps<typeof GlobalInterruptPanel>

function renderInterruptPanel(
  items: PendingInterrupt[],
  overrides: {
    open?: boolean
    respondableInterruptId?: string | null
    isSubmitting?: boolean
    onAcknowledge?: GlobalInterruptPanelProps['onAcknowledge']
    onOpenChange?: GlobalInterruptPanelProps['onOpenChange']
    onRequestPlanRevision?: GlobalInterruptPanelProps['onRequestPlanRevision']
    onRespond?: GlobalInterruptPanelProps['onRespond']
  } = {},
) {
  const onOpenChange = overrides.onOpenChange ?? vi.fn()

  render(
    <GlobalInterruptPanel
      items={items}
      open={overrides.open ?? true}
      respondableInterruptId={overrides.respondableInterruptId}
      isSubmitting={overrides.isSubmitting ?? false}
      onAcknowledge={overrides.onAcknowledge ?? (() => {})}
      onOpenChange={onOpenChange}
      onRequestPlanRevision={overrides.onRequestPlanRevision ?? (() => {})}
      onRespond={overrides.onRespond ?? (() => {})}
    />,
  )

  return { onOpenChange }
}

describe('GlobalInterruptPanel', () => {
  it('renders a fullscreen dialog with a pinned approval footer', () => {
    renderInterruptPanel([makePlanApprovalInterrupt()])

    const panel = screen.getByTestId('global-interrupt-panel')
    expect(panel).toHaveClass('h-[100dvh]', 'w-[100vw]', 'overflow-hidden', 'rounded-none')
    const body = screen.getByTestId('global-interrupt-body')
    expect(body).toHaveClass('overflow-y-auto')
    const footer = screen.getByTestId('global-interrupt-footer')
    expect(footer).toHaveClass('shrink-0')
    expect(footer).toContainElement(screen.getByRole('button', { name: /approve once/i }))
    expect(screen.getAllByText('Review the proposed plan').length).toBeGreaterThan(0)
    expect(screen.queryByPlaceholderText(/explain what should change in the plan/i)).not.toBeInTheDocument()
  })

  it('calls onOpenChange when the dialog is dismissed', () => {
    const { onOpenChange } = renderInterruptPanel([makeApprovalInterrupt()])

    fireEvent.click(screen.getByRole('button', { name: /hide waiting input dialog/i }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('renders the richer approval structure and auto-submits plain approval decisions', () => {
    const onRespond = vi.fn()
    const longCommand =
      'ssh-add --apple-use-keychain ~/.ssh/id_rsa ~/.ssh/squirro.key --with-an-exceptionally-long-token-that-should-wrap'

    renderInterruptPanel([makeApprovalInterrupt({ command: longCommand })], { onRespond })

    expect(screen.getByText('Allow the agent to run this command?')).toBeInTheDocument()
    expect(screen.getByText('Requested command')).toBeInTheDocument()
    expect(screen.getByText('Add SSH keys to macOS keychain agent')).toBeInTheDocument()
    expect(screen.getByText('Working directory')).toBeInTheDocument()
    expect(screen.getByText('/Users/olhapi-work')).toBeInTheDocument()
    const requestedCommand = screen.getByText(
      (content, element) => element?.tagName === 'CODE' && content.includes('exceptionally-long-token'),
    )
    expect(requestedCommand).toHaveClass('whitespace-pre-wrap', 'break-words')
    expect(requestedCommand).not.toHaveClass('overflow-x-auto')
    expect(screen.queryByRole('button', { name: /submit response/i })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /approve once/i }))

    expect(onRespond).toHaveBeenCalledWith({ interruptId: 'interrupt-approval', decision: 'approved_once' })
  })

  it('renders a steering note field for approvals and forwards note-only responses', () => {
    const onRespond = vi.fn()

    renderInterruptPanel([makeApprovalInterrupt()], { onRespond })

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

  it('can collapse and restore a drafted plan note without clearing it', () => {
    renderInterruptPanel([makePlanApprovalInterrupt()])

    fireEvent.click(screen.getByRole('button', { name: /add note/i }))

    const noteField = screen.getByPlaceholderText(/explain what should change in the plan/i)
    fireEvent.change(noteField, {
      target: { value: 'Keep the rollout small.' },
    })

    fireEvent.click(screen.getByRole('button', { name: /hide note/i }))

    expect(screen.queryByPlaceholderText(/explain what should change in the plan/i)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /edit note/i }))

    expect(screen.getByPlaceholderText(/explain what should change in the plan/i)).toHaveValue(
      'Keep the rollout small.',
    )
  })

  it('renders structured plan sections and requires a note before requesting changes', () => {
    const onRespond = vi.fn()
    const onRequestPlanRevision = vi.fn()

    renderInterruptPanel([makePlanApprovalInterrupt({
      markdown:
        'Questions:\n- Tighten the rollout\n\nAssumptions:\n- Keep the current deployment window\n\nPlan:\n1. Add a rollback check',
    })], { onRespond, onRequestPlanRevision })

    expect(screen.getByText('Questions to resolve')).toBeInTheDocument()
    expect(screen.getByText('Assumptions in scope')).toBeInTheDocument()
    expect(screen.getByText('Implementation plan')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /request changes/i }))

    const noteField = screen.getByPlaceholderText(/explain what should change in the plan/i)
    expect(noteField).toHaveFocus()

    fireEvent.change(noteField, {
      target: { value: 'Make the rollout smaller and add a rollback guard.' },
    })

    fireEvent.click(screen.getByRole('button', { name: /request changes/i }))

    expect(onRequestPlanRevision).toHaveBeenCalledWith({
      issueIdentifier: 'ISS-1',
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
        open={true}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onOpenChange={() => {}}
        onRequestPlanRevision={() => {}}
        onRespond={onRespond}
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
      <GlobalInterruptPanel
        items={[makePlanApprovalInterrupt({
          requestedAt: '2026-03-16T10:15:00Z',
          markdown: 'Review the updated plan before execution.\n\n- Tighten the rollout further',
        })]}
        open={true}
        isSubmitting={false}
        onAcknowledge={() => {}}
        onOpenChange={() => {}}
        onRequestPlanRevision={() => {}}
        onRespond={onRespond}
      />,
    )

    expect(screen.queryByPlaceholderText(/explain what should change in the plan/i)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /add note/i }))
    expect(screen.getByPlaceholderText(/explain what should change in the plan/i)).toHaveValue('')

    fireEvent.click(screen.getByRole('button', { name: /approve once/i }))

    expect(onRespond).toHaveBeenCalledWith({
      interruptId: 'interrupt-plan-approval',
      decision: 'approved_once',
    })
  })

  it('uses decision payload when approval options provide structured responses', () => {
    const onRespond = vi.fn()

    renderInterruptPanel([{
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
    }], { onRespond })

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

    renderInterruptPanel([{
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
    }], { onRespond })

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

    renderInterruptPanel([{
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
    }], { onRespond })

    const submitButton = screen.getByRole('button', { name: /submit response/i })
    expect(submitButton).toBeDisabled()

    fireEvent.click(screen.getByText('Use default').closest('button')!)

    expect(onRespond).not.toHaveBeenCalled()
    expect(submitButton).toBeEnabled()
  })

  it('defaults the detail pane to the first actionable interrupt when alerts are also queued', () => {
    renderInterruptPanel([
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
    ])

    expect(screen.getByRole('button', { name: /queue \(2\)/i })).toBeInTheDocument()
    expect(screen.getByText('Allow the agent to run this command?')).toBeInTheDocument()
    expect(screen.queryByText('Waiting queue')).not.toBeInTheDocument()
  })

  it('keeps later queued approvals read-only until they reach the front of the queue', () => {
    const onRespond = vi.fn()

    renderInterruptPanel([
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
    ], { respondableInterruptId: 'interrupt-approval', onRespond })

    fireEvent.click(screen.getByRole('button', { name: /queue \(2\)/i }))
    fireEvent.click(screen.getByText('Approve deployment').closest('button')!)

    expect(screen.getByText(/an earlier interrupt is still pending/i)).toBeInTheDocument()

    const approveButton = screen.getByRole('button', { name: /approve once/i })
    expect(approveButton).toBeDisabled()

    fireEvent.click(approveButton)

    expect(onRespond).not.toHaveBeenCalled()
  })

  it('renders alert actions and deep links for issue-level maestro blockers', () => {
    const onAcknowledge = vi.fn()

    renderInterruptPanel([{
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
    }], { onAcknowledge })

    expect(screen.getAllByText('Project dispatch blocked').length).toBeGreaterThan(0)
    expect(screen.getByText('Blocked issue is waiting for execution until the project scope mismatch is fixed.')).toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: 'Open issue' })[0]).toHaveAttribute('href', '/issues/ISS-7')
    expect(screen.getAllByRole('link', { name: 'Open project' })[0]).toHaveAttribute('href', '/projects/project-1')

    fireEvent.click(screen.getAllByRole('button', { name: 'Acknowledge' })[0]!)

    expect(onAcknowledge).toHaveBeenCalledWith('alert-project-dispatch-2')
  })
})
