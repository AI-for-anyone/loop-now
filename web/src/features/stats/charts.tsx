import { useEffect, useId, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode, type Ref } from 'react'
import { cn } from '@/lib/utils'
import { niceTicks } from './ticks'

// Charts for the stats page: inline SVG, no chart library. 2px lines; stacked columns sit flush on the baseline, their segments
// stacked flush against each other with a 2px navy outline around the whole column; horizontal bars have a 4px rounded right end;
// grid lines stay in the background.
// Every multi-series chart has a legend; bars show a tooltip per column, lines for the nearest x value, and once focused by
// keyboard the left and right arrow keys step through them one by one.

export type Series = { key: string; label: string; color: string; values: (number | null)[] }

// Container width: charts compute their geometry in actual pixels, so text isn't distorted by scaling. The browser calls back
// once as soon as it starts observing; where that isn't supported, fallback is used.
function useWidth(fallback: number) {
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(fallback)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const observer = new ResizeObserver(() => {
      if (el.clientWidth > 0) setWidth(el.clientWidth)
    })
    observer.observe(el)
    return () => observer.disconnect()
  }, [])
  return [ref, width] as const
}

// Leave at least gap pixels between x-axis labels: counting back from the last one (today, now), show one every few labels.
const labelEvery = (n: number, room: number, gap: number) => Math.max(1, Math.ceil(n / Math.max(1, Math.floor(room / gap))))


// Lines break at null values.
function linePath(points: ({ x: number; y: number } | null)[]) {
  let d = ''
  let pen = false
  for (const point of points) {
    if (!point) {
      pen = false
      continue
    }
    d += `${pen ? 'L' : 'M'}${point.x.toFixed(1)},${point.y.toFixed(1)}`
    pen = true
  }
  return d
}

export function Legend({ items }: { items: { label: string; color: string; kind: 'rect' | 'line' }[] }) {
  return (
    <ul className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-ink-700">
      {items.map((item) => (
        <li key={item.label} className="inline-flex items-center gap-[5px]">
          <span aria-hidden="true" className={item.kind === 'rect' ? 'size-2.5 rounded-[3px]' : 'h-0.5 w-3.5'} style={{ background: item.color }} />
          {item.label}
        </li>
      ))}
    </ul>
  )
}

type TipRow = { label: string; value: string; color?: string }

// Tooltip: the value first, in bold, then the series name; each row is marked with a short line in the series color. Near
// either edge it doesn't spill out of the chart.
function Tip({ id, x, width, title, rows }: { id: string; x: number; width: number; title: string; rows: TipRow[] }) {
  const shift = x < 90 ? undefined : x > width - 90 ? 'translateX(-100%)' : 'translateX(-50%)'
  return (
    <div
      id={id}
      role="tooltip"
      className="pointer-events-none absolute top-1 z-10 flex min-w-[120px] flex-col gap-0.5 rounded-input bg-tooltip-bg px-3 py-2 text-[12px] leading-[1.5] whitespace-nowrap text-tooltip-fg shadow-floating [[data-skin=plate]_&]:shadow-[3px_3px_0_var(--riso-pink)]"
      style={{ left: x, transform: shift }}
    >
      <span className="font-semibold text-tooltip-muted [[data-skin=plate]_&]:font-bold">{title}</span>
      {rows.map((row) => (
        <span key={row.label} className="flex items-center gap-2">
          {row.color && <span aria-hidden="true" className="h-0.5 w-3 shrink-0" style={{ background: row.color }} />}
          <b className="tabular-nums">{row.value}</b>
          <span className="text-tooltip-muted">{row.label}</span>
        </span>
      ))}
    </div>
  )
}

// Only keyboard focus shows the tooltip automatically; when focus comes from a mouse click, the pointer position decides.
function focusVisible(element: Element) {
  try {
    return element.matches(':focus-visible')
  } catch {
    return false
  }
}

