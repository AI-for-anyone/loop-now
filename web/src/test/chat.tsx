import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { agentsUrl, apiUrl, context, defaultAgentPath, makeAgent, makeMessagePage, makeStatus, toListItem } from './api'
import { FakeEventSource } from './fake-event-source'
import { renderRoutes } from './render'
import { server } from './server'

interface ChatSetup {
  agent?: Schemas['Agent']
  messages?: Schemas['Message'][]
  hasMore?: boolean
  // Turn counts and similar fields for this page; when not given, treated as "this page is the whole history" (see makeMessagePage).
  page?: Partial<Schemas['MessagePage']>
  status?: Partial<Schemas['AgentStatus']>
  handlers?: HttpHandler[]
  // Opens the chat page path by default; specify another when it needs a locate anchor or the like.
  path?: string
}

// Opens an Agent's chat page and returns the event stream controls and the record of sends.
export async function renderChat(setup: ChatSetup = {}) {
  const agent = setup.agent ?? makeAgent()
  const sent: Schemas['SendMessageInput'][] = []
  server.use(
    ...(setup.handlers ?? []),
    http.get(apiUrl('/context'), () => HttpResponse.json(context)),
    http.get(agentsUrl, () => HttpResponse.json({ agents: [toListItem(agent)] })),
    http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(agent)),
    http.get(apiUrl(`/agents/${agent.id}/status`), () => HttpResponse.json(makeStatus(agent.id, setup.status))),
    http.get(apiUrl(`/agents/${agent.id}/messages`), () => HttpResponse.json(makeMessagePage(setup.messages ?? [], { hasMore: setup.hasMore ?? false, ...setup.page }))),
  )
  const user = userEvent.setup()
  const result = renderRoutes(routes, setup.path ?? defaultAgentPath(agent.id, 'chat'))
  await screen.findByRole('log')
  // The event stream is set up only after the chat page mounts; in full parallel runs it
  // may appear later than the conversation area, so wait until it exists before returning.
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
