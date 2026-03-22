import { forwardRef, type ComponentPropsWithoutRef } from 'react'
import { fireEvent, screen } from '@testing-library/react'
import { vi } from 'vitest'

import { KanbanBoard } from '@/components/dashboard/kanban-board'
import { makeIssueSummary } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@tanstack/react-router', () => ({
  Link: forwardRef<
    HTMLAnchorElement,
    ComponentPropsWithoutRef<'a'> & {
      params?: { identifier?: string }
    }
  >(({ children, className, params, ...props }, ref) => (
    <a
      ref={ref}
      className={className}
      href={params?.identifier ? `/issues/${params.identifier}` : '#'}
      {...props}
    >
      {children}
    </a>
  )),
}))

describe('KanbanBoard', () => {
  it('collapses and expands grouped status rows independently', () => {
    renderWithQueryClient(
      <KanbanBoard
        items={[
          makeIssueSummary({
            id: 'issue-1',
            identifier: 'ISS-1',
            title: 'Investigate retries',
            state: 'backlog',
          }),
          makeIssueSummary({
            id: 'issue-2',
            identifier: 'ISS-2',
            title: 'Prepare release notes',
            state: 'ready',
          }),
        ]}
        mode="grouped"
        onCreateIssue={vi.fn()}
        onMoveIssue={vi.fn()}
        onOpenIssue={vi.fn()}
      />,
    )

    const backlogRow = screen.getByRole('button', { name: /collapse backlog status row/i })
    expect(backlogRow).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Investigate retries')).toBeInTheDocument()
    expect(screen.getByText('Prepare release notes')).toBeInTheDocument()

    fireEvent.click(backlogRow)

    expect(screen.getByRole('button', { name: /expand backlog status row/i })).toHaveAttribute(
      'aria-expanded',
      'false',
    )
    expect(screen.queryByText('Investigate retries')).not.toBeInTheDocument()
    expect(screen.getByText('Prepare release notes')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /expand backlog status row/i }))

    expect(screen.getByRole('button', { name: /collapse backlog status row/i })).toHaveAttribute(
      'aria-expanded',
      'true',
    )
    expect(screen.getByText('Investigate retries')).toBeInTheDocument()
  })
})
