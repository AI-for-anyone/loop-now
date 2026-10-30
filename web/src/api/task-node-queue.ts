import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { api, ApiError, call, type Schemas } from './client'
import { t } from '@/i18n'

const copy = t.taskInbox

type Queue = Schemas['TaskNodeQueue']

export const taskNodeQueueKeys = {
  task: (taskId: string) => ['taskNodeQueue', taskId] as const,
  node: (taskId: string, nodeRunId: string) => ['taskNodeQueue', taskId, nodeRunId] as const,
}

// task.node.queue.updated is a full replacement of one node's queue. Reads and
// events share an observation clock: a read that started before the latest
// event returns that event's queue instead of overwriting it with older data.
class NodeQueueProjection {
  private clock = 0
  private latest = new Map<string, { observed: number; queue: Queue }>()
  begin() { return ++this.clock }
  read(started: number, queue: Queue): Queue {
    const live = this.latest.get(`${queue.taskId}:${queue.nodeRunId}`)
    return live && live.observed > started ? live.queue : queue
  }
  event(client: QueryClient, taskId: string, queue: Queue) {
    if (!queue || queue.taskId !== taskId || typeof queue.nodeRunId !== 'string' || typeof queue.runtimeId !== 'string' || !Array.isArray(queue.items)) return false
    this.latest.set(`${taskId}:${queue.nodeRunId}`, { observed: ++this.clock, queue })
    client.setQueryData(taskNodeQueueKeys.node(taskId, queue.nodeRunId), queue)
    return true
  }
}
const stores = new WeakMap<QueryClient, NodeQueueProjection>()
export function taskNodeQueueProjection(client: QueryClient) { let store = stores.get(client); if (!store) { store = new NodeQueueProjection(); stores.set(client, store) } return store }

// The node's queued messages, one per message, in the server's claim order.
export function useTaskNodeQueue(taskId: string, nodeRunId: string, enabled: boolean) {
  const store = taskNodeQueueProjection(useQueryClient())
  return useQuery({ queryKey: taskNodeQueueKeys.node(taskId, nodeRunId), enabled, retry: false,
    queryFn: async ({ signal }) => {
      const started = store.begin(), queue = await call(api.GET('/tasks/{taskId}/nodes/{nodeRunId}/queue', { signal, params: { path: { taskId, nodeRunId } } }))
      if (queue.taskId !== taskId || queue.nodeRunId !== nodeRunId) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.scopeMismatch })
      return store.read(started, queue)
    },
  })
}

type Input = Schemas['TaskNodeQueueActionInput']
type Receipt = Schemas['TaskNodeQueueActionReceipt']
function checked(receipt: Receipt, taskId: string, nodeRunId: string, body: Input, operation: Receipt['operation'], messageId: string | null) {
  if (receipt.actionId !== body.actionId || receipt.taskId !== taskId || receipt.nodeRunId !== nodeRunId || receipt.operation !== operation || receipt.messageId !== messageId || receipt.agentId !== body.expectedAgentId || receipt.runtimeId !== body.expectedRuntimeId) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.scopeMismatch })
  return receipt
}

// actionId follows the node write-action contract: an unknown result is
// retried with the same actionId, never with a new action.
export async function taskNodeQueueAction(taskId: string, nodeRunId: string, messageId: string, operation: 'promote' | 'cancel', body: Input) {
  const params = { path: { taskId, nodeRunId, messageId } }
  const receipt = operation === 'promote'
    ? await call(api.POST('/tasks/{taskId}/nodes/{nodeRunId}/inbox-messages/{messageId}/promote-to-interrupt', { params, body }))
    : await call(api.POST('/tasks/{taskId}/nodes/{nodeRunId}/inbox-messages/{messageId}/cancel', { params, body }))
  return checked(receipt, taskId, nodeRunId, body, operation === 'promote' ? 'promote_inbox_message' : 'cancel_inbox_message', messageId)
}

// Stops only this turn of the node; later queued messages are still processed.
export async function stopTaskNodeRun(taskId: string, nodeRunId: string, runId: string, body: Input) {
  const receipt = await call(api.POST('/tasks/{taskId}/nodes/{nodeRunId}/runs/{runId}/stop', { params: { path: { taskId, nodeRunId, runId } }, body }))
  return checked(receipt, taskId, nodeRunId, body, 'stop_run', null)
}
