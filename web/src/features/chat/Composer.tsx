import { useEffect, useImperativeHandle, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode, type Ref } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowUpIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ApiError, type Schemas } from '@/api/client'
import { isCommandResult, upsertMessage, useSendMessage } from '@/api/queries'
import { t } from '@/i18n'
import { ImagePicker } from '@/features/images/ImagePicker'
import { imageProblem, type ImageDraft, type ImageLimits, type ImageUploads } from '@/features/images/image-drafts'
import { uploadMessageImages } from '@/api/message-images'
import { cn } from '@/lib/utils'
import { ReplyChip } from '@/features/console-inbox/ConsoleMessage'
import { useAnswerQuestion } from '@/features/console-inbox/useAnswerQuestion'
import { consoleInboxText } from '@/i18n/console-inbox'

// 编辑撤回的排队消息时，把原文与图片放回输入框；输入框里已有的内容保留在前面。
export interface ComposerHandle {
  refill: (text: string, images: ImageDraft[]) => void
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
  imageLimits?: ImageLimits | null
  maxMessageBytes: number | undefined
  // 发送成功后把结果交给对话页，历史请求进行中时也能显示这条消息。
  onSent: (result: Schemas['SendMessageResult']) => void
  // 以 / 开头的消息被插件认领时：插件的回复与原来的命令，由对话页显示成一行，不算发出的消息。
  onCommand?: (result: Schemas['ConsoleCommandResult'], command: string) => void
  // 贴在输入框顶上的排队消息，由对话页给出。
  queue?: ReactNode
  // 输入区下方的一行：左边是忙碌提示，右边是实时连接状态，都由对话页给出；忙时中间是按键说明。
  status?: ReactNode
  connection?: ReactNode
  // 有进行中的回复时由对话页给出。
  stop?: StopControl
  reply?: QuestionReply
}

const encoder = new TextEncoder()

// 边框归共同容器所有，文字区按内容增高，上限后独立滚动。
const inputClass =
  'field-sizing-fixed min-h-10 max-h-60 flex-1 resize-none overflow-y-auto rounded-none bg-transparent px-1 py-2 text-base leading-6 shadow-none focus-visible:shadow-none disabled:bg-transparent'

