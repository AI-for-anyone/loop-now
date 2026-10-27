import { useId } from 'react'
import { ScopeTag } from '@/features/projects/OverviewScope'
import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { AgentAvatar } from '@/components/AgentAvatar'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { runPhaseText, runStatusText } from '@/features/chat/run-status-text'
import { t } from '@/i18n'
import { SleepUntil } from '@/features/sleep/SleepUntil'
import { useNow } from '@/lib/use-now'
import { cn } from '@/lib/utils'
import { rowState, summarize, type AgentRowState, type OverviewState } from './overview-state'

type AgentOverview = Schemas['AgentOverview']

const elapsedSince = (time: string, now: number) => t.overview.elapsed(now - Date.parse(time))

// 汇总卡最多列出的 Agent 名称个数，更多的写“等 N 个”。
const NAMES_SHOWN = 3

function names(agents: AgentOverview[]): string {
  return agents.length === 0 ? '' : t.overview.run.names(agents.slice(0, NAMES_SHOWN).map((agent) => agent.name), agents.length)
}

// 四张汇总卡的底色、倾斜和大号数字的颜色，取自 ROverview 画板。
const TILES = [
  { key: 'busy', bg: 'var(--tint-pink-2)', tilt: '-0.8deg', number: 'text-riso-pink [text-shadow:4px_3px_0_var(--riso-blue)]' },
  { key: 'idle', bg: 'var(--tint-green)', tilt: '0.6deg', number: 'text-green-text [text-shadow:4px_3px_0_rgb(255_79_163/.6)]' },
  { key: 'sessionMissing', bg: 'var(--tint-red)', tilt: '-0.5deg', number: 'text-red-text [text-shadow:4px_3px_0_rgb(36_67_181/.5)]' },
  { key: 'queued', bg: 'var(--tint-blue)', tilt: '0.9deg', number: 'text-riso-blue [text-shadow:4px_3px_0_var(--riso-pink)]' },
] as const

function SummaryTiles({ agents }: { agents: AgentOverview[] }) {
  const summary = summarize(agents)
  const queuedTotal = summary.queued.interrupt + summary.queued.normal
  const values = {
    busy: { label: t.overview.run.busy, value: summary.busy.length, note: names(summary.busy) },
    idle: { label: t.overview.run.idle, value: summary.idle.length, note: names(summary.idle) },
    sessionMissing: { label: t.overview.run.sessionMissing, value: summary.sessionMissing.length, note: names(summary.sessionMissing) },
    queued: { label: t.overview.run.queued, value: queuedTotal, note: t.overview.run.queuedNote(summary.queued.interrupt, summary.queued.normal) },
  }
  return (
    <ul aria-label={t.overview.run.summaryLabel} className="grid grid-cols-[repeat(auto-fit,minmax(min(200px,100%),1fr))] gap-4">
      {TILES.map((tile) => {
        const { label, value, note } = values[tile.key]
        return (
          <li
            key={tile.key}
            data-tile={tile.key}
            className="riso-tilt flex flex-col gap-1.5 rounded-panel px-5 py-[18px] [--tilt-shadow:3px_3px_0_rgb(24_38_94/.14)]"
            style={{ '--tilt-bg': tile.bg, '--tilt': tile.tilt } as React.CSSProperties}
          >
            <span className="text-[14px] font-black">{label}</span>
            <span className={cn('font-display-latin text-[64px] leading-[0.95] font-extrabold', tile.number)}>{value}</span>
            <span className="min-h-[1.6em] text-[12.5px] font-bold text-ink-700 [overflow-wrap:anywhere]">{note}</span>
          </li>
        )
      })}
    </ul>
  )
}

const STATE_CHIP: Record<AgentRowState, { tone: 'busy' | 'success' | 'danger'; label: string }> = {
  busy: { tone: 'busy', label: t.overview.run.busy },
  idle: { tone: 'success', label: t.overview.run.idle },
  sessionMissing: { tone: 'danger', label: t.overview.run.sessionMissing },
}

