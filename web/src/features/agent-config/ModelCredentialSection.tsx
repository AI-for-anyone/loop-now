import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { ApiError, type Schemas } from '@/api/client'
import { invalidateAgent, putModelCredential, useDeleteModelCredential, useModelProviders } from '@/api/queries'
import { answerModelLogin, cancelModelLogin, loginActive, modelAuthKey, startModelLogin, useModelAuth } from '@/api/model-auth'
import { LocalLoginNote } from '@/features/project-credentials/LocalLoginNote'
import { DeviceCode, LoginPrompt } from '@/features/project-credentials/ProjectLogin'
import { hostOf, pluginAuthorizationUrl } from '@/features/project-credentials/project-authorization'
import { pluginName, providerName } from '@/features/project-credentials/provider-catalog'
import { t } from '@/i18n'
import { fieldMessages, unmatchedFieldError } from '@/lib/errors'
import { Section } from './Section'
import { WarningList } from './CredentialStatus'
import { LoginFeedback } from './LoginFeedback'
import { reserveAuthorizationWindow } from './authorizationWindow'
import { useSecretSubmit } from './useSecretSubmit'

// 不属于项目的 Agent 自己的模型凭证：API Key，或浏览器登录订阅账号（Codex，或插件注册的带 OAuth 的提供方）。
// Codex：点登录时先开一个空白窗口（防弹窗拦截），拿到授权地址后导航过去。
// 插件提供方：与项目登录相同，不预开窗口，地址通过通用检查后给原生链接（写明目标主机）；还可能给设备码，或中途问一个问题。
export function ModelCredentialSection({ agent }: { agent: Schemas['Agent'] }) {
  const client = useQueryClient(), auth = useModelAuth(agent), providers = useModelProviders()
  const queryKey = modelAuthKey(agent.id, agent.model.provider, agent.configVersion)
  const [editing, setEditing] = useState(false), [deleting, setDeleting] = useState(false)
  const [starting, setStarting] = useState(false), [cancelling, setCancelling] = useState(false), [answering, setAnswering] = useState(false)
  const [blocked, setBlocked] = useState(false), [error, setError] = useState<unknown>(null)
  const [startFailed, setStartFailed] = useState(false)
  const remove = useDeleteModelCredential(agent.id)
  const mounted = useRef(true), submitting = useRef(false)
  const popup = useRef<ReturnType<typeof reserveAuthorizationWindow> | null>(null)
  const opened = useRef<string | null>(null), saved = useRef<string | null>(null)
  const [previousOperation, setPreviousOperation] = useState<string | null>(null)
  const credential = auth.data === undefined ? agent.modelCredential : auth.data.credential
  // 最新操作可能属于改模型前的提供方；不把它显示成当前提供方的操作。
  const candidate = auth.data?.operation
  const operation = candidate?.agentId === agent.id && candidate.provider === agent.model.provider ? candidate : null
  const active = loginActive(operation)
  const current = providers.data?.providers.find(p => p.id === agent.model.provider)
  const methods = current?.authMethods ?? []
  const plugin = current?.source === 'plugin'
  const oauth = methods.includes('oauth')
  const apiKey = methods.includes('api_key')
  const unavailable = !auth.data || !!auth.error || !!providers.error || starting || active || cancelling
  const copy = t.config.modelLogin, steps = t.credentials.projectLogin
  const loginLabel = plugin && current ? t.credentials.providerPicker.login(providerName(current.id, current)) : copy.login
  // 插件提供方的授权地址、设备码与问题只在登录进行中显示；地址由服务端校验过，这里再做通用检查。
  const authorizationHref = plugin && active && operation?.authorizationUrl && pluginAuthorizationUrl(operation.authorizationUrl) ? operation.authorizationUrl : undefined
  const rejectedAuthorization = plugin && active && !!operation?.authorizationUrl && !authorizationHref
  const device = plugin && active ? operation?.deviceCode ?? null : null
  const deviceHref = device && pluginAuthorizationUrl(device.verificationUri) ? device.verificationUri : undefined
  const prompt = plugin && active ? operation?.prompt ?? null : null

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; popup.current?.closeUnused() }
  }, [])

  useEffect(() => {
    if (!operation) return
    if (loginActive(operation) && operation.authorizationUrl && popup.current && opened.current !== operation.id) {
      opened.current = operation.id
      setBlocked(!popup.current.navigate(operation.authorizationUrl))
      popup.current.closeUnused()
    }
    if (!loginActive(operation)) popup.current?.closeUnused()
    if (operation.status === 'succeeded' && saved.current !== operation.id) {
      saved.current = operation.id
      void invalidateAgent(client, agent.id)
    }
  }, [operation, agent.id, client])

  const refresh = () => client.invalidateQueries({ queryKey })
  const applyOperation = async (next: Schemas['ModelAuthOperation'], newLogin = false) => {
    if (!mounted.current) return
    // 取消先前的轮询，避免较早的等待响应覆盖取消或发起的结果。
    await client.cancelQueries({ queryKey })
    if (!mounted.current) return
    client.setQueryData<Schemas['ModelAuthState']>(queryKey, old => {
      // 其他控制台若已开始更新的操作，旧取消响应不能把它换回旧操作。
      if (!newLogin && old?.operation?.id !== next.id) return old
      return { credential: old?.credential ?? credential, operation: next }
    })
    await refresh()
  }
  const start = async () => {
    if (submitting.current || unavailable || !oauth) return
    submitting.current = true
    popup.current?.closeUnused()
    popup.current = plugin ? null : reserveAuthorizationWindow()
    opened.current = null
    setPreviousOperation(operation?.id ?? null)
    setStartFailed(false); setBlocked(false); setError(null); setStarting(true)
    try {
      await client.cancelQueries({ queryKey })
      const next = await startModelLogin(agent)
      if (!mounted.current) { popup.current?.closeUnused(); return }
      await applyOperation(next, true)
    } catch (e) {
      popup.current?.closeUnused()
      if (mounted.current) { setStartFailed(true); setError(e); void refresh() }
    } finally {
      submitting.current = false
      if (mounted.current) setStarting(false)
    }
  }
  const cancel = async () => {
    if (!operation || !active || submitting.current) return
    submitting.current = true; setCancelling(true); setError(null)
    try { await applyOperation(await cancelModelLogin(agent.id, operation.id)) }
    catch (e) { if (mounted.current) { setError(e); void refresh() } }
    finally { submitting.current = false; if (mounted.current) setCancelling(false) }
  }
  // 回答登录中途的问题：回答只随这次请求发出，不进任何缓存；服务端清空 prompt 后，下一步可能带来新问题或地址。
  const answer = async (promptId: string, value: string) => {
    if (!operation || !active || submitting.current) return
    const id = operation.id
    submitting.current = true; setAnswering(true); setError(null)
    try {
      const next = await answerModelLogin(agent.id, id, { promptId, value })
      if (!mounted.current) return
      await client.cancelQueries({ queryKey })
      if (!mounted.current) return
      client.setQueryData<Schemas['ModelAuthState']>(queryKey, old => (old?.operation?.id === id ? next : old))
      await refresh()
    } catch (e) { if (mounted.current) setError(e) }
    finally { submitting.current = false; if (mounted.current) setAnswering(false) }
  }
  const open = () => {
    if (!active || !operation?.authorizationUrl) return
    const target = reserveAuthorizationWindow()
    setBlocked(!target.navigate(operation.authorizationUrl)); target.closeUnused()
  }
  const failedToStart = startFailed && (!operation || operation.id === previousOperation)
  // 409 plugin_not_enabled：插件没有授权给这个 Agent（平台 Agent 没有项目启用这一层），写进登录结果，不再另显示通用错误。
  const notGranted = failedToStart && plugin && error instanceof ApiError && error.code === 'plugin_not_enabled'
  const view = starting ? 'opening' : failedToStart ? 'failed' : operation?.status === 'pending' ? (prompt ? 'waiting' : 'opening') : operation?.status === 'awaiting_authorization' ? 'waiting' : operation?.status === 'succeeded' ? 'success' : operation?.status
  const detail = notGranted && current ? copy.notGranted(pluginName(current))
    : !failedToStart && operation?.errorCode ? copy.errors[operation.errorCode]
      : prompt ? steps.prompt.detail : device ? steps.device.intro(hostOf(device.verificationUri)) : undefined
  return (
    <Section id="model-credential" title={t.config.modelCredential.title} actions={<>
      {oauth && <Button size="sm" variant="secondary" disabled={unavailable} onClick={() => void start()}>{credential ? copy.relogin : loginLabel}</Button>}
      {apiKey && <Button size="sm" variant="secondary" disabled={unavailable} onClick={() => setEditing(true)}>{credential ? copy.replaceKey : copy.setKey}</Button>}
      {credential && <Button size="sm" variant="ghost" disabled={unavailable} onClick={() => setDeleting(true)}>{t.common.delete}</Button>}
    </>}>
      <div className="grid min-w-0 gap-2 text-[14px] break-words">
        <h3 className="font-black">{copy.currentAccount}</h3>
        <div><Badge tone={credential?.status === 'set' ? 'success' : credential ? 'danger' : 'neutral'}>{credential?.status === 'set' && credential.authMethod === 'oauth' ? copy.loggedIn : credential ? t.config.credentialStatus[credential.status] : t.config.credentialStatus.unset}</Badge></div>
        <p>{t.common.labelledValue(copy.provider, credential?.provider ?? agent.model.provider)}</p>
        {credential?.authMethod && <p>{t.common.labelledValue(copy.method, copy.methods[credential.authMethod])}</p>}
        {credential?.accountLabel && <p className="break-all">{t.common.labelledValue(copy.account, credential.accountLabel)}</p>}
        {credential && <p className="text-[13px] text-ink-500">{t.common.updatedAt(credential.updatedAt)}</p>}
        {credential && <WarningList warnings={credential.warnings} />}
        {auth.data?.localLogin && <LocalLoginNote state={auth.data.localLogin} owner="agent" />}
      </div>
      {auth.isPending && <p className="text-[13px]">{copy.loading}</p>}
      {!providers.isPending && !providers.error && !oauth && !apiKey && <p className="text-[13px]">{copy.unsupported}</p>}
      <ErrorAlert error={auth.error ?? providers.error ?? (notGranted ? null : error)} />
      {(auth.error || providers.error) && <Button size="sm" variant="secondary" className="self-start" onClick={() => { void auth.refetch(); void providers.refetch() }}>{t.common.retry}</Button>}
      {view && <div className="grid min-w-0 gap-2"><h3 className="text-[14px] font-black">{copy.operation}</h3>
        <LoginFeedback view={view} title={prompt ? steps.prompt.title : undefined} hasAccount={!!credential} blocked={blocked} cancelling={cancelling}
          detail={detail}
          authorizationHref={authorizationHref} openLabel={authorizationHref ? steps.openHost(hostOf(authorizationHref)) : undefined}
          onOpen={plugin ? (authorizationHref ? () => setBlocked(false) : undefined) : active && operation?.authorizationUrl ? open : undefined}
          onCancel={active && !starting && !auth.error ? () => void cancel() : undefined}
          onRetry={!unavailable && oauth ? () => void start() : undefined} />
        {device && <DeviceCode code={device} href={deviceHref} />}
        {prompt && operation && <LoginPrompt key={`${operation.id}:${prompt.id}`} prompt={prompt} disabled={answering || cancelling} answering={answering} onAnswer={value => answer(prompt.id, value)} />}
        {rejectedAuthorization && <div role="alert" className="grid gap-2 text-[13px]"><p>{steps.urlRejectedNoOpen}</p><Button type="button" size="sm" variant="secondary" className="justify-self-start" onClick={() => void auth.refetch()}>{steps.reread}</Button></div>}
        {active && operation && <p className="text-[12px] text-ink-500">{copy.expiresAt(operation.expiresAt)}</p>}
      </div>}
      {editing && <ApiKeyDialog agent={agent} onClose={() => setEditing(false)} onSaved={refresh} />}
      <ConfirmDialog open={deleting} onOpenChange={setDeleting} title={t.config.modelCredential.deleteTitle} body={t.config.modelCredential.deleteBody} confirmLabel={t.common.delete} destructive onConfirm={async () => { await remove.mutateAsync(); await refresh() }} />
    </Section>
  )
}

