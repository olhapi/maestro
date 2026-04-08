import type { QueryClient } from '@tanstack/react-query'

import { appRoutes } from '@/lib/routes'

export const workDashboardRefreshCoalesceMs = 750

export function dashboardRefreshCoalesceMs(pathname: string) {
  return pathname === appRoutes.work ? workDashboardRefreshCoalesceMs : 0
}

function pathnameParam(pathname: string, prefix: string) {
  const suffix = pathname.slice(prefix.length)
  return suffix.split('/')[0] ?? ''
}

function queryKeysForPath(pathname: string) {
  if (pathname === appRoutes.work) {
    return [['work-bootstrap'], ['issues']] as const
  }

  if (pathname === appRoutes.projects) {
    return [['bootstrap'], ['projects'], ['epics']] as const
  }

  if (pathname.startsWith('/projects/')) {
    if (pathname.includes('/automations')) {
      const projectId = pathnameParam(pathname, '/projects/')
      return [['bootstrap'], ['project', projectId], ['project-automations', projectId]] as const
    }
    return [['bootstrap'], ['project', pathnameParam(pathname, '/projects/')]] as const
  }

  if (pathname.startsWith('/epics/')) {
    return [['bootstrap'], ['epic', pathnameParam(pathname, '/epics/')]] as const
  }

  if (pathname.startsWith('/issues/')) {
    const identifier = pathnameParam(pathname, '/issues/')
    return [['bootstrap'], ['issue', identifier], ['issue-execution', identifier]] as const
  }

  if (pathname === appRoutes.sessions || pathname.startsWith('/sessions/')) {
    return [['bootstrap'], ['sessions'], ['runtime-events']] as const
  }

  return [['bootstrap']] as const
}

export async function refreshDashboardQueries(queryClient: QueryClient, pathname: string) {
  await Promise.all(
    queryKeysForPath(pathname).map((queryKey) =>
      queryClient.invalidateQueries({
        queryKey,
        refetchType: 'active',
      }),
    ),
  )
}
