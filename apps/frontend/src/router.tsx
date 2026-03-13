import { Suspense, lazy, type ComponentType } from 'react'
import {
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'

import { AppShell } from '@/components/app-shell'
import { Card } from '@/components/ui/card'

function lazyPage<T extends Record<string, ComponentType>>(loader: () => Promise<T>, key: keyof T) {
  const Component = lazy(async () => ({ default: (await loader())[key] as ComponentType }))
  return function LazyComponent() {
    return (
      <Suspense fallback={<Card className="h-[420px] animate-pulse bg-white/5" />}>
        <Component />
      </Suspense>
    )
  }
}

const OverviewPage = lazyPage(() => import('@/routes/overview'), 'OverviewPage')
const WorkPage = lazyPage(() => import('@/routes/work'), 'WorkPage')
const ProjectsPage = lazyPage(() => import('@/routes/projects'), 'ProjectsPage')
const ProjectDetailPage = lazyPage(() => import('@/routes/project-detail'), 'ProjectDetailPage')
const EpicDetailPage = lazyPage(() => import('@/routes/epic-detail'), 'EpicDetailPage')
const IssueDetailPage = lazyPage(() => import('@/routes/issue-detail'), 'IssueDetailPage')
const SessionsPage = lazyPage(() => import('@/routes/sessions'), 'SessionsPage')
const SessionDetailPage = lazyPage(() => import('@/routes/session-detail'), 'SessionDetailPage')

const rootRoute = createRootRoute({
  component: AppShell,
})

const overviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: OverviewPage,
})

const workRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/work',
  component: WorkPage,
})

const projectsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects',
  component: ProjectsPage,
})

const projectDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId',
  component: ProjectDetailPage,
})

const epicDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/epics/$epicId',
  component: EpicDetailPage,
})

const issueDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/issues/$identifier',
  component: IssueDetailPage,
})

const sessionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sessions',
  component: SessionsPage,
})

const sessionDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/sessions/$identifier',
  component: SessionDetailPage,
})

const routeTree = rootRoute.addChildren([
  overviewRoute,
  workRoute,
  projectsRoute,
  projectDetailRoute,
  epicDetailRoute,
  issueDetailRoute,
  sessionsRoute,
  sessionDetailRoute,
])

export const router = createRouter({
  routeTree,
  defaultPreload: 'intent',
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
