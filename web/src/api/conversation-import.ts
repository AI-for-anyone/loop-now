import { useQuery } from '@tanstack/react-query'
import { api, call, type Schemas } from './client'

// Finding the Claude Code and Codex conversations on this computer, and importing the ones picked (contract ConversationScan,
// ConversationImport). Both are operations: the POST answers 202 with the operation as it starts, and the page reads it again until it
// is done or failed.
export const conversationImportKeys = {
  scan: (operationId: string) => ['conversationScan', operationId] as const,
  import: (operationId: string) => ['conversationImport', operationId] as const,
}

const POLL_MS = 700

export const startConversationScan = (input: Schemas['ConversationScanInput']) => call(api.POST('/conversation-scans', { body: input }))

export const startConversationImport = (input: Schemas['ConversationImportInput']) => call(api.POST('/conversation-imports', { body: input }))

const running = (state: Schemas['ConversationScan']['state'] | undefined) => state === undefined || state === 'running'

// Read again while it runs; not after an error (the page says so and offers to read again), and not when the window gets focus: a
// finished operation does not change, and the server forgets it after a while.
const poll = (query: { state: { status: string; data?: { state: Schemas['ConversationScan']['state'] } } }) => (query.state.status !== 'error' && running(query.state.data?.state) ? POLL_MS : false)

// first: the operation as the POST returned it, shown until the first read.
export function useConversationScan(first: Schemas['ConversationScan'] | null) {
  const operationId = first?.operationId ?? ''
  return useQuery({
    queryKey: conversationImportKeys.scan(operationId),
    enabled: first !== null,
    initialData: first ?? undefined,
    initialDataUpdatedAt: 0,
    retry: false,
    queryFn: ({ signal }) => call(api.GET('/conversation-scans/{operationId}', { signal, params: { path: { operationId } } })),
    refetchInterval: poll,
    refetchOnWindowFocus: false,
  })
}

export function useConversationImport(first: Schemas['ConversationImport'] | null) {
  const operationId = first?.operationId ?? ''
  return useQuery({
    queryKey: conversationImportKeys.import(operationId),
    enabled: first !== null,
    initialData: first ?? undefined,
    initialDataUpdatedAt: 0,
    retry: false,
    queryFn: ({ signal }) => call(api.GET('/conversation-imports/{operationId}', { signal, params: { path: { operationId } } })),
    refetchInterval: poll,
    refetchOnWindowFocus: false,
  })
}
