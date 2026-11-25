import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, call, type Schemas } from './client'

// Writing with AI: one global setting says which local CLI writes (null = Automatic, the first ready one), and a draft is asked for one
// request at a time. No Agent is involved; each request runs a throwaway session of that CLI on this computer.
export const textGenerationKeys = { settings: ['text-generation-settings'] as const }

// Who writes. `writer` says who would write right now (after Automatic or a fallback), so it is read again whenever it is looked at: a CLI
// may have been installed, logged in or out in the meantime (the server's own probe is cached for 30 seconds).
export function useTextGenerationSettings(enabled = true) {
  return useQuery({
    queryKey: textGenerationKeys.settings,
    queryFn: () => call(api.GET('/text-generation-settings')),
    staleTime: 0,
    enabled,
  })
}

// Choose who writes; the answer (with who writes now) becomes the cache.
export function usePutTextGenerationSettings() {
  const client = useQueryClient()
  return useMutation<Schemas['TextGenerationSettings'], ApiError, Schemas['TextGenerationSettingsPut']>({
    mutationFn: (body) => call(api.PUT('/text-generation-settings', { body })),
    onSuccess: (settings) => client.setQueryData(textGenerationKeys.settings, settings),
  })
}

// Ask for a Prompt. Aborting the signal (Stop, or the editor going away) makes the server stop the CLI at once; the promise then rejects
// with the DOMException named AbortError.
export const writePromptDraft = (body: Schemas['PromptDraftRequest'], signal?: AbortSignal) => call(api.POST('/prompt-drafts', { body, signal }))
