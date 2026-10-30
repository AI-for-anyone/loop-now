import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import type { Readable } from 'openapi-typescript-helpers'
import { api, ApiError, call, type Schemas } from './client'
import { t } from '@/i18n'

const copy = t.credentials.lark

type Receipt = Schemas['TaskLarkSwitchReceipt']
type Input = Schemas['TaskLarkSwitchInput']
export const taskLarkSwitchKeys = {
  action: (taskId: string, id: string | null) => ['task-lark-switch', taskId, id] as const,
  observed: (taskId: string, id: string) => ['task-lark-switch-observed', taskId, id] as const,
}
const invalid = () => new ApiError({ status: 502, code: 'unknown', serverMessage: copy.switchMismatch })
// openapi-fetch's Readable removes pure-null keys. Restore only the fixed nulls
// in the generated operation branches, without casting or inventing fields.
function response(value: Readable<Receipt>): Receipt {
  switch (value.status) {
    case 'pending': case 'handoff': return { ...value, bindingRevision: null, continuation: null, dispatchStatus: null, recoveryInstruction: null, errorCode: null, errorMessage: null, finishedAt: null }
    case 'failed': return { ...value, bindingRevision: null, continuation: null, dispatchStatus: null, recoveryInstruction: null }
    case 'succeeded': return { ...value, errorCode: null, errorMessage: null }
  }
}
export function validateTaskLarkSwitch(receipt: Receipt, taskId: string, actionId: string, input?: Input) {
  const members = receipt.members
  if (receipt.taskId !== taskId || receipt.actionId !== actionId || members.length !== 2 || new Set(members.map(m => m.agentId)).size !== 2 || new Set(members.map(m => m.runtimeId)).size !== 2 || members.filter(m => m.role === 'publisher').length !== 1 || members.filter(m => m.role === 'followup').length !== 1) throw invalid()
  const publisher = members.find(m => m.role === 'publisher')
  if (publisher?.agentId !== receipt.sourceAgentId || publisher.runtimeId !== receipt.sourceRuntimeId) throw invalid()
  if (receipt.status === 'succeeded' && (!receipt.continuation || !receipt.bindingRevision || !receipt.dispatchStatus)) throw invalid()
  if (input && (receipt.sourceNodeRunId !== input.expectedNodeRunId || receipt.sourceAgentId !== input.expectedAgentId || receipt.sourceRuntimeId !== input.expectedRuntimeId || receipt.target.accountId !== input.targetAccountId || receipt.target.versionId !== input.targetVersionId || receipt.target.accountRevision !== input.expectedAccountRevision || !members.every(m => input.expectedMembers.some(expected => expected.agentId === m.agentId && expected.runtimeId === m.runtimeId && expected.expectedBindingRevision === m.previousBindingRevision)))) throw invalid()
  return receipt
}
const terminal = (receipt: Receipt) => receipt.status === 'succeeded' || receipt.status === 'failed'
const endedRun = (status: Receipt['dispatchStatus']) => status === 'completed' || status === 'failed' || status === 'interrupted'
function identity(receipt: Receipt) {
  return JSON.stringify([receipt.actionId, receipt.taskId, receipt.sourceNodeRunId, receipt.sourceAgentId, receipt.sourceRuntimeId, receipt.reservationTaskVersion, receipt.target, [...receipt.members].sort((a, b) => a.runtimeId.localeCompare(b.runtimeId)), receipt.createdAt])
}
export function mergeTaskLarkSwitch(previous: Receipt | undefined, next: Receipt): Receipt {
  if (!previous) return next
  if (identity(previous) !== identity(next)) throw invalid()
  if (terminal(previous)) {
    if (next.status !== previous.status) return previous
    if (previous.status === 'succeeded' && next.status === 'succeeded') {
      if (JSON.stringify(previous.continuation) !== JSON.stringify(next.continuation) || previous.bindingRevision !== next.bindingRevision) throw invalid()
      if (endedRun(previous.dispatchStatus) && next.dispatchStatus !== previous.dispatchStatus) return previous
    }
  } else if (previous.status === 'handoff' && next.status === 'pending') return previous
  return next
}
export function acceptTaskLarkSwitch(client: QueryClient, receipt: Receipt) {
  const key = taskLarkSwitchKeys.observed(receipt.taskId, receipt.actionId)
  const accepted = mergeTaskLarkSwitch(client.getQueryData<Receipt>(key), receipt)
  client.setQueryData(key, accepted)
  client.setQueryData(taskLarkSwitchKeys.action(receipt.taskId, receipt.actionId), accepted)
  return accepted
}
export async function startTaskLarkSwitch(taskId: string, input: Input) {
  const result = response(await call(api.POST('/tasks/{taskId}/lark-account-switches', { params: { path: { taskId } }, body: input })))
  return validateTaskLarkSwitch(result, taskId, input.actionId, input)
}
export async function readTaskLarkSwitch(taskId: string, actionId: string, signal?: AbortSignal) {
  const result = response(await call(api.GET('/tasks/{taskId}/lark-account-switches/{actionId}', { params: { path: { taskId, actionId } }, signal })))
  return validateTaskLarkSwitch(result, taskId, actionId)
}
export function useTaskLarkSwitch(taskId: string, actionId: string | null, enabled = true) {
  const client = useQueryClient()
  return useQuery({
    queryKey: taskLarkSwitchKeys.action(taskId, actionId), enabled: enabled && !!actionId, retry: false,
    queryFn: async ({ signal }) => acceptTaskLarkSwitch(client, await readTaskLarkSwitch(taskId, actionId!, signal)),
    refetchInterval: query => {
      if (query.state.error || !enabled) return false
      const value = query.state.data
      return value && (value.status === 'pending' || value.status === 'handoff' || value.status === 'succeeded' && !endedRun(value.dispatchStatus)) ? 1500 : false
    },
    refetchIntervalInBackground: true,
  })
}
