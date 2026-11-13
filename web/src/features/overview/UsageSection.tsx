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
import { NumberedSection } from '@/components/NumberedSection'
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

// 四项明细的色块：输入孔版蓝、输出荧光粉、缓存读取浅蓝描蓝边、缓存写入浅粉描粉边。
const SWATCH = {
  input: 'bg-riso-blue',
  output: 'bg-riso-pink',
  cacheRead: 'bg-tint-blue shadow-[inset_0_0_0_2px_var(--riso-blue)]',
  cacheWrite: 'bg-tint-pink-2 shadow-[inset_0_0_0_2px_var(--pink-text)]',
} as const

const PARTS = ['input', 'output', 'cacheRead', 'cacheWrite'] as const

// 横条的网点填充（改版）：输入是孔版蓝底白网点，输出是荧光粉底藏青网点。
const FILL = {
  input: 'bg-riso-blue bg-[radial-gradient(rgb(255_255_255/.28)_1px,transparent_1.3px)] bg-size-[5px_5px]',
  output: 'bg-riso-pink bg-[radial-gradient(rgb(24_38_94/.22)_1px,transparent_1.3px)] bg-size-[5px_5px]',
} as const

// 合计一行：所选时间段的合计（大号数字），右边是四项明细。
function TotalLine({ rangeLabel, sum }: { rangeLabel: string; sum: UsageParts }) {
  return (
    <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
      <span className="flex flex-col gap-1">
        <span className="text-[13px] font-black">{t.overview.usage.total(rangeLabel)}</span>
        <span className="riso-misreg font-display-latin text-[46px] leading-none font-extrabold tracking-[-0.02em] [overflow-wrap:anywhere] text-riso-blue">{fmt(sum.total)}</span>
      </span>
      <dl className="flex flex-wrap gap-x-6 gap-y-2 pb-1">
        {PARTS.map((part) => (
          <div key={part} className="flex min-w-0 flex-col">
            <dt className="flex flex-wrap items-center gap-1.5 text-[12px] font-black text-ink-700">
              <span aria-hidden="true" className={cn('h-2.5 w-3 shrink-0 rounded-[3px]', SWATCH[part])} />
              {t.overview.usage.parts[part]}
              <span className="font-mono font-medium text-ink-500">{part}</span>
            </dt>
            <dd className="font-mono text-[14px] font-bold [overflow-wrap:anywhere]">{fmt(sum[part])}</dd>
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

// 一行：名称、横条（藏青描边的槽，按最多的一行算满格；网点填充，蓝为输入、粉为输出）和一直显示的合计，
// 最多的一行后面贴一个黄色“最多”；悬停或键盘聚焦时显示四项明细。
function UsageBar({ line, dimension, max, most }: { line: UsageLine; dimension: UsageDimension; max: number; most: boolean }) {
  const detailId = useId()
  const width = (value: number) => `${max > 0 ? (value / max) * 100 : 0}%`
  return (
    <li
      tabIndex={0}
      aria-describedby={detailId}
      className="group relative grid min-h-[34px] grid-cols-[minmax(76px,160px)_minmax(0,1fr)_minmax(120px,max-content)] items-center gap-x-3.5 gap-y-1.5 rounded-ticket outline-none focus-visible:shadow-focus"
    >
      <LineName line={line} dimension={dimension} />
      <span aria-hidden="true" data-usage-track className="flex h-[18px] min-w-0 overflow-hidden rounded-[4px] border-2 border-ink bg-paper">
        <span data-usage-part="input" className={cn('h-full shrink-0', FILL.input)} style={{ width: width(line.input) }} />
        <span data-usage-part="output" className={cn('h-full shrink-0', FILL.output)} style={{ width: width(line.output) }} />
      </span>
      <span className="flex items-center gap-2 font-mono text-[12.5px] font-bold">
        {fmt(line.total)}
        {most && <span className="rounded-[4px] bg-riso-yellow px-2 font-sans text-[11.5px] leading-5 font-black">{t.overview.usage.most}</span>}
      </span>
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

function Bars({ lines, dimension }: { lines: UsageLine[]; dimension: UsageDimension }) {
  const max = lines.reduce((value, line) => Math.max(value, line.input + line.output), 0)
  const mostKey = max > 0 ? lines.find((line) => line.input + line.output === max)?.key : undefined
  return (
    <div className="flex flex-col gap-2.5">
      <ul aria-label={t.overview.usage.dimensions[dimension]} className="flex flex-col gap-0.5">
        {lines.map((line) => (
          <UsageBar key={line.key} line={line} dimension={dimension} max={max} most={line.key === mostKey} />
        ))}
      </ul>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[12px] text-ink-500">
        <span aria-hidden="true" className="inline-flex items-center gap-1.5 font-bold text-ink-700">
          <span className={cn('h-2.5 w-3.5 rounded-[3px] border-[1.5px] border-ink', FILL.input)} />
          {t.overview.usage.parts.input}
        </span>
        <span aria-hidden="true" className="inline-flex items-center gap-1.5 font-bold text-ink-700">
          <span className={cn('h-2.5 w-3.5 rounded-[3px] border-[1.5px] border-ink', FILL.output)} />
          {t.overview.usage.parts.output}
        </span>
        <span>{t.overview.usage.barNote}</span>
      </div>
    </div>
  )
}

function Empty({ rangeLabel }: { rangeLabel: string }) {
  return (
    <div className="flex flex-col items-center gap-2 px-6 py-8 text-center">
      <span aria-hidden="true" className="font-display-latin text-[80px] leading-[0.9] font-extrabold text-tint-blue [text-shadow:4px_3px_0_var(--riso-blue)]">
        0
      </span>
      <span className="text-[15px] font-bold">{t.overview.usage.empty(rangeLabel)}</span>
      <span className="text-[13px] text-ink-700">{t.overview.usage.emptyHint}</span>
    </div>
  )
}

// 用量：所选时间段的合计与按 Agent 或按模型的横条，只统计 token，不显示费用。纸张上的第 number 个编号分区；
// 范围与分组是标题行右边的两组分段按钮。
export function UsageSection({ scope = {}, number = 2 }: { scope?: ScopeQuery; number?: number }) {
  const [range, setRange] = useState<UsageRange>(config.overviewDefaults.usageRange)
  const [dimension, setDimension] = useState<UsageDimension>(config.overviewDefaults.usageDimension)
  const usage = useUsage(range, () => usageRangeStart(range).toISOString(), scope)
  const rangeLabel = t.overview.usage.ranges[range]
  const rows = usage.data?.rows ?? []
  const sum = sumUsage(rows)

  return (
    <NumberedSection
      number={number}
      title={t.overview.usage.title}
      meta={<span className="text-[12px] text-ink-500">{t.overview.usage.unit}</span>}
      actions={
        <>
          <ToggleGroup aria-label={t.overview.usage.rangeLabel} className={segmentedClass} value={range} onValueChange={(value) => setRange(value as UsageRange)}>
            {USAGE_RANGES.map((item) => (
              <ToggleGroupItem key={item} value={item} className={segmentedItemClass}>
                {t.overview.usage.ranges[item]}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
          <ToggleGroup aria-label={t.overview.usage.dimensionLabel} className={segmentedClass} value={dimension} onValueChange={(value) => setDimension(value as UsageDimension)}>
            {USAGE_DIMENSIONS.map((item) => (
              <ToggleGroupItem key={item} value={item} className={segmentedItemClass}>
                {t.overview.usage.dimensions[item]}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </>
      }
    >
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
        <>
          <TotalLine rangeLabel={rangeLabel} sum={sum} />
          <Bars lines={aggregateUsage(rows, dimension)} dimension={dimension} />
        </>
      )}
    </NumberedSection>
  )
}
