import { useLayoutEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { CircleAlertIcon, DownloadIcon, FileTextIcon, InfoIcon, ScissorsIcon } from 'lucide-react'
import { Link } from 'react-router'
import { stopAllTaskNodeBackgroundTasks, stopTaskNodeBackgroundTask, useTaskNodeBackgroundTasks } from '@/api/background-tasks'
import type { Schemas } from '@/api/client'
import { messageImageLimits } from '@/api/message-images'
import { useTaskNodePermissionRequests } from '@/api/permissions'
import { useContextInfo } from '@/api/queries'
import { useTaskInboxMessages } from '@/api/task-inbox'
import { nodeMessagesUnsupported, useNodeMessageCapability } from '@/api/task-node-capability'
import { stopTaskNodeRun, useTaskNodeQueue } from '@/api/task-node-queue'
import { artifactUrl, taskKeys, useTaskMessages } from '@/api/tasks'
import { AgentAvatar } from '@/components/AgentAvatar'
import { CutLine } from '@/components/CutLine'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ScrollRuler } from '@/components/PrecisionRuler'
import { StickyNote } from '@/components/StickyNote'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { config } from '@/config'
import { BackgroundTasksMark, type BackgroundStopper } from '@/features/chat/BackgroundTasks'
import { Markdown } from '@/features/chat/Markdown'
import { QueueMessages } from '@/features/chat/QueueList'
import { nodeConflictNotice, useStopReply } from '@/features/chat/queue-actions'
import { ConsoleMessage } from '@/features/console-inbox/ConsoleMessage'
import type { ImageDraft } from '@/features/images/image-drafts'
import { DraftImagePreview } from '@/features/images/ImagePreview'
import { WaitingForApproval } from '@/features/permissions/WaitingForApproval'
import { t } from '@/i18n'
import { copyText } from '@/lib/clipboard'
import { segmentedClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { NodeMessageComposer } from './NodeMessageComposer'
import { NodeQueueList } from './NodeQueueList'
import { TaskActions } from './TaskActions'
import { TaskBadge } from './TaskBadge'
import { TaskInboxMessageBubble } from './TaskInboxMessageBubble'
import { TaskLarkSwitchController } from './TaskLarkSwitchController'
import { TaskMessageBubble } from './TaskMessageBubble'
import { stepNumber } from './workflow-steps'

const inboxCopy = t.taskInbox

// The node's open question: the question itself sits in the node conversation as a "For you" message, where options are clicked
// or a reply is written; this only states its status and can jump to it.
export function PendingQuestion({ question }: { question: Schemas['TaskQuestionRef'] }) {
  const locate = () => document.querySelector(`[data-console-message-id="${question.messageId}"]`)?.scrollIntoView({ block: 'center' })
  return (
    <section className="mt-2 grid gap-2.5 rounded-card border border-hairline-strong bg-tint-yellow p-3.5 [[data-skin=plate]_&]:border-0">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="font-semibold [[data-skin=plate]_&]:font-bold">{t.tasks.question}</h3>
        <Badge>{t.tasks.questionState[question.status]}</Badge>
      </div>
      <Markdown text={question.text} />
      {question.status === 'open' && question.canAnswer ? (
        <Button size="xs" className="justify-self-start" onClick={locate}>
          {t.consoleInbox.locateQuestion}
        </Button>
      ) : (
        <p className="text-sm">{question.status === 'open' ? t.tasks.cannotAnswer : t.tasks.questionState[question.status]}</p>
      )}
    </section>
  )
}

type Node = Schemas['TaskNodeRun']
export type NodePanelTab = 'conversation' | 'materials'
export type NodeDraft = {
  text: string
  images?: ImageDraft[]
  agentId: string
  runtimeId: string
  nodeRunId: string
  agentName: string
  action?: {
    body: Schemas['TaskNodeInboxInput']
    draftImageIds?: string[]
    state: 'sending' | 'unknown' | 'rejected' | 'accepted'
    error?: unknown
    receipt?: Schemas['TaskNodeInboxReceipt']
  }
}

// Scoped right node workspace: compact 44px header with step title, agent, inline visit selector, status badge, background tasks
// and Conversation | Materials segmented toggle; lifted history/blocked/retry/Lark strip directly beneath so recovery controls
// remain reachable in both tabs; scrollable Conversation and Materials bodies paired with ScrollRuler.
export function NodeRecordPanel({
  detail,
  node,
  tab: controlledTab,
  onTabChange,
  onSelectRecord,
  draft,
  onDraft,
  onImages,
  onAccepted,
  onAction,
  drafts,
  disabled,
}: {
  detail: Schemas['ConsoleTaskDetail']
  node: Node
  tab?: NodePanelTab
  onTabChange?: (tab: NodePanelTab) => void
  onSelectRecord?: (stepId: string, recordId: string) => void
  draft: NodeDraft
  onDraft: (text: string) => void
  onImages?: (images: ImageDraft[]) => void
  onAccepted: (text: string, draftImageIds?: string[]) => void
  onAction: (action: NodeDraft['action']) => void
  drafts: NodeDraft[]
  disabled: boolean
}) {
  const records = detail.nodes.filter((n) => n.stepId === node.stepId).sort((a, b) => a.visit - b.visit || a.attempt - b.attempt || a.id.localeCompare(b.id))
  const [uncontrolledTab, setUncontrolledTab] = useState<NodePanelTab>('conversation')
  const tab = controlledTab ?? uncontrolledTab
  const setTab = (next: NodePanelTab) => {
    if (onTabChange) onTabChange(next)
    else setUncontrolledTab(next)
  }

  // Materials expansion state scoped to the active node record; switching records opens the newly selected record by default.
  const [groups, setGroups] = useState<{ nodeId: string; open: Set<string>; closed: Set<string> }>(() => ({
    nodeId: node.id,
    open: new Set([node.id]),
    closed: new Set<string>(),
  }))
  const activeGroups = groups.nodeId === node.id ? groups : { nodeId: node.id, open: new Set([node.id]), closed: new Set<string>() }

  const conversation = useRef<HTMLDivElement>(null)
  const materials = useRef<HTMLDivElement>(null)
  const following = useRef(true)
  const reading = useRef<{ id: string; offset: number } | null>(null)
  const fingerprint = useRef('')
  const activeNodeIdRef = useRef(node.id)
  const [newMessagesFor, setNewMessagesFor] = useState<string | null>(null)
  const newMessages = newMessagesFor === node.id

  const current = node.id === detail.task.currentNodeRunId && !['completed', 'cancelled'].includes(detail.task.status)
  const isHistory = Boolean(detail.task.currentNodeRunId && node.id !== detail.task.currentNodeRunId)
  const context = useContextInfo()
  const capability = useNodeMessageCapability(detail.task.id, node.id, current)
  const inbox = useTaskInboxMessages(detail.task.id, node.id, node.agentId, node.runtimeId)
  const unsupported = nodeMessagesUnsupported(capability.error) || nodeMessagesUnsupported(inbox.error)
  const client = useQueryClient()

  // The node's reply in progress, if any, can be stopped without sending a new message.
  const nodeMessages = useTaskMessages(detail.task.id, node.id)
  const activeRunId = nodeMessages.data?.retainedMessages.filter((m) => m.nodeRunId === node.id && (m.status === 'starting' || m.status === 'running')).at(-1)?.runId
  const stop = useStopReply(
    (runId, actionId) => stopTaskNodeRun(detail.task.id, node.id, runId, { actionId, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId }),
    (error) => nodeConflictNotice(error, 'stop'),
  )
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

  // Replying to a question this node asked you: the composer sends text as the answer until the question leaves 'open'.
  const [replying, setReplying] = useState<{ nodeRunId: string; messageId: string } | null>(null)
  const replyTarget = replying?.nodeRunId === node.id ? inbox.data?.items.find((item) => item.message.id === replying.messageId && item.message.question?.state === 'open')?.message : undefined
  const refill = (text: string, images: ImageDraft[]) => {
    onDraft(draft.text.trim() ? `${draft.text}\n${text}` : text)
    if (images.length) onImages?.([...(draft.images ?? []), ...images])
  }
  const statusReason = node.reason || node.question?.text || node.sleep?.reason

  const capture = () => {
    const el = conversation.current
    if (!el) return
    following.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    const top = el.getBoundingClientRect().top
    const anchor = [...el.querySelectorAll<HTMLElement>('[data-reading-anchor]')].find((row) => row.getBoundingClientRect().bottom > top)
    reading.current = anchor ? { id: anchor.dataset.readingAnchor!, offset: anchor.getBoundingClientRect().top - top } : null
    if (following.current) setNewMessagesFor(null)
  }

  useLayoutEffect(() => {
    activeNodeIdRef.current = node.id
  }, [node.id])

  useLayoutEffect(() => {
    const el = conversation.current
    if (!el) return
    const observer = new MutationObserver(() => {
      const next = [...el.querySelectorAll<HTMLElement>('[data-message-id]')].map((row) => `${row.dataset.messageId}:${row.textContent}`).join('|')
      if (next === fingerprint.current) return
      const changed = fingerprint.current !== ''
      fingerprint.current = next
      if (following.current) el.scrollTop = el.scrollHeight
      else {
        const old = reading.current
        const anchor = old && [...el.querySelectorAll<HTMLElement>('[data-reading-anchor]')].find((row) => row.dataset.readingAnchor === old.id)
        if (anchor && old) el.scrollTop += anchor.getBoundingClientRect().top - el.getBoundingClientRect().top - old.offset
        if (changed) setNewMessagesFor(activeNodeIdRef.current)
      }
    })
    observer.observe(el, { childList: true, subtree: true, characterData: true })
    // Image previews grow the composer outside the message DOM. Keep a reader at the bottom there; someone reading history retains scrollTop.
    const resize = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(() => { if (following.current) el.scrollTop = el.scrollHeight })
    resize?.observe(el)
    return () => {
      observer.disconnect()
      resize?.disconnect()
    }
  }, [])

  useLayoutEffect(() => {
    fingerprint.current = ''
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
    setGroups({
      nodeId: node.id,
      open: new Set([...activeGroups.open, id]),
      closed: new Set([...activeGroups.closed].filter((value) => value !== id)),
    })
    setTab('materials')
    requestAnimationFrame(() => {
      const el = materials.current,
        target = el?.querySelector<HTMLElement>(`[data-material-record="${id}"]`)
      if (el && target) el.scrollTop += target.getBoundingClientRect().top - el.getBoundingClientRect().top
    })
  }

  const oldDrafts = drafts.filter((d) => d.nodeRunId !== draft.nodeRunId && (d.text || d.images?.length))
  const stepIndex = detail.workflow.steps.findIndex((s) => s.id === node.stepId)
  const stepTitle = detail.workflow.steps[stepIndex]?.title
  const number = stepNumber(stepIndex)
  const [reasonHead, ...reasonRest] = (statusReason ?? '').split('\n')

  // Node materials: the visit being viewed comes first, the rest newest to oldest; only expanded ones (viewed by default) are open.
  const materialRecords = [...records.filter((r) => r.id === node.id), ...records.filter((r) => r.id !== node.id).reverse()]
  const artifactCount = detail.artifacts.filter((a) => records.some((r) => r.id === a.nodeRunId)).length
  const backToConversation = (id: string) => {
    setTab('conversation')
    requestAnimationFrame(() => {
      const el = conversation.current,
        target = el?.querySelector<HTMLElement>(`[data-record="${id}"]`)
      if (el && target) el.scrollTop += target.getBoundingClientRect().top - el.getBoundingClientRect().top
    })
  }

  const showRetryAction = current && detail.task.status === 'blocked' && Boolean(detail.task.currentNodeRunId)
  const showLarkAction = detail.task.status === 'blocked' && detail.task.currentNodeRunId === node.id && detail.task.currentAgentId === node.agentId
  const hasActionRow = Boolean(statusReason || showRetryAction || showLarkAction)
  const targetPlaceholder = `${t.tasks.draftPlaceholder(draft.agentName)} · ${number} ${stepTitle ?? node.stepId} (${t.tasks.page.version(node.visit, node.attempt)})`
  const targetDetailsToggle = (
    <details className="text-[11.5px] text-ink-500">
      <summary className="inline-flex cursor-pointer list-none items-center gap-1 text-ink-500 transition-colors hover:text-ink [&::-webkit-details-marker]:hidden">
        <InfoIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2} />
        <span>{t.tasks.targetDetails}</span>
      </summary>
      <p className="mt-1.5 rounded-ticket border border-hairline bg-surface-subtle px-2.5 py-1.5 font-mono text-[11px] leading-[1.6] font-normal break-all text-ink-700">
        {t.tasks.task}: {detail.task.id}
        <br />
        {t.tasks.record}: {draft.nodeRunId}
        <br />
        {t.tasks.instance}: {draft.runtimeId}
      </p>
    </details>
  )

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      {/* Compact 44px node header + lifted status/history/retry/Lark strip shared above both Conversation and Materials tabs. */}
      <div data-node-status className="flex shrink-0 flex-col">
        <div className="flex min-h-11 shrink-0 flex-wrap items-center gap-x-2.5 gap-y-1.5 border-b border-hairline bg-surface-canvas px-4 py-1.5 sm:px-5 [[data-skin=plate]_&]:border-b-2 [[data-skin=plate]_&]:border-ink">
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span className="shrink-0 font-mono text-[12px] font-semibold text-ink-500 tabular-nums [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-riso-blue">
              {number}
            </span>
            <h2 className="min-w-0 truncate text-[14px] leading-snug font-semibold tracking-tight text-ink [[data-skin=plate]_&]:riso-marker [[data-skin=plate]_&]:px-1 [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[24px] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal">
              {stepTitle}
            </h2>
            <span className="inline-flex shrink-0 items-center gap-1.5 text-[12.5px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">
              <AgentAvatar size="sm" id={node.agentId} />
              <span className="max-w-[140px] truncate">{node.agentName}</span>
            </span>
            {records.length > 1 && onSelectRecord ? (
              <select
                aria-label={t.tasks.selectRecord}
                value={node.id}
                onChange={(event) => onSelectRecord(node.stepId, event.target.value)}
                className="h-6 max-w-[148px] cursor-pointer rounded-stamp border border-hairline-strong bg-surface-control px-1.5 font-mono text-[11px] font-medium text-ink-700 outline-none hover:bg-surface-hover focus-visible:shadow-focus [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:font-bold"
              >
                {records.map((item) => (
                  <option key={item.id} value={item.id}>
                    {t.tasks.page.version(item.visit, item.attempt)} · {t.tasks.nodeStatuses[item.status]}
                  </option>
                ))}
              </select>
            ) : (
              <span
                title={t.tasks.visit(node.visit, node.attempt)}
                className="shrink-0 rounded-stamp border border-hairline bg-surface-subtle px-1.5 py-0.5 font-mono text-[11px] leading-4 font-medium text-ink-500 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-paper-tab [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-ink-700"
              >
                {t.tasks.page.version(node.visit, node.attempt)}
              </span>
            )}
            {current ? (
              <span className="shrink-0 rounded-stamp bg-cta-bg px-1.5 py-0.5 text-[11px] leading-4 font-semibold text-cta-fg [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-white">
                {t.tasks.currentNode}
              </span>
            ) : (
              <span className="shrink-0 text-[11.5px] font-medium text-ink-500 [[data-skin=plate]_&]:font-bold">{t.tasks.readonly}</span>
            )}
          </div>
          <div className="ml-auto flex shrink-0 flex-wrap items-center gap-2">
            <BackgroundTasksMark tasks={backgroundTasks} stopper={backgroundStopper} hint={t.background.nodeHint} />
            <TaskBadge status={node.status} />
            {/* Scoped Conversation | Materials segmented toggle inside the node pane header; Left/Right arrows switch tabs. */}
            <div
              role="tablist"
              aria-label={t.tasks.record}
              onKeyDown={(event) => {
                if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
                  event.preventDefault()
                  const next = tab === 'conversation' ? 'materials' : 'conversation'
                  setTab(next)
                  event.currentTarget.querySelector<HTMLButtonElement>(`[data-tab="${next}"]`)?.focus()
                }
              }}
              className={cn(
                segmentedClass,
                'inline-flex h-7 shrink-0 items-center gap-0.5 rounded-input border-hairline bg-surface-subtle p-0.5 [[data-skin=plate]_&]:h-8 [[data-skin=plate]_&]:gap-0 [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:p-0',
              )}
            >
              {(['conversation', 'materials'] as const).map((value) => {
                const active = tab === value
                return (
                  <button
                    key={value}
                    type="button"
                    role="tab"
                    data-tab={value}
                    aria-selected={active}
                    aria-controls={`task-${value}`}
                    tabIndex={active ? 0 : -1}
                    onClick={() => setTab(value)}
                    className={cn(
                      'inline-flex h-6 shrink-0 cursor-pointer items-center gap-1 rounded-[calc(var(--skin-radius-input)-2px)] px-2.5 text-[12px] whitespace-nowrap transition-colors outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:h-full [[data-skin=plate]_&]:rounded-none [[data-skin=plate]_&]:px-3 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:not-first:border-l-2 [[data-skin=plate]_&]:not-first:border-ink',
                      active
                        ? 'bg-surface-control font-semibold text-ink shadow-surface [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-none'
                        : 'font-medium text-ink-500 hover:text-ink [[data-skin=plate]_&]:text-ink-700',
                    )}
                  >
                    <span>{value === 'conversation' ? t.tasks.conversationTab : t.tasks.materials}</span>
                    {value === 'materials' && (
                      <span aria-hidden="true" className="font-mono text-[10.5px] text-ink-500 tabular-nums">
                        ({artifactCount})
                      </span>
                    )}
                  </button>
                )
              })}
            </div>
          </div>
        </div>

        {isHistory && (
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-hairline bg-surface-subtle/50 px-4 py-1.5 text-[12px] text-ink-700 sm:px-5">
            <span className="font-medium [[data-skin=plate]_&]:font-bold">{t.tasks.history}</span>
            {onSelectRecord && detail.task.currentStepId && detail.task.currentNodeRunId && (
              <Button variant="secondary" size="xs" className="h-6 px-2.5 text-[12px]" onClick={() => onSelectRecord(detail.task.currentStepId!, detail.task.currentNodeRunId!)}>
                {t.tasks.currentChanged}
              </Button>
            )}
          </div>
        )}

        <TaskLarkSwitchController key={node.id} detail={detail} node={node} disabled={disabled}>
          {(lark) => (
            <>
              {(hasActionRow || node.question) && (
                <div className="flex max-h-[38vh] min-h-0 flex-col gap-2 overflow-y-auto border-b border-hairline bg-surface-subtle/30 px-4 py-2.5 sm:px-5 [[data-skin=plate]_&]:border-b-2 [[data-skin=plate]_&]:border-ink">
                  {hasActionRow && (
                    <div className="flex flex-wrap items-start gap-x-4 gap-y-2">
                      {statusReason && (
                        <StickyNote
                          tone={node.status === 'blocked' ? 'red' : 'yellow'}
                          className="min-w-0 flex-[1_1_320px] items-start py-2 text-[13px] leading-[1.55] font-normal [&>svg]:mt-[3px]"
                        >
                          <strong className="block font-semibold [overflow-wrap:anywhere] [[data-skin=plate]_&]:font-black">{reasonHead}</strong>
                          {reasonRest.length > 0 && <span className="line-clamp-2 text-ink [overflow-wrap:anywhere]">{reasonRest.join('\n')}</span>}
                          <details className="mt-0.5">
                            <summary className="cursor-pointer font-semibold underline underline-offset-3 [[data-skin=plate]_&]:font-black">{t.tasks.details}</summary>
                            <p className="mt-1 whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{statusReason}</p>
                          </details>
                        </StickyNote>
                      )}
                      {(showRetryAction || showLarkAction) && (
                        <div className="flex flex-wrap items-center gap-2 pt-0.5">
                          <TaskActions task={detail.task} showRetry={current} showCancel={false} disabled={disabled} />
                          {lark.toggle}
                        </div>
                      )}
                    </div>
                  )}
                  {node.question && (
                    <details className="text-[12.5px]">
                      <summary className="cursor-pointer font-semibold [[data-skin=plate]_&]:font-bold">
                        {t.tasks.question} · {t.tasks.questionState[node.question.status]}
                      </summary>
                      <PendingQuestion question={node.question} />
                    </details>
                  )}
                </div>
              )}
              <div className="[&:has([data-lark-switch-panel]:not([hidden]))]:max-h-[42vh] [&:has([data-lark-switch-panel]:not([hidden]))]:overflow-y-auto [&:has([data-lark-switch-panel]:not([hidden]))]:border-b [&:has([data-lark-switch-panel]:not([hidden]))]:border-hairline [&:has([data-lark-switch-panel]:not([hidden]))]:px-4 [&:has([data-lark-switch-panel]:not([hidden]))]:py-2.5 sm:[&:has([data-lark-switch-panel]:not([hidden]))]:px-5">
                {lark.panel}
              </div>
            </>
          )}
        </TaskLarkSwitchController>
      </div>

      {/* Conversation panel */}
      <div id="task-conversation" role="tabpanel" aria-label={t.tasks.conversation} hidden={tab !== 'conversation'} className="flex min-h-0 flex-1 flex-col">
        <div className="relative min-h-[120px] flex-1 overflow-hidden">
          <div
            ref={conversation}
            tabIndex={0}
            onScroll={capture}
            aria-label={t.tasks.conversation}
            className="relative h-full min-w-0 overflow-y-auto overscroll-contain px-4 pt-5 pb-3 outline-none focus-visible:shadow-focus sm:px-6"
            data-conversation-scroll
          >
            {records.map((record) => (
              <MessageSegment
                key={record.id}
                detail={detail}
                node={record}
                requests={record.id === node.id ? permissionRequests : []}
                suppressInboxError={current && record.id === node.id}
                onMaterials={() => showMaterials(record.id)}
                onReply={current && record.id === node.id ? (message) => setReplying({ nodeRunId: node.id, messageId: message.id }) : undefined}
              />
            ))}
          </div>
          <ScrollRuler targetRef={conversation} label={t.tasks.conversation} />
        </div>
        {newMessages && (
          <Button
            variant="secondary"
            size="xs"
            className="mx-4 mb-2 shrink-0 self-center"
            onClick={() => {
              const el = conversation.current
              if (el) el.scrollTop = el.scrollHeight
              following.current = true
              setNewMessagesFor(null)
            }}
          >
            {t.tasks.newMessages}
          </Button>
        )}
        {/* Input area: streamlined composer bar without the redundant "To:" banner; target lives in placeholder + compact details toggle. */}
        <div
          className="flex max-h-[42dvh] min-h-0 shrink-0 flex-col gap-1.5 overflow-y-auto border-t border-hairline bg-surface-canvas px-4 py-2.5 sm:px-5 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-tint-yellow"
          data-node-draft
        >
          {current && unsupported && (
            <p role="status" className="text-[12.5px] font-semibold">
              {t.tasks.nodeMessagesUnsupported}
            </p>
          )}
          {current && !unsupported && <ErrorAlert error={capability.error ?? inbox.error} />}
          {current && capability.data && !capability.data.canSend && (
            <p role="status" className="text-[12.5px] font-semibold">
              {capability.data.reason} · {capability.data.handlingHint}
            </p>
          )}
          {!current && (draft.text || !!draft.images?.length) && (
            <p role="status" className="text-[12.5px] font-semibold">
              {t.tasks.draftChanged}
            </p>
          )}
          {permissionRequests.length > 0 && <WaitingForApproval requests={permissionRequests} />}
          <NodeMessageComposer
            key={node.id}
            taskId={detail.task.id}
            draft={draft}
            current={current}
            disabled={disabled || unsupported || !!capability.error || !!inbox.error || inbox.isPending || capability.isFetching}
            capability={capability.data}
            text={draft.text}
            onChange={onDraft}
            onImages={onImages}
            imageLimits={messageImageLimits(context.data?.limits)}
            onAccepted={onAccepted}
            onAction={onAction}
            placeholder={targetPlaceholder}
            details={targetDetailsToggle}
            queue={
              <>
                <QueueMessages notice={stop.notice} error={stop.error} />
                {current && !unsupported && (
                  <NodeQueueList
                    key={node.id}
                    taskId={detail.task.id}
                    nodeRunId={node.id}
                    agentId={node.agentId}
                    runtimeId={node.runtimeId}
                    disabled={disabled}
                    onEdit={refill}
                  />
                )}
              </>
            }
            stop={current && !unsupported && activeRunId ? stop.control(activeRunId, refreshMessages) : undefined}
            stopAlso={backgroundTasks.length}
            reply={current && replyTarget ? { message: replyTarget, name: node.agentName, maxBytes: context.data?.limits.maxInboxMessage, onCancel: () => setReplying(null), onDone: () => setReplying(null) } : undefined}
          />
          {draft.text && !current && (
            <Button size="xs" variant="secondary" className="self-start" onClick={() => void copyText(draft.text)}>
              {t.tasks.copyDraft}
            </Button>
          )}
          {oldDrafts.length > 0 && (
            <details className="text-[12.5px]">
              <summary className="cursor-pointer font-semibold">{t.tasks.draftChanged}</summary>
              {oldDrafts.map((d) => (
                <div key={d.nodeRunId} className="mt-2">
                  <p className="break-all">
                    {d.agentName} · {d.nodeRunId}
                  </p>
                  <pre className="whitespace-pre-wrap break-words">{d.text}</pre>
                  <div className="flex flex-wrap gap-2">
                    {d.images?.map((image) => (
                      <DraftImagePreview key={image.id} file={image.file} />
                    ))}
                  </div>
                  <Button size="xs" variant="secondary" onClick={() => void copyText(d.text)}>
                    {t.tasks.copyDraft}
                  </Button>
                </div>
              ))}
            </details>
          )}
        </div>
      </div>

      {/* Materials panel */}
      <div id="task-materials" role="tabpanel" aria-label={t.tasks.materials} hidden={tab !== 'materials'} className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
        <div ref={materials} tabIndex={0} className="relative h-full min-w-0 flex-1 overflow-y-auto overscroll-contain outline-none focus-visible:shadow-focus" data-materials-scroll>
          <div className="flex flex-col gap-4 px-4 pt-4 pb-8 sm:px-6">
            <p className="font-mono text-[12px] text-ink-500">
              {t.tasks.page.visits(records.length)} · {t.tasks.page.artifactCount(artifactCount)}
            </p>
            {materialRecords.map((record, index) => (
              <MaterialRecord
                key={record.id}
                detail={detail}
                record={record}
                viewing={record.id === node.id}
                divided={index > 0}
                open={activeGroups.open.has(record.id)}
                onToggle={(open) =>
                  setGroups(() => {
                    const nextOpen = new Set(activeGroups.open)
                    const nextClosed = new Set(activeGroups.closed)
                    if (open) {
                      nextOpen.add(record.id)
                      nextClosed.delete(record.id)
                    } else {
                      nextOpen.delete(record.id)
                      nextClosed.add(record.id)
                    }
                    return { nodeId: node.id, open: nextOpen, closed: nextClosed }
                  })
                }
                onConversation={() => backToConversation(record.id)}
              />
            ))}
          </div>
        </div>
        <ScrollRuler targetRef={materials} label={t.tasks.materials} />
      </div>
    </div>
  )
}

