import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'

import { GlobalDashboardProvider } from '@/components/dashboard/global-dashboard-context'
import { TooltipProvider } from '@/components/ui/tooltip'

export function renderWithQueryClient(ui: ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })
  const Providers = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>
      <TooltipProvider delayDuration={0}>
        <GlobalDashboardProvider>{children}</GlobalDashboardProvider>
      </TooltipProvider>
    </QueryClientProvider>
  )
  const rendered = render(<Providers>{ui}</Providers>)

  return {
    queryClient,
    ...rendered,
    rerender: (nextUi: ReactNode) => rendered.rerender(<Providers>{nextUi}</Providers>),
  }
}

export async function selectOption(name: string | RegExp, option: string | RegExp) {
  const combobox = screen.getByRole('combobox', { name })

  await act(async () => {
    fireEvent.pointerDown(combobox, {
      button: 0,
      ctrlKey: false,
      pointerType: 'mouse',
    })
    await Promise.resolve()
  })

  const optionElement = await screen.findByRole('option', { name: option })

  await act(async () => {
    fireEvent.click(optionElement)
    await Promise.resolve()
  })

  await waitFor(() => {
    if (screen.queryByRole('option', { name: option })) {
      throw new Error('Select menu is still open')
    }
  })
}
