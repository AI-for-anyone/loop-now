import { useEffect, useId, useImperativeHandle, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode, type Ref } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowUpIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ApiError, type Schemas } from '@/api/client'
import { downloadAgentSession } from '@/api/session-export'
import { agentCommandsQuery, isCommandResult, keys, upsertMessage, useAgentCommands, useSendMessage } from '@/api/queries'
import { t } from '@/i18n'
import { ImagePicker } from '@/features/images/ImagePicker'
import { imageProblem, type ImageDraft, type ImageLimits, type ImageUploads } from '@/features/images/image-drafts'
import { uploadMessageImages } from '@/api/message-images'
import { cn } from '@/lib/utils'
import { ReplyChip } from '@/features/console-inbox/ConsoleMessage'
import { useAnswerQuestion } from '@/features/console-inbox/useAnswerQuestion'
import { copyText } from '@/lib/clipboard'
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
  // 会话文件丢失时，如果 /resume 此刻可用（以服务端的命令列表为准），仍允许无图片的 /resume，其余输入继续受 disabled 门禁约束。
  // /resume 不可用（Claude Code 的 Agent）时不放行：不留只能输入一条必被拒绝的命令的死路。
  allowResumeWhenDisabled?: boolean
  imageLimits?: ImageLimits | null
  maxMessageBytes: number | undefined
  // 发送成功后把结果交给对话页，历史请求进行中时也能显示这条消息。
  onSent: (result: Schemas['SendMessageResult']) => void
  // 以 / 开头的消息被平台或插件认领时：结果与原来的命令，由对话页显示成一行，不算发出的消息。
  // /copy 的内容没能写进剪贴板时带上 copyFailed，对话页在那一行给出手动复制的办法。
  onCommand?: (result: Schemas['ConsoleCommandResult'], command: string, outcome?: { copyFailed: boolean }) => void
  // 贴在输入框顶上的排队消息，由对话页给出。
  queue?: ReactNode
  // 输入区下方的一行：左边是忙碌提示（接着是按键说明），右边是实时连接状态，都由对话页给出。
  status?: ReactNode
  connection?: ReactNode
  // 有进行中的回复时由对话页给出。
  stop?: StopControl
  reply?: QuestionReply
}

const encoder = new TextEncoder()
type CommandList = Schemas['AgentCommandList']

// 服务端列出的平台内置命令名；列表还没取到时为 null。
const platformNames = (list: CommandList | undefined) => (list ? new Set(list.commands.filter((command) => command.source === 'platform').map((command) => command.name)) : null)

// 平台命令自己不带说明，按命令名显示控制台的说明；服务端新加、这里还不认识的命令没有说明。
const hints: Record<string, string | undefined> = t.chat.commands.hints

// 边框归外面的输入框容器所有，文字区按内容增高，上限后独立滚动。
const inputClass =
  'field-sizing-fixed min-h-10 max-h-60 w-full flex-none resize-none overflow-y-auto rounded-none border-0 bg-transparent px-0 py-2 text-[15.5px] leading-6 shadow-none focus-visible:shadow-none disabled:bg-transparent max-nav:text-base'