// 当前回复：阶段、本轮已用时间（从派发起）与配置版本；没有进行中的回复时显示上一条的结果与结束时间。
function RunCell({ agent, now }: { agent: AgentOverview; now: number }) {
  const current = agent.status.currentRun
  let main: string
  let meta: string | null = null
  if (current) {
    main = runPhaseText(current)
    meta = t.overview.run.runMeta(current.status, elapsedSince(current.startedAt ?? current.queuedAt, now), current.configVersion)
  } else if (agent.lastRun) {
    const run = agent.lastRun
    if (run.status === 'failed') {
      main = t.overview.run.last.failed(run.errorCode ? (t.chat.runError[run.errorCode] ?? t.common.unknown(run.errorCode)) : t.errors.unknown)
    } else if (run.status === 'interrupted') {
      main = run.endReason === 'sleep_superseded' || run.endReason === 'sleep_invalidated' ? t.chat.status[run.endReason] : run.endReason === 'server_restart' ? t.overview.run.last.server_restart : t.overview.run.last.user_interrupt
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
    <span className="flex flex-col">
      <span className="font-bold">{main}</span>
      {meta && <span className="font-mono text-[11.5px] text-ink-500">{meta}</span>}
    </span>
  )
}

function QueueCell({ queued }: { queued: Schemas['QueueCounts'] }) {
  const total = queued.interrupt + queued.normal
  return (
    // relative：读屏用的说明是绝对定位的，没有定位祖先时会逃出表格的横向滚动区，把整页撑宽。
    <span className="relative inline-flex items-baseline gap-1.5">
      {/* 排队数是展示字；读屏读到的是旁边的完整说明。 */}
      <span
        aria-hidden="true"
        className={cn(
          'font-display-latin text-[26px] font-extrabold',
          // 错版投影只用于 ≥ 28px 的展示字，这里 26px 不加。
          total > 0 ? 'text-riso-pink' : 'text-ink-500',
        )}
      >
        {total}
      </span>
      <span className="sr-only">{t.overview.run.queueTotal(total, queued.interrupt, queued.normal)}</span>
      {total > 0 && (
        <span aria-hidden="true" className="text-[12.5px] text-ink-700">
          {t.overview.run.queueDetail(queued.interrupt, queued.normal)}
        </span>
      )}
    </span>
  )
}

const PROCESS_DOT: Record<Schemas['ProcessState'], string> = {
  busy: 'bg-riso-pink',
  idle: 'bg-green',
  starting: 'bg-riso-yellow',
  stopped: 'bg-ink-500',
}

// Pi 进程：状态与进程号；运行中显示已运行时长，未运行时显示上次退出的时间与原因。
function ProcessCell({ agent, now }: { agent: AgentOverview; now: number }) {
  const { processState, processPid, processStartedAt } = agent.status
  const exit = agent.lastProcessExit
  const label = t.overview.run.process[processState] ?? t.common.unknown(processState)
  let meta: string | null = null
  if (processStartedAt && processState !== 'stopped') meta = t.overview.run.uptime(processStartedAt, elapsedSince(processStartedAt, now))
  else if (exit) meta = t.overview.run.exitMeta(exit.at, t.overview.run.exit[exit.reason] ?? t.common.unknown(exit.reason))
  else if (processState === 'stopped') meta = t.overview.run.neverStarted
  return (
    <span className="flex flex-col">
      <span className="inline-flex items-center gap-1.5 font-black">
        <span aria-hidden="true" className={cn('size-[9px] shrink-0 rounded-full shadow-[0_0_0_2px_var(--ink)]', PROCESS_DOT[processState])} />
        {processPid !== null && processState !== 'stopped' ? `${label} ${t.chat.metaSeparator} ${t.overview.run.pid(processPid)}` : label}
      </span>
      {meta && <span className="font-mono text-[11.5px] text-ink-500">{meta}</span>}
    </span>
  )
}

function AgentTable({ agents }: { agents: AgentOverview[] }) {
  // 运行时长与本轮已用时间每秒走动，由页面自己计算，不向服务端请求。
  const now = useNow(1000)
  return (
    <Table aria-label={t.overview.run.tableLabel} className="min-w-[960px]">
      <TableHeader>
        <TableRow>
          <TableHead scope="col" className="py-3 pl-[18px]">
            {t.overview.run.columns.agent}
          </TableHead>
          <TableHead scope="col" className="py-3">
            {t.overview.run.columns.state}
          </TableHead>
          <TableHead scope="col" className="py-3">
            {t.overview.run.columns.run}
          </TableHead>
          <TableHead scope="col" className="py-3">
            {t.overview.run.columns.queue}
          </TableHead>
          <TableHead scope="col" className="py-3 pr-[18px]">
            {t.overview.run.columns.process}
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {agents.map((agent) => {
          const chip = STATE_CHIP[rowState(agent)]
          return (
            <TableRow key={agent.agentId} data-agent-id={agent.agentId}>
              <TableCell className="py-3.5 pl-[18px] align-middle">
                <Link to={`/agents/${agent.agentId}/chat`} className="flex items-center gap-3 rounded-ticket text-ink">
                  <AgentAvatar id={agent.agentId} className="h-[30px] w-9" />
                  <span className="flex min-w-0 flex-col">
                    <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                      <span className="text-[15px] font-black underline decoration-riso-pink decoration-2 underline-offset-4 [overflow-wrap:anywhere]">
                        {agent.name}
                      </span>
                      <ScopeTag scope={agent.scope} projectId={agent.projectId} />
                    </span>
                    <span className="font-mono text-[11px] text-ink-500">
                      {agent.model.provider} / {agent.model.modelId}
                    </span>
                  </span>
                </Link>
              </TableCell>
              <TableCell className="py-3.5 align-middle">
                <Badge tone={chip.tone}>{chip.label}</Badge>
                {!agent.status.busy && agent.status.sleep?.state === 'sleeping' && agent.status.sleep.wakeAt && <SleepUntil at={agent.status.sleep.wakeAt} reason={agent.status.sleep.reason} />}
              </TableCell>
              <TableCell className="py-3.5 align-middle">
                <RunCell agent={agent} now={now} />
              </TableCell>
              <TableCell className="py-3.5 align-middle">
                <QueueCell queued={agent.status.queued} />
              </TableCell>
              <TableCell className="py-3.5 pr-[18px] align-middle">
                <ProcessCell agent={agent} now={now} />
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}

function Skeleton() {
  return (
    <div aria-busy="true" className="flex flex-col gap-3">
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(200px,100%),1fr))] gap-3.5">
        {[0, 1, 2, 3].map((index) => (
          <span key={index} className="riso-skeleton h-[120px] rounded-panel" />
        ))}
      </div>
      <span className="riso-skeleton h-[74px] rounded-[16px]" />
      <span className="riso-skeleton h-[74px] rounded-[16px] opacity-70" />
      <span role="status" className="text-[13px] font-bold text-ink-500">
        {t.common.loading}
      </span>
    </div>
  )
}

// 运行状态：四个汇总数与 Agent 表，随概览事件流实时更新。
export function RunStatusSection({ live }: { live: OverviewState }) {
  const titleId = useId()
  return (
    <section aria-labelledby={titleId} className="flex flex-col gap-[18px]">
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-2.5">
        <h2 id={titleId} className="riso-misreg-sm font-display-cn text-[40px] leading-[1.1] font-normal">
          {t.overview.run.title}
        </h2>
        {live.synced && live.lastChangeAt && (
          <span className="text-[13px] font-bold text-ink-500">{t.overview.run.lastChange(live.lastChangeAt)}</span>
        )}
      </div>
      {live.synced ? (
        <>
          <SummaryTiles agents={live.agents} />
          <AgentTable agents={live.agents} />
        </>
      ) : (
        <Skeleton />
      )}
    </section>
  )
}
