import { useId, type ReactNode } from 'react'
import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { sectionId } from '@/features/agent-config/sections'
import { t } from '@/i18n'
import { agentPath, type Scope } from '@/lib/scope'
import { useNow } from '@/lib/use-now'
import { cn } from '@/lib/utils'
import { Bar, Legend, LineChart, StackedColumns } from './charts'
import { estimateCost, modelFamily, type ModelFamily } from './pricing'
import { durationTicks, niceTicks } from './ticks'

const text = t.stats

type Report = Schemas['StatsReport']
export type ScopeOf = (projectId: string | null) => Scope | null
// The x-axis: each bucket's short label and tooltip title, computed by the page from the bucket width and passed in.
export type Axis = { labels: string[]; shortLabels: string[]; titles: string[]; hourly: boolean; rangeLabel: string }

// Once the average context exceeds this many tokens, consider having the Agent start a new session.
export const REOPEN_CONTEXT = 120_000

const COLOR = {
  blue: 'var(--chart-blue)',
  pink: 'var(--chart-pink)',
  ochre: 'var(--chart-ochre)',
  other: 'var(--chart-other)',
  done: 'var(--status-done)',
  interrupt: 'var(--status-interrupt)',
  fail: 'var(--status-fail)',
}

const wanOf = (tokens: number) => tokens / 10_000

// Restrained hairline card with crisp 13.5px semibold header; Plate skin keeps its 2px ink outline and offset shadow.
export function Card({ title, unit, aside, children, className }: { title: string; unit?: ReactNode; aside?: ReactNode; children: ReactNode; className?: string }) {
  const titleId = useId()
  return (
    <section
      aria-labelledby={titleId}
      className={cn(
        'flex min-w-0 flex-col gap-3 rounded-card border border-hairline bg-surface-control p-4 [[data-skin=plate]_&]:mr-1 [[data-skin=plate]_&]:mb-1 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink)]',
        className,
      )}
    >
      <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
        <h2
          id={titleId}
          className="text-[13.5px] leading-snug font-semibold tracking-tight text-ink [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[20px] [[data-skin=plate]_&]:leading-tight [[data-skin=plate]_&]:font-normal"
        >
          {title}
        </h2>
        {unit && <span className="text-[12px] text-ink-500">{unit}</span>}
        {aside && <div className="sm:ml-auto">{aside}</div>}
      </div>
      {children}
    </section>
  )
}

const Note = ({ children }: { children: ReactNode }) => <p className="text-[11.5px] leading-[1.5] text-ink-500">{children}</p>
const Quiet = ({ children }: { children: ReactNode }) => <p className="py-2 text-[12.5px] text-ink-500">{children}</p>

// 1. Daily usage (hourly for today), stacked by trigger: your messages, scheduled wakes, Agent-to-Agent reminders and tasks, other.
export function OriginsCard({ report, axis }: { report: Report; axis: Axis }) {
  const { series, unrequestedShare } = report.byOrigin
  const columns = [
    { key: 'console', label: text.origins.console, color: COLOR.blue, values: series.map((b) => wanOf(b.console)) },
    { key: 'sleepWake', label: text.origins.sleepWake, color: COLOR.pink, values: series.map((b) => wanOf(b.sleepWake)) },
    { key: 'agents', label: text.origins.agents, color: COLOR.ochre, values: series.map((b) => wanOf(b.inboxReminder + b.task)) },
    { key: 'other', label: text.origins.other, color: COLOR.other, values: series.map((b) => wanOf(b.other)) },
  ]
  return (
    <Card
      title={text.origins.title(axis.hourly)}
      unit={text.origins.unit}
      aside={<Legend items={columns.map((c) => ({ label: c.label, color: c.color, kind: 'rect' }))} />}
    >
      <StackedColumns
        label={text.origins.label(unrequestedShare === null ? null : text.wholePercent(unrequestedShare))}
        labels={axis.labels}
        titles={axis.titles}
        series={columns}
        height={220}
        format={(wan) => text.wan(Math.round(wan * 10_000))}
        axis={text.wanAxis}
        tops={(wan) => text.wanAxis(Math.round(wan))}
        totalLabel={text.total}
      />
    </Card>
  )
}

