import { http, HttpResponse } from 'msw'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody } from './api'

// Test data for plugins (second version): built per the contract; by default
// the webhook plugin shipped with the platform (platform side only).
const at = '2026-10-27T03:19:00Z'

// Config form of the webhook: callback URL (overridable per project, required), signing secret (secret), retry limit (integer, default 5).
export const webhookSchema = {
  type: 'object',
  required: ['url'],
  properties: {
    url: { type: 'string', format: 'uri', title: 'Callback URL', description: 'Events are POSTed here', 'x-scope': 'project' },
    secret: { type: 'string', title: 'Signing secret', description: 'Used to compute X-Loop-Now-Signature', 'x-secret': true },
    retries: { type: 'integer', title: 'Retry limit', minimum: 0, maximum: 10, default: 5 },
  },
} as unknown as Record<string, never>

export function makeComponents(overrides: Partial<Schemas['PluginComponents']> = {}): Schemas['PluginComponents'] {
  return { agentExtensions: 0, skills: [], mcpServers: [], bin: [], platform: true, actions: [], channels: [], http: false, schedules: [], ...overrides }
}

export function makeManifest(overrides: Partial<Schemas['PluginManifest']> = {}): Schemas['PluginManifest'] {
  return {
    id: 'webhook',
    name: '@loop-now/webhook',
    version: '1.2.0',
    displayName: 'Webhook notifications',
    description: 'POSTs new inbox messages to the callback URL',
    minPlatformVersion: null,
    events: ['inbox.received'],
    permissions: ['inbox.read', 'state'],
    failClosed: false,
    configSchema: webhookSchema,
    accounts: [],
    components: makeComponents(),
    ...overrides,
  }
}

export function makeResolvedSource(overrides: Partial<Schemas['PluginResolvedSource']> = {}): Schemas['PluginResolvedSource'] {
  return { kind: 'builtin', location: 'webhook', ref: null, commit: null, npmVersion: null, market: null, ...overrides }
}

export function makePlugin(overrides: Partial<Schemas['Plugin']> = {}): Schemas['Plugin'] {
  return {
    id: 'webhook',
    displayName: 'Webhook notifications',
    description: 'POSTs new inbox messages to the callback URL',
    version: '1.2.0',
    source: makeResolvedSource(),
    status: 'active',
    lastError: null,
    hasPlatform: true,
    enabledProjects: 1,
    grantedAgents: 0,
    installedAt: at,
    updatedAt: at,
    ...overrides,
  }
}

export function makeDetail(plugin: Schemas['Plugin'] = makePlugin(), manifest: Schemas['PluginManifest'] = makeManifest({ id: plugin.id, version: plugin.version, displayName: plugin.displayName })): Schemas['PluginDetail'] {
  return {
    plugin,
    manifest,
    versions: [{ version: plugin.version, digest: 'sha256-abc', installedAt: at, current: true }],
    installDir: `/work/platform/plugins/${plugin.id}/${plugin.version}`,
    dataDir: `/work/platform/plugins/${plugin.id}/data`,
    providers: [],
    providerErrors: [],
    providersError: null,
    providerState: {},
  }
}

export function makePreview(overrides: Partial<Schemas['PluginPreview']> = {}): Schemas['PluginPreview'] {
  return {
    previewId: '71000000-0000-7000-8000-000000000001',
    expiresAt: '2026-10-27T04:00:00Z',
    source: makeResolvedSource(),
    trusted: true,
    manifest: makeManifest(),
    installedVersion: null,
    ...overrides,
  }
}

export const noReadme: Schemas['PluginReadme'] = { file: null, markdown: null, truncated: false }

export function makeConfig(overrides: Partial<Schemas['PluginConfig']> = {}): Schemas['PluginConfig'] {
  return { values: {}, secretsSet: [], revision: 0, ...overrides } as Schemas['PluginConfig']
}

export function makeHost(overrides: Partial<Schemas['PluginHostStatus']> = {}): Schemas['PluginHostStatus'] {
  return {
    configured: true,
    state: 'running',
    pid: 4242,
    startedAt: at,
    hostVersion: '1.0.0',
    recentExits: [],
    lastCrash: null,
    loaded: ['webhook'],
    today: { delivered: 0, failed: 0 },
    ...overrides,
  }
}

export function makeProjectPlugin(overrides: Partial<Schemas['ProjectPlugin']> = {}): Schemas['ProjectPlugin'] {
  return { pluginId: 'webhook', displayName: 'Webhook notifications', version: '1.2.0', status: 'active', enabled: false, projectFields: ['url'], config: makeConfig(), ...overrides }
}

