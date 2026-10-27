import { http, HttpResponse } from 'msw'
import type { Schemas } from '@/api/client'
import { apiUrl, context, defaultProject, errorBody, makeAgent, makeStatus, toListItem } from './api'
import { catalogHandlers } from './config'

// 项目层的假服务端：在内存中保存项目与 Agent，按契约响应项目、按作用域列出与新建 Agent、Manager 与删除。
// 测试直接修改 state 来模拟“在别处”发生的改名、删除等变化。
export function projectBackend(init: { projects?: Schemas['Project'][]; agents?: Schemas['Agent'][] } = {}) {
  const state = {
    projects: init.projects ?? [defaultProject],
    agents: init.agents ?? [],
    // 不空闲的 Agent：删除项目时以 409 agent_not_idle 拒绝。
    busy: new Set<string>(),
    requests: [] as { method: string; path: string; body?: unknown }[],
  }
  const withCounts = (project: Schemas['Project']): Schemas['Project'] => ({
    ...project,
    agentCount: state.agents.filter((a) => a.projectId === project.id).length,
    managerAgentId: project.managerAgentId,
  })
  const agentOut = (agent: Schemas['Agent']): Schemas['Agent'] => ({
    ...agent,
    isManager: state.projects.some((p) => p.managerAgentId === agent.id),
  })
  const record = async (request: Request) => {
    const body = request.method === 'GET' || request.method === 'DELETE' ? undefined : await request.clone().json().catch(() => undefined)
    state.requests.push({ method: request.method, path: new URL(request.url).pathname, body })
  }
  const notFound = () => HttpResponse.json(errorBody('not_found'), { status: 404 })
  const create = (scope: Schemas['AgentScope'], projectId: string | null, body: Schemas['AgentCreate']) => {
    if (state.agents.some((a) => a.scope === scope && a.projectId === projectId && a.name === body.name))
      return HttpResponse.json(errorBody('name_conflict'), { status: 409 })
    const agent = makeAgent({ name: body.name, model: body.model, prompt: body.prompt ?? '', scope, projectId })
    state.agents.push(agent)
    return HttpResponse.json(agentOut(agent), { status: 201 })
  }
  const project = (id: string) => state.projects.find((p) => p.id === id)

  const handlers = [
    http.get(apiUrl('/context'), () => HttpResponse.json(context)),
    http.get(apiUrl('/projects'), () => HttpResponse.json({ projects: state.projects.map(withCounts) })),
    http.post(apiUrl('/projects'), async ({ request }) => {
      await record(request)
      const { name } = (await request.json()) as Schemas['ProjectCreate']
      if (state.projects.some((p) => p.name === name)) return HttpResponse.json(errorBody('name_conflict'), { status: 409 })
      const created: Schemas['Project'] = { ...defaultProject, id: crypto.randomUUID(), name, isDefault: false, managerAgentId: null }
      state.projects.push(created)
      return HttpResponse.json(withCounts(created), { status: 201 })
    }),
    http.patch(apiUrl('/projects/:projectId'), async ({ request, params }) => {
      await record(request)
      const target = project(String(params.projectId))
      if (!target) return notFound()
      const { name } = (await request.json()) as Schemas['ProjectUpdate']
      if (state.projects.some((p) => p.name === name && p.id !== target.id)) return HttpResponse.json(errorBody('name_conflict'), { status: 409 })
      target.name = name
      return HttpResponse.json(withCounts(target))
    }),
    http.delete(apiUrl('/projects/:projectId'), async ({ request, params }) => {
      await record(request)
      const id = String(params.projectId)
      const busy = state.agents.filter((a) => a.projectId === id && state.busy.has(a.id))
      if (busy.length > 0)
        return HttpResponse.json(errorBody('agent_not_idle', 'busy', { details: { agents: busy.map((a) => ({ id: a.id, name: a.name })) } }), { status: 409 })
      state.projects = state.projects.filter((p) => p.id !== id)
      state.agents = state.agents.filter((a) => a.projectId !== id)
      return new HttpResponse(null, { status: 204 })
    }),
    http.put(apiUrl('/projects/:projectId/manager'), async ({ request, params }) => {
      await record(request)
      const target = project(String(params.projectId))
      if (!target) return notFound()
      target.managerAgentId = ((await request.json()) as Schemas['ProjectManagerSet']).agentId
      return HttpResponse.json(withCounts(target))
    }),
    http.delete(apiUrl('/projects/:projectId/manager'), async ({ request, params }) => {
      await record(request)
      const target = project(String(params.projectId))
      if (!target) return notFound()
      target.managerAgentId = null
      return HttpResponse.json(withCounts(target))
    }),
    http.get(apiUrl('/projects/:projectId/agents'), ({ params }) =>
      project(String(params.projectId))
        ? HttpResponse.json({ agents: state.agents.filter((a) => a.projectId === params.projectId).map((a) => toListItem(agentOut(a))) })
        : notFound(),
    ),
    http.post(apiUrl('/projects/:projectId/agents'), async ({ request, params }) => {
      await record(request)
      if (!project(String(params.projectId))) return notFound()
      return create('project', String(params.projectId), (await request.json()) as Schemas['AgentCreate'])
    }),
    http.post(apiUrl('/platform/agents'), async ({ request }) => {
      await record(request)
      return create('platform', null, (await request.json()) as Schemas['AgentCreate'])
    }),
    http.get(apiUrl('/platform/agents'), () =>
      HttpResponse.json({ agents: state.agents.filter((a) => a.scope === 'platform').map((a) => toListItem(agentOut(a))) }),
    ),
    http.get(apiUrl('/agents/:agentId'), ({ params }) => {
      const agent = state.agents.find((a) => a.id === params.agentId)
      return agent ? HttpResponse.json(agentOut(agent)) : notFound()
    }),
    http.delete(apiUrl('/agents/:agentId'), async ({ request, params }) => {
      await record(request)
      state.projects.forEach((p) => {
        if (p.managerAgentId === params.agentId) p.managerAgentId = null
      })
      state.agents = state.agents.filter((a) => a.id !== params.agentId)
      return new HttpResponse(null, { status: 204 })
    }),
    http.get(apiUrl('/agents/:agentId/status'), ({ params }) => HttpResponse.json(makeStatus(String(params.agentId)))),
    http.get(apiUrl('/agents/:agentId/skills'), () => HttpResponse.json({ skills: [] })),
    http.get(apiUrl('/agents/:agentId/messages'), () => HttpResponse.json({ messages: [], hasMore: false, hasMoreAfter: false })),
    http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
    http.get(apiUrl('/skills'), () => HttpResponse.json({ skills: [] })),
    ...catalogHandlers,
  ]
  return { state, handlers }
}