export function Composer({ ref, agentId, agentName, busy, disabled, allowResumeWhenDisabled = false, maxMessageBytes, imageLimits, onSent, onCommand, queue, status, connection, stop, reply }: ComposerProps) {
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
  // "/" 菜单：输入以 "/" 开头、光标还在第一个词里时打开，按这个词筛选命令；Esc 关掉后，这个词变了再打开。回复问题时不显示菜单。
  // 命令只认服务端的列表（GET /agents/{id}/commands）：先是平台内置命令，带此刻能不能用；然后是 Pi 命令。
  const [caret, setCaret] = useState(0)
  const [dismissed, setDismissed] = useState<string | null>(null)
  const [active, setActive] = useState(0)
  // 会话丢失、又允许 /resume 时也要读列表：/resume 此刻可用才放开输入框。
  const gate = disabled && allowResumeWhenDisabled
  const token = reply || (disabled && !gate) ? null : slashToken(text, caret)
  const menuRequested = token !== null && dismissed !== token
  if (token === null && dismissed !== null) setDismissed(null)
  const commands = useAgentCommands(agentId, menuRequested || gate)
  const listed = (commands.data?.commands ?? []).map((command) => (command.source === 'platform' && command.description === null ? { ...command, description: hints[command.name] ?? null } : command))
  // 读不到列表时不把输入框锁死：照旧放行 /resume，由服务端判断（/resume 是会话丢失后唯一不丢上下文的出路）。
  const resumeOpen = gate && (commands.isError || listed.some((command) => command.source === 'platform' && command.name === 'resume' && command.available))
  const resumeAllowed = resumeOpen && !reply && (images.length === 0 || confirmResume) && /^\/resume(?:\s+\S+)?$/.test(text.trim())
  const inputDisabled = disabled && !resumeOpen
  const submitDisabled = disabled && !resumeAllowed
  // 带图片的 "/" 文字是普通消息，不提示平台命令；会话丢失时只提示 /resume。
  const pool = (images.length > 0 ? listed.filter((command) => command.source !== 'platform') : listed).filter((command) => !disabled || (command.source === 'platform' && command.name === 'resume'))
  const matched = matchCommands(pool, token ?? '/')
  // 此刻不能用的命令不能选，置灰列在菜单下方并写明原因。
  const selectable = matched.filter((command) => command.available)
  const blocked = matched.filter((command) => !command.available)
  // 有命令标着“运行时不支持”，说明这个 Agent 没有 Pi（Claude Code）：它运行多少次也不会有 Pi 命令。
  const withoutPi = listed.some((command) => command.unavailableReason === 'runtime_unsupported')
  // 会话丢失时只在有 /resume 可提示、或读不到列表（要说明原因）时打开菜单。
  const menuOpen = menuRequested && !inputDisabled && (matched.length > 0 || ((!disabled || commands.isError) && images.length === 0))
  const menuStatus = matched.length > 0
    ? null
    : commands.error
      ? t.chat.commands.failed(describeError(commands.error))
      : !commands.data
        ? t.chat.commands.loading
        : commands.data.capturedAt === null && !withoutPi
          ? t.chat.commands.notCaptured
          : t.chat.commands.noMatch
  const menuItems = menuOpen && menuStatus === null ? selectable : []
  const menuBlocked = menuOpen && menuStatus === null ? blocked : []
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
      // 这不是在输入命令：光标算在末尾，命令菜单不打开（菜单只看第一个词，拿整句去关它是关不掉的）。
      setCaret(Number.MAX_SAFE_INTEGER)
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
    // 这条文字是不是平台内置命令，只认服务端列的命令名；列表还没取到时现取。
    const word = images.length === 0 ? /^\/(\S+)/.exec(text.trim())?.[1] : undefined
    let names = platformNames(commands.data)
    if (word !== undefined && names === null) {
      // 现取列表期间发送按钮不可点，表示正在处理；不重试，取不到就按下面的退路走。
      busyRequest.current = true; setSubmitting(true)
      try { names = platformNames(await client.ensureQueryData({ ...agentCommandsQuery(agentId), retry: false })) } catch { /* 取不到列表：见下 */ } finally { busyRequest.current = false; setSubmitting(false) }
    }
    const platform = confirmedResume || (word !== undefined && names?.has(word) === true)
    // 列表取不到时不知道哪些是平台命令，但 /new 与 /resume <id> 仍先确认：多问一句没有坏处，漏掉确认会直接重开或切换会话。
    const guarded = platform || (word !== undefined && names === null)
    if (!confirmed && guarded && /^\/new(?:\s|$)/.test(text.trim())) {
      setConfirmNew(true)
      return
    }
    if (!confirmed && guarded && /^\/resume\s+\S/.test(text.trim())) {
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
        // /copy：这时已经过了一次网络往返，Safari 里不再算在按键手势内，写剪贴板可能被拒绝；用 http 的局域网地址打开时
        // 根本没有 navigator.clipboard。写不进去不算命令失败：内容照常交给对话页，由那一行给出手动复制的办法。
        const copyFailed = result.source === 'platform' && result.copyText != null && !(await copyText(result.copyText))
        if (result.source === 'platform' && result.exportFormat) await downloadAgentSession(agentId, result.exportFormat)
        onCommand?.(result, sent, copyFailed ? { copyFailed } : undefined)
        // 平台命令可能改变会话状态（/new、/resume、/reload），命令此刻能不能用要重新读。
        void client.invalidateQueries({ queryKey: keys.agentCommands(agentId) })
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
  const editor = <Textarea ref={editorRef} aria-label={t.chat.inputLabel} placeholder={reply ? t.consoleInbox.replyPlaceholder : busy ? t.chat.busyPlaceholder(agentName) : t.chat.inputPlaceholder(agentName)} value={text} disabled={inputDisabled}
    onChange={event => { setText(event.target.value); setCaret(event.target.selectionStart) }} onSelect={event => setCaret(event.currentTarget.selectionStart)} onKeyDown={onKeyDown} rows={1} className={inputClass}
    aria-autocomplete="list" aria-haspopup="listbox" aria-expanded={menuItems.length > 0} aria-controls={menuItems.length > 0 ? listId : undefined} aria-activedescendant={menuItems.length > 0 ? optionId(active) : undefined} />
  // 一行三样：左边加图片的圆按钮，中间输入框，右边发送的荧光粉圆章（忙且输入框为空时换成藏青的停止按钮）。
  // 输入框是浅纸底、藏青描边，单行时是一颗胶囊；回复条与待发送图片排在同一描边里、文字行之上。
  const input = (addButton?: ReactNode, strip?: ReactNode) => <SlashCommandMenu open={menuOpen} listId={listId} optionId={optionId} items={menuItems} blocked={menuBlocked} active={active} status={menuStatus} onActive={setActive} onChoose={choose} onDismiss={() => setDismissed(token)}><div data-chat-composer className="flex min-w-0 items-end gap-2.5">
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
      {/* 排队托盘与输入框之间没有间距，连成一体：托盘贴在输入框上沿，左右让出加号与发送按钮的位置（手机宽度下占满一行）。 */}
      <div className="flex min-w-0 flex-col">
        {queue && <div className={cn('flex min-w-0 flex-col sm:pr-[57px]', imageLimits && 'sm:pl-[50px]')}>{queue}</div>}
        {imageLimits ? <ImagePicker images={images} onChange={setImages} limits={imageLimits} disabled={disabled || reply !== undefined} uploads={uploads} addClassName="size-10 rounded-full border-2 border-ink bg-paper-raised text-ink hover:bg-white">{input}</ImagePicker> : input()}
      </div>
      {/* 下面一行：左边是忙碌提示与按键说明（空闲时是发送与换行，忙时是排队与打断），右边是实时连接状态。宽屏时与输入框左缘对齐。 */}
      <div className={cn('flex flex-wrap items-center justify-between gap-x-4 gap-y-1.5 pr-1 text-[12px]', imageLimits && 'nav:pl-[52px]')}>
        <div className="flex min-w-0 flex-wrap items-center gap-x-3.5 gap-y-1.5">
          {status}
          {busy ? <KeyHint stop={stop !== undefined} className="justify-start text-ink-700" kbdClassName={barKbdClass} keys={{ enter: t.chat.barKeys.enter, modEnter: t.chat.barKeys.modEnter(mac) }} /> : <IdleHint />}
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
const kbdClass = 'rounded-stamp border border-ink/25 bg-white px-[5px] font-mono text-[11px] leading-4 font-medium text-ink-700'
// 纸张底部输入区里的按键：浅纸底、藏青描边。
const barKbdClass = 'rounded-stamp border-[1.5px] border-ink bg-paper-raised px-1.5 font-sans text-[11px] leading-[17px] font-black text-ink'

// 空闲时输入框下方的按键说明：回车发送，Shift + 回车换行。
function IdleHint() {
  return (
    <p className="flex flex-wrap items-center gap-x-1.5 text-xs text-ink-700">
      <kbd className={barKbdClass}>{t.chat.barKeys.enter}</kbd>
      <span>{t.chat.idleHint.send}</span>
      <span aria-hidden="true">·</span>
      <kbd className={barKbdClass}>{t.chat.barKeys.shiftEnter}</kbd>
      <span>{t.chat.idleHint.newline}</span>
    </p>
  )
}

// 输入框下方的按键说明：Enter 排队，⌘／Ctrl+Enter 立即打断；有进行中的回复时说明停止按钮。任务节点的输入框也用它（样式与按键写法是默认值）；
// 对话页的输入区另给样式，并把按键写成这个界面语言的写法（中文是“回车”）。
export function KeyHint({
  stop = false,
  className,
  kbdClassName = kbdClass,
  keys = { enter: t.chat.keyHint.enter, modEnter: t.chat.keyHint.modEnter(mac) },
}: {
  stop?: boolean
  className?: string
  kbdClassName?: string
  keys?: { enter: string; modEnter: string }
}) {
  return (
    <p className={cn('flex flex-wrap items-center justify-center gap-x-1.5 text-xs text-ink-500', className)}>
      <kbd className={kbdClassName}>{keys.enter}</kbd>
      <span>{t.chat.keyHint.queue}</span>
      <span aria-hidden="true">·</span>
      <kbd className={kbdClassName}>{keys.modEnter}</kbd>
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
