import type { QueryClient } from '@tanstack/react-query'

import { appRoutes } from '@/lib/routes'

function queryKeysForPath(pathname: string) {
  if (pathname === appRoutes.work) {
    return [['bootstrap'], ['issues']] as const
  }

  if (pathname === appRoutes.projects) {
    return [['bootstrap'], ['projects'], ['epics']] as const
  }

  if (pathname.startsWith('/projects/')) {
    return [['bootstrap'], ['project']] as const
  }

  if (pathname.startsWith('/epics/')) {
    return [['bootstrap'], ['epic']] as const
  }

  if (pathname.startsWith('/issues/')) {
    return [['bootstrap'], ['issue'], ['issue-execution']] as const
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
