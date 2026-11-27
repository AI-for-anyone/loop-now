import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { stopAgentRun, useAgentQueue } from '@/api/agent-queue'
import { agentFolderContentURL, folderRoots, useAgentFolderLocation } from '@/api/folders'
import { useAgentStatus, useContextInfo, useMessages, useModels, useRunTimeline } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { agentPath } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { useAgentContext } from '@/features/agents/agent-context'
import { useScope } from '@/features/projects/scope-context'
import { CompactionNote } from './CompactionNote'
import { Composer, type ComposerHandle } from './Composer'
import { isBusy } from './live-state'
import { DEFAULT_IMAGE_PREVIEW_MAX_BYTES, type ReplyFolderContext } from './local-images'
import { MessageList } from './MessageList'
import { conflictNotice, useStopReply } from './queue-actions'
import { QueueList, QueueMessages } from './QueueList'
import { RunnerChip } from './RunnerChip'
import { runPhaseText } from './run-status-text'
import { runStartedAt } from './time'
import { buildTurns } from './turns'
import { useAgentLive } from './useAgentLive'
import { messageImageLimits } from '@/api/message-images'
import { newerSleep } from '@/features/sleep/projection'
import { useAgentConsoleMessages } from '@/api/console-inbox'
import { ConsoleMessage } from '@/features/console-inbox/ConsoleMessage'
import { CommandLine } from '@/features/plugins/ChatLines'
import { useRuntimeLookup } from '@/lib/runtime'
import { BackgroundTasksMark, RestartNotice, type BackgroundStopper } from './BackgroundTasks'
import { stopAllBackgroundTasks, stopBackgroundTask } from '@/api/background-tasks'
import { WaitingForApproval } from '@/features/permissions/WaitingForApproval'
import type { Schemas } from '@/api/client'

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

// The locating anchor #run-<run ID> in the address; null when there is no anchor.
function runFromHash(hash: string): string | null {
  return hash.startsWith('#run-') ? decodeURIComponent(hash.slice('#run-'.length)) : null
}

