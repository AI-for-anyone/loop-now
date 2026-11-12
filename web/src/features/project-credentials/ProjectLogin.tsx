import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { Schemas } from '@/api/client'
import { answerProjectLogin, cancelProjectLogin, credentialKeys, invalidateProjectCredentials, projectLoginActive, readProjectLogin, startProjectLogin, useProjectLogins } from '@/api/project-credentials'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { LoginFeedback } from '@/features/agent-config/LoginFeedback'
import { reserveAuthorizationWindow } from '@/features/agent-config/authorizationWindow'
import { pluginAuthorizationUrl, projectAuthorizationUrl } from './project-authorization'
import { providerName } from './provider-catalog'
import { t } from '@/i18n'
import { useProjectCredentialsAvailable } from './project-availability'
import { useDirtyForm } from '@/features/prompts/draft-navigation'
const loginErrors: Readonly<Record<string, string>> = t.config.modelLogin.errors
const copy = t.credentials.projectLogin

const hostOf = (url: string) => { try { return new URL(url).host } catch { return url } }

// 项目模型账号的浏览器登录。provider 是要登录的提供方：Codex，或插件注册的带 OAuth 的提供方（source 为 plugin）。
// Codex：点登录时先开一个空白窗口（防弹窗拦截），拿到授权地址后导航过去，地址走严格白名单。
// 插件提供方：服务端已校验地址，这里只做通用检查；不预开窗口，拿到地址后由人点原生链接（旁边写目标主机名）。
// 插件提供方还可能给设备码（打开地址输入代码），或在中途问一个问题（选一项、填文字、粘贴回调地址或授权码）。
export function ProjectLogin({ projectId, provider, account }: { projectId: string; provider: Schemas['ModelProvider']; account?: Schemas['ProjectCredentialAccount'] }) {
  const available = useProjectCredentialsAvailable()
  const plugin = provider.source === 'plugin'
  const displayName = providerName(provider.id, provider)
  const client = useQueryClient(), collection = useProjectLogins(projectId, available)
  const [name, setName] = useState(''), [local, setLocal] = useState<Schemas['ProjectModelAuthState'] | null>(null)
  const [starting, setStarting] = useState(false), [cancelling, setCancelling] = useState(false), [answering, setAnswering] = useState(false), [blocked, setBlocked] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const busy = useRef(false), mounted = useRef(true), popup = useRef<ReturnType<typeof reserveAuthorizationWindow> | null>(null), navigated = useRef<string | null>(null)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; popup.current?.closeUnused() } }, [])
  const discovered = collection.data?.operations.find(op => op.projectId === projectId && op.provider === provider.id && op.targetAccountId === (account?.id ?? null))
  if (!local && discovered) setLocal({ operation: discovered, account: null })
  const operation = local?.operation ?? discovered
  const currentId = useRef<string | null>(null)
  useEffect(() => { currentId.current = operation?.id ?? null }, [operation?.id])
  const detail = useQuery({ queryKey: [...credentialKeys.operations(projectId), operation?.id ?? null], queryFn: ({ signal }) => readProjectLogin(projectId, operation!.id, signal), enabled: query => available && !!operation && projectLoginActive(operation) && (!query.state.data || projectLoginActive(query.state.data.operation)), refetchIntervalInBackground: true, gcTime: 0, retry: false, refetchInterval: query => query.state.data && !projectLoginActive(query.state.data.operation) ? false : 1500 })
  const state = detail.data?.operation.id === operation?.id ? detail.data : local
  const op = state?.operation ?? operation
  const active = !!op && projectLoginActive(op)
  const safeUrl = (url: string) => (plugin ? pluginAuthorizationUrl(url) : projectAuthorizationUrl(url))
  const authorizationHref = available && active && op?.authorizationUrl && safeUrl(op.authorizationUrl) ? op.authorizationUrl : undefined
  const rejectedAuthorization = active && !!op?.authorizationUrl && !authorizationHref
  const device = available && active && plugin ? op?.deviceCode ?? null : null
  const deviceHref = device && pluginAuthorizationUrl(device.verificationUri) ? device.verificationUri : undefined
  const prompt = available && active && plugin ? op?.prompt ?? null : null
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
    popup.current?.closeUnused(); popup.current = plugin ? null : reserveAuthorizationWindow(); navigated.current = null
    try {
      const next = await startProjectLogin(projectId, account ? { provider: provider.id, accountId: account.id, expectedRevision: account.revision } : { provider: provider.id, name: name.trim() })
      if (!mounted.current) { popup.current?.closeUnused(); return }
      currentId.current = next.operation.id; setLocal(next)
      void client.invalidateQueries({ queryKey: credentialKeys.operations(projectId) })
    } catch (e) {
      if (mounted.current) { setError(e); popup.current?.closeUnused(); void collection.refetch() }
    } finally { busy.current = false; if (mounted.current) setStarting(false) }
  }
  const apply = async (id: string, next: Schemas['ProjectModelAuthState']) => {
    await client.cancelQueries({ queryKey: [...credentialKeys.operations(projectId), id] })
    client.setQueryData([...credentialKeys.operations(projectId), id], next); setLocal(next)
  }
  const cancel = async () => {
    if (!available || !op || busy.current) return
    const id = op.id; busy.current = true; setCancelling(true); setError(null)
    try {
      const next = await cancelProjectLogin(projectId, id)
      if (mounted.current && currentId.current === id) {
        await apply(id, next)
        void collection.refetch()
      }
    } catch (e) { if (mounted.current && currentId.current === id) setError(e) }
    finally { busy.current = false; if (mounted.current) setCancelling(false) }
  }
  // 回答中途的问题：回答只随这次请求发出，不进任何缓存；服务端清空 prompt 后，下一步可能带来新问题或地址。
  const answer = async (promptId: string, value: string) => {
    if (!available || !op || busy.current) return
    const id = op.id; busy.current = true; setAnswering(true); setError(null)
    try {
      const next = await answerProjectLogin(projectId, id, { promptId, value })
      if (mounted.current && currentId.current === id) await apply(id, next)
    } catch (e) { if (mounted.current && currentId.current === id) setError(e) }
    finally { busy.current = false; if (mounted.current) setAnswering(false) }
  }
  const view = starting ? 'opening' : error && !active ? 'failed' : op?.status === 'pending' ? (prompt ? 'waiting' : 'opening') : op?.status === 'awaiting_authorization' ? 'waiting' : op?.status === 'succeeded' ? 'success' : op?.status
  const guidance = prompt || device ? copy.continueOrCancel : authorizationHref ? copy.openOrCancel : rejectedAuthorization ? copy.urlRejected : copy.preparing
  const feedbackDetail = op?.errorCode ? loginErrors[op.errorCode] : prompt ? copy.prompt.detail : device ? copy.device.intro(hostOf(device.verificationUri)) : undefined
  return <div className="grid min-w-0 gap-3">
    {!account && <div className="grid gap-1.5">
      <Label htmlFor={`login-name-${projectId}-${provider.id}`}>{copy.nameLabel(displayName)}</Label>
      {/* 恢复进行中的操作只投影其固定名称，不覆盖用户尚未提交的本地草稿。 */}
      <Input id={`login-name-${projectId}-${provider.id}`} maxLength={200} value={active ? (op?.name ?? name) : name} disabled={starting || active} aria-describedby={active ? `login-name-hint-${projectId}-${provider.id}` : undefined} onChange={e => setName(e.target.value)} />
      {active && <p id={`login-name-hint-${projectId}-${provider.id}`} role="status" className="text-[13px] text-ink-700">{copy.nameLocked}{guidance}{copy.restartNote}</p>}
    </div>}
    <Button type="button" variant="secondary" className="justify-self-start" disabled={!available || starting || active || (!account && !name.trim()) || collection.isPending} onClick={() => void start()}>{account ? copy.relogin : t.credentials.providerPicker.login(displayName)}</Button>
    {op?.name && <p className="text-[13px] [overflow-wrap:anywhere]">{copy.newAccountLabel}{op.name}</p>}
    {view && <LoginFeedback view={view} title={prompt ? copy.prompt.title : undefined} detail={feedbackDetail} hasAccount={!!account && account.status !== 'deleted'} blocked={blocked && !!authorizationHref} cancelling={cancelling} onCancel={active ? () => void cancel() : undefined} onRetry={!active ? () => void start() : undefined} authorizationHref={authorizationHref} openLabel={plugin && authorizationHref ? copy.openHost(hostOf(authorizationHref)) : undefined} onOpen={authorizationHref ? () => {
      // URL已知：由原生链接同步导航，清掉尚未导航的预留窗，避免旧WindowProxy/HMR引用干扰。
      popup.current?.closeUnused(); popup.current = null; navigated.current = op!.id; setBlocked(false)
    } : undefined} />}
    {device && <DeviceCode code={device} href={deviceHref} />}
    {prompt && <LoginPrompt key={`${op!.id}:${prompt.id}`} prompt={prompt} disabled={answering || cancelling} answering={answering} onAnswer={value => answer(prompt.id, value)} />}
    {rejectedAuthorization && <div role="alert" className="grid gap-2 text-[13px]"><p>{copy.urlRejectedNoOpen}</p><Button type="button" size="sm" variant="secondary" onClick={() => void detail.refetch()}>{copy.reread}</Button></div>}
    <ErrorAlert error={error ?? detail.error ?? collection.error} />
  </div>
}

