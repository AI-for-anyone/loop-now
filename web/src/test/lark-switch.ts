import type { Schemas } from '@/api/client'
import { makeTaskDetail } from './tasks'
import { defaultProject } from './api'
import { larkUserAccount } from './lark-login'

export const switchActionId = '30000000-0000-7000-8000-000000000061'
export function larkSwitchDetail(): Schemas['ConsoleTaskDetail'] {
  return { ...makeTaskDetail(defaultProject.id), larkSwitchParticipants: larkSwitchInput().expectedMembers.map((m, index) => ({ agentId: m.agentId, runtimeId: m.runtimeId, baseSnapshotSHA: 'a'.repeat(64), bindingRevision: m.expectedBindingRevision, larkAllowed: true, prepared: index === 0, available: true, unavailableReason: null, stepIds: index === 0 ? ['check'] : ['finish'] })) }
}
export function larkSwitchInput(): Schemas['TaskLarkSwitchInput'] {
  const detail = makeTaskDetail(defaultProject.id), node = detail.nodes[1], account = larkUserAccount()
  return { actionId: switchActionId, expectedTaskVersion: detail.task.version, expectedNodeRunId: node.id, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId, expectedLarkBindingRevision: 0, expectedMembers: [{ agentId: node.agentId, runtimeId: node.runtimeId, expectedBindingRevision: 0 }, { agentId: '00000000-0000-7000-8000-000000000071', runtimeId: '00000000-0000-7000-8000-000000000072', expectedBindingRevision: 2 }], targetAccountId: account.id, targetVersionId: account.currentVersionId!, expectedAccountRevision: account.revision }
}
// Formal-contract HTTP fixture, not an authority for participant eligibility.
export function larkSwitchReceipt(status: Schemas['TaskLarkSwitchReceipt']['status'] = 'pending'): Schemas['TaskLarkSwitchReceipt'] {
  const detail = makeTaskDetail(defaultProject.id), input = larkSwitchInput(), account = larkUserAccount()
  if (!account.larkIdentity) throw new Error('Synthetic verified identity required')
  const base = { actionId: input.actionId, taskId: detail.task.id, sourceNodeRunId: input.expectedNodeRunId, sourceAgentId: input.expectedAgentId, sourceRuntimeId: input.expectedRuntimeId, reservationTaskVersion: input.expectedTaskVersion + 1, target: { accountId: account.id, versionId: input.targetVersionId, accountRevision: account.revision, identity: account.larkIdentity, runtimeVariantVersion: 'lark-cli-user-1.0.94-loop-authsidecar.1' as const }, members: input.expectedMembers.map((m, index) => ({ agentId: m.agentId, runtimeId: m.runtimeId, previousBindingRevision: m.expectedBindingRevision, baseSnapshotSHA: 'a'.repeat(64), role: index === 0 ? 'publisher' as const : 'followup' as const })), createdAt: '2026-10-20T00:00:00Z' }
  switch (status) {
    case 'pending': case 'handoff': return { ...base, status, bindingRevision: null, continuation: null, dispatchStatus: null, recoveryInstruction: null, errorCode: null, errorMessage: null, finishedAt: null }
    case 'failed': return { ...base, status, bindingRevision: null, continuation: null, dispatchStatus: null, recoveryInstruction: null, errorCode: 'task_conflict', errorMessage: 'Synthetic conflict', finishedAt: '2026-10-20T00:01:00Z' }
    case 'succeeded': return { ...base, status, bindingRevision: 3, continuation: { nodeRunId: '00000000-0000-7000-8000-000000000073', attempt: 2, runId: '00000000-0000-7000-8000-000000000074' }, dispatchStatus: 'queued', recoveryInstruction: 'Synthetic QA continuation only', errorCode: null, errorMessage: null, finishedAt: '2026-10-20T00:01:00Z' }
  }
}