// The chart frame: focusable; the left and right arrow keys, Home and End move between columns, and Esc hides the tooltip;
// it also hides when the pointer leaves or focus is lost.
function Frame({
  label,
  count,
  active,
  onActive,
  tip,
  children,
  frameRef,
}: {
  label: string
  count: number
  active: number | null
  onActive: (index: number | null) => void
  tip: (id: string) => ReactNode
  children: ReactNode
  frameRef: Ref<HTMLDivElement>
}) {
  const tipId = useId()
  const onKeyDown = (event: KeyboardEvent) => {
    if (count === 0) return
    const last = count - 1
    const current = active ?? last
    const next =
      event.key === 'ArrowLeft' ? Math.max(0, current - 1) : event.key === 'ArrowRight' ? Math.min(last, current + 1) : event.key === 'Home' ? 0 : event.key === 'End' ? last : event.key === 'Escape' ? null : undefined
    if (next === undefined) return
    event.preventDefault()
    onActive(next)
  }
  return (
    <div
      ref={frameRef}
      role="group"
      aria-label={label}
      aria-describedby={active !== null ? tipId : undefined}
      tabIndex={0}
      onKeyDown={onKeyDown}
      onFocus={(event) => {
        if (active === null && focusVisible(event.currentTarget)) onActive(count - 1)
      }}
      onBlur={() => onActive(null)}
      onPointerLeave={() => onActive(null)}
      className="relative min-w-0 rounded-ticket outline-none focus-visible:shadow-focus"
    >
      {children}
      {active !== null && tip(tipId)}
    </div>
  )
}

const AXIS = { fontSize: 11, fill: 'var(--ink-500)' }

// Stacked column chart. values are already converted to the y-axis unit; format writes the numbers in the tooltip, axis writes
// the y-axis ticks, tops writes the total above each bar (only when bars are wide enough), and when totalLabel is given the
// tooltip ends with an extra total row.
export function StackedColumns({
  label,
  labels,
  titles,
  series,
  height,
  format,
  axis,
  tops,
  steps,
  totalLabel,
}: {
  label: string
  labels: string[]
  titles: string[]
  series: Series[]
  height: number
  format: (value: number) => string
  axis: (value: number) => string
  tops?: (total: number) => string
  steps?: number[]
  totalLabel?: string
}) {
  const [ref, width] = useWidth(520)
  const [active, setActive] = useState<number | null>(null)
  const n = labels.length
  const totals = labels.map((_, i) => series.reduce((sum, s) => sum + (s.values[i] ?? 0), 0))
  const ticks = niceTicks(Math.max(0, ...totals), 4, steps)
  const max = ticks[ticks.length - 1]
  const left = 46
  const plotW = Math.max(40, width - left - 6)
  const slot = plotW / Math.max(1, n)
  const showTops = tops !== undefined && slot >= 40
  const top = showTops ? 20 : 10
  const plotH = height - top - 24
  const scale = plotH / max
  const barW = Math.max(2, Math.min(48, slot * 0.56))
  const every = labelEvery(n, plotW, 46)
  const cx = (i: number) => left + slot * i + slot / 2

  const onPointerMove = (event: PointerEvent<SVGSVGElement>) => {
    const x = event.clientX - event.currentTarget.getBoundingClientRect().left - left
    setActive(x < 0 || x >= plotW ? null : Math.min(n - 1, Math.floor(x / slot)))
  }

  return (
    <Frame
      frameRef={ref}
      label={label}
      count={n}
      active={active}
      onActive={setActive}
      tip={(id) =>
        active !== null && (
          <Tip
            id={id}
            x={cx(active)}
            width={width}
            title={titles[active]}
            rows={[
              ...series.map((s) => ({ label: s.label, value: format(s.values[active] ?? 0), color: s.color })),
              ...(totalLabel ? [{ label: totalLabel, value: format(totals[active]) }] : []),
            ]}
          />
        )
      }
    >
      <svg aria-hidden="true" width={width} height={height} className="block" onPointerMove={onPointerMove}>
        {ticks.map((value) => {
          const y = top + plotH - value * scale
          return (
            <g key={value}>
              <line x1={left} x2={left + plotW} y1={y} y2={y} stroke="var(--chart-grid)" strokeWidth={1} />
              <text x={left - 8} y={y + 4} textAnchor="end" {...AXIS}>
                {axis(value)}
              </text>
            </g>
          )
        })}
        <line data-baseline x1={left} x2={left + plotW} y1={top + plotH} y2={top + plotH} stroke="var(--hairline-strong)" strokeWidth={1} className="[[data-skin=plate]_&]:stroke-[2]" />
        {labels.map((text, i) => {
          const x = left + slot * i + (slot - barW) / 2
          const parts = series.map((s) => ({ key: s.key, color: s.color, value: s.values[i] ?? 0 })).filter((part) => part.value > 0)
          let base = 0
          const stackH = parts.reduce((sum, part) => sum + Math.max(part.value * scale, 1), 0)
          return (
            <g key={i} opacity={active !== null && active !== i ? 0.45 : 1}>
              {/* Segments are stacked flush against each other; Plate skin draws a 2px ink outline around the whole bar. */}
              {parts.map((part) => {
                const h = Math.max(part.value * scale, 1)
                const y = top + plotH - base * scale - h
                base += part.value
                return <rect key={part.key} x={x} y={y} width={barW} height={h} fill={part.color} />
              })}
              {parts.length > 0 && (
                <rect data-column-outline x={x} y={top + plotH - stackH} width={barW} height={stackH} fill="none" stroke="var(--chart-bar-stroke)" strokeWidth={2} />
              )}
              {showTops && totals[i] > 0 && (
                <text x={cx(i)} y={top + plotH - totals[i] * scale - 5} textAnchor="middle" fontSize={11} fontWeight={700} fill="var(--ink)">
                  {tops(totals[i])}
                </text>
              )}
              {(n - 1 - i) % every === 0 && (
                <text x={cx(i)} y={height - 6} textAnchor="middle" {...AXIS}>
                  {text}
                </text>
              )}
            </g>
          )
        })}
      </svg>
    </Frame>
  )
}

