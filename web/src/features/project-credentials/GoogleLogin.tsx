import { t } from '@/i18n'
import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiError, type Schemas } from '@/api/client'
import { cancelCLIAuth, cliAuthActive, cliAuthKeys, readCLIAuth, startCLIAuth, useProjectCLIAuth } from '@/api/cli-auth'
import { invalidateProjectCredentials } from '@/api/project-credentials'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ErrorAlert } from '@/components/ErrorAlert'
import { LoginFeedback } from '@/features/agent-config/LoginFeedback'
import { reserveAuthorizationWindow } from '@/features/agent-config/authorizationWindow'
import { useDirtyForm } from '@/features/prompts/draft-navigation'
import { useProjectCredentialsAvailable } from './project-availability'
import { googleAuthorizationUrl } from './google-authorization'

const errors: Record<Schemas['CLIAuthErrorCode'], string> = {
  callback_unavailable: t.credentials.google.callbackUnavailable,
  auth_cancelled: t.credentials.google.cancelled, auth_timeout: t.credentials.google.timeout,
  network_error: t.credentials.google.networkError, oauth_failed: t.credentials.google.oauthFailed,
  invalid_response: t.credentials.google.invalidResponse, account_mismatch: t.credentials.google.accountMismatch,
  requires_login: t.credentials.google.requiresLogin, helper_unavailable: t.credentials.google.helperUnavailable,
  version_conflict: t.credentials.google.versionConflict, server_restarted: t.credentials.google.serverRestarted,
}

