import { useId, type ReactNode } from 'react'
import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { useNow } from '@/lib/use-now'
import { agentPath, type Scope } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { sectionId } from '@/features/agent-config/sections'
import { Bar, Legend, LineChart, StackedColumns } from './charts'
import { durationTicks, niceTicks } from './ticks'
import { estimateCost, modelFamily, type ModelFamily } from './pricing'

const text = t.stats

type Report = Schemas['StatsReport']
export type ScopeOf = (projectId: string | null) => Scope | null
// 横轴：每个桶的短标签与提示框标题，由页面按桶宽算好传进来。
export type Axis = { labels: string[]; shortLabels: string[]; titles: string[]; hourly: boolean; rangeLabel: string }

// 平均上下文超过这个数（token）就该考虑让它重开会话。
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

export function Card({ title, unit, aside, children, className }: { title: string; unit?: ReactNode; aside?: ReactNode; children: ReactNode; className?: string }) {
  const titleId = useId()
  return (
    <section aria-labelledby={titleId} className={cn('flex min-w-0 flex-col gap-2.5 rounded-panel border-2 border-ink bg-white px-4 py-3.5', className)}>
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1.5">
        <h2 id={titleId} className="font-display-cn text-[22px] leading-tight font-normal">
          {title}
        </h2>
        {unit && <span className="text-[12px] font-bold text-ink-500">{unit}</span>}
        {aside && <div className="sm:ml-auto">{aside}</div>}
      </div>
      {children}
    </section>
  )
}

const Note = ({ children }: { children: ReactNode }) => <p className="text-[11.5px] leading-[1.5] text-ink-500">{children}</p>
const Quiet = ({ children }: { children: ReactNode }) => <p className="py-2 text-[12.5px] text-ink-500">{children}</p>

// 1. 每天（今天是每小时）的用量，按触发来源堆叠：你发的消息、定时唤醒、Agent 互相提醒与任务、其他。
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
        height={230}
        format={(wan) => text.wan(Math.round(wan * 10_000))}
        axis={text.wanAxis}
        tops={(wan) => text.wanAxis(Math.round(wan))}
        totalLabel={text.total}
      />
    </Card>
  )
}

// 唤醒的频率：按相邻两次的平均间隔写成“每 4 小时醒一次”。
function cadence(wake: Schemas['StatsAgentWakes']) {
  const s = wake.avgIntervalSeconds
  if (s === null) return text.wakes.once
  const interval =
    s < 3600 ? text.wakes.everyMinutes(Math.max(1, Math.round(s / 60))) : s < 36 * 3600 ? text.wakes.everyHours(Math.round(s / 3600)) : text.wakes.everyDays(Math.round(s / 86400))
  return text.wakes.every(interval)
}

