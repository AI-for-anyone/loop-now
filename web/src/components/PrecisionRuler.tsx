import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type PointerEvent,
  type RefObject,
} from 'react'
import { ArrowDownIcon } from 'lucide-react'
import { useLocation } from 'react-router'
import { cn } from '@/lib/utils'

// Vertical clearance kept around active ticks when a long ruler scrolls itself into view.
const FOLLOW_MARGIN = 16
// Half-height clearance for floating preview pills so they stay inside the ruler's vertical bounds.
const HINT_HALF = 16
// Minimum overflow (in px) required before ScrollRuler renders on a scroll container.
const SCROLL_OVERFLOW_THRESHOLD = 40

const EMPTY_ACTIVE_SET: ReadonlySet<string> = new Set()

export interface PrecisionRulerItem {
  id: string
  label: string
  sublabel?: string
  /** Short tabular badge shown in the hover preview and optionally on major ticks (e.g. "01", "#5"). */
  badge?: string
  /** Longer major calibration tick (e.g. every 5th turn or primary section). */
  major?: boolean
  /** Semantic accent tone for status/danger ticks. */
  tone?: 'default' | 'accent' | 'success' | 'warning' | 'danger'
}

export interface PrecisionRulerProps {
  items: PrecisionRulerItem[]
  activeIds?: ReadonlySet<string> | string[] | string | null
  onSelect: (id: string) => void
  label: string
  onJumpBottom?: () => void
  jumpBottomLabel?: string
  total?: number | null
  totalLabel?: string
  minItems?: number
  placement?: 'floating' | 'inline'
  className?: string
}

interface RulerHint {
  id: string
  top: number
}

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(value, max))
}

function clampPillTop(top: number, containerHeight: number): number {
  const minTop = Math.min(HINT_HALF, containerHeight / 2)
  return Math.max(minTop, Math.min(top, containerHeight - HINT_HALF))
}

function normalizeActiveSet(activeIds?: ReadonlySet<string> | string[] | string | null): ReadonlySet<string> {
  if (!activeIds) return EMPTY_ACTIVE_SET
  if (typeof activeIds === 'string') return new Set([activeIds])
  if (Array.isArray(activeIds)) return new Set(activeIds)
  return activeIds
}

// Group sorted active indices into contiguous runs (startIndex -> count) so multi-visible ranges render a single spine bar.
function contiguousRuns(indices: number[]): Map<number, number> {
  const runs = new Map<number, number>()
  if (indices.length === 0) return runs
  let start = indices[0]
  let count = 1
  for (let i = 1; i < indices.length; i++) {
    if (indices[i] === indices[i - 1] + 1) {
      count += 1
    } else {
      runs.set(start, count)
      start = indices[i]
      count = 1
    }
  }
  runs.set(start, count)
  return runs
}

function tickColorClass(tone: PrecisionRulerItem['tone'], active: boolean, major: boolean, highlighted: boolean): string {
  if (tone === 'danger') {
    return 'bg-red-text group-hover:bg-red-text group-focus-visible:bg-red-text'
  }
  if (tone === 'warning') {
    return 'bg-pink-text group-hover:bg-pink-text group-focus-visible:bg-pink-text'
  }
  if (tone === 'success') {
    return 'bg-green group-hover:bg-green group-focus-visible:bg-green'
  }
  if (highlighted) {
    return 'bg-riso-blue [[data-skin=plate]_&]:bg-riso-pink'
  }
  if (tone === 'accent' || active) {
    return 'bg-riso-blue group-hover:bg-riso-blue group-focus-visible:bg-riso-blue [[data-skin=plate]_&]:group-hover:bg-riso-pink [[data-skin=plate]_&]:group-focus-visible:bg-riso-pink'
  }
  return cn(
    major ? 'bg-ink/45' : 'bg-ink/25',
    'group-hover:bg-riso-blue group-focus-visible:bg-riso-blue [[data-skin=plate]_&]:group-hover:bg-riso-pink [[data-skin=plate]_&]:group-focus-visible:bg-riso-pink',
  )
}

