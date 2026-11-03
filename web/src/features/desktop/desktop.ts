import { useEffect } from 'react'
import { useNavigate } from 'react-router'
import type { QueryClient } from '@tanstack/react-query'
import { api, call, type Schemas } from '@/api/client'
import type { InboxEvent } from '@/api/events'
import { keys } from '@/api/queries'
import { tasksPath } from '@/api/tasks'
import { t } from '@/i18n'

// 桌面壳（desktop/src/preload.ts）在它的窗口里提供的接口；在浏览器里打开控制台时没有。
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

// 值得发系统通知的消息：发给你、仍为 open 的新问题（待你回复），以及任务实例发给你的其他新消息（任务等待）。
export function notificationKind(message: Message): 'question' | 'task' | null {
  if (message.to.kind !== 'console' || message.cancelledAt !== null) return null
  if (message.question?.state === 'open') return 'question'
  return message.task ? 'task' : null
}

// 已经通知过的消息：事件流重连后可能重放。只记最近的这么多条。
const notified = new Set<string>()
const notifiedLimit = 500

// 不在任务里的消息点开后去的地方，与“待你处理”里的链接相同：Agent 的对话、外部身份的页面，其余回到概览。
function senderPath(message: Message): string {
  if (message.from.kind === 'agent' && message.from.id) return `/agents/${message.from.id}/chat`
  if (message.from.kind === 'external' && message.from.id) return `/identities/${message.from.id}`
  return '/'
}

// 任务里的消息：任务名称与这条消息所属节点的地址；读不到任务或项目时用通用名称、回到概览。
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

// 收件箱事件交给桌面壳：Dock 上显示未读数；窗口不可见或没有焦点时，值得通知的新消息发一条系统通知，点开回到它所在的对话或任务节点。
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
    bridge.notify({ id: message.id, title: text.question(message.from.name), body: message.preview, path: senderPath(message) })
    return
  }
  const task = await taskTarget(client, message.task)
  bridge.notify(
    kind === 'question'
      ? { id: message.id, title: text.question(message.from.name), body: text.inTask(task.title, message.preview), path: task.path }
      : { id: message.id, title: text.task(task.title), body: text.from(message.from.name, message.preview), path: task.path },
  )
}

// 点了系统通知：桌面壳显示窗口后把地址交给页面，在控制台里跳转。
export function useDesktopNavigation() {
  const navigate = useNavigate()
  useEffect(() => window.loopNowDesktop?.onNavigate((path) => void navigate(path)), [navigate])
}
