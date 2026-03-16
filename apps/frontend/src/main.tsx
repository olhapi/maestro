import React from 'react'
import ReactDOM from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { Toaster } from 'sonner'

import { GlobalDashboardProvider } from '@/components/dashboard/global-dashboard-context'
import { TooltipProvider } from '@/components/ui/tooltip'
import { router } from '@/router'
import '@/index.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      staleTime: 5_000,
    },
  },
})

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <TooltipProvider delayDuration={120}>
        <GlobalDashboardProvider>
          <RouterProvider router={router} />
          <Toaster richColors position="top-right" />
        </GlobalDashboardProvider>
      </TooltipProvider>
    </QueryClientProvider>
  </React.StrictMode>,
)
