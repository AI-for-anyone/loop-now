import { http, HttpResponse } from 'msw'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody } from './api'

type Directory = Schemas['LocalDirectory']
type Listing = { revision: number; directories: Directory[] }

// 本机模式的假服务端：内存中保存三个开关与项目、Agent 的本机目录清单，按契约响应读写与修订号检查。
// 路径里带 missing 的目录视为不存在。测试可以直接改 state 模拟“在别处”的修改，或用 failNextPut 让下一次保存失败。
export function localModeBackend(init: { settings?: Partial<Schemas['LocalModeSettings']>; projects?: Record<string, Listing>; agents?: Record<string, Listing & { projectId: string | null }> } = {}) {
  const state = {
    settings: { directories: true, git: true, cli: true, logins: false, ...init.settings } as Schemas['LocalModeSettings'],
    projects: new Map(Object.entries(init.projects ?? {})),
    agents: new Map(Object.entries(init.agents ?? {})),
    requests: [] as { method: string; path: string; body: unknown }[],
    failNextPut: null as null | (() => Response),
  }
  const statusOf = (path: string): Directory['status'] => (path.includes('missing') ? 'missing' : 'ok')
  const stored = (input: Schemas['LocalDirectoryInput']): Directory => ({ path: input.path, note: input.note?.trim() || null, status: statusOf(input.path) })
  const project = (projectId: string) => state.projects.get(projectId) ?? { revision: 0, directories: [] }
  const agent = (agentId: string) => state.agents.get(agentId) ?? { revision: 0, directories: [], projectId: null }
  const agentOut = (agentId: string): Schemas['AgentLocalDirectories'] => {
    const { revision, directories, projectId } = agent(agentId)
    return { revision, directories, projectId, inherited: projectId ? project(projectId).directories : [] }
  }
  // 修订号不符为 412；否则（除非测试安排了失败）整体替换并加一。
  const put = async (request: Request, current: Listing, store: (next: Listing) => void) => {
    const body = (await request.json()) as Schemas['LocalDirectoriesPut']
    state.requests.push({ method: 'PUT', path: new URL(request.url).pathname, body })
    if (body.expectedRevision !== current.revision) return HttpResponse.json(errorBody('precondition_failed'), { status: 412 })
    const fail = state.failNextPut
    if (fail) {
      state.failNextPut = null
      return fail()
    }
    store({ revision: current.revision + 1, directories: body.directories.map(stored) })
    return null
  }

  const handlers = [
    http.get(apiUrl('/local-mode-settings'), () => HttpResponse.json(state.settings)),
    http.patch(apiUrl('/local-mode-settings'), async ({ request }) => {
      const body = (await request.json()) as Schemas['LocalModeSettingsPatch']
      state.requests.push({ method: 'PATCH', path: new URL(request.url).pathname, body })
      const fail = state.failNextPut
      if (fail) {
        state.failNextPut = null
        return fail()
      }
      state.settings = { ...state.settings, ...body }
      return HttpResponse.json(state.settings)
    }),
    http.get(apiUrl('/projects/:projectId/local-directories'), ({ params }) => HttpResponse.json(project(String(params.projectId)))),
    http.put(apiUrl('/projects/:projectId/local-directories'), async ({ request, params }) => {
      const id = String(params.projectId)
      const rejected = await put(request, project(id), (next) => state.projects.set(id, next))
      return rejected ?? HttpResponse.json(project(id))
    }),
    http.get(apiUrl('/agents/:agentId/local-directories'), ({ params }) => HttpResponse.json(agentOut(String(params.agentId)))),
    http.put(apiUrl('/agents/:agentId/local-directories'), async ({ request, params }) => {
      const id = String(params.agentId)
      const current = agent(id)
      const rejected = await put(request, current, (next) => state.agents.set(id, { ...next, projectId: current.projectId }))
      return rejected ?? HttpResponse.json(agentOut(id))
    }),
  ]
  return { state, handlers }
}

export const dir = (path: string, note: string | null = null, status: Directory['status'] = path.includes('missing') ? 'missing' : 'ok'): Directory => ({ path, note, status })