// 设备码登录：大号显示代码（可复制），链接到输入代码的地址（服务端已校验，这里再做通用检查），写明代码的有效期。
function DeviceCode({ code, href }: { code: Schemas['ProjectModelLoginDeviceCode']; href?: string }) {
  const labelId = useId()
  const [copied, setCopied] = useState(false)
  const copyCode = async () => {
    try { await navigator.clipboard.writeText(code.userCode) } catch { return }
    setCopied(true); setTimeout(() => setCopied(false), 1500)
  }
  return <div className="grid min-w-0 gap-2 rounded-input bg-tint-blue p-3 text-[13px]">
    <span id={labelId} className="font-bold">{copy.device.code}</span>
    <div className="flex min-w-0 flex-wrap items-center gap-2">
      <code aria-labelledby={labelId} className="min-w-0 font-mono text-[20px] leading-tight font-black tracking-[0.12em] select-all [overflow-wrap:anywhere]">{code.userCode}</code>
      <Button type="button" size="xs" variant="secondary" className="h-8 bg-riso-yellow px-3 text-[12.5px] hover:bg-riso-yellow" aria-describedby={labelId} onClick={() => void copyCode()}>{copied ? t.common.copied : t.common.copy}</Button>
    </div>
    {href
      ? <a href={href} target="_blank" rel="noopener noreferrer" className="justify-self-start font-bold text-riso-blue underline [overflow-wrap:anywhere]">{copy.device.open(hostOf(href))}</a>
      : <p role="alert" className="font-bold text-red-text">{copy.device.urlRejected}</p>}
    {code.expiresAt && <p className="text-ink-700">{copy.device.expires(t.chat.time(code.expiresAt))}</p>}
  </div>
}

