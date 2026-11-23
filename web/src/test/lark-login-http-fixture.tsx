// Actual product credential components. Browser harness must intercept every
// API/issuer request before creating the page; no built-in simulated replies.
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ProjectCredentialSettings } from '@/features/project-credentials/ProjectAccounts'
import '../index.css'

export function LarkHTTPFixture() {
  return <main className="mx-auto grid max-w-5xl min-w-0 gap-5 p-4 text-ink">
    <h1 className="text-2xl font-bold">Project accounts · the real React components over the formal HTTP fixture</h1>
    <p className="rounded-card bg-tint-yellow p-3">The isolated browser must intercept the API and the authorization page completely. This entry only mounts the product components: it has no built-in backend responses and performs no real login or task operations.</p>
    <ProjectCredentialSettings projectId="00000000-0000-7000-8000-000000000001" />
  </main>
}
const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
createRoot(document.getElementById('root')!).render(<BrowserRouter><QueryClientProvider client={client}><LarkHTTPFixture /></QueryClientProvider></BrowserRouter>)
