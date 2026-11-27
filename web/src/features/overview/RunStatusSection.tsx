import { Link } from 'react-router'
import { ArrowRightIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { AgentAvatar } from '@/components/AgentAvatar'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { BackgroundCountTag } from '@/features/chat/BackgroundTasks'
import { runErrorText, runPhaseText, runStatusText, useRunRuntimeName } from '@/features/chat/run-status-text'
import { ScopeTag } from '@/features/projects/OverviewScope'
import { SleepUntil } from '@/features/sleep/SleepUntil'
import { t } from '@/i18n'
import { useNow } from '@/lib/use-now'
import { cn } from '@/lib/utils'
import { rowState, summarize, type AgentRowState, type OverviewState } from './overview-state'

type AgentOverview = Schemas['AgentOverview']

const elapsedSince = (time: string, now: number) => t.overview.elapsed(now - Date.parse(time))

// The most Agent names a KPI cell lists; beyond that it says “and N more”.
const NAMES_SHOWN = 3

function names(agents: AgentOverview[]): string {
  return agents.length === 0 ? '' : t.overview.run.names(agents.slice(0, NAMES_SHOWN).map((agent) => agent.name), agents.length)
}

const KPI_CELL_CLASS =
  'flex min-w-0 flex-col justify-center gap-0.5 px-3.5 py-2'

// Single-row hairline KPI strip (~56px tall): Replying · Idle · Queued · Issues 24h · Today's tokens with Stats link.
// When syncing, renders a matching 5-cell skeleton strip.
export function OverviewKpiStrip({
  live,
  issuesCount,
  todayTokens,
  statsHref,
  onOpenIssues,
}: {
  live: OverviewState
  issuesCount?: number
  todayTokens?: number
  statsHref: string
  onOpenIssues?: () => void
}) {
  if (!live.synced) {
    return (
      <div
        aria-busy="true"
        className="grid min-h-[58px] grid-cols-2 divide-y divide-hairline overflow-hidden rounded-card border border-hairline bg-surface-control sm:grid-cols-3 lg:grid-cols-5 lg:divide-x lg:divide-y-0"
      >
        {[0, 1, 2, 3, 4].map((index) => (
          <div key={index} className={KPI_CELL_CLASS}>
            <span className="riso-skeleton h-3 w-16 rounded-stamp" />
            <span className="riso-skeleton mt-1 h-5 w-12 rounded-stamp" />
          </div>
        ))}
      </div>
    )
  }

  const summary = summarize(live.agents)
  const queuedTotal = summary.queued.interrupt + summary.queued.normal
  const hasIssues = (issuesCount ?? 0) > 0

  return (
    <ul
      aria-label={t.overview.run.summaryLabel}
      className="grid min-h-[58px] grid-cols-2 divide-y divide-hairline overflow-hidden rounded-card border border-hairline bg-surface-control sm:grid-cols-3 lg:grid-cols-5 lg:divide-x lg:divide-y-0"
    >
      <li data-tile="busy" className={cn(KPI_CELL_CLASS, '')}>
        <div className="flex items-baseline justify-between gap-2">
          <span className="text-[11.5px] font-medium text-ink-500">
            {t.overview.run.busy}
          </span>
          <span className="font-display-latin text-[20px] leading-none font-semibold text-ink tabular-nums">
            {summary.busy.length}
          </span>
        </div>
        <span className="min-h-[16px] truncate text-[11.5px] leading-4 text-ink-500" title={names(summary.busy)}>
          {names(summary.busy)}
        </span>
      </li>

      <li data-tile="idle" className={cn(KPI_CELL_CLASS, '')}>
        <div className="flex items-baseline justify-between gap-2">
          <span className="text-[11.5px] font-medium text-ink-500">
            {t.overview.run.idle}
          </span>
          <span className="font-display-latin text-[20px] leading-none font-semibold text-ink tabular-nums">
            {summary.idle.length}
          </span>
        </div>
        <span className="min-h-[16px] truncate text-[11.5px] leading-4 text-ink-500" title={names(summary.idle)}>
          {names(summary.idle)}
        </span>
      </li>

      <li data-tile="queued" className={cn(KPI_CELL_CLASS, '')}>
        <div className="flex items-baseline justify-between gap-2">
          <span className="text-[11.5px] font-medium text-ink-500">
            {t.overview.run.queued}
          </span>
          <span className="font-display-latin text-[20px] leading-none font-semibold text-ink tabular-nums">
            {queuedTotal}
          </span>
        </div>
        <span className="min-h-[16px] truncate text-[11.5px] leading-4 text-ink-500">
          {t.overview.run.queuedNote(summary.queued.interrupt, summary.queued.normal)}
        </span>
      </li>

      <li data-tile="issues" className={KPI_CELL_CLASS}>
        <div className="flex items-baseline justify-between gap-2">
          <span className="text-[11.5px] font-medium text-ink-500">
            {t.overview.kpi.issues}
          </span>
          <span
            className={cn(
              'font-display-latin text-[20px] leading-none font-semibold tabular-nums',
              hasIssues ? 'text-red-text' : 'text-ink',
            )}
          >
            {issuesCount !== undefined ? issuesCount : '—'}
          </span>
        </div>
        <span className="min-h-[16px] truncate text-[11.5px] leading-4 text-ink-500">
          {hasIssues && onOpenIssues ? (
            <button
              type="button"
              onClick={onOpenIssues}
              className="cursor-pointer font-medium text-red-text underline-offset-2 hover:underline focus-visible:shadow-focus"
            >
              {t.overview.anomalies.title}
            </button>
          ) : issuesCount === 0 ? (
            t.overview.kpi.issuesZero
          ) : null}
        </span>
      </li>

      <li data-tile="tokens" className={cn(KPI_CELL_CLASS, 'col-span-2 sm:col-span-1')}>
        <div className="flex items-baseline justify-between gap-2">
          <span className="text-[11.5px] font-medium text-ink-500">
            {t.overview.kpi.todayTokens}
          </span>
          <span className="font-display-latin text-[20px] leading-none font-semibold text-ink tabular-nums">
            {todayTokens !== undefined ? t.overview.tokens(todayTokens) : '—'}
          </span>
        </div>
        <span className="min-h-[16px] text-[11.5px] leading-4">
          <Link
            to={statsHref}
            className="inline-flex items-center gap-1 font-medium text-ink-700 underline-offset-2 hover:text-ink hover:underline focus-visible:shadow-focus"
          >
            {t.overview.kpi.statsLink}
            <ArrowRightIcon aria-hidden="true" className="size-3" strokeWidth={2.4} />
          </Link>
        </span>
      </li>
    </ul>
  )
}

// Status pill: flat crisp pill in Mono; slightly tilted background with a navy outline in Plate.
const STATE_PILL: Record<AgentRowState, { label: string; back: string; text: string; dot?: string }> = {
  busy: {
    label: t.overview.run.busy,
    back: 'border border-hairline-strong bg-surface-active',
    text: 'text-ink',
    dot: 'bg-ink',
  },
  idle: {
    label: t.overview.run.idle,
    back: 'border border-green/25 bg-tint-green',
    text: 'text-green-text',
    dot: 'bg-green',
  },
}

function StatePill({ state }: { state: AgentRowState }) {
  const pill = STATE_PILL[state]
  return (
    <span
      data-state-pill={state}
      className={cn(
        'relative inline-flex min-h-[22px] items-center px-2.5 text-[11.5px] font-semibold whitespace-nowrap',
        pill.text,
      )}
    >
      <span aria-hidden="true" className={cn('absolute inset-0 rounded-full', pill.back)} />
      <span className="relative inline-flex items-center gap-1.5">
        {pill.dot && <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full', pill.dot)} />}
        {pill.label}
      </span>
    </span>
  )
}

const PROCESS_DOT: Record<Schemas['ProcessState'], string> = {
  busy: 'bg-ink',
  idle: 'bg-green',
  starting: 'bg-[var(--status-interrupt)]',
  stopped: 'bg-ink-500',
}

// Low-priority process telemetry folded into a concise muted subline beneath the state pill (with full detail in title).
function ProcessSubline({ agent, now }: { agent: AgentOverview; now: number }) {
  const { processState, processPid, processStartedAt } = agent.status
  const exit = agent.lastProcessExit
  const label = t.overview.run.process[processState] ?? t.common.unknown(processState)
  const head = processPid !== null && processState !== 'stopped' ? `${label} ${t.chat.metaSeparator} ${t.overview.run.pid(processPid)}` : label
  let meta: string | null = null
  if (processStartedAt && processState !== 'stopped') meta = t.overview.run.uptime(processStartedAt, elapsedSince(processStartedAt, now))
  else if (exit) meta = t.overview.run.exitMeta(exit.at, t.overview.run.exit[exit.reason] ?? t.common.unknown(exit.reason))
  else if (processState === 'stopped') meta = t.overview.run.neverStarted
  const full = meta ? `${head} · ${meta}` : head
  return (
    <span title={full} className="mt-0.5 flex min-w-0 items-center gap-1.5 font-mono text-[11px] leading-snug text-ink-500">
      <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full', PROCESS_DOT[processState])} />
      <span className="truncate">{full}</span>
    </span>
  )
}

// Current reply: the phase, elapsed time and config version; when idle, shows the last reply's result and end time.
function RunCell({ agent, now }: { agent: AgentOverview; now: number }) {
  const runtimeName = useRunRuntimeName()
  const current = agent.status.currentRun
  let main: string
  let meta: string | null = null
  if (current) {
    main = runPhaseText(current)
    meta = t.overview.run.runMeta(current.status, elapsedSince(current.startedAt ?? current.queuedAt, now), current.configVersion)
  } else if (agent.lastRun) {
    const run = agent.lastRun
    if (run.status === 'failed') {
      main = t.overview.run.last.failed(runErrorText(run.errorCode, runtimeName(run)))
    } else if (run.status === 'interrupted') {
      main =
        run.endReason === 'sleep_superseded' || run.endReason === 'sleep_invalidated'
          ? t.chat.status[run.endReason]
          : run.endReason === 'server_restart'
            ? t.overview.run.last.server_restart
            : t.overview.run.last.user_interrupt
    } else if (run.status === 'completed') {
      main = t.overview.run.last.completed
    } else {
      main = runStatusText(run) ?? t.common.unknown(run.status)
    }
    const field =
      run.status === 'failed' && run.errorCode
        ? t.timeline.result.errorField(run.errorCode, null)
        : run.endReason
          ? t.timeline.result.endReasonField(run.endReason)
          : t.timeline.result.statusField(run.status)
    if (run.endedAt) meta = t.overview.run.lastMeta(run.endedAt, field)
  } else {
    main = t.overview.run.noRun
  }
  return (
    <span className="flex min-w-0 flex-col text-[12.5px] leading-snug text-ink-700" title={meta ? `${main} — ${meta}` : main}>
      <span className="truncate text-ink">{main}</span>
      {meta && <span className="truncate font-mono text-[11px] text-ink-500">{meta}</span>}
    </span>
  )
}

function QueueCell({ queued }: { queued: Schemas['QueueCounts'] }) {
  const total = queued.interrupt + queued.normal
  return (
    // relative: the screen-reader text is absolutely positioned; without a positioned ancestor it escapes the table container.
    <span className="relative inline-flex items-baseline gap-1.5">
      <span
        aria-hidden="true"
        className={cn(
          'font-display-latin text-[16px] font-semibold tabular-nums',
          total > 0 ? 'text-ink' : 'text-ink-500',
        )}
      >
        {total}
      </span>
      <span className="sr-only">{t.overview.run.queueTotal(total, queued.interrupt, queued.normal)}</span>
      {total > 0 && (
        <span aria-hidden="true" className="text-[11.5px] whitespace-nowrap text-ink-500">
          {t.overview.run.queueDetail(queued.interrupt, queued.normal)}
        </span>
      )}
    </span>
  )
}

const headClass = 'py-2 pl-1 text-[11.5px] font-semibold text-ink-500'

function AgentTable({ agents }: { agents: AgentOverview[] }) {
  // Uptime and elapsed time on the current turn tick every second locally without asking the server.
  const now = useNow(1000)
  return (
    <Table aria-label={t.overview.run.tableLabel} className="w-full table-fixed" card={false}>
      <TableHeader className="bg-transparent">
        <TableRow className="border-b border-solid border-hairline-strong last:border-b">
          <TableHead scope="col" className={cn(headClass, 'w-[30%]')}>
            {t.overview.run.columns.agent}
          </TableHead>
          <TableHead scope="col" className={cn(headClass, 'w-[28%]')}>
            {t.overview.run.columns.state}
          </TableHead>
          <TableHead scope="col" className={cn(headClass, 'w-[30%]')}>
            {t.overview.run.columns.run}
          </TableHead>
          <TableHead scope="col" className={cn(headClass, 'w-[12%] pr-1 text-right')}>
            {t.overview.run.columns.queue}
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {agents.map((agent) => {
          const state = rowState(agent)
          return (
            <TableRow
              key={agent.agentId}
              data-agent-id={agent.agentId}
              className="border-b border-hairline last:border-b"
            >
              <TableCell className="py-2 pl-1 align-middle">
                <Link to={`/agents/${agent.agentId}/chat`} className="group flex min-w-0 items-center gap-2.5 rounded-ticket text-ink">
                  <AgentAvatar
                    size="sm"
                    id={agent.agentId}
                    busy={agent.status.busy}
                    sleeping={!agent.status.busy && agent.status.sleep?.state === 'sleeping'}
                  />
                  <span className="flex min-w-0 flex-col leading-[1.3]">
                    <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5">
                      <span className="truncate text-[13.5px] font-semibold group-hover:underline">
                        {agent.name}
                      </span>
                      <ScopeTag scope={agent.scope} projectId={agent.projectId} />
                    </span>
                    <span className="truncate font-mono text-[11px] text-ink-500">
                      {agent.model.provider} / {agent.model.modelId}
                    </span>
                  </span>
                </Link>
              </TableCell>
              <TableCell className="py-2 align-middle">
                <div className="flex min-w-0 flex-col">
                  <span className="inline-flex flex-wrap items-center gap-1.5">
                    <StatePill state={state} />
                    {!agent.status.busy && <BackgroundCountTag count={agent.status.backgroundTasks.length} />}
                  </span>
                  {!agent.status.busy && agent.status.sleep?.state === 'sleeping' && agent.status.sleep.wakeAt && (
                    <SleepUntil at={agent.status.sleep.wakeAt} reason={agent.status.sleep.reason} />
                  )}
                  <ProcessSubline agent={agent} now={now} />
                </div>
              </TableCell>
              <TableCell className="py-2 align-middle">
                <RunCell agent={agent} now={now} />
              </TableCell>
              <TableCell className="py-2 pr-1 text-right align-middle">
                <QueueCell queued={agent.status.queued} />
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}

function TableSkeleton() {
  return (
    <div aria-busy="true" className="flex flex-col gap-2">
      <span className="riso-skeleton h-12 rounded-card" />
      <span className="riso-skeleton h-12 rounded-card opacity-75" />
      <span className="riso-skeleton h-12 rounded-card opacity-50" />
      <span role="status" className="text-[12.5px] font-medium text-ink-500">
        {t.common.loading}
      </span>
    </div>
  )
}

// Run status table: updated live from the overview event stream.
export function RunStatusSection({ live }: { live: OverviewState }) {
  return (
    <section aria-label={t.overview.run.title} className="flex min-w-0 flex-col gap-2">
      {live.synced && live.lastChangeAt && (
        <div className="flex items-center justify-end">
          <span className="font-mono text-[11px] text-ink-500">{t.overview.run.lastChange(live.lastChangeAt)}</span>
        </div>
      )}
      {live.synced ? <AgentTable agents={live.agents} /> : <TableSkeleton />}
    </section>
  )
}
