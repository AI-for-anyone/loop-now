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

// The own model credential of an Agent that doesn't belong to a project: an API key, or a subscription account logged in through the
// browser (Codex, or a plugin-registered provider with OAuth).
// Codex: on clicking log in, first open a blank window (to get past popup blockers), then navigate it once the authorization URL arrives.
// Plugin providers: same as project login; no window is opened in advance, and a URL that passes the generic check is given as a native
// link (naming the target host); there may also be a device code, or a question asked mid-way.
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
  // The latest operation may belong to the provider from before the model was changed; don't show it as the current provider's operation.
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
  // A plugin provider's authorization URL, device code and question are shown only while the login is in progress; the server has
  // validated the URL, and the generic check is applied here again.
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
    // Cancel earlier polling so that an older in-flight response can't overwrite the result of the cancel or the start.
    await client.cancelQueries({ queryKey })
    if (!mounted.current) return
    client.setQueryData<Schemas['ModelAuthState']>(queryKey, old => {
      // If another console has already started a newer operation, a stale cancel response must not switch it back to the old operation.
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
  // Answer a question asked mid-login: the answer is sent only with this request and never goes into any cache; after the server clears
  // the prompt, the next step may bring a new question or URL.
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
  // 409 plugin_not_enabled: the plugin isn't granted to this Agent (platform Agents have no project-enable layer); report it in the login
  // result instead of also showing a generic error.
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
        <h3 className="font-semibold">{copy.currentAccount}</h3>
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
      {view && <div className="grid min-w-0 gap-2"><h3 className="text-[14px] font-semibold">{copy.operation}</h3>
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
  return <Dialog open onOpenChange={open => { if (!open && !pending) { setKey(''); onClose() } }}><DialogContent kind="edit">
    <DialogHeader><DialogTitle>{t.config.modelLogin.setKey}</DialogTitle></DialogHeader>
    <form className="grid gap-4" onSubmit={event => void save(event)} autoComplete="off">
      <p className="text-[14px] break-words">{t.common.labelledValue(t.config.modelLogin.provider, agent.model.provider)}</p>
      <Input type="password" autoComplete="new-password" aria-label="API Key" value={key} onChange={event => { setKey(event.target.value); setInvalid(false) }} aria-invalid={invalid || undefined} />
      <FieldError message={invalid ? t.config.modelLogin.keyRequired : fieldMessages(error, field => field === 'key')} /><ErrorAlert error={unmatchedFieldError(error, field => field === 'key')} />
      <DialogFooter><Button type="button" variant="secondary" disabled={pending} onClick={() => { setKey(''); onClose() }}>{t.common.cancel}</Button><Button type="submit" disabled={pending}>{t.common.save}</Button></DialogFooter>
    </form>
  </DialogContent></Dialog>
}
