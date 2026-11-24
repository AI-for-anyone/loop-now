import { useInfiniteQuery, useMutation, useQueries, useQuery, useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query'
import { config } from '@/config'
import { lastScope, PLATFORM, renamedHere, scopeQueryKey, type ScopeKey, type ScopeQuery } from '@/lib/scope'
import { api, ApiError, archiveBody, call, requestIdOf, toApiError, type Schemas } from './client'
import { sleepProjections } from '@/features/sleep/projection'

export const keys = {
  context: ['context'] as const,
  health: ['health'] as const,
  // The provider catalog has no version number and no push updates: ['modelProviders'] is the common prefix of the global catalog and of
  // each Agent's view of it, and the whole lot is invalidated after a plugin is installed, updated, uninstalled, enabled in a project or
  // granted, after plugins are disabled globally, and after models.json is saved.
  modelProviders: ['modelProviders'] as const,
  agentModelProviders: (agentId: string) => ['modelProviders', 'agent', agentId] as const,
  newAgentModelProviders: (scope: ScopeKey) => ['modelProviders', 'new', scope] as const,
  allModels: ['modelProviders', 'models'] as const,
  runtimes: ['runtimes'] as const,
  runtimeDefinitions: ['runtimeDefinitions'] as const,
  modelSettings: ['modelSettings'] as const,
  models: (providerId: string) => ['models', providerId] as const,
  cliTemplates: ['cliTemplates'] as const,
  projects: ['projects'] as const,
  // There is one Agent list per scope (project ID or platform); ['agents'] is their common prefix, used to invalidate them all at once.
  agents: ['agents'] as const,
  scopeAgents: (scope: ScopeKey) => ['agents', scope] as const,
  agent: (agentId: string) => ['agent', agentId] as const,
  agentStatus: (agentId: string) => ['agentStatus', agentId] as const,
  agentQueue: (agentId: string) => ['agentQueue', agentId] as const,
  agentSkills: (agentId: string) => ['agentSkills', agentId] as const,
  agentCommands: (agentId: string) => ['agentCommands', agentId] as const,
  agentSkill: (agentId: string, agentSkillId: string) => ['agentSkill', agentId, agentSkillId] as const,
  // Conversation history: paged backward from the latest page; jumping to a specific reply uses a separate cache centered on it
  // (same prefix, so invalidation and reconciliation cover both).
  messages: (agentId: string) => ['messages', agentId] as const,
  messagesAround: (agentId: string, runId: string) => ['messages', agentId, 'around', runId] as const,
  timeline: (agentId: string, runId: string) => ['timeline', agentId, runId] as const,
  usage: (range: string, scope = 'all') => ['usage', range, scope] as const,
  anomalies: (scope = 'all') => ['anomalies', scope] as const,
  skills: ['skills'] as const,
  skill: (skillId: string) => ['skill', skillId] as const,
  // Inbox. The thread list and the inboxes of Agents and external identities all use cursor pagination; the various filters of the same
  // Agent or external identity share a prefix and are invalidated together when an event arrives.
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

// ---- Reads ----

const contextQuery = { queryKey: keys.context, queryFn: () => call(api.GET('/context')), staleTime: Infinity }

export function useContextInfo() {
  return useQuery(contextQuery)
}


// The health check returns 503 when the database is unavailable, with a Health body all the same; both are returned as data here.
// The top bar always shows the connection status: poll at the Agent list's refresh interval, and also when the window regains focus.
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

// Who the catalog's usable flags are for: an Agent (its id), or a new Agent in a project or the platform (a scope key).
export type CatalogFor = string | { newIn: ScopeKey }

// With an Agent, plugin providers also give the reason this Agent cannot use them right now (unavailableReason), and every provider whether
// it can run without entering an account or key first (usable); for a new Agent, usable is asked of its project or the platform. Accounts,
// project enablement and grants can change elsewhere, so these are not cached for long and are re-read when a page opens and when the
// window regains focus.
export function useModelProviders(subject?: CatalogFor) {
  const query =
    subject === undefined ? undefined : typeof subject === 'string' ? { agentId: subject } : subject.newIn === PLATFORM ? { scope: 'platform' as const } : { projectId: subject.newIn }
  return useQuery({
    queryKey: subject === undefined ? keys.modelProviders : typeof subject === 'string' ? keys.agentModelProviders(subject) : keys.newAgentModelProviders(subject.newIn),
    queryFn: () => call(api.GET('/model-providers', query ? { params: { query } } : {})),
    staleTime: subject === undefined ? Infinity : 0,
  })
}

// Every provider with its models in one read (include=models), for the runner picker's provider groups and its search across them; a
// provider without a models list (a server that ignores include) maps to undefined. The catalog changes only with plugins and models.json
// (invalidateModelCatalog), so it is read once.
export function useAllModels(enabled: boolean) {
  return useQuery({
    queryKey: keys.allModels,
    queryFn: () => call(api.GET('/model-providers', { params: { query: { include: 'models' } } })),
    select: (data) => new Map(data.providers.map((provider) => [provider.id, provider.models] as const)),
    staleTime: Infinity,
    enabled,
  })
}

// Whether each runtime is available on this machine (whether a runtime that is not bundled is installed and logged in). Answering it
// runs the runtimes' CLIs on this machine; the server caches the result for 30 seconds, and likewise it is not re-read here within
// 30 seconds; nothing is read while enabled is false (model selection only needs it when such a runtime is chosen).
export function useRuntimes(enabled = true) {
  return useQuery({ queryKey: keys.runtimes, queryFn: () => call(api.GET('/runtimes')), staleTime: 30_000, enabled })
}

// The fixed definition of each runtime: name, model providers, capabilities. Reading it checks nothing on this machine and it changes
// only when the server is upgraded or a local ACP command is added, changed or removed (api/acp-commands.ts reads it again then), so it
// is read once. Components use it through useRuntimeLookup (lib/runtime.ts).
export function useRuntimeDefinitions() {
  return useQuery({ queryKey: keys.runtimeDefinitions, queryFn: () => call(api.GET('/runtime-definitions')), staleTime: Infinity })
}

// Re-check without the server's cache (refresh=true) and put the result straight into the useRuntimes cache.
export function useRefreshRuntimes() {
  const client = useQueryClient()
  return useMutation<Schemas['RuntimeList'], ApiError, void>({
    mutationFn: () => call(api.GET('/runtimes', { params: { query: { refresh: true } } })),
    onSuccess: (data) => client.setQueryData(keys.runtimes, data),
  })
}

// A plugin changed the provider catalog or whether an Agent can use it: re-read the catalog and every provider's model list together.
export const invalidateModelCatalog = (client: QueryClient) =>
  Promise.all([client.invalidateQueries({ queryKey: keys.modelProviders }), client.invalidateQueries({ queryKey: ['models'] })])

export function useModels(providerId: string | null) {
  return useQuery({
    queryKey: keys.models(providerId ?? ''),
    queryFn: () => call(api.GET('/model-providers/{providerId}/models', { params: { path: { providerId: providerId! } } })),
    enabled: providerId !== null && providerId !== '',
    staleTime: Infinity,
  })
}

// The models of several providers at once, for the runner picker's provider groups and its search across them; shares useModels' cache.
export function useModelsOf(providerIds: readonly string[]) {
  return useQueries({
    queries: providerIds.map((providerId) => ({
      queryKey: keys.models(providerId),
      queryFn: () => call(api.GET('/model-providers/{providerId}/models', { params: { path: { providerId } } })),
      staleTime: Infinity,
    })),
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
      await invalidateModelCatalog(client)
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

// Projects that are not deleted, the default project first and the rest sorted by name (the server's order). Polled like the Agent list.
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

// The Agents of a project or of the platform; no request while scope is null (the scope is not known yet).
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

// Commands for the "/" menu on the conversation page: the platform's built-in commands (with whether each can be used right now) and Pi
// commands. Read only when "/" is first typed; when we must check before sending whether some text is a platform command, the same query
// is fetched on the spot (no retry, so sending does not wait for nothing). Whether a platform command can be used depends only on the
// runtime and the session state; the server pushes session.updated every time either one changes, and the conversation page re-reads when
// it receives that (see useAgentLive); there is no extra read after the session is restarted or the config changes.
export const agentCommandsQuery = (agentId: string) => ({
  queryKey: keys.agentCommands(agentId),
  queryFn: () => call(api.GET('/agents/{agentId}/commands', agentPath(agentId))),
  staleTime: 30_000,
})

export function useAgentCommands(agentId: string, enabled: boolean) {
  return useQuery({ ...agentCommandsQuery(agentId), enabled })
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

// Conversation history. Without aroundRun it starts from the latest page; with it, the first page is centered on that reply, and from
// there you can page in both directions.
// pages[0] is always the latest loaded page, and each later page is older; within a page, messages are in chronological order.
// Paging to older messages uses fetchNextPage (before), paging to newer ones uses fetchPreviousPage (after); the latter inserts the new
// page at the front.
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

// The run log of one run, requested only when it is expanded. A run that has ended no longer changes; one in progress is refetched by
// the conversation page as events arrive.
export function useRunTimeline(agentId: string, runId: string, enabled: boolean, ended: boolean) {
  return useQuery({
    queryKey: keys.timeline(agentId, runId),
    queryFn: () => call(api.GET('/agents/{agentId}/runs/{runId}/timeline', { params: { path: { agentId, runId } } })),
    enabled,
    staleTime: ended ? Infinity : 0,
  })
}

// Usage: from is computed on every request, so the bounds of the period move with time (e.g. “近 30 天” (Last 30 days)). Without a scope
// everything is counted, otherwise only that project or the platform.
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

// Anomalies and failures: the start time is fixed when the first page is requested and later pages reuse it; together with the cursor
// this makes sure paging neither repeats nor skips anything.
// A periodic refresh starts again from the first page, and the start time is updated with it.
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

// ---- Cache updates ----

// Update all of this Agent's history caches: the latest one, and the one used when jumping to a specific reply.
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

// Of two copies of the same run, take the newer one. The response to the send request can arrive later than the end event on the event
// stream, and an older state must not overwrite a newer one.
export function newerRun(current: Schemas['Run'], incoming: Schemas['Run']): Schemas['Run'] {
  return RUN_STAGE[incoming.status] >= RUN_STAGE[current.status] ? incoming : current
}

// Insert or replace a message in the history by message ID: replace it if it already exists (the run state never goes backward), otherwise
// append it to the end of the latest page.
// A cache positioned on a reply that has not loaded up to the latest page does not get the message appended; the conversation page points
// out new messages instead of inserting them in the middle.
export function upsertMessage(client: QueryClient, agentId: string, message: Schemas['Message']) {
  updateMessages(client, agentId, (messages, index, data) => {
    const exists = data.pages.some((page) => page.messages.some((m) => m.id === message.id))
    if (exists) return messages.map((m) => (m.id === message.id ? { ...message, run: newerRun(m.run, message.run) } : m))
    return index === 0 && !data.pages[0].hasMoreAfter ? [...messages, message] : messages
  })
}

// A user message and its reply share the same run, so both are updated when the run state changes (never backward).
export function updateRunInMessages(client: QueryClient, agentId: string, run: Schemas['Run']) {
  updateMessages(client, agentId, (messages) => messages.map((m) => (m.run.id === run.id ? { ...m, run: newerRun(m.run, run) } : m)))
}

// ---- Credentials ----
// Plaintext credentials do not go through TanStack Query's mutation cache (it keeps the call arguments); forms submit them directly
// from local state.

export function putModelCredential(agentId: string, body: Schemas['ModelCredentialInput']) {
  return call(api.PUT('/agents/{agentId}/model-credential', { ...agentPath(agentId), body }))
}

export function putCliCredential(agentId: string, cli: string, fields: Record<string, string>) {
  return call(api.PUT('/agents/{agentId}/clis/{cli}/credential', { params: { path: { agentId, cli } }, body: { fields } }))
}

// After a credential is submitted successfully, refresh the credential metadata in the Agent details.
export function invalidateAgent(client: QueryClient, agentId: string) {
  return Promise.all([client.invalidateQueries({ queryKey: keys.agent(agentId) }), client.invalidateQueries({ queryKey: keys.agents })])
}

// ---- Mutations ----

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

// Create an Agent in a project or the platform. The project's Agent count changes with it, so the project list is refreshed too.
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
      // Deleting the Agent that is the Manager removes the project's Manager, and the project's Agent count changes too.
      await Promise.all([client.invalidateQueries({ queryKey: keys.agents }), client.invalidateQueries({ queryKey: keys.projects })])
    },
  })
}


// ---- Projects ----

const projectPath = (projectId: string) => ({ params: { path: { projectId } } })

// Write the project returned by the server back into the project list. After a rename, the URL with the new name and the project list
// agree within the same render.
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

// Delete a project: on success, clear the last-used scope if it was this project; its Agent list is no longer needed.
// The details (ProjectNotIdleDetails) of a 409 agent_not_idle stay in the ApiError unchanged, and the page lists the busy Agents.
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

// Assigning or removing the Manager changes the project's managerAgentId and the isManager of the old and new Manager (present in both
// the list and the details).
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

// The result of sending: usually the new message and its run; a command that starts with / and is claimed by a plugin returns the plugin's
// reply (ConsoleCommandResult) and creates no message.
export type SendOutcome = Schemas['SendMessageResult'] | Schemas['ConsoleCommandResult']
export const isCommandResult = (outcome: SendOutcome): outcome is Schemas['ConsoleCommandResult'] => 'handled' in outcome

export function useSendMessage(agentId: string) {
  return useMutation<SendOutcome, ApiError, Schemas['SendMessageInput']>({
    mutationFn: (body) => call(api.POST('/agents/{agentId}/messages', { ...agentPath(agentId), body })),
  })
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
