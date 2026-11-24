import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { api, call, type ApiError, type Schemas } from './client'
import { keys } from './queries'

type Command = Schemas['AcpCommand']

// The local ACP commands people added (global settings). Each is a runtime of its own with one model provider of the same id, so adding,
// changing (its name) or removing one changes the runtime definitions, the runtime statuses and the provider catalog: all are read again,
// and the runner picker follows.
export const acpCommandsKey = ['acpCommands'] as const

export function useAcpCommands() {
  return useQuery({ queryKey: acpCommandsKey, queryFn: () => call(api.GET('/acp-commands')) })
}

const refreshRuntimes = (client: QueryClient) =>
  Promise.all([
    client.invalidateQueries({ queryKey: acpCommandsKey }),
    client.invalidateQueries({ queryKey: keys.runtimeDefinitions }),
    client.invalidateQueries({ queryKey: keys.runtimes }),
    client.invalidateQueries({ queryKey: keys.modelProviders }),
    client.invalidateQueries({ queryKey: ['models'] }),
  ])

export function useCreateAcpCommand() {
  const client = useQueryClient()
  return useMutation<Command, ApiError, Schemas['AcpCommandCreate']>({
    mutationFn: (body) => call(api.POST('/acp-commands', { body })),
    onSuccess: () => refreshRuntimes(client),
  })
}

export function useUpdateAcpCommand() {
  const client = useQueryClient()
  return useMutation<Command, ApiError, { id: string; body: Schemas['AcpCommandUpdate'] }>({
    mutationFn: ({ id, body }) => call(api.PATCH('/acp-commands/{commandId}', { params: { path: { commandId: id } }, body })),
    onSuccess: () => refreshRuntimes(client),
  })
}

export function useDeleteAcpCommand() {
  const client = useQueryClient()
  return useMutation<void, ApiError, string>({
    mutationFn: async (id) => {
      await call(api.DELETE('/acp-commands/{commandId}', { params: { path: { commandId: id } } }))
    },
    onSuccess: () => refreshRuntimes(client),
  })
}
