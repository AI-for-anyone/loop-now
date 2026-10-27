import { useId, useState } from 'react'
import { useUsage } from '@/api/queries'
import { ScopeTag } from '@/features/projects/OverviewScope'
import type { ScopeQuery } from '@/lib/scope'
import { SectionError } from '@/components/RequestError'
import { Badge } from '@/components/ui/badge'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { config } from '@/config'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
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

// 四项明细的色块：输入孔版蓝、输出荧光粉、缓存读取浅蓝描蓝边、缓存写入浅粉描粉边。
const SWATCH = {
  input: 'bg-riso-blue',
  output: 'bg-riso-pink',
  cacheRead: 'bg-tint-blue shadow-[inset_0_0_0_2px_var(--riso-blue)]',
  cacheWrite: 'bg-tint-pink-2 shadow-[inset_0_0_0_2px_var(--pink-text)]',
} as const

const PARTS = ['input', 'output', 'cacheRead', 'cacheWrite'] as const

function TotalCard({ rangeLabel, sum }: { rangeLabel: string; sum: UsageParts }) {
  return (
    <div className="flex flex-col gap-1.5 rounded-panel bg-paper-raised px-6 py-[22px] shadow-print-rest">
      <span className="text-[14px] font-black">{t.overview.usage.total(rangeLabel)}</span>
      <span className="riso-misreg font-display-latin text-[clamp(48px,12vw,80px)] leading-[0.95] font-extrabold tracking-[-0.03em] [overflow-wrap:anywhere] text-riso-blue">
        {fmt(sum.total)}
      </span>
      <dl className="mt-2.5 grid grid-cols-2 gap-x-4 gap-y-2.5">
        {PARTS.map((part) => (
          <div key={part} className="flex min-w-0 flex-col rounded-input bg-white px-3 py-2.5">
            <dt className="flex flex-wrap items-center gap-1.5 text-[12.5px] font-black text-ink-700">
              <span aria-hidden="true" className={cn('h-2.5 w-3 shrink-0 rounded-[3px]', SWATCH[part])} />
              {t.overview.usage.parts[part]}
              <span className="font-mono font-medium text-ink-500">{part}</span>
            </dt>
            <dd className="font-display-latin text-[26px] leading-[1.2] font-extrabold [overflow-wrap:anywhere]">{fmt(sum[part])}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}

function LineName({ line, dimension }: { line: UsageLine; dimension: UsageDimension }) {
  if (dimension === 'agent') {
    return (
      <span className="flex min-w-0 flex-wrap items-center gap-1.5 text-[14px] font-black [overflow-wrap:anywhere]">
        {line.agentName}
        {line.deleted && <Badge tone="neutral">{t.overview.usage.deleted}</Badge>}
        {line.scope && <ScopeTag scope={line.scope} projectId={line.projectId} />}
      </span>
    )
  }
  if (line.provider === null || line.model === null) {
    return <span className="text-[14px] font-black">{t.overview.usage.unknownModel}</span>
  }
  return <span className="font-mono text-[12.5px] font-black [overflow-wrap:anywhere]">{t.overview.usage.model(line.provider, line.model)}</span>
}

// 一行：名称、横条（长度为输入加输出，蓝为输入、粉为输出）和一直显示的合计；悬停或键盘聚焦时显示四项明细。
function UsageBar({ line, dimension, max }: { line: UsageLine; dimension: UsageDimension; max: number }) {
  const detailId = useId()
  const width = (value: number) => `${max > 0 ? (value / max) * 100 : 0}%`
  return (
    <li
      tabIndex={0}
      aria-describedby={detailId}
      className="group relative grid grid-cols-[minmax(110px,160px)_minmax(0,1fr)_max-content] items-center gap-x-3.5 gap-y-1.5 rounded-ticket outline-none focus-visible:shadow-focus"
    >
      <LineName line={line} dimension={dimension} />
      <span aria-hidden="true" className="flex h-[18px] gap-0.5">
        <span className="h-full rounded-l-[4px] bg-riso-blue" style={{ width: width(line.input) }} />
        <span className="h-full rounded-r-[4px] bg-riso-pink" style={{ width: width(line.output) }} />
      </span>
      <span className="text-right font-mono text-[12.5px] font-bold">{fmt(line.total)}</span>
      <span
        id={detailId}
        role="tooltip"
        className="invisible absolute right-0 bottom-full z-10 mb-2 max-w-[min(100%,420px)] rounded-input bg-ink px-3 py-2 text-[12.5px] leading-[1.6] text-white shadow-[3px_3px_0_var(--riso-pink)] group-hover:visible group-focus-visible:visible"
      >
        {t.overview.usage.detail(fmt(line.input), fmt(line.output), fmt(line.cacheRead), fmt(line.cacheWrite), fmt(line.total))}
      </span>
    </li>
  )
}

function BarsCard({ lines, dimension }: { lines: UsageLine[]; dimension: UsageDimension }) {
  const titleId = useId()
  const max = lines.reduce((value, line) => Math.max(value, line.input + line.output), 0)
  return (
    <div className="flex flex-col gap-3.5 rounded-panel bg-paper-raised px-6 py-[22px] shadow-print-rest">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <span id={titleId} className="text-[14px] font-black">
          {t.overview.usage.dimensions[dimension]}
        </span>
        <span aria-hidden="true" className="ml-auto inline-flex items-center gap-1.5 text-[12.5px] font-bold">
          <span className={cn('h-2.5 w-3.5 rounded-[3px]', SWATCH.input)} />
          {t.overview.usage.parts.input}
        </span>
        <span aria-hidden="true" className="inline-flex items-center gap-1.5 text-[12.5px] font-bold">
          <span className={cn('h-2.5 w-3.5 rounded-[3px]', SWATCH.output)} />
          {t.overview.usage.parts.output}
        </span>
      </div>
      <ul aria-labelledby={titleId} className="flex flex-col gap-3.5 pt-1">
        {lines.map((line) => (
          <UsageBar key={line.key} line={line} dimension={dimension} max={max} />
        ))}
      </ul>
      <span className="text-[12.5px] text-ink-500">{t.overview.usage.barNote}</span>
    </div>
  )
}

function Empty({ rangeLabel }: { rangeLabel: string }) {
  return (
    <div className="flex flex-col items-center gap-2 rounded-panel bg-paper-raised px-6 py-10 text-center shadow-print-rest">
      <span aria-hidden="true" className="font-display-latin text-[80px] leading-[0.9] font-extrabold text-tint-blue [text-shadow:4px_3px_0_var(--riso-blue)]">
        0
      </span>
      <span className="text-[15px] font-bold">{t.overview.usage.empty(rangeLabel)}</span>
      <span className="text-[13px] text-ink-700">{t.overview.usage.emptyHint}</span>
    </div>
  )
}

// 用量：所选时间段的合计与按 Agent 或按模型的横条，只统计 token，不显示费用。
export function UsageSection({ scope = {} }: { scope?: ScopeQuery }) {
  const titleId = useId()
  const [range, setRange] = useState<UsageRange>(config.overviewDefaults.usageRange)
  const [dimension, setDimension] = useState<UsageDimension>(config.overviewDefaults.usageDimension)
  const usage = useUsage(range, () => usageRangeStart(range).toISOString(), scope)
  const rangeLabel = t.overview.usage.ranges[range]
  const rows = usage.data?.rows ?? []
  const sum = sumUsage(rows)

  return (
    <section aria-labelledby={titleId} className="flex flex-col gap-[18px]">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
        <h2 id={titleId} className="riso-misreg-sm font-display-cn text-[40px] leading-[1.1] font-normal">
          {t.overview.usage.title}
        </h2>
        <span className="text-[13px] font-bold text-ink-500">{t.overview.usage.unit}</span>
        <div className="flex flex-wrap gap-2.5 sm:ml-auto">
          <ToggleGroup aria-label={t.overview.usage.rangeLabel} value={range} onValueChange={(value) => setRange(value as UsageRange)}>
            {USAGE_RANGES.map((item) => (
              <ToggleGroupItem key={item} value={item}>
                {t.overview.usage.ranges[item]}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
          <ToggleGroup aria-label={t.overview.usage.dimensionLabel} value={dimension} onValueChange={(value) => setDimension(value as UsageDimension)}>
            {USAGE_DIMENSIONS.map((item) => (
              <ToggleGroupItem key={item} value={item}>
                {t.overview.usage.dimensions[item]}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </div>
      </div>
      {usage.isError ? (
        <SectionError title={t.overview.usage.title} error={usage.error} onRetry={() => void usage.refetch()} />
      ) : usage.isPending ? (
        <div aria-busy="true" className="flex flex-col gap-3">
          <span className="riso-skeleton h-[180px] rounded-panel" />
          <span role="status" className="text-[13px] font-bold text-ink-500">
            {t.common.loading}
          </span>
        </div>
      ) : sum.total === 0 ? (
        <Empty rangeLabel={rangeLabel} />
      ) : (
        <div className="grid grid-cols-[repeat(auto-fit,minmax(min(560px,100%),1fr))] items-start gap-5">
          <TotalCard rangeLabel={rangeLabel} sum={sum} />
          <BarsCard lines={aggregateUsage(rows, dimension)} dimension={dimension} />
        </div>
      )}
    </section>
  )
}