// Zero-width-tax Swiss vernier tick ruler for turns, page sections, workflow steps, and arbitrary indexed lists.
export function PrecisionRuler({
  items,
  activeIds,
  onSelect,
  label,
  onJumpBottom,
  jumpBottomLabel,
  total,
  totalLabel,
  minItems = 2,
  placement = 'floating',
  className,
}: PrecisionRulerProps) {
  const navRef = useRef<HTMLElement>(null)
  const listRef = useRef<HTMLOListElement>(null)
  const [hovered, setHovered] = useState<RulerHint | null>(null)
  const [focused, setFocused] = useState<RulerHint | null>(null)
  // Track pointer presses so mouse clicks do not leave the keyboard-focus preview stuck after the pointer moves away.
  const pointing = useRef(false)

  const activeSet = normalizeActiveSet(activeIds)
  const activeIndices: number[] = []
  for (let i = 0; i < items.length; i++) {
    if (activeSet.has(items[i].id)) activeIndices.push(i)
  }
  const activeIndexKey = activeIndices.join(',')
  const activeRuns = contiguousRuns(activeIndices)

  useLayoutEffect(() => {
    const el = listRef.current
    if (!el || activeIndexKey === '' || el.scrollHeight <= el.clientHeight) return
    const indexes = activeIndexKey.split(',').map(Number)
    const first = el.children[indexes[0]] as HTMLElement | undefined
    const last = el.children[indexes[indexes.length - 1]] as HTMLElement | undefined
    if (!first || !last) return
    const top = Math.max(0, first.offsetTop - FOLLOW_MARGIN)
    const bottom = last.offsetTop + last.offsetHeight + FOLLOW_MARGIN
    if (top < el.scrollTop) {
      el.scrollTop = top
    } else if (bottom > el.scrollTop + el.clientHeight) {
      el.scrollTop = Math.min(top, bottom - el.clientHeight)
    }
  }, [activeIndexKey])

  if (items.length < minItems) return null

  const place = (id: string): RulerHint | null => {
    const index = items.findIndex((item) => item.id === id)
    const tick = index >= 0 ? listRef.current?.children[index] : undefined
    const outer = navRef.current
    if (!tick || !outer) return null
    const rect = tick.getBoundingClientRect()
    const box = outer.getBoundingClientRect()
    const top = rect.top + rect.height / 2 - box.top
    return { id, top: clampPillTop(top, box.height) }
  }

  const reposition = () => {
    if (hovered) setHovered(place(hovered.id))
    if (focused) setFocused(place(focused.id))
  }

  const hint = hovered ?? focused
  const hintItem = hint ? items.find((item) => item.id === hint.id) : undefined
  const showTotal = total !== undefined && total !== null
  const bottomButtonLabel = jumpBottomLabel ?? 'Jump to bottom'

  return (
    <nav
      ref={navRef}
      data-precision-ruler
      aria-label={label}
      className={cn(
        placement === 'floating'
          ? 'pointer-events-none absolute inset-y-3 right-1.5 z-20 flex w-7 flex-col items-end justify-center max-nav:hidden'
          : 'relative flex w-8 shrink-0 flex-col items-end justify-center max-nav:hidden',
        className,
      )}
    >
      <div className="pointer-events-auto relative flex max-h-full w-full flex-col items-end gap-1.5 opacity-70 transition-opacity duration-150 hover:opacity-100 focus-within:opacity-100">
        <ol
          ref={listRef}
          onScroll={reposition}
          className="no-scrollbar relative flex min-h-0 w-full flex-col overflow-x-hidden overflow-y-auto border-r border-hairline/70 py-1 pr-0.5 [[data-skin=plate]_&]:border-ink/35"
        >
          {items.map((item, index) => {
            const active = activeSet.has(item.id)
            const highlighted = hint?.id === item.id
            const rangeCount = activeRuns.get(index)
            return (
              <li key={item.id} className="relative flex min-h-2.5 flex-[0_1_18px] items-center justify-end">
                {rangeCount !== undefined && rangeCount > 1 && (
                  <span
                    aria-hidden="true"
                    data-ruler-range
                    data-count={rangeCount}
                    style={{ height: `calc(${(rangeCount - 1) * 100}% + 8px)` }}
                    className="pointer-events-none absolute top-[calc(50%-4px)] right-0 z-10 w-[2px] rounded-full bg-ink/60 [[data-skin=plate]_&]:bg-riso-yellow"
                  />
                )}
                <button
                  type="button"
                  aria-label={item.label}
                  aria-current={active ? 'true' : undefined}
                  onClick={() => onSelect(item.id)}
                  onPointerEnter={() => setHovered(place(item.id))}
                  onPointerLeave={() => setHovered((prev) => (prev?.id === item.id ? null : prev))}
                  onPointerDown={() => {
                    pointing.current = true
                    setFocused(null)
                  }}
                  onPointerUp={() => {
                    pointing.current = false
                  }}
                  onFocus={() => {
                    setFocused(pointing.current ? null : place(item.id))
                    pointing.current = false
                  }}
                  onBlur={() => setFocused((prev) => (prev?.id === item.id ? null : prev))}
                  className="group relative flex h-full w-full cursor-pointer items-center justify-end py-0.5 pr-1 outline-none focus-visible:shadow-none"
                >
                  <span
                    aria-hidden="true"
                    data-ruler-tick
                    data-major={item.major ? 'true' : undefined}
                    data-active={active ? 'true' : undefined}
                    data-tone={item.tone && item.tone !== 'default' ? item.tone : undefined}
                    className={cn(
                      'block shrink-0 rounded-full transition-[width,background-color] duration-150 group-hover:w-6 group-focus-visible:w-6',
                      active ? 'h-[2px] w-5' : item.major ? 'h-[1.5px] w-3.5' : 'h-[1.5px] w-2',
                      highlighted && 'w-6',
                      tickColorClass(item.tone, active, Boolean(item.major), highlighted),
                    )}
                  />
                </button>
              </li>
            )
          })}
        </ol>
        {(onJumpBottom || showTotal) && (
          <div className="flex shrink-0 flex-col items-end gap-1 pt-0.5">
            {onJumpBottom && (
              <button
                type="button"
                data-ruler-latest
                aria-label={bottomButtonLabel}
                title={bottomButtonLabel}
                onClick={onJumpBottom}
                className="inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded-full border border-hairline-strong bg-surface-elevated text-ink-700 hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus [[data-skin=plate]_&]:border-ink/40 [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:hover:bg-riso-yellow"
              >
                <ArrowDownIcon aria-hidden="true" className="size-3" strokeWidth={2.2} />
              </button>
            )}
            {showTotal &&
              (onJumpBottom ? (
                <button
                  type="button"
                  data-ruler-total
                  title={totalLabel ?? bottomButtonLabel}
                  onClick={onJumpBottom}
                  className="shrink-0 cursor-pointer rounded-stamp px-0.5 font-mono text-[10px] leading-none font-semibold text-ink-500 tabular-nums outline-none hover:text-ink focus-visible:shadow-focus"
                >
                  {totalLabel ? (
                    <>
                      <span aria-hidden="true">{total}</span>
                      <span className="sr-only">{totalLabel}</span>
                    </>
                  ) : (
                    <span>{total}</span>
                  )}
                </button>
              ) : (
                <div
                  data-ruler-total
                  className="shrink-0 px-0.5 font-mono text-[10px] leading-none font-semibold text-ink-500 tabular-nums"
                >
                  {totalLabel ? (
                    <>
                      <span aria-hidden="true">{total}</span>
                      <span className="sr-only">{totalLabel}</span>
                    </>
                  ) : (
                    <span>{total}</span>
                  )}
                </div>
              ))}
          </div>
        )}
      </div>
      {hint && hintItem && (
        <div
          aria-hidden="true"
          data-ruler-preview
          style={{ top: hint.top }}
          className="pointer-events-none absolute right-full z-30 mr-2 flex w-max max-w-[min(320px,55vw)] -translate-y-1/2 items-center gap-2 rounded-lg border border-hairline-strong bg-surface-elevated/95 px-2.5 py-1 text-[12px] leading-4 text-ink shadow-floating backdrop-blur-md [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-tooltip-bg [[data-skin=plate]_&]:text-tooltip-fg"
        >
          {hintItem.badge && (
            <span
              data-ruler-preview-badge
              className="shrink-0 font-mono text-[10.5px] text-ink-500 tabular-nums [[data-skin=plate]_&]:text-tooltip-muted"
            >
              {hintItem.badge}
            </span>
          )}
          <span className="truncate font-medium">{hintItem.label}</span>
          {hintItem.sublabel && (
            <span
              data-ruler-preview-sublabel
              className="shrink-0 text-[11px] text-ink-500 [[data-skin=plate]_&]:text-tooltip-muted"
            >
              {hintItem.sublabel}
            </span>
          )}
        </div>
      )}
    </nav>
  )
}

