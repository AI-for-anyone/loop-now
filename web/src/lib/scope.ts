import type { Schemas } from '@/api/client'

// The current scope: a project, or the platform (Agents owned by the platform).
export type Scope = { kind: 'project'; project: Schemas['Project'] } | { kind: 'platform' }

// A scope's key: a project ID, or `platform`. Used in query keys and for the last-used scope remembered in the browser.
// Project IDs are UUIDs, so they never equal `platform`.
export type ScopeKey = string
export const PLATFORM: ScopeKey = 'platform'

export const scopeKey = (scope: Scope): ScopeKey => (scope.kind === 'platform' ? PLATFORM : scope.project.id)

// URLs: projects live under /p/<project name> (the encoded name as one segment), the platform under /platform.
export const projectBase = (name: string) => `/p/${encodeURIComponent(name)}`
export const scopeBase = (scope: Scope) => (scope.kind === 'platform' ? '/platform' : projectBase(scope.project.name))
export const agentsPath = (scope: Scope) => `${scopeBase(scope)}/agents`
export type AgentTab = 'chat' | 'config' | 'folder' | 'inbox'
export const agentPath = (scope: Scope, agentId: string, tab?: AgentTab) =>
  `${agentsPath(scope)}/${agentId}${tab ? `/${tab}` : ''}`
export type ProjectSettingsTab = 'general' | 'context' | 'credentials' | 'plugins' | 'danger'
export const projectSettingsPath = (project: string | Pick<Schemas['Project'], 'name'>, tab?: ProjectSettingsTab) => {
  const name = typeof project === 'string' ? project : project.name
  return `${projectBase(name)}/settings${tab && tab !== 'general' ? `/${tab}` : ''}`
}
// The Agent runtimes section of global settings (its id is settings-runtimes, SettingsPage), where local ACP commands are edited.
export const runtimeSettingsPath = '/settings/runtimes#settings-runtimes'
// Who writes with AI, in global settings (its id is settings-writing, SettingsPage).
export const writingSettingsPath = '/settings/runtimes#settings-writing'

// The sidebar's scope: global (all projects, plus the Agents owned by the platform), or one project. The platform is no longer a scope
// of its own.
export type ShellScope = { kind: 'global' } | { kind: 'project'; name: string }
export const GLOBAL: { kind: 'global' } = { kind: 'global' }
// The scope resolved against the project list: project is undefined while the project list is loading or when the project in the URL
// cannot be found.
export type ResolvedShellScope = { kind: 'global' } | { kind: 'project'; name: string; project: Schemas['Project'] | undefined }

// The scope given by the URL: under /p/<project name> and on the overview with ?project= it is that project; under /platform and on any
// other overview it is global.
// Global pages such as the plugin market, Skills library, external identities and global settings, as well as old URLs, give no scope,
// and the sidebar keeps the previous one.
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

// The overview URL: /?project=<project name> for a project, / for global.
export const overviewPath = (scope: ShellScope) => (scope.kind === 'project' ? `/?${new URLSearchParams({ project: scope.name })}` : '/')

// The scope the Agent actually lives in; null when its project is not in the list (deleted, or the list has not loaded).
export function agentScope(agent: Pick<Schemas['Agent'], 'scope' | 'projectId'>, projects: Schemas['Project'][]): Scope | null {
  if (agent.scope === 'platform') return { kind: 'platform' }
  const project = projects.find((p) => p.id === agent.projectId)
  return project ? { kind: 'project', project } : null
}

export const sameScope = (a: Scope, b: Scope) => scopeKey(a) === scopeKey(b)

// The last-used scope remembered in the browser: only the project ID or `platform` is stored, never the name (names change, IDs do not).
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
      // When the browser disables storage, it is simply not remembered; nothing else is affected.
    }
    listeners.forEach((listener) => listener())
  },
  clear() {
    try {
      window.localStorage.removeItem(STORAGE_KEY)
    } catch {
      // Same as above
    }
    listeners.forEach((listener) => listener())
  },
  subscribe(listener: () => void) {
    listeners.add(listener)
    return () => listeners.delete(listener)
  },
}

// Find a usable scope from the remembered one: when the recorded project no longer exists or nothing is recorded, use the default
// project. null when the project list is empty.
export function resolveLastScope(projects: Schemas['Project'][], remembered: ScopeKey | null = lastScope.get()): Scope | null {
  if (remembered === PLATFORM) return { kind: 'platform' }
  const project = projects.find((p) => p.id === remembered) ?? projects.find((p) => p.isDefault)
  return project ? { kind: 'project', project } : null
}

// IDs of projects renamed in this console. The project list switches to the new name before the URL does, so ProjectScopeRoute uses this
// to only update the URL, without putting up the "renamed elsewhere" sticky note.
export const renamedHere = new Set<string>()

// Scope parameters for the overview, usage and anomalies: give either a project ID or platform=true; giving neither means every project
// that is not deleted plus the platform.
export type ScopeQuery = { projectId?: string; platform?: boolean }
export const scopeQueryKey = (query: ScopeQuery) => query.projectId ?? (query.platform ? PLATFORM : 'all')