export function makeDelivery(overrides: Partial<Schemas['PluginDelivery']> = {}): Schemas['PluginDelivery'] {
  return {
    eventId: '01JB0000000000000000000001',
    eventType: 'inbox.received',
    at: '2026-10-27T04:41:00Z',
    projectId: null,
    agentId: null,
    taskId: null,
    runId: null,
    status: 'delivered',
    attempts: 1,
    lastError: null,
    ackedAt: '2026-10-27T04:41:01Z',
    durationMs: 120,
    ...overrides,
  }
}

export function makeAccount(overrides: Partial<Schemas['PluginAccount']> = {}): Schemas['PluginAccount'] {
  return {
    id: '72000000-0000-7000-8000-000000000001',
    pluginId: 'lark-notify',
    projectId: '00000000-0000-7000-8000-00000000aaaa',
    kind: 'lark',
    name: 'Dev group bot',
    values: { appId: 'cli_a1b2' },
    secretsSet: ['appSecret'],
    revision: 1,
    ...overrides,
  } as Schemas['PluginAccount']
}

// Lark account kind: App ID (plain field) and App Secret (secret).
export const larkAccountKind: Schemas['PluginAccountKind'] = {
  kind: 'lark',
  label: 'Lark app',
  multiple: true,
  fields: {
    type: 'object',
    required: ['appId', 'appSecret'],
    properties: { appId: { type: 'string', title: 'App ID' }, appSecret: { type: 'string', title: 'App Secret', 'x-secret': true } },
  } as unknown as Record<string, never>,
}

// ---------- Fake plugin backend ----------
// For page tests: keeps plugins, config, project enablement, accounts and Agent grants in memory per the contract, and records
// every modifying request; save requests are checked against expectedRevision and get 412 when the version doesn't match.
export type FakePluginState = {
  details: Schemas['PluginDetail'][]
  configs: Record<string, Schemas['PluginConfig']>
  // project ID → plugin ID → enablement and config in the project
  projects: Record<string, Record<string, Schemas['ProjectPlugin']>>
  accounts: Schemas['PluginAccount'][]
  agents: Record<string, Schemas['AgentPlugin'][]>
  deliveries: Record<string, Schemas['PluginDelivery'][]>
  state: Record<string, Schemas['PluginStateEntry'][]>
  logs: Record<string, Schemas['PluginLogEntry'][]>
  host: Schemas['PluginHostStatus']
  settings: Schemas['PluginSettings']
  builtin: Schemas['BuiltinPluginSource'][]
  markets: Schemas['PluginMarket'][]
  // plugin ID → README of the installed version; plugins not listed have no README.
  readmes: Record<string, Schemas['PluginReadme']>
  // Responses of action buttons, keyed by "plugin id/action id"; ones not listed return a default notice.
  actions: Record<string, (body: Record<string, unknown>) => Response>
}

export type FakeRequest = { method: string; path: string; query: Record<string, string>; body: unknown }

