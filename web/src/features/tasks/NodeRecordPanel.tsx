import { useTaskInboxMessages } from '@/api/task-inbox'
import { TaskInboxMessageBubble } from './TaskInboxMessageBubble'
import { ConsoleMessage } from '@/features/console-inbox/ConsoleMessage'
import { TaskMessageBubble } from './TaskMessageBubble'
import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { CircleAlertIcon, DownloadIcon, FileTextIcon, ScissorsIcon } from 'lucide-react'
import { AgentAvatar } from '@/components/AgentAvatar'
import { CutLine } from '@/components/CutLine'
import { HollowNumber } from '@/components/NumberedSection'
import { Sheet } from '@/components/Sheet'
import { StickyNote } from '@/components/StickyNote'
import { indexTabClass, indexTabsClass } from '@/lib/index-tab'
import { cn } from '@/lib/utils'
import { stepNumber } from './workflow-steps'
import { NodeMessageComposer } from './NodeMessageComposer'
import { NodeQueueList } from './NodeQueueList'
import { TaskActions } from './TaskActions'
import { TaskLarkSwitchController } from './TaskLarkSwitchController'
import { TaskBadge } from './TaskBadge'
import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import type { ImageDraft } from '@/features/images/image-drafts'
import { DraftImagePreview } from '@/features/images/ImagePreview'
import { useContextInfo } from '@/api/queries'
import { messageImageLimits } from '@/api/message-images'
import { useTaskMessages, artifactUrl, taskKeys } from '@/api/tasks'
import { stopTaskNodeRun, useTaskNodeQueue } from '@/api/task-node-queue'
import { nodeConflictNotice, useStopReply } from '@/features/chat/queue-actions'
import { QueueMessages } from '@/features/chat/QueueList'
import { useQueryClient } from '@tanstack/react-query'
import { useNodeMessageCapability, nodeMessagesUnsupported } from '@/api/task-node-capability'
import { config } from '@/config'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Markdown } from '@/features/chat/Markdown'
import { t } from '@/i18n'
import { SleepUntil } from '@/features/sleep/SleepUntil'
import { stopAllTaskNodeBackgroundTasks, stopTaskNodeBackgroundTask, useTaskNodeBackgroundTasks } from '@/api/background-tasks'
import { BackgroundTasksMark, type BackgroundStopper } from '@/features/chat/BackgroundTasks'
import { useTaskNodePermissionRequests } from '@/api/permissions'
import { WaitingForApproval } from '@/features/permissions/WaitingForApproval'

const inboxCopy = t.taskInbox

