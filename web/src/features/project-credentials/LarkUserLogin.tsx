import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { ApiError, type Schemas } from '@/api/client'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Label } from '@/components/ui/label'
import { selectClass } from '@/lib/field'
import { larkText as copy } from '@/i18n/lark'

type Phase = Schemas['CLIAuthOperation']['status']
export type LarkUserLoginProps = {
  projectId: string
  // Only server-approved application choices, not an arbitrary account list.
  sources: readonly Schemas['ProjectCredentialAccount'][]
  available: boolean
  disabled?: boolean
  cancelDisabled?: boolean
  phase?: Phase
  operationId?: string | null
  operationSource?: ReactNode
  sourceDescription?: (source: Schemas['ProjectCredentialAccount']) => ReactNode
  targetAccount?: Schemas['ProjectCredentialAccount']
  expiresAt?: Schemas['CLIAuthOperation']['expiresAt']
  recovery?: 'loading' | 'unknown' | 'not-found'
  authorizationReady?: boolean
  authorizationHref?: string
  userCode?: string | null
  error?: unknown
  savedAccount?: ReactNode
  applicationSetup?: ReactNode
  onStart: (source: Schemas['ProjectCredentialAccount']) => Promise<void>
  onRelogin?: (target: Schemas['ProjectCredentialAccount']) => Promise<void>
  onRecover: (originalId: string) => Promise<void>
  onCancel?: (originalId: string) => Promise<void>
  // Adapter owns official URL validation and native navigation. No blank popup,
  // network DTO, secret, URL persistence or OAuth implementation in this view.
  onOpenAuthorization?: () => void
}
export function LarkUserLogin(props: LarkUserLoginProps) {
  return <LarkLoginForm key={`${props.projectId}:${props.targetAccount?.id ?? 'new'}`} {...props} />
}
function LarkLoginForm(props: LarkUserLoginProps) {
  const { projectId, sources, available, phase, operationId, recovery } = props
  const [selected, setSelected] = useState<Schemas['ProjectCredentialAccount'] | null>(null)
  const [pending, setPending] = useState(false), [localError, setLocalError] = useState<unknown>(null)
  const [readMissing, setReadMissing] = useState(false)
  const guard = useRef(false), id = useId()
  const [now, setNow] = useState(Date.now)
  // Absorb same-operation terminal observations even if an old parent render
  // arrives late. The generated query adapter will also guard its caches.
  const [observed, setObserved] = useState<{ id: string | null; terminal?: Phase }>({ id: operationId ?? null })
  const terminal = phase && phase !== 'pending' && phase !== 'awaiting_authorization' ? phase : undefined
  if (observed.id !== (operationId ?? null)) setObserved({ id: operationId ?? null, terminal })
  else if (!observed.terminal && terminal) setObserved({ ...observed, terminal })
  const status = observed.id === (operationId ?? null) ? observed.terminal ?? phase : phase
  const active = status === 'pending' || status === 'awaiting_authorization'
  const deadline = props.expiresAt ? Date.parse(props.expiresAt) : Infinity
  const windowExpired = !Number.isFinite(deadline) && !!props.expiresAt || now >= deadline
  useEffect(() => {
    if (!active || !props.expiresAt || windowExpired) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [active, props.expiresAt, windowExpired])
  const scoped = sources.filter(a => a.projectId === projectId && a.purpose === 'cli' && a.cli === 'lark-cli' && a.authMethod !== 'oauth' && a.status !== 'deleted')
  const latest = selected && scoped.find(a => a.id === selected.id)
  const changed = !!selected && (!latest || latest.revision !== selected.revision || latest.currentVersionId !== selected.currentVersionId)
  const locked = pending || active || !!recovery
  const target = props.targetAccount
  const displayError = localError ?? props.error
  const missingOriginal = (readMissing || recovery === 'not-found') && displayError instanceof ApiError && displayError.status === 404
  const validTarget = target?.projectId === projectId && target.purpose === 'cli' && target.cli === 'lark-cli' && target.authMethod === 'oauth' && target.status !== 'deleted'
  const canStart = available && !props.disabled && !locked && (target ? validTarget && !!props.onRelogin : !!selected && !changed)
  const run = async (callback: () => Promise<void>, originalRead = false) => {
    if (guard.current) return
    guard.current = true; setPending(true); setLocalError(null); setReadMissing(false)
    try { await callback() } catch (error) { setLocalError(error); setReadMissing(originalRead && error instanceof ApiError && error.status === 404) }
    finally { guard.current = false; setPending(false) }
  }
  return <section aria-label={copy.title} className="grid min-w-0 gap-3 rounded-card bg-tint-blue p-4">
    <h3 className="font-bold">{copy.title}</h3><p className="text-sm">{copy.boundary}</p><p className="text-sm">{copy.scopes}</p>
    {!available && <p role="status">{copy.unsupported}</p>}
    {!target && !active && <><Label htmlFor={id}>{copy.application}</Label>
    <select id={id} className={selectClass} value={selected?.id ?? ''} disabled={!available || props.disabled || locked} onChange={event => { setSelected(scoped.find(a => a.id === event.target.value) ?? null); setLocalError(null) }}>
      <option value="">{copy.chooseApplication}</option>
      {selected && !latest && <option value={selected.id}>{copy.selected(selected.name, selected.revision)}</option>}
      {scoped.map(a => <option key={a.id} value={a.id}>{copy.selected(a.name, a.revision)}</option>)}
    </select>
    {scoped.length === 0 && <div><p>{copy.noApplication}</p>{props.applicationSetup}</div>}
    {changed && <p role="alert">{copy.sourceChanged}</p>}
    {selected && <div className="text-sm [overflow-wrap:anywhere]"><p>{copy.selected(selected.name, selected.revision)}</p>{props.sourceDescription?.(selected)}</div>}</>}
    {target && <div className="text-sm [overflow-wrap:anywhere]"><strong>{target.name}</strong><p>{copy.reloginApplication}</p></div>}
    {active && <div><strong>{copy.source}</strong><p>{props.operationSource ?? copy.sourceLocked}</p></div>}
    <Button type="button" className="justify-self-start" disabled={!canStart} onClick={() => {
      if (!canStart) return
      const relogin = props.onRelogin
      if (target && relogin) void run(() => relogin(target))
      else if (selected) void run(() => props.onStart(selected))
    }}>{status && !active ? copy.newAttempt : target ? copy.relogin : copy.start}</Button>
    {status && <p role="status">{copy[status]}</p>}
    {(recovery || readMissing) && <p role="status">{readMissing || recovery === 'not-found' ? observed.terminal ? copy.originalUnavailable : copy.notFound : recovery === 'loading' ? copy.recovering : copy.unknown}</p>}
    {operationId && recovery && <Button type="button" variant="secondary" disabled={pending || !available} onClick={() => void run(() => props.onRecover(operationId), true)}>{copy.recover}</Button>}
    {active && windowExpired && <p role="status">{copy.windowExpired}</p>}
    {status === 'awaiting_authorization' && !recovery && !windowExpired && available && !props.disabled && <div className="grid gap-2">
      {props.userCode && <p className="[overflow-wrap:anywhere]"><strong>{copy.code}</strong> {props.userCode}</p>}
      {props.authorizationHref && props.authorizationReady ? <Button asChild><a href={props.authorizationHref} target="_blank" rel="noopener noreferrer">{copy.open}</a></Button> : <Button type="button" disabled={!props.authorizationReady || !props.onOpenAuthorization} onClick={props.onOpenAuthorization}>{copy.open}</Button>}
    </div>}
    {active && operationId && props.onCancel && <Button type="button" variant="secondary" disabled={!available || (props.cancelDisabled ?? props.disabled) || pending} onClick={() => void run(() => props.onCancel!(operationId))}>{copy.cancel}</Button>}
    {props.savedAccount}
    <ErrorAlert error={missingOriginal ? undefined : displayError} />
  </section>
}
