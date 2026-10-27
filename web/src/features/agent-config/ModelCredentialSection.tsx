import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import type { Schemas } from '@/api/client'
import { invalidateAgent, putModelCredential, useDeleteModelCredential, useModelProviders } from '@/api/queries'
import { cancelModelLogin, loginActive, modelAuthKey, startModelLogin, useModelAuth } from '@/api/model-auth'
import { t } from '@/i18n'
import { fieldMessages, unmatchedFieldError } from '@/lib/errors'
import { Section } from './Section'
import { WarningList } from './CredentialStatus'
import { LoginFeedback } from './LoginFeedback'
import { reserveAuthorizationWindow } from './authorizationWindow'
import { useSecretSubmit } from './useSecretSubmit'

export function ModelCredentialSection({ agent }: { agent: Schemas['Agent'] }) {
  const client = useQueryClient(), auth = useModelAuth(agent), providers = useModelProviders()
  const queryKey = modelAuthKey(agent.id, agent.model.provider, agent.configVersion)
  const [editing, setEditing] = useState(false), [deleting, setDeleting] = useState(false)
  const [starting, setStarting] = useState(false), [cancelling, setCancelling] = useState(false)
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
  const methods = providers.data?.providers.find(p => p.id === agent.model.provider)?.authMethods ?? []
  const oauth = agent.model.provider === 'openai-codex' && methods.includes('oauth')
  const apiKey = methods.includes('api_key')
  const unavailable = !auth.data || !!auth.error || !!providers.error || starting || active || cancelling
  const copy = t.config.modelLogin

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
    popup.current = reserveAuthorizationWindow()
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
  const open = () => {
    if (!active || !operation?.authorizationUrl) return
    const target = reserveAuthorizationWindow()
    setBlocked(!target.navigate(operation.authorizationUrl)); target.closeUnused()
  }
  const failedToStart = startFailed && (!operation || operation.id === previousOperation)
  const view = starting ? 'opening' : failedToStart ? 'failed' : operation?.status === 'pending' ? 'opening' : operation?.status === 'awaiting_authorization' ? 'waiting' : operation?.status === 'succeeded' ? 'success' : operation?.status
  return (
    <Section id="model-credential" title={t.config.modelCredential.title} actions={<>
      {oauth && <Button size="sm" variant="secondary" disabled={unavailable} onClick={() => void start()}>{credential ? copy.relogin : copy.login}</Button>}
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
      </div>
      {auth.isPending && <p className="text-[13px]">{copy.loading}</p>}
      {!providers.isPending && !providers.error && !oauth && !apiKey && <p className="text-[13px]">{copy.unsupported}</p>}
      <ErrorAlert error={auth.error ?? providers.error ?? error} />
      {(auth.error || providers.error) && <Button size="sm" variant="secondary" className="self-start" onClick={() => { void auth.refetch(); void providers.refetch() }}>{t.common.retry}</Button>}
      {view && <div className="grid min-w-0 gap-2"><h3 className="text-[14px] font-black">{copy.operation}</h3>
        <LoginFeedback view={view} hasAccount={!!credential} blocked={blocked} cancelling={cancelling}
          detail={!failedToStart && operation?.errorCode ? copy.errors[operation.errorCode] : undefined}
          onOpen={active && operation?.authorizationUrl ? open : undefined}
          onCancel={active && !starting && !auth.error ? () => void cancel() : undefined}
          onRetry={!unavailable && oauth ? () => void start() : undefined} />
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
  return <Dialog open onOpenChange={open => { if (!open) { setKey(''); onClose() } }}><DialogContent>
    <DialogHeader><DialogTitle>{t.config.modelLogin.setKey}</DialogTitle></DialogHeader>
    <form className="grid gap-4" onSubmit={event => void save(event)} autoComplete="off">
      <p className="text-[14px] break-words">{t.common.labelledValue(t.config.modelLogin.provider, agent.model.provider)}</p>
      <Input type="password" autoComplete="new-password" aria-label="API Key" value={key} onChange={event => { setKey(event.target.value); setInvalid(false) }} aria-invalid={invalid || undefined} />
      <FieldError message={invalid ? t.config.modelLogin.keyRequired : fieldMessages(error, field => field === 'key')} /><ErrorAlert error={unmatchedFieldError(error, field => field === 'key')} />
      <DialogFooter><Button type="button" variant="secondary" onClick={() => { setKey(''); onClose() }}>{t.common.cancel}</Button><Button type="submit" disabled={pending}>{t.common.save}</Button></DialogFooter>
    </form>
  </DialogContent></Dialog>
}
