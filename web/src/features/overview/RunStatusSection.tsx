import { ScopeTag } from '@/features/projects/OverviewScope'
import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { AgentAvatar } from '@/components/AgentAvatar'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { runPhaseText, runStatusText } from '@/features/chat/run-status-text'
import { t } from '@/i18n'
import { SleepUntil } from '@/features/sleep/SleepUntil'
import { useNow } from '@/lib/use-now'
import { cn } from '@/lib/utils'
import { NumberedSection } from '@/components/NumberedSection'
import { rowState, summarize, type AgentRowState, type OverviewState } from './overview-state'

type AgentOverview = Schemas['AgentOverview']

const elapsedSince = (time: string, now: number) => t.overview.elapsed(now - Date.parse(time))

// 汇总卡最多列出的 Agent 名称个数，更多的写“等 N 个”。
const NAMES_SHOWN = 3

function names(agents: AgentOverview[]): string {
  return agents.length === 0 ? '' : t.overview.run.names(agents.slice(0, NAMES_SHOWN).map((agent) => agent.name), agents.length)
}

// 四张汇总卡的底色（改版：带色底、2px 藏青描边、4px 藏青实色投影的卡片，不再倾斜）。
const TILES = [
  { key: 'busy', bg: 'bg-tint-pink-2' },
  { key: 'idle', bg: 'bg-tint-green' },
  { key: 'sessionMissing', bg: 'bg-tint-red' },
  { key: 'queued', bg: 'bg-tint-blue' },
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
    <ul aria-label={t.overview.run.summaryLabel} className="grid grid-cols-[repeat(auto-fit,minmax(min(190px,100%),1fr))] gap-x-3.5 gap-y-3">
      {TILES.map((tile) => {
        const { label, value, note } = values[tile.key]
        return (
          <li
            key={tile.key}
            data-tile={tile.key}
            className={cn('mr-1 mb-1 flex flex-col gap-1 rounded-card border-2 border-ink px-4 pt-3.5 pb-3 shadow-[4px_4px_0_var(--ink)]', tile.bg)}
          >
            <span className="text-[13px] font-black">{label}</span>
            <span className="font-display-latin text-[46px] leading-none font-extrabold">{value}</span>
            <span className="min-h-[18px] text-[12px] leading-[18px] text-ink-700 [overflow-wrap:anywhere]">{note}</span>
          </li>
        )
      })}
    </ul>
  )
}

// 状态胶囊（改版通用规则第 6 条）：略微倾斜的底子，藏青描边；回复中荧光粉、空闲浅绿带绿点；会话丢失是红色描边的印章。
const STATE_PILL: Record<AgentRowState, { label: string; back: string; text: string; dot?: string }> = {
  busy: { label: t.overview.run.busy, back: 'bg-riso-pink shadow-[inset_0_0_0_2px_var(--ink)]', text: 'text-ink', dot: 'bg-ink' },
  idle: { label: t.overview.run.idle, back: 'bg-tint-green shadow-[inset_0_0_0_2px_var(--ink)]', text: 'text-green-text', dot: 'bg-green' },
  sessionMissing: { label: t.overview.run.sessionMissing, back: 'rounded-[4px] shadow-[inset_0_0_0_2px_var(--red-text)]', text: 'text-red-text tracking-[0.06em]' },
}

function StatePill({ state }: { state: AgentRowState }) {
  const pill = STATE_PILL[state]
  return (
    <span data-state-pill={state} className={cn('relative inline-flex min-h-6 items-center px-[11px] text-[12px] font-black whitespace-nowrap', pill.text)}>
      <span aria-hidden="true" className={cn('absolute inset-0 rotate-[-3deg] rounded-full', pill.back)} />
      <span className="relative inline-flex items-center gap-1.5">
        {pill.dot && <span aria-hidden="true" className={cn('size-[7px] shrink-0 rounded-full', pill.dot)} />}
        {pill.label}
      </span>
    </span>
  )
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
    <span className="flex flex-col text-[13px] text-ink-700">
      <span>{main}</span>
      {meta && <span className="font-mono text-[11px] text-ink-500">{meta}</span>}
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
          'font-display-latin text-[20px] font-extrabold',
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
    <span className="flex flex-col text-[13px] text-ink-700">
      <span className="inline-flex items-center gap-1.5 font-bold">
        <span aria-hidden="true" className={cn('size-[9px] shrink-0 rounded-full shadow-[0_0_0_2px_var(--ink)]', PROCESS_DOT[processState])} />
        {processPid !== null && processState !== 'stopped' ? `${label} ${t.chat.metaSeparator} ${t.overview.run.pid(processPid)}` : label}
      </span>
      {meta && <span className="font-mono text-[11px] text-ink-500">{meta}</span>}
    </span>
  )
}