export function Composer({ ref, agentId, agentName, busy, disabled, maxMessageBytes, imageLimits, onSent, onCommand, queue, status, connection, stop, reply }: ComposerProps) {
  const client = useQueryClient()
  const send = useSendMessage(agentId)
  const [text, setText] = useState('')
  const [images, setImages] = useState<ImageDraft[]>([]), [submitting, setSubmitting] = useState(false), [uploads, setUploads] = useState<ImageUploads>({}), [imageError, setImageError] = useState<unknown>(null)
  const busyRequest = useRef(false)
  const editorRef = useRef<HTMLTextAreaElement>(null)
  const pending = send.isPending || submitting
  const [localError, setLocalError] = useState<string | null>(null)
  const answer = useAnswerQuestion()
  const replyId = reply?.message.id
  // 进入回复问题的模式时把光标放进输入框。
  useEffect(() => {
    if (replyId) editorRef.current?.focus()
  }, [replyId])

  useImperativeHandle(ref, () => ({
    refill: (value, drafts) => {
      setText((current) => (current.trim() === '' ? value : `${current}\n${value}`))
      setImages((current) => [...current, ...drafts])
      editorRef.current?.focus()
    },
  }), [])

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

  // 忙时 Enter 排队，⌘／Ctrl+Enter 作为打断发送；点发送按钮为排队。
  const submit = async (whenBusy: Schemas['WhenBusy'] = 'queue') => {
    if (disabled || busyRequest.current) return
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
    if (maxMessageBytes !== undefined && encoder.encode(text).length > maxMessageBytes) {
      setLocalError(t.chat.tooLong(maxMessageBytes))
      return
    }
    if (images.length && imageLimits) { const problem = imageProblem([], images.map(image => image.file), imageLimits); if (problem) { setLocalError(problem); return } }
    const sent = text, sentImages = [...images]
    busyRequest.current = true; setSubmitting(true)
    try {
      const refs = await uploadMessageImages({ kind: 'agent', agentId }, sentImages, setUploads)
      const input = { text: sent, ...(refs.length ? { imageIds: refs.map(ref => ref.id) } : {}) }
      // Agent 正忙时按所选方式发送；以为空闲、发送时才发现正忙的，同样按所选方式再发一次。
      const result = await send.mutateAsync(busy ? { ...input, whenBusy } : input).catch((error: unknown) => {
        if (busy || !(error instanceof ApiError && error.code === 'agent_busy')) throw error
        return send.mutateAsync({ ...input, whenBusy })
      })
      if (isCommandResult(result)) onCommand?.(result, sent)
      else {
        upsertMessage(client, agentId, result.message)
        onSent(result)
      }
      // 去掉已发出的部分，只留下发送期间新输入的文字；已发出的部分在发送期间被改动过时全部保留。
      setText((current) => (current.startsWith(sent) ? current.slice(sent.length) : current))
      setImages(current => current.filter(image => !sentImages.some(sent => sent.id === image.id)))
    } catch (error) {
      setImageError(error)
    } finally { busyRequest.current = false; setSubmitting(false) }
  }

  // 回复问题：只发文字，Agent 正忙时同样按排队或打断发送。是否记为答案以响应为准，记为普通留言时在下方说明。
  const answerQuestion = async (target: QuestionReply, whenBusy: Schemas['WhenBusy']) => {
    if (images.length) { setLocalError(consoleInboxText.replyNoImages); return }
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
    if (event.key !== 'Enter' || event.shiftKey) return
    // 输入法组字时的回车用于上屏，不发送。
    if (event.nativeEvent.isComposing || event.keyCode === 229) return
    event.preventDefault()
    if (disabled || pending) return
    void submit(event.metaKey || event.ctrlKey ? 'interrupt' : 'queue')
  }

  const sendLabel = busy ? t.chat.queueSend : t.chat.send
  const showStop = busy && stop !== undefined && !reply && text.trim() === '' && images.length === 0
  const stopLabel = stop?.pending ? t.chat.stopping : stop?.retry ? t.chat.retryStop : t.chat.stop
  const editor = <Textarea ref={editorRef} aria-label={t.chat.inputLabel} placeholder={reply ? consoleInboxText.replyPlaceholder : busy ? t.chat.busyPlaceholder(agentName) : t.chat.inputPlaceholder} value={text} disabled={disabled} onChange={event => setText(event.target.value)} onKeyDown={onKeyDown} rows={1} className={inputClass} />
  // 待发送图片排在同一边框里、文字行之上；有图片时边框圆角收小。
  const input = (addButton?: ReactNode, strip?: ReactNode) => <div data-chat-composer className={cn('flex min-h-[60px] min-w-0 flex-col border-2 border-ink/60 bg-white p-2 focus-within:border-riso-blue focus-within:ring-2 focus-within:ring-riso-blue/15', strip || reply ? 'rounded-[24px]' : 'rounded-[32px]')}>
    {reply && <ReplyChip name={reply.name} preview={reply.message.preview} onCancel={reply.onCancel} />}
    {strip}
    <div className="flex min-w-0 items-end gap-2">
      {addButton && <span className="flex h-10 shrink-0 items-center">{addButton}</span>}
      {editor}
      {showStop ? (
        <Button type="button" size="icon" className="size-10 bg-ink text-white shadow-none hover:bg-ink-700" aria-label={stopLabel} title={stopLabel} disabled={stop.pending} onClick={stop.onStop}><StopMark /></Button>
      ) : (
        <Button type="button" size="icon" className="size-10 shadow-none" aria-label={sendLabel} title={busy ? t.chat.queueSendTitle : sendLabel} disabled={disabled || pending} onClick={() => void submit()}><ArrowUpIcon aria-hidden="true" className="size-5" strokeWidth={2.4} /></Button>
      )}
    </div>
  </div>
  return (
    <div className="shrink-0 pt-4 pb-6">
      <div className="mx-auto flex min-w-0 w-full max-w-[960px] flex-col gap-3">
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
          {imageLimits ? <ImagePicker images={images} onChange={setImages} limits={imageLimits} disabled={disabled || reply !== undefined} uploads={uploads} addClassName="size-10">{input}</ImagePicker> : input()}
        </div>
        {(status || connection || busy) && (
          <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1.5 sm:grid sm:grid-cols-[1fr_auto_1fr]">
            <div className="flex min-w-0 items-center">{status}</div>
            {busy && <KeyHint stop={stop !== undefined} className="sm:col-start-2" />}
            <div className="ml-auto flex items-center justify-end sm:col-start-3 sm:ml-0">{connection}</div>
          </div>
        )}
      </div>
    </div>
  )
}

const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
const kbdClass = 'rounded-stamp border border-ink/25 bg-white px-[5px] font-mono text-[11px] leading-4 font-medium text-ink-700'

// 输入框下方的按键说明：Enter 排队，⌘／Ctrl+Enter 立即打断；有进行中的回复时说明停止按钮。任务节点的输入框也用它。
export function KeyHint({ stop = false, className }: { stop?: boolean; className?: string }) {
  return (
    <p className={cn('flex flex-wrap items-center justify-center gap-x-1.5 text-xs text-ink-500', className)}>
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
