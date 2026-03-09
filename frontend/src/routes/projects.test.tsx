import type { ReactNode } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

import { ProjectsPage } from '@/routes/projects'
import { makeBootstrapResponse } from '@/test/fixtures'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
}))

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

vi.mock('@/lib/api', () => ({
  api: {
    bootstrap: vi.fn(),
    listProjects: vi.fn(),
    listEpics: vi.fn(),
    deleteProject: vi.fn(),
    deleteEpic: vi.fn(),
    createProject: vi.fn(),
    updateProject: vi.fn(),
    createEpic: vi.fn(),
    createIssue: vi.fn(),
  },
}))

const { api } = await import('@/lib/api')

describe('ProjectsPage', () => {
  it('marks out-of-scope projects as not runnable', async () => {
    const base = makeBootstrapResponse()
    const bootstrap = makeBootstrapResponse({
      overview: {
        ...base.overview,
        status: {
          active_runs: 0,
          scoped_repo_path: '/repo/current',
        },
      },
      projects: [
        {
          ...base.projects[0],
          repo_path: '/repo/other',
          dispatch_ready: false,
          dispatch_error: 'Project repo is outside the current server scope (/repo/current)',
        },
      ],
    })

    vi.mocked(api.bootstrap).mockResolvedValue(bootstrap)
    vi.mocked(api.listProjects).mockResolvedValue({ items: bootstrap.projects })
    vi.mocked(api.listEpics).mockResolvedValue({ items: bootstrap.epics })

    renderWithQueryClient(<ProjectsPage />)

    await waitFor(() => {
      expect(screen.getByText('Out of scope')).toBeInTheDocument()
    })

    expect(screen.getByText('Project repo is outside the current server scope (/repo/current)')).toBeInTheDocument()
  })
})
