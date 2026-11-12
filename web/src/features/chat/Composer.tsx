import { useEffect, useId, useImperativeHandle, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode, type Ref } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowUpIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ApiError, type Schemas } from '@/api/client'
import { downloadAgentSession } from '@/api/session-export'
import { isCommandResult, upsertMessage, useAgentCommands, useSendMessage } from '@/api/queries'
import { t } from '@/i18n'
import { ImagePicker } from '@/features/images/ImagePicker'
import { imageProblem, type ImageDraft, type ImageLimits, type ImageUploads } from '@/features/images/image-drafts'
import { uploadMessageImages } from '@/api/message-images'
import { cn } from '@/lib/utils'
import { ReplyChip } from '@/features/console-inbox/ConsoleMessage'
import { useAnswerQuestion } from '@/features/console-inbox/useAnswerQuestion'
import { describeError } from '@/lib/errors'
import { applyCommand, matchCommands, slashToken, type Command } from './slash-commands'
import { SlashCommandMenu } from './SlashCommandMenu'

// 编辑撤回的排队消息时，把原文与图片放回输入框；输入框里已有的内容保留在前面。
export interface ComposerHandle {
  refill: (text: string, images: ImageDraft[]) => void
  resume: (sessionId: string) => void
}

// 停止当前回复：忙时输入框为空，发送按钮换成停止。
export interface StopControl {
  pending: boolean
  // 上次停止的结果不明，再点沿用原 actionId。
  retry: boolean
  onStop: () => void
}

// 回复一条问题：输入框上方显示回复谁的问题，发出的文字作为这个问题的回答（不是普通对话消息）。
export interface QuestionReply {
  message: Schemas['InboxMessage']
  // 提问的一方，显示在回复提示里。
  name: string
  maxBytes?: number
  onCancel: () => void
  onDone: () => void
}

interface ComposerProps {
  ref?: Ref<ComposerHandle>
  agentId: string
  // 忙时输入框的占位文字里用到。
  agentName: string
  busy: boolean
  disabled: boolean
  // 会话文件丢失时仍允许无图片的 /resume，其余输入继续受 disabled 门禁约束。
  allowResumeWhenDisabled?: boolean
  imageLimits?: ImageLimits | null
  maxMessageBytes: number | undefined
  // 发送成功后把结果交给对话页，历史请求进行中时也能显示这条消息。
  onSent: (result: Schemas['SendMessageResult']) => void
  // 以 / 开头的消息被插件认领时：插件的回复与原来的命令，由对话页显示成一行，不算发出的消息。
  onCommand?: (result: Schemas['ConsoleCommandResult'], command: string) => void
  // 贴在输入框顶上的排队消息，由对话页给出。
  queue?: ReactNode
  // 输入区下方的一行：左边是忙碌提示（接着是按键说明），右边是实时连接状态，都由对话页给出。
  status?: ReactNode
  connection?: ReactNode
  // 有进行中的回复时由对话页给出。
  stop?: StopControl
  reply?: QuestionReply
  // 是否支持 Pi 专属命令；Claude Code 只展示通用平台命令。
  piCommands?: boolean
}

const encoder = new TextEncoder()
const platformCommands = [
  { name: 'session', usage: '/session' },
  { name: 'new', usage: '/new' },
  { name: 'compact', usage: '/compact [instructions]' },
  { name: 'reload', usage: '/reload' },
  { name: 'copy', usage: '/copy' },
  { name: 'export', usage: '/export [html|jsonl]' },
  { name: 'resume', usage: '/resume [sessionId]' },
] as const
const platformCommandNames: Set<string> = new Set(platformCommands.map(command => command.name))

function isPlatformCommand(text: string): boolean {
  const trimmed = text.trim()
  if (!trimmed.startsWith('/')) return false
  return platformCommandNames.has(trimmed.slice(1).split(/\s/, 1)[0])
}

// 边框归外面的输入框容器所有，文字区按内容增高，上限后独立滚动。
const inputClass =
  'field-sizing-fixed min-h-10 max-h-60 w-full flex-none resize-none overflow-y-auto rounded-none border-0 bg-transparent px-0 py-2 text-[15.5px] leading-6 shadow-none focus-visible:shadow-none disabled:bg-transparent'

