import { useQuery } from '@tanstack/react-query'
import { api, ApiError, call, type Schemas } from './client'
import { keys } from './queries'
import { t } from '@/i18n'

type Item = Schemas['QueueItem']
type Receipt = Schemas['AgentQueueActionReceipt']

// Actions on a queued item: turn it into an interrupt, or withdraw it (cancel). Messages the console sent in the conversation are acted
// on by run, Inbox messages by message.
export type QueueOperation = 'promote' | 'cancel'

// The per-item queue of a normal conversation. Used until the SSE snapshot arrives; after that the snapshot and queue.updated are
// authoritative.
export function useAgentQueue(agentId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.agentQueue(agentId),
    enabled,
    queryFn: ({ signal }) => call(api.GET('/agents/{agentId}/queue', { signal, params: { path: { agentId } } })),
  })
}

function checked(receipt: Receipt, agentId: string, actionId: string, operation: Receipt['operation']) {
  if (receipt.actionId !== actionId || receipt.agentId !== agentId || receipt.operation !== operation) {
    throw new ApiError({ status: 502, code: 'unknown', serverMessage: t.chat.queueList.receiptMismatch })
  }
  return receipt
}

// The caller generates actionId; when the outcome is unknown, retry with the same actionId and the server returns the first result.
export async function agentQueueAction(agentId: string, item: Item, operation: QueueOperation, actionId: string) {
  const body = { actionId }
  if (item.kind === 'run') {
    const params = { path: { agentId, runId: item.runId } }
    const receipt = operation === 'promote'
      ? await call(api.POST('/agents/{agentId}/runs/{runId}/promote-to-interrupt', { params, body }))
      : await call(api.POST('/agents/{agentId}/runs/{runId}/cancel', { params, body }))
    return checked(receipt, agentId, actionId, operation === 'promote' ? 'promote_run' : 'cancel_run')
  }
  const params = { path: { agentId, messageId: item.messageId ?? '' } }
  const receipt = operation === 'promote'
    ? await call(api.POST('/agents/{agentId}/inbox-messages/{messageId}/promote-to-interrupt', { params, body }))
    : await call(api.POST('/agents/{agentId}/inbox-messages/{messageId}/cancel', { params, body }))
  return checked(receipt, agentId, actionId, operation === 'promote' ? 'promote_inbox_message' : 'cancel_inbox_message')
}

// Stop only this reply in progress, without sending a new message.
export async function stopAgentRun(agentId: string, runId: string, actionId: string) {
  const receipt = await call(api.POST('/agents/{agentId}/runs/{runId}/stop', { params: { path: { agentId, runId } }, body: { actionId } }))
  return checked(receipt, agentId, actionId, 'stop_run')
}

// A stable key for a queued item: by run for console messages, by message for Inbox messages.
export function queueItemKey(item: Item) {
  return item.kind === 'run' ? `run:${item.runId}` : `inbox:${item.messageId}`
}
