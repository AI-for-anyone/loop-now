import { useInfiniteQuery, useMutation, useQuery, useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query'
import { config } from '@/config'
import { lastScope, PLATFORM, renamedHere, scopeQueryKey, type ScopeKey, type ScopeQuery } from '@/lib/scope'
import { api, ApiError, archiveBody, call, requestIdOf, toApiError, type Schemas } from './client'
import { sleepProjections } from '@/features/sleep/projection'

export const keys = {
  context: ['context'] as const,
  health: ['health'] as const,
  modelProviders: ['modelProviders'] as const,
  modelSettings: ['modelSettings'] as const,
  models: (providerId: string) => ['models', providerId] as const,
  cliTemplates: ['cliTemplates'] as const,
  projects: ['projects'] as const,
  // Agent 列表按作用域（项目 ID 或 platform）各有一份；['agents'] 是它们共同的前缀，用于整体失效。
  agents: ['agents'] as const,
  scopeAgents: (scope: ScopeKey) => ['agents', scope] as const,
  agent: (agentId: string) => ['agent', agentId] as const,
  agentStatus: (agentId: string) => ['agentStatus', agentId] as const,
  agentQueue: (agentId: string) => ['agentQueue', agentId] as const,
  agentSkills: (agentId: string) => ['agentSkills', agentId] as const,
  agentSkill: (agentId: string, agentSkillId: string) => ['agentSkill', agentId, agentSkillId] as const,
  // 对话历史：最新的一页起向前翻；定位到某条回复时另用一份以它为中心的缓存（前缀相同，失效与对账一并处理）。
  messages: (agentId: string) => ['messages', agentId] as const,
  messagesAround: (agentId: string, runId: string) => ['messages', agentId, 'around', runId] as const,
  timeline: (agentId: string, runId: string) => ['timeline', agentId, runId] as const,
  usage: (range: string, scope = 'all') => ['usage', range, scope] as const,
  anomalies: (scope = 'all') => ['anomalies', scope] as const,
  skills: ['skills'] as const,
  skill: (skillId: string) => ['skill', skillId] as const,
  // 收件箱。线索列表与 Agent、外部身份的收件箱都是游标分页；同一个 Agent 或外部身份的各种筛选共用前缀，事件到达时一并失效。
  inboxThreads: ['inboxThreads'] as const,
  inboxThread: (threadId: string) => ['inboxThread', threadId] as const,
  agentInbox: (agentId: string, unreadOnly?: boolean) =>
    unreadOnly === undefined ? (['agentInbox', agentId] as const) : (['agentInbox', agentId, unreadOnly ? 'unread' : 'all'] as const),
  agentInboxMessage: (agentId: string, messageId: string) => ['agentInboxMessage', agentId, messageId] as const,
  identities: ['identities'] as const,
  identity: (identityId: string) => ['identity', identityId] as const,
  identityTokens: (identityId: string) => ['identityTokens', identityId] as const,
  identityInbox: (identityId: string, direction?: InboxDirection, unreadOnly?: boolean) =>
    direction === undefined
      ? (['identityInbox', identityId] as const)
      : (['identityInbox', identityId, direction, unreadOnly ? 'unread' : 'all'] as const),
  identityInboxMessage: (identityId: string, messageId: string) => ['identityInboxMessage', identityId, messageId] as const,
  fileContent: (agentId: string, sourceId: string, path: string) => ['fileContent', agentId, sourceId, path] as const,
}

export type InboxDirection = 'all' | 'received' | 'sent'

const agentPath = (agentId: string) => ({ params: { path: { agentId } } })

// ---- 读取 ----

const contextQuery = { queryKey: keys.context, queryFn: () => call(api.GET('/context')), staleTime: Infinity }

export function useContextInfo() {
  return useQuery(contextQuery)
}


// 健康检查在依赖不可用时返回 503，响应体同样是 Health，这里两种都作为数据返回。
// 顶栏常驻显示连接状态：按 Agent 列表的刷新间隔定期查询，窗口重新获得焦点时也查询。
export function useHealth() {
  return useQuery({
    refetchInterval: config.agentListPollMs,
    refetchOnWindowFocus: true,
    queryKey: keys.health,
    queryFn: async (): Promise<Schemas['Health']> => {
      let result
      try {
        result = await api.GET('/health')
      } catch {
        throw new ApiError({ status: 0, code: 'unreachable' })
      }
      if (result.response.ok && result.data) return result.data
      if (result.response.status === 503 && result.error) return result.error as Schemas['Health']
      throw toApiError(result.response.status, result.error, requestIdOf(result.response))
    },
  })
}

export function useModelProviders() {
  return useQuery({ queryKey: keys.modelProviders, queryFn: () => call(api.GET('/model-providers')), staleTime: Infinity })
}

export function useModels(providerId: string | null) {
  return useQuery({
    queryKey: keys.models(providerId ?? ''),
    queryFn: () => call(api.GET('/model-providers/{providerId}/models', { params: { path: { providerId: providerId! } } })),
    enabled: providerId !== null && providerId !== '',
    staleTime: Infinity,
  })
}

const getModelSettings = () => call(api.GET('/model-settings'))

export function useModelSettings() {
  return useQuery({ queryKey: keys.modelSettings, queryFn: getModelSettings, staleTime: Infinity })
}

export function useSaveModelSettings() {
  const client = useQueryClient()
  const mutation = useMutation<Schemas['ModelSettings'], ApiError, Schemas['ModelSettingsPut']>({
    mutationFn: (body) => call(api.PUT('/model-settings', { body })),
    onSuccess: async (settings) => {
      client.setQueryData(keys.modelSettings, settings)
      await Promise.all([
        client.invalidateQueries({ queryKey: keys.modelProviders }),
        client.invalidateQueries({ queryKey: ['models'] }),
      ])
    },
  })
  return {
    ...mutation,
    readLatest: async () => {
      const latest = await getModelSettings()
      client.setQueryData(keys.modelSettings, latest)
      return latest
    },
  }
}

export function useCliTemplates() {
  return useQuery({ queryKey: keys.cliTemplates, queryFn: () => call(api.GET('/cli-templates')) })
}

// 未删除的项目，默认项目在前，其余按名称排序（服务端的顺序）。与 Agent 列表同样定期刷新。
export function useProjects() {
  return useQuery({
    queryKey: keys.projects,
    queryFn: () => call(api.GET('/projects')),
    refetchInterval: config.agentListPollMs,
    refetchOnWindowFocus: true,
  })
}

const listAgents = (scope: ScopeKey) =>
  scope === PLATFORM
    ? call(api.GET('/platform/agents'))
    : call(api.GET('/projects/{projectId}/agents', { params: { path: { projectId: scope } } }))

// 某个项目或平台中的 Agent；scope 为 null 时（作用域尚未确定）不请求。
export function useAgents(scope: ScopeKey | null) {
  const client = useQueryClient()
  return useQuery({
    queryKey: keys.scopeAgents(scope ?? ''),
    queryFn: async () => sleepProjections(client).list(await listAgents(scope!)),
    enabled: scope !== null,
    refetchInterval: config.agentListPollMs,
    refetchOnWindowFocus: true,
  })
}

export function useAgent(agentId: string, refetchInterval?: number) {
  return useQuery({ queryKey: keys.agent(agentId), queryFn: () => call(api.GET('/agents/{agentId}', agentPath(agentId))), refetchInterval })
}

export function useAgentStatus(agentId: string, refetchInterval?: number) {
  const client = useQueryClient()
  return useQuery({
    queryKey: keys.agentStatus(agentId),
    queryFn: async () => sleepProjections(client).status(await call(api.GET('/agents/{agentId}/status', agentPath(agentId)))),
    refetchInterval,
  })
}

export function useAgentSkills(agentId: string) {
  return useQuery({ queryKey: keys.agentSkills(agentId), queryFn: () => call(api.GET('/agents/{agentId}/skills', agentPath(agentId))) })
}

export function useAgentSkill(agentId: string, agentSkillId: string | null) {
  return useQuery({
    queryKey: keys.agentSkill(agentId, agentSkillId ?? ''),
    queryFn: () =>
      call(api.GET('/agents/{agentId}/skills/{agentSkillId}', { params: { path: { agentId, agentSkillId: agentSkillId! } } })),
    enabled: agentSkillId !== null,
  })
}

export type MessagePages = InfiniteData<Schemas['MessagePage'], unknown>

type MessagePageParam = { before: string } | { after: string } | { aroundRun: string } | null

// 对话历史。不传 aroundRun 时从最新的一页开始；传入时第一页以这条回复为中心，之后可以向两边翻。
// pages[0] 总是已加载的最新一页，之后的页依次更早；每页内按时间正序。
// 向更早翻用 fetchNextPage（before），向更新翻用 fetchPreviousPage（after），后者把新页插在最前面。
export function useMessages(agentId: string, aroundRun: string | null = null) {
  return useInfiniteQuery({
    queryKey: aroundRun ? keys.messagesAround(agentId, aroundRun) : keys.messages(agentId),
    queryFn: ({ pageParam }) =>
      call(
        api.GET('/agents/{agentId}/messages', {
          params: { path: { agentId }, query: { limit: config.messagePageSize, ...(pageParam ?? {}) } },
        }),
      ),
    initialPageParam: (aroundRun ? { aroundRun } : null) as MessagePageParam,
    getNextPageParam: (lastPage): MessagePageParam | undefined =>
      lastPage.hasMore && lastPage.messages.length > 0 ? { before: lastPage.messages[0].id } : undefined,
    getPreviousPageParam: (firstPage): MessagePageParam | undefined =>
      firstPage.hasMoreAfter && firstPage.messages.length > 0 ? { after: firstPage.messages.at(-1)!.id } : undefined,
  })
}

// 一次执行的运行记录，展开时才请求。已结束的执行不再变化；进行中的由对话页按事件重新获取。
export function useRunTimeline(agentId: string, runId: string, enabled: boolean, ended: boolean) {
  return useQuery({
    queryKey: keys.timeline(agentId, runId),
    queryFn: () => call(api.GET('/agents/{agentId}/runs/{runId}/timeline', { params: { path: { agentId, runId } } })),
    enabled,
    staleTime: ended ? Infinity : 0,
  })
}

// 用量：from 在每次请求时计算，时间段的边界随时间推移（例如“近 30 天”）。不给范围时统计全部，否则只统计该项目或平台。
export function useUsage(range: string, from: () => string, scope: ScopeQuery = {}) {
  return useQuery({
    queryKey: keys.usage(range, scopeQueryKey(scope)),
    queryFn: () => call(api.GET('/usage', { params: { query: { from: from(), ...scope } } })),
    refetchInterval: config.overviewPollMs,
    refetchOnWindowFocus: true,
  })
}

export type AnomalyPage = Schemas['AnomalyPage'] & { from: string }
type AnomalyPageParam = { from: string; cursor: string } | null

// 异常与失败：第一页请求时确定开始时间，之后的页沿用它，与游标一起保证翻页不重复、不缺失。
// 定期刷新时从第一页重新开始，开始时间随之更新。
export function useAnomalies(from: () => string, scope: ScopeQuery = {}) {
  return useInfiniteQuery({
    queryKey: keys.anomalies(scopeQueryKey(scope)),
    queryFn: async ({ pageParam }): Promise<AnomalyPage> => {
      const start = pageParam?.from ?? from()
      const page = await call(
        api.GET('/anomalies', {
          params: { query: { from: start, limit: config.anomalyPageSize, ...scope, ...(pageParam ? { cursor: pageParam.cursor } : {}) } },
        }),
      )
      return { ...page, from: start }
    },
    initialPageParam: null as AnomalyPageParam,
    getNextPageParam: (lastPage): AnomalyPageParam | undefined =>
      lastPage.nextCursor ? { from: lastPage.from, cursor: lastPage.nextCursor } : undefined,
    refetchInterval: config.overviewPollMs,
    refetchOnWindowFocus: true,
  })
}

export function useSkills() {
  return useQuery({ queryKey: keys.skills, queryFn: () => call(api.GET('/skills')) })
}

export function useSkill(skillId: string) {
  return useQuery({
    queryKey: keys.skill(skillId),
    queryFn: () => call(api.GET('/skills/{skillId}', { params: { path: { skillId } } })),
  })
}

// ---- 缓存更新 ----

// 更新这个 Agent 的全部历史缓存：最新的一份，以及定位到某条回复时的那份。
function updateMessages(
  client: QueryClient,
  agentId: string,
  update: (messages: Schemas['Message'][], pageIndex: number, data: MessagePages) => Schemas['Message'][],
) {
  client.setQueriesData<MessagePages>({ queryKey: keys.messages(agentId) }, (data) => {
    if (!data) return data
    return { ...data, pages: data.pages.map((page, index) => ({ ...page, messages: update(page.messages, index, data) })) }
  })
}

const RUN_STAGE: Record<Schemas['RunStatus'], number> = {
  queued: 0,
  starting: 1,
  running: 2,
  aborting: 3,
  completed: 4,
  failed: 4,
  interrupted: 4,
}

// 两份同一执行的数据中取更新的一份。发送请求的响应可能晚于事件流中的结束事件到达，
// 不能让较早的状态覆盖较新的状态。
export function newerRun(current: Schemas['Run'], incoming: Schemas['Run']): Schemas['Run'] {
  return RUN_STAGE[incoming.status] >= RUN_STAGE[current.status] ? incoming : current
}

// 按消息 ID 插入或替换历史中的消息：已存在就替换（执行状态不倒退），否则追加到最新一页末尾。
// 定位时还没有加载到最新的那份缓存不追加，新消息由对话页提示，不插在中间。
export function upsertMessage(client: QueryClient, agentId: string, message: Schemas['Message']) {
  updateMessages(client, agentId, (messages, index, data) => {
    const exists = data.pages.some((page) => page.messages.some((m) => m.id === message.id))
    if (exists) return messages.map((m) => (m.id === message.id ? { ...message, run: newerRun(m.run, message.run) } : m))
    return index === 0 && !data.pages[0].hasMoreAfter ? [...messages, message] : messages
  })
}

// 用户消息与回复共用同一个执行，执行状态变化时一并更新（不倒退）。
export function updateRunInMessages(client: QueryClient, agentId: string, run: Schemas['Run']) {
  updateMessages(client, agentId, (messages) => messages.map((m) => (m.run.id === run.id ? { ...m, run: newerRun(m.run, run) } : m)))
}

// ---- 凭证 ----
// 凭证明文不经过 TanStack Query 的变更缓存（它会保留调用参数），由表单以局部状态直接提交。

export function putModelCredential(agentId: string, body: Schemas['ModelCredentialInput']) {
  return call(api.PUT('/agents/{agentId}/model-credential', { ...agentPath(agentId), body }))
}

export function putCliCredential(agentId: string, cli: string, fields: Record<string, string>) {
  return call(api.PUT('/agents/{agentId}/clis/{cli}/credential', { params: { path: { agentId, cli } }, body: { fields } }))
}

// 凭证提交成功后，刷新 Agent 详情中的凭证元数据。
export function invalidateAgent(client: QueryClient, agentId: string) {
  return Promise.all([client.invalidateQueries({ queryKey: keys.agent(agentId) }), client.invalidateQueries({ queryKey: keys.agents })])
}

// ---- 变更 ----

function useInvalidate() {
  const client = useQueryClient()
  return (...queryKeys: readonly (readonly unknown[])[]) => Promise.all(queryKeys.map((queryKey) => client.invalidateQueries({ queryKey })))
}

function useAgentMutation<TVariables, TData>(agentId: string, fn: (variables: TVariables) => Promise<TData>, extraKeys: readonly (readonly unknown[])[] = []) {
  const invalidate = useInvalidate()
  return useMutation<TData, ApiError, TVariables>({
    mutationFn: fn,
    onSuccess: () => invalidate(keys.agent(agentId), keys.agents, keys.agentStatus(agentId), ['credential-selections', agentId], ...extraKeys),
  })
}

// 在某个项目或平台中新建 Agent。项目的 Agent 数随之变化，项目列表一并刷新。
export function useCreateAgent(scope: ScopeKey) {
  const invalidate = useInvalidate()
  return useMutation<Schemas['Agent'], ApiError, Schemas['AgentCreate']>({
    mutationFn: (body) =>
      scope === PLATFORM
        ? call(api.POST('/platform/agents', { body }))
        : call(api.POST('/projects/{projectId}/agents', { params: { path: { projectId: scope } }, body })),
    onSuccess: () => invalidate(keys.agents, keys.projects),
  })
}

export function useUpdateAgent(agentId: string) {
  return useAgentMutation(agentId, (body: Schemas['AgentUpdate']) => call(api.PATCH('/agents/{agentId}', { ...agentPath(agentId), body })))
}

export function useDeleteAgent(agentId: string) {
  const client = useQueryClient()
  return useMutation<void, ApiError, void>({
    mutationFn: () => call(api.DELETE('/agents/{agentId}', agentPath(agentId))),
    onSuccess: async () => {
      client.removeQueries({ queryKey: keys.agent(agentId) })
      // 删除担任 Manager 的 Agent 会取消项目的 Manager，项目的 Agent 数也会变。
      await Promise.all([client.invalidateQueries({ queryKey: keys.agents }), client.invalidateQueries({ queryKey: keys.projects })])
    },
  })
}


// ---- 项目 ----

const projectPath = (projectId: string) => ({ params: { path: { projectId } } })

// 把服务端返回的项目写回项目列表。改名后，带新名称的地址与项目列表在同一次渲染中一致。
function putProject(client: QueryClient, project: Schemas['Project']) {
  client.setQueryData<{ projects: Schemas['Project'][] }>(keys.projects, (data) =>
    data ? { projects: data.projects.map((p) => (p.id === project.id ? project : p)) } : data,
  )
}

export function useCreateProject() {
  const invalidate = useInvalidate()
  return useMutation<Schemas['Project'], ApiError, Schemas['ProjectCreate']>({
    mutationFn: (body) => call(api.POST('/projects', { body })),
    onSuccess: () => invalidate(keys.projects),
  })
}

export function useRenameProject(projectId: string) {
  const client = useQueryClient()
  return useMutation<Schemas['Project'], ApiError, Schemas['ProjectUpdate']>({
    mutationFn: (body) => call(api.PATCH('/projects/{projectId}', { ...projectPath(projectId), body })),
    onSuccess: async (project) => {
      renamedHere.add(project.id)
      putProject(client, project)
      await client.invalidateQueries({ queryKey: keys.projects })
    },
  })
}

// 删除项目：成功后，最近使用的作用域若是它就清除；它的 Agent 列表不再需要。
// 409 agent_not_idle 的 details（ProjectNotIdleDetails）原样留在 ApiError 中，由页面列出忙碌的 Agent。
export function useDeleteProject(projectId: string) {
  const client = useQueryClient()
  return useMutation<void, ApiError, void>({
    mutationFn: () => call(api.DELETE('/projects/{projectId}', projectPath(projectId))),
    onSuccess: async () => {
      if (lastScope.get() === projectId) lastScope.clear()
      client.removeQueries({ queryKey: keys.scopeAgents(projectId) })
      client.removeQueries({ queryKey: ['project-credentials', projectId] })
      client.removeQueries({ queryKey: ['project-model-auth', projectId] })
      await client.invalidateQueries({ queryKey: keys.projects })
    },
  })
}

// 指定或取消 Manager 会改变项目的 managerAgentId，以及新旧 Manager 的 isManager（列表与详情中都有）。
function useManagerMutation<TVariables>(projectId: string, fn: (variables: TVariables) => Promise<Schemas['Project']>) {
  const client = useQueryClient()
  return useMutation<Schemas['Project'], ApiError, TVariables>({
    mutationFn: fn,
    onSuccess: async (project) => {
      putProject(client, project)
      await Promise.all([
        client.invalidateQueries({ queryKey: keys.projects }),
        client.invalidateQueries({ queryKey: keys.scopeAgents(projectId) }),
        client.invalidateQueries({ queryKey: ['agent'] }),
      ])
    },
  })
}

export function useSetManager(projectId: string) {
  return useManagerMutation(projectId, (agentId: string) =>
    call(api.PUT('/projects/{projectId}/manager', { ...projectPath(projectId), body: { agentId } })),
  )
}

export function useClearManager(projectId: string) {
  return useManagerMutation(projectId, () => call(api.DELETE('/projects/{projectId}/manager', projectPath(projectId))))
}

export function useDeleteModelCredential(agentId: string) {
  return useAgentMutation(agentId, () => call(api.DELETE('/agents/{agentId}/model-credential', agentPath(agentId))))
}

export function useAddCli(agentId: string) {
  return useAgentMutation(agentId, (cli: string) => call(api.PUT('/agents/{agentId}/clis/{cli}', { params: { path: { agentId, cli } } })))
}

export function useRemoveCli(agentId: string) {
  return useAgentMutation(agentId, (cli: string) => call(api.DELETE('/agents/{agentId}/clis/{cli}', { params: { path: { agentId, cli } } })))
}


export function useDeleteCliCredential(agentId: string) {
  return useAgentMutation(agentId, (cli: string) =>
    call(api.DELETE('/agents/{agentId}/clis/{cli}/credential', { params: { path: { agentId, cli } } })),
  )
}

export function useAddAgentSkill(agentId: string) {
  return useAgentMutation(
    agentId,
    (templateId: string) => call(api.POST('/agents/{agentId}/skills', { ...agentPath(agentId), body: { templateId } })),
    [keys.agentSkills(agentId)],
  )
}

export function useRemoveAgentSkill(agentId: string) {
  return useAgentMutation(
    agentId,
    (agentSkillId: string) => call(api.DELETE('/agents/{agentId}/skills/{agentSkillId}', { params: { path: { agentId, agentSkillId } } })),
    [keys.agentSkills(agentId)],
  )
}

export function useRecopyAgentSkill(agentId: string) {
  return useAgentMutation(
    agentId,
    (agentSkillId: string) =>
      call(api.POST('/agents/{agentId}/skills/{agentSkillId}/recopy', { params: { path: { agentId, agentSkillId } } })),
    [keys.agentSkills(agentId), ['agentSkill', agentId]],
  )
}

export function useUpdateAgentSkillMarkdown(agentId: string) {
  return useAgentMutation(
    agentId,
    ({ agentSkillId, content }: { agentSkillId: string; content: string }) =>
      call(api.PUT('/agents/{agentId}/skills/{agentSkillId}/skill-md', { params: { path: { agentId, agentSkillId } }, body: { content } })),
    [keys.agentSkills(agentId), ['agentSkill', agentId]],
  )
}

// 发送的结果：一般是新消息与它的执行；以 / 开头、被某个插件认领的命令返回插件的回复（ConsoleCommandResult），不产生消息。
export type SendOutcome = Schemas['SendMessageResult'] | Schemas['ConsoleCommandResult']
export const isCommandResult = (outcome: SendOutcome): outcome is Schemas['ConsoleCommandResult'] => 'handled' in outcome

export function useSendMessage(agentId: string) {
  return useMutation<SendOutcome, ApiError, Schemas['SendMessageInput']>({
    mutationFn: (body) => call(api.POST('/agents/{agentId}/messages', { ...agentPath(agentId), body })),
  })
}

export function useResetSession(agentId: string) {
  return useAgentMutation(agentId, () => call(api.POST('/agents/{agentId}/session/reset', agentPath(agentId))), [keys.agentStatus(agentId)])
}

export function useCreateSkillFromArchive() {
  const invalidate = useInvalidate()
  return useMutation<Schemas['SkillTemplate'], ApiError, Blob>({
    mutationFn: (archive) => call(api.POST('/skills', archiveBody(archive))),
    onSuccess: () => invalidate(keys.skills),
  })
}

export function useCreateSkillFromMarkdown() {
  const invalidate = useInvalidate()
  return useMutation<Schemas['SkillTemplate'], ApiError, string>({
    mutationFn: (skillMd) => call(api.POST('/skills', { body: { skillMd } })),
    onSuccess: () => invalidate(keys.skills),
  })
}

export function useReplaceSkill(skillId: string) {
  const invalidate = useInvalidate()
  return useMutation<Schemas['SkillTemplate'], ApiError, Blob>({
    mutationFn: (archive) => call(api.PUT('/skills/{skillId}', { params: { path: { skillId } }, ...archiveBody(archive) })),
    onSuccess: () => invalidate(keys.skills, keys.skill(skillId)),
  })
}

export function useUpdateSkillMarkdown(skillId: string) {
  const invalidate = useInvalidate()
  return useMutation<Schemas['SkillTemplate'], ApiError, string>({
    mutationFn: (content) => call(api.PUT('/skills/{skillId}/skill-md', { params: { path: { skillId } }, body: { content } })),
    onSuccess: () => invalidate(keys.skills, keys.skill(skillId)),
  })
}

export function useDeleteSkill(skillId: string) {
  const client = useQueryClient()
  return useMutation<void, ApiError, void>({
    mutationFn: () => call(api.DELETE('/skills/{skillId}', { params: { path: { skillId } } })),
    onSuccess: async () => {
      client.removeQueries({ queryKey: keys.skill(skillId) })
      await client.invalidateQueries({ queryKey: keys.skills })
    },
  })
}
