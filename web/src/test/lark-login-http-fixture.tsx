// Actual product credential components. Browser harness must intercept every
// API/issuer request before creating the page; no built-in simulated replies.
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ProjectCredentialSettings } from '@/features/project-credentials/ProjectAccounts'
import '../index.css'

export function LarkHTTPFixture() {
  return <main className="mx-auto grid max-w-5xl min-w-0 gap-5 p-4 text-ink">
    <h1 className="text-2xl font-bold">项目账号 · 实际React正式HTTP fixture</h1>
    <p className="rounded-card bg-tint-yellow p-3">隔离浏览器必须完整拦截API与授权页。此入口只挂载产品组件，不内置后端响应，不执行真实登录或任务操作。</p>
    <ProjectCredentialSettings projectId="00000000-0000-7000-8000-000000000001" />
  </main>
}
const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
createRoot(document.getElementById('root')!).render(<BrowserRouter><QueryClientProvider client={client}><LarkHTTPFixture /></QueryClientProvider></BrowserRouter>)
