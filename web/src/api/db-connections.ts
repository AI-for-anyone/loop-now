import { useQuery, type QueryClient } from '@tanstack/react-query'
import { api, call, type Schemas } from './client'
import { config } from '@/config'

export const dbConnectionKey = (agentId: string, cli?: Schemas['DBConnectionCLI']) => cli ? ['db-connections', agentId, cli] as const : ['db-connections', agentId] as const
export function readDBConnections(agentId: string, cli: Schemas['DBConnectionCLI'], signal?: AbortSignal) {
  return call(api.GET('/agents/{agentId}/db-connections/{cli}', { params: { path: { agentId, cli } }, signal }))
}
export function useDBConnections(agentId: string, cli: Schemas['DBConnectionCLI']) {
  return useQuery({ queryKey: dbConnectionKey(agentId, cli), queryFn: ({ signal }) => readDBConnections(agentId, cli, signal), retry: false, refetchInterval: config.agentListPollMs })
}
export function putDBConnections(agentId: string, cli: Schemas['DBConnectionCLI'], body: Schemas['DBConnectionsPut']) {
  return call(api.PUT('/agents/{agentId}/db-connections/{cli}', { params: { path: { agentId, cli } }, body }))
}
export function deleteDBConnections(agentId: string, cli: Schemas['DBConnectionCLI'], expectedConfigVersion: number) {
  return call(api.DELETE('/agents/{agentId}/db-connections/{cli}', { params: { path: { agentId, cli }, query: { expectedConfigVersion } } }))
}
export async function invalidateDBConnections(client: QueryClient, agentId: string) {
  await Promise.all([
    client.invalidateQueries({ queryKey: dbConnectionKey(agentId) }),
    client.invalidateQueries({ queryKey: ['agent', agentId] }),
  ])
}
