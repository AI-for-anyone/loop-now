// Actual TaskDetail, no built-in API replies. The isolated browser must install
// context-wide HTTP interception before opening this entry; never use live API.
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ScopeContext } from '@/features/projects/scope-context'
import { TaskDetail } from '@/features/tasks/TaskDetail'
import { defaultProject } from './api'
import '../index.css'

export function TaskSwitchHTTPFixture() {
  return <div className="h-screen min-h-0 bg-paper text-ink"><ScopeContext.Provider value={{ kind: 'project', project: defaultProject }}><Routes><Route path="/__lark-switch-http/:taskId" element={<TaskDetail />} /></Routes></ScopeContext.Provider></div>
}
const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
createRoot(document.getElementById('root')!).render(<BrowserRouter><QueryClientProvider client={client}><TaskSwitchHTTPFixture /></QueryClientProvider></BrowserRouter>)
