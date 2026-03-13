import { screen } from '@testing-library/react'
import { vi } from 'vitest'

import { SessionDetailPage } from '@/routes/session-detail'
import { renderWithQueryClient } from '@/test/test-utils'

vi.mock('@tanstack/react-router', () => ({
  Navigate: ({
    params,
    replace,
    to,
  }: {
    params: { identifier: string }
    replace?: boolean
    to: string
  }) => (
    <div
      data-identifier={params.identifier}
      data-replace={replace ? 'true' : 'false'}
      data-testid="session-detail-redirect"
      data-to={to}
    />
  ),
  useParams: () => ({ identifier: 'ISS-1' }),
}))

describe('SessionDetailPage', () => {
  it('redirects legacy session detail URLs to the issue detail page', () => {
    renderWithQueryClient(<SessionDetailPage />)

    expect(screen.getByTestId('session-detail-redirect')).toHaveAttribute('data-to', '/issues/$identifier')
    expect(screen.getByTestId('session-detail-redirect')).toHaveAttribute('data-identifier', 'ISS-1')
    expect(screen.getByTestId('session-detail-redirect')).toHaveAttribute('data-replace', 'true')
  })
})
