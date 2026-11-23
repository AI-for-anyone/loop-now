import { useEffect, useId, useImperativeHandle, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode, type Ref } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowUpIcon } from 'lucide-react'
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
import { barKbdClass, mac } from './composer-style'
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
  // When the session file is missing and /resume is available right now (per the server's command list), an image-free /resume is still
  // allowed; all other input stays behind the disabled gate.
  // When /resume isn't available (a Claude Code Agent), nothing is let through: don't leave a dead end where the only thing you can type
  // is a command that is bound to be rejected.
  allowResumeWhenDisabled?: boolean
  imageLimits?: ImageLimits | null
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
  reply?: QuestionReply
}

const encoder = new TextEncoder()
type CommandList = Schemas['AgentCommandList']

// Names of the built-in platform commands listed by the server; null while the list hasn't been fetched yet.
const platformNames = (list: CommandList | undefined) => (list ? new Set(list.commands.filter((command) => command.source === 'platform').map((command) => command.name)) : null)

// The border belongs to the outer input box container; the text area grows with its content and scrolls on its own past the maximum.
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
  // The list must also be read when the session is missing and /resume is allowed: the input box opens up only if /resume is available
  // right now.
  const gate = disabled && allowResumeWhenDisabled
  const token = reply || (disabled && !gate) ? null : slashToken(text, caret)
  const menuRequested = token !== null && dismissed !== token
  if (token === null && dismissed !== null) setDismissed(null)
  const commands = useAgentCommands(agentId, menuRequested || gate)
  // The runtime the command list states, as the runtime definitions describe it.
  const runtimes = useRuntimeLookup()
  const listRuntime = runtimes.get(commands.data?.runtime)
  const listError = commands.error ?? (commands.data && !listRuntime ? runtimes.error : null)
  const listed = commands.data?.commands ?? []
  // Don't lock the input box when the list can't be read: let /resume through anyway and let the server decide (after the session is
  // lost, /resume is the only way out that keeps the context).
  const resumeOpen = gate && (commands.isError || listed.some((command) => command.source === 'platform' && command.name === 'resume' && command.available))
  const resumeAllowed = resumeOpen && !reply && (images.length === 0 || confirmResume) && /^\/resume(?:\s+\S+)?$/.test(text.trim())
  const inputDisabled = disabled && !resumeOpen
  const submitDisabled = disabled && !resumeAllowed
  // "/" text with images is an ordinary message, so platform commands aren't suggested; when the session is missing, only /resume is
  // suggested.
  const pool = (images.length > 0 ? listed.filter((command) => command.source !== 'platform') : listed).filter((command) => !disabled || (command.source === 'platform' && command.name === 'resume'))
  const matched = matchCommands(pool, token ?? '/')
  // Commands that can't be used right now can't be chosen; they are grayed out at the bottom of the menu with the reason.
  const selectable = matched.filter((command) => command.available)
  const blocked = matched.filter((command) => !command.available)
  // When the session is missing, open the menu only if there is a /resume to suggest, or the list can't be read (the reason must be shown).
  const menuOpen = menuRequested && !inputDisabled && (matched.length > 0 || ((!disabled || commands.isError) && images.length === 0))
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
  }), [replyId, text])

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
        setText((current) => (current.startsWith(sent) ? current.slice(sent.length) : current))
      }
      setImages(current => current.filter(image => !sentImages.some(sent => sent.id === image.id)))
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
  const showStop = busy && stop !== undefined && !reply && text.trim() === '' && images.length === 0
  const stopLabel = stop?.pending ? t.chat.stopping : stop?.retry ? t.chat.retryStop : t.chat.stop
  const editor = <Textarea ref={editorRef} aria-label={t.chat.inputLabel} placeholder={reply ? t.consoleInbox.replyPlaceholder : busy ? t.chat.busyPlaceholder(agentName) : t.chat.inputPlaceholder(agentName)} value={text} disabled={inputDisabled}
    onChange={event => { setText(event.target.value); setCaret(event.target.selectionStart) }} onSelect={event => setCaret(event.currentTarget.selectionStart)} onKeyDown={onKeyDown} rows={1} className={inputClass}
    aria-autocomplete="list" aria-haspopup="listbox" aria-expanded={menuItems.length > 0} aria-controls={menuItems.length > 0 ? listId : undefined} aria-activedescendant={menuItems.length > 0 ? optionId(active) : undefined} />
  // Three things in a row: the round add-image button on the left, the input box in the middle, and the fluorescent pink round send
  // stamp on the right (it turns into a navy stop button when busy and the input box is empty).
  // The input box has a light paper fill and a navy border, and is a pill when it has one line; the reply bar and images waiting to be
  // sent sit inside the same border, above the text line.
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
    // The input area is a light yellow bar at the bottom of the sheet, with a navy line along its top edge. On wide screens the top and
    // bottom padding is tighter, leaving a few more lines for the conversation.
    <div data-chat-bar className="flex shrink-0 flex-col gap-2.5 border-t-2 border-ink bg-tint-yellow px-[clamp(12px,2vw,22px)] pt-3.5 pb-3 nav:gap-1.5 nav:pt-2.5 nav:pb-2">
      {localError && (
        <p role="alert" className="text-[13px] font-black text-red-text">
          {localError}
        </p>
      )}
      <ErrorAlert error={imageError ?? (send.error?.code === 'agent_busy' ? null : send.error)} />
      {answer.outcome === 'ordinary' && <p role="status" className="text-[13px] font-bold">{t.inbox.question.ordinary}</p>}
      {/* No gap between the queue tray and the input box, so they read as one piece: the tray sits on the top edge of the input box,
          leaving room on the left and right for the plus and send buttons (at phone width it takes the full row). */}
      <div className="flex min-w-0 flex-col">
        {queue && <div className={cn('flex min-w-0 flex-col sm:pr-[57px]', imageLimits && 'sm:pl-[50px]')}>{queue}</div>}
        {imageLimits ? <ImagePicker images={images} onChange={setImages} limits={imageLimits} disabled={disabled || reply !== undefined} uploads={uploads} addClassName="size-10 rounded-full border-2 border-ink bg-paper-raised text-ink hover:bg-white">{input}</ImagePicker> : input()}
      </div>
      {/* The line below: on the left the busy hint and key hints (send and new line when idle, queue and interrupt when busy), on the
          right the live connection status. On wide screens it lines up with the left edge of the input box. */}
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

const kbdClass = 'rounded-stamp border border-ink/25 bg-white px-[5px] font-mono text-[11px] leading-4 font-medium text-ink-700'
// Key hints below the input box when idle: Enter sends, Shift + Enter starts a new line.
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

// The solid square on the stop button.
export function StopMark({ className = 'size-3.5' }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className={className}>
      <rect x="5" y="5" width="14" height="14" rx="2.5" fill="currentColor" />
    </svg>
  )
}
