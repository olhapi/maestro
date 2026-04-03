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

function makeDoneIssues(count: number) {
  return Array.from({ length: count }, (_, index) =>
    makeIssueSummary({
      id: `done-${index + 1}`,
      identifier: `DONE-${String(index + 1).padStart(2, '0')}`,
      title: `Done task ${index + 1}`,
      state: 'done',
      updated_at: `2026-03-${String((index % 28) + 1).padStart(2, '0')}T11:00:00Z`,
    }),
  )
}

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

  it('progressively reveals done issues in grouped mode', () => {
    renderWithQueryClient(
      <KanbanBoard
        items={[
          makeIssueSummary({
            id: 'ready-1',
            identifier: 'READY-1',
            title: 'Ready task',
            state: 'ready',
          }),
          ...makeDoneIssues(35),
        ]}
        mode="grouped"
        onCreateIssue={vi.fn()}
        onMoveIssue={vi.fn()}
        onOpenIssue={vi.fn()}
      />,
    )

    expect(screen.getByText('Ready task')).toBeInTheDocument()
    expect(screen.getAllByText(/Done task \d+/)).toHaveLength(30)
    expect(screen.getByText('Showing 30 of 35')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Load 5 more' })).toBeInTheDocument()
    expect(screen.queryByText('Done task 31')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Load 5 more' }))

    expect(screen.getAllByText(/Done task \d+/)).toHaveLength(35)
    expect(screen.getByText('Done task 31')).toBeInTheDocument()
    expect(screen.queryByText('Showing 30 of 35')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Load 5 more' })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /collapse done status row/i }))

    expect(screen.getByRole('button', { name: /expand done status row/i })).toHaveAttribute(
      'aria-expanded',
      'false',
    )

    fireEvent.click(screen.getByRole('button', { name: /expand done status row/i }))

    expect(screen.getByRole('button', { name: /collapse done status row/i })).toHaveAttribute(
      'aria-expanded',
      'true',
    )
    expect(screen.getAllByText(/Done task \d+/)).toHaveLength(35)
    expect(screen.queryByText('Showing 30 of 35')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Load 5 more' })).not.toBeInTheDocument()
  })

  it('progressively reveals done issues in board mode', () => {
    const { rerender } = renderWithQueryClient(
      <KanbanBoard
        items={[
          makeIssueSummary({
            id: 'ready-1',
            identifier: 'READY-1',
            title: 'Ready task',
            state: 'ready',
          }),
          ...makeDoneIssues(35),
        ]}
        onCreateIssue={vi.fn()}
        onMoveIssue={vi.fn()}
        onOpenIssue={vi.fn()}
      />,
    )

    expect(screen.getByText('Ready task')).toBeInTheDocument()
    expect(screen.getAllByText(/Done task \d+/)).toHaveLength(30)
    expect(screen.getByText('Showing 30 of 35')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Load 5 more' })).toBeInTheDocument()
    expect(screen.queryByText('Done task 31')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Load 5 more' }))

    expect(screen.getAllByText(/Done task \d+/)).toHaveLength(35)
    expect(screen.getByText('Done task 31')).toBeInTheDocument()
    expect(screen.queryByText('Showing 30 of 35')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Load 5 more' })).not.toBeInTheDocument()

    rerender(
      <KanbanBoard
        items={[
          makeIssueSummary({
            id: 'ready-1',
            identifier: 'READY-1',
            title: 'Ready task',
            state: 'ready',
          }),
          makeIssueSummary({
            id: 'backlog-1',
            identifier: 'BACKLOG-1',
            title: 'Backlog task',
            state: 'backlog',
          }),
          ...makeDoneIssues(35),
        ]}
        onCreateIssue={vi.fn()}
        onMoveIssue={vi.fn()}
        onOpenIssue={vi.fn()}
      />,
    )

    expect(screen.getByText('Backlog task')).toBeInTheDocument()
    expect(screen.getAllByText(/Done task \d+/)).toHaveLength(30)
    expect(screen.getByText('Showing 30 of 35')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Load 5 more' })).toBeInTheDocument()
    expect(screen.queryByText('Done task 31')).not.toBeInTheDocument()
  })
})
