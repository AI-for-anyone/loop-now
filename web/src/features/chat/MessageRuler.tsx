import { useLayoutEffect, useRef, useState } from 'react'
import { RulerMark, Stub } from '@/components/Sheet'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// 刻度尺滚动跟随可见轮时，刻度与上下边缘留出的距离。
const FOLLOW_MARGIN = 16
// 预览（约 32px 高）的中线离刻度尺上下边至少这么远，免得伸到纸张外面被裁掉。
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
  // 第一条刻度是第几轮。不知道时（更早的历史还没加载完，也没有给出前面的轮数）为 null：只画刻度，不写数字。
  firstNumber: number | null
  // 底部的总轮数；不知道时不显示。
  total: number | null
  className?: string
}

// 纸张右侧票根里的刻度尺，取代原生滚动条：一轮一条刻度，贴着纸张右缘，从上到下与对话流同序；每 5 轮一条长刻度带数字，底部是总轮数。
// 当前可见的轮次用一笔黄色记号笔罩住（从第一条可见刻度到最后一条），左侧一个粉色箭头。
// 悬停或聚焦刻度时刻度变长、变粉，并在左侧单行显示这一轮的开头；点击跳到那一轮。
// 刻度间距随票根高度变化，上限 23px、下限 8px；再放不下就让刻度尺自己滚动，并跟着可见轮滚动。
// 不加过渡与平滑滚动：控制台只保留三个动效，减少动态效果时也就不用另做处理。
export function MessageRuler({ turns, visible, onJump, firstNumber, total, className }: MessageRulerProps) {
  const nav = useRef<HTMLElement>(null)
  const list = useRef<HTMLOListElement>(null)
  // 悬停与聚焦各记一份；两者都有时显示悬停的那条。
  const [hovered, setHovered] = useState<Hint | null>(null)
  const [focused, setFocused] = useState<Hint | null>(null)
  // 键盘聚焦时才保留预览：鼠标点击也会让按钮获得焦点（按下时），那时移开鼠标预览就该消失。
  const pointing = useRef(false)

  const visibleList = turns.flatMap((turn, index) => (visible.has(turn.key) ? [index] : []))
  const firstVisible = visibleList[0] ?? -1
  const lastVisible = visibleList[visibleList.length - 1] ?? -1
  // 刻度尺可以滚动时，让可见轮的刻度留在刻度尺的可视范围里；可见轮多到放不下时以最上面的一条为准。
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

  // 预览显示在票根左侧、与刻度对齐。刻度尺会滚动并裁掉溢出的内容，预览因此放在外层，按刻度的位置算出高度，
  // 并夹在票根上下边之内。
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
      <ol ref={list} onScroll={replace} className="no-scrollbar relative flex min-h-0 flex-1 flex-col overflow-y-auto py-2 font-mono text-[10.5px] leading-none font-bold text-ink-700">
        {turns.map((turn, index) => {
          const current = visible.has(turn.key)
          const number = firstNumber === null ? null : firstNumber + index
          const long = number !== null && number % 5 === 0
          return (
            <li key={turn.key} className="relative flex min-h-2 flex-[0_1_23px]">
              {/* 记号笔画在第一条可见刻度所在的这一格里：每格一样高，按格数算出要罩住的高度，刻度间距变了也不用量。 */}
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
                    'h-0.5 shrink-0 bg-ink group-hover:bg-riso-pink group-focus-visible:bg-riso-pink group-focus-visible:shadow-focus',
                    long ? 'w-6 group-hover:w-[30px] group-focus-visible:w-[30px]' : 'w-3 group-hover:w-[18px] group-focus-visible:w-[18px]',
                  )}
                />
              </button>
            </li>
          )
        })}
      </ol>
      {total !== null && (
        <div data-ruler-total className="shrink-0 text-center font-display-latin text-[22px] leading-none font-extrabold text-riso-blue">
          <span aria-hidden="true">{total}</span>
          <span className="sr-only">{t.chat.ruler.total(total)}</span>
        </div>
      )}
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
    </Stub>
  )
}