export interface SectionRulerAnchor {
  id: string
  titleId?: string
  title: string
  danger?: boolean
}

// Tracks the currently active section within a scroll root and handles hash and direct jumps.
// eslint-disable-next-line react-refresh/only-export-components
export function useSectionRuler(sections: SectionRulerAnchor[], rootRef?: RefObject<HTMLElement | null>) {
  const [activeId, setActiveId] = useState<string | null>(null)
  const { hash } = useLocation()
  const sectionsRef = useRef(sections)
  useLayoutEffect(() => {
    sectionsRef.current = sections
  })

  const idsKey = sections.map((section) => section.id).join('\0')

  useEffect(() => {
    if (typeof IntersectionObserver === 'undefined') return
    const ids = idsKey ? idsKey.split('\0') : []
    if (ids.length === 0) return
    const visible = new Set<string>()
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) visible.add(entry.target.id)
          else visible.delete(entry.target.id)
        }
        const first = ids.find((id) => visible.has(id))
        if (first) setActiveId(first)
      },
      { root: rootRef?.current ?? null, rootMargin: '0px 0px -55% 0px' },
    )
    for (const id of ids) {
      const element = document.getElementById(id)
      if (element) observer.observe(element)
    }
    return () => observer.disconnect()
  }, [idsKey, rootRef])

  useEffect(() => {
    if (!hash || !hash.startsWith('#')) return
    const targetId = hash.slice(1)
    const section = sectionsRef.current.find((entry) => entry.id === targetId)
    if (!section) return
    document.getElementById(section.id)?.scrollIntoView({ block: 'start' })
    if (section.titleId) {
      document.getElementById(section.titleId)?.focus({ preventScroll: true })
    }
  }, [hash, idsKey])

  const jumpTo = useCallback((id: string) => {
    const section = sectionsRef.current.find((entry) => entry.id === id)
    if (typeof IntersectionObserver !== 'undefined') setActiveId(id)
    document.getElementById(id)?.scrollIntoView({ block: 'start' })
    if (section?.titleId) {
      document.getElementById(section.titleId)?.focus({ preventScroll: true })
    }
  }, [])

  return { activeId, jumpTo }
}

