import type { QueryClient } from '@tanstack/react-query'

import { appRoutes } from '@/lib/routes'

function pathnameParam(pathname: string, prefix: string) {
  const suffix = pathname.slice(prefix.length)
  return suffix.split('/')[0] ?? ''
}

function queryKeysForPath(pathname: string) {
  if (pathname === appRoutes.work) {
    return [['bootstrap'], ['issues']] as const
  }

  if (pathname === appRoutes.projects) {
    return [['bootstrap'], ['projects'], ['epics']] as const
  }

  if (pathname.startsWith('/projects/')) {
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
