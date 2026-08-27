import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { createRoot } from 'react-dom/client'
import { queryClient } from './query'
import { router } from './router'
import { ConfirmProvider } from './components/ui'
import './styles.css'

createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={queryClient}>
    <ConfirmProvider>
      <RouterProvider router={router} />
    </ConfirmProvider>
  </QueryClientProvider>,
)
