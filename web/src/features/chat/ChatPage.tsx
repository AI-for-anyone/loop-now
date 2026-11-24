import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { stopAgentRun, useAgentQueue } from '@/api/agent-queue'
import { useAgentStatus, useContextInfo, useMessages, useModels, useRunTimeline } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { cn } from '@/lib/utils'
import { useAgentContext } from '@/features/agents/agent-context'
import { CompactionNote } from './CompactionNote'
import { Composer, type ComposerHandle } from './Composer'
import { isBusy } from './live-state'
import { MessageList } from './MessageList'
import { conflictNotice, useStopReply } from './queue-actions'
import { QueueList, QueueMessages } from './QueueList'
import { RunnerChip } from './RunnerChip'
import { runPhaseText } from './run-status-text'
import { runStartedAt } from './time'
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

// The locating anchor #run-<run ID> in the address; null when there is no anchor.
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
  // While locating, the history isn't loaded up to the latest and later turns aren't in the conversation flow, so use the total from
  // the API (pages are fetched at different times; take the largest);
  // turns that start in the meantime only show a new-messages notice and are not added to the total. Some of them are already
  // counted in the API's total and there's no telling which, so adding them would overcount.
  const turnsTotal =
    turnsBefore === undefined ? undefined : atLatest ? turnsBefore + turns.length : Math.max(...messages.data!.pages.map((page) => page.turnsTotal))

  // The located run isn't in the fetched page: it may be a manual compaction (/compact), which has no messages and is not a turn
  // (contract Anomaly.compaction).
  // In that case fetch the run through the run record API; if it is a manual compaction, show a note where it started.
  const locatedMissing = aroundRun !== null && messages.isSuccess && !turns.some((turn) => turn.run.id === aroundRun)
  const locatedRun = useRunTimeline(agent.id, aroundRun ?? '', locatedMissing, true)
  const compactionRun = locatedMissing && locatedRun.data?.run.compaction ? locatedRun.data.run : null

  // Messages and questions the Agent sent you are placed into the conversation by time; the ones you sent it show up as its platform
  // reminder line and aren't repeated.
  // When replying to a question, remember the original question; once it no longer awaits an answer (answered, expired or closed),
  // the input box leaves reply mode.
  const consoleMessages = useAgentConsoleMessages(agent.id)
  const toYou = useMemo(() => (consoleMessages.data ?? []).filter((m) => m.from.kind === 'agent' && m.to.kind === 'console'), [consoleMessages.data])
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
      node: <ConsoleMessage message={message} agent={agent} onReply={(m) => setReplying({ agentId: agent.id, messageId: m.id })} />,
    })),
    ...commands
      .filter((item) => item.agentId === agent.id)
      .map((item) => ({ key: `command:${item.id}`, at: item.at, node: <CommandLine command={item.command} result={item.result} at={item.at} copyFailed={item.copyFailed} onResume={(sessionId) => composer.current?.resume(sessionId)} /> })),
  ]

  // Before a snapshot arrives, busy/idle and session state come from the status API and the Agent details.
  const synced = live.snapshots > 0
  const busy = synced ? isBusy(live) : (status.data?.busy ?? agent.busy)
  // Per-item queue list: before a snapshot arrives it comes from the queue API; after that, snapshots and queue.updated are authoritative.
  const initialQueue = useAgentQueue(agent.id, !synced)
  const queueItems = synced ? live.queueItems : initialQueue.data?.agentId === agent.id ? initialQueue.data.items : []
  const currentRunId = synced ? live.current?.run.id : status.data?.currentRun?.id
  const session = live.session ?? status.data?.session ?? agent.session
  // The model the Agent runs takes no images (a local ACP command's default model, a text-only model): the message box takes none. Unknown
  // while its provider's models are being read, and then images are offered as before.
  const runModel = useModels(agent.model.provider).data?.models.find((model) => model.id === agent.model.modelId)
  const imagesRefused = runModel !== undefined && !runModel.input.includes('image')
  const sessionMissing = session.state === 'missing'
  const hasRunInProgress = synced ? live.current !== null : status.data?.currentRun != null
  const phase = live.current ? runPhaseText(live.current.run) : null
  const sleep = newerSleep(live, status.data ?? { sleep: null, sleepRevision: 0 }).sleep

  // Once a snapshot has arrived, hand the live status to the header sticker; clear it when leaving the chat page so the header goes back
  // to the polled status.
  useEffect(() => {
    if (synced) reportLiveStatus({ sessionMissing, busy, phase, sleep })
  }, [synced, sessionMissing, busy, phase, sleep, reportLiveStatus])
  useEffect(() => () => reportLiveStatus(null), [reportLiveStatus])

  const connection = live.connection === 'closed' ? 'reconnecting' : live.connection
  return (
    // The whole page sits in the sheet: above is the message scroll area (with the ruler in the stub on the right), below is the input
    // area merged into the bottom of the sheet.
    // On narrow screens the conversation part gets a fixed height and scrolls with the whole page; the conversation flow still scrolls
    // inside its own area (loading earlier messages and sticking to the bottom both depend on it).
    <div className="flex h-full min-h-0 flex-col lg:overflow-hidden">
      <div className="flex h-[clamp(28rem,80dvh,48rem)] min-w-0 shrink-0 flex-col lg:h-auto lg:min-h-0 lg:flex-1 lg:shrink">
        {/* Notice bars sit above the message area, with the same left and right padding as the message area. */}
        <div className="flex shrink-0 flex-col px-[clamp(12px,2vw,28px)]">
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
        </div>
        <div className="min-h-0 flex-1">
          {messages.isError && !(aroundRun !== null && isNotFound(messages.error)) ? (
            <ErrorAlert error={messages.error} className="mx-[clamp(12px,2vw,28px)] mt-6 w-auto" />
          ) : (
            <MessageList
              // Locating mode and normal mode each have their own scroll state, which starts over when switching.
              key={aroundRun ?? 'latest'}
              agent={agent}
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
          disabled={sessionMissing}
          allowResumeWhenDisabled={sessionMissing}
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
                onReceipt={recordQueueAction}
                onEdit={(text, images) => composer.current?.refill(text, images)}
              />
            </>
          }
          runner={<RunnerChip agent={agent} />}
          stop={currentRunId ? stop.control(currentRunId, (receipt) => recordQueueAction(receipt, live.queueSeq)) : undefined}
          status={
            // Busy hint: a fluorescent pink dot with a navy outline, no background fill.
            busy && (
              <span role="status" className="inline-flex items-center gap-2 text-[12.5px] font-bold text-ink">
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
                'inline-flex items-center gap-1.5 text-[12px] font-black',
                connection === 'open' ? 'text-green-text' : connection === 'reconnecting' ? 'text-red-text' : 'text-ink-500',
              )}
            >
              <span
                aria-hidden="true"
                className={cn('size-[7px] shrink-0 rounded-full', connection === 'open' ? 'bg-green' : connection === 'reconnecting' ? 'bg-red' : 'bg-ink-500')}
              />
              {t.chat.connection[connection]}
            </span>
          }
        />
      </div>
    </div>
  )
}
