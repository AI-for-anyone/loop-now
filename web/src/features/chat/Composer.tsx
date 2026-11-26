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
  // When the /copy content couldn't be written to the clipboard, copyFailed is passed along and the chat page offers a way to copy it
  // by hand on that line.
  onCommand?: (result: Schemas['ConsoleCommandResult'], command: string, outcome?: { copyFailed: boolean }) => void
  // Queued messages attached to the top of the input box, provided by the chat page.
  queue?: ReactNode
  // The line below the input area: the busy hint on the left (followed by the key hints) and the live connection status on the right,
  // both provided by the chat page.
  status?: ReactNode
  connection?: ReactNode
  // Provided by the chat page when a reply is in progress.
  stop?: StopControl
  // Who runs the Agent now (the runner chip), at the start of the line below the input box.
  runner?: ReactNode
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

export function Composer({ ref, agentId, agentName, busy, disabled, maxMessageBytes, imageLimits, imagesRefused = false, onSent, onCommand, queue, status, connection, stop, reply, runner, notice, stopAlso = 0 }: ComposerProps) {
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
  const setText = useCallback((next: string | ((current: string) => string)) => {
    setDraftState((prev) => {
      const value = typeof next === 'function' ? next(prev.text) : next
      setChatDraftText(prev.agentId, value)
      return value === prev.text ? prev : { ...prev, text: value }
    })
  }, [])
  const setImages = useCallback((next: ImageDraft[] | ((current: ImageDraft[]) => ImageDraft[])) => {
    setDraftState((prev) => {
      const value = typeof next === 'function' ? next(prev.images) : next
      setChatDraftImages(prev.agentId, value)
      return value === prev.images ? prev : { ...prev, images: value }
    })
  }, [])
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
  // Commands come only from the server's list (GET /agents/{id}/commands): first the built-in platform commands, each with whether it
  // can be used right now; then the Pi commands.
  const [caret, setCaret] = useState(0)
  const [dismissed, setDismissed] = useState<string | null>(null)
  const [active, setActive] = useState(0)
  const token = reply || disabled ? null : slashToken(text, caret)
  const menuRequested = token !== null && dismissed !== token
  if (token === null && dismissed !== null) setDismissed(null)
  const commands = useAgentCommands(agentId, menuRequested)
  // The runtime the command list states, as the runtime definitions describe it.
  const runtimes = useRuntimeLookup()
  const listRuntime = runtimes.get(commands.data?.runtime)
  const listError = commands.error ?? (commands.data && !listRuntime ? runtimes.error : null)
  const listed = commands.data?.commands ?? []
  const inputDisabled = disabled
  const submitDisabled = disabled
  // "/" text with images is an ordinary message, so platform commands aren't suggested.
  const pool = images.length > 0 ? listed.filter((command) => command.source !== 'platform') : listed
  const matched = matchCommands(pool, token ?? '/')
  // Commands that can't be used right now can't be chosen; they are grayed out at the bottom of the menu with the reason.
  const selectable = matched.filter((command) => command.available)
  const blocked = matched.filter((command) => !command.available)
  // When the session is missing, open the menu only if there is a /resume to suggest, or the list can't be read (the reason must be shown).
  const menuOpen = menuRequested && !inputDisabled && (matched.length > 0 || images.length === 0)
  const menuStatus = matched.length > 0
    ? null
    : listError
      ? t.chat.commands.failed(describeError(listError))
      : !commands.data || !listRuntime
        ? t.chat.commands.loading
        // The list states the runtime: an Agent whose runtime lists no commands of its own (Claude Code) has no Pi commands, no matter
        // how many times it runs; only when the runtime does and the Agent has never started do we say “运行一次后才有” (The command
        // list appears after the Agent has run once).
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
      // This isn't typing a command: the caret counts as being at the end, so the command menu doesn't open (the menu only looks at the
      // first word, so dismissing it with the whole sentence wouldn't close it).
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
    // A resume confirmed from the list is always a platform command; images already attached stay in the input box and are neither
    // uploaded nor sent with the command.
    const confirmedResume = confirmed && confirmResume && /^\/resume\s+\S/.test(text.trim())
    // Whether this text is a built-in platform command is decided only by the command names the server lists; if the list hasn't been
    // fetched yet, fetch it now.
    const word = images.length === 0 ? /^\/(\S+)/.exec(text.trim())?.[1] : undefined
    let names = platformNames(commands.data)
    if (word !== undefined && names === null) {
      // While the list is being fetched the send button can't be clicked, showing that work is in progress; no retry: if it can't be
      // fetched, take the fallback below.
      busyRequest.current = true; setSubmitting(true)
      try { names = platformNames(await client.ensureQueryData({ ...agentCommandsQuery(agentId), retry: false })) } catch { /* List unavailable: see below. */ } finally { busyRequest.current = false; setSubmitting(false) }
    }
    const platform = confirmedResume || (word !== undefined && names?.has(word) === true)
    // Without the list we don't know which ones are platform commands, but /new and /resume <id> still ask for confirmation first:
    // asking once more does no harm, while skipping the confirmation would restart or switch the session outright.
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
      // An ordinary message is resent the chosen way when the idle check turns out to be stale; a platform command just reports busy and
      // is not turned into a queued message.
      const result = platform
        ? await send.mutateAsync(input)
        : await send.mutateAsync(busy ? { ...input, whenBusy } : input).catch((error: unknown) => {
          if (busy || !(error instanceof ApiError && error.code === 'agent_busy')) throw error
          return send.mutateAsync({ ...input, whenBusy })
        })
      if (isCommandResult(result)) {
        // /copy: a network round trip has already happened by now, so Safari no longer counts this as part of the key gesture and may
        // refuse the clipboard write; when the console is opened at an http LAN address, there is no navigator.clipboard at all.
        // Failing to write doesn't count as the command failing: the content goes to the chat page as usual, and that line offers a way
        // to copy it by hand.
        const copyFailed = result.source === 'platform' && result.copyText != null && !(await copyText(result.copyText))
        if (result.source === 'platform' && result.exportFormat) await downloadAgentSession(agentId, result.exportFormat)
        onCommand?.(result, sent, copyFailed ? { copyFailed } : undefined)
        // When the command changed the session (/new, /resume), the server pushes session.updated and the command list is reread then,
        // so it isn't reread here.
      } else {
        upsertMessage(client, agentId, result.message)
        onSent(result)
      }
      if (confirmedResume && resumeDraft !== null) {
        setText(resumeDraft)
        setResumeDraft(null)
      } else {
        // Remove the part that was sent and keep only text typed during the send; if the sent part was changed during the send, keep
        // everything.
        setDraftState((prev) => {
          const nextText = prev.text.startsWith(sent) ? prev.text.slice(sent.length) : prev.text
          const nextImages = prev.images.filter((image) => !sentImages.some((sentImage) => sentImage.id === image.id))
          if (nextText === '' && nextImages.length === 0) {
            clearChatDraft(prev.agentId)
          } else {
            setChatDraftText(prev.agentId, nextText)
            setChatDraftImages(prev.agentId, nextImages)
          }
          return { ...prev, text: nextText, images: nextImages }
        })
      }
      if (confirmedResume) {
        setImages(current => current.filter(image => !sentImages.some(sent => sent.id === image.id)))
      }
    } catch (error) {
      setImageError(error)
    } finally { busyRequest.current = false; setSubmitting(false) }
  }

  // Answering a question: text only; when the Agent is busy it is likewise sent as queued or as an interrupt. Whether it is recorded as
  // the answer depends on the response; when it is saved as an ordinary note, a notice below says so.
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
    // Key presses during IME composition commit the composed text; they neither complete nor send.
    if (event.nativeEvent.isComposing || event.keyCode === 229) return
    if (menuOpen) {
      if (event.key === 'Escape') {
        event.preventDefault()
        setDismissed(token)
        return
      }
      // When the menu has commands, the up/down keys move between them and Enter or Tab completes the selected command (without
      // sending); when it has none, send as usual.
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
  // integrated bottom toolbar holds image attach + RunnerChip + busy status on the left, and degraded connection + Stop / Interrupt / Send on the right.
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
        className="flex min-w-0 flex-col rounded-2xl border border-hairline-strong bg-surface-elevated shadow-surface transition-colors focus-within:border-riso-blue/50 [[data-skin=plate]_&]:rounded-[22px] [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-none [[data-skin=plate]_&]:focus-within:border-riso-blue [[data-skin=plate]_&]:focus-within:ring-2 [[data-skin=plate]_&]:focus-within:ring-riso-blue/25"
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
              <span aria-hidden="true" className="mx-0.5 h-3.5 w-px shrink-0 bg-hairline-strong [[data-skin=plate]_&]:hidden" />
            )}
            {runner}
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
                className="h-7 shrink-0 gap-1 rounded-full border border-hairline-strong bg-surface-subtle px-2.5 text-[11.5px] font-medium text-ink shadow-none hover:border-ink/30 hover:bg-surface-hover [[data-skin=plate]_&]:h-9 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-tint-pink-2 [[data-skin=plate]_&]:text-[12px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:hover:bg-tint-pink"
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
                className="size-7 rounded-full border-0 bg-cta-bg text-cta-fg shadow-[0_1px_4px_rgb(0_102_219/0.30)] transition-[background-color,transform,box-shadow] hover:-translate-y-[0.5px] hover:bg-cta-hover active:scale-[0.94] disabled:translate-y-0 disabled:bg-ink/[0.08] disabled:text-ink/30 disabled:opacity-100 disabled:shadow-none [[data-skin=plate]_&]:size-9 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)] [[data-skin=plate]_&]:hover:translate-y-0 [[data-skin=plate]_&]:hover:bg-riso-pink [[data-skin=plate]_&]:disabled:opacity-40"
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
                className="size-7 rounded-full border-0 bg-red text-white shadow-[0_1px_4px_rgb(220_38_38/0.30)] transition-[background-color,transform,box-shadow] hover:-translate-y-[0.5px] hover:bg-red/90 active:scale-[0.94] [[data-skin=plate]_&]:size-9 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:text-white [[data-skin=plate]_&]:shadow-none [[data-skin=plate]_&]:hover:translate-y-0 [[data-skin=plate]_&]:hover:border-ink [[data-skin=plate]_&]:hover:bg-ink-700 [[data-skin=plate]_&]:hover:text-white"
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
    // Centered floating composer dock aligned with the 800px conversation stream; Plate skin keeps its yellow bar and 2px navy edge.
    <div
      data-chat-bar
      className="shrink-0 bg-surface-canvas px-4 pt-1 pb-3 sm:px-6 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-tint-yellow [[data-skin=plate]_&]:px-[clamp(12px,2vw,22px)] [[data-skin=plate]_&]:pt-3.5 [[data-skin=plate]_&]:pb-3"
    >
      <div className="mx-auto flex w-full max-w-[800px] min-w-0 flex-col gap-2">
        {localError && (
          <p role="alert" className="text-[13px] font-semibold text-red-text [[data-skin=plate]_&]:font-black">
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
              addClassName="size-7 rounded-lg border-0 bg-transparent text-ink-500 shadow-none hover:bg-surface-hover hover:text-ink [[data-skin=plate]_&]:size-8 [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:hover:bg-white"
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

const kbdClass = 'rounded-stamp border border-hairline-strong bg-surface-control px-[5px] font-mono text-[11px] leading-4 font-medium text-ink-700 [[data-skin=plate]_&]:border-ink/25 [[data-skin=plate]_&]:bg-white'

// Key hints below the input box: Enter queues, ⌘/Ctrl+Enter interrupts right away; when a reply is in progress, also explain the stop
// button. The task node input box uses it too (with the default style and key labels);
// the chat page's input area passes its own style and writes the keys the way the UI language does (in Chinese, “回车” (Enter)).
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
    <span role="status" data-stop-also className="inline-flex items-center gap-1.5 rounded-full border border-red-text/30 bg-tint-red px-2.5 text-[12px] leading-[22px] font-semibold text-red-text [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:shadow-[inset_0_0_0_1.5px_var(--red)]">
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
