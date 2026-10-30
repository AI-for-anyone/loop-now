import { useTaskLarkSwitch } from '@/api/task-lark-switch'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { taskLarkSwitchLocatorKey, useOriginalActionLocator } from '@/lib/useOriginalActionLocator'
import { t } from '@/i18n'

const copy = t.credentials.lark

// This controller only reads an existing action. Until the formal participant
// projection is available there is deliberately no inferred member list/POST.
export function TaskLarkSwitchRecovery({ taskId, currentBlocked, showUnavailable = true, readEnabled = true }: { taskId: string; currentBlocked: boolean; showUnavailable?: boolean; readEnabled?: boolean }) {
  const locator = useOriginalActionLocator(taskLarkSwitchLocatorKey(taskId))
  const action = useTaskLarkSwitch(taskId, locator.id, locator.valid && readEnabled)
  if ((!currentBlocked || !showUnavailable) && !locator.id) return null
  const receipt = action.data
  const missing = action.error instanceof ApiError && action.error.status === 404
  return <section aria-label={copy.switch} className="mt-3 grid min-w-0 gap-2 rounded-input bg-tint-blue p-3 text-sm [overflow-wrap:anywhere]">
    {showUnavailable && <Button type="button" variant="secondary" disabled>{copy.switch}</Button>}
    {showUnavailable && <p role="status">{copy.switchUnavailable}</p>}
    {!locator.valid && <p role="alert">{copy.invalidLocator}</p>}
    {locator.id && locator.valid && <>
      <p>{copy.operation} · {locator.id}</p>
      {!receipt && <p role="status">{missing ? copy.notFound : action.isFetching ? copy.recovering : copy.unknown}</p>}
      {receipt && <>
        <p role="status">{receipt.status === 'succeeded' ? copy.committed : receipt.status === 'failed' ? copy.rejected : receipt.status === 'handoff' ? copy.handoff : copy.busy}</p>
        <details><summary>{copy.receiptDetails}</summary><div className="mt-2 max-h-48 space-y-2 overflow-y-auto overscroll-contain">
        <p>{copy.currentNode} · {receipt.sourceNodeRunId} · {receipt.sourceAgentId} · {receipt.sourceRuntimeId}</p>
        <p>{copy.target} · {receipt.target.accountId} · {receipt.target.versionId}</p>
        <p>{copy.identity} · {receipt.target.identity.displayName} · {receipt.target.identity.openId}</p>
        <details><summary>{copy.members}</summary>{receipt.members.map(member => <p key={member.runtimeId}>{member.role === 'publisher' ? copy.publisher : copy.followupMember} · {member.agentId} · {member.runtimeId} · {copy.oldBinding} {member.previousBindingRevision}</p>)}</details>
        {receipt.status === 'succeeded' && <p>{copy.continuation} · {receipt.continuation.nodeRunId} · {copy.attempt(receipt.continuation.attempt)} · {receipt.continuation.runId}</p>}
        </div></details>
        {receipt.status === 'succeeded' && <>
          <p role="status">{receipt.dispatchStatus === 'failed' || receipt.dispatchStatus === 'interrupted' ? copy.dispatchFailed : <>{copy.dispatch} · {copy.runStatus[receipt.dispatchStatus]}</>}</p>
        </>}
        {receipt.errorMessage && <p role="alert">{receipt.errorMessage}</p>}
        {action.error && <p role="status">{copy.originalUnavailable}</p>}
      </>}
      <Button type="button" variant="secondary" disabled={!readEnabled || action.isFetching} onClick={() => { void action.refetch() }}>{copy.recover}</Button>
      <ErrorAlert error={missing ? undefined : action.error} />
    </>}
  </section>
}
