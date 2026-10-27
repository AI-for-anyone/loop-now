import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useInRouterContext, useSearchParams } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError, type Schemas } from '@/api/client'
import { acceptCLIAuthState, cancelCLIAuth, cliAuthActive, cliAuthKeys, useCLIAuthOperation, startCLIAuth, useProjectCLIAuth } from '@/api/cli-auth'
import { invalidateProjectCredentials } from '@/api/project-credentials'
import { backendSignals } from '@/i18n/backend-signals'
import { githubLoginText as copy } from '@/i18n/github-login'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useProjectCredentialsAvailable } from './project-availability'
import { nonzeroUUID } from './import-selection'
import { githubLocatorKey } from './github-authorization'

type Start = Extract<Schemas['CLIAuthStart'], { cli: 'gh' }>
export function GitHubLogin({ projectId, account }: { projectId: string; account?: Schemas['ProjectCredentialAccount'] }) {
  const routed = useInRouterContext()
  return routed ? <RoutedLogin key={`${projectId}:${account?.id ?? 'new'}`} projectId={projectId} account={account} />
    : <div className="grid gap-2"><Button type="button" variant="secondary" disabled>{account ? copy.relogin : copy.login}</Button><p className="text-[13px]">{copy.routeRequired}</p></div>
}
function RoutedLogin({ projectId, account }: { projectId: string; account?: Schemas['ProjectCredentialAccount'] }) {
  const [params, setParams] = useSearchParams(), key = githubLocatorKey(projectId, account?.id)
  const rawId = params.get(key), id = nonzeroUUID(rawId) ? rawId.toLowerCase() : rawId
  const locate = (nextId: string | null) => { const next = new URLSearchParams(params); if (nextId) next.set(key, nextId); else next.delete(key); setParams(next, { replace: true }) }
  return <Login projectId={projectId} account={account} id={id} locate={locate} />
}
function Login({ projectId, account, id, locate }: { projectId: string; account?: Schemas['ProjectCredentialAccount']; id: string | null; locate: (id: string | null) => void }) {
  const available = useProjectCredentialsAvailable(), client = useQueryClient()
  const [starting, setStarting] = useState(false), [cancelling, setCancelling] = useState(false)
  const [request, setRequest] = useState<Start | null>(null), [startError, setStartError] = useState<unknown>(null), [cancelError, setCancelError] = useState<unknown>(null)
  const [copiedFor, setCopiedFor] = useState<string | null>(null), [copyError, setCopyError] = useState(false), [now, setNow] = useState(Date.now)
  const mounted = useRef(true), latestId = useRef(id), latestLocate = useRef(locate), sent = useRef<string | null>(null), completed = useRef<string | null>(null), busy = useRef(false)
  useLayoutEffect(() => { latestId.current = id; latestLocate.current = locate }, [id, locate])
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const collection = useProjectCLIAuth(projectId, available, 'gh'), validId = nonzeroUUID(id)
  const refetchCollection = collection.refetch
  const detail = useCLIAuthOperation(projectId, 'gh', validId ? id : null, available && validId && !starting)
  const op = detail.data?.operation.id === id ? detail.data.operation : undefined
  const target = account?.id ?? null, mismatch = !!op && op.targetAccountId !== target
  const active = !!op && cliAuthActive(op), unresolved = !!id && !op
  const discovered = collection.data?.operations.find(item => item.cli === 'gh' && item.targetAccountId === target)
  useEffect(() => {
    if (!id && discovered && latestId.current === null) {
      latestId.current = discovered.id; latestLocate.current(discovered.id)
    }
  }, [id, discovered])
  useEffect(() => {
    if (!request || id !== request.operationId || sent.current === request.operationId) return
    // This effect runs only after the project/CLI/target route has committed.
    // Re-renders and retries of GET cannot dispatch this POST again.
    sent.current = request.operationId
    void (async () => {
      try {
        const next = await startCLIAuth(projectId, request)
        if (next.operation.targetAccountId !== ('accountId' in request ? request.accountId : null)) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.mismatch })
        if (!mounted.current || latestId.current !== request.operationId) return
        acceptCLIAuthState(client, projectId, 'gh', request.operationId, next)
      } catch (error) {
        if (!mounted.current || latestId.current !== request.operationId) return
        setStartError(error)
        // A definitive rejected POST is different from an unknown POST or GET404.
        // Release only this rejected ID; discovering an existing active login is GET-only.
        if (error instanceof ApiError && error.status > 0 && error.status < 500) {
          latestId.current = null; latestLocate.current(null)
        }
      } finally {
        if (mounted.current && (latestId.current === request.operationId || latestId.current === null)) {
          busy.current = false; setStarting(false); setRequest(null)
          void refetchCollection()
        }
      }
    })()
  }, [request, id, projectId, client, refetchCollection])
  useEffect(() => {
    if (!op || mismatch || op.status !== 'succeeded' || completed.current === op.id) return
    completed.current = op.id
    void invalidateProjectCredentials(client, projectId)
  }, [op, mismatch, client, projectId])
  useEffect(() => {
    if (!active) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [active])
  const unsupported = (error: unknown) => error instanceof ApiError && error.status === 404 && error.code === 'not_found' && error.serverMessage === backendSignals.missingEndpoint
  const notFound = detail.error instanceof ApiError && detail.error.status === 404 && !unsupported(detail.error)
  const unavailable = unsupported(collection.error) || unsupported(detail.error)
  const expiredLocally = !!op && now >= Date.parse(op.expiresAt)
  const validPair = op?.cli === 'gh' && op.status === 'awaiting_authorization' && op.authorizationUrl === 'https://github.com/login/device' && /^[A-Z0-9]{4}-[A-Z0-9]{4}$/.test(op.userCode)
  const canAuthorize = available && !mismatch && active && !expiredLocally && validPair
  const rejected = active && op?.status === 'awaiting_authorization' && !validPair
  const canStart = available && !starting && !active && !unresolved && !mismatch && !unavailable && !collection.isPending && !collection.isError && !discovered
  const begin = () => {
    if (!canStart || busy.current) return
    const operationId = crypto.randomUUID()
    const next: Start = account ? { operationId, cli: 'gh', authType: 'github_user', accountId: account.id, expectedRevision: account.revision } : { operationId, cli: 'gh', authType: 'github_user' }
    busy.current = true; setStarting(true); setStartError(null); setCancelError(null); setCopyError(false); setCopiedFor(null); setNow(Date.now()); setRequest(next)
    latestId.current = operationId; latestLocate.current(operationId)
  }
  const cancel = async () => {
    if (!op || !active || mismatch || cancelling || busy.current || !available) return
    const operationId = op.id; busy.current = true; setCancelling(true); setCancelError(null)
    try {
      const next = await cancelCLIAuth(projectId, operationId)
      if (!mounted.current || latestId.current !== operationId) return
      if (next.operation.targetAccountId !== target) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.mismatch })
      await client.cancelQueries({ queryKey: cliAuthKeys.operation(projectId, operationId, 'gh') })
      acceptCLIAuthState(client, projectId, 'gh', operationId, next)
      void collection.refetch()
    } catch (error) { if (mounted.current && latestId.current === operationId) setCancelError(error) }
    finally { if (mounted.current && latestId.current === operationId) { busy.current = false; setCancelling(false) } }
  }
  const copyCode = async () => {
    if (!canAuthorize || op?.cli !== 'gh' || op.status !== 'awaiting_authorization') return
    const operationId = op.id
    try { await navigator.clipboard.writeText(op.userCode); if (mounted.current && latestId.current === operationId) { setCopiedFor(operationId); setCopyError(false) } }
    catch { if (mounted.current && latestId.current === operationId) setCopyError(true) }
  }
  const statusText = starting ? copy.opening : op?.status === 'pending' ? copy.opening : op?.status === 'awaiting_authorization' ? copy.waiting : op?.status === 'succeeded' ? copy.success : op?.status === 'failed' ? copy.failed : op?.status === 'cancelled' ? copy.cancelled : op?.status === 'expired' ? copy.expired : null
  return <section aria-label={account ? copy.target(account.name) : copy.newTarget} className="grid min-w-0 gap-3 rounded-card bg-tint-blue p-4">
    <p className="font-bold [overflow-wrap:anywhere]">{account ? copy.target(account.name) : copy.newTarget}</p>
    <p className="text-[13px]">{copy.app}</p><p className="text-[13px]">{copy.scopes}</p><p className="text-[13px]">{copy.permission}</p>
    <p className="text-[13px]">{account ? copy.replaceIdentity : copy.newName}</p>
    <Button type="button" className="justify-self-start" disabled={!canStart} onClick={begin}>{op && !active ? copy.newAttempt : account ? copy.relogin : copy.login}</Button>
    {statusText && <p role="status" className="font-bold">{statusText}</p>}
    {op && !active && op.status !== 'succeeded' && account && <p className="text-[13px]">{copy.retained}</p>}
    {id && !validId && <p role="alert">{copy.invalidLocator}</p>}
    {unresolved && validId && !starting && !notFound && <p role="status" className="text-[13px]">{copy.unknown}</p>}
    {notFound && <p role="alert" className="text-[13px]">{copy.notFound}</p>}
    {mismatch && <p role="alert">{copy.mismatch}</p>}
    {unavailable && <p role="status">{copy.unsupported}</p>}
    {op && !mismatch && <><p className="text-[13px] [overflow-wrap:anywhere]">{copy.deadline}{op.expiresAt}</p>{active && <p role="status" className="text-[13px]">{expiredLocally ? copy.deadlineReached : copy.remaining(Math.max(0, Math.ceil((Date.parse(op.expiresAt) - now) / 1000)))}</p>}</>}
    {rejected && <p role="alert">{copy.rejected}</p>}
    {canAuthorize && op?.cli === 'gh' && op.status === 'awaiting_authorization' && <div className="grid min-w-0 gap-3 rounded-card bg-white p-4">
      <p className="text-[13px]">{copy.steps}</p><p className="text-sm font-bold">{copy.code}</p><code tabIndex={0} className="select-all font-mono text-[24px] tracking-widest [overflow-wrap:anywhere]">{op.userCode}</code>
      <div className="flex flex-wrap gap-2"><Button type="button" variant="secondary" size="sm" onClick={() => void copyCode()}>{copy.copy}</Button><Button asChild size="sm"><a href="https://github.com/login/device" target="_blank" rel="noopener noreferrer">{copy.open}</a></Button></div>
      {copiedFor === op.id && <p role="status" className="text-[13px]">{copy.copied}</p>}{copyError && <p role="alert" className="text-[13px]">{copy.copyFailed}</p>}
    </div>}
    {op?.errorCode && !mismatch && <p role="alert" className="text-[13px]">{copy.errors[op.errorCode]}</p>}
    {validId && <Button type="button" variant="secondary" className="justify-self-start" disabled={starting || detail.isFetching || !available} onClick={() => void detail.refetch()}>{detail.isFetching ? copy.reading : copy.recover}</Button>}
    {active && !mismatch && <Button type="button" variant="ghost" className="justify-self-start" disabled={starting || cancelling || !available} onClick={() => void cancel()}>{cancelling ? copy.cancelling : copy.cancel}</Button>}
    {collection.isError && !unavailable && <Button type="button" variant="secondary" onClick={() => void collection.refetch()}>{copy.refresh}</Button>}
    {!unavailable && <ErrorAlert error={cancelError ?? (notFound ? null : detail.error) ?? (!op ? startError : null) ?? collection.error} />}
    {id && <details className="text-[12px] [overflow-wrap:anywhere]"><summary>{copy.operation}{id}</summary></details>}
    <p className="text-[12px]">{copy.cancelHelp}</p>
  </section>
}
