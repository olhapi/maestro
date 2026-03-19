import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'

import { refreshDashboardQueries } from '@/lib/query-refresh'

describe('refreshDashboardQueries', () => {
  it('refreshes work route queries without touching bootstrap', async () => {
    const queryClient = new QueryClient()
    const invalidateQueries = vi.spyOn(queryClient, 'invalidateQueries')

    await refreshDashboardQueries(queryClient, '/work')

    expect(invalidateQueries).toHaveBeenCalledTimes(2)
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['work-bootstrap'],
      refetchType: 'active',
    })
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['issues'],
      refetchType: 'active',
    })
  })
})
