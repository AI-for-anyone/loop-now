import { screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { apiUrl, defaultAgentsPath, defaultProject, makeAgent, projectsUrl } from '@/test/api'
import { agentParty, consoleParty, emitInbox, makeInboxMessage, openInboxStream, summaryOf } from '@/test/inbox'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { makeTaskDetail, nodeId, taskId } from '@/test/tasks'
import type { DesktopBridge } from './desktop'

// 桌面壳的窗口里：收件箱事件交给它显示 Dock 未读数与系统通知，点通知后由它把地址交回页面。
const agent = makeAgent({ name: '审查员' })
const question = (overrides: Partial<Schemas['InboxMessage']> = {}) =>
  summaryOf(
    makeInboxMessage({
      from: agentParty(agent),
      to: consoleParty,
      text: '要合并这个 PR 吗？',
      question: { options: [], expiresAt: '2026-12-01T00:00:00Z', state: 'open', answer: null },
      ...overrides,
    }),
  )

let bridge: { notify: ReturnType<typeof vi.fn>; setBadge: ReturnType<typeof vi.fn>; onNavigate: ReturnType<typeof vi.fn>; navigate?: (path: string) => void }
let focused = false

beforeEach(() => {
  bridge = { notify: vi.fn(), setBadge: vi.fn(), onNavigate: vi.fn() }
  bridge.onNavigate.mockImplementation((listener: (path: string) => void) => {
    bridge.navigate = listener
    return () => undefined
  })
  window.loopNowDesktop = bridge as unknown as DesktopBridge
  focused = false
  vi.spyOn(document, 'hasFocus').mockImplementation(() => focused)
})

afterEach(() => {
  delete window.loopNowDesktop
  vi.restoreAllMocks()
})

async function start() {
  server.use(...baseHandlers([agent]))
  renderRoutes(routes, defaultAgentsPath)
  await openInboxStream(2)
}

describe('桌面壳的通知与 Dock 未读数', () => {
  it('每个收件箱事件都更新 Dock 未读数', async () => {
    await start()
    expect(bridge.setBadge).toHaveBeenLastCalledWith(2)
    await emitInbox('inbox.message.read', { id: crypto.randomUUID(), threadId: crypto.randomUUID(), to: consoleParty, readAt: '2026-11-03T00:00:00Z', unreadTotal: 1 })
    expect(bridge.setBadge).toHaveBeenLastCalledWith(1)
  })

  it('窗口没有焦点时，Agent 发给你的新问题发一条"待你回复"，点开去它的对话；同一条只通知一次', async () => {
    await start()
    const message = question()
    await emitInbox('inbox.message.created', { message, unreadTotal: 3 })
    await emitInbox('inbox.message.created', { message, unreadTotal: 3 })
    expect(bridge.notify).toHaveBeenCalledTimes(1)
    expect(bridge.notify).toHaveBeenCalledWith({ id: message.id, title: '待你回复：审查员', body: '要合并这个 PR 吗？', path: `/agents/${agent.id}/chat` })
    expect(bridge.setBadge).toHaveBeenLastCalledWith(3)
  })

  it('任务实例发给你的消息发一条"任务等待"，点开去任务里这条消息所属的节点', async () => {
    const detail = makeTaskDetail(defaultProject.id, defaultProject.name)
    server.use(http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)), http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })))
    await start()
    const message = summaryOf(makeInboxMessage({ from: agentParty({ ...agent, name: '任务检查员' }), to: consoleParty, text: '检查完了，等你确认', task: { taskId, nodeRunId: nodeId, runtimeId: crypto.randomUUID() } }))
    await emitInbox('inbox.message.created', { message, unreadTotal: 3 })
    await waitFor(() => expect(bridge.notify).toHaveBeenCalledTimes(1))
    expect(bridge.notify).toHaveBeenCalledWith({
      id: message.id,
      title: '任务等待：通用任务验收',
      body: '任务检查员：检查完了，等你确认',
      path: `/p/${encodeURIComponent(defaultProject.name)}/tasks/${taskId}?record=${nodeId}`,
    })
  })

  it('任务实例提的问题也是"待你回复"，正文带任务名称', async () => {
    const detail = makeTaskDetail(defaultProject.id, defaultProject.name)
    server.use(http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)), http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })))
    await start()
    const message = question({ from: agentParty({ ...agent, name: '任务检查员' }), task: { taskId, nodeRunId: nodeId, runtimeId: crypto.randomUUID() } })
    await emitInbox('inbox.message.created', { message, unreadTotal: 3 })
    await waitFor(() => expect(bridge.notify).toHaveBeenCalledTimes(1))
    expect(bridge.notify.mock.calls[0][0]).toMatchObject({ title: '待你回复：任务检查员', body: '任务「通用任务验收」：要合并这个 PR 吗？' })
  })

  it('不通知：窗口可见且有焦点时、Agent 发给你的普通消息、已不是 open 的问题、发给 Agent 的消息', async () => {
    await start()
    focused = true
    await emitInbox('inbox.message.created', { message: question(), unreadTotal: 3 })
    focused = false
    await emitInbox('inbox.message.created', { message: summaryOf(makeInboxMessage({ from: agentParty(agent), to: consoleParty, text: '做完了' })), unreadTotal: 3 })
    await emitInbox('inbox.message.created', { message: question({ question: { options: [], expiresAt: '2026-12-01T00:00:00Z', state: 'answered', answer: null } }), unreadTotal: 3 })
    await emitInbox('inbox.message.created', { message: summaryOf(makeInboxMessage({ from: consoleParty, to: agentParty(agent), text: '继续' })), unreadTotal: 3 })
    expect(bridge.notify).not.toHaveBeenCalled()
  })

  it('点了通知：桌面壳把地址交回页面，控制台跳过去', async () => {
    server.use(...baseHandlers([agent]))
    const { router } = renderRoutes(routes, defaultAgentsPath)
    await openInboxStream(0)
    expect(bridge.onNavigate).toHaveBeenCalled()
    bridge.navigate?.('/settings')
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings'))
    expect(await screen.findByRole('heading', { level: 1 })).toBeInTheDocument()
  })
})
