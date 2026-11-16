import { useInfiniteQuery, useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { locale } from '@/i18n/locale'
import { api, call, type ApiError, type Schemas } from './client'
import { invalidateModelCatalog } from './queries'

type Plugin = Schemas['Plugin']
type ConfigUpdate = Schemas['PluginConfigUpdate']
export type DeliveryFilter = { status?: Schemas['PluginDelivery']['status']; projectId?: string }

// Plugins (second version). ['plugins'] is the common prefix of every plugin query: they are all invalidated after an install, update,
// uninstall, enable or grant, so the counts of enabled projects and granted Agents in the list refresh with them. A plugin's Pi extension
// registers model providers, so the model catalog is invalidated as well after these operations.
export const pluginKeys = {
  all: ['plugins'] as const,
  list: ['plugins', 'list'] as const,
  detail: (pluginId: string) => ['plugins', 'detail', pluginId] as const,
  readme: (pluginId: string) => ['plugins', 'readme', pluginId] as const,
  config: (pluginId: string) => ['plugins', 'config', pluginId] as const,
  logs: (pluginId: string) => ['plugins', 'logs', pluginId] as const,
  deliveries: (pluginId: string, filter: DeliveryFilter = {}) => ['plugins', 'deliveries', pluginId, filter.status ?? 'all', filter.projectId ?? 'all'] as const,
  state: (pluginId: string, prefix: string, projectId?: string) => ['plugins', 'state', pluginId, prefix, projectId ?? 'global'] as const,
  host: ['plugins', 'host'] as const,
  builtin: ['plugins', 'builtin'] as const,
  markets: ['plugins', 'markets'] as const,
  settings: ['plugins', 'settings'] as const,
  project: (projectId: string) => ['plugins', 'project', projectId] as const,
  accounts: (projectId: string, pluginId: string) => ['plugins', 'accounts', projectId, pluginId] as const,
  agent: (agentId: string) => ['plugins', 'agent', agentId] as const,
}

const pluginPath = (pluginId: string) => ({ params: { path: { pluginId } } })
const projectPluginPath = (projectId: string, pluginId: string) => ({ params: { path: { projectId, pluginId } } })

export const invalidatePlugins = (client: QueryClient) => client.invalidateQueries({ queryKey: pluginKeys.all })

// ---------- Reads ----------

export function usePlugins() {
  return useQuery({ queryKey: pluginKeys.list, queryFn: () => call(api.GET('/plugins')) })
}

export function usePlugin(pluginId: string) {
  return useQuery({ queryKey: pluginKeys.detail(pluginId), queryFn: () => call(api.GET('/plugins/{pluginId}', pluginPath(pluginId))) })
}

// The README file is picked by the console's language (the server falls back README.<language>.md → README.<primary language>.md → README.md).
// An installed plugin's README is invalidated on update. The one in a preview never changes and is not kept under ['plugins'] either: once a
// preview has been used for an install it can no longer be read, so it must not be re-read when the install finishes.
export function usePluginReadme(pluginId: string) {
  return useQuery({ queryKey: pluginKeys.readme(pluginId), queryFn: () => call(api.GET('/plugins/{pluginId}/readme', { params: { path: { pluginId }, query: { locale } } })) })
}

export function usePreviewReadme(previewId: string) {
  return useQuery({
    queryKey: ['plugin-previews', previewId, 'readme'],
    queryFn: () => call(api.GET('/plugin-previews/{previewId}/readme', { params: { path: { previewId }, query: { locale } } })),
    staleTime: Infinity,
  })
}

export function usePluginConfig(pluginId: string) {
  return useQuery({ queryKey: pluginKeys.config(pluginId), queryFn: () => call(api.GET('/plugins/{pluginId}/config', pluginPath(pluginId))) })
}

// Host logs: newest first; page back to older entries with nextBefore.
export function usePluginLogs(pluginId: string, limit = 100) {
  return useInfiniteQuery({
    queryKey: pluginKeys.logs(pluginId),
    initialPageParam: undefined as number | undefined,
    queryFn: ({ pageParam }) => call(api.GET('/plugins/{pluginId}/logs', { params: { path: { pluginId }, query: { limit, ...(pageParam ? { before: pageParam } : {}) } } })),
    getNextPageParam: (page) => page.nextBefore ?? undefined,
  })
}

// Deliveries of observation events to this plugin: newest event time first, filterable by status and project.
export function usePluginDeliveries(pluginId: string, filter: DeliveryFilter = {}, limit = 50) {
  return useInfiniteQuery({
    queryKey: pluginKeys.deliveries(pluginId, filter),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      call(api.GET('/plugins/{pluginId}/deliveries', { params: { path: { pluginId }, query: { limit, ...filter, ...(pageParam ? { before: pageParam } : {}) } } })),
    getNextPageParam: (page) => page.nextBefore ?? undefined,
  })
}

// The plugin's persistent state (read-only), e.g. the deliveries/ prefix that delivery records use by convention.
export function usePluginState(pluginId: string, prefix: string, projectId?: string) {
  return useQuery({
    queryKey: pluginKeys.state(pluginId, prefix, projectId),
    queryFn: () => call(api.GET('/plugins/{pluginId}/state', { params: { path: { pluginId }, query: { prefix, ...(projectId ? { projectId } : {}) } } })),
  })
}

// The host state changes on its own (starting, restarting after a crash), so it is polled while the plugin market page stays open.
export function usePluginHost(refetchInterval: number | false = 10_000) {
  return useQuery({ queryKey: pluginKeys.host, queryFn: () => call(api.GET('/plugin-host')), refetchInterval })
}

// Official plugins that ship with the platform, listed under the “官方” (Official) source of the install entry; installed by id
// (source kind builtin).
export function useBuiltinPluginSources() {
  return useQuery({ queryKey: pluginKeys.builtin, queryFn: () => call(api.GET('/plugin-sources/builtin')) })
}

// The plugins of every market (the official market and the markets added in global settings), marking the installed version and available
// updates; when a market fails to load, its error is not empty.
export function usePluginMarkets(enabled = true) {
  return useQuery({ queryKey: pluginKeys.markets, queryFn: () => call(api.GET('/plugin-markets')), enabled })
}

// An installed plugin's entry in the market it came from; undefined when it was not installed from a market or the market no longer has it.
export function marketEntryOf(markets: Schemas['PluginMarket'][] | undefined, plugin: Pick<Schemas['Plugin'], 'id' | 'source'>) {
  if (!plugin.source.market) return undefined
  return markets?.find((market) => market.url === plugin.source.market)?.plugins.find((entry) => entry.id === plugin.id)
}

// Whether an installed plugin's source counts as reviewed, matching the server's judgment at install time: official for plugins that ship
// with the platform or come from the official market, verified for markets marked as verified, null for the rest. Only the market URL is
// recorded at install time, so plugins from a market need the market list: undefined while it has not been read yet, and treated as
// unreviewed when the market or the entry is no longer in the list.
export function pluginTrust(markets: Schemas['PluginMarket'][] | undefined, plugin: Pick<Schemas['Plugin'], 'id' | 'source'>): 'official' | 'verified' | null | undefined {
  if (plugin.source.kind === 'builtin') return 'official'
  if (!plugin.source.market) return null
  if (!markets) return undefined
  const market = markets.find((item) => item.url === plugin.source.market)
  if (market?.official) return 'official'
  return market?.plugins.find((entry) => entry.id === plugin.id)?.verified ? 'verified' : null
}

// A source in a market: install by market URL and entry id; when version is given, install that version (an update uses the market's
// new version).
export const marketSource = (market: string, entry: Pick<Schemas['PluginMarketEntry'], 'id' | 'version'>): Schemas['PluginSource'] => ({
  kind: 'market',
  market,
  id: entry.id,
  ...(entry.version ? { version: entry.version } : {}),
})

export function usePluginSettings() {
  return useQuery({ queryKey: pluginKeys.settings, queryFn: () => call(api.GET('/plugin-settings')) })
}

export function useProjectPlugins(projectId: string) {
  return useQuery({
    queryKey: pluginKeys.project(projectId),
    queryFn: () => call(api.GET('/projects/{projectId}/plugins', { params: { path: { projectId } } })),
    enabled: projectId !== '',
  })
}

// This plugin's enablement state and project-level config in a project (taken from the project's plugin list).
export function useProjectPlugin(projectId: string, pluginId: string) {
  const list = useProjectPlugins(projectId)
  return { ...list, item: list.data?.plugins.find((plugin) => plugin.pluginId === pluginId) }
}

export function usePluginAccounts(projectId: string, pluginId: string, enabled = true) {
  return useQuery({
    queryKey: pluginKeys.accounts(projectId, pluginId),
    queryFn: () => call(api.GET('/projects/{projectId}/plugins/{pluginId}/accounts', projectPluginPath(projectId, pluginId))),
    enabled,
  })
}

export function useAgentPlugins(agentId: string) {
  return useQuery({ queryKey: pluginKeys.agent(agentId), queryFn: () => call(api.GET('/agents/{agentId}/plugins', { params: { path: { agentId } } })) })
}

// ---------- Install, update, uninstall ----------

// Step one: fetch the plugin and read its manifest. A new install passes source; a preview before an update passes pluginId.
export function previewPlugin(body: Schemas['PluginPreviewRequest']) {
  return call(api.POST('/plugin-previews', { body }))
}

// Step two: install or update from the preview. A non-official source must carry confirm (after a person has reviewed it in the
// confirmation dialog).
export function usePluginInstall() {
  const client = useQueryClient()
  return useMutation<Plugin, ApiError, { preview: Schemas['PluginPreview']; confirm: boolean }>({
    mutationFn: ({ preview, confirm }) => {
      const body: Schemas['PluginInstall'] = { previewId: preview.previewId, ...(confirm ? { confirm: true } : {}) }
      return preview.installedVersion === null
        ? call(api.POST('/plugins', { body }))
        : call(api.POST('/plugins/{pluginId}/update', { ...pluginPath(preview.manifest.id), body }))
    },
    onSuccess: () => Promise.all([invalidatePlugins(client), invalidateModelCatalog(client)]),
  })
}

export function useUninstallPlugin(pluginId: string) {
  const client = useQueryClient()
  return useMutation<void, ApiError, { deleteData: boolean }>({
    mutationFn: async ({ deleteData }) => {
      await call(api.DELETE('/plugins/{pluginId}', { params: { path: { pluginId }, query: deleteData ? { deleteData: true } : {} } }))
    },
    onSuccess: async () => {
      client.removeQueries({ queryKey: pluginKeys.detail(pluginId) })
      await Promise.all([invalidatePlugins(client), invalidateModelCatalog(client)])
    },
  })
}

// ---------- Config, host, global settings ----------

export function useSavePluginConfig(pluginId: string) {
  const client = useQueryClient()
  return useMutation<Schemas['PluginConfig'], ApiError, ConfigUpdate>({
    mutationFn: (body) => call(api.PUT('/plugins/{pluginId}/config', { ...pluginPath(pluginId), body })),
    onSuccess: (saved) => client.setQueryData(pluginKeys.config(pluginId), saved),
  })
}

export function useRestartPluginHost() {
  const client = useQueryClient()
  return useMutation<Schemas['PluginHostStatus'], ApiError, void>({
    mutationFn: () => call(api.POST('/plugin-host/restart')),
    onSuccess: (host) => client.setQueryData(pluginKeys.host, host),
  })
}

// One-click disable: stop the host and stop mounting the Agent side into newly started Pi; the host state and every plugin's state change
// with it.
export function useSavePluginSettings() {
  const client = useQueryClient()
  return useMutation<Schemas['PluginSettings'], ApiError, Schemas['PluginSettings']>({
    mutationFn: (body) => call(api.PUT('/plugin-settings', { body })),
    onSuccess: async (settings) => {
      client.setQueryData(pluginKeys.settings, settings)
      await Promise.all([invalidatePlugins(client), invalidateModelCatalog(client)])
    },
  })
}

// ---------- Project enablement and accounts ----------

export function useSaveProjectPlugin(projectId: string, pluginId: string) {
  const client = useQueryClient()
  return useMutation<Schemas['ProjectPlugin'], ApiError, Schemas['ProjectPluginUpdate']>({
    mutationFn: (body) => call(api.PUT('/projects/{projectId}/plugins/{pluginId}', { ...projectPluginPath(projectId, pluginId), body })),
    onSuccess: async (saved) => {
      client.setQueryData<Schemas['ProjectPluginList']>(pluginKeys.project(projectId), (list) =>
        list && { plugins: list.plugins.map((item) => (item.pluginId === saved.pluginId ? saved : item)) },
      )
      await Promise.all([invalidatePlugins(client), invalidateModelCatalog(client)])
    },
  })
}

export function useCreatePluginAccount(projectId: string, pluginId: string) {
  const client = useQueryClient()
  return useMutation<Schemas['PluginAccount'], ApiError, Schemas['PluginAccountCreate']>({
    mutationFn: (body) => call(api.POST('/projects/{projectId}/plugins/{pluginId}/accounts', { ...projectPluginPath(projectId, pluginId), body })),
    onSuccess: () => client.invalidateQueries({ queryKey: pluginKeys.accounts(projectId, pluginId) }),
  })
}

export function useUpdatePluginAccount(projectId: string, pluginId: string, accountId: string) {
  const client = useQueryClient()
  return useMutation<Schemas['PluginAccount'], ApiError, Schemas['PluginAccountUpdate']>({
    mutationFn: (body) =>
      call(api.PUT('/projects/{projectId}/plugins/{pluginId}/accounts/{accountId}', { params: { path: { projectId, pluginId, accountId } }, body })),
    onSuccess: () => client.invalidateQueries({ queryKey: pluginKeys.accounts(projectId, pluginId) }),
  })
}

// Delete an account: references to it in Agent grants are removed with it, and the Agents' plugin sections are invalidated too.
export function useDeletePluginAccount(projectId: string, pluginId: string) {
  const client = useQueryClient()
  return useMutation<void, ApiError, string>({
    mutationFn: async (accountId) => {
      await call(api.DELETE('/projects/{projectId}/plugins/{pluginId}/accounts/{accountId}', { params: { path: { projectId, pluginId, accountId } } }))
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: pluginKeys.accounts(projectId, pluginId) })
      await invalidatePlugins(client)
    },
  })
}