// The node's open question: the question itself sits in the node conversation as a “发给你” (For you) message, where options are clicked
// or a reply is written; this only states its status and can jump to it.
export function PendingQuestion({ question }: { question: Schemas['TaskQuestionRef'] }) {
  const locate = () => document.querySelector(`[data-console-message-id="${question.messageId}"]`)?.scrollIntoView({ block: 'center' })
  return <section className="grid gap-3 rounded-card bg-tint-yellow p-4"><h3 className="font-bold">{t.tasks.question}</h3><Badge>{t.tasks.questionState[question.status]}</Badge><Markdown text={question.text} />
    {question.status === 'open' && question.canAnswer ? <Button className="justify-self-start" onClick={locate}>{t.consoleInbox.locateQuestion}</Button> : <p className="text-sm">{question.status === 'open' ? t.tasks.cannotAnswer : t.tasks.questionState[question.status]}</p>}
  </section>
}
type Node = Schemas['TaskNodeRun']
export type NodeDraft = { text: string; images?: ImageDraft[]; agentId: string; runtimeId: string; nodeRunId: string; agentName: string; action?: { body: Schemas['TaskNodeInboxInput']; draftImageIds?: string[]; state: 'sending' | 'unknown' | 'rejected' | 'accepted'; error?: unknown; receipt?: Schemas['TaskNodeInboxReceipt'] } }
// The task detail sheet (redesign step 5): two index tabs on the sheet's top edge (Conversation, Node materials); on the sheet, the content
// of this visit of the selected node on the left,
// and on the right the stub supplied by the caller (the workflow ruler, stub).
export function NodeRecordPanel({ detail, node, draft, onDraft, onImages, onAccepted, onAction, drafts, disabled, stub }: { detail: Schemas['ConsoleTaskDetail']; node: Node; draft: NodeDraft; onDraft: (text: string) => void; onImages?: (images: ImageDraft[]) => void; onAccepted: (text: string, draftImageIds?: string[]) => void; onAction: (action: NodeDraft['action']) => void; drafts: NodeDraft[]; disabled: boolean; stub?: ReactNode }) {
  const records = detail.nodes.filter(n => n.stepId === node.stepId).sort((a, b) => a.visit - b.visit || a.attempt - b.attempt || a.id.localeCompare(b.id))
  const [tab, setTab] = useState<'conversation' | 'materials'>('conversation')
  const [groups, setGroups] = useState(() => ({ open: new Set([node.id]), closed: new Set<string>() }))
  const conversation = useRef<HTMLDivElement>(null)
  const materials = useRef<HTMLDivElement>(null)
  const following = useRef(true)
  const reading = useRef<{ id: string; offset: number } | null>(null)
  const fingerprint = useRef('')
  const [newMessages, setNewMessages] = useState(false)
  const current = node.id === detail.task.currentNodeRunId && !['completed', 'cancelled'].includes(detail.task.status)
  const context = useContextInfo()
  const capability = useNodeMessageCapability(detail.task.id, node.id, current)
  const inbox = useTaskInboxMessages(detail.task.id, node.id, node.agentId, node.runtimeId)
  const unsupported = nodeMessagesUnsupported(capability.error) || nodeMessagesUnsupported(inbox.error)
  const client = useQueryClient()
  // The node's reply in progress, if any, can be stopped without sending a new message.
  const nodeMessages = useTaskMessages(detail.task.id, node.id)
  const activeRunId = nodeMessages.data?.retainedMessages.filter(m => m.nodeRunId === node.id && (m.status === 'starting' || m.status === 'running')).at(-1)?.runId
  const stop = useStopReply((runId, actionId) => stopTaskNodeRun(detail.task.id, node.id, runId, { actionId, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId }), error => nodeConflictNotice(error, 'stop'))
  const refreshMessages = () => void client.invalidateQueries({ queryKey: taskKeys.messages(detail.task.id, node.id) })
  // Background tasks the node's turns started: the turn waits for them, so the node shows them (with Stop) while they run, and its Stop
  // ends them with the turn.
  const background = useTaskNodeBackgroundTasks(detail.task.id, node.id, current)
  // Requests the node's turn waits on: cards in the replies that asked, and a line above the message box.
  const permission = useTaskNodePermissionRequests(detail.task.id, node.id, current)
  const permissionRequests = current && permission.data?.nodeRunId === node.id ? permission.data.requests : []
  const backgroundTasks = current && background.data?.nodeRunId === node.id ? background.data.tasks : []
  const target = { taskId: detail.task.id, nodeRunId: node.id, agentId: node.agentId, runtimeId: node.runtimeId }
  const backgroundStopper: BackgroundStopper = {
    stop: (taskId, actionId) => stopTaskNodeBackgroundTask(target, taskId, actionId),
    stopAll: (actionId) => stopAllTaskNodeBackgroundTasks(target, actionId),
  }
  // Editing a cancelled message puts its text and images back into the draft after what is already there.
  // Replying to a question this node asked you: the composer sends text as the answer until the question leaves 'open'.
  const [replying, setReplying] = useState<{ nodeRunId: string; messageId: string } | null>(null)
  const replyTarget = replying?.nodeRunId === node.id ? inbox.data?.items.find(item => item.message.id === replying.messageId && item.message.question?.state === 'open')?.message : undefined
  const refill = (text: string, images: ImageDraft[]) => { onDraft(draft.text.trim() ? `${draft.text}\n${text}` : text); if (images.length) onImages?.([...(draft.images ?? []), ...images]) }
  const statusReason = node.reason || node.question?.text || node.sleep?.reason
  const capture = () => {
    const el = conversation.current
    if (!el) return
    following.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    const top = el.getBoundingClientRect().top
    const anchor = [...el.querySelectorAll<HTMLElement>('[data-reading-anchor]')].find(row => row.getBoundingClientRect().bottom > top)
    reading.current = anchor ? { id: anchor.dataset.readingAnchor!, offset: anchor.getBoundingClientRect().top - top } : null
    if (following.current) setNewMessages(false)
  }
  useLayoutEffect(() => {
    const el = conversation.current
    if (!el) return
    const observer = new MutationObserver(() => {
      const next = [...el.querySelectorAll<HTMLElement>('[data-message-id]')].map(row => `${row.dataset.messageId}:${row.textContent}`).join('|')
      if (next === fingerprint.current) return
      const changed = fingerprint.current !== ''
      fingerprint.current = next
      if (following.current) el.scrollTop = el.scrollHeight
      else {
        const old = reading.current
        const anchor = old && [...el.querySelectorAll<HTMLElement>('[data-reading-anchor]')].find(row => row.dataset.readingAnchor === old.id)
        if (anchor && old) el.scrollTop += anchor.getBoundingClientRect().top - el.getBoundingClientRect().top - old.offset
        if (changed) setNewMessages(true)
      }
    })
    observer.observe(el, { childList: true, subtree: true, characterData: true })
    // Image previews grow the composer outside the message DOM. Keep a reader
    // at the bottom there; someone reading history retains the same scrollTop.
    const resize = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(() => { if (following.current) el.scrollTop = el.scrollHeight })
    resize?.observe(el)
    return () => { observer.disconnect(); resize?.disconnect() }
  }, [])
  useLayoutEffect(() => {
    const el = conversation.current
    const anchor = el?.querySelector<HTMLElement>(`[data-record="${node.id}"]`)
    if (el && anchor) {
      el.scrollTop += anchor.getBoundingClientRect().top - el.getBoundingClientRect().top
      following.current = node.id === records.at(-1)?.id
      reading.current = { id: `record:${node.id}`, offset: 0 }
    }
    // An explicit record link is a viewport anchor, not a filter or a new send target.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [node.id])
  const showMaterials = (id: string) => {
    setGroups(old => ({ open: new Set([...old.open, id]), closed: new Set([...old.closed].filter(value => value !== id)) })); setTab('materials')
    requestAnimationFrame(() => {
      const el = materials.current, target = el?.querySelector<HTMLElement>(`[data-material-record="${id}"]`)
      if (el && target) el.scrollTop += target.getBoundingClientRect().top - el.getBoundingClientRect().top
    })
  }
  const oldDrafts = drafts.filter(d => d.nodeRunId !== draft.nodeRunId && (d.text || d.images?.length))
  const stepIndex = detail.workflow.steps.findIndex(s => s.id === node.stepId)
  const stepTitle = detail.workflow.steps[stepIndex]?.title
  const number = stepNumber(stepIndex)
  const [reasonHead, ...reasonRest] = (statusReason ?? '').split('\n')
  // Node materials: the visit being viewed comes first, the rest newest to oldest; only the ones that have been expanded (by default the
  // one being viewed) are open.
  const materialRecords = [...records.filter(r => r.id === node.id), ...records.filter(r => r.id !== node.id).reverse()]
  const artifactCount = detail.artifacts.filter(a => records.some(r => r.id === a.nodeRunId)).length
  const backToConversation = (id: string) => {
    setTab('conversation')
    requestAnimationFrame(() => {
      const el = conversation.current, target = el?.querySelector<HTMLElement>(`[data-record="${id}"]`)
      if (el && target) el.scrollTop += target.getBoundingClientRect().top - el.getBoundingClientRect().top
    })
  }
  const pad = 'px-[clamp(14px,3vw,34px)]'
  return <>
    {/* Two index tabs on the sheet's top edge: Conversation and Node materials. The left/right arrow keys switch between them. */}
    <div role="tablist" className={indexTabsClass} aria-label={t.tasks.record} onKeyDown={event => { if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') { event.preventDefault(); const next = tab === 'conversation' ? 'materials' : 'conversation'; setTab(next); event.currentTarget.querySelector<HTMLButtonElement>(`[data-tab="${next}"]`)?.focus() } }}>
      {(['conversation', 'materials'] as const).map(value => <button key={value} type="button" role="tab" data-tab={value} aria-selected={tab === value} aria-controls={`task-${value}`} tabIndex={tab === value ? 0 : -1} className={cn(indexTabClass(tab === value), 'cursor-pointer')} onClick={() => setTab(value)}>{value === 'conversation' ? t.tasks.conversationTab : t.tasks.materials}</button>)}
    </div>
    {/* On narrow screens the whole page scrolls and the sheet gets a fixed height (about one screen); the conversation still scrolls on its
        own inside the sheet. */}
    <Sheet className="min-h-0 flex-1 max-nav:h-[max(560px,calc(100dvh-72px))] max-nav:flex-none">
      <div className="flex min-h-0 flex-1">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <div id="task-conversation" role="tabpanel" aria-label={t.tasks.conversation} hidden={tab !== 'conversation'} className="flex min-h-0 flex-1 flex-col">
            {/* Node header: hollow step number plus step name (yellow highlighter mark), Agent, which visit, a navy “当前节点” (Current node)
                tag, status stamp;
                below it the reason (a red sticky note when blocked) and recovery actions (fluorescent-pink “重试当前节点” (Retry current
                node); the others are secondary buttons). */}
            <div className={cn('flex max-h-[45%] min-h-0 shrink-0 flex-col gap-3 overflow-y-auto border-b-2 border-ink pt-[22px] pb-4 max-nav:pt-4', pad)} data-node-status>
              <NodeHeading number={number} title={stepTitle} agentId={node.agentId} agentName={node.agentName}>
                <span className="font-mono text-[12px] text-ink-500">{t.tasks.visit(node.visit, node.attempt)}</span>
                {current ? <span className="rounded-[4px] bg-ink px-2 text-[11.5px] leading-5 font-black text-white">{t.tasks.currentNode}</span> : <span className="text-[12px] font-bold text-ink-500">{t.tasks.readonly}</span>}
                <BackgroundTasksMark tasks={backgroundTasks} stopper={backgroundStopper} hint={t.background.nodeHint} />
                <TaskBadge className="ml-auto" status={node.status} />
              </NodeHeading>
              {/* Next to the reason note are the recovery actions: fluorescent-pink “重试当前节点” (Retry current node), and beside it
                  “更换 Lark 账号并继续 QA” (Switch Lark account and continue QA) (a secondary button that expands the form below when clicked). */}
              <TaskLarkSwitchController detail={detail} node={node} disabled={disabled}>
                {(lark) => <>
                  {(statusReason || current) && <div className="flex flex-wrap items-start gap-x-[22px] gap-y-3">
                {statusReason && <StickyNote tone={node.status === 'blocked' ? 'red' : 'yellow'} className="min-w-0 flex-[1_1_380px] items-start text-[14px] leading-[1.6] font-normal [&>svg]:mt-[3px]">
                  <strong className="block font-black [overflow-wrap:anywhere]">{reasonHead}</strong>
                  {reasonRest.length > 0 && <span className="line-clamp-2 text-ink [overflow-wrap:anywhere]">{reasonRest.join('\n')}</span>}
                  <details className="mt-0.5"><summary className="cursor-pointer font-black underline underline-offset-3">{t.tasks.details}</summary><p className="mt-1 whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{statusReason}</p></details>
                </StickyNote>}
                    <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2 pt-1.5"><TaskActions task={detail.task} showRetry={current} showCancel={false} disabled={disabled} />{lark.toggle}</div>
                  </div>}
                  {lark.panel}
                </>}
              </TaskLarkSwitchController>
              {node.sleep?.state === 'sleeping' && node.sleep.nodeRunId === node.id && node.sleep.runtimeId === node.runtimeId && node.sleep.wakeAt && <SleepUntil at={node.sleep.wakeAt} reason={node.sleep.reason} />}
              {node.question && <details className="text-[13.5px]"><summary className="cursor-pointer font-bold">{t.tasks.question} · {t.tasks.questionState[node.question.status]}</summary><PendingQuestion question={node.question} /></details>}
            </div>
            <div className="min-h-[120px] flex-1 overflow-hidden">
              <div ref={conversation} tabIndex={0} onScroll={capture} aria-label={t.tasks.conversation} className={cn('relative h-full min-w-0 overflow-y-auto overscroll-contain pt-5 pb-3 outline-none focus-visible:shadow-focus', pad)} data-conversation-scroll>
                {records.map(record => <MessageSegment key={record.id} detail={detail} node={record} requests={record.id === node.id ? permissionRequests : []} suppressInboxError={current && record.id === node.id} onMaterials={() => showMaterials(record.id)} onReply={current && record.id === node.id ? message => setReplying({ nodeRunId: node.id, messageId: message.id }) : undefined} />)}
              </div>
            </div>
            {newMessages && <Button variant="secondary" size="sm" className="mx-4 mb-2 shrink-0 self-center" onClick={() => { const el = conversation.current; if (el) el.scrollTop = el.scrollHeight; following.current = true; setNewMessages(false) }}>{t.tasks.newMessages}</Button>}
            {/* Input area: a light-yellow bar at the bottom of the sheet (same as the chat page), with a line above it saying who it goes to. */}
            <div className="flex max-h-[45dvh] min-h-0 shrink-0 flex-col gap-2 overflow-y-auto border-t-2 border-ink bg-tint-yellow px-[clamp(12px,2vw,22px)] pt-2.5 pb-3" data-node-draft>
              <div className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-[12px] font-bold text-ink-700 sm:pl-[52px]">
                <span>{t.tasks.page.sendTo}</span>
                <span className="rounded-[4px] border-[1.5px] border-ink bg-paper-raised px-[7px] leading-[18px] font-black text-ink [overflow-wrap:anywhere]">{number} {stepTitle} · {draft.agentName} · {t.tasks.visit(node.visit, node.attempt)}</span>
                <details className="ml-1"><summary className="cursor-pointer text-ink-500 underline decoration-dotted underline-offset-3">{t.tasks.targetDetails}</summary><p className="mt-1 font-mono text-[11.5px] font-normal break-all">{t.tasks.task}: {detail.task.id}<br />{t.tasks.record}: {draft.nodeRunId}<br />{t.tasks.instance}: {draft.runtimeId}</p></details>
              </div>
              {current && unsupported && <p role="status" className="text-[13px] font-bold">{t.tasks.nodeMessagesUnsupported}</p>}
              {current && !unsupported && <ErrorAlert error={capability.error ?? inbox.error} />}
              {current && capability.data && !capability.data.canSend && <p role="status" className="text-[13px] font-bold">{capability.data.reason} · {capability.data.handlingHint}</p>}
              {!current && (draft.text || !!draft.images?.length) && <p role="status" className="text-[13px] font-bold">{t.tasks.draftChanged}</p>}
              {permissionRequests.length > 0 && <WaitingForApproval requests={permissionRequests} />}
              <NodeMessageComposer taskId={detail.task.id} draft={draft} current={current} disabled={disabled || unsupported || !!capability.error || !!inbox.error || inbox.isPending || capability.isFetching} capability={capability.data} text={draft.text} onChange={onDraft} onImages={onImages} imageLimits={messageImageLimits(context.data?.limits)} onAccepted={onAccepted} onAction={onAction}
                queue={<><QueueMessages notice={stop.notice} error={stop.error} />{current && !unsupported && <NodeQueueList taskId={detail.task.id} nodeRunId={node.id} agentId={node.agentId} runtimeId={node.runtimeId} disabled={disabled} onEdit={refill} />}</>}
                stop={current && !unsupported && activeRunId ? stop.control(activeRunId, refreshMessages) : undefined}
                stopAlso={backgroundTasks.length}
                reply={current && replyTarget ? { message: replyTarget, name: node.agentName, maxBytes: context.data?.limits.maxInboxMessage, onCancel: () => setReplying(null), onDone: () => setReplying(null) } : undefined} />
              {draft.text && !current && <Button size="sm" variant="secondary" className="self-start" onClick={() => void navigator.clipboard.writeText(draft.text)}>{t.tasks.copyDraft}</Button>}
              {oldDrafts.length > 0 && <details className="text-[13px]"><summary className="cursor-pointer font-bold">{t.tasks.draftChanged}</summary>{oldDrafts.map(d => <div key={d.nodeRunId} className="mt-2"><p className="break-all">{d.agentName} · {d.nodeRunId}</p><pre className="whitespace-pre-wrap break-words">{d.text}</pre><div className="flex flex-wrap gap-2">{d.images?.map(image => <DraftImagePreview key={image.id} file={image.file} />)}</div><Button size="sm" variant="secondary" onClick={() => void navigator.clipboard.writeText(d.text)}>{t.tasks.copyDraft}</Button></div>)}</details>}
            </div>
          </div>
          <div id="task-materials" role="tabpanel" aria-label={t.tasks.materials} hidden={tab !== 'materials'} ref={materials} tabIndex={0} className="min-h-0 flex-1 overflow-y-auto overscroll-contain outline-none focus-visible:shadow-focus" data-materials-scroll>
            <div className={cn('border-b-2 border-ink pt-[22px] pb-4 max-nav:pt-4', pad)}>
              <NodeHeading number={number} title={stepTitle} agentId={node.agentId} agentName={node.agentName}>
                <span className="font-mono text-[12px] text-ink-500">{t.tasks.page.visits(records.length)} · {t.tasks.page.artifactCount(artifactCount)}</span>
              </NodeHeading>
            </div>
            <div className={cn('flex flex-col gap-4 pt-5 pb-8', pad)}>
              {materialRecords.map((record, index) => <MaterialRecord key={record.id} detail={detail} record={record} viewing={record.id === node.id} divided={index > 0}
                open={groups.open.has(record.id)}
                onToggle={open => setGroups(old => { const next = { open: new Set(old.open), closed: new Set(old.closed) }; if (open) { next.open.add(record.id); next.closed.delete(record.id) } else { next.open.delete(record.id); next.closed.add(record.id) } return next })}
                onConversation={() => backToConversation(record.id)} />)}
            </div>
          </div>
        </div>
        {stub}
      </div>
    </Sheet>
  </>
}

// The first line of the node header: hollow step number, step name (ZCOOL 30px with a yellow highlighter mark), Agent, followed by the
// details the caller supplies (which visit, status...).
function NodeHeading({ number, title, agentId, agentName, children }: { number: string; title: string | undefined; agentId: string; agentName: string; children?: ReactNode }) {
  return <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
    <HollowNumber className="w-auto">{number}</HollowNumber>
    <h2 className="riso-marker self-start px-1.5 font-display-cn text-[30px] leading-[1.25] font-normal [overflow-wrap:anywhere] max-nav:text-[26px]">{title}</h2>
    <span className="flex items-center gap-1.5 text-[13.5px] font-black"><AgentAvatar size="sm" id={agentId} />{agentName}</span>
    {children}
  </div>
}

// One visit in the node materials: a "receipt". The top line has the version (V2·A1), which visit, status, start and end time;
// when opened: inputs (a key-value table, where values from other visits become links), output (JSON on a light-blue background), and
// artifacts (file cards with a download button).
// A visit that was sent back (blocked but with output) gets a red warning on top. Visits not being viewed are collapsed by default.
function MaterialRecord({ detail, record, viewing, divided, open, onToggle, onConversation }: { detail: Schemas['TaskDetail']; record: Node; viewing: boolean; divided: boolean; open: boolean; onToggle: (open: boolean) => void; onConversation: () => void }) {
  const values = Object.entries(record.inputs)
  const versions = new Map(record.inputVersions.map(v => [v.input, v.nodeRunId]))
  const keys = [...new Set([...values.map(([key]) => key), ...versions.keys()])]
  const source = (id: string) => { const from = detail.nodes.find(n => n.id === id), index = detail.workflow.steps.findIndex(s => s.id === from?.stepId); return { from, index } }
  return <details data-material-record={record.id} open={open} onToggle={event => onToggle(event.currentTarget.open)} className={cn('group min-w-0', divided && 'border-t-2 border-dashed border-ink/40 pt-4')}>
    <summary className="flex cursor-pointer list-none flex-wrap items-center gap-x-3 gap-y-1.5 [&::-webkit-details-marker]:hidden">
      <span className={cn('rounded-[4px] px-2 font-mono text-[11.5px] leading-[22px] font-bold', viewing ? 'bg-ink text-white' : 'bg-paper-tab text-ink-700')}>{t.tasks.page.version(record.visit, record.attempt)}</span>
      <span className="text-[15px] font-black">{t.tasks.visit(record.visit, record.attempt)}</span>
      <TaskBadge status={record.status} />
      {record.startedAt && <span className="font-mono text-[12px] text-ink-500">{t.tasks.page.ran(record.startedAt, record.endedAt)}</span>}
      <span className="ml-auto text-[13px] font-black text-riso-blue">{open ? t.tasks.page.collapse : t.tasks.page.expand}<span aria-hidden="true"> {open ? '▴' : '▾'}</span></span>
    </summary>
    <div className="mt-3.5 flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-end gap-x-5 gap-y-2.5">
        {record.status === 'blocked' && record.output && <p role="note" className="flex min-w-0 flex-[1_1_320px] items-center gap-2 rounded-[4px] bg-tint-red px-4 py-2.5 text-[13.5px] font-black text-red-text"><CircleAlertIcon aria-hidden="true" className="size-4 shrink-0" strokeWidth={2.4} />{t.tasks.inspectOnly}</p>}
        <button type="button" className="shrink-0 cursor-pointer text-[13px] font-black text-riso-blue underline underline-offset-3" onClick={onConversation}>{t.tasks.page.backToConversation}</button>
      </div>
      {record.reason && record.status !== 'blocked' && <p className="text-[14px] [overflow-wrap:anywhere]">{record.reason}</p>}
      <div className="grid min-w-0 gap-x-7 gap-y-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <section className="min-w-0">
          <h3 className="riso-marker mb-2 inline-block px-1 font-display-cn text-[20px] leading-[1.3] font-normal">{t.tasks.page.inputs}</h3>
          {keys.length === 0 ? <p className="text-[13.5px] text-ink-500">{t.tasks.none}</p> : <dl className="grid grid-cols-[minmax(72px,auto)_minmax(0,1fr)] border-t-2 border-ink text-[13.5px]">
            {keys.map(key => { const version = versions.get(key), value = record.inputs[key], origin = version ? source(version) : null
              return <div key={key} className="contents">
                <dt className="border-b-[1.5px] border-dashed border-ink/25 py-2.5 pr-4 pl-0.5 font-mono text-[12.5px] text-ink-700 [overflow-wrap:anywhere]">{key}</dt>
                <dd className="flex min-w-0 flex-col gap-1 border-b-[1.5px] border-dashed border-ink/25 py-2.5 [overflow-wrap:anywhere]">
                  {version && <Link className="font-black text-riso-blue underline underline-offset-3" to={`?${new URLSearchParams({ step: origin?.from?.stepId ?? '', record: version })}`}>{origin?.from ? t.tasks.page.versionLink(stepNumber(origin.index), detail.workflow.steps[origin.index]?.title ?? '', origin.from.visit) : `${t.tasks.source(key)} · ${version}`}</Link>}
                  {value !== undefined && (typeof value === 'string' ? <span className={cn(version && 'text-[12.5px] text-ink-700')}>{value}</span> : <code className="font-mono text-[12px] break-all">{JSON.stringify(value)}</code>)}
                </dd>
              </div> })}
          </dl>}
        </section>
        <section className="min-w-0">
          <h3 className="riso-marker mb-2 inline-block px-1 font-display-cn text-[20px] leading-[1.3] font-normal">{t.tasks.output}</h3>
          {record.output === null ? <p className="text-[13.5px] text-ink-500">{t.tasks.noOutput}</p> : <pre className="max-w-full overflow-x-auto rounded-card bg-tint-blue px-[18px] py-3.5 font-mono text-[13px] leading-[1.7] whitespace-pre-wrap break-all">{JSON.stringify(record.output, null, 2)}</pre>}
        </section>
      </div>
      <Artifacts artifacts={detail.artifacts.filter(a => a.nodeRunId === record.id)} />
    </div>
  </details>
}
// A question the node raised is answerable only while the task still says so; other messages to you defer to the server.
const answerableFor = (node: Node, message: Schemas['InboxMessage']) => node.question?.messageId === message.id ? node.question.status === 'open' && node.question.canAnswer : true
// onReply: only the current record's questions can switch the node composer into reply mode.
function MessageSegment({ detail, node, onMaterials, suppressInboxError, onReply, requests }: { detail: Schemas['TaskDetail']; node: Node; onMaterials: () => void; suppressInboxError: boolean; onReply?: (message: Schemas['InboxMessage']) => void; requests: Schemas['PermissionRequest'][] }) {
  const messages = useTaskMessages(detail.task.id, node.id)
  const inbox = useTaskInboxMessages(detail.task.id, node.id, node.agentId, node.runtimeId)
  // Like normal chat, a message still waiting in the node's queue is shown only in the queue tray; it joins the flow once it starts.
  // Cancelled or not-executed messages have left the queue and keep showing their final status here.
  const queue = useTaskNodeQueue(detail.task.id, node.id, node.id === detail.task.currentNodeRunId)
  const queued = new Set(queue.data?.runtimeId === node.runtimeId ? queue.data.items.map(item => item.messageId) : [])
  // A turn still queued is not in the flow yet, nor is one voided before it ran (its messages promoted, cancelled, or already read):
  // both its prompt and its empty reply are hidden. A queued turn joins the flow once it starts.
  const workRows = [...new Map(messages.data?.retainedMessages.filter(m => m.nodeRunId === node.id && !m.voidReason && m.status !== 'queued').map(m => [m.id, m])).values()].sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.id.localeCompare(b.id))
  const rows = [
    ...workRows.map(message => ({ key: `task:${message.id}`, at: message.createdAt, body: <TaskMessageBubble message={message} agent={{ id: node.agentId, name: node.agentName }} taskId={detail.task.id} nodeRunId={node.id} runtimeId={inbox.data && !nodeMessagesUnsupported(inbox.error) ? node.runtimeId : undefined} requests={message.role === 'assistant' ? requests.filter(request => request.runId === message.runId) : []} /> })),
    // An approval question is the request card in the reply that asked (and the run record keeps it), so it is not shown twice.
    ...(inbox.data?.items ?? []).filter(record => !queued.has(record.message.id) && record.message.question?.kind !== 'permission').map(record => {
      const extras = <>
        {record.notExecuted && <p role="status" className="rounded-input bg-tint-yellow p-2 text-sm [overflow-wrap:anywhere]">{inboxCopy.notExecuted} · {inboxCopy.targetReasons[record.notExecuted.reason] ?? record.notExecuted.reason}</p>}
        {record.runLinks.length > 0 && <details className="text-xs [overflow-wrap:anywhere]"><summary>{inboxCopy.links}</summary><ul>{record.runLinks.map(link => <li key={`${link.runId}:${link.role}`}>{link.role === 'notification' ? inboxCopy.notification : link.role === 'read' ? inboxCopy.readLink : inboxCopy.replyLink} · {link.runId}</li>)}</ul></details>}
      </>
      // Messages this node's instance sent you show as “发给你” (For you) (with options when it asked a question); the rest keep the Inbox bubble.
      const body = record.message.from.kind === 'agent' && record.message.to.kind === 'console'
        ? <div data-inbox-message-id={record.message.id}><ConsoleMessage message={record.message} agent={{ id: node.agentId, name: node.agentName }} answerable={answerableFor(node, record.message)} onReply={onReply} meta={<Badge tone="info">{record.message.inReplyTo ? inboxCopy.reply : inboxCopy.message}</Badge>}>{extras}</ConsoleMessage></div>
        : <TaskInboxMessageBubble message={record.message}>{extras}</TaskInboxMessageBubble>
      return { key: `inbox:${record.message.id}`, at: record.message.createdAt, body }
    }),
  ].sort((a, b) => a.at.localeCompare(b.at) || a.key.localeCompare(b.key))
  const cutLabel = `${t.tasks.visit(node.visit, node.attempt)} · ${t.tasks.nodeStatuses[node.status]} · ${t.tasks.page.seeMaterials}`
  return <section data-record={node.id} data-reading-anchor={`record:${node.id}`} className="mb-7 min-w-0">
    {/* Between visits of the same node is a scissors cut line (like the chat page's “会话已重开” (session reopened) line); clicking the
        label in the middle opens this visit's node materials. */}
    <div className="mb-[18px]"><CutLine icon={ScissorsIcon} label={t.tasks.execution}><button type="button" className="min-w-0 cursor-pointer text-center text-[12.5px] font-black text-ink underline decoration-dotted underline-offset-4 [overflow-wrap:anywhere]" aria-label={`${t.tasks.execution} · ${cutLabel}`} onClick={onMaterials}>{cutLabel}</button></CutLine></div>
    <ErrorAlert error={messages.error} />{(messages.isPending || inbox.isPending) && <p>{t.common.loading}</p>}{messages.isSuccess && inbox.isSuccess && rows.length === 0 && <p>{t.tasks.noMessages}</p>}
    {!suppressInboxError && !nodeMessagesUnsupported(inbox.error) && <ErrorAlert error={inbox.error} />}
    <ol className="flex min-w-0 flex-col gap-7 pb-2 pr-2">{rows.map(row => <li key={row.key} data-message-id={row.key} data-reading-anchor={`message:${row.key}`} className="min-w-0">{row.body}</li>)}</ol>
    {inbox.hasNextPage && <div className="mt-4"><p className="text-sm">{t.tasks.incomplete}</p><Button variant="secondary" disabled={inbox.isFetching} onClick={() => void inbox.fetchNextPage({ cancelRefetch: false })}>{t.tasks.more}</Button></div>}
    {messages.hasNextPage && <div className="mt-4"><p className="text-sm">{t.tasks.incomplete}</p><Button variant="secondary" disabled={messages.isFetching} onClick={() => void messages.fetchNextPage({ cancelRefetch: false })}>{t.tasks.more}</Button></div>}
  </section>
}
// Artifacts: file cards with a download button (yellow file icon, monospace file name, type and size).
export function Artifacts({ artifacts }: { artifacts: Schemas['TaskArtifact'][] }) {
  return <section className="min-w-0">
    <h3 className="riso-marker mb-3 inline-block px-1 font-display-cn text-[20px] leading-[1.3] font-normal">{t.tasks.page.artifacts}</h3>
    {artifacts.length === 0 && <p className="text-[13.5px] text-ink-500">{t.tasks.noArtifacts}</p>}
    <ul className="grid gap-3.5 sm:grid-cols-[repeat(auto-fill,minmax(260px,1fr))]">{artifacts.map((a) => <li key={a.id} className="mr-1 flex min-w-0 items-center gap-3 rounded-card border-2 border-ink bg-paper-raised px-3.5 py-3 shadow-[4px_4px_0_var(--ink)]">
      <span aria-hidden="true" className="flex size-9 shrink-0 items-center justify-center rounded-[10px] border-2 border-ink bg-riso-yellow"><FileTextIcon className="size-4" strokeWidth={2.4} /></span>
      <span className="flex min-w-0 flex-1 flex-col leading-[1.4]"><span id={`artifact-${a.id}`} className="font-mono text-[13.5px] font-bold break-all">{a.name}</span><span className="text-[12px] text-ink-500">{a.contentType} · {t.common.bytes(a.size)}</span></span>
      <a href={config.apiBase + artifactUrl(a)} aria-label={t.tasks.download} aria-describedby={`artifact-${a.id}`} title={t.tasks.download} className="flex size-9 shrink-0 items-center justify-center rounded-full border-2 border-ink bg-paper-raised text-ink outline-none hover:bg-riso-yellow focus-visible:shadow-focus"><DownloadIcon aria-hidden="true" className="size-4" strokeWidth={2.4} /></a>
    </li>)}</ul>
  </section>
}