export function Composer({ ref, agentId, agentName, busy, disabled, allowResumeWhenDisabled = false, maxMessageBytes, imageLimits, onSent, onCommand, queue, status, connection, stop, reply, piCommands = true }: ComposerProps) {
  const client = useQueryClient()
  const send = useSendMessage(agentId)
  const [text, setText] = useState('')
  const [images, setImages] = useState<ImageDraft[]>([]), [submitting, setSubmitting] = useState(false), [uploads, setUploads] = useState<ImageUploads>({}), [imageError, setImageError] = useState<unknown>(null)
  const busyRequest = useRef(false)
  const editorRef = useRef<HTMLTextAreaElement>(null)
  const pending = send.isPending || submitting
  const [localError, setLocalError] = useState<string | null>(null)
  const [confirmNew, setConfirmNew] = useState(false)
  const [confirmResume, setConfirmResume] = useState(false)
  // 只有从列表发起的恢复保存原草稿；手动输入命令仍保持原确认行为。
  const [resumeDraft, setResumeDraft] = useState<string | null>(null)
  const answer = useAnswerQuestion()
  const replyId = reply?.message.id
  const resumeAllowed = disabled && allowResumeWhenDisabled && !reply && (images.length === 0 || confirmResume) && /^\/resume(?:\s+\S+)?$/.test(text.trim())
  const inputDisabled = disabled && !allowResumeWhenDisabled
  const submitDisabled = disabled && !resumeAllowed

  // "/" 菜单：输入以 "/" 开头、光标还在第一个词里时打开，按这个词筛选命令；Esc 关掉后，这个词变了再打开。
  // 回复问题时不显示菜单；Pi 列表按运行时加载，平台命令始终可用。
  const [caret, setCaret] = useState(0)
  const [dismissed, setDismissed] = useState<string | null>(null)
  const [active, setActive] = useState(0)
  const token = reply || inputDisabled ? null : slashToken(text, caret)
  const menuRequested = token !== null && dismissed !== token
  if (token === null && dismissed !== null) setDismissed(null)
  const commands = useAgentCommands(agentId, menuRequested && !disabled && piCommands)
  const builtins: Command[] = images.length === 0
    ? platformCommands.filter(command => (!disabled || command.name === 'resume') && (piCommands || !['compact', 'export', 'resume'].includes(command.name))).map(command => ({
      ...command, source: 'platform', piSource: '', description: t.chat.commands.hints[command.name], pluginId: null, pluginDisplayName: null,
    }))
    : []
  const matched = matchCommands([...builtins, ...(!disabled && piCommands ? commands.data?.commands ?? [] : [])], token ?? '/')
  const menuOpen = menuRequested && (matched.length > 0 || !disabled && piCommands && images.length === 0)
  const menuStatus = matched.length > 0 ? null : !piCommands || disabled ? t.chat.commands.noMatch : commands.error
    ? t.chat.commands.failed(describeError(commands.error))
    : !commands.data
      ? t.chat.commands.loading
      : commands.data.capturedAt === null
        ? t.chat.commands.notCaptured
        : commands.data.commands.length === 0
          ? t.chat.commands.none
          : matched.length === 0
            ? t.chat.commands.noMatch
            : null
  const menuItems = menuOpen && menuStatus === null ? matched : []
  const [activeFor, setActiveFor] = useState<string | null>(null)
  if (token !== activeFor) { setActiveFor(token); setActive(0) }
  const listId = useId()
  const optionId = (index: number) => `${listId}-${index}`
  const pendingCaret = useRef<number | null>(null)
  const choose = (command: Command) => {
    const next = command.source === 'platform'
      ? { text: `/${command.name}${text.match(/\s[\s\S]*$/)?.[0] ?? ''}`, caret: command.name.length + 1 }
      : applyCommand(text, command)
    if (command.source === 'platform') setDismissed(`/${command.name}`)
    pendingCaret.current = next.caret
    setText(next.text)
    setCaret(next.caret)
  }
  const moveActive = (next: number) => {
    setActive(next)
    document.getElementById(optionId(next))?.scrollIntoView?.({ block: 'nearest' })
  }
  // 进入回复问题的模式时把光标放进输入框。
  useEffect(() => {
    if (replyId) editorRef.current?.focus()
  }, [replyId])

  useImperativeHandle(ref, () => ({
    refill: (value, drafts) => {
      setText((current) => (current.trim() === '' ? value : `${current}\n${value}`))
      // 放回的文字光标在末尾，不当作正在输入命令。
      setCaret(Number.MAX_SAFE_INTEGER)
      setImages((current) => [...current, ...drafts])
      editorRef.current?.focus()
    },
    resume: (sessionId) => {
      if (replyId || busyRequest.current) return
      setResumeDraft(text)
      setText(`/resume ${sessionId}`)
      setDismissed(`/resume ${sessionId}`)
      setConfirmResume(true)
    },
  }), [replyId, text])

  // 补全命令后把光标放到命令后面的空白之后。
  useLayoutEffect(() => {
    const at = pendingCaret.current
    if (at === null || !editorRef.current) return
    pendingCaret.current = null
    editorRef.current.setSelectionRange(at, at)
  }, [text])

  useLayoutEffect(() => {
    const editor = editorRef.current
    if (!editor) return
    const resize = () => {
      editor.style.height = 'auto'
      editor.style.height = `${Math.min(editor.scrollHeight, 240)}px`
    }
    resize()
    if (typeof ResizeObserver === 'undefined') return
    let width = editor.getBoundingClientRect().width
    const observer = new ResizeObserver(() => {
      const next = editor.getBoundingClientRect().width
      if (next !== width) { width = next; resize() }
    })
    observer.observe(editor)
    return () => observer.disconnect()
  }, [text])

  // 忙时 Enter 排队，⌘／Ctrl+Enter 作为打断发送；点发送按钮为排队。平台命令不自动排队重试。
  const submit = async (whenBusy: Schemas['WhenBusy'] = 'queue', confirmed = false) => {
    if (submitDisabled || busyRequest.current) return
    setLocalError(null); setImageError(null); setUploads({})
    send.reset()
    if (text.trim() === '' && images.length === 0) {
      setLocalError(t.chat.emptyMessage)
      return
    }
    if (reply) {
      await answerQuestion(reply, whenBusy)
      return
    }
    // 从列表确认的恢复始终是平台命令；已有图片留在输入框，不上传也不随命令发送。
    const confirmedResume = confirmed && confirmResume && /^\/resume\s+\S/.test(text.trim())
    const platform = confirmedResume || (images.length === 0 && isPlatformCommand(text))
    if (!confirmed && platform && /^\/new(?:\s|$)/.test(text.trim())) {
      setConfirmNew(true)
      return
    }
    if (!confirmed && platform && /^\/resume\s+\S/.test(text.trim())) {
      setConfirmResume(true)
      return
    }
    if (maxMessageBytes !== undefined && encoder.encode(text).length > maxMessageBytes) {
      setLocalError(t.chat.tooLong(maxMessageBytes))
      return
    }
    if (!confirmedResume && images.length && imageLimits) { const problem = imageProblem([], images.map(image => image.file), imageLimits); if (problem) { setLocalError(problem); return } }
    const sent = text, sentImages = confirmedResume ? [] : [...images]
    busyRequest.current = true; setSubmitting(true)
    try {
      const refs = await uploadMessageImages({ kind: 'agent', agentId }, sentImages, setUploads)
      const input = { text: sent, ...(refs.length ? { imageIds: refs.map(ref => ref.id) } : {}) }
      // 普通消息在空闲判断过期后按所选方式重发；平台命令直接报告忙碌，不转成排队消息。
      const result = platform
        ? await send.mutateAsync(input)
        : await send.mutateAsync(busy ? { ...input, whenBusy } : input).catch((error: unknown) => {
          if (busy || !(error instanceof ApiError && error.code === 'agent_busy')) throw error
          return send.mutateAsync({ ...input, whenBusy })
        })
      if (isCommandResult(result)) {
        if (result.source === 'platform' && result.copyText != null) await navigator.clipboard.writeText(result.copyText)
        if (result.source === 'platform' && result.exportFormat) await downloadAgentSession(agentId, result.exportFormat)
        onCommand?.(result, sent)
      } else {
        upsertMessage(client, agentId, result.message)
        onSent(result)
      }
      if (confirmedResume && resumeDraft !== null) {
        setText(resumeDraft)
        setResumeDraft(null)
      } else {
        // 去掉已发出的部分，只留下发送期间新输入的文字；已发出的部分在发送期间被改动过时全部保留。
        setText((current) => (current.startsWith(sent) ? current.slice(sent.length) : current))
      }
      setImages(current => current.filter(image => !sentImages.some(sent => sent.id === image.id)))
    } catch (error) {
      setImageError(error)
    } finally { busyRequest.current = false; setSubmitting(false) }
  }

  // 回复问题：只发文字，Agent 正忙时同样按排队或打断发送。是否记为答案以响应为准，记为普通留言时在下方说明。
  const answerQuestion = async (target: QuestionReply, whenBusy: Schemas['WhenBusy']) => {
    if (images.length) { setLocalError(t.consoleInbox.replyNoImages); return }
    if (target.maxBytes !== undefined && encoder.encode(text).length > target.maxBytes) { setLocalError(t.chat.tooLong(target.maxBytes)); return }
    const sent = text
    busyRequest.current = true; setSubmitting(true)
    try {
      await answer.submit(target.message, { text: sent, whenBusy: busy ? whenBusy : 'queue' })
      setText((current) => (current.startsWith(sent) ? current.slice(sent.length) : current))
      target.onDone()
    } catch (error) {
      setImageError(error)
    } finally { busyRequest.current = false; setSubmitting(false) }
  }

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    // 输入法组字时的按键用于上屏，不补全、不发送。
    if (event.nativeEvent.isComposing || event.keyCode === 229) return
    if (menuOpen) {
      if (event.key === 'Escape') {
        event.preventDefault()
        setDismissed(token)
        return
      }
      // 菜单里有命令时，上下键在命令间移动，Enter 或 Tab 补全选中的命令（不发送）；没有命令时照常发送。
      if (menuItems.length > 0) {
        if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
          event.preventDefault()
          moveActive(event.key === 'ArrowDown' ? Math.min(active + 1, menuItems.length - 1) : Math.max(active - 1, 0))
          return
        }
        if ((event.key === 'Enter' && !event.shiftKey && !event.metaKey && !event.ctrlKey) || (event.key === 'Tab' && !event.shiftKey)) {
          event.preventDefault()
          const command = menuItems[active] ?? menuItems[0]
          if (command) choose(command)
          return
        }
      }
    }
    if (event.key !== 'Enter' || event.shiftKey) return
    event.preventDefault()
    if (submitDisabled || pending) return
    void submit(event.metaKey || event.ctrlKey ? 'interrupt' : 'queue')
  }

  const sendLabel = busy ? t.chat.queueSend : t.chat.send
  const showStop = busy && stop !== undefined && !reply && text.trim() === '' && images.length === 0
  const stopLabel = stop?.pending ? t.chat.stopping : stop?.retry ? t.chat.retryStop : t.chat.stop
  const editor = <Textarea ref={editorRef} aria-label={t.chat.inputLabel} placeholder={reply ? t.consoleInbox.replyPlaceholder : busy ? t.chat.busyPlaceholder(agentName) : t.chat.inputPlaceholder} value={text} disabled={inputDisabled}
    onChange={event => { setText(event.target.value); setCaret(event.target.selectionStart) }} onSelect={event => setCaret(event.currentTarget.selectionStart)} onKeyDown={onKeyDown} rows={1} className={inputClass}
    aria-autocomplete="list" aria-haspopup="listbox" aria-expanded={menuItems.length > 0} aria-controls={menuItems.length > 0 ? listId : undefined} aria-activedescendant={menuItems.length > 0 ? optionId(active) : undefined} />
  // 一行三样：左边加图片的圆按钮，中间输入框，右边发送的荧光粉圆章（忙且输入框为空时换成藏青的停止按钮）。
  // 输入框是浅纸底、藏青描边，单行时是一颗胶囊；回复条与待发送图片排在同一描边里、文字行之上。
  const input = (addButton?: ReactNode, strip?: ReactNode) => <SlashCommandMenu open={menuOpen} listId={listId} optionId={optionId} items={menuItems} active={active} status={menuStatus} onActive={setActive} onChoose={choose} onDismiss={() => setDismissed(token)}><div data-chat-composer className="flex min-w-0 items-end gap-2.5">
    {addButton && <span className="flex h-11 shrink-0 items-center">{addButton}</span>}
    <div data-chat-input className="flex min-h-11 min-w-0 flex-1 flex-col rounded-[22px] border-2 border-ink bg-paper-raised px-4 focus-within:border-riso-blue focus-within:ring-2 focus-within:ring-riso-blue/25">
      {reply && <ReplyChip name={reply.name} preview={reply.message.preview} onCancel={reply.onCancel} />}
      {strip}
      {editor}
    </div>
    {showStop ? (
      <Button type="button" size="icon" className="mr-[3px] size-11 border-2 border-ink bg-ink text-white shadow-none hover:bg-ink-700" aria-label={stopLabel} title={stopLabel} disabled={stop.pending} onClick={stop.onStop}><StopMark /></Button>
    ) : (
      <Button type="button" variant="interrupt" size="icon" className="mr-[3px] size-11 border-2 border-ink" aria-label={sendLabel} title={busy ? t.chat.queueSendTitle : sendLabel} disabled={submitDisabled || pending} onClick={() => void submit()}><ArrowUpIcon aria-hidden="true" className="size-5" strokeWidth={2.8} /></Button>
    )}
  </div></SlashCommandMenu>
  return (
    // 输入区是纸张底部的一条浅黄色栏，上缘一道藏青线。
    <div data-chat-bar className="flex shrink-0 flex-col gap-2.5 border-t-2 border-ink bg-tint-yellow px-[clamp(12px,2vw,22px)] pt-3.5 pb-3">
      {localError && (
        <p role="alert" className="text-[13px] font-black text-red-text">
          {localError}
        </p>
      )}
      <ErrorAlert error={imageError ?? (send.error?.code === 'agent_busy' ? null : send.error)} />
      {answer.outcome === 'ordinary' && <p role="status" className="text-[13px] font-bold">{t.inbox.question.ordinary}</p>}
      {/* 排队托盘与输入框之间没有间距，连成一体。 */}
      <div className="flex min-w-0 flex-col">
        {queue}
        {imageLimits ? <ImagePicker images={images} onChange={setImages} limits={imageLimits} disabled={disabled || reply !== undefined} uploads={uploads} addClassName="size-10 rounded-full border-2 border-ink bg-paper-raised text-ink hover:bg-white">{input}</ImagePicker> : input()}
      </div>
      {/* 下面一行：左边是忙碌提示与按键说明（空闲时是发送与换行，忙时是排队与打断），右边是实时连接状态。宽屏时与输入框左缘对齐。 */}
      <div className={cn('flex flex-wrap items-center justify-between gap-x-4 gap-y-1.5 pr-1 text-[12px]', imageLimits && 'nav:pl-[52px]')}>
        <div className="flex min-w-0 flex-wrap items-center gap-x-3.5 gap-y-1.5">
          {status}
          {busy ? <KeyHint stop={stop !== undefined} /> : <IdleHint />}
        </div>
        {connection}
      </div>
      <ConfirmDialog
        open={confirmNew}
        onOpenChange={setConfirmNew}
        title={t.chat.commands.newConfirmTitle}
        body={t.chat.commands.newConfirmBody}
        confirmLabel={t.chat.commands.newConfirmAction}
        destructive
        onConfirm={() => submit('queue', true)}
      />
      <ConfirmDialog
        open={confirmResume}
        onOpenChange={(open) => {
          setConfirmResume(open)
          if (!open && resumeDraft !== null) {
            setText(resumeDraft)
            setResumeDraft(null)
          }
        }}
        title={t.chat.commands.resumeConfirmTitle}
        body={t.chat.commands.resumeConfirmBody}
        confirmLabel={t.chat.commands.resumeConfirmAction}
        onConfirm={() => submit('queue', true)}
      />
    </div>
  )
}

