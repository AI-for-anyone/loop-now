import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { Schemas } from '@/api/client'
import { cancelProjectLogin, credentialKeys, invalidateProjectCredentials, projectLoginActive, readProjectLogin, startProjectLogin, useProjectLogins } from '@/api/project-credentials'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ErrorAlert } from '@/components/ErrorAlert'
import { LoginFeedback } from '@/features/agent-config/LoginFeedback'
import { reserveAuthorizationWindow } from '@/features/agent-config/authorizationWindow'
import { projectAuthorizationUrl } from './project-authorization'
import { t } from '@/i18n'
import { useProjectCredentialsAvailable } from './project-availability'
import { useDirtyForm } from '@/features/prompts/draft-navigation'
const loginErrors: Readonly<Record<string, string>> = t.config.modelLogin.errors

export function ProjectLogin({ projectId, account }: { projectId: string; account?: Schemas['ProjectCredentialAccount'] }) {
  const available = useProjectCredentialsAvailable()
  const client = useQueryClient(), collection = useProjectLogins(projectId, available)
  const [name, setName] = useState(''), [local, setLocal] = useState<Schemas['ProjectModelAuthState'] | null>(null)
  const [starting, setStarting] = useState(false), [cancelling, setCancelling] = useState(false), [blocked, setBlocked] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const busy = useRef(false), mounted = useRef(true), popup = useRef<ReturnType<typeof reserveAuthorizationWindow> | null>(null), navigated = useRef<string | null>(null)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; popup.current?.closeUnused() } }, [])
  const discovered = collection.data?.operations.find(op => op.projectId === projectId && op.targetAccountId === (account?.id ?? null))
  if (!local && discovered) setLocal({ operation: discovered, account: null })
  const operation = local?.operation ?? discovered
  const currentId = useRef<string | null>(null)
  useEffect(() => { currentId.current = operation?.id ?? null }, [operation?.id])
  const detail = useQuery({ queryKey: [...credentialKeys.operations(projectId), operation?.id ?? null], queryFn: ({ signal }) => readProjectLogin(projectId, operation!.id, signal), enabled: query => available && !!operation && projectLoginActive(operation) && (!query.state.data || projectLoginActive(query.state.data.operation)), refetchIntervalInBackground: true, gcTime: 0, retry: false, refetchInterval: query => query.state.data && !projectLoginActive(query.state.data.operation) ? false : 1500 })
  const state = detail.data?.operation.id === operation?.id ? detail.data : local
  const op = state?.operation ?? operation
  const active = !!op && projectLoginActive(op)
  const authorizationHref = available && active && op?.authorizationUrl && projectAuthorizationUrl(op.authorizationUrl) ? op.authorizationUrl : undefined
  const rejectedAuthorization = active && !!op?.authorizationUrl && !authorizationHref
  useDirtyForm(`project-login:${projectId}:${account?.id ?? 'new'}`, !account && !!name.trim() && !active && op?.status !== 'succeeded')
  const [cleared, setCleared] = useState<string | null>(null)
  if (op?.status === 'succeeded' && cleared !== op.id) { setCleared(op.id); setName('') }
  const observed = useRef<string | null>(null)
  useEffect(() => {
    if (op?.status === 'succeeded' && observed.current !== op.id) {
      observed.current = op.id
      void invalidateProjectCredentials(client, projectId)
      void client.invalidateQueries({ queryKey: credentialKeys.operations(projectId) })
    }
    if (op && !projectLoginActive(op)) popup.current?.closeUnused()
    if (op?.authorizationUrl && projectLoginActive(op) && popup.current && navigated.current !== op.id) {
      const opened = projectAuthorizationUrl(op.authorizationUrl) && popup.current.navigate(op.authorizationUrl)
      setBlocked(!opened)
      if (opened) navigated.current = op.id
      else popup.current.closeUnused()
    }
  }, [op, client, projectId])
  const start = async () => {
    if (!available || busy.current || active || (!account && !name.trim())) return
    busy.current = true; setStarting(true); setError(null); setLocal(null)
    popup.current?.closeUnused(); popup.current = reserveAuthorizationWindow(); navigated.current = null
    try {
      const next = await startProjectLogin(projectId, account ? { provider: 'openai-codex', accountId: account.id, expectedRevision: account.revision } : { provider: 'openai-codex', name: name.trim() })
      if (!mounted.current) { popup.current?.closeUnused(); return }
      currentId.current = next.operation.id; setLocal(next)
      void client.invalidateQueries({ queryKey: credentialKeys.operations(projectId) })
    } catch (e) {
      if (mounted.current) { setError(e); popup.current?.closeUnused(); void collection.refetch() }
    } finally { busy.current = false; if (mounted.current) setStarting(false) }
  }
  const cancel = async () => {
    if (!available || !op || busy.current) return
    const id = op.id; busy.current = true; setCancelling(true); setError(null)
    try {
      const next = await cancelProjectLogin(projectId, id)
      if (mounted.current && currentId.current === id) {
        await client.cancelQueries({ queryKey: [...credentialKeys.operations(projectId), id] })
        client.setQueryData([...credentialKeys.operations(projectId), id], next); setLocal(next)
        void collection.refetch()
      }
    } catch (e) { if (mounted.current && currentId.current === id) setError(e) }
    finally { busy.current = false; if (mounted.current) setCancelling(false) }
  }
  const view = starting ? 'opening' : error && !active ? 'failed' : op?.status === 'pending' ? 'opening' : op?.status === 'awaiting_authorization' ? 'waiting' : op?.status === 'succeeded' ? 'success' : op?.status
  return <div className="grid min-w-0 gap-3">
    {!account && <div className="grid gap-1.5">
      <Label htmlFor={`login-name-${projectId}`}>{t.credentials.projectLogin.nameLabel}</Label>
      {/* 恢复进行中的操作只投影其固定名称，不覆盖用户尚未提交的本地草稿。 */}
      <Input id={`login-name-${projectId}`} maxLength={200} value={active ? (op?.name ?? name) : name} disabled={starting || active} aria-describedby={active ? `login-name-hint-${projectId}` : undefined} onChange={e => setName(e.target.value)} />
      {active && <p id={`login-name-hint-${projectId}`} role="status" className="text-[13px] text-ink-700">{t.credentials.projectLogin.nameLocked}{authorizationHref ? t.credentials.projectLogin.openOrCancel : rejectedAuthorization ? t.credentials.projectLogin.urlRejected : t.credentials.projectLogin.preparing}{t.credentials.projectLogin.restartNote}</p>}
    </div>}
    <Button type="button" variant="secondary" className="justify-self-start" disabled={!available || starting || active || (!account && !name.trim()) || collection.isPending} onClick={() => void start()}>{account ? t.credentials.projectLogin.relogin : t.credentials.projectLogin.login}</Button>
    {op?.name && <p className="text-[13px] [overflow-wrap:anywhere]">{t.credentials.projectLogin.newAccountLabel}{op.name}</p>}
    {view && <LoginFeedback view={view} detail={op?.errorCode ? loginErrors[op.errorCode] : undefined} hasAccount={!!account && account.status !== 'deleted'} blocked={blocked && !!authorizationHref} cancelling={cancelling} onCancel={active ? () => void cancel() : undefined} onRetry={!active ? () => void start() : undefined} authorizationHref={authorizationHref} onOpen={authorizationHref ? () => {
      // URL已知：由原生链接同步导航，清掉尚未导航的预留窗，避免旧WindowProxy/HMR引用干扰。
      popup.current?.closeUnused(); popup.current = null; navigated.current = op!.id; setBlocked(false)
    } : undefined} />}
    {rejectedAuthorization && <div role="alert" className="grid gap-2 text-[13px]"><p>{t.credentials.projectLogin.urlRejectedNoOpen}</p><Button type="button" size="sm" variant="secondary" onClick={() => void detail.refetch()}>{t.credentials.projectLogin.reread}</Button></div>}
    <ErrorAlert error={error ?? detail.error ?? collection.error} />
  </div>
}