export function fakePlugins(initial: Partial<FakePluginState> = {}) {
  const state: FakePluginState = {
    details: [makeDetail()],
    configs: {},
    projects: {},
    accounts: [],
    agents: {},
    deliveries: {},
    state: {},
    logs: {},
    host: makeHost(),
    settings: { disabled: false, markets: [] },
    builtin: [],
    markets: [],
    readmes: {},
    actions: {},
    ...initial,
  }
  const requests: FakeRequest[] = []
  const note = async (request: Request) => {
    const url = new URL(request.url)
    const body = request.method === 'GET' || request.method === 'DELETE' ? undefined : await request.clone().json().catch(() => undefined)
    requests.push({ method: request.method, path: url.pathname.replace(/^.*\/api\/v1/, ''), query: Object.fromEntries(url.searchParams), body })
    return { url, body: body as Record<string, unknown> }
  }
  const detail = (id: string) => state.details.find((d) => d.plugin.id === id)
  const notFound = () => HttpResponse.json(errorBody('not_found', 'not found'), { status: 404 })
  const stale = () => HttpResponse.json(errorBody('precondition_failed', 'revision mismatch'), { status: 412 })
  const config = (id: string) => state.configs[id] ?? makeConfig()
  const projectList = (projectId: string): Schemas['ProjectPlugin'][] =>
    state.details.map((d) => state.projects[projectId]?.[d.plugin.id] ?? makeProjectPlugin({ pluginId: d.plugin.id, displayName: d.plugin.displayName, version: d.plugin.version, status: d.plugin.status }))
  const page = <T,>(items: T[], url: URL, idOf: (item: T) => string | number) => {
    const limit = Number(url.searchParams.get('limit') ?? 50), before = url.searchParams.get('before')
    const start = before === null ? 0 : items.findIndex((item) => String(idOf(item)) === before) + 1
    const slice = items.slice(start, start + limit)
    return { slice, next: start + limit < items.length ? idOf(slice.at(-1)!) : null }
  }
  const handlers = [
    http.get(apiUrl('/plugins'), () => HttpResponse.json({ plugins: state.details.map((d) => d.plugin) })),
    http.get(apiUrl('/plugin-sources/builtin'), () => HttpResponse.json({ sources: state.builtin })),
    http.get(apiUrl('/plugin-markets'), () => HttpResponse.json({ markets: state.markets })),
    http.post(apiUrl('/plugins/:pluginId/actions/:actionId'), async ({ request, params }) => {
      const { body } = await note(request)
      const custom = state.actions[`${params.pluginId}/${params.actionId}`]
      return custom ? custom(body) : HttpResponse.json({ notice: 'Done', url: null })
    }),
    http.get(apiUrl('/plugin-host'), () => HttpResponse.json(state.host)),
    http.post(apiUrl('/plugin-host/restart'), async ({ request }) => {
      await note(request)
      state.host = { ...state.host, state: 'starting', lastCrash: null, recentExits: [] }
      return HttpResponse.json(state.host, { status: 202 })
    }),
    http.get(apiUrl('/plugin-settings'), () => HttpResponse.json(state.settings)),
    http.put(apiUrl('/plugin-settings'), async ({ request }) => {
      const { body } = await note(request)
      state.settings = body as Schemas['PluginSettings']
      return HttpResponse.json(state.settings)
    }),
    http.get(apiUrl('/plugins/:pluginId'), ({ params }) => {
      const found = detail(String(params.pluginId))
      return found ? HttpResponse.json(found) : notFound()
    }),
    http.get(apiUrl('/plugins/:pluginId/readme'), async ({ request, params }) => {
      await note(request)
      return detail(String(params.pluginId)) ? HttpResponse.json(state.readmes[String(params.pluginId)] ?? noReadme) : notFound()
    }),
    http.delete(apiUrl('/plugins/:pluginId'), async ({ request, params }) => {
      await note(request)
      state.details = state.details.filter((d) => d.plugin.id !== params.pluginId)
      return new HttpResponse(null, { status: 204 })
    }),
    http.get(apiUrl('/plugins/:pluginId/config'), ({ params }) => HttpResponse.json(config(String(params.pluginId)))),
    http.put(apiUrl('/plugins/:pluginId/config'), async ({ request, params }) => {
      const { body } = await note(request)
      const id = String(params.pluginId), current = config(id)
      if (body.expectedRevision !== current.revision) return stale()
      const secrets = (body.secrets ?? {}) as Record<string, string | null>
      const secretsSet = [...new Set([...current.secretsSet, ...Object.keys(secrets).filter((k) => secrets[k] !== null)])].filter((k) => secrets[k] !== null)
      state.configs[id] = makeConfig({ values: body.values as never, secretsSet, revision: current.revision + 1 })
      return HttpResponse.json(state.configs[id])
    }),
    http.get(apiUrl('/plugins/:pluginId/logs'), ({ request, params }) => {
      const url = new URL(request.url)
      const { slice, next } = page(state.logs[String(params.pluginId)] ?? [], url, (entry) => entry.id)
      return HttpResponse.json({ entries: slice, nextBefore: next })
    }),
    http.get(apiUrl('/plugins/:pluginId/deliveries'), async ({ request, params }) => {
      const { url } = await note(request)
      const status = url.searchParams.get('status'), projectId = url.searchParams.get('projectId')
      const items = (state.deliveries[String(params.pluginId)] ?? []).filter((d) => (!status || d.status === status) && (!projectId || d.projectId === projectId))
      const { slice, next } = page(items, url, (d) => d.eventId)
      return HttpResponse.json({ deliveries: slice, nextBefore: next })
    }),
    http.get(apiUrl('/plugins/:pluginId/state'), ({ request, params }) => {
      const prefix = new URL(request.url).searchParams.get('prefix') ?? ''
      return HttpResponse.json({ entries: (state.state[String(params.pluginId)] ?? []).filter((e) => e.key.startsWith(prefix)) })
    }),
    http.post(apiUrl('/plugin-previews'), async ({ request }) => {
      const { body } = await note(request)
      const source = body.source as { kind: string; location?: string; id?: string } | undefined
      const id = typeof body.pluginId === 'string' ? body.pluginId : String(source?.kind === 'market' ? source.id : source?.location)
      const installed = detail(id)
      return HttpResponse.json(makePreview({ manifest: installed ? { ...installed.manifest, version: '1.3.0' } : makeManifest({ id }), installedVersion: installed?.plugin.version ?? null }), { status: 201 })
    }),
    // Install: installs from the manifest of the most recent preview (tests only ever have one preview ID).
    http.post(apiUrl('/plugins'), async ({ request }) => {
      await note(request)
      const last = [...requests].reverse().find((r) => r.path === '/plugin-previews')?.body as { source?: { kind: string; location?: string; id?: string } } | undefined
      const id = (last?.source?.kind === 'market' ? last.source.id : last?.source?.location) ?? 'webhook'
      const installed = makeDetail(makePlugin({ id, displayName: id, enabledProjects: 0 }))
      state.details.push(installed)
      return HttpResponse.json(installed.plugin, { status: 201 })
    }),
    http.post(apiUrl('/plugins/:pluginId/update'), async ({ request, params }) => {
      await note(request)
      const found = detail(String(params.pluginId))!
      found.plugin = { ...found.plugin, version: '1.3.0' }
      return HttpResponse.json(found.plugin)
    }),
    http.get(apiUrl('/projects/:projectId/plugins'), ({ params }) => HttpResponse.json({ plugins: projectList(String(params.projectId)) })),
    http.put(apiUrl('/projects/:projectId/plugins/:pluginId'), async ({ request, params }) => {
      const { body } = await note(request)
      const projectId = String(params.projectId), pluginId = String(params.pluginId)
      const current = projectList(projectId).find((p) => p.pluginId === pluginId)!
      if (body.expectedRevision !== current.config.revision) return stale()
      const saved = { ...current, enabled: Boolean(body.enabled), config: makeConfig({ values: body.values as never, secretsSet: current.config.secretsSet, revision: current.config.revision + 1 }) }
      state.projects[projectId] = { ...state.projects[projectId], [pluginId]: saved }
      return HttpResponse.json(saved)
    }),
    http.get(apiUrl('/projects/:projectId/plugins/:pluginId/accounts'), ({ params }) =>
      HttpResponse.json({ accounts: state.accounts.filter((a) => a.projectId === params.projectId && a.pluginId === params.pluginId) }),
    ),
    http.post(apiUrl('/projects/:projectId/plugins/:pluginId/accounts'), async ({ request, params }) => {
      const { body } = await note(request)
      const account = makeAccount({ id: `72000000-0000-7000-8000-${String(state.accounts.length + 10).padStart(12, '0')}`, projectId: String(params.projectId), pluginId: String(params.pluginId), kind: String(body.kind), name: String(body.name), values: body.values as never, secretsSet: Object.keys((body.secrets ?? {}) as object), revision: 1 })
      state.accounts.push(account)
      return HttpResponse.json(account, { status: 201 })
    }),
    http.put(apiUrl('/projects/:projectId/plugins/:pluginId/accounts/:accountId'), async ({ request, params }) => {
      const { body } = await note(request)
      const index = state.accounts.findIndex((a) => a.id === params.accountId)
      if (body.expectedRevision !== state.accounts[index].revision) return stale()
      state.accounts[index] = { ...state.accounts[index], name: String(body.name), values: body.values as never, revision: state.accounts[index].revision + 1 }
      return HttpResponse.json(state.accounts[index])
    }),
    http.delete(apiUrl('/projects/:projectId/plugins/:pluginId/accounts/:accountId'), async ({ request, params }) => {
      await note(request)
      state.accounts = state.accounts.filter((a) => a.id !== params.accountId)
      return new HttpResponse(null, { status: 204 })
    }),
    http.get(apiUrl('/agents/:agentId/plugins'), ({ params }) => HttpResponse.json({ plugins: state.agents[String(params.agentId)] ?? [] })),
    http.put(apiUrl('/agents/:agentId/plugins/:pluginId'), async ({ request, params }) => {
      const { body } = await note(request)
      const list = state.agents[String(params.agentId)] ?? []
      const index = list.findIndex((p) => p.pluginId === params.pluginId)
      if (!list[index].available) return HttpResponse.json(errorBody('plugin_not_enabled', 'not enabled'), { status: 409 })
      list[index] = { ...list[index], granted: Boolean(body.granted), accountIds: body.accountIds as string[] }
      return HttpResponse.json(list[index])
    }),
  ]
  return { state, requests, handlers, writes: () => requests.filter((r) => r.method !== 'GET') }
}

// The market and the entries in it.
export function makeMarketEntry(overrides: Partial<Schemas['PluginMarketEntry']> = {}): Schemas['PluginMarketEntry'] {
  return { id: 'usage-report', displayName: 'Weekly usage report', description: 'Every Monday, sends last week\'s tokens and cost to your inbox.', source: 'git:https://github.com/loop-now/usage-report@v0.9.0', version: '0.9.0', verified: true, installedVersion: null, updateAvailable: false, providerState: {}, ...overrides }
}

export function makeMarket(overrides: Partial<Schemas['PluginMarket']> = {}): Schemas['PluginMarket'] {
  return { url: 'https://market.loop-now.dev/loop-now-market.json', name: 'Loop Now official market', official: true, error: null, plugins: [makeMarketEntry()], ...overrides }
}

export function makeAction(overrides: Partial<Schemas['PluginAction']> = {}): Schemas['PluginAction'] {
  return { id: 'resend', label: 'Resend', where: ['global'], fields: {} as never, ...overrides }
}