// 登录中途的问题：select 列出选项让人自己选（不替人预选），text 填文字，manual_code 粘贴回调地址或授权码。
// 问题与选项是扩展原文，按纯文本显示。回答按秘密处理：提交后清空，不放进缓存。
function LoginPrompt({ prompt, disabled, answering, onAnswer }: { prompt: Schemas['ProjectModelLoginPrompt']; disabled: boolean; answering: boolean; onAnswer: (value: string) => Promise<void> }) {
  const id = useId()
  const [value, setValue] = useState(''), [missing, setMissing] = useState(false)
  const text = copy.prompt
  const message = prompt.message.trim() || (prompt.kind === 'manual_code' ? text.manualDefault : prompt.kind === 'text' ? text.textDefault : '')
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (disabled) return
    if (!value) { setMissing(true); return }
    setMissing(false)
    const sent = value
    setValue('')
    await onAnswer(sent)
  }
  const error = missing ? (prompt.kind === 'select' ? text.choose : text.required) : undefined
  return <form className="grid min-w-0 gap-2.5 rounded-input bg-tint-yellow p-3 text-[13.5px]" onSubmit={event => void submit(event)} noValidate>
    {prompt.kind === 'select'
      ? <fieldset className="grid min-w-0 gap-1.5" aria-describedby={error ? `${id}-error` : undefined}>
        <legend className="mb-1 font-bold whitespace-pre-wrap [overflow-wrap:anywhere]">{message || text.choose}</legend>
        {prompt.options.map(option => <label key={option.id} className="flex min-w-0 items-start gap-2">
          <input type="radio" name={id} value={option.id} checked={value === option.id} disabled={disabled} onChange={() => setValue(option.id)} className="mt-1 shrink-0" />
          <span className="min-w-0 whitespace-pre-wrap [overflow-wrap:anywhere]">{option.label}</span>
        </label>)}
      </fieldset>
      : <div className="grid min-w-0 gap-1.5">
        <Label htmlFor={`${id}-value`} className="whitespace-pre-wrap [overflow-wrap:anywhere]">{message}</Label>
        <Input id={`${id}-value`} type="text" autoComplete="off" spellCheck={false} maxLength={4096} value={value} disabled={disabled} aria-invalid={error ? true : undefined} aria-describedby={error ? `${id}-error` : undefined} onChange={event => setValue(event.target.value)} className={prompt.kind === 'manual_code' ? 'font-mono text-[13px]' : undefined} />
      </div>}
    {error && <div id={`${id}-error`}><FieldError message={error} /></div>}
    <Button type="submit" size="sm" className="justify-self-start" disabled={disabled}>{answering ? text.submitting : text.submit}</Button>
  </form>
}
