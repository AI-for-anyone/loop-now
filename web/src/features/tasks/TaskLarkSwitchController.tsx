import { useId, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { type Schemas } from '@/api/client'
import { useProjectAccounts } from '@/api/project-credentials'
import { taskKeys } from '@/api/tasks'
import { acceptTaskLarkSwitch, startTaskLarkSwitch } from '@/api/task-lark-switch'
import { taskLarkSwitchLocatorKey, useOriginalActionLocator } from '@/lib/useOriginalActionLocator'
import { TaskLarkAccountSwitch, type LarkSwitchMemberView } from './TaskLarkAccountSwitch'
import { TaskLarkSwitchRecovery } from './TaskLarkSwitchRecovery'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { selectClass } from '@/lib/field'
import { t } from '@/i18n'

const copy = t.credentials.lark

type Participant = Schemas['TaskLarkSwitchParticipant']
type Members = readonly [Participant, Participant]
const usable = (member: Participant) => member.available && member.larkAllowed === true
const memberKey = (member: Participant) => JSON.stringify([member.agentId, member.runtimeId, member.baseSnapshotSHA, member.larkAllowed, member.bindingRevision, member.prepared, member.available, member.unavailableReason, member.stepIds])
export function TaskLarkSwitchController({ detail, node, disabled }: { detail: Schemas['ConsoleTaskDetail']; node: Schemas['TaskNodeRun']; disabled: boolean }) {
  const locator = useOriginalActionLocator(taskLarkSwitchLocatorKey(detail.task.id)), client = useQueryClient()
  const [error, setError] = useState<unknown>(null)
  const current = detail.task.status === 'blocked' && detail.task.currentNodeRunId === node.id && detail.task.currentAgentId === node.agentId
  const participants = detail.larkSwitchParticipants
  // Retain view drafts during shared-SSE/HTTP calibration, never permission.
  // Only the current console projection can enable a new submission.
  const [lastParticipants, setLastParticipants] = useState(participants)
  if (participants !== undefined && participants !== lastParticipants) setLastParticipants(participants)
  const shownParticipants = participants ?? lastParticipants
  const submit = (account: Schemas['ProjectCredentialAccount'], members: Members) => new Promise<void>((resolve, reject) => {
    if (disabled || !current || locator.id || !account.currentVersionId || !participants || !members.every(member => usable(member) && participants.some(latest => memberKey(latest) === memberKey(member)))) { reject(new Error(copy.changed)); return }
    const targetVersionId = account.currentVersionId
    setError(null)
    const began = locator.begin(async actionId => {
      const body: Schemas['TaskLarkSwitchInput'] = { actionId, expectedTaskVersion: detail.task.version, expectedNodeRunId: node.id, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId, expectedLarkBindingRevision: members[0].bindingRevision, expectedMembers: members.map(m => ({ agentId: m.agentId, runtimeId: m.runtimeId, expectedBindingRevision: m.bindingRevision })), targetAccountId: account.id, targetVersionId, expectedAccountRevision: account.revision }
      try {
        acceptTaskLarkSwitch(client, await startTaskLarkSwitch(detail.task.id, body))
        void client.invalidateQueries({ queryKey: taskKeys.detail(detail.task.id) })
        resolve()
      } catch (failure) {
        // Any uncertain/rejected switch remains scoped to this original ID.
        // Never silently create a new action or fall back to retry/send.
        setError(failure); reject(failure); throw failure
      }
    })
    if (!began) reject(new Error(copy.changed))
  })
  return <>
    {shownParticipants !== undefined && current && <><ParticipantForm detail={detail} node={node} participants={shownParticipants} disabled={participants === undefined || disabled || !locator.valid || !!locator.id || locator.pending} stage={locator.pending ? 'sending' : 'review'} onConfirm={submit} />{participants === undefined && <p role="status">{copy.switchUnavailable}</p>}</>}
    <TaskLarkSwitchRecovery taskId={detail.task.id} currentBlocked={current && shownParticipants === undefined} showUnavailable={shownParticipants === undefined} readEnabled={!locator.pending} />
    <ErrorAlert error={error} />
  </>
}
function ParticipantForm({ detail, node, participants, disabled, stage, onConfirm }: { detail: Schemas['ConsoleTaskDetail']; node: Schemas['TaskNodeRun']; participants: Participant[]; disabled: boolean; stage: 'review' | 'sending' | 'handing-over' | 'committed' | 'rejected' | 'unknown'; onConfirm: (account: Schemas['ProjectCredentialAccount'], members: Members) => Promise<void> }) {
  const [selected, setSelected] = useState<Members | null>(null)
  const [publisherId, setPublisherId] = useState(''), [followupId, setFollowupId] = useState('')
  const accounts = useProjectAccounts(detail.task.projectId), id = useId()
  const publisher = participants.find(m => m.runtimeId === publisherId), followup = participants.find(m => m.runtimeId === followupId)
  const exact = publisher?.agentId === node.agentId && publisher.runtimeId === node.runtimeId && followup && followup.agentId !== publisher.agentId && followup.runtimeId !== publisher.runtimeId
  const changed = !!selected && (!publisher || !followup || JSON.stringify(selected) !== JSON.stringify([publisher, followup]))
  const choose = (first: string, second: string) => {
    setPublisherId(first); setFollowupId(second)
    const p = participants.find(m => m.runtimeId === first), f = participants.find(m => m.runtimeId === second)
    setSelected(p && f ? [p, f] : null)
  }
  const names = (member: Participant) => `${member.agentId} · ${member.runtimeId} · ${copy.frozenSteps} ${member.stepIds.join(', ')}`
  const reason = (member: Participant) => member.larkAllowed === null ? copy.permissionUnknown : !member.larkAllowed ? copy.permissionDenied : member.unavailableReason ?? copy.unavailableMember
  const view = (member: Participant): LarkSwitchMemberView => ({ identity: { agentId: member.agentId, runtimeId: member.runtimeId, agentName: member.agentId }, title: member.runtimeId === node.runtimeId ? copy.publisher : copy.followupMember, originalPermission: copy.permissionAllowed, oldBinding: <>{copy.bindingRevision} {member.bindingRevision} · {copy.originalSnapshot} {member.baseSnapshotSHA}<br />{member.prepared ? copy.prepared : copy.unprepared} · {copy.frozenSteps} {member.stepIds.join(', ')}</> })
  return <div className="mt-3 grid min-w-0 gap-2 text-sm [overflow-wrap:anywhere]">
    {participants.length === 0 && <p role="status">{copy.noParticipants}</p>}
    <Label htmlFor={id + '-publisher'}>{copy.choosePublisher}</Label>
    <select id={id + '-publisher'} className={selectClass} disabled={disabled} value={publisherId} onChange={event => choose(event.target.value, followupId)}>
      <option value="">{copy.chooseMember}</option>
      {publisherId && !publisher && <option value={publisherId} disabled>{copy.memberChanged} · {publisherId}</option>}
      {participants.filter(m => m.agentId === node.agentId && m.runtimeId === node.runtimeId).map(m => <option key={m.runtimeId} value={m.runtimeId} disabled={!usable(m)}>{names(m)}{!usable(m) ? ` · ${reason(m)}` : ''}</option>)}
    </select>
    <Label htmlFor={id + '-followup'}>{copy.chooseFollowup}</Label>
    <select id={id + '-followup'} className={selectClass} disabled={disabled} value={followupId} onChange={event => choose(publisherId, event.target.value)}>
      <option value="">{copy.chooseMember}</option>
      {followupId && !followup && <option value={followupId} disabled>{copy.memberChanged} · {followupId}</option>}
      {participants.map(m => <option key={m.runtimeId} value={m.runtimeId} disabled={!usable(m) || m.runtimeId === publisherId || m.agentId === publisher?.agentId}>{names(m)}{!usable(m) ? ` · ${reason(m)}` : ''}</option>)}
    </select>
    {changed && <p role="alert">{copy.memberChanged}</p>}
    <ErrorAlert error={accounts.error} />
    {!selected && <Button type="button" variant="secondary" disabled>{copy.switch}</Button>}
    {selected && <TaskLarkAccountSwitch task={detail.task} node={node} members={[view(selected[0]), view(selected[1])]} accounts={accounts.data?.accounts ?? []} reviewKey={JSON.stringify([detail.task.version, selected])} eligible={!disabled && !!exact && !changed && selected.every(usable) && !accounts.error && !accounts.isPending && accounts.data?.projectId === detail.task.projectId} disabledReason={changed ? copy.memberChanged : copy.notEligible} stage={stage} onConfirm={account => onConfirm(account, selected)} onRecover={async () => { /* Original GET control is rendered outside the modal. */ }} />}
  </div>
}