// One visit in the node materials: a "receipt". The top line has the version (V2·A1), which visit, status, start and end time;
// when opened: inputs (a key-value table, where values from other visits become links), output (JSON), and
// artifacts (file cards with a download button).
// A visit that was sent back (blocked but with output) gets a red warning on top. Visits not being viewed are collapsed by default.
function MaterialRecord({
  detail,
  record,
  viewing,
  divided,
  open,
  onToggle,
  onConversation,
}: {
  detail: Schemas['TaskDetail']
  record: Node
  viewing: boolean
  divided: boolean
  open: boolean
  onToggle: (open: boolean) => void
  onConversation: () => void
}) {
  const values = Object.entries(record.inputs)
  const versions = new Map(record.inputVersions.map((v) => [v.input, v.nodeRunId]))
  const keys = [...new Set([...values.map(([key]) => key), ...versions.keys()])]
  const source = (id: string) => {
    const from = detail.nodes.find((n) => n.id === id),
      index = detail.workflow.steps.findIndex((s) => s.id === from?.stepId)
    return { from, index }
  }
  return (
    <details
      data-material-record={record.id}
      open={open}
      onToggle={(event) => onToggle(event.currentTarget.open)}
      className={cn(
        'group min-w-0',
        divided && 'border-t border-hairline pt-4 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/40',
      )}
    >
      <summary className="flex cursor-pointer list-none flex-wrap items-center gap-x-3 gap-y-1.5 [&::-webkit-details-marker]:hidden">
        <span
          className={cn(
            'rounded-[4px] px-2 font-mono text-[11.5px] leading-[22px] font-semibold [[data-skin=plate]_&]:font-bold',
            viewing
              ? 'bg-cta-bg text-cta-fg [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:text-white'
              : 'border border-hairline bg-surface-subtle text-ink-700 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-paper-tab',
          )}
        >
          {t.tasks.page.version(record.visit, record.attempt)}
        </span>
        <span className="text-[14px] font-semibold [[data-skin=plate]_&]:text-[15px] [[data-skin=plate]_&]:font-black">
          {t.tasks.visit(record.visit, record.attempt)}
        </span>
        <TaskBadge status={record.status} />
        {record.startedAt && <span className="font-mono text-[12px] text-ink-500">{t.tasks.page.ran(record.startedAt, record.endedAt)}</span>}
        <span className="ml-auto text-[12.5px] font-medium text-ink-700 hover:text-ink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-riso-blue">
          {open ? t.tasks.page.collapse : t.tasks.page.expand}
          <span aria-hidden="true"> {open ? '▴' : '▾'}</span>
        </span>
      </summary>
      <div className="mt-3.5 flex flex-col gap-5">
        <div className="flex flex-wrap items-center justify-end gap-x-5 gap-y-2.5">
          {record.status === 'blocked' && record.output && (
            <p
              role="note"
              className="flex min-w-0 flex-[1_1_320px] items-center gap-2 rounded-ticket border border-red-text/25 bg-tint-red px-3.5 py-2 text-[13px] font-semibold text-red-text [[data-skin=plate]_&]:rounded-[4px] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:font-black"
            >
              <CircleAlertIcon aria-hidden="true" className="size-4 shrink-0" strokeWidth={2.4} />
              {t.tasks.inspectOnly}
            </p>
          )}
          <button
            type="button"
            className="shrink-0 cursor-pointer text-[12.5px] font-medium text-ink-700 underline underline-offset-3 hover:text-ink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-riso-blue"
            onClick={onConversation}
          >
            {t.tasks.page.backToConversation}
          </button>
        </div>
        {record.reason && record.status !== 'blocked' && <p className="text-[13.5px] [overflow-wrap:anywhere]">{record.reason}</p>}
        <div className="grid min-w-0 gap-x-7 gap-y-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
          <section className="min-w-0">
            <h3 className="riso-marker mb-2 inline-block text-[14px] leading-snug font-semibold [[data-skin=plate]_&]:px-1 [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[20px] [[data-skin=plate]_&]:leading-[1.3] [[data-skin=plate]_&]:font-normal">
              {t.tasks.page.inputs}
            </h3>
            {keys.length === 0 ? (
              <p className="text-[13.5px] text-ink-500">{t.tasks.none}</p>
            ) : (
              <dl className="grid grid-cols-[minmax(72px,auto)_minmax(0,1fr)] border-t border-hairline text-[13.5px] [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-ink">
                {keys.map((key) => {
                  const version = versions.get(key),
                    value = record.inputs[key],
                    origin = version ? source(version) : null
                  return (
                    <div key={key} className="contents">
                      <dt className="border-b border-hairline py-2.5 pr-4 pl-0.5 font-mono text-[12.5px] text-ink-700 [overflow-wrap:anywhere] [[data-skin=plate]_&]:border-b-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/25">
                        {key}
                      </dt>
                      <dd className="flex min-w-0 flex-col gap-1 border-b border-hairline py-2.5 [overflow-wrap:anywhere] [[data-skin=plate]_&]:border-b-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/25">
                        {version && (
                          <Link
                            className="font-semibold text-riso-blue underline underline-offset-3 [[data-skin=plate]_&]:font-black"
                            to={`?${new URLSearchParams({ step: origin?.from?.stepId ?? '', record: version })}`}
                          >
                            {origin?.from ? t.tasks.page.versionLink(stepNumber(origin.index), detail.workflow.steps[origin.index]?.title ?? '', origin.from.visit) : `${t.tasks.source(key)} · ${version}`}
                          </Link>
                        )}
                        {value !== undefined && (typeof value === 'string' ? <span className={cn(version && 'text-[12.5px] text-ink-700')}>{value}</span> : <code className="font-mono text-[12px] break-all">{JSON.stringify(value)}</code>)}
                      </dd>
                    </div>
                  )
                })}
              </dl>
            )}
          </section>
          <section className="min-w-0">
            <h3 className="riso-marker mb-2 inline-block text-[14px] leading-snug font-semibold [[data-skin=plate]_&]:px-1 [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[20px] [[data-skin=plate]_&]:leading-[1.3] [[data-skin=plate]_&]:font-normal">
              {t.tasks.output}
            </h3>
            {record.output === null ? (
              <p className="text-[13.5px] text-ink-500">{t.tasks.noOutput}</p>
            ) : (
              <pre className="max-w-full overflow-x-auto rounded-card border border-code-border bg-code-bg px-[18px] py-3.5 font-mono text-[13px] leading-[1.7] whitespace-pre-wrap break-all text-code-fg [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:text-ink">
                {JSON.stringify(record.output, null, 2)}
              </pre>
            )}
          </section>
        </div>
        <Artifacts artifacts={detail.artifacts.filter((a) => a.nodeRunId === record.id)} />
      </div>
    </details>
  )
}