export interface PageRulerProps {
  sections: SectionRulerAnchor[]
  label: string
  rootRef?: RefObject<HTMLElement | null>
  placement?: 'floating' | 'inline'
  minItems?: number
  className?: string
}

// Convenience wrapper that turns page section anchors into a numbered PrecisionRuler.
export function PageRuler({ sections, label, rootRef, placement, minItems, className }: PageRulerProps) {
  const { activeId, jumpTo } = useSectionRuler(sections, rootRef)
  const items: PrecisionRulerItem[] = sections.map((section, index) => ({
    id: section.id,
    label: section.title,
    badge: String(index + 1).padStart(2, '0'),
    major: true,
    tone: section.danger ? 'danger' : 'default',
  }))
  return (
    <PrecisionRuler
      items={items}
      activeIds={activeId}
      onSelect={jumpTo}
      label={label}
      placement={placement}
      minItems={minItems}
      className={className}
    />
  )
}

interface ScrollMetrics {
  scrollable: boolean
  thumbStart: number
  thumbEnd: number
  percent: number
}

const INACTIVE_SCROLL: ScrollMetrics = {
  scrollable: false,
  thumbStart: 0,
  thumbEnd: 1,
  percent: 0,
}

function measureScroll(el: HTMLElement | null): ScrollMetrics {
  if (!el || el.clientHeight <= 0 || el.scrollHeight <= el.clientHeight + SCROLL_OVERFLOW_THRESHOLD) {
    return INACTIVE_SCROLL
  }
  const maxScroll = Math.max(1, el.scrollHeight - el.clientHeight)
  const top = clamp(el.scrollTop, 0, maxScroll)
  const thumbStart = clamp(top / el.scrollHeight, 0, 1)
  const thumbEnd = clamp((top + el.clientHeight) / el.scrollHeight, 0, 1)
  const percent = Math.round((top / maxScroll) * 100)
  return { scrollable: true, thumbStart, thumbEnd, percent }
}

