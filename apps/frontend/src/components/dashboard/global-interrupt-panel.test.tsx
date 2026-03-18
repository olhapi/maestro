import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { GlobalInterruptPanel } from '@/components/dashboard/global-interrupt-panel'

describe('GlobalInterruptPanel', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('renders the richer approval structure and auto-submits plain approval decisions', () => {
    const onRespond = vi.fn()

    render(
      <GlobalInterruptPanel
        count={1}
        current={{
          id: 'interrupt-approval',
          kind: 'approval',
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
        }}
        isSubmitting={false}
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

    expect(onRespond).toHaveBeenCalledWith({ decision: 'approved_once' })
  })

  it('uses decision payload when approval options provide structured responses', () => {
    const onRespond = vi.fn()

    render(
      <GlobalInterruptPanel
        count={1}
        current={{
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
        }}
        isSubmitting={false}
        onRespond={onRespond}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: /persist allow rule/i }))

    expect(onRespond).toHaveBeenCalledWith({
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
        count={1}
        current={{
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
        }}
        isSubmitting={false}
        onRespond={onRespond}
      />,
    )

    expect(screen.queryByRole('button', { name: /submit response/i })).not.toBeInTheDocument()

    fireEvent.click(screen.getByText('Staging').closest('button')!)

    expect(onRespond).toHaveBeenCalledWith({
      answers: {
        environment: ['Staging'],
      },
    })
  })

  it('keeps the submit button when user input includes an other-answer text input', () => {
    const onRespond = vi.fn()

    render(
      <GlobalInterruptPanel
        count={1}
        current={{
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
        }}
        isSubmitting={false}
        onRespond={onRespond}
      />,
    )

    const submitButton = screen.getByRole('button', { name: /submit response/i })
    expect(submitButton).toBeInTheDocument()
    expect(submitButton).toBeDisabled()

    fireEvent.click(screen.getByText('Use default').closest('button')!)

    expect(onRespond).not.toHaveBeenCalled()
    expect(submitButton).toBeEnabled()
  })

  it('applies the closing state before unmounting when the current interrupt is hidden', () => {
    vi.useFakeTimers()
    const onRespond = vi.fn()

    const interrupt = {
      id: 'interrupt-hiding',
      kind: 'approval' as const,
      issue_identifier: 'ISS-9',
      issue_title: 'Approve command',
      requested_at: '2026-03-16T10:00:00Z',
      approval: {
        command: 'gh pr view',
        decisions: [{ value: 'approved', label: 'Approve once' }],
      },
    }

    const { rerender } = render(
      <GlobalInterruptPanel count={1} current={interrupt} isSubmitting={false} onRespond={onRespond} />,
    )

    expect(screen.getByTestId('global-interrupt-panel')).toHaveAttribute('data-visibility', 'visible')

    rerender(
      <GlobalInterruptPanel
        count={1}
        current={interrupt}
        hiddenCurrentId="interrupt-hiding"
        isSubmitting
        onRespond={onRespond}
      />,
    )

    expect(screen.getByTestId('global-interrupt-panel')).toHaveAttribute('data-visibility', 'exiting')

    act(() => {
      vi.advanceTimersByTime(180)
    })

    expect(screen.queryByTestId('global-interrupt-panel')).not.toBeInTheDocument()
  })
})
