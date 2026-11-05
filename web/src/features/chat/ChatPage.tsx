import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { stopAgentRun, useAgentQueue } from '@/api/agent-queue'
import { useAgentStatus, useContextInfo, useMessages } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { cn } from '@/lib/utils'
import { useAgentContext } from '@/features/agents/agent-context'
import { Composer, type ComposerHandle } from './Composer'
import { isBusy } from './live-state'
import { MessageList } from './MessageList'
import { conflictNotice, useStopReply } from './queue-actions'
import { QueueList, QueueMessages } from './QueueList'
import { runPhaseText } from './run-status-text'
import { SessionMissingBanner } from './SessionMissingBanner'
import { buildTurns } from './turns'
import { useAgentLive } from './useAgentLive'
import { messageImageLimits } from '@/api/message-images'
import { newerSleep } from '@/features/sleep/projection'
import { useAgentConsoleMessages } from '@/api/console-inbox'
import { ConsoleMessage } from '@/features/console-inbox/ConsoleMessage'
import { CommandLine } from '@/features/plugins/ChatLines'
import type { Schemas } from '@/api/client'

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

// 地址中的定位锚点 #run-<执行 ID>；没有锚点时为 null。
function runFromHash(hash: string): string | null {
  return hash.startsWith('#run-') ? decodeURIComponent(hash.slice('#run-'.length)) : null
}

