import { useQuery } from '@tanstack/react-query'
import { api, call, type Schemas } from './client'
import { config } from '@/config'

export const modelAuthKey = (agentId: string, provider: string, configVersion: number) => ['model-auth', agentId, provider, configVersion] as const
export const loginActive = (operation: Schemas['ModelAuthOperation'] | null | undefined) =>
  operation?.status === 'pending' || operation?.status === 'awaiting_authorization'

export function useModelAuth(agent: Schemas['Agent']) {
  return useQuery({
    queryKey: modelAuthKey(agent.id, agent.model.provider, agent.configVersion),
    queryFn: ({ signal }) => call(api.GET('/agents/{agentId}/model-auth', { params: { path: { agentId: agent.id } }, signal })),
    // The authorization URL lives only in the memory of the active page; the query is cleared on leaving, and no persistent cache is used.
    gcTime: 0,
    staleTime: 0,
    retry: false,
    refetchOnMount: 'always',
    refetchOnWindowFocus: 'always',
    refetchInterval: (query) => loginActive(query.state.data?.operation) ? 1500 : config.agentListPollMs,
  })
}

export function startModelLogin(agent: Schemas['Agent']) {
  return call(api.POST('/agents/{agentId}/model-auth/operations', {
    params: { path: { agentId: agent.id } },
    body: { provider: agent.model.provider, expectedConfigVersion: agent.configVersion },
  }))
}

export function cancelModelLogin(agentId: string, operationId: string) {
  return call(api.POST('/agents/{agentId}/model-auth/operations/{operationId}/cancel', {
    params: { path: { agentId, operationId } }, body: {},
  }))
}

// Answer a question asked partway through a plugin provider's login: the answer is treated as a secret and sent directly, without the
// mutation cache and without logging; returns the new auth state.
export function answerModelLogin(agentId: string, operationId: string, body: Schemas['ProjectModelLoginInput']) {
  return call(api.POST('/agents/{agentId}/model-auth/operations/{operationId}/input', {
    params: { path: { agentId, operationId } }, body,
  }))
}
