import { useId, useRef, useState, type ReactNode } from 'react'
import { ApiError, type Schemas } from '@/api/client'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Label } from '@/components/ui/label'
import { selectClass } from '@/lib/field'
import { larkText as copy } from '@/i18n/lark'

// UI presentation inputs, not candidate HTTP DTOs. The formal adapter must
// provide server-authoritative eligibility/members and capture all wire CAS.
export type LarkSwitchMemberView = {
  identity: Pick<Schemas['TaskNodeRun'], 'agentId' | 'agentName' | 'runtimeId'>
  title: string
  originalPermission: ReactNode
  oldBinding: ReactNode
}
export type TaskLarkAccountSwitchProps = {
  task: Schemas['TaskSummary']
  node: Schemas['TaskNodeRun']
  members: readonly [LarkSwitchMemberView, LarkSwitchMemberView]
  accounts: readonly Schemas['ProjectCredentialAccount'][]
  // An opaque presentation fingerprint from the authoritative adapter; never
  // invent member revisions or use display names to infer runtime identity.
  reviewKey: string
  eligible: boolean
  disabledReason?: ReactNode
  stage?: 'review' | 'sending' | 'handing-over' | 'committed' | 'rejected' | 'unknown'
  actionId?: string | null
  dispatchStatus?: Schemas['RunStatus']
  error?: unknown
  recoveryError?: unknown
  onConfirm: (selected: Schemas['ProjectCredentialAccount']) => Promise<void>
  onRecover: (originalId: string) => Promise<void>
}
export function TaskLarkAccountSwitch(props: TaskLarkAccountSwitchProps) {
  return <SwitchForm key={props.task.id} {...props} />
}
function SwitchForm(props: TaskLarkAccountSwitchProps) {
  const { task, node, members, eligible, reviewKey } = props
  const [seen, setSeen] = useState<{ id: string | null; committed: boolean }>({ id: props.actionId ?? null, committed: false })
  if (seen.id !== (props.actionId ?? null)) setSeen({ id: props.actionId ?? null, committed: props.stage === 'committed' })
  else if (!seen.committed && props.stage === 'committed') setSeen({ ...seen, committed: true })
  const stage = seen.id === (props.actionId ?? null) && seen.committed ? 'committed' : props.stage ?? 'review'
  const [open, setOpen] = useState(false), [base, setBase] = useState('')
  const [selected, setSelected] = useState<Schemas['ProjectCredentialAccount'] | null>(null)
  const [pending, setPending] = useState(false), [localError, setLocalError] = useState<unknown>(null)
  const guard = useRef(false), id = useId()
  const accounts = props.accounts.filter(a => a.projectId === task.projectId && a.purpose === 'cli' && a.cli === 'lark-cli' && a.authMethod === 'oauth' && a.status === 'logged_in' && !!a.currentVersionId && !!a.larkIdentity)
  const latest = selected && accounts.find(a => a.id === selected.id)
  const fingerprint = JSON.stringify([reviewKey, task.id, task.version, task.currentNodeRunId, task.currentAgentId, node.id, node.agentId, node.runtimeId, members.map(m => [m.identity.agentId, m.identity.runtimeId])])
  const changed = open && base !== fingerprint || !!selected && (!latest || selected.revision !== latest.revision || selected.currentVersionId !== latest.currentVersionId)
  const validMembers = new Set(members.map(m => m.identity.runtimeId)).size === 2 && new Set(members.map(m => m.identity.agentId)).size === 2
    && members[0].identity.agentId === node.agentId && members[0].identity.runtimeId === node.runtimeId
  const current = task.status === 'blocked' && task.currentNodeRunId === node.id && task.currentAgentId === node.agentId
  const locked = pending || stage === 'sending' || stage === 'handing-over' || stage === 'committed' || stage === 'unknown'
  const canSubmit = current && eligible && validMembers && !changed && !locked && !!selected
  const dispatchFailed = props.dispatchStatus === 'failed' || props.dispatchStatus === 'interrupted'
  const readError = localError ?? props.recoveryError
  const readMissing = readError instanceof ApiError && readError.status === 404
  const recover = async () => {
    if (!props.actionId || guard.current) return
    const original = props.actionId
    guard.current = true; setPending(true); setLocalError(null)
    try { await props.onRecover(original) } catch (error) { setLocalError(error) }
    finally { guard.current = false; setPending(false) }
  }
  return <section aria-label={copy.switch} className="grid min-w-0 gap-3">
    <Button type="button" variant="secondary" disabled={!current || !eligible || !validMembers || locked} onClick={() => { setBase(fingerprint); setOpen(true); setLocalError(null) }}>{copy.switch}</Button>
    {!eligible && <p role="status">{props.disabledReason ?? copy.notEligible}</p>}
    {stage !== 'review' && <p role="status">{stage === 'committed' ? copy.committed : stage === 'unknown' ? copy.unknown : stage === 'handing-over' ? copy.handoff : stage === 'sending' ? copy.busy : copy.rejected}</p>}
    {stage === 'committed' && props.dispatchStatus && <p role="status">{dispatchFailed ? copy.dispatchFailed : <>{copy.dispatch} · {copy.runStatus[props.dispatchStatus]}</>}</p>}
    {props.actionId && <p className="text-xs [overflow-wrap:anywhere]">{copy.operation} · {props.actionId}</p>}
    {props.actionId && (stage === 'unknown' || stage === 'handing-over' || stage === 'committed') && <Button type="button" variant="secondary" disabled={pending} onClick={() => void recover()}>{copy.recover}</Button>}
    {readMissing && <p role="status">{stage === 'committed' ? copy.originalUnavailable : copy.notFound}</p>}
    <ErrorAlert error={readMissing ? props.error : readError ?? props.error} />
    <ConfirmDialog scrollBody open={open} onOpenChange={setOpen} title={copy.switchTitle} body={copy.twoMembers} confirmLabel={copy.confirm} confirmDisabled={!canSubmit} onConfirm={async () => {
      if (!selected || !canSubmit || guard.current) throw new Error(copy.changed)
      const captured = selected
      guard.current = true; setPending(true); setLocalError(null)
      try { await props.onConfirm(captured) }
      finally { guard.current = false; setPending(false) }
    }}>
      <dl className="grid min-w-0 gap-2 text-sm [overflow-wrap:anywhere]"><div><dt className="font-bold">{copy.task}</dt><dd>{task.title} · {task.id}</dd></div><div><dt className="font-bold">{copy.currentNode}</dt><dd>{node.stepId} · {node.agentName} · {node.id}</dd></div></dl>
      <div className="grid min-w-0 gap-3" aria-label={copy.members}>{members.map(member => <div key={member.identity.runtimeId} className="rounded-input bg-tint-blue p-3 text-sm [overflow-wrap:anywhere]"><strong>{member.title} · {member.identity.agentName}</strong><p>{member.identity.agentId} · {member.identity.runtimeId}</p><p>{copy.permission} · {member.originalPermission}</p><p>{copy.oldBinding} · {member.oldBinding}</p></div>)}</div>
      <Label htmlFor={id}>{copy.target}</Label>
      <select id={id} className={selectClass} value={selected?.id ?? ''} disabled={locked} onChange={event => setSelected(accounts.find(a => a.id === event.target.value) ?? null)}>
        <option value="">{copy.explicit}</option>
        {selected && !latest && <option value={selected.id}>{copy.version(selected.name, selected.currentVersionId ?? '')}</option>}
        {accounts.map(a => <option key={a.id} value={a.id}>{copy.version(a.name, a.currentVersionId ?? '')}</option>)}
      </select>
      {accounts.length === 0 && <p role="status">{copy.noUser}</p>}
      {selected && <p className="text-sm [overflow-wrap:anywhere]">{copy.version(selected.name, selected.currentVersionId ?? '')}</p>}
      {changed && <p role="alert">{copy.changed}</p>}
      <p className="text-sm">{copy.preserve}</p><p className="text-sm">{copy.followup}</p>
    </ConfirmDialog>
  </section>
}
