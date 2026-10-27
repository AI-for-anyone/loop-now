import { useInfiniteQuery, useMutation, useQuery, useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query'
import { config } from '@/config'
import { api, call, type ApiError, type Schemas } from './client'
import { keys, type InboxDirection } from './queries'

// 收件箱、外部身份与令牌的读取和修改。实时变化由收件箱事件流（features/inbox/useInboxLive）让这里的缓存失效或就地更新。

export type ThreadPages = InfiniteData<Schemas['InboxThreadPage'], string | null>
export type MessagePages = InfiniteData<Schemas['InboxMessagePage'], string | null>

const pageQuery = (cursor: string | null) => ({ limit: config.inboxPageSize, ...(cursor ? { cursor } : {}) })

// ---- 你的往来（收件箱页已去掉，见 api/console-inbox） ----

// 新建消息：每次开启新线索。收件人是外部身份时不提交 whenBusy。
export function useSendInbox() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: Schemas['InboxSendInput']) => call(api.POST('/inbox/messages', { body: input })),
    onSuccess: (message) => {
      void client.invalidateQueries({ queryKey: keys.inboxThreads })
      void client.invalidateQueries({ queryKey: keys.inboxThread(message.threadId) })
    },
  })
}

// 在线索中回复：回复线索中的最后一条消息，收件人由服务端确定为线索中的另一方。
export function useReplyInbox() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ messageId, ...body }: { messageId: string } & Schemas['InboxReplyInput']) =>
      call(api.POST('/inbox/messages/{messageId}/reply', { params: { path: { messageId } }, body })),
    onSuccess: (message) => {
      void client.invalidateQueries({ queryKey: keys.inboxThreads })
      void client.invalidateQueries({ queryKey: keys.inboxThread(message.threadId) })
    },
  })
}

// ---- Agent 的收件箱（只读） ----

export function useAgentInbox(agentId: string, unreadOnly: boolean) {
  return useInfiniteQuery({
    queryKey: keys.agentInbox(agentId, unreadOnly),
    queryFn: ({ pageParam }) =>
      call(
        api.GET('/agents/{agentId}/inbox', {
          params: { path: { agentId }, query: { ...pageQuery(pageParam), ...(unreadOnly ? { unread: true } : {}) } },
        }),
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
  })
}

// 消息全文，展开时才请求；只读，不改变已读状态。
export function useAgentInboxMessage(agentId: string, messageId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.agentInboxMessage(agentId, messageId),
    queryFn: () => call(api.GET('/agents/{agentId}/inbox/{messageId}', { params: { path: { agentId, messageId } } })),
    enabled,
  })
}

// ---- 外部身份 ----

export function useIdentities() {
  return useQuery({ queryKey: keys.identities, queryFn: () => call(api.GET('/external-identities')) })
}

// 单个外部身份，已删除的也能读到（deletedAt 不为 null）。
export function useIdentity(identityId: string) {
  return useQuery({
    queryKey: keys.identity(identityId),
    queryFn: () => call(api.GET('/external-identities/{identityId}', { params: { path: { identityId } } })),
  })
}

export function useCreateIdentity() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => call(api.POST('/external-identities', { body: { name } })),
    onSuccess: (identity) => {
      client.setQueryData(keys.identity(identity.id), identity)
      void client.invalidateQueries({ queryKey: keys.identities })
    },
  })
}

export function useDeleteIdentity() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (identityId: string) => call(api.DELETE('/external-identities/{identityId}', { params: { path: { identityId } } })),
    onSuccess: (_, identityId) => {
      client.removeQueries({ queryKey: keys.identityTokens(identityId) })
      void client.invalidateQueries({ queryKey: keys.identity(identityId) })
      void client.invalidateQueries({ queryKey: keys.identities })
    },
  })
}

// 代管：把这个身份交给一个插件的渠道（null 清空）。插件停用或宿主停止时绑定保留，卸载插件时清空。
export function useSetIdentityManager(identityId: string) {
  const client = useQueryClient()
  return useMutation<Schemas['ExternalIdentity'], ApiError, Schemas['ExternalIdentityManager'] | null>({
    mutationFn: (managedBy) => call(api.PUT('/external-identities/{identityId}/manager', { params: { path: { identityId } }, body: { managedBy } })),
    onSuccess: (identity) => {
      client.setQueryData(keys.identity(identityId), identity)
      void client.invalidateQueries({ queryKey: keys.identities })
    },
  })
}

// 令牌列表，含已吊销与已过期的，新的在前。已删除的身份没有令牌列表，不请求。
export const identityTokensQuery = (identityId: string) => ({
  queryKey: keys.identityTokens(identityId),
  queryFn: () => call(api.GET('/external-identities/{identityId}/tokens', { params: { path: { identityId } } })),
})

export function useIdentityTokens(identityId: string, enabled: boolean) {
  return useQuery({ ...identityTokensQuery(identityId), enabled })
}

// 签发令牌。明文只在返回值中出现一次：不经过 TanStack Query 的变更缓存（它会保留结果），由签发对话框以局部状态保存。
export function issueToken(identityId: string, expiresAt: string | null) {
  return call(
    api.POST('/external-identities/{identityId}/tokens', {
      params: { path: { identityId } },
      body: expiresAt ? { expiresAt } : {},
    }),
  )
}

// 签发成功后只刷新令牌列表（列表中只有元数据）。
export function invalidateTokens(client: QueryClient, identityId: string) {
  return client.invalidateQueries({ queryKey: keys.identityTokens(identityId) })
}

export function useRevokeToken(identityId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (tokenId: string) =>
      call(api.DELETE('/external-identities/{identityId}/tokens/{tokenId}', { params: { path: { identityId, tokenId } } })),
    onSuccess: () => invalidateTokens(client, identityId),
  })
}

// 外部身份的收件箱（只读）：收到与发出的消息，新的在前。
export function useIdentityInbox(identityId: string, direction: InboxDirection, unreadOnly: boolean) {
  return useInfiniteQuery({
    queryKey: keys.identityInbox(identityId, direction, unreadOnly),
    queryFn: ({ pageParam }) =>
      call(
        api.GET('/external-identities/{identityId}/inbox', {
          params: {
            path: { identityId },
            query: { ...pageQuery(pageParam), direction, ...(unreadOnly ? { unread: true } : {}) },
          },
        }),
      ),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
  })
}

export function useIdentityInboxMessage(identityId: string, messageId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.identityInboxMessage(identityId, messageId),
    queryFn: () =>
      call(api.GET('/external-identities/{identityId}/inbox/{messageId}', { params: { path: { identityId, messageId } } })),
    enabled,
  })
}