export function ChatPage() {
  const { agent, reportLiveStatus } = useAgentContext()
  const location = useLocation()
  const navigate = useNavigate()
  const { live, recordSent, recordQueueAction } = useAgentLive(agent.id)
  const composer = useRef<ComposerHandle>(null)
  const stop = useStopReply((runId, actionId) => stopAgentRun(agent.id, runId, actionId), (error) => conflictNotice(error, 'stop'))
  // 从概览定位到某条回复：地址带 #run-<执行 ID> 时，第一页以这条回复为中心。找不到时（不属于这个 Agent、
  // 不存在或格式不对）提示后显示最新的对话。
  const hashRun = runFromHash(location.hash)
  const [missingRun, setMissingRun] = useState<string | null>(null)
  const aroundRun = hashRun !== null && hashRun !== missingRun && UUID.test(hashRun) ? hashRun : null
  const messages = useMessages(agent.id, aroundRun)
  if (aroundRun !== null && messages.isError && isNotFound(messages.error)) setMissingRun(aroundRun)
  const notFound = hashRun !== null && aroundRun === null
  const status = useAgentStatus(agent.id)
  const context = useContextInfo()
  // 定位时历史还没有加载到最新，新消息只提示，不插进对话流。
  const atLatest = !messages.data?.pages[0]?.hasMoreAfter
  // 对话流与输入框上方的排队列表共用同一份对话轮次。
  const {
    turns,
    queued: queuedTurns,
    hiddenNew,
  } = useMemo(() => buildTurns(messages.data, live, atLatest), [messages.data, live, atLatest])
  const backToLatest = () => void navigate(location.pathname, { replace: true })

  // Agent 发给你的消息与问题按时间排进对话；你发给它的由它的平台提醒一行显示，不重复。
  // 回复问题时记下原题，原题不再待回答（答过、到期或关闭）时输入框退出回复模式。
  const consoleMessages = useAgentConsoleMessages(agent.id)
  const toYou = useMemo(() => (consoleMessages.data ?? []).filter((m) => m.from.kind === 'agent' && m.to.kind === 'console'), [consoleMessages.data])
  const [replying, setReplying] = useState<{ agentId: string; messageId: string } | null>(null)
  const replyTarget = replying?.agentId === agent.id ? toYou.find((m) => m.id === replying.messageId && m.question?.state === 'open') : undefined
  // 被平台或插件认领的 / 命令只在这次打开的对话里按时间排进去（服务端不留命令消息），换 Agent 时清空。
  const [commands, setCommands] = useState<{ agentId: string; id: number; at: string; command: string; result: Schemas['ConsoleCommandResult'] }[]>([])
  const inserts = [
    ...toYou.map((message) => ({
      key: `console:${message.id}`,
      at: message.createdAt,
      node: <ConsoleMessage message={message} agent={agent} onReply={(m) => setReplying({ agentId: agent.id, messageId: m.id })} />,
    })),
    ...commands
      .filter((item) => item.agentId === agent.id)
      .map((item) => ({ key: `command:${item.id}`, at: item.at, node: <CommandLine command={item.command} result={item.result} at={item.at} onResume={(sessionId) => composer.current?.resume(sessionId)} /> })),
  ]

  // 收到快照之前，忙闲与会话状态取自状态接口与 Agent 详情。
  const synced = live.snapshots > 0
  const busy = synced ? isBusy(live) : (status.data?.busy ?? agent.busy)
  // 逐条排队列表：收到快照之前取自排队接口，之后以快照与 queue.updated 为准。
  const initialQueue = useAgentQueue(agent.id, !synced)
  const queueItems = synced ? live.queueItems : initialQueue.data?.agentId === agent.id ? initialQueue.data.items : []
  const currentRunId = synced ? live.current?.run.id : status.data?.currentRun?.id
  const session = live.session ?? status.data?.session ?? agent.session
  const sessionMissing = session.state === 'missing'
  const hasRunInProgress = synced ? live.current !== null : status.data?.currentRun != null
  const phase = live.current ? runPhaseText(live.current.run) : null
  const sleep = newerSleep(live, status.data ?? { sleep: null, sleepRevision: 0 }).sleep

  // 收到快照后把实时状态交给头部贴纸；离开对话页时清空，头部回到轮询的状态。
  useEffect(() => {
    if (synced) reportLiveStatus({ sessionMissing, busy, phase, sleep })
  }, [synced, sessionMissing, busy, phase, sleep, reportLiveStatus])
  useEffect(() => () => reportLiveStatus(null), [reportLiveStatus])

  const connection = live.connection === 'closed' ? 'reconnecting' : live.connection
  return (
    // 窄屏时对话部分给定高度，随整页一起滚动；对话流仍在自己的区域内滚动（加载更早的消息、贴底都依赖它）。
    <div className="flex h-full min-h-0 flex-col px-6 lg:overflow-hidden">
      <div className="flex h-[clamp(28rem,80dvh,48rem)] min-w-0 shrink-0 flex-col lg:h-auto lg:min-h-0 lg:flex-1 lg:shrink">
        {sessionMissing && <SessionMissingBanner agentId={agent.id} hasRunInProgress={hasRunInProgress} />}
        {notFound && (
          <Alert variant="warning" className="mt-4">
            <AlertDescription>{t.timeline.locate.notFound}</AlertDescription>
          </Alert>
        )}
        {aroundRun !== null && (
          <div className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-ticket bg-tint-blue px-4 py-2 text-[13.5px] font-bold">
            <span>{t.timeline.locate.viewing}</span>
            <span role="status" aria-live="polite">
              {hiddenNew > 0 && <Badge tone="busy">{t.timeline.locate.newMessages}</Badge>}
            </span>
            <Button size="xs" variant="ink" className="ml-auto" onClick={backToLatest}>
              {t.timeline.locate.backToLatest}
            </Button>
          </div>
        )}
        <div className="min-h-0 flex-1">
          {messages.isError && !(aroundRun !== null && isNotFound(messages.error)) ? (
            <ErrorAlert error={messages.error} className="mt-6 w-auto" />
          ) : (
            <MessageList
              // 定位与常规模式各用一份滚动状态，切换时重新开始。
              key={aroundRun ?? 'latest'}
              agent={agent}
              turns={turns}
              hasQueued={queuedTurns.length > 0 || queueItems.length > 0}
              hasOlder={messages.hasNextPage}
              loadingOlder={messages.isFetchingNextPage}
              // 整页重新获取（快照要求的对账、窗口聚焦刷新）进行中时不加载更早的消息，也不取消这次获取。
              refreshing={messages.isRefetching}
              onLoadOlder={() => void messages.fetchNextPage({ cancelRefetch: false })}
              targetRunId={aroundRun}
              inserts={inserts}
              hasNewer={messages.hasPreviousPage}
              loadingNewer={messages.isFetchingPreviousPage}
              onLoadNewer={() => void messages.fetchPreviousPage({ cancelRefetch: false })}
            />
          )}
        </div>
        <Composer
          ref={composer}
          agentId={agent.id}
          agentName={agent.name}
          busy={busy}
          disabled={sessionMissing}
          allowResumeWhenDisabled={sessionMissing}
          maxMessageBytes={context.data?.limits.maxMessage}
          imageLimits={messageImageLimits(context.data?.limits)}
          onSent={recordSent}
          onCommand={(result, command) => setCommands((current) => [...current, { agentId: agent.id, id: current.length + 1, at: new Date().toISOString(), command, result }])}
          reply={replyTarget && { message: replyTarget, name: agent.name, maxBytes: context.data?.limits.maxInboxMessage, onCancel: () => setReplying(null), onDone: () => setReplying(null) }}
          queue={
            <>
              <QueueMessages notice={stop.notice} error={stop.error} />
              <QueueList
                key={agent.id}
                agentId={agent.id}
                items={queueItems}
                queueSeq={live.queueSeq}
                onReceipt={recordQueueAction}
                onEdit={(text, images) => composer.current?.refill(text, images)}
              />
            </>
          }
          stop={currentRunId ? stop.control(currentRunId, (receipt) => recordQueueAction(receipt, live.queueSeq)) : undefined}
          status={
            // 忙碌提示：荧光粉圆点加藏青描边，没有底色。
            busy && (
              <span role="status" className="inline-flex items-center gap-2 text-[13.5px] font-bold">
                <span aria-hidden="true" className="size-2.5 shrink-0 rounded-full bg-riso-pink shadow-[0_0_0_2px_var(--ink)]" />
                {t.chat.busyBar(phase ?? t.chat.status.running)}
              </span>
            )
          }
          connection={
            <span
              role="status"
              aria-live="polite"
              className={cn(
                'inline-flex items-center gap-1.5 text-[12.5px] font-bold',
                connection === 'open' ? 'text-green-text' : connection === 'reconnecting' ? 'text-red-text' : 'text-ink-500',
              )}
            >
              <span
                aria-hidden="true"
                className={cn('size-2 shrink-0 rounded-full', connection === 'open' ? 'bg-green' : connection === 'reconnecting' ? 'bg-red' : 'bg-ink-500')}
              />
              {t.chat.connection[connection]}
            </span>
          }
        />
      </div>
    </div>
  )
}
