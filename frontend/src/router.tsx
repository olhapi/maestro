import {
  Outlet,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'

import { AppShell } from '@/components/app-shell'
import { IssueDetailPage } from '@/routes/issue-detail'
import { OverviewPage } from '@/routes/overview'
import { ProjectsPage } from '@/routes/projects'
import { SessionsPage } from '@/routes/sessions'
import { WorkPage } from '@/routes/work'

const rootRoute = createRootRoute({
  component: () => <Outlet />,
})

const shellRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/dashboard',
  component: AppShell,
})

const overviewRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/',
  component: OverviewPage,
})

const workRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/work',
  component: WorkPage,
})

const projectsRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/projects',
  component: ProjectsPage,
})

const issueDetailRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/issues/$identifier',
  component: IssueDetailPage,
})

const sessionsRoute = createRoute({
  getParentRoute: () => shellRoute,
  path: '/sessions',
  component: SessionsPage,
})

const routeTree = rootRoute.addChildren([
  shellRoute.addChildren([overviewRoute, workRoute, projectsRoute, issueDetailRoute, sessionsRoute]),
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