// ---------- Console action buttons ----------

// Actions declared in the plugin manifest's actions: where is the page object the button sits on (an empty object when global), and
// input is the form values generated from the action's fields.
export function useInvokePluginAction(pluginId: string, actionId: string) {
  return useMutation<Schemas['PluginActionResult'], ApiError, Schemas['PluginActionInput']>({
    mutationFn: (body) => call(api.POST('/plugins/{pluginId}/actions/{actionId}', { params: { path: { pluginId, actionId } }, body })),
  })
}

// ---------- Agent grants ----------

export function useSaveAgentPlugin(agentId: string, pluginId: string) {
  const client = useQueryClient()
  return useMutation<Schemas['AgentPlugin'], ApiError, Schemas['AgentPluginUpdate']>({
    mutationFn: (body) => call(api.PUT('/agents/{agentId}/plugins/{pluginId}', { params: { path: { agentId, pluginId } }, body })),
    onSuccess: async (saved) => {
      client.setQueryData<Schemas['AgentPluginList']>(pluginKeys.agent(agentId), (list) =>
        list && { plugins: list.plugins.map((item) => (item.pluginId === saved.pluginId ? saved : item)) },
      )
      await Promise.all([client.invalidateQueries({ queryKey: pluginKeys.list }), invalidateModelCatalog(client)])
    },
  })
}