// Wake frequency: written from the average interval between consecutive wakes, as in “Wakes every 4 h”.
function cadence(wake: Schemas['StatsAgentWakes']) {
  const s = wake.avgIntervalSeconds
  if (s === null) return text.wakes.once
  const interval =
    s < 3600 ? text.wakes.everyMinutes(Math.max(1, Math.round(s / 60))) : s < 36 * 3600 ? text.wakes.everyHours(Math.round(s / 3600)) : text.wakes.everyDays(Math.round(s / 86400))
  return text.wakes.every(interval)
}

// Who burns tokens after waking: Agents ranked by what their wakes consume; links to the top spender's prompt.
export function WakesCard({ report, scopeOf }: { report: Report; scopeOf: ScopeOf }) {
  const wakes = report.wakes
  const count = wakes.reduce((sum, w) => sum + w.count, 0)
  const tokens = wakes.reduce((sum, w) => sum + w.tokens, 0)
  const top = wakes[0]
  const rest = wakes.slice(1)
  const restCount = rest.reduce((sum, w) => sum + w.count, 0)
  const restAvg = restCount ? rest.reduce((sum, w) => sum + w.tokens, 0) / restCount : 0
  const times = top && restAvg > 0 ? Math.round(top.avgTokens / restAvg) : null
  const topScope = top && scopeOf(top.projectId)
  return (
    <Card title={text.wakes.title} unit={count > 0 ? text.wakes.summary(count, text.wan(tokens)) : undefined}>
      {top ? (
        <>
          <ul className="flex flex-col gap-1.5">
            {wakes.slice(0, 5).map((wake, i) => (
              <li
                key={wake.agentId}
                className={cn(
                  'flex flex-col gap-1 rounded-ticket border border-hairline px-2.5 py-1.5 [[data-skin=plate]_&]:border-0',
                  i === 0 ? 'bg-tint-pink-2' : 'bg-surface-subtle [[data-skin=plate]_&]:bg-paper/60',
                )}
              >
                <span className="flex min-w-0 items-baseline gap-2">
                  <span className="truncate text-[13px] font-semibold [[data-skin=plate]_&]:font-black">{wake.name}</span>
                  <span className="min-w-0 truncate text-[11.5px] text-ink-700">{text.wakes.row(cadence(wake), text.wan(wake.avgTokens))}</span>
                  <span className="ml-auto shrink-0 font-mono text-[12.5px] font-semibold tabular-nums [[data-skin=plate]_&]:font-bold">
                    {text.wan(wake.tokens)}
                  </span>
                </span>
                <Bar value={wake.tokens} max={top.tokens} color={COLOR.pink} height="h-2" />
              </li>
            ))}
          </ul>
          <div className="mt-auto flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-ink-700">
            <span
              aria-hidden="true"
              className="inline-flex size-[18px] shrink-0 items-center justify-center rounded-full bg-[var(--status-interrupt)] text-[12px] font-semibold text-[#111111] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink"
            >
              !
            </span>
            <span className="min-w-0 flex-1">{text.wakes.note(top.name, text.wan(top.avgTokens), times !== null && times >= 2 ? times : null)}</span>
            {topScope && (
              <Link
                to={`${agentPath(topScope, top.agentId, 'config')}#${sectionId('prompt')}`}
                className="rounded-sm font-semibold whitespace-nowrap text-ink underline underline-offset-2 outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-riso-blue"
              >
                {text.wakes.prompt}
              </Link>
            )}
          </div>
        </>
      ) : (
        <Quiet>{text.wakes.none}</Quiet>
      )}
    </Card>
  )
}

