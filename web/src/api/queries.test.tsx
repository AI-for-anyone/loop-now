import { act, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { agentsUrl, apiUrl, context, makeAgent, makeMessage, makeMessagePage, makeRun, platformAgentsUrl, toListItem } from '@/test/api'
import { PLATFORM } from '@/lib/scope'
import { renderHookWithQuery } from '@/test/render'
import { server } from '@/test/server'
import {
  keys,
  putCliCredential,
  putModelCredential,
  upsertMessage,
  updateRunInMessages,
  useAgent,
  useAgents,
  useCreateAgent,
  useMessages,
  useUpdateAgent,
  type MessagePages,
} from './queries'

describe('Agent list and creation go by scope', () => {
  it('lists the Agents of a project and creates one in it', async () => {
    const agent = makeAgent()
    const created: unknown[] = []
    server.use(
      http.get(agentsUrl, () => HttpResponse.json({ agents: [toListItem(agent)] })),
      http.post(agentsUrl, async ({ request }) => {
        created.push(await request.json())
        return HttpResponse.json(agent, { status: 201 })
      }),
    )
    const { result } = renderHookWithQuery(() => ({ agents: useAgents(context.project.id), create: useCreateAgent(context.project.id) }))
    await waitFor(() => expect(result.current.agents.data?.agents.map((a) => a.id)).toEqual([agent.id]))
    await act(() => result.current.create.mutateAsync({ name: 'New Assistant', model: agent.model }))
    expect(created).toEqual([{ name: 'New Assistant', model: agent.model }])
  })

  it('platform Agents go through /platform/agents', async () => {
    const agent = makeAgent({ scope: 'platform', projectId: null })
    const created: unknown[] = []
    server.use(
      http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(agent)] })),
      http.post(platformAgentsUrl, async ({ request }) => {
        created.push(await request.json())
        return HttpResponse.json(agent, { status: 201 })
      }),
    )
    const { result } = renderHookWithQuery(() => ({ agents: useAgents(PLATFORM), create: useCreateAgent(PLATFORM) }))
    await waitFor(() => expect(result.current.agents.data?.agents.map((a) => a.id)).toEqual([agent.id]))
    await act(() => result.current.create.mutateAsync({ name: 'Inspector', model: agent.model }))
    expect(created).toEqual([{ name: 'Inspector', model: agent.model }])
  })

  it('does not request while the scope is not known yet', () => {
    const { result } = renderHookWithQuery(() => useAgents(null))
    expect(result.current.fetchStatus).toBe('idle')
  })
})

describe('cache invalidation after changes', () => {
  it('refetches the Agent details after editing the Agent', async () => {
    const agent = makeAgent({ name: 'Old name' })
    let current = agent
    server.use(
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
      http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
        current = { ...current, ...((await request.json()) as object) }
        return HttpResponse.json(current)
      }),
      http.get(agentsUrl, () => HttpResponse.json({ agents: [] })),
    )
    const { result } = renderHookWithQuery(() => ({ agent: useAgent(agent.id), update: useUpdateAgent(agent.id) }))
    await waitFor(() => expect(result.current.agent.data?.name).toBe('Old name'))
    await act(() => result.current.update.mutateAsync({ name: 'New name' }))
    await waitFor(() => expect(result.current.agent.data?.name).toBe('New name'))
  })
})

describe('credentials stay out of the query cache', () => {
  it('after entering a model credential and a CLI credential, the cache holds no plain text', async () => {
    const agent = makeAgent()
    const meta = { status: 'set', updatedAt: '2026-10-12T00:00:00Z', fieldNames: [], envNames: ['ANTHROPIC_API_KEY'], filePaths: [], warnings: [] }
    server.use(
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(agent)),
      http.get(agentsUrl, () => HttpResponse.json({ agents: [] })),
      http.put(apiUrl(`/agents/${agent.id}/model-credential`), () => HttpResponse.json(meta)),
      http.put(apiUrl(`/agents/${agent.id}/clis/gh/credential`), () => HttpResponse.json(meta)),
    )
    const { result, client } = renderHookWithQuery(() => useAgent(agent.id))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    await putModelCredential(agent.id, { provider: 'anthropic', key: 'sk-secret-model' })
    await putCliCredential(agent.id, 'gh', { token: 'ghp_secret_token' })
    const cache = JSON.stringify([
      client.getQueryCache().getAll().map((q) => q.state),
      client.getMutationCache().getAll().map((m) => m.state),
    ])
    expect(cache).not.toContain('sk-secret-model')
    expect(cache).not.toContain('ghp_secret_token')
  })
})

describe('history cache updates', () => {
  it('inserts new messages by ID and updates the user message and the reply by run', async () => {
    const agentId = 'a1'
    const run = makeRun({ status: 'running' })
    const user = makeMessage({ run })
    server.use(http.get(apiUrl(`/agents/${agentId}/messages`), () => HttpResponse.json(makeMessagePage([user]))))
    const { result, client } = renderHookWithQuery(() => useMessages(agentId))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const reply = makeMessage({ role: 'assistant', text: 'OK', run: { ...run, status: 'completed' } })
    act(() => {
      upsertMessage(client, agentId, reply)
      upsertMessage(client, agentId, reply)
      updateRunInMessages(client, agentId, { ...run, status: 'completed' })
    })
    const data = client.getQueryData<MessagePages>(keys.messages(agentId))!
    expect(data.pages[0].messages.map((m) => [m.role, m.run.status])).toEqual([
      ['user', 'completed'],
      ['assistant', 'completed'],
    ])
  })
})

describe('run status does not go backwards', () => {
  it('keeps the ended status when the end event arrives before the send response', async () => {
    const agentId = 'a2'
    const run = makeRun({ status: 'completed' })
    const user = makeMessage({ run })
    server.use(http.get(apiUrl(`/agents/${agentId}/messages`), () => HttpResponse.json(makeMessagePage([user]))))
    const { result, client } = renderHookWithQuery(() => useMessages(agentId))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    act(() => {
      upsertMessage(client, agentId, { ...user, run: { ...run, status: 'starting', endedAt: null } })
      updateRunInMessages(client, agentId, { ...run, status: 'running' })
    })
    const data = client.getQueryData<MessagePages>(keys.messages(agentId))!
    expect(data.pages[0].messages[0].run.status).toBe('completed')
  })
})
