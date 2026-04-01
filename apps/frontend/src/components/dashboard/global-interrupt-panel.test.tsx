import { act, fireEvent, render, screen, within } from '@testing-library/react'
import type { ComponentProps } from 'react'
import { afterEach, vi } from 'vitest'

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

function makeElicitationInterrupt(overrides: {
  mode?: 'form' | 'url'
  message?: string
  url?: string
  requestedSchema?: Record<string, unknown>
} = {}) {
  return {
    id: 'interrupt-elicitation',
    kind: 'elicitation' as const,
    issue_identifier: 'ISS-8',
    issue_title: 'Fill contact details',
    requested_at: '2026-03-16T10:00:00Z',
    elicitation: {
      server_name: 'support-bot',
      message: overrides.message ?? 'Need contact details',
      mode: overrides.mode ?? 'form',
      requested_schema:
        overrides.requestedSchema ?? {
          type: 'object',
          properties: {
            email: { type: 'string' },
          },
          required: ['email'],
        },
      url: overrides.url,
      elicitation_id: overrides.mode === 'url' ? 'elicitation-42' : 'elicitation-7',
    },
  }
}

function makeNestedElicitationSchema() {
  return {
    type: 'object',
    properties: {
      profile: {
        type: 'object',
        title: 'Profile',
        properties: {
          name: {
            type: 'string',
            title: 'Name',
          },
          contact: {
            type: 'object',
            title: 'Contact',
            properties: {
              email: {
                type: 'string',
                title: 'Email',
                format: 'email',
              },
            },
            required: ['email'],
          },
          role: {
            type: 'string',
            title: 'Role',
            enum: ['engineer', 'manager'],
            enumNames: ['Engineer', 'Manager'],
            default: 'engineer',
          },
        },
        required: ['name', 'contact'],
      },
      delivery: {
        oneOf: [
          {
            title: 'Email',
            type: 'object',
            properties: {
              address: {
                type: 'string',
                title: 'Address',
                format: 'email',
              },
            },
            required: ['address'],
          },
          {
            title: 'Webhook',
            type: 'object',
            properties: {
              endpoint: {
                type: 'string',
                title: 'Endpoint',
                format: 'uri',
              },
            },
            required: ['endpoint'],
          },
        ],
      },
    },
    required: ['profile', 'delivery'],
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

afterEach(() => {
  vi.useRealTimers()
})

describe('GlobalInterruptPanel', () => {
  it('renders a fullscreen dialog with in-body plan review actions', () => {
    renderInterruptPanel([makePlanApprovalInterrupt()])

    const panel = screen.getByTestId('global-interrupt-panel')
    expect(panel).toHaveClass('h-[100dvh]', 'w-[100vw]', 'overflow-hidden', 'rounded-none')
    const body = screen.getByTestId('global-interrupt-body')
    expect(body).toHaveClass('overflow-y-auto')
    expect(screen.queryByTestId('global-interrupt-footer')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /approve once/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /more actions/i })).toBeInTheDocument()
    expect(screen.getAllByText('Review the proposed plan')).toHaveLength(1)
    expect(screen.queryByPlaceholderText(/explain what should change in the plan/i)).not.toBeInTheDocument()
  })

  it('calls onOpenChange when the dialog is dismissed', () => {
    const { onOpenChange } = renderInterruptPanel([makeApprovalInterrupt()])

    fireEvent.click(screen.getByRole('button', { name: /hide waiting input dialog/i }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('keeps selected and queued interrupt ages ticking from activity or request time', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-03-16T10:00:00Z'))

    renderInterruptPanel([
      {
        ...makeApprovalInterrupt(),
        last_activity_at: '2026-03-16T10:00:00Z',
      },
      {
        ...makeApprovalInterrupt({ command: 'deploy production' }),
        id: 'interrupt-approval-2',
        issue_identifier: 'ISS-2',
        issue_title: 'Approve deployment',
        requested_at: '2026-03-16T09:59:58Z',
        last_activity_at: undefined,
      },
    ])

    fireEvent.click(screen.getByRole('button', { name: /queue \(2\)/i }))

    expect(screen.getAllByText('Updated 0s ago')).toHaveLength(2)
    expect(screen.getByText('Updated 2s ago')).toBeInTheDocument()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000)
    })

    expect(screen.getAllByText('Updated 2s ago')).toHaveLength(2)
    expect(screen.getByText('Updated 4s ago')).toBeInTheDocument()
  })

  it('renders the richer approval structure and auto-submits plain approval decisions', () => {
    const onRespond = vi.fn()
    const longCommand =
      'ssh-add --apple-use-keychain ~/.ssh/id_rsa ~/.ssh/squirro.key --with-an-exceptionally-long-token-that-should-wrap-and-keep-going-beyond-the-preview-threshold-for-testing'

    renderInterruptPanel([makeApprovalInterrupt({ command: longCommand })], { onRespond })

    expect(screen.getByText('Allow the agent to run this command?')).toBeInTheDocument()
    expect(screen.getByText('Reason')).toBeInTheDocument()
    expect(screen.getByText('Requested command')).toBeInTheDocument()
    expect(screen.getByText('Add SSH keys to macOS keychain agent')).toBeInTheDocument()
    expect(screen.getByText('Working directory')).toBeInTheDocument()
    expect(screen.getByText('/Users/olhapi-work')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /show full command/i })).toBeInTheDocument()
    expect(
      screen.queryByText(
        (content, element) =>
          element?.tagName === 'CODE' &&
          content.includes('preview-threshold-for-testing') &&
          content === longCommand,
      ),
    ).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /show full command/i }))

    const requestedCommand = screen.getByText((content, element) => element?.tagName === 'CODE' && content === longCommand)
    expect(requestedCommand).toHaveClass('whitespace-pre-wrap', 'break-words')
    expect(screen.queryByRole('button', { name: /submit response/i })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /approve once/i }))

    expect(onRespond).toHaveBeenCalledWith({ interruptId: 'interrupt-approval', decision: 'approved_once' })
  })

  it('keeps non-default approval actions inside More actions and forwards decision payloads', () => {
    const onRespond = vi.fn()

    renderInterruptPanel([{
      id: 'interrupt-approval-overflow',
      kind: 'approval',
      issue_identifier: 'ISS-4',
      issue_title: 'Review network access',
      requested_at: '2026-03-16T10:00:00Z',
      approval: {
        command: 'curl https://api.github.com',
        decisions: [
          {
            value: 'approved_once',
            label: 'Approve once',
            description: 'Run this request once and keep the turn going.',
          },
          {
            value: 'accept_with_execpolicy_amendment',
            label: 'Approve and store rule',
            description: 'Run this request and allow future matching commands without prompting.',
            decision_payload: {
              acceptWithExecpolicyAmendment: {
                execpolicy_amendment: ['allow command=curl https://api.github.com'],
              },
            },
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
          {
            value: 'abort',
            label: 'Abort',
            description: 'Reject the request and interrupt the current turn.',
          },
        ],
      },
    }], { onRespond })

    expect(screen.queryByRole('button', { name: /approve and store rule/i })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /more actions/i }))

    expect(screen.getByText('Allow more broadly')).toBeInTheDocument()
    expect(screen.getByText('Reject request')).toBeInTheDocument()
    expect(screen.getByText('Stop current turn')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /approve and store rule/i }))

    expect(onRespond).toHaveBeenCalledWith({
      interruptId: 'interrupt-approval-overflow',
      decision_payload: {
        acceptWithExecpolicyAmendment: {
          execpolicy_amendment: ['allow command=curl https://api.github.com'],
        },
      },
    })
  })

  it('keeps the approval note composer collapsed by default and forwards note-only responses', () => {
    const onRespond = vi.fn()

    renderInterruptPanel([makeApprovalInterrupt()], { onRespond })

    expect(screen.queryByText('Agent note')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /add steering note/i }))

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

    fireEvent.click(screen.getByRole('button', { name: /add steering note/i }))

    const noteField = screen.getByPlaceholderText(/explain what should change in the plan/i)
    fireEvent.change(noteField, {
      target: { value: 'Keep the rollout small.' },
    })

    fireEvent.click(screen.getByRole('button', { name: /hide note/i }))

    expect(screen.queryByPlaceholderText(/explain what should change in the plan/i)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /edit steering note/i }))

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

    fireEvent.click(screen.getByRole('button', { name: /add steering note/i }))
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
    fireEvent.click(screen.getByRole('button', { name: /add steering note/i }))
    expect(screen.getByPlaceholderText(/explain what should change in the plan/i)).toHaveValue('')

    fireEvent.click(screen.getByRole('button', { name: /approve once/i }))

    expect(onRespond).toHaveBeenCalledWith({
      interruptId: 'interrupt-plan-approval',
      decision: 'approved_once',
    })
  })

  it('uses decision payload when the primary approval option provides a structured response', () => {
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

  it('renders elicitation prompts and accepts an empty confirmation', () => {
    const onRespond = vi.fn()

    renderInterruptPanel([
      makeElicitationInterrupt({
        requestedSchema: {
          type: 'object',
          properties: {},
        },
      }),
    ], { onRespond })

    expect(screen.getByText('MCP elicitation')).toBeInTheDocument()
    expect(screen.getAllByText('Form')).toHaveLength(2)
    expect(screen.getByRole('button', { name: /accept and continue/i })).toBeEnabled()

    fireEvent.click(screen.getByRole('button', { name: /accept and continue/i }))

    expect(onRespond).toHaveBeenCalledWith({
      interruptId: 'interrupt-elicitation',
      action: 'accept',
      content: {},
    })
  })

  it('renders nested elicitation schemas without falling back to manual JSON', () => {
    renderInterruptPanel([
      makeElicitationInterrupt({
        requestedSchema: makeNestedElicitationSchema(),
      }),
    ])

    expect(screen.getByText('Profile')).toBeInTheDocument()
    expect(screen.getByText('Contact')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /engineer/i })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: /webhook/i })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.queryByText(/manual json payload/i)).not.toBeInTheDocument()
  })

  it('preserves elicitation drafts when switching between queued interrupts', () => {
    renderInterruptPanel([
      makeElicitationInterrupt({
        message: 'Need billing contact email',
      }),
      {
        ...makeElicitationInterrupt({
          message: 'Need shipping contact email',
        }),
        id: 'interrupt-elicitation-2',
        issue_identifier: 'ISS-9',
        issue_title: 'Fill shipping contact',
        requested_at: '2026-03-16T10:05:00Z',
      },
    ])

    fireEvent.click(screen.getByRole('button', { name: /queue \(2\)/i }))

    const queue = screen.getByText('Waiting queue').closest('aside')
    expect(queue).not.toBeNull()

    fireEvent.change(screen.getByLabelText(/email/i), {
      target: {
        value: 'billing@example.com',
      },
    })

    fireEvent.click(within(queue!).getByText('Need shipping contact email').closest('button')!)

    expect(screen.getByLabelText(/email/i)).toHaveValue('')

    fireEvent.click(within(queue!).getByText('Need billing contact email').closest('button')!)

    expect(screen.getByLabelText(/email/i)).toHaveValue('billing@example.com')
  })

  it('renders url-mode elicitation links', () => {
    renderInterruptPanel([
      makeElicitationInterrupt({
        mode: 'url',
        url: 'https://example.com/forms/contact',
      }),
    ])

    expect(screen.getAllByText('URL')).toHaveLength(2)
    expect(screen.getByRole('link', { name: /open url/i })).toHaveAttribute(
      'href',
      'https://example.com/forms/contact',
    )
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

    const secondInterrupt = makeApprovalInterrupt()

    renderInterruptPanel([
      makeApprovalInterrupt(),
      {
        ...secondInterrupt,
        id: 'interrupt-approval-2',
        issue_identifier: 'ISS-2',
        issue_title: 'Approve deployment',
        approval: {
          ...secondInterrupt.approval,
          command: 'deploy production',
        },
      },
    ], { respondableInterruptId: 'interrupt-approval', onRespond })

    fireEvent.click(screen.getByRole('button', { name: /queue \(2\)/i }))
    fireEvent.click(screen.getByText('Approve deployment').closest('button')!)

    expect(screen.getByText(/an earlier interrupt is still pending/i)).toBeInTheDocument()

    const approveButton = screen.getByRole('button', { name: /approve once/i })
    expect(approveButton).toBeDisabled()
    expect(screen.getByRole('button', { name: /more actions/i })).toBeDisabled()

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
