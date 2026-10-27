import { useQuery } from '@tanstack/react-query'
import { api, call, type Schemas } from './client'
import { config } from '@/config'
export const projectPromptKey = (projectId: string) => ['project-prompt', projectId] as const
export const getProjectPrompt = (projectId: string) => call(api.GET('/projects/{projectId}/prompt', { params: { path: { projectId } } }))
export function useProjectPrompt(projectId: string) {
  return useQuery({ queryKey: projectPromptKey(projectId), queryFn: () => getProjectPrompt(projectId), refetchInterval: config.agentListPollMs, refetchOnWindowFocus: true, retry: false })
}
export const putProjectPrompt = (projectId: string, body: Schemas['ProjectPromptUpdate']) => call(api.PUT('/projects/{projectId}/prompt', { params: { path: { projectId } }, body }))
export const getAgentPrompt = (agentId: string) => call(api.GET('/agents/{agentId}', { params: { path: { agentId } } }))
export const putAgentPrompt = (agentId: string, body: Schemas['AgentUpdate']) => call(api.PATCH('/agents/{agentId}', { params: { path: { agentId } }, body }))