export function ChatPage() {
  const { agent, reportLiveStatus } = useAgentContext()
  const scope = useScope()
  const location = useLocation()
  const navigate = useNavigate()
  const { live, recordSent, recordQueueAction } = useAgentLive(agent.id)
  const composer = useRef<ComposerHandle>(null)
  const stop = useStopReply((runId, actionId) => stopAgentRun(agent.id, runId, actionId), (error) => conflictNotice(error, 'stop'))
  // Locating a reply from the overview: when the address has #run-<run ID>, the first page is centered on that reply. When it can't be
  // found (it doesn't belong to this Agent, doesn't exist or is malformed), show a notice and then the latest conversation.
  const hashRun = runFromHash(location.hash)
  const [missingRun, setMissingRun] = useState<string | null>(null)
  const aroundRun = hashRun !== null && hashRun !== missingRun && UUID.test(hashRun) ? hashRun : null
  const messages = useMessages(agent.id, aroundRun)
  if (aroundRun !== null && messages.isError && isNotFound(messages.error)) setMissingRun(aroundRun)
  const notFound = hashRun !== null && aroundRun === null
  const status = useAgentStatus(agent.id)
  const context = useContextInfo()
  const folderLocation = useAgentFolderLocation(agent.id)
  const maxImageBytes = context.data?.limits.textEdit.maxBytes ?? DEFAULT_IMAGE_PREVIEW_MAX_BYTES
  const folderTabPath = agentPath(scope, agent.id, 'folder')
  // Stable reference so memo(Markdown) skips completed turns while a reply streams.
  const replyFolder = useMemo<ReplyFolderContext>(
    () => ({
      roots: folderRoots(folderLocation.data),
      contentURL: (path) => agentFolderContentURL(agent.id, path),
      folderLink: (path) => `${folderTabPath}?${new URLSearchParams({ path })}`,
      maxBytes: maxImageBytes,
    }),
    [folderLocation.data, agent.id, folderTabPath, maxImageBytes],
  )
  // While locating, the history isn't loaded up to the latest yet, so new messages are only announced, not inserted into the
  // conversation flow.
  const atLatest = !messages.data?.pages[0]?.hasMoreAfter
  // The conversation flow and the queue list above the input box share the same conversation turns.
  const {
    turns,
    queued: queuedTurns,
    hiddenNew,
  } = useMemo(() => buildTurns(messages.data, live, atLatest), [messages.data, live, atLatest])
  const backToLatest = () => void navigate(location.pathname, { replace: true })
  // Turn numbering (contract MessagePage): how many turns come before the earliest loaded page (the last of pages); the turns are
  // numbered on from there. Unknown while the history hasn't been fetched yet.
  const turnsBefore = messages.data?.pages.at(-1)?.turnsBefore
  // Total turn count at the bottom of the ruler. When loaded up to the latest, use the number of loaded turns: newly sent messages and
  // newly arrived replies keep counting locally without waiting for a refetch.
  const turnsTotal =
    turnsBefore === undefined ? undefined : atLatest ? turnsBefore + turns.length : Math.max(...messages.data!.pages.map((page) => page.turnsTotal))

  // The located run isn't in the fetched page: it may be a manual compaction (/compact), which has no messages and is not a turn
  // (contract Anomaly.compaction).
  const locatedMissing = aroundRun !== null && messages.isSuccess && !turns.some((turn) => turn.run.id === aroundRun)
  const locatedRun = useRunTimeline(agent.id, aroundRun ?? '', locatedMissing, true)
  const compactionRun = locatedMissing && locatedRun.data?.run.compaction ? locatedRun.data.run : null

  // Messages and questions the Agent sent you are placed into the conversation by time; the ones you sent it show up as its platform
  // reminder line and aren't repeated.
  const consoleMessages = useAgentConsoleMessages(agent.id)
  // An approval question (kind permission) is the request card in the reply that asked, so it is not shown a second time here.
  const toYou = useMemo(() => (consoleMessages.data ?? []).filter((m) => m.from.kind === 'agent' && m.to.kind === 'console' && m.question?.kind !== 'permission'), [consoleMessages.data])
  const [replying, setReplying] = useState<{ agentId: string; messageId: string } | null>(null)
  const replyTarget = replying?.agentId === agent.id ? toYou.find((m) => m.id === replying.messageId && m.question?.state === 'open') : undefined
  // Slash (/) commands claimed by the platform or a plugin are placed by time only in the conversation as opened this time
  // (the server keeps no command messages), and are cleared when switching Agents.
  const [commands, setCommands] = useState<{ agentId: string; id: number; at: string; command: string; result: Schemas['ConsoleCommandResult']; copyFailed: boolean }[]>([])
  const inserts = [
    ...(compactionRun ? [{ key: `compaction:${compactionRun.id}`, at: runStartedAt(compactionRun), node: <CompactionNote agentId={agent.id} run={compactionRun} /> }] : []),
    ...toYou.map((message) => ({
      key: `console:${message.id}`,
      at: message.createdAt,
      node: <ConsoleMessage message={message} agent={agent} folder={replyFolder} onReply={(m) => setReplying({ agentId: agent.id, messageId: m.id })} />,
    })),
    ...commands
      .filter((item) => item.agentId === agent.id)
      .map((item) => ({ key: `command:${item.id}`, at: item.at, node: <CommandLine command={item.command} result={item.result} at={item.at} copyFailed={item.copyFailed} onResume={(sessionId) => composer.current?.resume(sessionId)} /> })),
  ]

  // Before a snapshot arrives, busy/idle comes from the status API and the Agent details.
  const synced = live.snapshots > 0
  const busy = synced ? isBusy(live) : (status.data?.busy ?? agent.busy)
  // Per-item queue list: before a snapshot arrives it comes from the queue API; after that, snapshots and queue.updated are authoritative.
  const initialQueue = useAgentQueue(agent.id, !synced)
  const queueItems = synced ? live.queueItems : initialQueue.data?.agentId === agent.id ? initialQueue.data.items : []
  const currentRunId = synced ? live.current?.run.id : status.data?.currentRun?.id
  // The model the Agent runs takes no images (a local ACP command's default model, a text-only model): the message box takes none.
  const runModel = useModels(agent.model.provider).data?.models.find((model) => model.id === agent.model.modelId)
  const imagesRefused = runModel !== undefined && !runModel.input.includes('image')
  const phase = live.current ? runPhaseText(live.current.run) : null
  const runtimes = useRuntimeLookup()
  const runningTasks = synced ? live.backgroundTasks : (status.data?.backgroundTasks ?? [])
  const running = runningTasks.length
  const restartNotice = running > 0 && status.data?.restartOnNextMessage ? <RestartNotice runtimeName={runtimes.name(runningTasks[0]?.runtime ?? agent.runtime)} count={running} /> : null
  // Requests waiting for an approval: as cards in the replies that asked, and one line above the message box that leads to the first.
  const permissionRequests = (synced ? live.permissionRequests : (status.data?.permissionRequests ?? [])).filter((request) => request.taskId === null)
  const replyRuntime = (synced ? live.current?.run : status.data?.currentRun)?.selection?.runtime ?? agent.runtime
  const stopAlso = runtimes.get(replyRuntime)?.capabilities.stopEndsBackgroundTasks ? running : 0
  const sleep = newerSleep(live, status.data ?? { sleep: null, sleepRevision: 0 }).sleep

  // The numbers of the loaded replies that started a running background task, so the list under the message box can name them.
  const numbered =
    turnsBefore === undefined
      ? ''
      : turns.flatMap((turn, index) => (runningTasks.some((task) => task.runId === turn.run.id) ? [`${turn.run.id}=${turnsBefore + index + 1}`] : [])).join(',')
  const replyNumbers = useMemo(() => Object.fromEntries(numbered ? numbered.split(',').map((pair) => { const [runId, number] = pair.split('='); return [runId, Number(number)] }) : []), [numbered])
  const background = (
    <BackgroundTasksMark
      tasks={runningTasks}
      stopper={backgroundStopper(agent.id)}
      // The reply that started a task: located in the conversation, named by its number when it is loaded, otherwise by its time.
      origin={(task) => {
        const number = replyNumbers[task.runId]
        return { to: `${location.pathname}#run-${encodeURIComponent(task.runId)}`, label: number ? t.background.fromReply(number) : t.background.fromReplyAt(t.chat.time(task.startedAt)) }
      }}
    />
  )

  // Once a snapshot has arrived, hand the live status to the header sticker; clear it when leaving the chat page so the header goes back
  // to the polled status.
  useEffect(() => {
    if (synced) reportLiveStatus({ busy, phase, sleep })
  }, [synced, busy, phase, sleep, reportLiveStatus])
  useEffect(() => () => reportLiveStatus(null), [reportLiveStatus])

  const connection = live.connection === 'closed' ? 'reconnecting' : live.connection
  return (
    // The whole page sits in the sheet: above is the centered 960px message stream (with the floating PrecisionRuler overlay on the right),
    // below is the floating unified composer card.
    // On narrow screens (below nav: 901px) the conversation gets a fixed viewport height and scrolls with the whole page;
    // at nav: and wider it locks cleanly to the viewport height matching AppLayout's nav:h-screen.
    <div className="flex h-full min-h-0 flex-col nav:overflow-hidden">
      <div className="flex h-[clamp(28rem,80dvh,48rem)] min-w-0 shrink-0 flex-col nav:h-auto nav:min-h-0 nav:flex-1 nav:shrink">
        {/* Notice bars sit above the message area, centered with the conversation column. */}
        <div className="mx-auto flex w-full max-w-[960px] shrink-0 flex-col px-4 sm:px-6 empty:hidden">
          {notFound && (
            <Alert variant="warning" className="mt-4">
              <AlertDescription>{t.timeline.locate.notFound}</AlertDescription>
            </Alert>
          )}
          {aroundRun !== null && (
            <div className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-ticket border border-hairline bg-surface-subtle px-4 py-2 text-[14px] font-semibold">
              <span>{t.timeline.locate.viewing}</span>
              <span role="status" aria-live="polite">
                {hiddenNew > 0 && <Badge tone="busy">{t.timeline.locate.newMessages}</Badge>}
              </span>
              <Button size="xs" variant="ink" className="ml-auto" onClick={backToLatest}>
                {t.timeline.locate.backToLatest}
              </Button>
            </div>
          )}
        </div>
        <div className="flex min-h-0 flex-1 flex-col">
          {messages.isError && !(aroundRun !== null && isNotFound(messages.error)) ? (
            <ErrorAlert error={messages.error} className="mx-auto mt-6 w-full max-w-[960px] px-4 sm:px-6" />
          ) : (
            <MessageList
              // Locating mode and normal mode each have their own scroll state, which starts over when switching.
              key={`${agent.id}:${aroundRun ?? 'latest'}`}
              agent={agent}
              folder={replyFolder}
              turns={turns}
              hasQueued={queuedTurns.length > 0 || queueItems.length > 0}
              hasOlder={messages.hasNextPage}
              loadingOlder={messages.isFetchingNextPage}
              // While a full refetch is in progress (reconciliation requested by a snapshot, refresh on window focus), don't load earlier
              // messages, and don't cancel that refetch either.
              refreshing={messages.isRefetching}
              onLoadOlder={() => void messages.fetchNextPage({ cancelRefetch: false })}
              targetRunId={aroundRun}
              inserts={inserts}
              permissionRequests={permissionRequests}
              turnsBefore={turnsBefore}
              turnsTotal={turnsTotal}
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
          disabled={false}
          maxMessageBytes={context.data?.limits.maxMessage}
          imageLimits={messageImageLimits(context.data?.limits)}
          imagesRefused={imagesRefused}
          onSent={recordSent}
          onCommand={(result, command, outcome) => setCommands((current) => [...current, { agentId: agent.id, id: current.length + 1, at: new Date().toISOString(), command, result, copyFailed: outcome?.copyFailed ?? false }])}
          reply={replyTarget && { message: replyTarget, name: agent.name, maxBytes: context.data?.limits.maxInboxMessage, onCancel: () => setReplying(null), onDone: () => setReplying(null) }}
          queue={
            <>
              <QueueMessages notice={stop.notice} error={stop.error} />
              <QueueList
                key={agent.id}
                agentId={agent.id}
                items={queueItems}
                queueSeq={live.queueSeq}
                collapsible
                onReceipt={recordQueueAction}
                onEdit={(text, images) => composer.current?.refill(text, images)}
              />
            </>
          }
          runner={<RunnerChip agent={agent} />}
          background={background}
          notice={
            <>
              {permissionRequests.length > 0 && <WaitingForApproval requests={permissionRequests} onLocate={(runId) => void navigate(`${location.pathname}#run-${encodeURIComponent(runId)}`)} />}
              {restartNotice}
            </>
          }
          stopAlso={stopAlso}
          stop={currentRunId ? stop.control(currentRunId, (receipt) => recordQueueAction(receipt, live.queueSeq)) : undefined}
          status={
            // Visually quiet (header status + composer placeholder + stop button already convey busy state); kept accessible via role="status".
            busy && (
              <span role="status" className="sr-only">
                {t.chat.busyBar(phase ?? t.chat.status.running)}
              </span>
            )
          }
          connection={
            // Surface only degraded connection states (connecting / reconnecting); omit permanent "Live" noise when healthy.
            connection !== 'open' ? (
              <span
                role="status"
                aria-live="polite"
                className={cn(
                  'inline-flex items-center gap-1.5 text-[11.5px] font-semibold',
                  connection === 'reconnecting' ? 'text-red-text' : 'text-ink-500',
                )}
              >
                <span
                  aria-hidden="true"
                  className={cn('size-1.5 shrink-0 rounded-full', connection === 'reconnecting' ? 'bg-red' : 'bg-ink-500')}
                />
                {t.chat.connection[connection]}
              </span>
            ) : undefined
          }
        />
      </div>
    </div>
  )
}

const backgroundStopper = (agentId: string): BackgroundStopper => ({
  stop: (taskId, actionId) => stopBackgroundTask(agentId, taskId, actionId),
  stopAll: (actionId) => stopAllBackgroundTasks(agentId, actionId),
})
