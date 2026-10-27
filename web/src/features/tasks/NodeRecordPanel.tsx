import { taskInboxText as inboxCopy } from '@/i18n/task-inbox'
import { useTaskInboxMessages } from '@/api/task-inbox'
import { TaskInboxMessageBubble } from './TaskInboxMessageBubble'
import { ConsoleMessage } from '@/features/console-inbox/ConsoleMessage'
import { consoleInboxText } from '@/i18n/console-inbox'
import { TaskMessageBubble } from './TaskMessageBubble'
import { useLayoutEffect, useRef, useState } from 'react'
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

// 节点的待回答问题：问题本身作为“发给你”的消息排在节点对话里，在那里点选项或回复；这里只写明状态，并可以跳到它。
export function PendingQuestion({ question }: { question: Schemas['TaskQuestionRef'] }) {
  const locate = () => document.querySelector(`[data-console-message-id="${question.messageId}"]`)?.scrollIntoView({ block: 'center' })
  return <section className="grid gap-3 rounded-card bg-tint-yellow p-4"><h3 className="font-bold">{t.tasks.question}</h3><Badge>{t.tasks.questionState[question.status]}</Badge><Markdown text={question.text} />
    {question.status === 'open' && question.canAnswer ? <Button className="justify-self-start" onClick={locate}>{consoleInboxText.locateQuestion}</Button> : <p className="text-sm">{question.status === 'open' ? t.tasks.cannotAnswer : t.tasks.questionState[question.status]}</p>}
  </section>
}
type Node = Schemas['TaskNodeRun']
export type NodeDraft = { text: string; images?: ImageDraft[]; agentId: string; runtimeId: string; nodeRunId: string; agentName: string; action?: { body: Schemas['TaskNodeInboxInput']; draftImageIds?: string[]; state: 'sending' | 'unknown' | 'rejected' | 'accepted'; error?: unknown; receipt?: Schemas['TaskNodeInboxReceipt'] } }
export function NodeRecordPanel({ detail, node, draft, onDraft, onImages, onAccepted, onAction, drafts, disabled }: { detail: Schemas['ConsoleTaskDetail']; node: Node; draft: NodeDraft; onDraft: (text: string) => void; onImages?: (images: ImageDraft[]) => void; onAccepted: (text: string, draftImageIds?: string[]) => void; onAction: (action: NodeDraft['action']) => void; drafts: NodeDraft[]; disabled: boolean }) {
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
  return <section className="flex h-[800px] min-h-0 min-w-0 flex-col overflow-hidden rounded-panel bg-white shadow-print-rest lg:h-full">
    <div className="max-h-[35%] min-h-0 shrink overflow-y-auto border-b border-ink-200 bg-paper-raised p-4" data-node-status>
      <div className="flex flex-wrap items-center justify-between gap-2"><strong>{detail.workflow.steps.find(s => s.id === node.stepId)?.title} · {node.agentName}</strong><TaskBadge status={node.status} /></div>
      <p className="mt-1 text-xs">{t.tasks.visit(node.visit, node.attempt)} · {current ? t.tasks.currentNode : t.tasks.readonly}</p>
      {statusReason && <><p className="mt-2 line-clamp-2 break-words">{statusReason}</p><details className="mt-2 text-sm"><summary>{t.tasks.details}</summary><p className="mt-2 whitespace-pre-wrap break-words">{statusReason}</p></details></>}
      {node.sleep?.state === 'sleeping' && node.sleep.nodeRunId === node.id && node.sleep.runtimeId === node.runtimeId && node.sleep.wakeAt && <SleepUntil at={node.sleep.wakeAt} reason={node.sleep.reason} />}
      <div className="mt-2"><TaskActions task={detail.task} showRetry={current} showCancel={false} disabled={disabled} /></div>
      <TaskLarkSwitchController detail={detail} node={node} disabled={disabled} />
      {node.question && <details className="mt-2"><summary>{t.tasks.question} · {t.tasks.questionState[node.question.status]}</summary><PendingQuestion question={node.question} /></details>}
    </div>
    <div role="tablist" className="flex shrink-0 gap-2 border-b border-ink-200 p-3" aria-label={t.tasks.record} onKeyDown={event => { if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') { event.preventDefault(); const next = tab === 'conversation' ? 'materials' : 'conversation'; setTab(next); event.currentTarget.querySelector<HTMLButtonElement>(`[data-tab="${next}"]`)?.focus() } }}>
      {(['conversation', 'materials'] as const).map(value => <button key={value} role="tab" data-tab={value} aria-selected={tab === value} aria-controls={`task-${value}`} tabIndex={tab === value ? 0 : -1} className={tab === value ? 'rounded-input bg-riso-yellow px-4 py-2 font-bold' : 'rounded-input px-4 py-2'} onClick={() => setTab(value)}>{value === 'conversation' ? t.tasks.conversationTab : t.tasks.materials}</button>)}
    </div>
    <div id="task-conversation" role="tabpanel" aria-label={t.tasks.conversation} hidden={tab !== 'conversation'} className="min-h-[120px] flex-1 overflow-hidden">
      <div ref={conversation} tabIndex={0} onScroll={capture} aria-label={t.tasks.conversation} className="relative h-full min-w-0 overflow-y-auto overscroll-contain p-4" data-conversation-scroll>
        {records.map(record => <MessageSegment key={record.id} detail={detail} node={record} suppressInboxError={current && record.id === node.id} onMaterials={() => showMaterials(record.id)} onReply={current && record.id === node.id ? message => setReplying({ nodeRunId: node.id, messageId: message.id }) : undefined} />)}
      </div>
    </div>
    <div id="task-materials" role="tabpanel" aria-label={t.tasks.materials} hidden={tab !== 'materials'} ref={materials} tabIndex={0} className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-4" data-materials-scroll>
      {records.map(record => <details key={record.id} data-material-record={record.id} open={groups.open.has(record.id) || record.id === records.at(-1)?.id && !groups.closed.has(record.id)} onToggle={event => { const open = event.currentTarget.open; setGroups(old => { const next = { open: new Set(old.open), closed: new Set(old.closed) }; if (open) { next.open.add(record.id); next.closed.delete(record.id) } else { next.open.delete(record.id); next.closed.add(record.id) } return next }) }} className="mb-4 rounded-card border border-ink-200 p-3">
        <summary className="cursor-pointer font-bold">{t.tasks.visit(record.visit, record.attempt)} · {t.tasks.nodeStatuses[record.status]}</summary><div className="mt-4 grid gap-5">
          {record.reason && <p className="break-words">{record.reason}</p>}
          <DataBlock title={t.tasks.input} value={record.inputs} />
          {record.inputVersions.length > 0 && <div><h3 className="font-bold">{t.tasks.versions}</h3><ul>{record.inputVersions.map(v => <li key={v.input}><Link className="break-all text-riso-blue underline" to={`?${new URLSearchParams({ step: detail.nodes.find(n => n.id === v.nodeRunId)?.stepId ?? '', record: v.nodeRunId })}`}>{t.tasks.source(v.input)} · {v.nodeRunId}</Link></li>)}</ul></div>}
          <div>{record.status === 'blocked' && record.output && <p className="mb-2 rounded-card bg-tint-red p-3 font-bold text-red-text">{t.tasks.inspectOnly}</p>}<DataBlock title={t.tasks.output} value={record.output} /></div>
          <Artifacts artifacts={detail.artifacts.filter(a => a.nodeRunId === record.id)} />
        </div>
      </details>)}
    </div>
    {tab === 'conversation' && newMessages && <Button variant="secondary" className="mx-4 mb-2 shrink-0" onClick={() => { const el = conversation.current; if (el) el.scrollTop = el.scrollHeight; following.current = true; setNewMessages(false) }}>{t.tasks.newMessages}</Button>}
    <div hidden={tab !== 'conversation'} className="max-h-[45dvh] min-h-0 shrink overflow-y-auto border-t border-ink-200 p-3" data-node-draft>
      <p className="text-xs font-bold">{t.tasks.draftTarget} · {detail.workflow.steps.find(s => s.id === node.stepId)?.title} · {draft.agentName} · {t.tasks.visit(node.visit, node.attempt)}</p>
      {current && unsupported && <p role="status" className="mt-2 text-sm">{t.tasks.nodeMessagesUnsupported}</p>}
      {current && !unsupported && <ErrorAlert error={capability.error ?? inbox.error} />}
      {current && capability.data && !capability.data.canSend && <p role="status" className="mt-2 text-sm">{capability.data.reason} · {capability.data.handlingHint}</p>}
      <details className="mt-1 text-xs"><summary>{t.tasks.targetDetails}</summary><p className="mt-1 break-all">{t.tasks.task}: {detail.task.id}<br />{t.tasks.record}: {draft.nodeRunId}<br />{t.tasks.instance}: {draft.runtimeId}</p></details>
      {!current && (draft.text || !!draft.images?.length) && <p role="status" className="mt-2 text-sm">{t.tasks.draftChanged}</p>}
      <NodeMessageComposer taskId={detail.task.id} draft={draft} current={current} disabled={disabled || unsupported || !!capability.error || !!inbox.error || inbox.isPending || capability.isFetching} capability={capability.data} text={draft.text} onChange={onDraft} onImages={onImages} imageLimits={messageImageLimits(context.data?.limits)} onAccepted={onAccepted} onAction={onAction}
        queue={<><QueueMessages notice={stop.notice} error={stop.error} />{current && !unsupported && <NodeQueueList taskId={detail.task.id} nodeRunId={node.id} agentId={node.agentId} runtimeId={node.runtimeId} disabled={disabled} onEdit={refill} />}</>}
        stop={current && !unsupported && activeRunId ? stop.control(activeRunId, refreshMessages) : undefined}
        reply={current && replyTarget ? { message: replyTarget, name: node.agentName, maxBytes: context.data?.limits.maxInboxMessage, onCancel: () => setReplying(null), onDone: () => setReplying(null) } : undefined} />
      {draft.text && !current && <Button size="sm" variant="secondary" onClick={() => void navigator.clipboard.writeText(draft.text)}>{t.tasks.copyDraft}</Button>}
      {oldDrafts.length > 0 && <details className="mt-2 text-sm"><summary>{t.tasks.draftChanged}</summary>{oldDrafts.map(d => <div key={d.nodeRunId} className="mt-2"><p className="break-all">{d.agentName} · {d.nodeRunId}</p><pre className="whitespace-pre-wrap break-words">{d.text}</pre><div className="flex flex-wrap gap-2">{d.images?.map(image => <DraftImagePreview key={image.id} file={image.file} />)}</div><Button size="sm" variant="secondary" onClick={() => void navigator.clipboard.writeText(d.text)}>{t.tasks.copyDraft}</Button></div>)}</details>}
    </div>
  </section>
}
// A question the node raised is answerable only while the task still says so; other messages to you defer to the server.
const answerableFor = (node: Node, message: Schemas['InboxMessage']) => node.question?.messageId === message.id ? node.question.status === 'open' && node.question.canAnswer : true
// onReply: only the current record's questions can switch the node composer into reply mode.
function MessageSegment({ detail, node, onMaterials, suppressInboxError, onReply }: { detail: Schemas['TaskDetail']; node: Node; onMaterials: () => void; suppressInboxError: boolean; onReply?: (message: Schemas['InboxMessage']) => void }) {
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
    ...workRows.map(message => ({ key: `task:${message.id}`, at: message.createdAt, body: <TaskMessageBubble message={message} agent={{ id: node.agentId, name: node.agentName }} taskId={detail.task.id} nodeRunId={node.id} runtimeId={inbox.data && !nodeMessagesUnsupported(inbox.error) ? node.runtimeId : undefined} /> })),
    ...(inbox.data?.items ?? []).filter(record => !queued.has(record.message.id)).map(record => {
      const extras = <>
        {record.notExecuted && <p role="status" className="rounded-input bg-tint-yellow p-2 text-sm [overflow-wrap:anywhere]">{inboxCopy.notExecuted} · {record.notExecuted.reason}</p>}
        {record.runLinks.length > 0 && <details className="text-xs [overflow-wrap:anywhere]"><summary>{inboxCopy.links}</summary><ul>{record.runLinks.map(link => <li key={`${link.runId}:${link.role}`}>{link.role === 'notification' ? inboxCopy.notification : link.role === 'read' ? inboxCopy.readLink : inboxCopy.replyLink} · {link.runId}</li>)}</ul></details>}
      </>
      // Messages this node's instance sent you show as 发给你 (with options when it asked a question); the rest keep the Inbox bubble.
      const body = record.message.from.kind === 'agent' && record.message.to.kind === 'console'
        ? <div data-inbox-message-id={record.message.id}><ConsoleMessage message={record.message} agent={{ id: node.agentId, name: node.agentName }} answerable={answerableFor(node, record.message)} onReply={onReply} meta={<Badge tone="info">{record.message.inReplyTo ? inboxCopy.reply : inboxCopy.message}</Badge>}>{extras}</ConsoleMessage></div>
        : <TaskInboxMessageBubble message={record.message}>{extras}</TaskInboxMessageBubble>
      return { key: `inbox:${record.message.id}`, at: record.message.createdAt, body }
    }),
  ].sort((a, b) => a.at.localeCompare(b.at) || a.key.localeCompare(b.key))
  return <section data-record={node.id} data-reading-anchor={`record:${node.id}`} className="mb-6 min-w-0">
    <button className="mb-3 w-full rounded-input bg-paper-raised p-3 text-left font-bold underline decoration-dotted" onClick={onMaterials}>{t.tasks.execution} · {t.tasks.visit(node.visit, node.attempt)} · {t.tasks.nodeStatuses[node.status]}</button>
    {node.reason && <p className="mb-3 break-words text-sm">{node.reason}</p>}
    <ErrorAlert error={messages.error} />{(messages.isPending || inbox.isPending) && <p>{t.common.loading}</p>}{messages.isSuccess && inbox.isSuccess && rows.length === 0 && <p>{t.tasks.noMessages}</p>}
    {!suppressInboxError && !nodeMessagesUnsupported(inbox.error) && <ErrorAlert error={inbox.error} />}
    <ol className="flex min-w-0 flex-col gap-7 pb-2 pr-2">{rows.map(row => <li key={row.key} data-message-id={row.key} data-reading-anchor={`message:${row.key}`} className="min-w-0">{row.body}</li>)}</ol>
    {inbox.hasNextPage && <div className="mt-4"><p className="text-sm">{t.tasks.incomplete}</p><Button variant="secondary" disabled={inbox.isFetching} onClick={() => void inbox.fetchNextPage({ cancelRefetch: false })}>{t.tasks.more}</Button></div>}
    {messages.hasNextPage && <div className="mt-4"><p className="text-sm">{t.tasks.incomplete}</p><Button variant="secondary" disabled={messages.isFetching} onClick={() => void messages.fetchNextPage({ cancelRefetch: false })}>{t.tasks.more}</Button></div>}
  </section>
}
function DataBlock({ title, value }: { title: string; value: unknown }) { return <section className="min-w-0"><h3 className="mb-2 font-bold">{title}</h3>{value === null ? <p>{t.tasks.noOutput}</p> : <pre className="max-w-full overflow-x-auto rounded-input bg-tint-blue p-3 text-sm whitespace-pre-wrap break-all">{JSON.stringify(value, null, 2)}</pre>}</section> }
export function Artifacts({ artifacts }: { artifacts: Schemas['TaskArtifact'][] }) { return <section className="min-w-0"><h3 className="mb-3 font-display-cn text-[28px]">{t.tasks.artifacts}</h3>{artifacts.length === 0 && <p>{t.tasks.noArtifacts}</p>}<ul className="grid gap-3">{artifacts.map((a) => <li key={a.id} className="flex min-w-0 flex-wrap justify-between gap-2 rounded-card bg-tint-blue p-3"><span className="min-w-0 break-all">{a.name} · {a.contentType} · {a.size}</span><a href={config.apiBase + artifactUrl(a)} className="font-bold text-riso-blue underline">{t.tasks.download}</a></li>)}</ul></section> }