function ApiKeyDialog({ agent, onClose, onSaved }: { agent: Schemas['Agent']; onClose: () => void; onSaved: () => Promise<unknown> }) {
  const [key, setKey] = useState(''), [invalid, setInvalid] = useState(false)
  const { submit, pending, error, clearError } = useSecretSubmit(agent.id)
  const save = async (event: FormEvent) => {
    event.preventDefault()
    clearError()
    if (!key.trim()) { setInvalid(true); return }
    if (await submit(() => putModelCredential(agent.id, { provider: agent.model.provider, key, expectedConfigVersion: agent.configVersion }))) {
      setKey(''); await onSaved(); onClose()
    }
  }
  return <Dialog open onOpenChange={open => { if (!open) { setKey(''); onClose() } }}><DialogContent kind="edit">
    <DialogHeader><DialogTitle>{t.config.modelLogin.setKey}</DialogTitle></DialogHeader>
    <form className="grid gap-4" onSubmit={event => void save(event)} autoComplete="off">
      <p className="text-[14px] break-words">{t.common.labelledValue(t.config.modelLogin.provider, agent.model.provider)}</p>
      <Input type="password" autoComplete="new-password" aria-label="API Key" value={key} onChange={event => { setKey(event.target.value); setInvalid(false) }} aria-invalid={invalid || undefined} />
      <FieldError message={invalid ? t.config.modelLogin.keyRequired : fieldMessages(error, field => field === 'key')} /><ErrorAlert error={unmatchedFieldError(error, field => field === 'key')} />
      <DialogFooter><Button type="button" variant="secondary" onClick={() => { setKey(''); onClose() }}>{t.common.cancel}</Button><Button type="submit" disabled={pending}>{t.common.save}</Button></DialogFooter>
    </form>
  </DialogContent></Dialog>
}