// 2. How much context is sent with each model call, per Agent; an average above 120K gets marked "New session due".
export function ContextCard({ report }: { report: Report }) {
  const agents = report.context.agents.slice(0, 6)
  const max = agents[0]?.avgInputPerCall ?? 0
  return (
    <Card title={text.context.title} unit={text.context.unit}>
      {agents.length ? (
        <ul className="flex flex-col gap-1.5">
          {agents.map((agent) => (
            <li
              key={agent.agentId}
              className="grid grid-cols-[minmax(48px,120px)_minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-0.5 text-[12.5px] sm:grid-cols-[minmax(48px,120px)_minmax(0,1fr)_54px_118px]"
            >
              <span className="truncate font-medium [[data-skin=plate]_&]:font-bold">{agent.name}</span>
              <Bar value={agent.avgInputPerCall} max={max} color={COLOR.blue} />
              <span className="text-right font-mono text-[12px] font-medium tabular-nums [[data-skin=plate]_&]:font-bold">
                {text.wan(agent.avgInputPerCall)}
              </span>
              <span className="col-span-3 flex items-center gap-1.5 text-[11.5px] whitespace-nowrap text-ink-700 sm:col-span-1">
                {text.context.compactions(agent.compactions)}
                {agent.avgInputPerCall > REOPEN_CONTEXT && (
                  <span
                    title={text.context.reopenTitle}
                    className="rounded-stamp bg-[var(--status-interrupt)] px-1.5 text-[10.5px] font-semibold text-[#111111] [[data-skin=plate]_&]:rounded-[6px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink"
                  >
                    {text.context.reopen}
                  </span>
                )}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <Quiet>{text.context.none}</Quiet>
      )}
      <Note>{text.context.note}</Note>
    </Card>
  )
}

// Sum token shares by model family (Claude, GPT, other), from largest to smallest.
function familyShares(tokens: Schemas['StatsTokens']) {
  const sums = new Map<ModelFamily, number>()
  for (const row of tokens.byModel) {
    const family = modelFamily(row.provider, row.model)
    sums.set(family, (sums.get(family) ?? 0) + row.total)
  }
  const total = [...sums.values()].reduce((a, b) => a + b, 0)
  return [...sums.entries()]
    .filter(([, n]) => n > 0)
    .sort((a, b) => b[1] - a[1])
    .map(([family, n]) => ({
      family,
      tokens: n,
      label: text.process.share(text.process.families[family], text.wholePercent(total ? n / total : 0)),
    }))
}

// Models & cost card (split out of ProcessCard onto the Usage & Cost tab).
export function ModelCostCard({ report }: { report: Report }) {
  const shares = familyShares(report.tokens)
  const shareLabels = shares.map((s) => s.label)
  const cost = estimateCost(report.tokens)
  const rows: { label: string; value: string; note?: string; extra?: string }[] = [
    {
      label: text.process.byModel,
      value: shareLabels[0] ?? text.none,
      note: shareLabels.length > 1 ? text.process.others(shareLabels.slice(1)) : undefined,
    },
    {
      label: text.process.cost,
      value: cost.priced ? text.usd(cost.usd) : text.none,
      note: cost.priced ? text.process.costNote : text.process.noPrice,
      extra: cost.unpriced.length ? text.process.unpriced(cost.unpriced) : undefined,
    },
  ]
  const topModels = [...report.tokens.byModel].sort((a, b) => b.total - a.total).slice(0, 4)
  const maxModelTokens = topModels[0]?.total ?? 0

  return (
    <Card title={text.modelCost.title}>
      <dl className="flex flex-col">
        {rows.map((row) => (
          <div
            key={row.label}
            className="flex flex-wrap items-baseline gap-x-2 border-b border-hairline py-2 first:pt-0 last:border-b-0 [[data-skin=plate]_&]:border-ink/10"
          >
            <dt className="text-[12.5px] text-ink-700">{row.label}</dt>
            <dd className="ml-auto font-display-latin text-[17px] font-semibold text-ink tabular-nums [[data-skin=plate]_&]:font-bold">{row.value}</dd>
            {row.note && <dd className="w-full text-[11.5px] text-ink-500">{row.note}</dd>}
            {row.extra && <dd className="w-full text-[11.5px] text-ink-500">{row.extra}</dd>}
          </div>
        ))}
      </dl>
      {topModels.length > 0 && (
        <ul className="flex flex-col gap-1.5 border-t border-hairline pt-2.5 [[data-skin=plate]_&]:border-ink/10">
          {topModels.map((row) => {
            const label = row.provider && row.model ? `${row.provider} / ${row.model}` : row.model || text.none
            return (
              <li key={`${row.provider ?? ''}:${row.model ?? ''}`} className="grid grid-cols-[minmax(80px,150px)_minmax(0,1fr)_auto] items-center gap-2 text-[12px]">
                <span className="truncate font-mono text-[11.5px] text-ink-700" title={label}>
                  {row.model || text.none}
                </span>
                <Bar value={row.total} max={maxModelTokens} color={COLOR.blue} height="h-2" />
                <span className="font-mono text-[11.5px] font-medium text-ink tabular-nums">{text.wan(row.total)}</span>
              </li>
            )
          })}
        </ul>
      )}
    </Card>
  )
}

// 3. Waiting for your answer (trimmed: redundant 3-tile sub-KPI <dl> removed since Reliability MetricStrip owns those totals).
export function QuestionsCard({ report, scopeOf }: { report: Report; scopeOf: ScopeOf }) {
  const { maxAnswerSeconds, pending } = report.questions
  const now = useNow(60_000)
  const unit = maxAnswerSeconds !== null ? `${text.questions.unit} · ${text.questions.longestWait(text.minutes(maxAnswerSeconds))}` : text.questions.unit
  return (
    <Card title={text.questions.title} unit={unit}>
      {pending.length ? (
        <ul className="flex flex-col gap-1.5">
          {pending.slice(0, 5).map((question) => {
            const scope = scopeOf(question.projectId)
            const body = (
              <>
                <span className="rounded-stamp bg-surface-active px-1.5 text-[11px] font-medium text-ink [[data-skin=plate]_&]:rounded-[6px] [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:font-black">
                  {question.name}
                </span>
                <span className="truncate text-[12.5px]">{question.text}</span>
                <span className="text-[11.5px] font-medium whitespace-nowrap text-red-text [[data-skin=plate]_&]:font-bold">
                  {text.questions.waited(text.minutes(Math.max(0, (now - Date.parse(question.askedAt)) / 1000)))}
                </span>
              </>
            )
            const row =
              'grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 rounded-ticket border border-hairline-strong bg-surface-subtle px-2.5 py-1.5 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)]'
            return (
              <li key={question.messageId}>
                {scope ? (
                  <Link
                    to={agentPath(scope, question.agentId, 'chat')}
                    className={cn(row, 'outline-none hover:bg-surface-hover focus-visible:shadow-focus [[data-skin=plate]_&]:hover:bg-tint-yellow')}
                  >
                    {body}
                  </Link>
                ) : (
                  <span className={row}>{body}</span>
                )}
              </li>
            )
          })}
        </ul>
      ) : (
        <Quiet>{text.questions.noPending}</Quiet>
      )}
    </Card>
  )
}

// 4. Time to first reply: p50 and p90 from your message being queued to the reply starting, including time spent queued.
export function WaitCard({ report, axis }: { report: Report; axis: Axis }) {
  const series = [
    { key: 'p50', label: text.wait.p50Short, color: COLOR.blue, values: report.wait.series.map((b) => b.p50) },
    { key: 'p90', label: text.wait.p90Short, color: COLOR.pink, values: report.wait.series.map((b) => b.p90) },
  ]
  const max = Math.max(0, ...series.flatMap((s) => s.values.filter((v) => v !== null)))
  return (
    <Card
      title={text.wait.title}
      aside={
        <Legend
          items={[
            { label: text.wait.p50, color: COLOR.blue, kind: 'line' },
            { label: text.wait.p90, color: COLOR.pink, kind: 'line' },
          ]}
        />
      }
    >
      {report.wait.p50 === null ? (
        <Quiet>{text.wait.none}</Quiet>
      ) : (
        <LineChart
          label={text.wait.label}
          labels={axis.labels}
          titles={axis.titles}
          series={series}
          height={180}
          ticks={durationTicks(max, 3)}
          axis={(s) => (s === 0 ? '0' : text.seconds(s))}
          format={text.seconds}
          ends
        />
      )}
      <Note>{text.wait.note}</Note>
    </Card>
  )
}

// 5. Reply outcomes: the number of turns per period that completed, were interrupted or stopped, or failed.
export function OutcomesCard({ report, axis }: { report: Report; axis: Axis }) {
  const { totals, series } = report.outcomes
  const runs = totals.completed + totals.interrupted + totals.failed
  const rate = runs ? text.percent(totals.completed / runs) : null
  const columns = [
    { key: 'completed', label: text.outcomes.completed, color: COLOR.done, values: series.map((b) => b.completed) },
    { key: 'interrupted', label: text.outcomes.interrupted, color: COLOR.interrupt, values: series.map((b) => b.interrupted) },
    { key: 'failed', label: text.outcomes.failed, color: COLOR.fail, values: series.map((b) => b.failed) },
  ]
  return (
    <Card
      title={text.outcomes.title}
      unit={text.outcomes.unit(axis.hourly)}
      aside={<Legend items={columns.map((c) => ({ label: c.label, color: c.color, kind: 'rect' }))} />}
    >
      <StackedColumns
        label={text.outcomes.label(rate)}
        labels={axis.shortLabels}
        titles={axis.titles}
        series={columns}
        height={180}
        format={(n) => text.count(n)}
        axis={(n) => String(n)}
        steps={[1, 2, 5]}
        totalLabel={text.total}
      />
      <Note>{text.outcomes.note(axis.rangeLabel, runs, rate)}</Note>
    </Card>
  )
}

const reasonText = (reason: Schemas['StatsOutcomeReason']) => (reason.code === null ? text.reasons.noCode : (text.reasons.codes[reason.code] ?? reason.code))

// Why replies didn't finish: reasons and counts for interrupted, stopped and failed turns.
export function ReasonsCard({ report }: { report: Report }) {
  const { reasons, totals } = report.outcomes
  const total = reasons.reduce((sum, r) => sum + r.count, 0)
  const topFailure = reasons.find((r) => r.kind === 'failed')
  return (
    <Card title={text.reasons.title} unit={total ? text.reasons.total(total) : undefined}>
      {reasons.length ? (
        <ul className="flex flex-col gap-1.5">
          {reasons.slice(0, 6).map((reason) => (
            <li
              key={`${reason.kind}:${reason.code}`}
              className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded-ticket border border-hairline bg-surface-subtle px-2.5 py-1.5 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-paper/60"
            >
              <span className="flex min-w-0 items-center gap-[7px]">
                <span
                  className={cn(
                    'shrink-0 rounded-stamp px-1.5 text-[11px] font-semibold [[data-skin=plate]_&]:rounded-[6px] [[data-skin=plate]_&]:font-black',
                    reason.kind === 'failed' ? 'bg-tint-red text-red-text' : 'bg-tint-yellow text-ink',
                  )}
                >
                  {text.reasons.kinds[reason.kind]}
                </span>
                <span className={cn('truncate text-[12.5px]', reason.code !== null && !(reason.code in text.reasons.codes) && 'font-mono')}>
                  {reasonText(reason)}
                </span>
              </span>
              <span className="text-right font-mono text-[12.5px] font-medium tabular-nums [[data-skin=plate]_&]:font-bold">{reason.count}</span>
            </li>
          ))}
        </ul>
      ) : (
        <Quiet>{text.reasons.none}</Quiet>
      )}
      {topFailure && totals.failed > 0 && <Note>{text.reasons.note(totals.failed, topFailure.count, reasonText(topFailure))}</Note>}
    </Card>
  )
}

const rateOr = (rate: number | null) => (rate === null ? text.none : text.percent(rate))

// Agent health: tight py-1.5 rows, Agent names link to chat, zero native scrollbars.
export function HealthCard({ report, scopeOf }: { report: Report; scopeOf: ScopeOf }) {
  const columns = text.health.columns
  const cell = 'px-2 py-1.5 text-right'
  return (
    <Card title={text.health.title}>
      {report.agents.length ? (
        <div className="-mx-1 overflow-x-auto px-1 no-scrollbar">
          <table aria-label={text.health.label} className="w-full min-w-[780px] border-collapse text-[12.5px] tabular-nums">
            <thead>
              <tr className="border-b border-hairline-strong text-[11.5px] font-medium text-ink-500 [[data-skin=plate]_&]:border-b-0 [[data-skin=plate]_&]:font-bold">
                <th scope="col" className="px-2 py-1.5 text-left font-medium [[data-skin=plate]_&]:font-bold">
                  {columns.name}
                </th>
                {[
                  columns.runs,
                  columns.completion,
                  columns.waitP90,
                  columns.durationP50,
                  columns.wakes,
                  columns.wakeTokens,
                  columns.context,
                  columns.compactions,
                  columns.tokens,
                  columns.cache,
                ].map((name) => (
                  <th key={name} scope="col" className="px-2 py-1.5 text-right font-medium [[data-skin=plate]_&]:font-bold">
                    {name}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {report.agents.map((agent) => {
                const scope = scopeOf(agent.projectId)
                return (
                  <tr key={agent.agentId} className="border-t border-hairline hover:bg-surface-hover/50 [[data-skin=plate]_&]:border-ink/10">
                    <th scope="row" className="max-w-[160px] truncate px-2 py-1.5 text-left font-medium [[data-skin=plate]_&]:font-bold">
                      {scope ? (
                        <Link
                          to={agentPath(scope, agent.agentId, 'chat')}
                          className="rounded-sm text-ink underline-offset-2 outline-none hover:underline focus-visible:shadow-focus [[data-skin=plate]_&]:hover:text-riso-blue"
                        >
                          {agent.name}
                        </Link>
                      ) : (
                        agent.name
                      )}
                    </th>
                    <td className={cell}>{agent.runs}</td>
                    <td
                      className={cn(
                        cell,
                        'font-medium [[data-skin=plate]_&]:font-bold',
                        agent.completionRate === null ? '' : agent.completionRate >= 0.95 ? 'text-green-text' : 'text-red-text',
                      )}
                    >
                      {rateOr(agent.completionRate)}
                    </td>
                    <td className={cell}>{agent.waitP90 === null ? text.none : text.seconds(agent.waitP90)}</td>
                    <td className={cell}>{agent.durationP50 === null ? text.none : text.clock(agent.durationP50)}</td>
                    <td className={cell}>{agent.wakes}</td>
                    <td className={cell}>{agent.wakes ? text.wan(agent.wakeTokens) : text.none}</td>
                    <td className={cell}>{agent.avgContext === null ? text.none : text.wan(agent.avgContext)}</td>
                    <td className={cell}>{agent.compactions}</td>
                    <td className={cell}>{text.wan(agent.tokens)}</td>
                    <td className={cell}>{agent.cacheHitRate === null ? text.none : text.wholePercent(agent.cacheHitRate)}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <Quiet>{text.health.none}</Quiet>
      )}
    </Card>
  )
}

// Tool calls: tools ranked by call count, each with its failure rate.
export function ToolsCard({ report }: { report: Report }) {
  const { byName, callsPerRunAvg, callsPerRunMax } = report.tools
  const tools = byName.slice(0, 6)
  const max = tools[0]?.calls ?? 0
  return (
    <Card title={text.tools.title} unit={text.tools.unit}>
      {tools.length ? (
        <ul className="flex flex-col gap-1.5">
          {tools.map((tool) => {
            const rate = tool.calls ? text.percent(tool.failures / tool.calls) : text.none
            return (
              <li key={tool.name} className="grid grid-cols-[minmax(44px,max-content)_minmax(0,1fr)_auto_44px] items-center gap-2">
                <code className="max-w-[110px] truncate font-mono text-[12px] font-medium [[data-skin=plate]_&]:font-bold">{tool.name}</code>
                <Bar value={tool.calls} max={max} color={COLOR.blue} height="h-2.5" />
                <b className="text-right font-mono text-[12px] tabular-nums">{text.number(tool.calls)}</b>
                <span title={text.tools.failureRate(tool.name, rate)} className="text-right font-mono text-[11.5px] text-ink-700 tabular-nums">
                  {rate}
                </span>
              </li>
            )
          })}
        </ul>
      ) : (
        <Quiet>{text.tools.none}</Quiet>
      )}
      {callsPerRunAvg !== null && callsPerRunMax !== null && <Note>{text.tools.note(callsPerRunAvg, callsPerRunMax)}</Note>}
    </Card>
  )
}

// Process diagnostics (trimmed: model share & cost moved to ModelCostCard on Usage & Cost tab).
export function ProcessCard({ report }: { report: Report }) {
  const { modelRetries, piExits, piColdStarts, piColdStartAvgMs } = report.process
  const rows: { label: string; value: string; note?: string }[] = [
    { label: text.process.retries, value: text.count(modelRetries) },
    { label: text.process.piExits, value: text.count(piExits), note: text.process.piExitsNote },
    {
      label: text.process.coldStarts,
      value: text.count(piColdStarts),
      note: piColdStartAvgMs === null ? undefined : text.process.coldStartAvg(text.process.coldStartSeconds(piColdStartAvgMs)),
    },
  ]
  return (
    <Card title={text.process.title}>
      <dl className="flex flex-col">
        {rows.map((row) => (
          <div
            key={row.label}
            className="flex flex-wrap items-baseline gap-x-2 border-b border-hairline py-2 first:pt-0 last:border-b-0 [[data-skin=plate]_&]:border-ink/10"
          >
            <dt className="text-[12.5px] text-ink-700">{row.label}</dt>
            <dd className="ml-auto font-display-latin text-[16px] font-semibold tabular-nums [[data-skin=plate]_&]:text-[17px] [[data-skin=plate]_&]:font-bold">
              {row.value}
            </dd>
            {row.note && <dd className="w-full text-[11.5px] text-ink-500">{row.note}</dd>}
          </div>
        ))}
      </dl>
    </Card>
  )
}

// How busy the day gets: average concurrent replies per hour in server local time.
export function BusyCard({ report }: { report: Report }) {
  const values = report.concurrency
  const known = values.filter((v): v is number => v !== null)
  const peakValue = known.length ? Math.max(...known) : null
  const peakIndex = peakValue === null ? -1 : values.indexOf(peakValue)
  const hours = values.map((_, h) => text.hour(h))
  const peakText = peakValue !== null && peakValue > 0 ? text.busy.peak(peakIndex, text.busy.value(peakValue)) : null
  return (
    <Card title={text.busy.title} unit={text.busy.unit}>
      <LineChart
        label={text.busy.label(peakText)}
        labels={hours}
        titles={hours}
        series={[{ key: 'busy', label: text.busy.series, color: COLOR.blue, values }]}
        height={170}
        ticks={niceTicks(peakValue ?? 0, 5, [1, 2, 5])}
        axis={(v) => text.busy.value(v)}
        format={text.busy.value}
        peak={peakText ? { index: peakIndex, text: peakText } : undefined}
        labelAt={(hour) => hour % 6 === 0 || hour === 23}
        area
      />
    </Card>
  )
}
