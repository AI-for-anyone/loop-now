import { useSearchParams } from 'react-router'
import type { Schemas } from '@/api/client'
import { STATS_RANGES, statsWindow, useStats, type StatsRange } from '@/api/stats'
import { SubNav } from '@/components/IndexTabs'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { SectionError } from '@/components/RequestError'
import { PageBody, Sheet } from '@/components/Sheet'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useOverviewFilter, type OverviewFilter } from '@/features/projects/overview-filter'
import { OverviewScopeNotFound, OverviewScopeSelect } from '@/features/projects/OverviewScope'
import { t } from '@/i18n'
import { overviewPath, type ScopeQuery } from '@/lib/scope'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { Spark } from './charts'
import { estimateCost } from './pricing'
import {
  BusyCard,
  ContextCard,
  HealthCard,
  ModelCostCard,
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
type StatsTab = 'usage' | 'reliability' | 'diagnostics'

const STATS_TABS: readonly StatsTab[] = ['usage', 'reliability', 'diagnostics']

function parseStatsTab(raw: string | null): StatsTab {
  return STATS_TABS.includes(raw as StatsTab) ? (raw as StatsTab) : 'usage'
}

function parseStatsRange(raw: string | null): StatsRange {
  return STATS_RANGES.includes(raw as StatsRange) ? (raw as StatsRange) : 'week'
}

const sameDay = (a: Date, b: Date) => a.toDateString() === b.toDateString()

function axisOf(report: Report, range: StatsRange): Axis {
  const today = new Date()
  const hourly = report.bucket === 'hour'
  const starts = report.buckets.map((start) => new Date(start))
  const labels = starts.map((d) => (hourly ? text.hour(d.getHours()) : sameDay(d, today) ? text.today : text.day(d.getMonth() + 1, d.getDate())))
  const shortLabels = starts.map((d, i) => (hourly || sameDay(d, today) ? labels[i] : text.shortDay(d.getDate())))
  return { labels, shortLabels, titles: labels, hourly, rangeLabel: text.ranges[range] }
}

const isEmpty = (report: Report) =>
  report.tokens.totals.total === 0 &&
  report.outcomes.totals.completed + report.outcomes.totals.interrupted + report.outcomes.totals.failed === 0 &&
  report.questions.asked === 0

interface MetricItem {
  label: string
  value: string
  delta: string
  bad?: boolean
  spark?: (number | null)[]
}

// Single-row 4-column hairline MetricStrip (~76px tall).
function MetricStrip({ label, items }: { label: string; items: MetricItem[] }) {
  return (
    <section
      aria-label={label}
      className="grid grid-cols-1 divide-y divide-hairline overflow-hidden rounded-card border border-hairline bg-surface-control sm:grid-cols-2 lg:grid-cols-4 lg:divide-x lg:divide-y-0 [[data-skin=plate]_&]:mr-1 [[data-skin=plate]_&]:mb-1 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink)]"
    >
      {items.map((item) => (
        <div key={item.label} className="flex min-w-0 flex-col justify-center gap-0.5 px-4 py-2.5">
          <div className="flex items-center justify-between gap-2">
            <span className="truncate text-[11.5px] font-medium text-ink-500 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink">
              {item.label}
            </span>
            {item.spark && <Spark values={item.spark} className="h-[16px] w-14 shrink-0" />}
          </div>
          <span className="font-display-latin text-[22px] leading-[1.15] font-semibold text-ink tabular-nums [overflow-wrap:anywhere] [[data-skin=plate]_&]:text-[26px] [[data-skin=plate]_&]:font-extrabold">
            {item.value}
          </span>
          <span className={cn('truncate text-[11.5px] leading-snug', item.bad ? 'font-medium text-red-text [[data-skin=plate]_&]:font-bold' : 'text-ink-500')}>
            {item.delta}
          </span>
        </div>
      ))}
    </section>
  )
}

function usageMetrics(report: Report): MetricItem[] {
  const { tokens, byOrigin, context } = report
  const wakes = report.wakes.reduce((sum, w) => sum + w.count, 0)
  const topCompactions = [...context.agents].sort((a, b) => b.compactions - a.compactions)[0]
  const cost = estimateCost(tokens)
  const totalInput = tokens.totals.input + tokens.totals.cacheRead
  return [
    {
      label: text.kpis.tokens,
      value: text.wan(tokens.totals.total),
      delta: text.kpis.tokensCostDelta(cost.priced ? text.usd(cost.usd) : null),
      spark: tokens.series,
    },
    {
      label: text.kpis.unrequested,
      value: byOrigin.unrequestedShare === null ? text.none : text.wholePercent(byOrigin.unrequestedShare),
      delta: text.kpis.unrequestedDelta(wakes, text.wan(byOrigin.totals.sleepWake)),
      bad: (byOrigin.unrequestedShare ?? 0) >= 0.5,
      spark: byOrigin.series.map((b) => {
        const total = b.console + b.inboxReminder + b.sleepWake + b.task + b.other
        return total ? b.sleepWake / total : null
      }),
    },
    {
      label: text.kpis.context,
      value: context.avgInputPerCall === null ? text.none : text.wan(context.avgInputPerCall),
      delta: text.kpis.contextDelta(context.compactions, topCompactions?.compactions ? topCompactions : null),
      bad: context.agents.some((a) => a.avgInputPerCall > REOPEN_CONTEXT),
      spark: context.series,
    },
    {
      label: text.kpis.cacheHit,
      value: tokens.cacheHitRate === null ? text.none : text.wholePercent(tokens.cacheHitRate),
      delta: text.kpis.cacheHitDelta(text.wan(tokens.totals.cacheRead), text.wan(totalInput)),
    },
  ]
}

function reliabilityMetrics(report: Report): MetricItem[] {
  const { outcomes, wait, questions } = report
  const runs = outcomes.totals.completed + outcomes.totals.interrupted + outcomes.totals.failed
  const completionRate = runs ? outcomes.totals.completed / runs : null
  return [
    {
      label: text.kpis.completion,
      value: completionRate === null ? text.none : text.percent(completionRate),
      delta: text.kpis.completionDelta(runs),
      bad: completionRate !== null && completionRate < 0.95,
    },
    {
      label: text.kpis.failures,
      value: text.count(outcomes.totals.interrupted + outcomes.totals.failed),
      delta: text.kpis.failuresDelta(outcomes.totals.failed, outcomes.totals.interrupted),
      bad: outcomes.totals.failed > 0,
      spark: outcomes.series.map((b) => b.interrupted + b.failed),
    },
    {
      label: text.kpis.wait,
      value: wait.p50 === null ? text.none : text.seconds(wait.p50),
      delta: wait.p90 === null ? text.none : text.kpis.waitDelta(text.seconds(wait.p90)),
      spark: wait.series.map((b) => b.p50),
    },
    {
      label: text.kpis.questions,
      value: text.items(questions.asked),
      delta: text.kpis.questionsDelta(
        questions.avgAnswerSeconds === null ? null : text.minutes(questions.avgAnswerSeconds),
        questions.pending.length,
      ),
      bad: questions.pending.length > 0,
      spark: questions.series,
    },
  ]
}

const scopeName = (filter: OverviewFilter) => (filter.kind === 'all' ? text.global : filter.kind === 'platform' ? text.platform : filter.name)
const scopeHref = (filter: OverviewFilter) =>
  filter.kind === 'project' ? overviewPath({ kind: 'project', name: filter.name }) : filter.kind === 'platform' ? '/?platform=1' : '/'

// Stats & Diagnostics workspace: URL-backed ?tab=usage|reliability|diagnostics and ?range=today|week|month,
// with SubNav directly below PageHeader and PageBody width="wide" (auto-mounting ScrollRuler).
export function StatsPage() {
  const [params, setParams] = useSearchParams()
  const overview = useOverviewFilter()
  const tab = parseStatsTab(params.get('tab'))
  const range = parseStatsRange(params.get('range'))
  const { from } = statsWindow(range)
  const eyebrow = range === 'today' ? text.today : text.window(text.day(from.getMonth() + 1, from.getDate()), text.today)
  const { filter } = overview

  const updateParam = (key: 'tab' | 'range', value: string) => {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        next.set(key, value)
        return next
      },
      { replace: true },
    )
  }

  return (
    <PageFrame>
      <PageHeader
        crumbs={[{ label: scopeName(filter), to: scopeHref(filter) }]}
        title={text.title}
        actions={
          <>
            <OverviewScopeSelect filter={filter} projects={overview.projects} onSelect={overview.select} allLabel={text.allProjects} />
            <ToggleGroup
              aria-label={text.rangeLabel}
              className={segmentedClass}
              value={range}
              onValueChange={(value) => value && updateParam('range', value)}
            >
              {STATS_RANGES.map((item) => (
                <ToggleGroupItem key={item} value={item} className={segmentedItemClass}>
                  {text.ranges[item]}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          </>
        }
      >
        <span className="font-mono text-[12px] text-ink-500">{eyebrow}</span>
      </PageHeader>

      {overview.query ? (
        <StatsWorkspace
          key={overview.key}
          tab={tab}
          range={range}
          query={overview.query}
          projects={overview.projects}
          onTabChange={(next) => updateParam('tab', next)}
        />
      ) : (
        <Sheet className="flex-1">
          <PageBody width="wide">
            {filter.kind === 'project' && filter.project === null && (
              <OverviewScopeNotFound name={filter.name} onBack={() => overview.select('')} />
            )}
          </PageBody>
        </Sheet>
      )}
    </PageFrame>
  )
}

function StatsWorkspace({
  tab,
  range,
  query,
  projects,
  onTabChange,
}: {
  tab: StatsTab
  range: StatsRange
  query: ScopeQuery
  projects: Schemas['Project'][] | undefined
  onTabChange: (tab: StatsTab) => void
}) {
  const stats = useStats(range, query)
  const report = stats.data
  const reliabilityAlerts = report ? report.questions.pending.length + report.outcomes.totals.failed : 0

  return (
    <>
      <SubNav
        aria-label={text.tabs.label}
        value={tab}
        onChange={onTabChange}
        items={[
          { key: 'usage', label: text.tabs.usage, controls: 'stats-panel-usage' },
          {
            key: 'reliability',
            label: text.tabs.reliability,
            badge:
              reliabilityAlerts > 0 ? (
                <span className="inline-flex min-w-[18px] items-center justify-center rounded-full bg-tint-red px-1.5 font-mono text-[10.5px] leading-[16px] font-semibold text-red-text tabular-nums [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:text-ink">
                  {reliabilityAlerts}
                </span>
              ) : undefined,
            controls: 'stats-panel-reliability',
          },
          { key: 'diagnostics', label: text.tabs.diagnostics, controls: 'stats-panel-diagnostics' },
        ]}
      />
      <Sheet className="flex-1">
        <PageBody width="wide" innerClassName="flex flex-col gap-4">
          <StatsBody tab={tab} range={range} stats={stats} projects={projects} />
        </PageBody>
      </Sheet>
    </>
  )
}

function StatsBody({
  tab,
  range,
  stats,
  projects,
}: {
  tab: StatsTab
  range: StatsRange
  stats: ReturnType<typeof useStats>
  projects: Schemas['Project'][] | undefined
}) {
  const scopeOf: ScopeOf = (projectId) => {
    if (projectId === null) return { kind: 'platform' }
    const project = projects?.find((p) => p.id === projectId)
    return project ? { kind: 'project', project } : null
  }

  if (stats.isError && !stats.data) return <SectionError title={text.title} error={stats.error} onRetry={() => void stats.refetch()} />
  if (!stats.data) {
    return (
      <div aria-busy="true" className="flex flex-col gap-4">
        <span className="riso-skeleton h-[76px] rounded-card" />
        <div className="grid gap-4 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
          <span className="riso-skeleton h-[260px] rounded-card" />
          <span className="riso-skeleton h-[260px] rounded-card" />
        </div>
      </div>
    )
  }

  const report = stats.data
  if (isEmpty(report)) {
    return (
      <p
        role="status"
        className="rounded-card border border-hairline-strong bg-surface-subtle px-5 py-8 text-center text-[14px] font-medium text-ink-700 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/25 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:font-bold"
      >
        {text.empty}
      </p>
    )
  }

  const axis = axisOf(report, range)
  return (
    <div
      id={`stats-panel-${tab}`}
      role="tabpanel"
      className={cn('flex flex-col gap-4', stats.isPlaceholderData && 'opacity-60')}
      aria-busy={stats.isPlaceholderData || undefined}
    >
      {tab === 'usage' && (
        <>
          <MetricStrip label={text.kpis.label} items={usageMetrics(report)} />
          <div className="grid gap-4 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
            <OriginsCard report={report} axis={axis} />
            <WakesCard report={report} scopeOf={scopeOf} />
          </div>
          <div className="grid gap-4 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
            <ContextCard report={report} />
            <ModelCostCard report={report} />
          </div>
        </>
      )}

      {tab === 'reliability' && (
        <>
          <MetricStrip label={text.kpis.label} items={reliabilityMetrics(report)} />
          <div className="grid gap-4 lg:grid-cols-2">
            <WaitCard report={report} axis={axis} />
            <OutcomesCard report={report} axis={axis} />
          </div>
          <div className="grid gap-4 lg:grid-cols-2">
            <ReasonsCard report={report} />
            <QuestionsCard report={report} scopeOf={scopeOf} />
          </div>
        </>
      )}

      {tab === 'diagnostics' && (
        <>
          <HealthCard report={report} scopeOf={scopeOf} />
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            <ToolsCard report={report} />
            <ProcessCard report={report} />
            <BusyCard report={report} />
          </div>
        </>
      )}
    </div>
  )
}