// A question the node raised is answerable only while the task still says so; other messages to you defer to the server.
const answerableFor = (node: Node, message: Schemas['InboxMessage']) => (node.question?.messageId === message.id ? node.question.status === 'open' && node.question.canAnswer : true)

// onReply: only the current record's questions can switch the node composer into reply mode.
function MessageSegment({
  detail,
  node,
  onMaterials,
  suppressInboxError,
  onReply,
  requests,
}: {
  detail: Schemas['TaskDetail']
  node: Node
  onMaterials: () => void
  suppressInboxError: boolean
  onReply?: (message: Schemas['InboxMessage']) => void
  requests: Schemas['PermissionRequest'][]
}) {
  const messages = useTaskMessages(detail.task.id, node.id)
  const inbox = useTaskInboxMessages(detail.task.id, node.id, node.agentId, node.runtimeId)
  // Like normal chat, a message still waiting in the node's queue is shown only in the queue tray; it joins the flow once it starts.
  // Cancelled or not-executed messages have left the queue and keep showing their final status here.
  const queue = useTaskNodeQueue(detail.task.id, node.id, node.id === detail.task.currentNodeRunId)
  const queued = new Set(queue.data?.runtimeId === node.runtimeId ? queue.data.items.map((item) => item.messageId) : [])
  // A turn still queued is not in the flow yet, nor is one voided before it ran (its messages promoted, cancelled, or already read):
  // both its prompt and its empty reply are hidden. A queued turn joins the flow once it starts.
  const workRows = [
    ...new Map(
      messages.data?.retainedMessages.filter((m) => m.nodeRunId === node.id && !m.voidReason && m.status !== 'queued').map((m) => [m.id, m]),
    ).values(),
  ].sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.id.localeCompare(b.id))
  const rows = [
    ...workRows.map((message) => ({
      key: `task:${message.id}`,
      at: message.createdAt,
      body: (
        <TaskMessageBubble
          message={message}
          agent={{ id: node.agentId, name: node.agentName }}
          taskId={detail.task.id}
          nodeRunId={node.id}
          runtimeId={inbox.data && !nodeMessagesUnsupported(inbox.error) ? node.runtimeId : undefined}
          requests={message.role === 'assistant' ? requests.filter((request) => request.runId === message.runId) : []}
        />
      ),
    })),
    // An approval question is the request card in the reply that asked (and the run record keeps it), so it is not shown twice.
    ...(inbox.data?.items ?? [])
      .filter((record) => !queued.has(record.message.id) && record.message.question?.kind !== 'permission')
      .map((record) => {
        const extras = (
          <>
            {record.notExecuted && (
              <p role="status" className="rounded-input border border-hairline bg-tint-yellow p-2 text-sm [overflow-wrap:anywhere] [[data-skin=plate]_&]:border-0">
                {inboxCopy.notExecuted} · {inboxCopy.targetReasons[record.notExecuted.reason] ?? record.notExecuted.reason}
              </p>
            )}
            {record.runLinks.length > 0 && (
              <details className="text-xs [overflow-wrap:anywhere]">
                <summary>{inboxCopy.links}</summary>
                <ul>
                  {record.runLinks.map((link) => (
                    <li key={`${link.runId}:${link.role}`}>
                      {link.role === 'notification' ? inboxCopy.notification : link.role === 'read' ? inboxCopy.readLink : inboxCopy.replyLink} · {link.runId}
                    </li>
                  ))}
                </ul>
              </details>
            )}
          </>
        )
        // Messages this node's instance sent you show as "For you" (with options when it asked a question); the rest keep the Inbox bubble.
        const body =
          record.message.from.kind === 'agent' && record.message.to.kind === 'console' ? (
            <div data-inbox-message-id={record.message.id}>
              <ConsoleMessage
                message={record.message}
                agent={{ id: node.agentId, name: node.agentName }}
                answerable={answerableFor(node, record.message)}
                onReply={onReply}
                meta={<Badge tone="info">{record.message.inReplyTo ? inboxCopy.reply : inboxCopy.message}</Badge>}
              >
                {extras}
              </ConsoleMessage>
            </div>
          ) : (
            <TaskInboxMessageBubble message={record.message}>{extras}</TaskInboxMessageBubble>
          )
        return { key: `inbox:${record.message.id}`, at: record.message.createdAt, body }
      }),
  ].sort((a, b) => a.at.localeCompare(b.at) || a.key.localeCompare(b.key))
  const cutLabel = `${t.tasks.visit(node.visit, node.attempt)} · ${t.tasks.nodeStatuses[node.status]} · ${t.tasks.page.seeMaterials}`
  return (
    <section data-record={node.id} data-reading-anchor={`record:${node.id}`} className="mb-7 min-w-0">
      {/* Between visits of the same node is a scissors cut line; clicking the label opens this visit's node materials. */}
      <div className="mb-[18px]">
        <CutLine icon={ScissorsIcon} label={t.tasks.execution}>
          <button
            type="button"
            className="min-w-0 cursor-pointer text-center text-[12.5px] font-semibold text-ink underline decoration-dotted underline-offset-4 [overflow-wrap:anywhere] [[data-skin=plate]_&]:font-black"
            aria-label={`${t.tasks.execution} · ${cutLabel}`}
            onClick={onMaterials}
          >
            {cutLabel}
          </button>
        </CutLine>
      </div>
      <ErrorAlert error={messages.error} />
      {(messages.isPending || inbox.isPending) && <p>{t.common.loading}</p>}
      {messages.isSuccess && inbox.isSuccess && rows.length === 0 && <p>{t.tasks.noMessages}</p>}
      {!suppressInboxError && !nodeMessagesUnsupported(inbox.error) && <ErrorAlert error={inbox.error} />}
      <ol className="flex min-w-0 flex-col gap-7 pr-2 pb-2">
        {rows.map((row) => (
          <li key={row.key} data-message-id={row.key} data-reading-anchor={`message:${row.key}`} className="min-w-0">
            {row.body}
          </li>
        ))}
      </ol>
      {inbox.hasNextPage && (
        <div className="mt-4">
          <p className="text-sm">{t.tasks.incomplete}</p>
          <Button variant="secondary" disabled={inbox.isFetching} onClick={() => void inbox.fetchNextPage({ cancelRefetch: false })}>
            {t.tasks.more}
          </Button>
        </div>
      )}
      {messages.hasNextPage && (
        <div className="mt-4">
          <p className="text-sm">{t.tasks.incomplete}</p>
          <Button variant="secondary" disabled={messages.isFetching} onClick={() => void messages.fetchNextPage({ cancelRefetch: false })}>
            {t.tasks.more}
          </Button>
        </div>
      )}
    </section>
  )
}

