import { useId, useState } from 'react'
import { Link } from 'react-router'
import { ArrowRightIcon } from 'lucide-react'
import { useUsage } from '@/api/queries'
import { ScopeTag } from '@/features/projects/OverviewScope'
import type { ScopeQuery } from '@/lib/scope'
import { SectionError } from '@/components/RequestError'
import { Badge } from '@/components/ui/badge'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { config } from '@/config'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import {
  aggregateUsage,
  sumUsage,
  usageRangeStart,
  USAGE_DIMENSIONS,
  USAGE_RANGES,
  type UsageDimension,
  type UsageLine,
  type UsageParts,
  type UsageRange,
} from './usage'

const fmt = (count: number) => t.overview.tokens(count)

// Swatches for the four breakdown items: monochrome chart scale in Mono; riso blue, fluorescent pink and outlined tints in Plate.
const SWATCH = {
  input: 'bg-[var(--chart-blue)]',
  output: 'bg-[var(--chart-pink)]',
  cacheRead: 'bg-[var(--chart-ochre)]',
  cacheWrite: 'bg-[var(--chart-other)]',
} as const

const PARTS = ['input', 'output', 'cacheRead', 'cacheWrite'] as const

// Fill of the stacked total bars: flat chart tokens in Mono; halftone/tinted fills in Plate.
const FILL = {
  input: 'bg-[var(--chart-blue)]',
  output: 'bg-[var(--chart-pink)]',
  cacheRead: 'bg-[var(--chart-ochre)]',
  cacheWrite: 'bg-[var(--chart-other)]',
} as const

