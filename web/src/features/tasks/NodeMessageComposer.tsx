import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ArrowUpIcon } from 'lucide-react'
import { api, ApiError, call, type Schemas } from '@/api/client'
import { taskKeys } from '@/api/tasks'
import { taskInboxKeys, readTaskInboxAction, validateTaskInboxReceipt } from '@/api/task-inbox'
import { nodeCapabilityKey } from '@/api/task-node-capability'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'
import { t } from '@/i18n'
import { ImagePicker } from '@/features/images/ImagePicker'
import { imageProblem, type ImageDraft, type ImageLimits, type ImageUploads } from '@/features/images/image-drafts'
import { uploadMessageImages } from '@/api/message-images'
import { KeyHint, StopMark, type QuestionReply, type StopControl } from '@/features/chat/Composer'
import { ReplyChip } from '@/features/console-inbox/ConsoleMessage'
import { useAnswerQuestion } from '@/features/console-inbox/useAnswerQuestion'
import type { NodeDraft } from './NodeRecordPanel'

const copy = t.taskInbox

// 边框归共同容器所有，文字区按内容增高，上限后独立滚动。
const inputClass = 'min-h-9 max-h-24 flex-1 resize-none overflow-y-auto rounded-none bg-transparent px-0.5 py-2 text-sm leading-5 shadow-none focus-visible:shadow-none disabled:bg-transparent'

