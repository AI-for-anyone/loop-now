import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call, type Schemas } from './client'

// Permission modes and the requests an Agent sends to be approved (contract PermissionMode, PermissionRequest).
export const permissionKeys = {
  settings: ['permissionSettings'] as const,
}

// The mode new Agents get (Settings); Full access until someone chooses otherwise.
export function usePermissionSettings() {
  return useQuery({ queryKey: permissionKeys.settings, queryFn: ({ signal }) => call(api.GET('/permission-settings', { signal })) })
}

export function usePutPermissionSettings() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: Schemas['PermissionSettings']) => call(api.PUT('/permission-settings', { body })),
    onSuccess: (settings) => client.setQueryData(permissionKeys.settings, settings),
  })
}

// Approve or reject a request (a reason rejects it and tells the Agent why). Answered once: a second answer, from here or the Inbox,
// gets 409 permission_request_answered with the request as it was answered.
export const answerPermissionRequest = (permissionRequestId: string, body: Schemas['PermissionAnswer']) =>
  call(api.POST('/permission-requests/{permissionRequestId}/answer', { params: { path: { permissionRequestId } }, body }))
