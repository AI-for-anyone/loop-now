import { useCallback, useEffect, useId, useImperativeHandle, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode, type Ref } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowUpIcon, TriangleAlertIcon, ZapIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ApiError, type Schemas } from '@/api/client'
import { downloadAgentSession } from '@/api/session-export'
import { agentCommandsQuery, isCommandResult, upsertMessage, useAgentCommands, useSendMessage } from '@/api/queries'
import { t } from '@/i18n'
import { ImagePicker } from '@/features/images/ImagePicker'
import { imageProblem, type ImageDraft, type ImageLimits, type ImageUploads } from '@/features/images/image-drafts'
import { uploadMessageImages } from '@/api/message-images'
import { cn } from '@/lib/utils'
import { mac } from './composer-style'
import { clearChatDraft, getChatDraft, setChatDraftImages, setChatDraftText } from './draft-store'
import { ReplyChip } from '@/features/console-inbox/ConsoleMessage'
import { useAnswerQuestion } from '@/features/console-inbox/useAnswerQuestion'
import { copyText } from '@/lib/clipboard'
import { describeError } from '@/lib/errors'
import { useRuntimeLookup } from '@/lib/runtime'
import { applyCommand, matchCommands, slashToken, type Command } from './slash-commands'
import { SlashCommandMenu } from './SlashCommandMenu'

// When a queued message is withdrawn for editing, put its text and images back into the input box; whatever is already in the input
// box stays in front.
export interface ComposerHandle {
  refill: (text: string, images: ImageDraft[]) => void
  resume: (sessionId: string) => void
}

// Stop the current reply: when busy and the input box is empty, the send button turns into a stop button.
export interface StopControl {
  pending: boolean
  // The outcome of the last stop is unknown; clicking again reuses the original actionId.
  retry: boolean
  onStop: () => void
}

// Answering a question: above the input box it shows whose question is being answered, and the text sent becomes the answer to that
// question (not an ordinary conversation message).
export interface QuestionReply {
  message: Schemas['InboxMessage']
  // The party who asked, shown in the reply hint.
  name: string
  maxBytes?: number
  onCancel: () => void
  onDone: () => void
}

interface ComposerProps {
  ref?: Ref<ComposerHandle>
  agentId: string
  // Used in the input box placeholder while busy.
  agentName: string
  busy: boolean
  disabled: boolean
  imageLimits?: ImageLimits | null
  // The Agent's model takes no images (its input has no image): none can be added.
  imagesRefused?: boolean
  maxMessageBytes: number | undefined
  // After a successful send, hand the result to the chat page so the message shows even while a history request is in flight.
  onSent: (result: Schemas['SendMessageResult']) => void
  // When a message starting with / is claimed by the platform or a plugin: the result and the original command, which the chat page
  // shows as one line; it doesn't count as a sent message.
  onCommand?: (result: Schemas['ConsoleCommandResult'], command: string, outcome?: { copyFailed: boolean }) => void
  // Queued messages attached to the top of the input box, provided by the chat page.
  queue?: ReactNode
  // Busy status accessible region, provided by the chat page.
  status?: ReactNode
  // Degraded connection indicator, provided by the chat page.
  connection?: ReactNode
  // Provided by the chat page when a reply is in progress.
  stop?: StopControl
  // Who runs the Agent now (the runner chip), at the start of the composer toolbar.
  runner?: ReactNode
  // The running background tasks (a shell mark with the count), right after the runner chip.
  background?: ReactNode
  reply?: QuestionReply
  // One line above the input box that the next message needs to say first (the next message restarts the CLI and stops its background
  // tasks).
  notice?: ReactNode
  // How many background tasks stopping or interrupting this reply also stops (the runtime's stop ends its process); 0 when none.
  stopAlso?: number
}

const encoder = new TextEncoder()
type CommandList = Schemas['AgentCommandList']

// Names of the built-in platform commands listed by the server; null while the list hasn't been fetched yet.
const platformNames = (list: CommandList | undefined) => (list ? new Set(list.commands.filter((command) => command.source === 'platform').map((command) => command.name)) : null)

// The border belongs to the unified floating composer card; the text area grows with its content and scrolls past the maximum.
const inputClass =
  'field-sizing-fixed min-h-[42px] max-h-60 w-full flex-none resize-none overflow-y-auto rounded-none border-0 bg-transparent px-3.5 pt-2.5 pb-1 text-[14px] leading-6 shadow-none focus-visible:shadow-none disabled:bg-transparent max-nav:text-base'

