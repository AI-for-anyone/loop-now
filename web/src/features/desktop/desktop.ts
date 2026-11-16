import { useEffect } from 'react'
import { useNavigate } from 'react-router'
import type { QueryClient } from '@tanstack/react-query'
import { api, call, type Schemas } from '@/api/client'
import type { InboxEvent } from '@/api/events'
import { keys } from '@/api/queries'
import { tasksPath } from '@/api/tasks'
import { partyName } from '@/features/inbox/participants'
import { t } from '@/i18n'

// The interface the desktop shell (desktop/src/preload.ts) provides in its windows; absent when the console is opened in a browser.
export type DesktopNotification = { id: string; title: string; body: string; path: string }
export type DesktopBridge = {
  notify: (notification: DesktopNotification) => void
  setBadge: (count: number) => void
  onNavigate: (listener: (path: string) => void) => () => void
}

declare global {
  interface Window {
    loopNowDesktop?: DesktopBridge
  }
}

type Message = Schemas['InboxMessageSummary']

// Messages worth a system notification: new questions sent to you that are still open (waiting for your reply), and other new
// messages a task instance sent you (task waiting).
export function notificationKind(message: Message): 'question' | 'task' | null {
  if (message.to.kind !== 'console' || message.cancelledAt !== null) return null
  if (message.question?.state === 'open') return 'question'
  return message.task ? 'task' : null
}

// Messages already notified about: they may be replayed after the event stream reconnects. Only this many recent ones are kept.
const notified = new Set<string>()
const notifiedLimit = 500

// Where a message outside a task leads when clicked, the same as the links in “待你处理” (Needs you): the Agent's chat, the
// external identity's page, and the overview for everything else.
function senderPath(message: Message): string {
  if (message.from.kind === 'agent' && message.from.id) return `/agents/${message.from.id}/chat`
  if (message.from.kind === 'external' && message.from.id) return `/identities/${message.from.id}`
  return '/'
}

// A message in a task: the task name and the URL of the node this message belongs to; when the task or project can't be
// loaded, use a generic name and go back to the overview.
async function taskTarget(client: QueryClient, task: Schemas['InboxTaskRef']): Promise<{ title: string; path: string }> {
  try {
    const detail = await call(api.GET('/tasks/{taskId}', { params: { path: { taskId: task.taskId } } }))
    const projects = await client.fetchQuery({ queryKey: keys.projects, queryFn: () => call(api.GET('/projects')) })
    const project = projects.projects.find((p) => p.id === detail.task.projectId)
    const record = task.nodeRunId ? `?${new URLSearchParams({ record: task.nodeRunId })}` : ''
    return { title: detail.task.title, path: project ? `${tasksPath(project.name)}/${task.taskId}${record}` : '/' }
  } catch {
    return { title: t.consoleInbox.taskFallback, path: '/' }
  }
}

// Pass inbox events to the desktop shell: the Dock shows the unread count; when the window is hidden or unfocused, a new
// message worth notifying about gets a system notification, and clicking it goes back to its chat or task node.
export async function forwardToDesktop(client: QueryClient, event: InboxEvent): Promise<void> {
  const bridge = window.loopNowDesktop
  if (!bridge) return
  bridge.setBadge(event.data.unreadTotal)
  if (event.type !== 'inbox.message.created') return
  const { message } = event.data
  const kind = notificationKind(message)
  if (!kind || notified.has(message.id)) return
  if (document.visibilityState === 'visible' && document.hasFocus()) return
  notified.add(message.id)
  if (notified.size > notifiedLimit) notified.delete(notified.values().next().value as string)
  const text = t.consoleInbox.desktop
  if (!message.task) {
    bridge.notify({ id: message.id, title: text.question(partyName(message.from)), body: message.preview, path: senderPath(message) })
    return
  }
  const task = await taskTarget(client, message.task)
  bridge.notify(
    kind === 'question'
      ? { id: message.id, title: text.question(partyName(message.from)), body: text.inTask(task.title, message.preview), path: task.path }
      : { id: message.id, title: text.task(task.title), body: text.from(partyName(message.from), message.preview), path: task.path },
  )
}

// In a desktop shell window: the window has no system title bar (macOS hiddenInset), the page extends to the top of the window
// with the system traffic-light buttons in the top-left corner, and the top of the page must leave a strip for dragging the
// window (window-drag in index.css). Not affected when the console is opened in a browser.
export const inDesktopWindow = () => window.loopNowDesktop !== undefined

// A system notification was clicked: after showing the window, the desktop shell hands the path to the page, which navigates
// within the console.
export function useDesktopNavigation() {
  const navigate = useNavigate()
  useEffect(() => window.loopNowDesktop?.onNavigate((path) => void navigate(path)), [navigate])
}
