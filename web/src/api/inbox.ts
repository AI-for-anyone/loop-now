import { useInfiniteQuery, useMutation, useQuery, useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query'
import { config } from '@/config'
import { api, call, type ApiError, type Schemas } from './client'
import { keys, type InboxDirection } from './queries'

// Reading and changing the inbox, external identities and tokens. Live changes come from the inbox event stream
// (features/inbox/useInboxLive), which invalidates the caches here or updates them in place.

export type ThreadPages = InfiniteData<Schemas['InboxThreadPage'], string | null>
export type MessagePages = InfiniteData<Schemas['InboxMessagePage'], string | null>

const pageQuery = (cursor: string | null) => ({ limit: config.inboxPageSize, ...(cursor ? { cursor } : {}) })

// ---- Your exchanges (the inbox page is gone, see api/console-inbox) ----

// New message: always starts a new thread. whenBusy is not submitted when the recipient is an external identity.
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

// Reply in a thread: replies to the last message in the thread; the server sets the recipient to the other party in the thread.
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

// ---- An Agent's inbox (read-only) ----

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

// The full text of a message, requested only when expanded; read-only, it does not change the read state.
export function useAgentInboxMessage(agentId: string, messageId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.agentInboxMessage(agentId, messageId),
    queryFn: () => call(api.GET('/agents/{agentId}/inbox/{messageId}', { params: { path: { agentId, messageId } } })),
    enabled,
  })
}

// ---- External identities ----

export function useIdentities() {
  return useQuery({ queryKey: keys.identities, queryFn: () => call(api.GET('/external-identities')) })
}

// A single external identity; deleted ones can be read too (deletedAt is not null).
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

// Managed by a plugin: hand this identity over to a plugin's channel (null clears it). The binding is kept when the plugin is disabled or
// the host stops, and cleared when the plugin is uninstalled.
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

// The token list, including revoked and expired tokens, newest first. A deleted identity has no token list, so nothing is requested.
export const identityTokensQuery = (identityId: string) => ({
  queryKey: keys.identityTokens(identityId),
  queryFn: () => call(api.GET('/external-identities/{identityId}/tokens', { params: { path: { identityId } } })),
})

export function useIdentityTokens(identityId: string, enabled: boolean) {
  return useQuery({ ...identityTokensQuery(identityId), enabled })
}

// Issue a token. The plaintext appears only once, in the return value: it does not go through TanStack Query's mutation cache (which keeps
// results); the issue dialog holds it in local state.
export function issueToken(identityId: string, expiresAt: string | null) {
  return call(
    api.POST('/external-identities/{identityId}/tokens', {
      params: { path: { identityId } },
      body: expiresAt ? { expiresAt } : {},
    }),
  )
}

// After a token is issued, refresh only the token list (the list holds only metadata).
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

// An external identity's inbox (read-only): received and sent messages, newest first.
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