// Artifacts: file cards with a download button (file icon, monospace file name, type and size).
export function Artifacts({ artifacts }: { artifacts: Schemas['TaskArtifact'][] }) {
  return (
    <section className="min-w-0">
      <h3 className="riso-marker mb-3 inline-block text-[14px] leading-snug font-semibold [[data-skin=plate]_&]:px-1 [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[20px] [[data-skin=plate]_&]:leading-[1.3] [[data-skin=plate]_&]:font-normal">
        {t.tasks.page.artifacts}
      </h3>
      {artifacts.length === 0 && <p className="text-[13.5px] text-ink-500">{t.tasks.noArtifacts}</p>}
      <ul className="grid gap-3.5 sm:grid-cols-[repeat(auto-fill,minmax(260px,1fr))]">
        {artifacts.map((a) => (
          <li
            key={a.id}
            className="mr-1 flex min-w-0 items-center gap-3 rounded-card border border-hairline bg-surface-control px-3.5 py-3 shadow-surface [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink)]"
          >
            <span
              aria-hidden="true"
              className="flex size-9 shrink-0 items-center justify-center rounded-[10px] border border-hairline-strong bg-surface-subtle [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow"
            >
              <FileTextIcon className="size-4" strokeWidth={2.4} />
            </span>
            <span className="flex min-w-0 flex-1 flex-col leading-[1.4]">
              <span id={`artifact-${a.id}`} className="font-mono text-[13.5px] font-bold break-all">
                {a.name}
              </span>
              <span className="text-[12px] text-ink-500">
                {a.contentType} · {t.common.bytes(a.size)}
              </span>
            </span>
            <a
              href={config.apiBase + artifactUrl(a)}
              aria-label={t.tasks.download}
              aria-describedby={`artifact-${a.id}`}
              title={t.tasks.download}
              className="flex size-9 shrink-0 items-center justify-center rounded-full border border-hairline-strong bg-surface-control text-ink outline-none hover:bg-surface-hover focus-visible:shadow-focus [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:hover:bg-riso-yellow"
            >
              <DownloadIcon aria-hidden="true" className="size-4" strokeWidth={2.4} />
            </a>
          </li>
        ))}
      </ul>
    </section>
  )
}
