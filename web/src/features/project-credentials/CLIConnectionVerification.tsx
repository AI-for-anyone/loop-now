import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError, type Schemas } from '@/api/client'
import { cancelVerification, mergeVerification, startVerification, useVerificationOperation, useVerificationState, verificationActive, verificationKeys, verificationUnsupported, type Verification } from '@/api/cli-verification'
import { keys } from '@/api/queries'
import { credentialKeys } from '@/api/project-credentials'
import { t } from '@/i18n'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useProjectCredentialsAvailable } from './project-availability'

const copy = t.credentials.verification

const builtin = new Set(['gh', 'lark-cli', 'gcloud', 'psql', 'mysql'])
export function CLIConnectionVerification({ account }: { account: Schemas['ProjectCredentialAccount'] }) {
  if (account.purpose !== 'cli' || !account.cli || !builtin.has(account.cli) || !account.currentVersionId || account.status === 'deleted') return null
  return <VerificationVersion key={`${account.projectId}:${account.id}:${account.currentVersionId}`} account={account} version={account.currentVersionId} />
}
function VerificationVersion({ account, version }: { account: Schemas['ProjectCredentialAccount']; version: string }) {
  const available = useProjectCredentialsAvailable(), client = useQueryClient()
  const [requested, setRequested] = useState<string | null>(null), [unknown, setUnknown] = useState(false)
  const [starting, setStarting] = useState(false), [cancelling, setCancelling] = useState(false), [error, setError] = useState<unknown>(null)
  const state = useVerificationState(account.projectId, account.id, version, available)
  const discovered = state.data?.operation
  const id = requested ?? discovered?.id ?? null
  const detail = useVerificationOperation(account.projectId, account.id, version, id, available && !starting)
  const op = detail.data?.id === id ? detail.data : discovered?.id === id ? discovered : undefined
  const targetMatches = !op || op.projectId === account.projectId && op.accountId === account.id && op.versionId === version && op.cli === account.cli
  const current = targetMatches && (!op || op.isCurrentVersion) && (!state.data || state.data.accountId === account.id && state.data.currentVersionId === version)
  const active = !!op && verificationActive(op)
  const terminalId = op && !active ? op.id : null
  useEffect(() => {
    // Installation is an independent server fact; reread it after the worker settles.
    if (terminalId) void client.invalidateQueries({ queryKey: keys.cliTemplates })
  }, [client, terminalId])
  const unsupported = verificationUnsupported(state.error)
  const requestError = unknown && op ? detail.error ?? state.error : error ?? detail.error ?? state.error
  const conflict = error instanceof ApiError && error.code === 'verification_in_progress'
  const changed = !current || error instanceof ApiError && error.status === 412 && error.details && 'expectedVersionId' in error.details
  const query = async () => { await Promise.allSettled([state.refetch(), ...(id ? [detail.refetch()] : [])]) }
  const store = (next: Verification) => {
    if (next.projectId !== account.projectId || next.accountId !== account.id || next.versionId !== version || next.cli !== account.cli) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.invalidReceipt })
    const key = verificationKeys.operation(account.projectId, account.id, version, next.id)
    client.setQueryData<Verification>(key, old => mergeVerification(old, next))
  }
  const start = async () => {
    if (!available || starting || active || unknown && !op || !current || state.isError || state.isPending || unsupported) return
    const operationId = crypto.randomUUID()
    setStarting(true); setError(null)
    try {
      const next = await startVerification(account.projectId, account.id, { operationId, expectedVersionId: version })
      if (next.id !== operationId) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.invalidReceipt })
      store(next); setRequested(operationId); setUnknown(false)
      void state.refetch()
    } catch (cause) {
      setError(cause)
      const uncertain = cause instanceof ApiError && (cause.status === 0 || cause.status >= 500)
      if (uncertain) { setRequested(operationId); setUnknown(true) }
      if (cause instanceof ApiError && cause.status === 412) void client.invalidateQueries({ queryKey: credentialKeys.project(account.projectId) })
      if (cause instanceof ApiError && cause.code === 'verification_in_progress' && cause.details && 'operationId' in cause.details) { setRequested(cause.details.operationId); void state.refetch() }
    } finally { setStarting(false) }
  }
  const cancel = async () => {
    if (!available || !op || !active || cancelling || !current) return
    const operationId = op.id
    setCancelling(true); setError(null)
    try {
      const next = await cancelVerification(account.projectId, account.id, operationId)
      if (next.id !== operationId) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.invalidReceipt })
      await client.cancelQueries({ queryKey: verificationKeys.account(account.projectId, account.id, version) })
      store(next)
      client.setQueryData<Schemas['CLIVerificationState']>(verificationKeys.state(account.projectId, account.id, version), { accountId: account.id, currentVersionId: version, operation: next })
      setUnknown(false)
    } catch (cause) { setError(cause) }
    finally { setCancelling(false) }
  }
  // An operation found by GET resolves an uncertain POST without sending it again.
  const unresolved = unknown && !op
  return <section aria-label={`${copy.title} · ${account.name}`} className="grid min-w-0 gap-2 rounded-input bg-tint-blue p-3 text-[13px]">
    <h4 className="font-bold">{copy.title}</h4><p>{copy.scope}</p><p>{copy.prepare}</p>
    {unsupported ? <p role="status">{copy.unsupported}</p> : <>
      {state.isPending && <p role="status">{copy.reading}</p>}
      {changed && <p role="status" className="font-bold">{copy.changed}</p>}
      {unresolved && <p role="status">{copy.unknown}</p>}
      {conflict && <p role="status">{copy.conflict}</p>}
      {op && targetMatches && <div role="status" className="grid gap-1">
        <p className="font-bold">{copy.status[op.status]}</p>
        {!op.isCurrentVersion && <p>{copy.old}</p>}
        {active && op.phase && <p>{copy.phase[op.phase]}</p>}
        {op.status === 'succeeded' && op.result?.outcome === 'passed' && <><p>{copy.depth[op.probeKind]}</p><p>{copy.method[op.authMethod]}</p></>}
        {op.result?.errorCode && <p>{copy.errors[op.result.errorCode]}</p>}
        {op.result?.verifiedAt && <p>{copy.time}{op.result.verifiedAt}</p>}
        {op.completedAt && <p>{copy.completed}{op.completedAt}</p>}
        {active && <p>{copy.deadline}{op.expiresAt}</p>}
        {op.toolVersion && <p>{copy.tool}{op.toolVersion}</p>}
        <details><summary>{copy.details}</summary><p className="[overflow-wrap:anywhere]">{copy.version}{op.versionId}</p>{op.result?.httpStatus && <p>{copy.http}{op.result.httpStatus}</p>}</details>
      </div>}
      {!op && state.data && !state.error && !detail.error && !unresolved && !changed && <p>{copy.empty}</p>}
      <ErrorAlert error={requestError} />
    </>}
    <div className="flex flex-wrap gap-2">
      <Button type="button" size="sm" variant="secondary" disabled={!available || starting || active || unresolved || !!changed || state.isError || state.isPending || unsupported} onClick={() => void start()}>{starting ? copy.status.pending : op ? copy.again : copy.test}</Button>
      {active && <Button type="button" size="sm" variant="ghost" disabled={!available || cancelling || !current} onClick={() => void cancel()}>{cancelling ? copy.cancelling : copy.cancel}</Button>}
      {(unresolved || active || requestError || changed || unsupported) && <Button type="button" size="sm" variant="ghost" disabled={!available || state.isFetching || detail.isFetching} onClick={() => void query()}>{id ? copy.recover : copy.reload}</Button>}
    </div>
  </section>
}
