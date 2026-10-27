import { useInfiniteQuery, useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { api, call, type ApiError, type Schemas } from './client'

type Plugin = Schemas['Plugin']
type ConfigUpdate = Schemas['PluginConfigUpdate']
export type DeliveryFilter = { status?: Schemas['PluginDelivery']['status']; projectId?: string }

// 插件（第二版）。['plugins'] 是全部插件查询的共同前缀：安装、更新、卸载、启用、授权之后整体失效，
// 列表上的启用项目数与授权 Agent 数随之刷新。
export const pluginKeys = {
  all: ['plugins'] as const,
  list: ['plugins', 'list'] as const,
  detail: (pluginId: string) => ['plugins', 'detail', pluginId] as const,
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

// ---------- 读取 ----------

export function usePlugins() {
  return useQuery({ queryKey: pluginKeys.list, queryFn: () => call(api.GET('/plugins')) })
}

export function usePlugin(pluginId: string) {
  return useQuery({ queryKey: pluginKeys.detail(pluginId), queryFn: () => call(api.GET('/plugins/{pluginId}', pluginPath(pluginId))) })
}

export function usePluginConfig(pluginId: string) {
  return useQuery({ queryKey: pluginKeys.config(pluginId), queryFn: () => call(api.GET('/plugins/{pluginId}/config', pluginPath(pluginId))) })
}

// 宿主日志：新的在前，按 nextBefore 往更早翻页。
export function usePluginLogs(pluginId: string, limit = 100) {
  return useInfiniteQuery({
    queryKey: pluginKeys.logs(pluginId),
    initialPageParam: undefined as number | undefined,
    queryFn: ({ pageParam }) => call(api.GET('/plugins/{pluginId}/logs', { params: { path: { pluginId }, query: { limit, ...(pageParam ? { before: pageParam } : {}) } } })),
    getNextPageParam: (page) => page.nextBefore ?? undefined,
  })
}

// 观察类事件对这个插件的投递：按事件时间新的在前，可按状态、项目筛选。
export function usePluginDeliveries(pluginId: string, filter: DeliveryFilter = {}, limit = 50) {
  return useInfiniteQuery({
    queryKey: pluginKeys.deliveries(pluginId, filter),
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      call(api.GET('/plugins/{pluginId}/deliveries', { params: { path: { pluginId }, query: { limit, ...filter, ...(pageParam ? { before: pageParam } : {}) } } })),
    getNextPageParam: (page) => page.nextBefore ?? undefined,
  })
}

// 插件的持久状态（只读），例如投递记录约定的 deliveries/ 前缀。
export function usePluginState(pluginId: string, prefix: string, projectId?: string) {
  return useQuery({
    queryKey: pluginKeys.state(pluginId, prefix, projectId),
    queryFn: () => call(api.GET('/plugins/{pluginId}/state', { params: { path: { pluginId }, query: { prefix, ...(projectId ? { projectId } : {}) } } })),
  })
}

// 宿主状态会自己变化（启动、崩溃后重启），插件市场页停留时定期刷新。
export function usePluginHost(refetchInterval: number | false = 10_000) {
  return useQuery({ queryKey: pluginKeys.host, queryFn: () => call(api.GET('/plugin-host')), refetchInterval })
}

// 随平台发布的官方插件，安装入口的“官方”来源里列出；按 id 安装（source kind builtin）。
export function useBuiltinPluginSources() {
  return useQuery({ queryKey: pluginKeys.builtin, queryFn: () => call(api.GET('/plugin-sources/builtin')) })
}

// 各市场的插件（官方市场与全局设置里加的市场），标出已安装的版本与可更新；某个市场读取失败时它的 error 不为空。
export function usePluginMarkets() {
  return useQuery({ queryKey: pluginKeys.markets, queryFn: () => call(api.GET('/plugin-markets')) })
}

// 已安装的插件在它来源市场里的条目；不是从市场装的、或市场里已没有它时为 undefined。
export function marketEntryOf(markets: Schemas['PluginMarket'][] | undefined, plugin: Pick<Schemas['Plugin'], 'id' | 'source'>) {
  if (!plugin.source.market) return undefined
  return markets?.find((market) => market.url === plugin.source.market)?.plugins.find((entry) => entry.id === plugin.id)
}

// 市场里的来源：按市场地址与条目 id 安装；version 给出时装这个版本（更新时用市场里的新版本）。
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

// 这个插件在某个项目里的启用状态与项目级配置（取自项目插件列表）。
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

// ---------- 安装、更新、卸载 ----------

// 第一步：取来插件并读出清单。新安装给 source，更新前预览给 pluginId。
export function previewPlugin(body: Schemas['PluginPreviewRequest']) {
  return call(api.POST('/plugin-previews', { body }))
}

// 第二步：按预览安装或更新。非官方来源须带 confirm（人在确认框里看过之后）。
export function usePluginInstall() {
  const client = useQueryClient()
  return useMutation<Plugin, ApiError, { preview: Schemas['PluginPreview']; confirm: boolean }>({
    mutationFn: ({ preview, confirm }) => {
      const body: Schemas['PluginInstall'] = { previewId: preview.previewId, ...(confirm ? { confirm: true } : {}) }
      return preview.installedVersion === null
        ? call(api.POST('/plugins', { body }))
        : call(api.POST('/plugins/{pluginId}/update', { ...pluginPath(preview.manifest.id), body }))
    },
    onSuccess: () => invalidatePlugins(client),
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
      await invalidatePlugins(client)
    },
  })
}

// ---------- 配置、宿主、全局设置 ----------

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

// 一键停用：停止宿主、不再给新启动的 Pi 挂载 Agent 半边；宿主状态与各插件状态随之变化。
export function useSavePluginSettings() {
  const client = useQueryClient()
  return useMutation<Schemas['PluginSettings'], ApiError, Schemas['PluginSettings']>({
    mutationFn: (body) => call(api.PUT('/plugin-settings', { body })),
    onSuccess: async (settings) => {
      client.setQueryData(pluginKeys.settings, settings)
      await invalidatePlugins(client)
    },
  })
}

// ---------- 项目启用与账号 ----------

export function useSaveProjectPlugin(projectId: string, pluginId: string) {
  const client = useQueryClient()
  return useMutation<Schemas['ProjectPlugin'], ApiError, Schemas['ProjectPluginUpdate']>({
    mutationFn: (body) => call(api.PUT('/projects/{projectId}/plugins/{pluginId}', { ...projectPluginPath(projectId, pluginId), body })),
    onSuccess: async (saved) => {
      client.setQueryData<Schemas['ProjectPluginList']>(pluginKeys.project(projectId), (list) =>
        list && { plugins: list.plugins.map((item) => (item.pluginId === saved.pluginId ? saved : item)) },
      )
      await invalidatePlugins(client)
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

// 删除账号：各 Agent 授权里对它的引用随之移除，Agent 的插件段一并失效。
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

// ---------- 控制台操作按钮 ----------

// 插件清单 actions 声明的操作：where 是按钮所在的页面对象（全局为空对象），input 是按操作的 fields 生成的表单值。
export function useInvokePluginAction(pluginId: string, actionId: string) {
  return useMutation<Schemas['PluginActionResult'], ApiError, Schemas['PluginActionInput']>({
    mutationFn: (body) => call(api.POST('/plugins/{pluginId}/actions/{actionId}', { params: { path: { pluginId, actionId } }, body })),
  })
}

// ---------- Agent 授权 ----------

export function useSaveAgentPlugin(agentId: string, pluginId: string) {
  const client = useQueryClient()
  return useMutation<Schemas['AgentPlugin'], ApiError, Schemas['AgentPluginUpdate']>({
    mutationFn: (body) => call(api.PUT('/agents/{agentId}/plugins/{pluginId}', { params: { path: { agentId, pluginId } }, body })),
    onSuccess: async (saved) => {
      client.setQueryData<Schemas['AgentPluginList']>(pluginKeys.agent(agentId), (list) =>
        list && { plugins: list.plugins.map((item) => (item.pluginId === saved.pluginId ? saved : item)) },
      )
      await client.invalidateQueries({ queryKey: pluginKeys.list })
    },
  })
}
