import { useId, useState, type ReactNode } from 'react'
import { SelectChevron } from '@/components/SelectChevron'
import type { Schemas } from '@/api/client'
import { STATS_RANGES, statsWindow, useStats, type StatsRange } from '@/api/stats'
import { SectionError } from '@/components/RequestError'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useOverviewFilter, type OverviewFilter } from '@/features/projects/overview-filter'
import { OverviewScopeNotFound } from '@/features/projects/OverviewScope'
import { t } from '@/i18n'
import { HollowNumber } from '@/components/NumberedSection'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet } from '@/components/Sheet'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { PLATFORM, type ScopeQuery } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { Spark } from './charts'
import { CardNumber } from './card-number'
import { estimateCost } from './pricing'
import {
  BusyCard,
  ContextCard,
  HealthCard,
  OriginsCard,
  OutcomesCard,
  ProcessCard,
  QuestionsCard,
  REOPEN_CONTEXT,
  ReasonsCard,
  ToolsCard,
  WaitCard,
  WakesCard,
  type Axis,
  type ScopeOf,
} from './StatsCards'

const text = t.stats

type Report = Schemas['StatsReport']

const sameDay = (a: Date, b: Date) => a.toDateString() === b.toDateString()

// 横轴标签：按小时为“15 点”；按天为“10/21”，回复结果图用更短的“21”；今天写“今天”。
function axisOf(report: Report, range: StatsRange): Axis {
  const today = new Date()
  const hourly = report.bucket === 'hour'
  const starts = report.buckets.map((start) => new Date(start))
  const labels = starts.map((d) => (hourly ? text.hour(d.getHours()) : sameDay(d, today) ? text.today : text.day(d.getMonth() + 1, d.getDate())))
  const shortLabels = starts.map((d, i) => (hourly || sameDay(d, today) ? labels[i] : text.shortDay(d.getDate())))
  return { labels, shortLabels, titles: labels, hourly, rangeLabel: text.ranges[range] }
}

// 这段时间什么都没发生：没有用量、没有已结束的回合、没有提问。
const isEmpty = (report: Report) =>
  report.tokens.totals.total === 0 &&
  report.outcomes.totals.completed + report.outcomes.totals.interrupted + report.outcomes.totals.failed === 0 &&
  report.questions.asked === 0

// tone：投影颜色区分含义——中性藏青，失败与中断荧光粉，等你回答黄，合计孔版蓝（改版）。
type Tile = { label: string; value: string; delta: string; bad: boolean; spark: (number | null)[]; tone: 'ink' | 'pink' | 'yellow' | 'blue' }

function tiles(report: Report): Tile[] {
  const { tokens, byOrigin, context, wait, outcomes, questions } = report
  const wakes = report.wakes.reduce((sum, w) => sum + w.count, 0)
  const topCompactions = [...context.agents].sort((a, b) => b.compactions - a.compactions)[0]
  const cost = estimateCost(tokens)
  return [
    {
      label: text.kpis.unrequested,
      value: byOrigin.unrequestedShare === null ? text.none : text.wholePercent(byOrigin.unrequestedShare),
      delta: text.kpis.unrequestedDelta(wakes, text.wan(byOrigin.totals.sleepWake)),
      bad: (byOrigin.unrequestedShare ?? 0) >= 0.5,
      spark: byOrigin.series.map((b) => {
        const total = b.console + b.inboxReminder + b.sleepWake + b.task + b.other
        return total ? b.sleepWake / total : null
      }),
      tone: 'ink',
    },
    {
      label: text.kpis.context,
      value: context.avgInputPerCall === null ? text.none : text.wan(context.avgInputPerCall),
      delta: text.kpis.contextDelta(context.compactions, topCompactions?.compactions ? topCompactions : null),
      bad: context.agents.some((a) => a.avgInputPerCall > REOPEN_CONTEXT),
      spark: context.series,
      tone: 'ink',
    },
    {
      label: text.kpis.wait,
      value: wait.p50 === null ? text.none : text.seconds(wait.p50),
      delta: wait.p90 === null ? '' : text.kpis.waitDelta(text.seconds(wait.p90)),
      bad: false,
      spark: wait.series.map((b) => b.p50),
      tone: 'ink',
    },
    {
      label: text.kpis.failures,
      value: text.count(outcomes.totals.interrupted + outcomes.totals.failed),
      delta: text.kpis.failuresDelta(outcomes.totals.failed, outcomes.totals.interrupted),
      bad: false,
      spark: outcomes.series.map((b) => b.interrupted + b.failed),
      tone: 'pink',
    },
    {
      label: text.kpis.questions,
      value: text.items(questions.asked),
      delta: text.kpis.questionsDelta(questions.avgAnswerSeconds === null ? null : text.minutes(questions.avgAnswerSeconds), questions.pending.length),
      bad: questions.pending.length > 0,
      spark: questions.series,
      tone: 'yellow',
    },
    {
      label: text.kpis.tokens,
      value: text.wan(tokens.totals.total),
      delta: text.kpis.tokensDelta(cost.priced ? text.usd(cost.usd) : null, tokens.cacheHitRate === null ? null : text.wholePercent(tokens.cacheHitRate)),
      bad: false,
      spark: tokens.series,
      tone: 'blue',
    },
  ]
}

