import { useLayoutEffect, useRef, useState } from 'react'
import { ArrowDownIcon } from 'lucide-react'
import { RulerMark, Stub } from '@/components/Sheet'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// When the ruler scrolls to follow the visible turns, the distance kept between the ticks and the top and bottom edges.
const FOLLOW_MARGIN = 16
// The preview's center line (the preview is about 32px tall) stays at least this far from the ruler's top and bottom edges, so it
// doesn't stick out of the sheet and get clipped.
const HINT_HALF = 16

export interface RulerTurn {
  key: string
  label: string
}

type Hint = { key: string; top: number }

interface MessageRulerProps {
  turns: RulerTurn[]
  visible: ReadonlySet<string>
  onJump: (key: string) => void
  // Jump to the true bottom of the conversation and re-enable auto-follow.
  onJumpBottom?: () => void
  // The turn number of the first tick. null when unknown (earlier history isn't fully loaded and no count of the preceding turns was
  // given): only ticks are drawn, without numbers.
  firstNumber: number | null
  // The total turn count at the bottom; not shown when unknown.
  total: number | null
  className?: string
}

// The ruler in the stub on the right of the sheet, replacing the native scrollbar: one tick per turn, against the right edge of the
// sheet, in the same top-to-bottom order as the conversation flow; every 5 turns a long tick with a number, and the total turn count at
// the bottom.
// The currently visible turns are covered by one yellow highlighter mark (from the first visible tick to the last), with a pink arrow on
// the left.
// Hovering or focusing a tick makes it longer and pink and shows the start of that turn on one line to its left; clicking jumps to that
// turn.
// Tick spacing follows the stub's height, at most 23px and at least 8px; when they still don't fit, the ruler scrolls on its own and
// follows the visible turns.
// No transitions or smooth scrolling: the console keeps only three animations, so reduced motion needs no special handling either.
export function MessageRuler({ turns, visible, onJump, onJumpBottom, firstNumber, total, className }: MessageRulerProps) {
  const nav = useRef<HTMLElement>(null)
  const list = useRef<HTMLOListElement>(null)
  // Hover and focus are tracked separately; when both are set, show the hovered one.
  const [hovered, setHovered] = useState<Hint | null>(null)
  const [focused, setFocused] = useState<Hint | null>(null)
  // Keep the preview only for keyboard focus: a mouse click also focuses the button (on press), and then the preview should disappear
  // once the mouse moves away.
  const pointing = useRef(false)

  const visibleList = turns.flatMap((turn, index) => (visible.has(turn.key) ? [index] : []))
  const firstVisible = visibleList[0] ?? -1
  const lastVisible = visibleList[visibleList.length - 1] ?? -1
  // When the ruler can scroll, keep the ticks of the visible turns within its visible range; when there are too many visible turns to
  // fit, the topmost one wins.
  const visibleIndexes = visibleList.join(',')
  useLayoutEffect(() => {
    const el = list.current
    if (!el || visibleIndexes === '' || el.scrollHeight <= el.clientHeight) return
    const indexes = visibleIndexes.split(',').map(Number)
    const first = el.children[indexes[0]] as HTMLElement | undefined
    const last = el.children[indexes[indexes.length - 1]] as HTMLElement | undefined
    if (!first || !last) return
    const top = first.offsetTop - FOLLOW_MARGIN
    const bottom = last.offsetTop + last.offsetHeight + FOLLOW_MARGIN
    if (top < el.scrollTop) el.scrollTop = top
    else if (bottom > el.scrollTop + el.clientHeight) el.scrollTop = Math.min(top, bottom - el.clientHeight)
  }, [visibleIndexes])

  // The preview shows to the left of the stub, aligned with the tick. The ruler scrolls and clips overflowing content, so the preview
  // lives in the outer layer, with its height (vertical position) computed from the tick's position,
  // clamped within the stub's top and bottom edges.
  const place = (key: string): Hint | null => {
    const index = turns.findIndex((turn) => turn.key === key)
    const tick = index >= 0 ? list.current?.children[index] : undefined
    const outer = nav.current
    if (!tick || !outer) return null
    const rect = tick.getBoundingClientRect()
    const box = outer.getBoundingClientRect()
    const top = rect.top + rect.height / 2 - box.top
    return { key, top: Math.max(Math.min(HINT_HALF, box.height / 2), Math.min(top, box.height - HINT_HALF)) }
  }
  const replace = () => {
    if (hovered) setHovered(place(hovered.key))
    if (focused) setFocused(place(focused.key))
  }

  const hint = hovered ?? focused
  const hintLabel = hint && turns.find((turn) => turn.key === hint.key)?.label
  return (
    <Stub as="nav" ref={nav} data-turn-stub aria-label={t.chat.ruler.label} label={t.chat.ruler.title} className={cn('relative w-[74px]', className)}>
      <ol ref={list} onScroll={replace} className="no-scrollbar relative flex min-h-0 flex-1 flex-col overflow-x-hidden overflow-y-auto py-2 font-mono text-[10.5px] leading-none font-bold text-ink-700">
        {turns.map((turn, index) => {
          const current = visible.has(turn.key)
          const number = firstNumber === null ? null : firstNumber + index
          const long = number !== null && number % 5 === 0
          return (
            <li key={turn.key} className="relative flex min-h-2 flex-[0_1_23px]">
              {/* The highlighter mark is drawn in the cell of the first visible tick: every cell is the same height, so the height to
                  cover is computed from the number of cells, with no measuring needed when the tick spacing changes. */}
              {index === firstVisible && (
                <RulerMark
                  data-ruler-mark
                  data-count={lastVisible - firstVisible + 1}
                  className="top-[calc(50%-10px)]"
                  style={{ height: `calc(${(lastVisible - firstVisible) * 100}% + 19px)` }}
                />
              )}
              <button
                type="button"
                aria-label={turn.label}
                aria-current={current ? 'true' : undefined}
                onClick={() => onJump(turn.key)}
                onPointerEnter={() => setHovered(place(turn.key))}
                onPointerLeave={() => setHovered((hint) => (hint?.key === turn.key ? null : hint))}
                onPointerDown={() => (pointing.current = true)}
                onPointerUp={() => (pointing.current = false)}
                onFocus={() => {
                  setFocused(pointing.current ? null : place(turn.key))
                  pointing.current = false
                }}
                onBlur={() => setFocused((hint) => (hint?.key === turn.key ? null : hint))}
                className="group relative flex w-full cursor-pointer items-center justify-end gap-1.5 focus-visible:shadow-none"
              >
                {long && (
                  <span aria-hidden="true" data-ruler-number className={cn(current && 'text-ink')}>
                    {number}
                  </span>
                )}
                <span
                  aria-hidden="true"
                  data-ruler-long={long || undefined}
                  className={cn(
                    'h-0.5 shrink-0 bg-ink/35 group-hover:bg-ink group-focus-visible:bg-ink group-focus-visible:shadow-focus [[data-skin=plate]_&]:bg-ink [[data-skin=plate]_&]:group-hover:bg-riso-pink [[data-skin=plate]_&]:group-focus-visible:bg-riso-pink',
                    long ? 'w-6 group-hover:w-[30px] group-focus-visible:w-[30px]' : 'w-3 group-hover:w-[18px] group-focus-visible:w-[18px]',
                  )}
                />
              </button>
            </li>
          )
        })}
      </ol>
      {onJumpBottom && (
        <button
          type="button"
          data-ruler-latest
          aria-label={t.chat.ruler.latest}
          title={t.chat.ruler.latest}
          onClick={onJumpBottom}
          className="mx-auto inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-full border border-hairline-strong bg-surface-control text-ink-700 hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus [[data-skin=plate]_&]:border-ink/40 [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:hover:bg-riso-yellow"
        >
          <ArrowDownIcon aria-hidden="true" className="size-3.5" strokeWidth={2.4} />
        </button>
      )}
      {total !== null && (
        onJumpBottom ? (
          <button
            type="button"
            data-ruler-total
            title={t.chat.ruler.latest}
            onClick={onJumpBottom}
            className="shrink-0 cursor-pointer rounded-stamp text-center font-display-latin text-[22px] leading-none font-extrabold text-riso-blue outline-none hover:text-ink focus-visible:shadow-focus"
          >
            <span aria-hidden="true">{total}</span>
            <span className="sr-only">{t.chat.ruler.total(total)}</span>
          </button>
        ) : (
          <div data-ruler-total className="shrink-0 text-center font-display-latin text-[22px] leading-none font-extrabold text-riso-blue">
            <span aria-hidden="true">{total}</span>
            <span className="sr-only">{t.chat.ruler.total(total)}</span>
          </div>
        )
      )}
      {hint && hintLabel && (
        <div
          aria-hidden="true"
          data-ruler-preview
          style={{ top: hint.top }}
          className="pointer-events-none absolute right-full z-10 mr-1.5 w-max max-w-[min(360px,60vw)] -translate-y-1/2 truncate rounded-ticket bg-tooltip-bg px-3 py-1.5 text-[12.5px] leading-5 font-semibold text-tooltip-fg shadow-floating [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:shadow-none"
        >
          {hintLabel}
        </div>
      )}
    </Stub>
  )
}