// Line chart (with area). ticks are the y-axis ticks; ends labels the value at the last point of each line; peak marks the
// highest point; labelAt picks which x-axis labels to draw.
export function LineChart({
  label,
  labels,
  titles,
  series,
  height,
  ticks,
  axis,
  format,
  ends,
  peak,
  area,
  labelAt,
}: {
  label: string
  labels: string[]
  titles: string[]
  series: Series[]
  height: number
  ticks: number[]
  axis: (value: number) => string
  format: (value: number) => string
  ends?: boolean
  peak?: { index: number; text: string }
  area?: boolean
  labelAt?: (index: number) => boolean
}) {
  const [ref, width] = useWidth(300)
  const [active, setActive] = useState<number | null>(null)
  const n = labels.length
  const max = ticks[ticks.length - 1]
  const left = 40
  const right = 12
  const top = 22
  const plotW = Math.max(40, width - left - right)
  const plotH = height - top - 24
  const stepX = n > 1 ? plotW / (n - 1) : 0
  const x = (i: number) => left + (n > 1 ? i * stepX : plotW / 2)
  const y = (value: number) => top + plotH - (Math.min(value, max) / max) * plotH
  const every = labelEvery(n, plotW, 52)
  const points = series.map((s) => s.values.map((v, i) => (v === null ? null : { x: x(i), y: y(v) })))

  // The value at the last point of each line; when two labels are too close, the lower one moves below its point.
  const endMarks = ends
    ? series
        .map((s) => {
          const i = s.values.findLastIndex((v) => v !== null)
          return i < 0 ? null : { key: s.key, color: s.color, x: x(i), y: y(s.values[i]!), text: format(s.values[i]!), below: false }
        })
        .filter((mark) => mark !== null)
        .sort((a, b) => a.y - b.y)
    : []
  for (let j = 1; j < endMarks.length; j++) if (Math.abs(endMarks[j].y - endMarks[j - 1].y) < 16) endMarks[j].below = true

  const onPointerMove = (event: PointerEvent<SVGSVGElement>) => {
    const px = event.clientX - event.currentTarget.getBoundingClientRect().left - left
    setActive(n === 0 ? null : Math.max(0, Math.min(n - 1, Math.round(n > 1 ? px / stepX : 0))))
  }

  return (
    <Frame
      frameRef={ref}
      label={label}
      count={n}
      active={active}
      onActive={setActive}
      tip={(id) =>
        active !== null && (
          <Tip
            id={id}
            x={x(active)}
            width={width}
            title={titles[active]}
            rows={series.map((s) => ({ label: s.label, value: s.values[active] === null ? '—' : format(s.values[active]!), color: s.color }))}
          />
        )
      }
    >
      <svg aria-hidden="true" width={width} height={height} className="block" onPointerMove={onPointerMove}>
        {ticks.map((value) => (
          <g key={value}>
            <line x1={left} x2={left + plotW} y1={y(value)} y2={y(value)} stroke="var(--chart-grid)" strokeWidth={1} />
            <text x={left - 8} y={y(value) + 4} textAnchor="end" {...AXIS}>
              {axis(value)}
            </text>
          </g>
        ))}
        {series.map((s, j) => (
          <g key={s.key}>
            {area && (
              <path d={`${linePath(points[j])}V${top + plotH}H${points[j].find((p) => p)?.x ?? left}Z`} fill={s.color} opacity={0.1} />
            )}
            <path d={linePath(points[j])} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
            {/* A single point with breaks on both sides is drawn as a small dot so it stays visible. */}
            {points[j].map((p, i) => p && !points[j][i - 1] && !points[j][i + 1] && <circle key={i} cx={p.x} cy={p.y} r={2.5} fill={s.color} />)}
          </g>
        ))}
        {active !== null && <line x1={x(active)} x2={x(active)} y1={top} y2={top + plotH} stroke="var(--ink)" strokeOpacity={0.3} strokeWidth={1} />}
        {active !== null &&
          series.map((s, j) => {
            const p = points[j][active]
            return p && <circle key={s.key} cx={p.x} cy={p.y} r={4} fill={s.color} stroke="var(--chart-point-stroke)" strokeWidth={2} />
          })}
        {active === null &&
          endMarks.map((mark) => (
            <g key={mark.key}>
              <circle cx={mark.x} cy={mark.y} r={4.5} fill={mark.color} stroke="var(--chart-point-stroke)" strokeWidth={2} />
              <text x={mark.x - 2} y={mark.below ? mark.y + 17 : mark.y - 9} textAnchor="end" fontSize={11.5} fontWeight={700} fill="var(--ink)">
                {mark.text}
              </text>
            </g>
          ))}
        {active === null && peak && series[0] && series[0].values[peak.index] !== null && (
          <g>
            <circle cx={x(peak.index)} cy={y(series[0].values[peak.index]!)} r={4.5} fill={series[0].color} stroke="var(--chart-point-stroke)" strokeWidth={2} />
            <text x={x(peak.index)} y={y(series[0].values[peak.index]!) - 10} textAnchor="middle" fontSize={11} fontWeight={700} fill="var(--ink)">
              {peak.text}
            </text>
          </g>
        )}
        {labels.map(
          (text, i) =>
            (labelAt ? labelAt(i) : (n - 1 - i) % every === 0) && (
              <text key={i} x={x(i)} y={height - 6} textAnchor={n > 1 && i === 0 ? 'start' : n > 1 && i === n - 1 ? 'end' : 'middle'} {...AXIS}>
                {text}
              </text>
            ),
        )}
      </svg>
    </Frame>
  )
}

