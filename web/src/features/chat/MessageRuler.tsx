import { useLayoutEffect, useRef, useState } from 'react'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// 刻度尺滚动跟随可见轮时，刻度与上下边缘留出的距离。
const FOLLOW_MARGIN = 16

export interface RulerTurn {
  key: string
  label: string
}

type Hint = { key: string; top: number }

// 对话区右侧的刻度尺：一条刻度一轮，从上到下与对话流同序；可视范围内的轮次刻度加深。
// 悬停或聚焦刻度时加深、变长，并在左侧单行显示这一轮的开头；点击跳到那一轮。
// 刻度先按 20px 的间距排，放不下时压缩到 8px，再放不下就让刻度尺自己滚动，并跟着可见轮滚动。
// 不加过渡与平滑滚动：控制台只保留三个动效，减少动态效果时也就不用另做处理。
export function MessageRuler({ turns, visible, onJump, className }: { turns: RulerTurn[]; visible: ReadonlySet<string>; onJump: (key: string) => void; className?: string }) {
  const nav = useRef<HTMLElement>(null)
  const list = useRef<HTMLOListElement>(null)
  // 悬停与聚焦各记一份；两者都有时显示悬停的那条。
  const [hovered, setHovered] = useState<Hint | null>(null)
  const [focused, setFocused] = useState<Hint | null>(null)
  // 键盘聚焦时才保留预览：鼠标点击也会让按钮获得焦点（按下时），那时移开鼠标预览就该消失。
  const pointing = useRef(false)

  // 刻度尺可以滚动时，让可见轮的刻度留在刻度尺的可视范围里；可见轮多到放不下时以最上面的一条为准。
  const visibleIndexes = turns.flatMap((turn, index) => (visible.has(turn.key) ? [index] : [])).join(',')
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

  // 预览显示在刻度尺左侧、与刻度对齐。刻度尺会滚动并裁掉溢出的内容，预览因此放在外层，按刻度的位置算出高度。
  const place = (key: string): Hint | null => {
    const index = turns.findIndex((turn) => turn.key === key)
    const tick = index >= 0 ? list.current?.children[index] : undefined
    const outer = nav.current
    if (!tick || !outer) return null
    const rect = tick.getBoundingClientRect()
    return { key, top: rect.top + rect.height / 2 - outer.getBoundingClientRect().top }
  }
  const replace = () => {
    if (hovered) setHovered(place(hovered.key))
    if (focused) setFocused(place(focused.key))
  }

  const hint = hovered ?? focused
  const hintLabel = hint && turns.find((turn) => turn.key === hint.key)?.label
  return (
    <nav ref={nav} aria-label={t.chat.ruler.label} className={cn('relative flex w-8 shrink-0 flex-col border-l border-border', className)}>
      <ol ref={list} onScroll={replace} className="no-scrollbar relative flex min-h-0 flex-1 flex-col overflow-y-auto py-6">
        {turns.map((turn) => {
          const current = visible.has(turn.key)
          return (
            <li key={turn.key} className="flex min-h-2 flex-[0_1_20px]">
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
                className="group flex w-full cursor-pointer items-center justify-center focus-visible:shadow-none"
              >
                <span
                  aria-hidden="true"
                  className={cn(
                    'h-[3px] w-3 rounded-full group-hover:w-4 group-hover:bg-ink group-focus-visible:w-4 group-focus-visible:bg-ink group-focus-visible:shadow-focus',
                    current ? 'bg-ink-500' : 'bg-border',
                  )}
                />
              </button>
            </li>
          )
        })}
      </ol>
      {hint && hintLabel && (
        <div
          aria-hidden="true"
          data-ruler-preview
          style={{ top: hint.top }}
          className="pointer-events-none absolute right-full z-10 mr-1.5 w-max max-w-[min(360px,60vw)] -translate-y-1/2 truncate rounded-ticket bg-ink px-3 py-1.5 text-[12.5px] leading-5 font-bold text-white"
        >
          {hintLabel}
        </div>
      )}
    </nav>
  )
}
