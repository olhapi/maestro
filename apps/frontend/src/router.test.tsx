import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

let shouldThrowOverviewPage = false

vi.mock('@/components/app-shell', async () => {
  const { Outlet } = await vi.importActual<typeof import('@tanstack/react-router')>('@tanstack/react-router')

  return {
    AppShell: () => (
      <div>
        <div>Shell chrome</div>
        <Outlet />
      </div>
    ),
  }
})

vi.mock('@/routes/overview', () => {
  return {
    OverviewPage: () => {
      if (shouldThrowOverviewPage) {
        throw new Error('overview page crashed')
      }

      return <div>Overview page recovered</div>
    },
  }
})

vi.mock('@/routes/work', () => ({
  WorkPage: () => <div>Work page</div>,
}))

vi.mock('@/routes/projects', () => ({
  ProjectsPage: () => <div>Projects page</div>,
}))

vi.mock('@/routes/project-detail', () => ({
  ProjectDetailPage: () => <div>Project detail page</div>,
}))

vi.mock('@/routes/epic-detail', () => ({
  EpicDetailPage: () => <div>Epic detail page</div>,
}))

vi.mock('@/routes/issue-detail', () => ({
  IssueDetailPage: () => <div>Issue detail page</div>,
}))

vi.mock('@/routes/sessions', () => ({
  SessionsPage: () => <div>Sessions page</div>,
}))

vi.mock('@/routes/session-detail', () => ({
  SessionDetailPage: () => <div>Session detail page</div>,
}))

describe('router page error boundaries', () => {
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    shouldThrowOverviewPage = false
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    window.history.pushState({}, '', '/')
  })

  afterEach(() => {
    consoleErrorSpy.mockRestore()
  })

  it('keeps the shell visible when a route page crashes and recovers on reload', async () => {
    shouldThrowOverviewPage = true
    vi.resetModules()

    const { RouterProvider } = await import('@tanstack/react-router')
    const { router } = await import('@/router')

    await act(async () => {
      await router.load()
    })

    render(<RouterProvider router={router} />)

    const reloadButton = await screen.findByRole('button', { name: /reload overview page/i })
    expect(screen.getByText('Shell chrome')).toBeInTheDocument()

    shouldThrowOverviewPage = false
    fireEvent.click(reloadButton)

    expect(await screen.findByText('Overview page recovered')).toBeInTheDocument()
    expect(screen.getByText('Shell chrome')).toBeInTheDocument()
  })

  it('retries a rejected lazy route import when reload is pressed', async () => {
    vi.resetModules()
    let loadAttempts = 0
    vi.doMock('@/routes/overview', () => {
      if (loadAttempts === 0) {
        loadAttempts += 1
        throw new Error('failed to load overview chunk')
      }

      return {
        OverviewPage: () => <div>Overview page recovered</div>,
      }
    })

    const { RouterProvider } = await import('@tanstack/react-router')
    const { router } = await import('@/router')

    await act(async () => {
      await router.load().catch(() => {})
    })

    render(<RouterProvider router={router} />)

    const reloadButton = await screen.findByRole('button', { name: /reload overview page/i })
    expect(screen.getByText('Shell chrome')).toBeInTheDocument()

    fireEvent.click(reloadButton)

    expect(await screen.findByText('Overview page recovered')).toBeInTheDocument()
  })
})