export function Composer({ ref, agentId, agentName, busy, disabled, maxMessageBytes, imageLimits, imagesRefused = false, onSent, onCommand, queue, status, connection, stop, reply, runner, background, notice, stopAlso = 0 }: ComposerProps) {
  const client = useQueryClient()
  const send = useSendMessage(agentId)
  const [draftState, setDraftState] = useState(() => {
    const initial = getChatDraft(agentId)
    return { agentId, text: initial.text, images: initial.images }
  })
  if (draftState.agentId !== agentId) {
    const next = getChatDraft(agentId)
    setDraftState({ agentId, text: next.text, images: next.images })
  }
  const text = draftState.text
  const images = draftState.images
  const setText = useCallback(
    (next: string | ((current: string) => string)) => {
      const current = getChatDraft(agentId).text
      const value = typeof next === 'function' ? next(current) : next
      setChatDraftText(agentId, value)
      setDraftState((prev) => (prev.agentId === agentId && prev.text !== value ? { ...prev, text: value } : prev))
    },
    [agentId],
  )
  const setImages = useCallback(
    (next: ImageDraft[] | ((current: ImageDraft[]) => ImageDraft[])) => {
      const current = getChatDraft(agentId).images
      const value = typeof next === 'function' ? next(current) : next
      setChatDraftImages(agentId, value)
      setDraftState((prev) => (prev.agentId === agentId && prev.images !== value ? { ...prev, images: value } : prev))
    },
    [agentId],
  )
  const [submitting, setSubmitting] = useState(false), [uploads, setUploads] = useState<ImageUploads>({}), [imageError, setImageError] = useState<unknown>(null)
  const busyRequest = useRef(false)
  const editorRef = useRef<HTMLTextAreaElement>(null)
  const pending = send.isPending || submitting
  const [localError, setLocalError] = useState<string | null>(null)
  const [confirmNew, setConfirmNew] = useState(false)
  const [confirmResume, setConfirmResume] = useState(false)
  // Only a resume started from the list saves the original draft; a command typed by hand keeps the original confirmation behavior.
  const [resumeDraft, setResumeDraft] = useState<string | null>(null)
  const answer = useAnswerQuestion()
  const replyId = reply?.message.id
  // The "/" menu: opens when the input starts with "/" and the caret is still in the first word, and filters commands by that word;
  // after Esc closes it, it reopens once that word changes. The menu isn't shown while answering a question.
  const [caret, setCaret] = useState(0)
  const [dismissed, setDismissed] = useState<string | null>(null)
  const [active, setActive] = useState(0)
  const token = reply || disabled ? null : slashToken(text, caret)
  const menuRequested = token !== null && dismissed !== token
  if (token === null && dismissed !== null) setDismissed(null)
  const commands = useAgentCommands(agentId, menuRequested)
  const runtimes = useRuntimeLookup()
  const listRuntime = runtimes.get(commands.data?.runtime)
  const listError = commands.error ?? (commands.data && !listRuntime ? runtimes.error : null)
  const listed = commands.data?.commands ?? []
  const inputDisabled = disabled
  const submitDisabled = disabled
  // "/" text with images is an ordinary message, so platform commands aren't suggested.
  const pool = images.length > 0 ? listed.filter((command) => command.source !== 'platform') : listed
  const matched = matchCommands(pool, token ?? '/')
  const selectable = matched.filter((command) => command.available)
  const blocked = matched.filter((command) => !command.available)
  const menuOpen = menuRequested && !inputDisabled && (matched.length > 0 || images.length === 0)
  const menuStatus = matched.length > 0
    ? null
    : listError
      ? t.chat.commands.failed(describeError(listError))
      : !commands.data || !listRuntime
        ? t.chat.commands.loading
        : !listRuntime.capabilities.nativeCommands
          ? t.chat.commands.noPi(listRuntime.name)
          : commands.data.capturedAt === null
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
  // Put the cursor in the input box when entering question-reply mode.
  useEffect(() => {
    if (replyId) editorRef.current?.focus()
  }, [replyId])

  useImperativeHandle(ref, () => ({
    refill: (value, drafts) => {
      setText((current) => (current.trim() === '' ? value : `${current}\n${value}`))
      // For text put back, the caret is at the end, so it doesn't count as typing a command.
      setCaret(Number.MAX_SAFE_INTEGER)
      setImages((current) => [...current, ...drafts])
      editorRef.current?.focus()
    },
    resume: (sessionId) => {
      if (replyId || busyRequest.current) return
      setResumeDraft(text)
      setText(`/resume ${sessionId}`)
      setCaret(Number.MAX_SAFE_INTEGER)
      setConfirmResume(true)
    },
  }), [replyId, setImages, setText, text])

  // After completing a command, put the caret after the whitespace following the command.
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

  // While busy, Enter queues and ⌘/Ctrl+Enter sends as an interrupt; clicking the send button queues. Platform commands are not
  // automatically retried as queued messages.
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
    const sentAgentId = agentId
    // A resume confirmed from the list is always a platform command; images already attached stay in the input box and are neither
    // uploaded nor sent with the command.
    const confirmedResume = confirmed && confirmResume && /^\/resume\s+\S/.test(text.trim())
    const word = images.length === 0 ? /^\/(\S+)/.exec(text.trim())?.[1] : undefined
    let names = platformNames(commands.data)
    if (word !== undefined && names === null) {
      busyRequest.current = true; setSubmitting(true)
      try { names = platformNames(await client.ensureQueryData({ ...agentCommandsQuery(sentAgentId), retry: false })) } catch { /* List unavailable: see below. */ } finally { busyRequest.current = false; setSubmitting(false) }
    }
    const platform = confirmedResume || (word !== undefined && names?.has(word) === true)
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
      const refs = await uploadMessageImages({ kind: 'agent', agentId: sentAgentId }, sentImages, setUploads)
      const input = { text: sent, ...(refs.length ? { imageIds: refs.map(ref => ref.id) } : {}) }
      const result = platform
        ? await send.mutateAsync(input)
        : await send.mutateAsync(busy ? { ...input, whenBusy } : input).catch((error: unknown) => {
          if (busy || !(error instanceof ApiError && error.code === 'agent_busy')) throw error
          return send.mutateAsync({ ...input, whenBusy })
        })
      if (isCommandResult(result)) {
        const copyFailed = result.source === 'platform' && result.copyText != null && !(await copyText(result.copyText))
        if (result.source === 'platform' && result.exportFormat) await downloadAgentSession(sentAgentId, result.exportFormat)
        onCommand?.(result, sent, copyFailed ? { copyFailed } : undefined)
      } else {
        upsertMessage(client, sentAgentId, result.message)
        onSent(result)
      }
      // Update only the sending Agent's draft so switching Agents while a send is in flight never clears the new Agent's draft.
      const latest = getChatDraft(sentAgentId)
      const nextImages = latest.images.filter((image) => !sentImages.some((sentImage) => sentImage.id === image.id))
      if (confirmedResume && resumeDraft !== null) {
        setChatDraftText(sentAgentId, resumeDraft)
        setChatDraftImages(sentAgentId, nextImages)
        setDraftState((prev) => (prev.agentId === sentAgentId ? { ...prev, text: resumeDraft, images: nextImages } : prev))
        setResumeDraft(null)
      } else {
        const nextText = latest.text.startsWith(sent) ? latest.text.slice(sent.length) : latest.text
        if (nextText === '' && nextImages.length === 0) {
          clearChatDraft(sentAgentId)
        } else {
          setChatDraftText(sentAgentId, nextText)
          setChatDraftImages(sentAgentId, nextImages)
        }
        setDraftState((prev) => (prev.agentId === sentAgentId ? { ...prev, text: nextText, images: nextImages } : prev))
      }
    } catch (error) {
      setImageError(error)
    } finally { busyRequest.current = false; setSubmitting(false) }
  }

  // Answering a question: text only; when the Agent is busy it is likewise sent as queued or as an interrupt.
  const answerQuestion = async (target: QuestionReply, whenBusy: Schemas['WhenBusy']) => {
    if (images.length) { setLocalError(t.consoleInbox.replyNoImages); return }
    if (target.maxBytes !== undefined && encoder.encode(text).length > target.maxBytes) { setLocalError(t.chat.tooLong(target.maxBytes)); return }
    const sentAgentId = agentId
    const sent = text
    busyRequest.current = true; setSubmitting(true)
    try {
      await answer.submit(target.message, { text: sent, whenBusy: busy ? whenBusy : 'queue' })
      const latest = getChatDraft(sentAgentId)
      const nextText = latest.text.startsWith(sent) ? latest.text.slice(sent.length) : latest.text
      setChatDraftText(sentAgentId, nextText)
      setDraftState((prev) => (prev.agentId === sentAgentId ? { ...prev, text: nextText } : prev))
      target.onDone()
    } catch (error) {
      setImageError(error)
    } finally { busyRequest.current = false; setSubmitting(false) }
  }

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    // Key presses during IME composition commit the composed text; they neither complete nor send.
    if (event.nativeEvent.isComposing || event.keyCode === 229) return
    if (menuOpen) {
      if (event.key === 'Escape') {
        event.preventDefault()
        setDismissed(token)
        return
      }
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
  const hasDraft = text.trim() !== '' || images.length > 0
  const showStop = busy && stop !== undefined
  const showInterrupt = busy && !submitDisabled && hasDraft && !reply
  const showSend = !showStop || hasDraft || reply !== undefined
  const stopLabel = stop?.pending ? t.chat.stopping : stop?.retry ? t.chat.retryStop : stopAlso > 0 ? t.background.stopWithTasks(stopAlso) : t.chat.stop
  const editor = <Textarea ref={editorRef} aria-label={t.chat.inputLabel} placeholder={reply ? t.consoleInbox.replyPlaceholder : busy ? t.chat.busyPlaceholder(agentName) : t.chat.inputPlaceholder(agentName)} value={text} disabled={inputDisabled}
    onChange={event => { setText(event.target.value); setCaret(event.target.selectionStart) }} onSelect={event => setCaret(event.currentTarget.selectionStart)} onKeyDown={onKeyDown} rows={1} className={inputClass}
    aria-autocomplete="list" aria-haspopup="listbox" aria-expanded={menuItems.length > 0} aria-controls={menuItems.length > 0 ? listId : undefined} aria-activedescendant={menuItems.length > 0 ? optionId(active) : undefined} />
  // Unified floating composer card: upper region holds ReplyChip, attached image thumbnails and the borderless auto-sizing editor;
  // integrated bottom toolbar holds image attach + RunnerChip + BackgroundTasksMark + busy status on the left,
  // and degraded connection + Stop / Interrupt / Send on the right.
  const input = (addButton?: ReactNode, strip?: ReactNode) => (
    <SlashCommandMenu
      runtimeName={listRuntime?.name}
      open={menuOpen}
      listId={listId}
      optionId={optionId}
      items={menuItems}
      blocked={menuBlocked}
      active={active}
      status={menuStatus}
      onActive={setActive}
      onChoose={choose}
      onDismiss={() => setDismissed(token)}
    >
      <div
        data-chat-composer
        data-chat-input
        className="flex min-w-0 flex-col rounded-2xl border border-hairline-strong bg-surface-elevated shadow-surface transition-colors focus-within:border-riso-blue/50"
      >
        {(reply || strip) && (
          <div className="flex min-w-0 flex-col gap-1.5 px-3 pt-2.5">
            {reply && <ReplyChip name={reply.name} preview={reply.message.preview} onCancel={reply.onCancel} />}
            {strip}
          </div>
        )}
        {editor}
        <div
          data-chat-toolbar
          className="flex flex-wrap items-center justify-between gap-x-1.5 gap-y-1.5 px-2 pt-1 pb-2"
        >
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1">
            {addButton}
            {addButton && runner && (
              <span aria-hidden="true" className="mx-0.5 h-3.5 w-px shrink-0 bg-hairline-strong" />
            )}
            {runner}
            {background}
            {status}
            {/* Warn before stopping or interrupting when the runtime's stop also terminates running background tasks. */}
            {busy && stopAlso > 0 && <StopAlsoWarning count={stopAlso} />}
          </div>
          <div className="ml-auto flex shrink-0 items-center gap-1.5">
            {connection}
            {showInterrupt && (
              <Button
                type="button"
                variant="secondary"
                className="h-7 shrink-0 gap-1 rounded-full border border-hairline-strong bg-surface-subtle px-2.5 text-[11.5px] font-medium text-ink shadow-none hover:border-ink/30 hover:bg-surface-hover"
                aria-label={t.chat.interruptSend}
                title={t.chat.interruptSendTitle(mac)}
                disabled={submitDisabled || pending}
                onClick={() => void submit('interrupt')}
              >
                <ZapIcon aria-hidden="true" className="size-3 shrink-0 text-pink-text" strokeWidth={2.4} />
                <span className="hidden sm:inline">{t.chat.interruptAction}</span>
              </Button>
            )}
            {showSend && (
              <Button
                type="button"
                variant="interrupt"
                size="icon"
                className="size-7 rounded-full border-0 bg-cta-bg text-cta-fg shadow-[0_1px_4px_rgb(0_102_219/0.30)] transition-[background-color,transform,box-shadow] hover:-translate-y-[0.5px] hover:bg-cta-hover active:scale-[0.94] disabled:translate-y-0 disabled:bg-ink/[0.08] disabled:text-ink/30 disabled:opacity-100 disabled:shadow-none"
                aria-label={sendLabel}
                title={busy ? t.chat.queueSendTitle : `${sendLabel} (${t.chat.barKeys.enter})`}
                disabled={submitDisabled || pending}
                onClick={() => void submit()}
              >
                <ArrowUpIcon aria-hidden="true" className="size-3.5" strokeWidth={2.6} />
              </Button>
            )}
            {showStop && (
              <Button
                type="button"
                size="icon"
                className="size-7 rounded-full border-0 bg-red text-white shadow-[0_1px_4px_rgb(220_38_38/0.30)] transition-[background-color,transform,box-shadow] hover:-translate-y-[0.5px] hover:bg-red/90 active:scale-[0.94]"
                aria-label={stopLabel}
                title={stopLabel}
                disabled={stop.pending}
                onClick={stop.onStop}
              >
                <StopMark className="size-2.5" />
              </Button>
            )}
          </div>
        </div>
      </div>
    </SlashCommandMenu>
  )
  return (
    // Centered floating composer dock aligned with the 800px conversation stream.
    <div data-chat-bar className="shrink-0 bg-surface-canvas px-4 pt-1 pb-3 sm:px-6">
      <div data-chat-bar-inner className="mx-auto flex w-full max-w-[800px] min-w-0 flex-col gap-2">
        {localError && (
          <p role="alert" className="text-[13px] font-semibold text-red-text">
            {localError}
          </p>
        )}
        <ErrorAlert error={imageError ?? (send.error?.code === 'agent_busy' ? null : send.error)} />
        {answer.outcome === 'ordinary' && <p role="status" className="text-[13px] font-bold">{t.inbox.question.ordinary}</p>}
        {notice}
        {/* Queue tray docks cleanly on top of the composer card with no gap between them. */}
        <div className="flex min-w-0 flex-col">
          {queue && <div className="flex min-w-0 flex-col">{queue}</div>}
          {imageLimits ? (
            <ImagePicker
              images={images}
              onChange={setImages}
              limits={imageLimits}
              disabled={disabled || reply !== undefined}
              refused={imagesRefused ? t.images.modelRefuses : null}
              uploads={uploads}
              addClassName="size-7 rounded-lg border-0 bg-transparent text-ink-500 shadow-none hover:bg-surface-hover hover:text-ink"
            >
              {input}
            </ImagePicker>
          ) : (
            input()
          )}
        </div>
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

const kbdClass = 'rounded-stamp border border-hairline-strong bg-surface-control px-[5px] font-mono text-[11px] leading-4 font-medium text-ink-700'

// Key hints below the input box: Enter queues, ⌘/Ctrl+Enter interrupts right away; when a reply is in progress, also explain the stop
// button. The task node input box uses it too.
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

// Next to the key hints when stopping or interrupting the reply also stops background tasks (the runtime's stop ends its process, or a
// task node's turn takes its tasks with it).
export function StopAlsoWarning({ count }: { count: number }) {
  return (
    <span role="status" data-stop-also className="inline-flex items-center gap-1.5 rounded-full border border-red-text/30 bg-tint-red px-2.5 text-[12px] leading-[22px] font-semibold text-red-text">
      <TriangleAlertIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.4} />
      {t.background.stopWarning(count)}
    </span>
  )
}

// The solid square on the stop button.
export function StopMark({ className = 'size-3.5' }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className={className}>
      <rect x="5" y="5" width="14" height="14" rx="2.5" fill="currentColor" />
    </svg>
  )
}