const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
const kbdClass = 'rounded-stamp border-[1.5px] border-ink bg-paper-raised px-1.5 font-sans text-[11px] leading-[17px] font-black text-ink'

// 空闲时输入框下方的按键说明：回车发送，Shift + 回车换行。
function IdleHint() {
  return (
    <p className="flex flex-wrap items-center gap-x-1.5 text-xs text-ink-700">
      <kbd className={kbdClass}>{t.chat.idleHint.enter}</kbd>
      <span>{t.chat.idleHint.send}</span>
      <span aria-hidden="true">·</span>
      <kbd className={kbdClass}>{t.chat.idleHint.shiftEnter}</kbd>
      <span>{t.chat.idleHint.newline}</span>
    </p>
  )
}

// 输入框下方的按键说明：Enter 排队，⌘／Ctrl+Enter 立即打断；有进行中的回复时说明停止按钮。任务节点的输入框也用它。
export function KeyHint({ stop = false, className }: { stop?: boolean; className?: string }) {
  return (
    <p className={cn('flex flex-wrap items-center gap-x-1.5 text-xs text-ink-700', className)}>
      <kbd className={kbdClass}>{t.chat.keyHint.enter}</kbd>
      <span>{t.chat.keyHint.queue}</span>
      <span aria-hidden="true">·</span>
      <kbd className={kbdClass}>{t.chat.keyHint.modEnter(mac)}</kbd>
      <span>{t.chat.keyHint.interrupt}</span>
      {stop && (
        <>
          <span aria-hidden="true">·</span>
          <span>
            <span aria-hidden="true">{t.chat.keyHint.stopMark} </span>
            {t.chat.keyHint.stop}
          </span>
        </>
      )}
    </p>
  )
}

// 停止按钮上的实心方块。
export function StopMark({ className = 'size-3.5' }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className={className}>
      <rect x="5" y="5" width="14" height="14" rx="2.5" fill="currentColor" />
    </svg>
  )
}
