import { Suspense, lazy, useMemo, useState, type ComponentType } from 'react'
import {
  createRootRoute,
  createRoute,
  createRouter,
  stripSearchParams,
  type RouterHistory,
  useRouterState,
} from '@tanstack/react-router'

import { AppShell } from '@/components/app-shell'
import { ComponentErrorBoundary } from '@/components/ui/component-error-boundary'
import { Card } from '@/components/ui/card'
import { workSearchDefaults, workSearchSchema } from '@/lib/work-url-state'

function lazyPage<T extends Record<string, ComponentType>>(loader: () => Promise<T>, key: keyof T, label: string) {
  return function LazyComponent() {
    const [retryNonce, setRetryNonce] = useState(0)
    const pathname = useRouterState({
      select: (state) => state.location.pathname,
    })
    const componentVersion = `${pathname}:${retryNonce}`
    const Component = useMemo(
      () => {
        void componentVersion
        return lazy(async () => ({ default: (await loader())[key] as ComponentType }))
      },
      [componentVersion],
    )

    return (
      <ComponentErrorBoundary
        className="min-h-[420px]"
        label={label}
        onRecover={() => setRetryNonce((current) => current + 1)}
        resetKeys={[pathname]}
        scope="page"
      >
        <Suspense fallback={<Card className="h-[420px] animate-pulse bg-white/5" />}>
          <Component />
        </Suspense>
      </ComponentErrorBoundary>
    )
  }
}

const OverviewPage = lazyPage(() => import('@/routes/overview'), 'OverviewPage', 'overview page')
const WorkPage = lazyPage(() => import('@/routes/work'), 'WorkPage', 'work page')
const ProjectsPage = lazyPage(() => import('@/routes/projects'), 'ProjectsPage', 'projects page')
const ProjectDetailPage = lazyPage(() => import('@/routes/project-detail'), 'ProjectDetailPage', 'project page')
const ProjectAutomationsPage = lazyPage(() => import('@/routes/project-automations'), 'ProjectAutomationsPage', 'project automations page')
const EpicDetailPage = lazyPage(() => import('@/routes/epic-detail'), 'EpicDetailPage', 'epic page')
const IssueDetailPage = lazyPage(() => import('@/routes/issue-detail'), 'IssueDetailPage', 'issue page')
const SessionsPage = lazyPage(() => import('@/routes/sessions'), 'SessionsPage', 'sessions page')
const SessionDetailPage = lazyPage(() => import('@/routes/session-detail'), 'SessionDetailPage', 'session page')

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
  validateSearch: workSearchSchema,
  search: {
    middlewares: [stripSearchParams(workSearchDefaults)],
  },
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

const projectAutomationsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId/automations',
  component: ProjectAutomationsPage,
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
  projectAutomationsRoute,
  epicDetailRoute,
  issueDetailRoute,
  sessionsRoute,
  sessionDetailRoute,
])

export function createAppRouter(history?: RouterHistory) {
  return createRouter({
    routeTree,
    defaultPreload: 'intent',
    ...(history ? { history } : {}),
  })
}

export const router = createAppRouter()

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