// 表头下一道 2px 实线，行与行之间 1.5px 虚线（改版通用规则第 9 条）；表格直接排在纸上，不套卡片。
const headClass = 'py-2 pl-1 text-[12px] font-black text-ink-500'

function AgentTable({ agents }: { agents: AgentOverview[] }) {
  // 运行时长与本轮已用时间每秒走动，由页面自己计算，不向服务端请求。
  const now = useNow(1000)
  return (
    <Table aria-label={t.overview.run.tableLabel} className="min-w-[860px]" card={false}>
      <TableHeader className="bg-transparent">
        <TableRow className="border-b-2 border-solid border-ink last:border-b-2">
          <TableHead scope="col" className={headClass}>
            {t.overview.run.columns.agent}
          </TableHead>
          <TableHead scope="col" className={headClass}>
            {t.overview.run.columns.state}
          </TableHead>
          <TableHead scope="col" className={headClass}>
            {t.overview.run.columns.run}
          </TableHead>
          <TableHead scope="col" className={cn(headClass, 'text-center')}>
            {t.overview.run.columns.queue}
          </TableHead>
          <TableHead scope="col" className={headClass}>
            {t.overview.run.columns.process}
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {agents.map((agent) => {
          const state = rowState(agent)
          return (
            <TableRow key={agent.agentId} data-agent-id={agent.agentId} className="border-b-[1.5px] border-ink/25 last:border-b-[1.5px]">
              <TableCell className="py-[11px] pl-1 align-middle">
                <Link to={`/agents/${agent.agentId}/chat`} className="group flex items-center gap-2.5 rounded-ticket text-ink">
                  <AgentAvatar size="sm" id={agent.agentId} />
                  <span className="flex min-w-0 flex-col leading-[1.35]">
                    <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                      <span className="text-[14.5px] font-black [overflow-wrap:anywhere] group-hover:text-riso-blue group-hover:underline">{agent.name}</span>
                      <ScopeTag scope={agent.scope} projectId={agent.projectId} />
                    </span>
                    <span className="font-mono text-[11px] text-ink-500">
                      {agent.model.provider} / {agent.model.modelId}
                    </span>
                  </span>
                </Link>
              </TableCell>
              <TableCell className="py-[11px] align-middle">
                <StatePill state={state} />
                {!agent.status.busy && agent.status.sleep?.state === 'sleeping' && agent.status.sleep.wakeAt && <SleepUntil at={agent.status.sleep.wakeAt} reason={agent.status.sleep.reason} />}
              </TableCell>
              <TableCell className="py-[11px] align-middle">
                <RunCell agent={agent} now={now} />
              </TableCell>
              <TableCell className="py-[11px] text-center align-middle">
                <QueueCell queued={agent.status.queued} />
              </TableCell>
              <TableCell className="py-[11px] pr-1 align-middle">
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

// 运行状态：四个汇总数与 Agent 表，随概览事件流实时更新。纸张上的第 number 个编号分区。
export function RunStatusSection({ live, number = 1 }: { live: OverviewState; number?: number }) {
  return (
    <NumberedSection
      number={number}
      title={t.overview.run.title}
      meta={live.synced && live.lastChangeAt && <span className="font-mono text-[11.5px] text-ink-500">{t.overview.run.lastChange(live.lastChangeAt)}</span>}
    >
      {live.synced ? (
        <>
          <SummaryTiles agents={live.agents} />
          <AgentTable agents={live.agents} />
        </>
      ) : (
        <Skeleton />
      )}
    </NumberedSection>
  )
}
