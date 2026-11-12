import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, call, type Schemas } from './client'

// 个人版“本机模式”：全局三个开关，以及项目与 Agent 各自的本机目录清单。
export const localModeKeys = {
  settings: ['local-mode-settings'] as const,
  projectDirectories: (projectId: string) => ['project-local-directories', projectId] as const,
  // ['agent-local-directories'] 是全部 Agent 清单的共同前缀：项目清单变化后，Agent 页里继承的条目一并重新读取。
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

// 每次只改一项，响应是修改后的全部开关。
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

// 整体替换清单；响应带每项的 status，直接作为新的缓存，不再读一次。
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
