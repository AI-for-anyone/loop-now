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

// X-axis labels: hourly they read “15 点” (15:00); daily "10/21", with the shorter "21" in the reply outcomes chart; today
// reads “今天” (Today).
function axisOf(report: Report, range: StatsRange): Axis {
  const today = new Date()
  const hourly = report.bucket === 'hour'
  const starts = report.buckets.map((start) => new Date(start))
  const labels = starts.map((d) => (hourly ? text.hour(d.getHours()) : sameDay(d, today) ? text.today : text.day(d.getMonth() + 1, d.getDate())))
  const shortLabels = starts.map((d, i) => (hourly || sameDay(d, today) ? labels[i] : text.shortDay(d.getDate())))
  return { labels, shortLabels, titles: labels, hourly, rangeLabel: text.ranges[range] }
}

// Nothing happened in this period: no usage, no finished turns, no questions.
const isEmpty = (report: Report) =>
  report.tokens.totals.total === 0 &&
  report.outcomes.totals.completed + report.outcomes.totals.interrupted + report.outcomes.totals.failed === 0 &&
  report.questions.asked === 0

// tone: the shadow color tells the meaning apart: neutral navy, fluorescent pink for failures and interruptions, yellow for
// waiting for your answer, riso blue for totals (redesign).
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
  ink: '[[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink)]',
  pink: '[[data-skin=plate]_&]:shadow-[4px_4px_0_var(--riso-pink)]',
  yellow: '[[data-skin=plate]_&]:shadow-[4px_4px_0_var(--riso-yellow)]',
  blue: '[[data-skin=plate]_&]:shadow-[4px_4px_0_var(--riso-blue)]',
} as const

// Key metrics: the first numbered section on the sheet. Crisp surface-control cards with hairline borders; Plate keeps its 2px navy
// outline and solid colored offset shadows.
function Kpis({ report }: { report: Report }) {
  return (
    <div className="flex min-w-0 flex-col gap-[18px]">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5">
        <HollowNumber data-section-number>01</HollowNumber>
        <h2 className="riso-marker px-1.5 font-display-cn text-[30px] leading-[1.25] font-normal">{text.kpis.label}</h2>
      </div>
      <section aria-label={text.kpis.label} className="grid grid-cols-1 gap-x-3.5 gap-y-3 sm:grid-cols-2 lg:grid-cols-3">
        {tiles(report).map((tile) => (
          <div key={tile.label} className={cn('mr-1 mb-1 flex min-w-0 flex-col gap-1 rounded-card border border-hairline bg-surface-control px-4 pt-3.5 pb-3 shadow-surface [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink', TILE_SHADOW[tile.tone])}>
            <span className="text-[12.5px] font-semibold text-ink [[data-skin=plate]_&]:font-black">{tile.label}</span>
            <span className="font-display-latin text-[30px] leading-[1.1] font-extrabold [overflow-wrap:anywhere]">{tile.value}</span>
            <span className={cn('text-[12px] leading-[1.4]', tile.bad ? 'font-bold text-red-text' : 'text-ink-700')}>{tile.delta}</span>
            <Spark values={tile.spark} className="mt-auto" />
          </div>
        ))}
      </section>
    </div>
  )
}

// Scope filter: all projects, each project, or the platform, stored in the URL (the same ?project= / ?platform=1 as the
// overview); all by default.
function ScopeSelect({ overview }: { overview: ReturnType<typeof useOverviewFilter> }) {
  const { filter, projects } = overview
  const value = filter.kind === 'all' ? '' : filter.kind === 'platform' ? PLATFORM : `project:${filter.name}`
  const id = useId()
  // Redesign: the same dashed box as the overview, reading “范围” (Scope) followed by the current scope, with a small arrow on
  // the right; still a native select.
  return (
    <span className="relative inline-flex h-[26px] max-w-full min-w-0 items-center gap-2 rounded-[4px] border border-hairline-strong bg-surface-control pr-1.5 pl-2.5 text-[12.5px] font-bold text-ink focus-within:shadow-focus [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/45 [[data-skin=plate]_&]:bg-transparent">
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

// The stats page: the first screen holds the six key metrics and the few things most worth knowing; below the dashed line
// are the details to look at when troubleshooting.
// Time range: today by hour, the last 7 and last 30 days by day; as in the overview, the scope can be all, one project or
// the platform.
// Redesign: the header is the shared page title (next to it the segmented time range, the scope in a dashed box, and the time
// window); the content is laid out on one sheet, numbered 01, 02, ...
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
        {/* The scroll area is the positioning container: absolutely positioned elements inside it (such as sr-only text)
            scroll with the content instead of stretching the whole page based on their unscrolled position. */}
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
  // The scope an Agent lives in: a platform Agent's projectId is null; when the project isn't in the list, no link is given.
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
      <p role="status" className="rounded-panel border border-dashed border-hairline-strong px-5 py-8 text-center text-[14px] font-bold text-ink-700 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink/25">
        {text.empty}
      </p>
    )
  const axis = axisOf(report, range)
  // Each chart is a numbered section on the sheet, numbered in display order (01 is the key metrics).
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
      {/* Below the divider are the details to look at when troubleshooting. */}
      <p className="mt-2 self-start rounded-full bg-cta-bg px-3 text-[12.5px] leading-6 font-semibold text-cta-fg [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-white">{text.more}</p>
      {n(9, <HealthCard report={report} />)}
      <div className="grid gap-x-8 gap-y-6 md:grid-cols-2 xl:grid-cols-3">
        {n(10, <ToolsCard report={report} />)}
        {n(11, <ProcessCard report={report} />)}
        {n(12, <BusyCard report={report} />)}
      </div>
    </div>
  )
}
