import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { applyTokens } from './theme/tokens';
import { App } from './App';
import { callMedia, useCalls } from './store/calls';
import './styles/global.css';

applyTokens();
// Dev-only diagnostics hook (never shipped in the production build).
if (import.meta.env.DEV) (window as unknown as Record<string, unknown>).__exim = { callMedia, useCalls };
const queryClient = new QueryClient({ defaultOptions: { queries: { refetchOnWindowFocus: false, retry: 1 } } });

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