export function GoogleLogin({ projectId, account }: { projectId: string; account?: Schemas['ProjectCredentialAccount'] }) {
  const available = useProjectCredentialsAvailable(), client = useQueryClient()
  const collection = useProjectCLIAuth(projectId, available)
  const [name, setName] = useState(''), [local, setLocal] = useState<Schemas['CLIAuthState'] | null>(null)
  const [starting, setStarting] = useState(false), [cancelling, setCancelling] = useState(false), [unknown, setUnknown] = useState(false)
  const [blocked, setBlocked] = useState(false), [error, setError] = useState<unknown>(null)
  const [pendingBody, setPendingBody] = useState<Schemas['CLIAuthStart'] | null>(null), [cleared, setCleared] = useState<string | null>(null)
  const pending = useRef<Schemas['CLIAuthStart'] | null>(null)
  const busy = useRef(false), mounted = useRef(true), popup = useRef<ReturnType<typeof reserveAuthorizationWindow> | null>(null)
  const navigated = useRef<string | null>(null), completed = useRef<string | null>(null)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; popup.current?.closeUnused() } }, [])
  const discovered = collection.data?.operations.find(op => op.projectId === projectId && op.cli === 'gcloud' && op.targetAccountId === (account?.id ?? null) && (!pendingBody || op.id === pendingBody.operationId))
  const operation = local?.operation ?? discovered
  const currentId = useRef<string | null>(null)
  useEffect(() => { currentId.current = operation?.id ?? null }, [operation?.id])
  const detail = useQuery({
    queryKey: cliAuthKeys.operation(projectId, operation?.id ?? null),
    queryFn: ({ signal }) => readCLIAuth(projectId, operation!.id, signal),
    enabled: query => available && !!operation && cliAuthActive(operation) && (!query.state.data || cliAuthActive(query.state.data.operation)),
    retry: false, gcTime: 0, refetchIntervalInBackground: true,
    refetchInterval: query => query.state.data && !cliAuthActive(query.state.data.operation) ? false : 1500,
  })
  const state = detail.data?.operation.id === operation?.id ? detail.data : local
  const op = state?.operation ?? operation, active = !!op && cliAuthActive(op)
  const authorizationHref = available && active && op?.authorizationUrl && googleAuthorizationUrl(op.authorizationUrl) ? op.authorizationUrl : undefined
  const rejected = active && !!op?.authorizationUrl && !authorizationHref
  useDirtyForm(`google-cli-login:${projectId}:${account?.id ?? 'new'}`, !account && !!name.trim() && !active && op?.status !== 'succeeded')
  if (active && unknown) { setUnknown(false); setError(null) }
  if (!local && discovered) setLocal({ operation: discovered, account: null })
  if (op && !active && local?.operation.id === op.id && cliAuthActive(local.operation)) setLocal(state ?? null)
  if (op && !active && pendingBody) setPendingBody(null)
  if (op?.status === 'succeeded' && cleared !== op.id) { setCleared(op.id); setName(''); setUnknown(false) }
  useEffect(() => {
    if (!op) return
    if (!active) { popup.current?.closeUnused(); pending.current = null }
    if (op.status === 'succeeded' && completed.current !== op.id) {
      completed.current = op.id
      void invalidateProjectCredentials(client, projectId)
      void client.invalidateQueries({ queryKey: cliAuthKeys.project(projectId) })
    }
    if (active && op.authorizationUrl && popup.current && navigated.current !== op.id) {
      const opened = googleAuthorizationUrl(op.authorizationUrl) && popup.current.navigate(op.authorizationUrl)
      setBlocked(!opened)
      if (opened) navigated.current = op.id
      else popup.current.closeUnused()
    }
  }, [op, active, unknown, local, discovered, state, client, projectId])
  const start = async () => {
    if (!available || busy.current || active || (!account && !name.trim())) return
    busy.current = true; setStarting(true); setError(null)
    const body = pending.current ?? (account ? { operationId: crypto.randomUUID(), cli: 'gcloud', authType: 'google_user', accountId: account.id, expectedRevision: account.revision } : { operationId: crypto.randomUUID(), cli: 'gcloud', authType: 'google_user', name: name.trim() }) satisfies Schemas['CLIAuthStart']
    pending.current = body; setPendingBody(body)
    setLocal(null); popup.current?.closeUnused(); popup.current = reserveAuthorizationWindow(); navigated.current = null
    try {
      const next = await startCLIAuth(projectId, body)
      if (!mounted.current) { popup.current?.closeUnused(); return }
      currentId.current = next.operation.id; setLocal(next); setUnknown(false)
      void collection.refetch()
    } catch (e) {
      if (mounted.current) {
        setError(e); popup.current?.closeUnused()
        const uncertain = e instanceof ApiError && (e.status === 0 || e.status >= 500)
        setUnknown(uncertain); if (!uncertain) { pending.current = null; setPendingBody(null) }
        void collection.refetch()
      }
    } finally { busy.current = false; if (mounted.current) setStarting(false) }
  }
  const cancel = async () => {
    if (!available || !op || !active || busy.current) return
    const id = op.id; busy.current = true; setCancelling(true); setError(null)
    try {
      const next = await cancelCLIAuth(projectId, id)
      if (mounted.current && currentId.current === id) {
        await client.cancelQueries({ queryKey: cliAuthKeys.operation(projectId, id) })
        client.setQueryData(cliAuthKeys.operation(projectId, id), next); setLocal(next)
        void collection.refetch()
      }
    } catch (e) { if (mounted.current && currentId.current === id) setError(e) }
    finally { busy.current = false; if (mounted.current) setCancelling(false) }
  }
  const recover = async () => {
    void collection.refetch()
    const id = pending.current?.operationId
    if (!id) return
    try { const next = await readCLIAuth(projectId, id); if (mounted.current && pending.current?.operationId === id) { setLocal(next); setUnknown(false); setError(null) } }
    catch (e) { if (mounted.current) setError(e) }
  }
  const view = starting ? 'opening' : active ? op!.status === 'pending' ? 'opening' : 'waiting' : op?.status === 'succeeded' ? 'success' : op?.status === 'failed' || op?.status === 'cancelled' || op?.status === 'expired' ? op.status : error ? 'failed' : undefined
  const lockedName = active ? op?.name ?? name : unknown && pendingBody && 'name' in pendingBody ? pendingBody.name : name
  return <div className="grid min-w-0 gap-3">
    <p className="font-bold">{t.credentials.google.title}</p>
    {!account && <div className="grid gap-1.5"><Label htmlFor={`google-cli-name-${projectId}`}>{t.credentials.google.nameLabel}</Label><Input id={`google-cli-name-${projectId}`} maxLength={200} value={lockedName} disabled={starting || active || unknown} onChange={event => setName(event.target.value)} /></div>}
    {active && <p role="status" className="text-[13px]">{t.credentials.google.targetFixed}{authorizationHref ? t.credentials.google.openOrCancel : rejected ? t.credentials.google.urlRejected : t.credentials.google.preparing}{t.credentials.google.refillNote}</p>}
    {op?.name && <p className="text-[13px] [overflow-wrap:anywhere]">{t.credentials.google.newAccountLabel}{op.name}</p>}
    <Button type="button" variant="secondary" className="justify-self-start" disabled={!available || starting || active || unknown || collection.isPending || collection.isError || (!account && !name.trim())} onClick={() => void start()}>{account ? t.credentials.google.relogin : t.credentials.google.login}</Button>
    {unknown && <div role="status" className="grid gap-2 text-[13px]"><p>{t.credentials.google.startUnknown}</p><Button type="button" size="sm" variant="secondary" onClick={() => void recover()}>{t.credentials.google.readActive}</Button></div>}
    {view && <LoginFeedback view={view} detail={op?.errorCode ? errors[op.errorCode] : unknown ? t.credentials.google.checkResult : view === 'opening' ? t.credentials.google.preparingGcloud : undefined} hasAccount={!!account && account.status !== 'deleted'} blocked={blocked && !!authorizationHref} cancelling={cancelling} retryLabel={unknown ? t.credentials.google.retrySameOperation : undefined} onCancel={active ? () => void cancel() : undefined} onRetry={!active ? () => void start() : undefined} authorizationHref={authorizationHref} onOpen={authorizationHref ? () => { popup.current?.closeUnused(); popup.current = null; navigated.current = op!.id; setBlocked(false) } : undefined} />}
    {rejected && <p role="alert" className="text-[13px] font-bold">{t.credentials.google.urlRejectedNoOpen}</p>}
    {(rejected || detail.isError || collection.isError) && <Button type="button" size="sm" variant="secondary" onClick={() => void (operation ? detail.refetch() : collection.refetch())}>{t.credentials.google.reread}</Button>}
    <ErrorAlert error={error ?? detail.error ?? collection.error} />
    <p className="text-[13px] text-ink-700">{t.credentials.google.note}</p>
  </div>
}