const TILE_SHADOW = {
  ink: 'shadow-[4px_4px_0_var(--ink)]',
  pink: 'shadow-[4px_4px_0_var(--riso-pink)]',
  yellow: 'shadow-[4px_4px_0_var(--riso-yellow)]',
  blue: 'shadow-[4px_4px_0_var(--riso-blue)]',
} as const

// 关键指标：纸上的第一个编号分区。白底、2px 藏青描边的卡片，投影颜色区分含义；数字是 Bricolage 大字。
function Kpis({ report }: { report: Report }) {
  return (
    <div className="flex min-w-0 flex-col gap-[18px]">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5">
        <HollowNumber data-section-number>01</HollowNumber>
        <h2 className="riso-marker px-1.5 font-display-cn text-[30px] leading-[1.25] font-normal">{text.kpis.label}</h2>
      </div>
      <section aria-label={text.kpis.label} className="grid grid-cols-1 gap-x-3.5 gap-y-3 sm:grid-cols-2 lg:grid-cols-3">
        {tiles(report).map((tile) => (
          <div key={tile.label} className={cn('mr-1 mb-1 flex min-w-0 flex-col gap-1 rounded-card border-2 border-ink bg-white px-4 pt-3.5 pb-3', TILE_SHADOW[tile.tone])}>
            <span className="text-[12.5px] font-black text-ink">{tile.label}</span>
            <span className="font-display-latin text-[30px] leading-[1.1] font-extrabold [overflow-wrap:anywhere]">{tile.value}</span>
            <span className={cn('text-[12px] leading-[1.4]', tile.bad ? 'font-bold text-red-text' : 'text-ink-700')}>{tile.delta}</span>
            <Spark values={tile.spark} className="mt-auto" />
          </div>
        ))}
      </section>
    </div>
  )
}

// 范围筛选：全部项目、各项目、平台，写在地址里（与概览相同的 ?project= / ?platform=1），默认全部。
function ScopeSelect({ overview }: { overview: ReturnType<typeof useOverviewFilter> }) {
  const { filter, projects } = overview
  const value = filter.kind === 'all' ? '' : filter.kind === 'platform' ? PLATFORM : `project:${filter.name}`
  const id = useId()
  // 改版：与概览同样的虚线框，里面写着“范围 当前范围”，右边一个小箭头；仍是原生下拉。
  return (
    <span className="relative inline-flex h-[26px] max-w-full min-w-0 items-center gap-2 rounded-[4px] border-[1.5px] border-dashed border-ink/45 pr-1.5 pl-2.5 text-[12.5px] font-bold text-ink focus-within:shadow-focus">
      <label htmlFor={id} aria-hidden="true" className="shrink-0 text-ink-500">
        {text.scopeLabel}
      </label>
      <select
        id={id}
        aria-label={text.scopeLabel}
        value={value}
        onChange={(event) => overview.select(event.target.value)}
        className="max-w-[200px] min-w-0 cursor-pointer appearance-none truncate bg-transparent pr-[18px] font-bold text-ink outline-none"
      >
        <option value="">{text.allProjects}</option>
        {filter.kind === 'project' && !filter.project && (
          <option value={value} disabled>
            {filter.project === null ? text.missingProject(filter.name) : filter.name}
          </option>
        )}
        <optgroup label={text.projectGroup}>
          {(projects ?? []).map((project) => (
            <option key={project.id} value={`project:${project.name}`}>
              {project.name}
            </option>
          ))}
        </optgroup>
        <option value={PLATFORM}>{text.platform}</option>
      </select>
      <SelectChevron />
    </span>
  )
}

const scopeName = (filter: OverviewFilter) => (filter.kind === 'all' ? text.global : filter.kind === 'platform' ? text.platform : filter.name)

