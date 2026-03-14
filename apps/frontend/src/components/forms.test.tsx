import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { IssueDialog, ProjectDialog } from '@/components/forms'
import { MockSpeechRecognition } from '@/test/mock-speech-recognition'
import {
  makeBootstrapResponse,
  makeIssueDetail,
  makeIssueImage,
  makeIssueSummary,
} from '@/test/fixtures'
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
    MockSpeechRecognition.reset()
    vi.unstubAllGlobals()
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
        {
          newImages: [],
          removeImageIDs: [],
        },
      )
    })
  })

  it('returns queued uploads and removals with the issue payload', async () => {
    const bootstrap = makeBootstrapResponse()
    vi.mocked(api.listIssues).mockResolvedValue({
      items: [],
      total: 0,
      limit: 200,
      offset: 0,
    })
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    const file = new File(['png'], 'bug.png', { type: 'image/png' })

    renderWithQueryClient(
      <IssueDialog
        open
        onOpenChange={vi.fn()}
        initial={makeIssueDetail({ images: [makeIssueImage()] })}
        projects={bootstrap.projects}
        epics={bootstrap.epics}
        onSubmit={onSubmit}
      />,
    )

    fireEvent.change(screen.getByLabelText(/title/i), {
      target: { value: 'Capture failing layout' },
    })
    fireEvent.change(screen.getByLabelText(/images/i, { selector: 'input' }), {
      target: { files: [file] },
    })
    await waitFor(() => {
      expect(screen.getAllByText('bug.png').length).toBeGreaterThan(0)
    })
    fireEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0])
    await waitFor(() => {
      expect(screen.getByText(/will be deleted after save/i)).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /update issue/i }))

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          title: 'Capture failing layout',
        }),
        {
          newImages: [file],
          removeImageIDs: ['img-1'],
        },
      )
    })
  })

  it('keeps the issue type toggle inset and radius aligned with the field container', async () => {
    const bootstrap = makeBootstrapResponse()
    vi.mocked(api.listIssues).mockResolvedValue({
      items: [],
      total: 0,
      limit: 200,
      offset: 0,
    })

    renderWithQueryClient(
      <IssueDialog
        open
        onOpenChange={vi.fn()}
        projects={bootstrap.projects}
        epics={bootstrap.epics}
        onSubmit={vi.fn().mockResolvedValue(undefined)}
      />,
    )

    const standard = await screen.findByRole('radio', { name: /standard/i })
    const recurring = screen.getByRole('radio', { name: /recurring/i })

    expect(standard).toHaveClass('rounded-lg')
    expect(recurring).toHaveClass('rounded-lg')
    expect(standard.parentElement).toHaveClass('rounded-xl')
    expect(standard.parentElement).toHaveClass('gap-1')
    expect(standard.parentElement).toHaveClass('p-[3px]')
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

  it('submits dictated description text after speech recognition finishes', async () => {
    vi.stubGlobal('SpeechRecognition', MockSpeechRecognition)

    const bootstrap = makeBootstrapResponse()
    vi.mocked(api.listIssues).mockResolvedValue({
      items: [],
      total: 0,
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

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Document voice control' } })
    fireEvent.change(screen.getByLabelText(/description/i), { target: { value: 'Initial note' } })
    fireEvent.click(screen.getByRole('button', { name: /start speech to text/i }))

    const recognition = MockSpeechRecognition.instances[0]
    await act(async () => {
      recognition.emitResult([{ transcript: ' dictated detail', isFinal: true }])
    })
    await waitFor(() => {
      expect(screen.getByLabelText(/description/i)).toHaveValue('Initial note dictated detail')
    })
    await act(async () => {
      recognition.stop()
    })

    fireEvent.click(screen.getByRole('button', { name: /create issue/i }))

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({
          title: 'Document voice control',
          description: 'Initial note dictated detail',
        }),
        {
          newImages: [],
          removeImageIDs: [],
        },
      )
    })
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
