import { useState } from 'react'
import type { Schemas } from '@/api/client'
import { STATS_RANGES, statsWindow, useStats, type StatsRange } from '@/api/stats'
import { SectionError } from '@/components/RequestError'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useOverviewFilter, type OverviewFilter } from '@/features/projects/overview-filter'
import { OverviewScopeNotFound } from '@/features/projects/OverviewScope'
import { t } from '@/i18n'
import { pageTitleClass } from '@/lib/page-title'
import { PLATFORM, type ScopeQuery } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { Spark } from './charts'
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

type Tile = { label: string; value: string; delta: string; bad: boolean; spark: (number | null)[]; lead?: boolean }

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
      lead: true,
    },
    {
      label: text.kpis.context,
      value: context.avgInputPerCall === null ? text.none : text.wan(context.avgInputPerCall),
      delta: text.kpis.contextDelta(context.compactions, topCompactions?.compactions ? topCompactions : null),
      bad: context.agents.some((a) => a.avgInputPerCall > REOPEN_CONTEXT),
      spark: context.series,
    },
    {
      label: text.kpis.wait,
      value: wait.p50 === null ? text.none : text.seconds(wait.p50),
      delta: wait.p90 === null ? '' : text.kpis.waitDelta(text.seconds(wait.p90)),
      bad: false,
      spark: wait.series.map((b) => b.p50),
    },
    {
      label: text.kpis.failures,
      value: text.count(outcomes.totals.interrupted + outcomes.totals.failed),
      delta: text.kpis.failuresDelta(outcomes.totals.failed, outcomes.totals.interrupted),
      bad: false,
      spark: outcomes.series.map((b) => b.interrupted + b.failed),
    },
    {
      label: text.kpis.questions,
      value: text.items(questions.asked),
      delta: text.kpis.questionsDelta(questions.avgAnswerSeconds === null ? null : text.minutes(questions.avgAnswerSeconds), questions.pending.length),
      bad: questions.pending.length > 0,
      spark: questions.series,
    },
    {
      label: text.kpis.tokens,
      value: text.wan(tokens.totals.total),
      delta: text.kpis.tokensDelta(cost.priced ? text.usd(cost.usd) : null, tokens.cacheHitRate === null ? null : text.wholePercent(tokens.cacheHitRate)),
      bad: false,
      spark: tokens.series,
    },
  ]
}

function Kpis({ report }: { report: Report }) {
  return (
    <section aria-label={text.kpis.label} className="grid grid-cols-2 gap-2.5 md:grid-cols-3 xl:grid-cols-6">
      {tiles(report).map((tile) => (
        <div
          key={tile.label}
          className={cn(
            'flex min-w-0 flex-col gap-1 rounded-card border-2 border-ink bg-white px-3 pt-3 pb-2.5',
            tile.lead ? 'shadow-[3px_3px_0_var(--chart-pink)]' : 'shadow-[3px_3px_0_var(--riso-blue)]',
          )}
        >
          <span className="text-[12px] font-bold text-ink-700">{tile.label}</span>
          <span className="text-[24px] leading-[1.15] font-bold [overflow-wrap:anywhere]">{tile.value}</span>
          <span className={cn('text-[11.5px] leading-[1.35] font-bold', tile.bad ? 'text-red-text' : 'text-ink-500')}>{tile.delta}</span>
          <Spark values={tile.spark} className="mt-auto" />
        </div>
      ))}
    </section>
  )
}

// 范围筛选：全部项目、各项目、平台，写在地址里（与概览相同的 ?project= / ?platform=1），默认全部。
function ScopeSelect({ overview }: { overview: ReturnType<typeof useOverviewFilter> }) {
  const { filter, projects } = overview
  const value = filter.kind === 'all' ? '' : filter.kind === 'platform' ? PLATFORM : `project:${filter.name}`
  return (
    <select
      aria-label={text.scopeLabel}
      value={value}
      onChange={(event) => overview.select(event.target.value)}
      className="h-9 max-w-[220px] min-w-0 rounded-full border-2 border-ink bg-white pr-3 pl-3.5 text-[13px] font-bold text-ink outline-none focus-visible:shadow-focus"
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
  )
}

const scopeName = (filter: OverviewFilter) => (filter.kind === 'all' ? text.global : filter.kind === 'platform' ? text.platform : filter.name)

// 统计页：第一屏是六个关键指标与最该知道的几件事，虚线以下是排查时再看的明细。
// 时间范围：今天按小时，近 7 天、近 30 天按天；范围与概览一样可选全部、某个项目或平台。
export function StatsPage() {
  const overview = useOverviewFilter()
  const [range, setRange] = useState<StatsRange>('week')
  const { from } = statsWindow(range)
  const eyebrow = range === 'today' ? text.today : text.window(text.day(from.getMonth() + 1, from.getDate()), text.today)
  const { filter } = overview
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-[1480px] flex-col gap-4 px-4 pt-[22px] pb-16 sm:px-8">
        <header className="flex flex-wrap items-end gap-x-3.5 gap-y-3">
          <div className="flex flex-col gap-1.5">
            <span className="text-[12px] font-bold text-ink-500">{`${scopeName(filter)} · ${eyebrow}`}</span>
            <h1 className={pageTitleClass}>{text.title}</h1>
          </div>
          <div className="flex flex-wrap items-center gap-2.5 sm:ml-auto">
            <ToggleGroup aria-label={text.rangeLabel} value={range} onValueChange={(value) => setRange(value as StatsRange)} className="border-2 border-ink bg-white p-[3px]">
              {STATS_RANGES.map((item) => (
                <ToggleGroupItem key={item} value={item} className="min-h-7 px-3 text-[13px] data-[state=on]:shadow-none">
                  {text.ranges[item]}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
            <ScopeSelect overview={overview} />
          </div>
        </header>
        {overview.query ? (
          <StatsBody key={overview.key} range={range} query={overview.query} projects={overview.projects} />
        ) : (
          filter.kind === 'project' && filter.project === null && <OverviewScopeNotFound name={filter.name} onBack={() => overview.select('')} />
        )}
      </div>
    </div>
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
  return (
    <div className={cn('flex flex-col gap-4', stats.isPlaceholderData && 'opacity-60')} aria-busy={stats.isPlaceholderData || undefined}>
      <Kpis report={report} />
      <div className="grid gap-3.5 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <OriginsCard report={report} axis={axis} />
        <WakesCard report={report} scopeOf={scopeOf} />
      </div>
      <div className="grid gap-3.5 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <ContextCard report={report} />
        <QuestionsCard report={report} axis={axis} scopeOf={scopeOf} />
      </div>
      <div className="grid gap-3.5 md:grid-cols-2 xl:grid-cols-3">
        <WaitCard report={report} axis={axis} />
        <OutcomesCard report={report} axis={axis} />
        <ReasonsCard report={report} />
      </div>
      <div className="mt-1 flex items-center gap-3">
        <span className="font-display-cn text-[18px] whitespace-nowrap text-ink-500">{text.more}</span>
        <span aria-hidden="true" className="flex-1 border-t-2 border-dashed border-ink/25" />
      </div>
      <HealthCard report={report} />
      <div className="grid gap-3.5 md:grid-cols-2 xl:grid-cols-3">
        <ToolsCard report={report} />
        <ProcessCard report={report} />
        <BusyCard report={report} />
      </div>
    </div>
  )
}
