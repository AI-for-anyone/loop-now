import { useEffect, useId, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode, type Ref } from 'react'
import { cn } from '@/lib/utils'
import { niceTicks } from './ticks'

// 统计页的图表：内联 SVG，不用图表库。2px 折线；柱子顶端 4px 圆角、底端贴住基线，堆叠的段之间留 2px；网格线退后。
// 多系列的图都有图例；柱子按列、折线按最近的横坐标显示提示框，键盘聚焦后可用左右方向键逐个查看。

export type Series = { key: string; label: string; color: string; values: (number | null)[] }

// 容器宽度：图表按实际像素计算几何，文字不随缩放变形。浏览器首次观察时就会回调一次；不支持时用 fallback。
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

// 横轴标签之间至少留 gap 像素：从最后一个（今天、现在）往前每隔几个显示一个。
const labelEvery = (n: number, room: number, gap: number) => Math.max(1, Math.ceil(n / Math.max(1, Math.floor(room / gap))))

const topRounded = (x: number, y: number, w: number, h: number) => {
  const r = Math.min(4, w / 2, h)
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`
}

// 折线遇到 null 断开。
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

// 提示框：数值在前、加粗，系列名在后；每行用一小段系列颜色的线作标记。靠近两端时不越出图表。
function Tip({ id, x, width, title, rows }: { id: string; x: number; width: number; title: string; rows: TipRow[] }) {
  const shift = x < 90 ? undefined : x > width - 90 ? 'translateX(-100%)' : 'translateX(-50%)'
  return (
    <div
      id={id}
      role="tooltip"
      className="pointer-events-none absolute top-1 z-10 flex min-w-[120px] flex-col gap-0.5 rounded-input bg-ink px-3 py-2 text-[12px] leading-[1.5] whitespace-nowrap text-white shadow-[3px_3px_0_var(--riso-pink)]"
      style={{ left: x, transform: shift }}
    >
      <span className="font-bold text-white/75">{title}</span>
      {rows.map((row) => (
        <span key={row.label} className="flex items-center gap-2">
          {row.color && <span aria-hidden="true" className="h-0.5 w-3 shrink-0" style={{ background: row.color }} />}
          <b className="tabular-nums">{row.value}</b>
          <span className="text-white/80">{row.label}</span>
        </span>
      ))}
    </div>
  )
}

// 只有键盘带来的聚焦才自动显示提示框；鼠标点进来时由指针位置决定。
function focusVisible(element: Element) {
  try {
    return element.matches(':focus-visible')
  } catch {
    return false
  }
}

// 图表的外框：可聚焦，左右方向键、Home、End 在各列之间移动，Esc 收起提示框；指针离开或失去焦点时收起。
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

// 堆叠柱状图。values 已换成纵轴的单位；format 写提示框里的数，axis 写纵轴刻度，tops 写柱顶的合计（柱子够宽时才写），
// totalLabel 给出时提示框末尾多一行合计。
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
  const barW = Math.max(2, Math.min(30, slot * 0.56))
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
        {labels.map((text, i) => {
          const x = left + slot * i + (slot - barW) / 2
          const parts = series.map((s) => ({ key: s.key, color: s.color, value: s.values[i] ?? 0 })).filter((part) => part.value > 0)
          let base = 0
          return (
            <g key={i} opacity={active !== null && active !== i ? 0.45 : 1}>
              {parts.map((part, j) => {
                const h = Math.max(part.value * scale, 1)
                const y = top + plotH - base * scale - h
                base += part.value
                // 最上面一段画圆角；下面的段让出 2px，留出与上一段之间的空隙。
                return j === parts.length - 1 ? (
                  <path key={part.key} d={topRounded(x, y, barW, h)} fill={part.color} />
                ) : (
                  <rect key={part.key} x={x} y={y + 2} width={barW} height={Math.max(h - 2, 1)} fill={part.color} />
                )
              })}
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

// 折线图（含面积）。ticks 是纵轴刻度；ends 在每条线最后一个点上标出数值；peak 标出最高点；labelAt 指定哪些横轴标签要写。
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

  // 每条线最后一个点的数值；两条标签靠得太近时，下面那条移到点的下方。
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
            {/* 前后都断开的单个点画成小圆点，免得看不见。 */}
            {points[j].map((p, i) => p && !points[j][i - 1] && !points[j][i + 1] && <circle key={i} cx={p.x} cy={p.y} r={2.5} fill={s.color} />)}
          </g>
        ))}
        {active !== null && <line x1={x(active)} x2={x(active)} y1={top} y2={top + plotH} stroke="var(--ink)" strokeOpacity={0.3} strokeWidth={1} />}
        {active !== null &&
          series.map((s, j) => {
            const p = points[j][active]
            return p && <circle key={s.key} cx={p.x} cy={p.y} r={4} fill={s.color} stroke="#FFFFFF" strokeWidth={2} />
          })}
        {active === null &&
          endMarks.map((mark) => (
            <g key={mark.key}>
              <circle cx={mark.x} cy={mark.y} r={4.5} fill={mark.color} stroke="#FFFFFF" strokeWidth={2} />
              <text x={mark.x - 2} y={mark.below ? mark.y + 17 : mark.y - 9} textAnchor="end" fontSize={11.5} fontWeight={700} fill="var(--ink)">
                {mark.text}
              </text>
            </g>
          ))}
        {active === null && peak && series[0] && series[0].values[peak.index] !== null && (
          <g>
            <circle cx={x(peak.index)} cy={y(series[0].values[peak.index]!)} r={4.5} fill={series[0].color} stroke="#FFFFFF" strokeWidth={2} />
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

// 指标卡里的小折线：只看走势，不标数值。少于两个点时不画。
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

// 横条：长度为 value ÷ max，右端 4px 圆角，左端贴住起点。
export function Bar({ value, max, color, height = 'h-3.5' }: { value: number; max: number; color: string; height?: string }) {
  return (
    <span aria-hidden="true" className={cn('flex min-w-0', height)}>
      <span className="h-full rounded-r-[4px]" style={{ width: `${max > 0 ? Math.max((value / max) * 100, value > 0 ? 1 : 0) : 0}%`, background: color }} />
    </span>
  )
}