function sameScrollMetrics(a: ScrollMetrics, b: ScrollMetrics): boolean {
  return (
    a.scrollable === b.scrollable &&
    a.percent === b.percent &&
    Math.abs(a.thumbStart - b.thumbStart) < 0.001 &&
    Math.abs(a.thumbEnd - b.thumbEnd) < 0.001
  )
}

export interface ScrollRulerProps {
  targetRef: RefObject<HTMLElement | null>
  label?: string
  tickCount?: number
  className?: string
}

// Automatic right-edge vernier scroll indicator for any overflowing scroll container.
export function ScrollRuler({ targetRef, label, tickCount = 17, className }: ScrollRulerProps) {
  const outerRef = useRef<HTMLDivElement>(null)
  const railRef = useRef<HTMLDivElement>(null)
  const draggingRef = useRef(false)
  const [metrics, setMetrics] = useState<ScrollMetrics>(INACTIVE_SCROLL)
  const [hoverTop, setHoverTop] = useState<number | null>(null)
  const [dragging, setDragging] = useState(false)

  useLayoutEffect(() => {
    const el = targetRef.current
    if (!el) return

    const update = () => {
      const next = measureScroll(el)
      setMetrics((prev) => (sameScrollMetrics(prev, next) ? prev : next))
    }

    update()
    el.addEventListener('scroll', update, { passive: true })
    window.addEventListener('resize', update, { passive: true })

    const resizeObserver = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(update)
    resizeObserver?.observe(el)

    const mutationObserver = typeof MutationObserver === 'undefined' ? null : new MutationObserver(update)
    mutationObserver?.observe(el, { childList: true, subtree: true })

    return () => {
      el.removeEventListener('scroll', update)
      window.removeEventListener('resize', update)
      resizeObserver?.disconnect()
      mutationObserver?.disconnect()
    }
  }, [targetRef])

  if (!metrics.scrollable) return null

  const count = Math.max(2, Math.floor(tickCount))
  const halfStep = 0.5 / (count - 1)

  const pillTopFromEvent = (clientY: number): number => {
    const outer = outerRef.current
    if (!outer) return 0
    const box = outer.getBoundingClientRect()
    if (box.height <= 0) return 0
    return clampPillTop(clientY - box.top, box.height)
  }

  const jumpToRatio = (ratio: number) => {
    const el = targetRef.current
    if (!el) return
    const maxScroll = Math.max(0, el.scrollHeight - el.clientHeight)
    el.scrollTop = Math.round(clamp(ratio, 0, 1) * maxScroll)
    const next = measureScroll(el)
    setMetrics((prev) => (sameScrollMetrics(prev, next) ? prev : next))
  }

  const ratioFromPointer = (clientY: number): number => {
    const rail = railRef.current
    if (!rail) return 0
    const rect = rail.getBoundingClientRect()
    if (rect.height <= 0) return 0
    return clamp((clientY - rect.top) / rect.height, 0, 1)
  }

  const handlePointerEnter = (event: PointerEvent<HTMLDivElement>) => {
    setHoverTop(pillTopFromEvent(event.clientY))
  }

  const handlePointerDown = (event: PointerEvent<HTMLDivElement>) => {
    draggingRef.current = true
    setDragging(true)
    try {
      event.currentTarget.setPointerCapture?.(event.pointerId)
    } catch {
      // Ignore synthetic pointer events without an active pointer id.
    }
    jumpToRatio(ratioFromPointer(event.clientY))
    setHoverTop(pillTopFromEvent(event.clientY))
  }

  const handlePointerMove = (event: PointerEvent<HTMLDivElement>) => {
    if (draggingRef.current) {
      jumpToRatio(ratioFromPointer(event.clientY))
    }
    setHoverTop(pillTopFromEvent(event.clientY))
  }

  const finishDrag = (event: PointerEvent<HTMLDivElement>) => {
    if (!draggingRef.current) return
    draggingRef.current = false
    setDragging(false)
    try {
      if (event.currentTarget.hasPointerCapture?.(event.pointerId)) {
        event.currentTarget.releasePointerCapture?.(event.pointerId)
      }
    } catch {
      // Ignore synthetic pointer events without an active pointer id.
    }
  }

  const handlePointerLeave = () => {
    if (!draggingRef.current) setHoverTop(null)
  }

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const currentRatio = metrics.percent / 100
    const step = 0.1
    if (event.key === 'ArrowDown' || event.key === 'PageDown') {
      event.preventDefault()
      jumpToRatio(currentRatio + (event.key === 'PageDown' ? 0.25 : step))
    } else if (event.key === 'ArrowUp' || event.key === 'PageUp') {
      event.preventDefault()
      jumpToRatio(currentRatio - (event.key === 'PageUp' ? 0.25 : step))
    } else if (event.key === 'Home') {
      event.preventDefault()
      jumpToRatio(0)
    } else if (event.key === 'End') {
      event.preventDefault()
      jumpToRatio(1)
    }
  }

  return (
    <div
      ref={outerRef}
      data-scroll-ruler
      className={cn(
        'pointer-events-none absolute inset-y-4 right-1.5 z-10 flex w-5 flex-col items-end justify-center max-nav:hidden',
        className,
      )}
    >
      <div
        ref={railRef}
        role="slider"
        tabIndex={0}
        aria-orientation="vertical"
        aria-label={label ?? 'Scroll position'}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={metrics.percent}
        data-scroll-ruler-rail
        onPointerEnter={handlePointerEnter}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={finishDrag}
        onPointerCancel={finishDrag}
        onPointerLeave={handlePointerLeave}
        onKeyDown={handleKeyDown}
        className={cn(
          'pointer-events-auto relative flex cursor-pointer touch-none flex-col items-end justify-between gap-1.5 border-r border-hairline/60 py-1 pr-0.5 opacity-60 transition-opacity duration-150 outline-none select-none hover:opacity-100 focus-visible:opacity-100 focus-visible:shadow-focus [[data-skin=plate]_&]:border-ink/35',
          dragging && 'opacity-100',
        )}
      >
        {Array.from({ length: count }, (_, index) => {
          const position = index / (count - 1)
          const major = index % 4 === 0
          const active = position + halfStep >= metrics.thumbStart && position - halfStep <= metrics.thumbEnd
          return (
            <span
              key={index}
              aria-hidden="true"
              data-scroll-tick
              data-major={major ? 'true' : undefined}
              data-active={active ? 'true' : undefined}
              className={cn(
                'block shrink-0 rounded-full transition-[width,background-color] duration-150',
                active
                  ? 'h-[2px] w-4 bg-riso-blue'
                  : major
                    ? 'h-[1.5px] w-2.5 bg-ink/35'
                    : 'h-[1.5px] w-1.5 bg-ink/20',
              )}
            />
          )
        })}
      </div>
      {hoverTop !== null && (
        <div
          aria-hidden="true"
          data-scroll-ruler-preview
          style={{ top: hoverTop }}
          className="pointer-events-none absolute right-full z-30 mr-2 -translate-y-1/2 rounded-md border border-hairline-strong bg-surface-elevated/95 px-1.5 py-0.5 font-mono text-[10.5px] leading-4 font-medium text-ink tabular-nums shadow-floating backdrop-blur-md [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-tooltip-bg [[data-skin=plate]_&]:text-tooltip-fg"
        >
          {metrics.percent}%
        </div>
      )}
    </div>
  )
}
