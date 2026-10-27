import { act, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { agentsUrl, apiUrl, context, makeAgent, makeMessage, makeRun, platformAgentsUrl, toListItem } from '@/test/api'
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

describe('Agent 列表与新建按作用域进行', () => {
  it('按项目列出、新建其中的 Agent', async () => {
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
    await act(() => result.current.create.mutateAsync({ name: '新助手', model: agent.model }))
    expect(created).toEqual([{ name: '新助手', model: agent.model }])
  })

  it('平台的 Agent 走 /platform/agents', async () => {
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
    await act(() => result.current.create.mutateAsync({ name: '巡检助手', model: agent.model }))
    expect(created).toEqual([{ name: '巡检助手', model: agent.model }])
  })

  it('作用域尚未确定时不请求', () => {
    const { result } = renderHookWithQuery(() => useAgents(null))
    expect(result.current.fetchStatus).toBe('idle')
  })
})

describe('变更后的缓存失效', () => {
  it('修改 Agent 后重新获取 Agent 详情', async () => {
    const agent = makeAgent({ name: '旧名字' })
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
    await waitFor(() => expect(result.current.agent.data?.name).toBe('旧名字'))
    await act(() => result.current.update.mutateAsync({ name: '新名字' }))
    await waitFor(() => expect(result.current.agent.data?.name).toBe('新名字'))
  })
})

describe('凭证不进入查询缓存', () => {
  it('录入模型凭证与 CLI 凭证后，缓存中找不到明文', async () => {
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

describe('历史缓存的更新', () => {
  it('按 ID 插入新消息，并按执行更新用户消息与回复', async () => {
    const agentId = 'a1'
    const run = makeRun({ status: 'running' })
    const user = makeMessage({ run })
    server.use(http.get(apiUrl(`/agents/${agentId}/messages`), () => HttpResponse.json({ messages: [user], hasMore: false })))
    const { result, client } = renderHookWithQuery(() => useMessages(agentId))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const reply = makeMessage({ role: 'assistant', text: '好的', run: { ...run, status: 'completed' } })
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

describe('执行状态不倒退', () => {
  it('结束事件先到、发送响应后到时，保留已结束的状态', async () => {
    const agentId = 'a2'
    const run = makeRun({ status: 'completed' })
    const user = makeMessage({ run })
    server.use(http.get(apiUrl(`/agents/${agentId}/messages`), () => HttpResponse.json({ messages: [user], hasMore: false })))
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
