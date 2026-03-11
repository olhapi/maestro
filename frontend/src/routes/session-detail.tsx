import { Navigate, useParams } from '@tanstack/react-router'

import { appRoutes } from '@/lib/routes'

export function SessionDetailPage() {
  const { identifier } = useParams({ from: '/sessions/$identifier' })

  return <Navigate to={appRoutes.issueDetail} params={{ identifier }} replace />
}