// 谁在睡醒后烧钱：按唤醒消耗排的 Agent；底部说明消耗最多的那个每次醒来用多少，链接到它的提示词（睡多久由它自己决定）。
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
          <ul className="flex flex-col gap-2">
            {wakes.slice(0, 5).map((wake, i) => (
              <li key={wake.agentId} className={cn('flex flex-col gap-1 rounded-ticket px-2.5 py-2', i === 0 ? 'bg-tint-pink-2' : 'bg-paper/60')}>
                <span className="flex min-w-0 items-baseline gap-2">
                  <span className="truncate font-black">{wake.name}</span>
                  <span className="min-w-0 truncate text-[12px] text-ink-700">{text.wakes.row(cadence(wake), text.wan(wake.avgTokens))}</span>
                  <span className="ml-auto shrink-0 font-bold tabular-nums">{text.wan(wake.tokens)}</span>
                </span>
                <Bar value={wake.tokens} max={top.tokens} color={COLOR.pink} height="h-2.5" />
              </li>
            ))}
          </ul>
          <div className="mt-auto flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-ink-700">
            <span aria-hidden="true" className="inline-flex size-[18px] shrink-0 items-center justify-center rounded-full bg-[var(--status-interrupt)] text-[12px] font-black text-ink">
              !
            </span>
            <span className="min-w-0 flex-1">{text.wakes.note(top.name, text.wan(top.avgTokens), times !== null && times >= 2 ? times : null)}</span>
            {topScope && (
              <Link
                to={`${agentPath(topScope, top.agentId, 'config')}#${sectionId('prompt')}`}
                className="rounded-sm font-bold whitespace-nowrap text-riso-blue underline outline-none focus-visible:shadow-focus"
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

// 2. 每次模型调用送进去多少上下文，按 Agent；平均超过 12 万的标“该重开”。
export function ContextCard({ report }: { report: Report }) {
  const agents = report.context.agents.slice(0, 6)
  const max = agents[0]?.avgInputPerCall ?? 0
  return (
    <Card title={text.context.title} unit={text.context.unit}>
      {agents.length ? (
        <ul className="flex flex-col gap-2">
          {agents.map((agent) => (
            <li key={agent.agentId} className="grid grid-cols-[minmax(48px,72px)_minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-0.5 sm:grid-cols-[minmax(48px,72px)_minmax(0,1fr)_54px_118px]">
              <span className="truncate font-bold">{agent.name}</span>
              <Bar value={agent.avgInputPerCall} max={max} color={COLOR.blue} />
              <span className="text-right font-bold tabular-nums">{text.wan(agent.avgInputPerCall)}</span>
              <span className="col-span-3 flex items-center gap-1.5 text-[12px] whitespace-nowrap text-ink-700 sm:col-span-1">
                {text.context.compactions(agent.compactions)}
                {agent.avgInputPerCall > REOPEN_CONTEXT && (
                  <span title={text.context.reopenTitle} className="rounded-[6px] bg-[var(--status-interrupt)] px-1.5 text-[11px] font-black text-ink">
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

// 3. 等你回答：时间窗内问了多少、你平均多久回、最久的一次；下面是现在还开着的问题，点开到那个 Agent 的对话。
export function QuestionsCard({ report, axis, scopeOf }: { report: Report; axis: Axis; scopeOf: ScopeOf }) {
  const { asked, avgAnswerSeconds, maxAnswerSeconds, pending } = report.questions
  const now = useNow(60_000)
  const stats = [
    { label: text.questions.asked(axis.rangeLabel), value: text.items(asked) },
    { label: text.questions.avg, value: avgAnswerSeconds === null ? text.none : text.minutes(avgAnswerSeconds) },
    { label: text.questions.max, value: maxAnswerSeconds === null ? text.none : text.minutes(maxAnswerSeconds) },
  ]
  return (
    <Card title={text.questions.title} unit={text.questions.unit}>
      <dl className="grid grid-cols-3 gap-2">
        {stats.map((stat) => (
          <div key={stat.label} className="flex min-w-0 flex-col gap-0.5 rounded-ticket bg-paper/60 px-2.5 py-2">
            <dt className="text-[11.5px] leading-snug text-ink-700">{stat.label}</dt>
            <dd className="text-[18px] leading-[1.1] font-bold">{stat.value}</dd>
          </div>
        ))}
      </dl>
      <h3 className="text-[12px] font-bold text-ink-700">{text.questions.pending}</h3>
      {pending.length ? (
        <ul className="flex flex-col gap-2">
          {pending.slice(0, 4).map((question) => {
            const scope = scopeOf(question.projectId)
            const body = (
              <>
                <span className="rounded-[6px] bg-riso-pink px-1.5 text-[11px] font-black text-ink">{question.name}</span>
                <span className="truncate">{question.text}</span>
                <span className="text-[11.5px] font-bold whitespace-nowrap text-red-text">
                  {text.questions.waited(text.minutes(Math.max(0, (now - Date.parse(question.askedAt)) / 1000)))}
                </span>
              </>
            )
            const row = 'grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 rounded-ticket border-2 border-ink bg-white px-2.5 py-[7px] shadow-[2px_2px_0_var(--ink)]'
            return (
              <li key={question.messageId}>
                {scope ? (
                  <Link to={agentPath(scope, question.agentId, 'chat')} className={cn(row, 'outline-none hover:bg-tint-yellow focus-visible:shadow-focus')}>
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

// 4. 发出去多久开始回：你发的消息从排入到开始回复的 p50 与 p90，含排队。
export function WaitCard({ report, axis }: { report: Report; axis: Axis }) {
  const series = [
    { key: 'p50', label: text.wait.p50Short, color: COLOR.blue, values: report.wait.series.map((b) => b.p50) },
    { key: 'p90', label: text.wait.p90Short, color: COLOR.pink, values: report.wait.series.map((b) => b.p90) },
  ]
  const max = Math.max(0, ...series.flatMap((s) => s.values.filter((v) => v !== null)))
  return (
    <Card title={text.wait.title}>
      <Legend
        items={[
          { label: text.wait.p50, color: COLOR.blue, kind: 'line' },
          { label: text.wait.p90, color: COLOR.pink, kind: 'line' },
        ]}
      />
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

// 5. 回复结果：每个时段完成、被打断或停止、失败的回合数。
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
    <Card title={text.outcomes.title} unit={text.outcomes.unit(axis.hourly)}>
      <Legend items={columns.map((c) => ({ label: c.label, color: c.color, kind: 'rect' }))} />
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

// 没完成的原因：打断、停止、失败各自的原因与次数；失败最多的原因写在底部。
export function ReasonsCard({ report }: { report: Report }) {
  const { reasons, totals } = report.outcomes
  const total = reasons.reduce((sum, r) => sum + r.count, 0)
  const topFailure = reasons.find((r) => r.kind === 'failed')
  return (
    <Card title={text.reasons.title} unit={total ? text.reasons.total(total) : undefined}>
      {reasons.length ? (
        <ul className="flex flex-col gap-1.5">
          {reasons.slice(0, 6).map((reason) => (
            <li key={`${reason.kind}:${reason.code}`} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded-ticket bg-paper/60 px-2.5 py-1.5">
              <span className="flex min-w-0 items-center gap-[7px]">
                <span
                  className={cn(
                    'shrink-0 rounded-[6px] px-1.5 text-[11px] font-black',
                    reason.kind === 'failed' ? 'bg-tint-red text-red-text' : 'bg-tint-yellow text-ink',
                  )}
                >
                  {text.reasons.kinds[reason.kind]}
                </span>
                <span className={cn('truncate text-[12.5px]', reason.code !== null && !(reason.code in text.reasons.codes) && 'font-mono')}>{reasonText(reason)}</span>
              </span>
              <span className="text-right font-bold tabular-nums">{reason.count}</span>
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

// Agent 体检：每个 Agent 一行，窄屏时表格在卡片里横向滚动。完成率不到 95% 标红。
export function HealthCard({ report }: { report: Report }) {
  const columns = text.health.columns
  const cell = 'px-2 py-2 text-right'
  return (
    <Card title={text.health.title}>
      {report.agents.length ? (
        <div className="-mx-1 overflow-x-auto px-1 [scrollbar-width:thin]">
          <table aria-label={text.health.label} className="w-full min-w-[820px] border-collapse text-[12.5px] tabular-nums">
            <thead>
              <tr className="text-[11.5px] font-bold text-ink-500">
                <th scope="col" className="px-2 py-1.5 text-left font-bold">
                  {columns.name}
                </th>
                {[columns.runs, columns.completion, columns.waitP90, columns.durationP50, columns.wakes, columns.wakeTokens, columns.context, columns.compactions, columns.tokens, columns.cache].map(
                  (name) => (
                    <th key={name} scope="col" className="px-2 py-1.5 text-right font-bold">
                      {name}
                    </th>
                  ),
                )}
              </tr>
            </thead>
            <tbody>
              {report.agents.map((agent) => (
                <tr key={agent.agentId} className="border-t border-ink/10">
                  <th scope="row" className="max-w-[160px] truncate px-2 py-2 text-left font-bold">
                    {agent.name}
                  </th>
                  <td className={cell}>{agent.runs}</td>
                  <td className={cn(cell, 'font-bold', agent.completionRate === null ? '' : agent.completionRate >= 0.95 ? 'text-green-text' : 'text-red-text')}>
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
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <Quiet>{text.health.none}</Quiet>
      )}
    </Card>
  )
}

// 工具调用：按次数排的工具与各自的失败率。
export function ToolsCard({ report }: { report: Report }) {
  const { byName, callsPerRunAvg, callsPerRunMax } = report.tools
  const tools = byName.slice(0, 6)
  const max = tools[0]?.calls ?? 0
  return (
    <Card title={text.tools.title} unit={text.tools.unit}>
      {tools.length ? (
        <ul className="flex flex-col gap-2">
          {tools.map((tool) => {
            const rate = tool.calls ? text.percent(tool.failures / tool.calls) : text.none
            return (
              <li key={tool.name} className="grid grid-cols-[minmax(44px,max-content)_minmax(0,1fr)_auto_44px] items-center gap-2">
                <code className="max-w-[110px] truncate font-mono text-[12px] font-bold">{tool.name}</code>
                <Bar value={tool.calls} max={max} color={COLOR.blue} height="h-3" />
                <b className="text-right text-[12px] tabular-nums">{text.number(tool.calls)}</b>
                <span title={text.tools.failureRate(tool.name, rate)} className="text-right text-[11.5px] text-ink-700 tabular-nums">
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

// 按模型系列（Claude、GPT、其他）汇总 token 占比，从多到少。
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
    .map(([family, n]) => text.process.share(text.process.families[family], text.wholePercent(total ? n / total : 0)))
}

// 模型与进程：模型重试、Pi 异常退出与冷启动、按模型的 token 占比与估算花费。
export function ProcessCard({ report }: { report: Report }) {
  const { modelRetries, piExits, piColdStarts, piColdStartAvgMs } = report.process
  const shares = familyShares(report.tokens)
  const cost = estimateCost(report.tokens)
  const rows: { label: string; value: string; note?: string; extra?: string }[] = [
    { label: text.process.retries, value: text.count(modelRetries) },
    { label: text.process.piExits, value: text.count(piExits), note: text.process.piExitsNote },
    {
      label: text.process.coldStarts,
      value: text.count(piColdStarts),
      note: piColdStartAvgMs === null ? undefined : text.process.coldStartAvg(text.process.coldStartSeconds(piColdStartAvgMs)),
    },
    { label: text.process.byModel, value: shares[0] ?? text.none, note: shares.length > 1 ? text.process.others(shares.slice(1)) : undefined },
    {
      label: text.process.cost,
      value: cost.priced ? text.usd(cost.usd) : text.none,
      note: cost.priced ? text.process.costNote : text.process.noPrice,
      extra: cost.unpriced.length ? text.process.unpriced(cost.unpriced) : undefined,
    },
  ]
  return (
    <Card title={text.process.title}>
      <dl className="flex flex-col">
        {rows.map((row) => (
          <div key={row.label} className="flex flex-wrap items-baseline gap-x-2 border-b border-ink/10 py-[7px] last:border-b-0">
            <dt className="text-[12.5px] text-ink-700">{row.label}</dt>
            <dd className="ml-auto text-[17px] font-bold">{row.value}</dd>
            {row.note && <dd className="text-[11.5px] text-ink-500">{row.note}</dd>}
            {row.extra && <dd className="w-full text-right text-[11.5px] text-ink-500">{row.extra}</dd>}
          </div>
        ))}
      </dl>
    </Card>
  )
}

// 一天里有多忙：服务端本地时间每个小时平均同时在回复的数目；还没到的小时为空。
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
