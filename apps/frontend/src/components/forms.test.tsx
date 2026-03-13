import { fireEvent, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { IssueDialog, ProjectDialog } from '@/components/forms'
import { makeBootstrapResponse, makeIssueSummary } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@/lib/api', () => ({
  api: {
    listIssues: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

describe('IssueDialog', () => {
  beforeEach(() => {
    vi.mocked(api.listIssues).mockReset()
  })

  it('serializes recurring issue fields on submit', async () => {
    const bootstrap = makeBootstrapResponse()
    vi.mocked(api.listIssues).mockResolvedValue({
      items: [
        makeIssueSummary({
          identifier: 'ISS-1',
          title: 'Current issue',
          labels: ['api'],
        }),
        makeIssueSummary({
          id: 'issue-2',
          identifier: 'ISS-2',
          title: 'Unblock scheduler',
          labels: ['automation'],
        }),
      ],
      total: 2,
      limit: 200,
      offset: 0,
    })
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

    await waitFor(() => {
      expect(api.listIssues).toHaveBeenCalledWith({
        project_id: 'project-1',
        limit: 200,
        offset: 0,
        sort: 'identifier_asc',
      })
    })

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Scan GitHub ready-to-work' } })
    fireEvent.click(screen.getByRole('radio', { name: /recurring/i }))
    fireEvent.change(screen.getByLabelText(/cron/i), { target: { value: '*/15 * * * *' } })
    fireEvent.click(screen.getByRole('switch', { name: /schedule/i }))

    const labelsInput = screen.getByLabelText(/labels/i)
    fireEvent.focus(labelsInput)
    fireEvent.change(labelsInput, { target: { value: 'api' } })
    fireEvent.click(await screen.findByRole('option', { name: 'api' }))
    fireEvent.change(labelsInput, { target: { value: 'github' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create label "github"' }))

    const blockersInput = screen.getByLabelText(/blockers/i)
    fireEvent.focus(blockersInput)
    fireEvent.change(blockersInput, { target: { value: 'ISS-2' } })
    fireEvent.click(await screen.findByRole('option', { name: /ISS-2 · Unblock scheduler/i }))

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
          labels: ['api', 'github'],
          blocked_by: ['ISS-2'],
        }),
      )
    })
  })

  it('limits blocker suggestions to the selected project and excludes the edited issue', async () => {
    const bootstrap = makeBootstrapResponse()
    vi.mocked(api.listIssues).mockResolvedValue({
      items: [
        makeIssueSummary({
          identifier: 'ISS-1',
          title: 'Current issue',
        }),
        makeIssueSummary({
          id: 'issue-2',
          identifier: 'ISS-2',
          title: 'Project issue',
        }),
      ],
      total: 2,
      limit: 200,
      offset: 0,
    })

    renderWithQueryClient(
      <IssueDialog
        open
        onOpenChange={vi.fn()}
        initial={{ identifier: 'ISS-1', project_id: 'project-1' }}
        projects={bootstrap.projects}
        epics={bootstrap.epics}
        onSubmit={vi.fn().mockResolvedValue(undefined)}
      />,
    )

    await waitFor(() => {
      expect(api.listIssues).toHaveBeenCalled()
    })

    const blockersInput = screen.getByLabelText(/blockers/i)
    fireEvent.focus(blockersInput)
    fireEvent.change(blockersInput, { target: { value: 'ISS' } })

    expect(await screen.findByRole('option', { name: /ISS-2 · Project issue/i })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /ISS-1 · Current issue/i })).not.toBeInTheDocument()
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

describe('ProjectDialog', () => {
  it('caps dialog height to the viewport and enables internal scrolling', () => {
    renderWithQueryClient(
      <ProjectDialog
        open
        onOpenChange={vi.fn()}
        onSubmit={vi.fn().mockResolvedValue(undefined)}
      />,
    )

    expect(screen.getByRole('dialog')).toHaveClass('max-h-[calc(100vh-2rem)]', 'overflow-y-auto')
  })
})
