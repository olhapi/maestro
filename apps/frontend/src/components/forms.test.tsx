import { fireEvent, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { IssueDialog } from '@/components/forms'
import { makeBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

describe('IssueDialog', () => {
  it('serializes recurring issue fields on submit', async () => {
    const bootstrap = makeBootstrapResponse()
    const onSubmit = vi.fn().mockResolvedValue(undefined)

    renderWithQueryClient(
      <IssueDialog
        open
        onOpenChange={vi.fn()}
        projects={bootstrap.projects}
        epics={bootstrap.epics}
        onSubmit={onSubmit}
      />,
    )

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Scan GitHub ready-to-work' } })
    fireEvent.change(screen.getByLabelText(/^type$/i), { target: { value: 'recurring' } })
    fireEvent.change(screen.getByLabelText(/cron/i), { target: { value: '*/15 * * * *' } })
    fireEvent.change(screen.getByLabelText(/schedule/i), { target: { value: 'false' } })
    fireEvent.change(screen.getByLabelText(/labels/i), { target: { value: 'github, automation' } })
    fireEvent.change(screen.getByLabelText(/blockers/i), { target: { value: 'ISS-2, ISS-3' } })

    fireEvent.click(screen.getByRole('button', { name: /create issue/i }))

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          project_id: 'project-1',
          epic_id: '',
          title: 'Scan GitHub ready-to-work',
          issue_type: 'recurring',
          cron: '*/15 * * * *',
          enabled: false,
          labels: ['github', 'automation'],
          blocked_by: ['ISS-2', 'ISS-3'],
        }),
      )
    })
  })

  it('requires a project before submitting', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)

    renderWithQueryClient(
      <IssueDialog
        open
        onOpenChange={vi.fn()}
        projects={[]}
        epics={[]}
        onSubmit={onSubmit}
      />,
    )

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Missing project' } })

    expect(screen.getByRole('button', { name: /create issue/i })).toBeDisabled()
  })
})