type Props = { taskId: string; draft: NodeDraft; current: boolean; disabled: boolean; capability: Schemas['TaskNodeMessageCapability'] | undefined; text: string; onChange: (text: string) => void; imageLimits?: ImageLimits | null; onImages?: (images: ImageDraft[]) => void; onAccepted: (sent: string, draftImageIds?: string[]) => void; onAction: (action: NodeDraft['action']) => void; queue?: ReactNode; stop?: StopControl; reply?: QuestionReply }
// queue: the node's queue tray, attached to the top of the input.
// stop: given while the node has a reply in progress; with an empty draft the send button becomes stop.
// reply: answering a question the node asked you; the text goes out as that answer (an Inbox reply), not as a node message.
export function NodeMessageComposer({ taskId, draft, current, disabled, capability, text, onChange, onImages, imageLimits, onAccepted, onAction, queue, stop, reply }: Props) {
  const client = useQueryClient(), busy = useRef(false)
  const [localError, setLocalError] = useState(''), [reading, setReading] = useState(false), [preparing, setPreparing] = useState(false), [uploads, setUploads] = useState<ImageUploads>({})
  const canSend = current && !disabled && capability?.canSend === true && capability.taskId === taskId && capability.nodeRunId === draft.nodeRunId && capability.agentId === draft.agentId && capability.runtimeId === draft.runtimeId
  const send = useMutation({ retry: false, mutationFn: async ({ nodeRunId, body }: { nodeRunId: string; body: Schemas['TaskNodeInboxInput'] }) => validateTaskInboxReceipt(await call(api.POST('/tasks/{taskId}/nodes/{nodeRunId}/inbox-messages', { params: { path: { taskId, nodeRunId } }, body })), taskId, nodeRunId, body) })
  const error = draft.action?.error
  const unknown = draft.action?.state === 'unknown'
  const unconfirmedNotFound = unknown && error instanceof ApiError && error.status === 404
  const pending = preparing || send.isPending || reading || draft.action?.state === 'sending'
  const received = draft.action?.receipt
  const targetMismatch = capability?.canSend === true && (capability.taskId !== taskId || capability.nodeRunId !== draft.nodeRunId || capability.agentId !== draft.agentId || capability.runtimeId !== draft.runtimeId)
  const refresh = (nodeRunId: string) => { void client.invalidateQueries({ queryKey: taskInboxKeys.records(taskId, nodeRunId) }); void client.invalidateQueries({ queryKey: taskKeys.detail(taskId) }); void client.invalidateQueries({ queryKey: nodeCapabilityKey(taskId, nodeRunId) }) }
  // Enter queues; ⌘/Ctrl+Enter sends as an interrupt. A retry resends the original body unchanged.
  const submit = async (whenBusy: Schemas['WhenBusy'] = 'queue') => {
    if (!canSend || pending || busy.current) return
    setLocalError(''); setUploads({})
    if (!unknown && !text.trim() && !draft.images?.length) { setLocalError(t.chat.emptyMessage); return }
    if (!unknown && imageLimits) { const problem = imageProblem([], (draft.images ?? []).map(image => image.file), imageLimits); if (problem) { setLocalError(problem); return } }
    busy.current = true
    const images = [...(draft.images ?? [])], draftImageIds = unknown ? draft.action?.draftImageIds : images.map(image => image.id)
    const body: Schemas['TaskNodeInboxInput'] = unknown && draft.action ? draft.action.body : { actionId: crypto.randomUUID(), expectedAgentId: draft.agentId, expectedRuntimeId: draft.runtimeId, text, whenBusy, ...(images.length ? { imageIds: images.map(image => image.id) } : {}) }
    // Capture the callbacks and exact record before the request. A node switch
    // cannot redirect a late receipt into the newly selected record's draft.
    const nodeRunId = draft.nodeRunId, saveAction = onAction, accepted = onAccepted
    saveAction({ body, draftImageIds, state: 'sending' })
    let posted = false
    setPreparing(true)
    try {
      if (!unknown) await uploadMessageImages({ kind: 'task', taskId, nodeRunId }, images, setUploads)
      posted = true
      const receipt = await send.mutateAsync({ nodeRunId, body })
      saveAction({ body, draftImageIds, state: 'accepted', receipt }); accepted(body.text, draftImageIds)
    }
    catch (error) { saveAction({ body, draftImageIds, state: posted && error instanceof ApiError && (error.status === 0 || error.status >= 500) ? 'unknown' : 'rejected', error }) }
    finally { busy.current = false; setPreparing(false); refresh(nodeRunId) }
  }
  const recover = async () => {
    if (!draft.action || !unknown || pending || busy.current) return
    busy.current = true
    const body = draft.action.body, draftImageIds = draft.action.draftImageIds, nodeRunId = draft.nodeRunId, saveAction = onAction, accepted = onAccepted
    setReading(true); setLocalError('')
    try { const receipt = validateTaskInboxReceipt(await readTaskInboxAction(taskId, nodeRunId, body.actionId), taskId, nodeRunId, body); saveAction({ body, draftImageIds, state: 'accepted', receipt }); accepted(body.text, draftImageIds) }
    catch (error) { saveAction({ body, draftImageIds, state: 'unknown', error });  }
    finally { busy.current = false; setReading(false); refresh(nodeRunId) }
  }
  const answer = useAnswerQuestion(), [answering, setAnswering] = useState(false), latestText = useRef(text)
  // The draft lives in the panel; after an answer only the part typed during the request is kept.
  useEffect(() => { latestText.current = text }, [text])
  const answerQuestion = async (target: QuestionReply, whenBusy: Schemas['WhenBusy']) => {
    if (answering || busy.current) return
    setLocalError('')
    if (draft.images?.length) { setLocalError(t.consoleInbox.replyNoImages); return }
    if (!text.trim()) { setLocalError(t.chat.emptyMessage); return }
    if (target.maxBytes !== undefined && new TextEncoder().encode(text).length > target.maxBytes) { setLocalError(t.chat.tooLong(target.maxBytes)); return }
    const sent = text
    busy.current = true; setAnswering(true)
    try {
      await answer.submit(target.message, { text: sent, whenBusy })
      if (latestText.current.startsWith(sent)) onChange(latestText.current.slice(sent.length))
      target.onDone()
    } catch { /* shown below */ } finally { busy.current = false; setAnswering(false) }
  }
  const keyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing || event.keyCode === 229) return
    event.preventDefault()
    const whenBusy = event.metaKey || event.ctrlKey ? 'interrupt' : 'queue'
    if (reply) void answerQuestion(reply, whenBusy)
    else if (!unknown) void submit(whenBusy)
  }
  const editor = <Textarea aria-label={t.tasks.draft} placeholder={reply ? t.consoleInbox.replyPlaceholder : t.tasks.draftPlaceholder(draft.agentName)} value={text} readOnly={!current} onChange={event => onChange(event.target.value)} onKeyDown={keyDown} rows={1} className={inputClass} />
  const sendLabel = pending || answering ? t.tasks.sending : t.chat.send
  const showStop = current && stop !== undefined && !reply && !unknown && !text.trim() && !draft.images?.length
  const stopLabel = stop?.pending ? t.chat.stopping : stop?.retry ? t.chat.retryStop : t.chat.stop
  // Draft images sit inside the same border, above the text row; the corners tighten when they show.
  const field = (addButton?: ReactNode, strip?: ReactNode) => <div data-node-composer className={cn('flex min-h-[52px] min-w-0 flex-col border-2 border-ink/60 bg-white p-1.5 focus-within:border-riso-blue focus-within:ring-2 focus-within:ring-riso-blue/15', strip || reply ? 'rounded-[18px]' : 'rounded-[24px]')}>
    {reply && <ReplyChip name={reply.name} preview={reply.message.preview} onCancel={reply.onCancel} />}
    {strip}
    <div className="flex min-w-0 items-end gap-1.5">
      {addButton && <span className="flex h-9 shrink-0 items-center">{addButton}</span>}
      {editor}
      {showStop ? <Button type="button" size="icon" className="size-9 bg-ink text-white shadow-none hover:bg-ink-700" aria-label={stopLabel} title={stopLabel} disabled={stop.pending} onClick={stop.onStop}><StopMark className="size-[13px]" /></Button>
        : current && <Button type="button" size="icon" className="size-9 shadow-none" aria-label={sendLabel} title={sendLabel} disabled={reply ? answering : !canSend || pending || unknown} onClick={() => void (reply ? answerQuestion(reply, 'queue') : submit())}><ArrowUpIcon aria-hidden="true" className="size-[18px]" strokeWidth={2.4} /></Button>}
    </div>
  </div>
  return <div className="mt-2 grid gap-2">
    <div className="grid min-w-0">{queue}{imageLimits && onImages ? <ImagePicker images={draft.images ?? []} onChange={onImages} limits={imageLimits} disabled={!current || reply !== undefined} uploads={uploads} addClassName="size-9 [&>svg]:size-[18px]">{field}</ImagePicker> : field()}</div>
    {current && <KeyHint stop={stop !== undefined} className="text-[11.5px]" />}
    {localError && <p role="alert" className="text-sm text-red-text">{localError}</p>}<ErrorAlert error={answer.error} />{answer.outcome === 'ordinary' && <p role="status" className="text-sm font-bold">{t.inbox.question.ordinary}</p>}<ErrorAlert error={unconfirmedNotFound ? null : error} />{unconfirmedNotFound && <p role="status" className="text-sm">{copy.notFound}</p>}
    {targetMismatch && <p role="status" className="text-sm">{t.tasks.targetChanged}</p>}
    {unknown && <><p role="status" className="text-sm">{t.tasks.sendUnknown}</p><div className="flex flex-wrap gap-2"><Button size="sm" variant="secondary" disabled={pending} onClick={() => void recover()}>{reading ? copy.reading : copy.readOriginal}</Button>{current && <Button size="sm" disabled={!canSend || pending} onClick={() => void submit()}>{t.tasks.retryOriginalMessage}</Button>}</div></>}
    {received && <p role="status" className="text-sm">{copy.accepted}</p>}
  </div>
}