// 统计页：第一屏是六个关键指标与最该知道的几件事，虚线以下是排查时再看的明细。
// 时间范围：今天按小时，近 7 天、近 30 天按天；范围与概览一样可选全部、某个项目或平台。
// 改版：页头是共用的页面标题（旁边是分段的时间范围、虚线框里的范围与时间窗口）；内容排在一张纸上，按 01、02…编号。
export function StatsPage() {
  const overview = useOverviewFilter()
  const [range, setRange] = useState<StatsRange>('week')
  const { from } = statsWindow(range)
  const eyebrow = range === 'today' ? text.today : text.window(text.day(from.getMonth() + 1, from.getDate()), text.today)
  const { filter } = overview
  return (
    <PageFrame>
      <PageHeader crumbs={[{ label: t.shell.global }]} title={text.title}>
        <ToggleGroup aria-label={text.rangeLabel} className={segmentedClass} value={range} onValueChange={(value) => setRange(value as StatsRange)}>
          {STATS_RANGES.map((item) => (
            <ToggleGroupItem key={item} value={item} className={segmentedItemClass}>
              {text.ranges[item]}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <ScopeSelect overview={overview} />
        <span className="font-mono text-[12px] text-ink-500">{`${scopeName(filter)} · ${eyebrow}`}</span>
      </PageHeader>
      <Sheet className="mt-2 flex-1">
        {/* 滚动区是定位容器：里面绝对定位的元素（如 sr-only 说明）随内容滚动，不会按未滚动时的位置把整页撑高。 */}
        <div className="relative min-h-0 flex-1 overflow-y-auto">
          <div className="flex max-w-[1480px] flex-col gap-6 pt-6 pr-[clamp(14px,3vw,40px)] pb-16 pl-[clamp(12px,2vw,30px)]">
            {overview.query ? (
              <StatsBody key={overview.key} range={range} query={overview.query} projects={overview.projects} />
            ) : (
              filter.kind === 'project' && filter.project === null && <OverviewScopeNotFound name={filter.name} onBack={() => overview.select('')} />
            )}
          </div>
        </div>
      </Sheet>
    </PageFrame>
  )
}

function StatsBody({ range, query, projects }: { range: StatsRange; query: ScopeQuery; projects: Schemas['Project'][] | undefined }) {
  const stats = useStats(range, query)
  // Agent 所在的作用域：平台 Agent 的 projectId 为 null；项目不在列表里时不给链接。
  const scopeOf: ScopeOf = (projectId) => {
    if (projectId === null) return { kind: 'platform' }
    const project = projects?.find((p) => p.id === projectId)
    return project ? { kind: 'project', project } : null
  }

  if (stats.isError && !stats.data) return <SectionError title={text.title} error={stats.error} onRetry={() => void stats.refetch()} />
  if (!stats.data)
    return (
      <div aria-busy="true" className="flex flex-col gap-3">
        <span className="riso-skeleton h-[120px] rounded-panel" />
        <span className="riso-skeleton h-[260px] rounded-panel" />
      </div>
    )
  const report = stats.data
  if (isEmpty(report))
    return (
      <p role="status" className="rounded-panel border-2 border-dashed border-ink/25 px-5 py-8 text-center text-[14px] font-bold text-ink-700">
        {text.empty}
      </p>
    )
  const axis = axisOf(report, range)
  // 每张图是纸上的一个编号分区，按排列顺序编号（01 是关键指标）。
  const n = (number: number, card: ReactNode) => <CardNumber.Provider value={number}>{card}</CardNumber.Provider>
  return (
    <div className={cn('flex flex-col gap-6', stats.isPlaceholderData && 'opacity-60')} aria-busy={stats.isPlaceholderData || undefined}>
      <Kpis report={report} />
      <div className="grid gap-x-8 gap-y-6 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        {n(2, <OriginsCard report={report} axis={axis} />)}
        {n(3, <WakesCard report={report} scopeOf={scopeOf} />)}
      </div>
      <div className="grid gap-x-8 gap-y-6 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        {n(4, <ContextCard report={report} />)}
        {n(5, <QuestionsCard report={report} axis={axis} scopeOf={scopeOf} />)}
      </div>
      <div className="grid gap-x-8 gap-y-6 md:grid-cols-2 xl:grid-cols-3">
        {n(6, <WaitCard report={report} axis={axis} />)}
        {n(7, <OutcomesCard report={report} axis={axis} />)}
        {n(8, <ReasonsCard report={report} />)}
      </div>
      {/* 虚线以下是排查时再看的明细。 */}
      <p className="mt-2 self-start rounded-full bg-ink px-3 text-[12.5px] leading-6 font-black text-white">{text.more}</p>
      {n(9, <HealthCard report={report} />)}
      <div className="grid gap-x-8 gap-y-6 md:grid-cols-2 xl:grid-cols-3">
        {n(10, <ToolsCard report={report} />)}
        {n(11, <ProcessCard report={report} />)}
        {n(12, <BusyCard report={report} />)}
      </div>
    </div>
  )
}
