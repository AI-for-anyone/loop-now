import type { Schemas } from '@/api/client'

// 当前作用域：某个项目，或平台（平台持有的 Agent）。
export type Scope = { kind: 'project'; project: Schemas['Project'] } | { kind: 'platform' }

// 作用域的标识：项目 ID，或 `platform`。用于查询键与浏览器中记住的最近使用的作用域。
// 项目 ID 是 UUID，不会与 `platform` 相同。
export type ScopeKey = string
export const PLATFORM: ScopeKey = 'platform'

export const scopeKey = (scope: Scope): ScopeKey => (scope.kind === 'platform' ? PLATFORM : scope.project.id)

// 地址：项目在 /p/<项目名> 下（名称编码后作为一段），平台在 /platform 下。
export const projectBase = (name: string) => `/p/${encodeURIComponent(name)}`
export const scopeBase = (scope: Scope) => (scope.kind === 'platform' ? '/platform' : projectBase(scope.project.name))
export const agentsPath = (scope: Scope) => `${scopeBase(scope)}/agents`
export type AgentTab = 'chat' | 'config' | 'folder' | 'inbox'
export const agentPath = (scope: Scope, agentId: string, tab?: AgentTab) =>
  `${agentsPath(scope)}/${agentId}${tab ? `/${tab}` : ''}`
export const projectSettingsPath = (project: Pick<Schemas['Project'], 'name'>) => `${projectBase(project.name)}/settings`

// 左侧栏的范围：全局（所有项目，以及平台持有的 Agent），或某个项目。平台不再是单独的范围。
export type ShellScope = { kind: 'global' } | { kind: 'project'; name: string }
export const GLOBAL: { kind: 'global' } = { kind: 'global' }
// 按项目列表解析后的范围：项目列表加载中，或地址里的项目找不到时 project 为 undefined。
export type ResolvedShellScope = { kind: 'global' } | { kind: 'project'; name: string; project: Schemas['Project'] | undefined }

// 地址给出的范围：/p/<项目名> 下与带 ?project= 的概览是该项目，/platform 下与其余的概览是全局。
// 插件市场、Skills 库、外部身份、全局设置这些全局页面与旧地址不给出范围，左侧栏沿用之前的范围。
export function urlScope({ pathname, search }: { pathname: string; search: string }): ShellScope | null {
  if (pathname === '/') {
    const name = new URLSearchParams(search).get('project')
    return name !== null ? { kind: 'project', name } : GLOBAL
  }
  if (/^\/platform(\/|$)/.test(pathname)) return GLOBAL
  const match = /^\/p\/([^/]+)(?:\/|$)/.exec(pathname)
  if (!match) return null
  try { return { kind: 'project', name: decodeURIComponent(match[1]) } }
  catch { return { kind: 'project', name: match[1] } }
}

// 概览的地址：项目为 /?project=<项目名>，全局为 /。
export const overviewPath = (scope: ShellScope) => (scope.kind === 'project' ? `/?${new URLSearchParams({ project: scope.name })}` : '/')

// Agent 实际所在的作用域；所属项目不在列表中（已删除或列表未加载）时为 null。
export function agentScope(agent: Pick<Schemas['Agent'], 'scope' | 'projectId'>, projects: Schemas['Project'][]): Scope | null {
  if (agent.scope === 'platform') return { kind: 'platform' }
  const project = projects.find((p) => p.id === agent.projectId)
  return project ? { kind: 'project', project } : null
}

export const sameScope = (a: Scope, b: Scope) => scopeKey(a) === scopeKey(b)

// 浏览器中记住的最近使用的作用域：只存项目 ID 或 `platform`，不存名称（名称会变，ID 不会）。
const STORAGE_KEY = 'loop-now.lastScope'
const listeners = new Set<() => void>()

function read(): ScopeKey | null {
  try {
    return window.localStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

export const lastScope = {
  get: read,
  set(key: ScopeKey) {
    if (read() === key) return
    try {
      window.localStorage.setItem(STORAGE_KEY, key)
    } catch {
      // 浏览器禁用存储时只是记不住，不影响其他功能。
    }
    listeners.forEach((listener) => listener())
  },
  clear() {
    try {
      window.localStorage.removeItem(STORAGE_KEY)
    } catch {
      // 同上
    }
    listeners.forEach((listener) => listener())
  },
  subscribe(listener: () => void) {
    listeners.add(listener)
    return () => listeners.delete(listener)
  },
}

// 按记住的作用域找到当前可用的作用域：记录的项目已不存在或没有记录时，用默认项目。项目列表为空时为 null。
export function resolveLastScope(projects: Schemas['Project'][], remembered: ScopeKey | null = lastScope.get()): Scope | null {
  if (remembered === PLATFORM) return { kind: 'platform' }
  const project = projects.find((p) => p.id === remembered) ?? projects.find((p) => p.isDefault)
  return project ? { kind: 'project', project } : null
}

// 在本控制台改名的项目 ID。项目列表先于地址换成新名称，ProjectScopeRoute 据此只换地址、不贴“在别处改名”的便签。
export const renamedHere = new Set<string>()

// 概览、用量、异常的范围参数：给出项目 ID 或 platform=true 之一；都不给表示所有未删除项目与平台。
export type ScopeQuery = { projectId?: string; platform?: boolean }
export const scopeQueryKey = (query: ScopeQuery) => query.projectId ?? (query.platform ? PLATFORM : 'all')