// The total line: the total for the selected period, with the four breakdown items to the right (no raw camelCase keys).
function TotalLine({ rangeLabel, sum }: { rangeLabel: string; sum: UsageParts }) {
  return (
    <div className="flex flex-wrap items-end gap-x-8 gap-y-2.5 rounded-card border border-hairline bg-surface-control px-4 py-3">
      <span className="flex flex-col gap-0.5">
        <span className="text-[12px] font-medium text-ink-500">
          {t.overview.usage.total(rangeLabel)}
        </span>
        <span className="riso-misreg font-display-latin text-[28px] leading-none font-semibold tracking-[-0.02em] text-ink tabular-nums [overflow-wrap:anywhere]">
          {fmt(sum.total)}
        </span>
      </span>
      <dl className="flex flex-wrap gap-x-6 gap-y-1.5">
        {PARTS.map((part) => (
          <div key={part} className="flex min-w-0 flex-col">
            <dt className="flex items-center gap-1.5 text-[11.5px] font-medium text-ink-500">
              <span aria-hidden="true" className={cn('h-2 w-2.5 shrink-0 rounded-[2px]', SWATCH[part])} />
              {t.overview.usage.parts[part]}
            </dt>
            <dd className="font-mono text-[13px] font-semibold text-ink tabular-nums [overflow-wrap:anywhere]">
              {fmt(sum[part])}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

function LineName({ line, dimension }: { line: UsageLine; dimension: UsageDimension }) {
  if (dimension === 'agent') {
    return (
      <span className="flex min-w-0 flex-wrap items-center gap-1.5 text-[13.5px] font-semibold [overflow-wrap:anywhere]">
        {line.agentName}
        {line.deleted && <Badge tone="neutral">{t.overview.usage.deleted}</Badge>}
        {line.scope && <ScopeTag scope={line.scope} projectId={line.projectId} />}
      </span>
    )
  }
  if (line.provider === null || line.model === null) {
    return <span className="text-[13.5px] font-semibold">{t.overview.usage.unknownModel}</span>
  }
  return (
    <span className="font-mono text-[12px] font-semibold [overflow-wrap:anywhere]">
      {t.overview.usage.model(line.provider, line.model)}
    </span>
  )
}

// One row: the name, a stacked bar scaled to the exact sorted total (so row #1 always has the widest bar and matches "Most"),
// and the always-visible total. Hover or keyboard focus shows the four breakdown items.
function UsageBar({ line, dimension, max, most }: { line: UsageLine; dimension: UsageDimension; max: number; most: boolean }) {
  const detailId = useId()
  const width = (value: number) => `${max > 0 ? (value / max) * 100 : 0}%`
  return (
    <li
      tabIndex={0}
      aria-describedby={detailId}
      className="group relative grid min-h-[32px] grid-cols-[minmax(76px,160px)_minmax(0,1fr)_minmax(112px,max-content)] items-center gap-x-3.5 gap-y-1 rounded-ticket outline-none focus-visible:shadow-focus"
    >
      <LineName line={line} dimension={dimension} />
      <span
        aria-hidden="true"
        data-usage-track
        className="flex h-[16px] min-w-0 overflow-hidden rounded-[4px] border border-hairline-strong bg-surface-subtle"
      >
        {PARTS.map((part) =>
          line[part] > 0 ? (
            <span key={part} data-usage-part={part} className={cn('h-full shrink-0', FILL[part])} style={{ width: width(line[part]) }} />
          ) : null,
        )}
      </span>
      <span className="flex items-center justify-end gap-2 font-mono text-[12px] font-medium tabular-nums">
        {fmt(line.total)}
        {most && (
          <span className="rounded-[4px] bg-surface-active px-1.5 font-sans text-[11px] leading-[18px] font-semibold text-ink">
            {t.overview.usage.most}
          </span>
        )}
      </span>
      <span
        id={detailId}
        role="tooltip"
        className="invisible absolute right-0 bottom-full z-10 mb-2 max-w-[min(100%,420px)] rounded-input bg-tooltip-bg px-3 py-2 text-[12px] leading-[1.6] text-tooltip-fg shadow-floating group-hover:visible group-focus-visible:visible"
      >
        {t.overview.usage.detail(fmt(line.input), fmt(line.output), fmt(line.cacheRead), fmt(line.cacheWrite), fmt(line.total))}
      </span>
    </li>
  )
}

function Bars({ lines, dimension }: { lines: UsageLine[]; dimension: UsageDimension }) {
  // Align bar scaling and the "Most" badge with aggregateUsage's exact sort metric (total tokens).
  const max = lines.reduce((value, line) => Math.max(value, line.total), 0)
  const mostKey = max > 0 ? lines[0]?.key : undefined
  return (
    <div className="flex flex-col gap-2">
      <ul aria-label={t.overview.usage.dimensions[dimension]} className="flex flex-col gap-0.5">
        {lines.map((line) => (
          <UsageBar key={line.key} line={line} dimension={dimension} max={max} most={line.key === mostKey} />
        ))}
      </ul>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[11.5px] text-ink-500">
        {PARTS.map((part) => (
          <span key={part} aria-hidden="true" className="inline-flex items-center gap-1.5 font-medium text-ink-700">
            <span className={cn('h-2 w-3 rounded-[2px] border border-hairline-strong', SWATCH[part])} />
            {t.overview.usage.parts[part]}
          </span>
        ))}
        <span>{t.overview.usage.barNote}</span>
      </div>
    </div>
  )
}

function Empty({ rangeLabel }: { rangeLabel: string }) {
  return (
    <p
      role="status"
      className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 rounded-card border border-hairline bg-surface-subtle px-4 py-3 text-[13px] text-ink-700"
    >
      <span className="font-semibold text-ink">{t.overview.usage.empty(rangeLabel)}</span>
      <span className="text-ink-500">{t.overview.usage.emptyHint}</span>
    </p>
  )
}

// Usage tab panel: period and dimension filters, total breakdown, proportional bars aligned to total tokens, and a link to Stats.
export function UsageSection({
  scope = {},
  statsHref = '/stats',
  todayQuery,
}: {
  scope?: ScopeQuery
  statsHref?: string
  todayQuery?: ReturnType<typeof useUsage>
}) {
  const [range, setRange] = useState<UsageRange>(config.overviewDefaults.usageRange)
  const [dimension, setDimension] = useState<UsageDimension>(config.overviewDefaults.usageDimension)
  if (range === 'today' && todayQuery) {
    return (
      <UsageBody
        usage={todayQuery}
        range={range}
        dimension={dimension}
        statsHref={statsHref}
        onRangeChange={setRange}
        onDimensionChange={setDimension}
      />
    )
  }
  return (
    <UsageLoader
      scope={scope}
      range={range}
      dimension={dimension}
      statsHref={statsHref}
      onRangeChange={setRange}
      onDimensionChange={setDimension}
    />
  )
}

function UsageLoader({
  scope,
  range,
  dimension,
  statsHref,
  onRangeChange,
  onDimensionChange,
}: {
  scope: ScopeQuery
  range: UsageRange
  dimension: UsageDimension
  statsHref: string
  onRangeChange: (range: UsageRange) => void
  onDimensionChange: (dimension: UsageDimension) => void
}) {
  const usage = useUsage(range, () => usageRangeStart(range).toISOString(), scope)
  return (
    <UsageBody
      usage={usage}
      range={range}
      dimension={dimension}
      statsHref={statsHref}
      onRangeChange={onRangeChange}
      onDimensionChange={onDimensionChange}
    />
  )
}

function UsageBody({
  usage,
  range,
  dimension,
  statsHref,
  onRangeChange,
  onDimensionChange,
}: {
  usage: ReturnType<typeof useUsage>
  range: UsageRange
  dimension: UsageDimension
  statsHref: string
  onRangeChange: (range: UsageRange) => void
  onDimensionChange: (dimension: UsageDimension) => void
}) {
  const rangeLabel = t.overview.usage.ranges[range]
  const rows = usage.data?.rows ?? []
  const sum = sumUsage(rows)

  return (
    <section aria-label={t.overview.usage.title} className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <ToggleGroup
          aria-label={t.overview.usage.rangeLabel}
          className={segmentedClass}
          value={range}
          onValueChange={(value) => value && onRangeChange(value as UsageRange)}
        >
          {USAGE_RANGES.map((item) => (
            <ToggleGroupItem key={item} value={item} className={segmentedItemClass}>
              {t.overview.usage.ranges[item]}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <ToggleGroup
          aria-label={t.overview.usage.dimensionLabel}
          className={segmentedClass}
          value={dimension}
          onValueChange={(value) => value && onDimensionChange(value as UsageDimension)}
        >
          {USAGE_DIMENSIONS.map((item) => (
            <ToggleGroupItem key={item} value={item} className={segmentedItemClass}>
              {t.overview.usage.dimensions[item]}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <span className="text-[12px] text-ink-500">{t.overview.usage.unit}</span>
        <Link
          to={statsHref}
          className="ml-auto inline-flex items-center gap-1 text-[12.5px] font-medium text-ink-700 underline-offset-2 hover:text-ink hover:underline focus-visible:shadow-focus"
        >
          {t.overview.usage.fullAnalytics}
          <ArrowRightIcon aria-hidden="true" className="size-3.5" strokeWidth={2.4} />
        </Link>
      </div>

      {usage.isError ? (
        <SectionError title={t.overview.usage.title} error={usage.error} onRetry={() => void usage.refetch()} />
      ) : usage.isPending ? (
        <div aria-busy="true" className="flex flex-col gap-2.5">
          <span className="riso-skeleton h-16 rounded-card" />
          <span className="riso-skeleton h-28 rounded-card opacity-75" />
          <span role="status" className="text-[12.5px] font-medium text-ink-500">
            {t.common.loading}
          </span>
        </div>
      ) : sum.total === 0 ? (
        <Empty rangeLabel={rangeLabel} />
      ) : (
        <>
          <TotalLine rangeLabel={rangeLabel} sum={sum} />
          <Bars lines={aggregateUsage(rows, dimension)} dimension={dimension} />
        </>
      )}
    </section>
  )
}
