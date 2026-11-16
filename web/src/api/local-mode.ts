import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, call, type Schemas } from './client'

// “本机模式” (Local mode) in the personal edition: three global switches, plus the local directory lists of each project and each Agent.
export const localModeKeys = {
  settings: ['local-mode-settings'] as const,
  projectDirectories: (projectId: string) => ['project-local-directories', projectId] as const,
  // ['agent-local-directories'] is the common prefix of every Agent's list: after a project's list changes, the entries inherited on the
  // Agent page are re-read too.
  agentDirectories: ['agent-local-directories'] as const,
  agentDirectoriesOf: (agentId: string) => ['agent-local-directories', agentId] as const,
}

export function useLocalModeSettings(enabled = true) {
  return useQuery({
    queryKey: localModeKeys.settings,
    queryFn: () => call(api.GET('/local-mode-settings')),
    enabled,
  })
}

// Change one item at a time; the response is the full set of switches after the change.
export function useUpdateLocalModeSettings() {
  const client = useQueryClient()
  return useMutation<Schemas['LocalModeSettings'], ApiError, Schemas['LocalModeSettingsPatch']>({
    mutationFn: (body) => call(api.PATCH('/local-mode-settings', { body })),
    onSuccess: (settings) => client.setQueryData(localModeKeys.settings, settings),
  })
}

const projectPath = (projectId: string) => ({ params: { path: { projectId } } })
const agentPath = (agentId: string) => ({ params: { path: { agentId } } })

export const getProjectLocalDirectories = (projectId: string) => call(api.GET('/projects/{projectId}/local-directories', projectPath(projectId)))
export const getAgentLocalDirectories = (agentId: string) => call(api.GET('/agents/{agentId}/local-directories', agentPath(agentId)))

export function useProjectLocalDirectories(projectId: string) {
  return useQuery({ queryKey: localModeKeys.projectDirectories(projectId), queryFn: () => getProjectLocalDirectories(projectId) })
}

export function useAgentLocalDirectories(agentId: string) {
  return useQuery({ queryKey: localModeKeys.agentDirectoriesOf(agentId), queryFn: () => getAgentLocalDirectories(agentId) })
}

// Replace the whole list; the response carries each entry's status and becomes the new cache directly, with no second read.
export function usePutProjectLocalDirectories(projectId: string) {
  const client = useQueryClient()
  return useMutation<Schemas['ProjectLocalDirectories'], ApiError, Schemas['LocalDirectoriesPut']>({
    mutationFn: (body) => call(api.PUT('/projects/{projectId}/local-directories', { ...projectPath(projectId), body })),
    onSuccess: async (result) => {
      client.setQueryData(localModeKeys.projectDirectories(projectId), result)
      await client.invalidateQueries({ queryKey: localModeKeys.agentDirectories })
    },
  })
}

export function usePutAgentLocalDirectories(agentId: string) {
  const client = useQueryClient()
  return useMutation<Schemas['AgentLocalDirectories'], ApiError, Schemas['LocalDirectoriesPut']>({
    mutationFn: (body) => call(api.PUT('/agents/{agentId}/local-directories', { ...agentPath(agentId), body })),
    onSuccess: (result) => client.setQueryData(localModeKeys.agentDirectoriesOf(agentId), result),
  })
}
