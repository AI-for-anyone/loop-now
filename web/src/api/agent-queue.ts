import { useQuery } from '@tanstack/react-query'
import { api, ApiError, call, type Schemas } from './client'
import { keys } from './queries'
import { t } from '@/i18n'

type Item = Schemas['QueueItem']
type Receipt = Schemas['AgentQueueActionReceipt']

// 排队项上的动作：转为打断或撤回（取消）。控制台在对话中发出的消息按执行操作，Inbox 消息按消息操作。
export type QueueOperation = 'promote' | 'cancel'

// 普通对话的逐条排队列表。收到 SSE 快照之前用它，之后以快照与 queue.updated 为准。
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

// actionId 由调用方生成；结果不明时用同一 actionId 重试，服务端返回首次结果。
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

// 只停止这一次进行中的回复，不发送新消息。
export async function stopAgentRun(agentId: string, runId: string, actionId: string) {
  const receipt = await call(api.POST('/agents/{agentId}/runs/{runId}/stop', { params: { path: { agentId, runId } }, body: { actionId } }))
  return checked(receipt, agentId, actionId, 'stop_run')
}

// 排队项的稳定键：控制台消息按执行，Inbox 消息按消息。
export function queueItemKey(item: Item) {
  return item.kind === 'run' ? `run:${item.runId}` : `inbox:${item.messageId}`
}
