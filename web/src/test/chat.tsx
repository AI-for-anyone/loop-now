import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { agentsUrl, apiUrl, context, defaultAgentPath, makeAgent, makeStatus, toListItem } from './api'
import { FakeEventSource } from './fake-event-source'
import { renderRoutes } from './render'
import { server } from './server'

interface ChatSetup {
  agent?: Schemas['Agent']
  messages?: Schemas['Message'][]
  hasMore?: boolean
  status?: Partial<Schemas['AgentStatus']>
  handlers?: HttpHandler[]
  // 默认打开对话页地址；需要带定位锚点等时另行指定。
  path?: string
}

// 打开某个 Agent 的对话页，返回事件流控制与发送记录。
export async function renderChat(setup: ChatSetup = {}) {
  const agent = setup.agent ?? makeAgent()
  const sent: Schemas['SendMessageInput'][] = []
  server.use(
    ...(setup.handlers ?? []),
    http.get(apiUrl('/context'), () => HttpResponse.json(context)),
    http.get(agentsUrl, () => HttpResponse.json({ agents: [toListItem(agent)] })),
    http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(agent)),
    http.get(apiUrl(`/agents/${agent.id}/status`), () => HttpResponse.json(makeStatus(agent.id, setup.status))),
    http.get(apiUrl(`/agents/${agent.id}/messages`), () => HttpResponse.json({ messages: setup.messages ?? [], hasMore: setup.hasMore ?? false })),
  )
  const user = userEvent.setup()
  const result = renderRoutes(routes, setup.path ?? defaultAgentPath(agent.id, 'chat'))
  await screen.findByRole('log')
  // 事件流在对话页挂载后才建立；全量并行运行时可能晚于对话区出现，等它建立后再返回。
  const source = await waitFor(() => {
    const found = FakeEventSource.instances.find((s) => s.url.endsWith(`/agents/${agent.id}/events`))
    if (!found) throw new Error('对话事件流还没有建立')
    return found
  })
  const emit = async (type: string, data: unknown, id?: string) => {
    await act(async () => {
      source.emit(type, data, id ?? nextId())
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
  }
  return { ...result, agent, user, source, emit, sent }
}

let seq = 1000
export const nextId = () => `${++seq}-0`
