import { QueryClient } from '@tanstack/react-query'

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // The console only talks to the local server, so requests are not paused when the browser decides it is offline from the internet.
        networkMode: 'always',
        // Do not retry when the server is unreachable, the session is unauthenticated, or the resource does not exist; show the reason right away.
        retry: (count, error) => count < 2 && !('status' in error && (error.status === 0 || error.status === 401 || error.status === 404)),
      },
      mutations: { networkMode: 'always' },
    },
  })
}