// The small line in a metric card: shows the trend only, with no values. Not drawn with fewer than two points.
export function Spark({ values, className }: { values: (number | null)[]; className?: string }) {
  const known = values.filter((v): v is number => v !== null)
  if (known.length < 2) return <span aria-hidden="true" className={cn('block h-[26px]', className)} />
  const min = Math.min(...known)
  const span = Math.max(...known) - min || 1
  const step = 120 / Math.max(1, values.length - 1)
  const d = linePath(values.map((v, i) => (v === null ? null : { x: 2 + i * step, y: 23 - ((v - min) / span) * 20 })))
  return (
    <svg aria-hidden="true" viewBox="0 0 124 26" preserveAspectRatio="none" className={cn('block h-[26px] w-full', className)}>
      <path d={d} fill="none" stroke="var(--chart-blue)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" opacity={0.55} vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

// Horizontal bar: its length is value ÷ max, with a 4px rounded right end and the left end flush with the start.
export function Bar({ value, max, color, height = 'h-3.5' }: { value: number; max: number; color: string; height?: string }) {
  return (
    <span aria-hidden="true" className={cn('flex min-w-0', height)}>
      <span className="h-full rounded-r-[4px]" style={{ width: `${max > 0 ? Math.max((value / max) * 100, value > 0 ? 1 : 0) : 0}%`, background: color }} />
    </span>
  )
}
