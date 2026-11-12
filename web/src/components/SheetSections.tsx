import { useEffect, useState, type ReactNode } from 'react'
import { useLocation } from 'react-router'
import { HollowNumber } from '@/components/NumberedSection'
import { RulerMark, RulerTicks, Stub } from '@/components/Sheet'
import { cn } from '@/lib/utils'

// 设置一类页面（Agent 配置、项目设置）的编号分区与票根里的目录尺：同一套，按 id 找到彼此。
export interface SectionTocItem {
  // 分区元素的 id（目录项的锚点）与分区标题的 id（跳转后移入焦点）。
  id: string
  titleId: string
  title: string
  // 删除一类的分区：编号、标题与目录项是红色。
  danger?: boolean
}

interface SheetSectionProps {
  id: string
  titleId: string
  // 第几个分区（从 0 起），编号显示为 01、02…，与目录尺上的编号一致。
  index: number
  title: string
  // 标题旁的条目数，只作视觉提示；分区名称仍是标题本身。
  count?: number
  actions?: ReactNode
  // danger：删除一类的分区，编号与标题红色，内容放在红色斜纹的底上。
  tone?: 'default' | 'danger'
  children: ReactNode
}

// 编号分区（改版通用规则第 4 条）：左侧空心编号，标题是 ZCOOL 30px 加一笔黄色记号笔；分区直接排在纸上，
// 彼此之间一道 2px 虚线（第一个上面没有）。标题可以接收焦点，供目录跳转后移入焦点。
export function SheetSection({ id, titleId, index, title, count, actions, tone = 'default', children }: SheetSectionProps) {
  const danger = tone === 'danger'
  return (
    <section id={id} aria-label={title} className={cn('flex scroll-mt-6 gap-[18px] text-ink', index > 0 && 'border-t-2 border-dashed border-ink/[0.22] pt-6')}>
      <HollowNumber data-section-number className={cn('pt-0.5', danger && '[-webkit-text-stroke:1.6px_var(--red-text)]')}>
        {String(index + 1).padStart(2, '0')}
      </HollowNumber>
      <div className="flex min-w-0 flex-1 flex-col gap-[18px]">
        <div className="flex flex-wrap items-center gap-2.5">
          <h2
            id={titleId}
            tabIndex={-1}
            className={cn('riso-marker self-start px-1.5 font-display-cn text-[30px] leading-[1.25] font-normal outline-none focus-visible:shadow-focus', danger && 'text-red-text')}
          >
            {title}
            {count !== undefined && (
              <span aria-hidden="true" className="ml-2 font-display-latin text-[20px] font-extrabold text-ink-500">
                {count}
              </span>
            )}
          </h2>
          {actions && <div className="ml-auto flex flex-wrap gap-1.5">{actions}</div>}
        </div>
        <div data-section-body className={cn('flex min-w-0 flex-col gap-3.5', danger && 'riso-stripes-red rounded-panel bg-error-field px-5 py-[18px]')}>
          {children}
        </div>
      </div>
    </section>
  )
}

const observable = () => typeof IntersectionObserver !== 'undefined'

// 分区进入视口上部（上方 45%）时算作当前分区，多个同时可见时取靠前的一个。
// 浏览器不支持 IntersectionObserver 时不高亮，其余功能不受影响。
function useActiveSection(ids: string[]) {
  const [active, setActive] = useState<string | null>(null)
  const key = ids.join(' ')
  useEffect(() => {
    if (!observable()) return
    const visible = new Set<string>()
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) visible.add(entry.target.id)
          else visible.delete(entry.target.id)
        }
        const first = ids.find((id) => visible.has(id))
        if (first) setActive(first)
      },
      { rootMargin: '0px 0px -55% 0px' },
    )
    for (const id of ids) {
      const element = document.getElementById(id)
      if (element) observer.observe(element)
    }
    return () => observer.disconnect()
  }, [key]) // eslint-disable-line react-hooks/exhaustive-deps
  return [active, setActive] as const
}

// 分区目录：纸张右侧票根里的一把目录尺（改版通用规则第 5 条）。右缘一排刻度，每项是“名称 编号 短刻度”，
// 当前分区那一项用略微倾斜的黄色记号笔加粉色小箭头标出；删除一类的分区一项是红色。窄屏（左侧栏收进抽屉时）不显示。
// 点击目录项滚动到对应分区，并把焦点移到分区标题；不用平滑滚动，控制台只保留三个动效。
// 编号在 DOM 里排在名称前面（读屏读成“01 基本信息”），显示时用 order 排到名称后面。
// 带着分区地址打开页面（例如统计页的“去看它的提示词”）时，滚动到那个分区并把焦点移到它的标题。
export function SectionToc({ label, items, className }: { label: string; items: SectionTocItem[]; className?: string }) {
  const [active, setActive] = useActiveSection(items.map((item) => item.id))
  const { hash } = useLocation()

  useEffect(() => {
    const item = items.find((entry) => hash === `#${entry.id}`)
    if (!item) return
    document.getElementById(item.id)?.scrollIntoView({ block: 'start' })
    document.getElementById(item.titleId)?.focus({ preventScroll: true })
  }, [hash]) // eslint-disable-line react-hooks/exhaustive-deps

  const jump = (item: SectionTocItem) => {
    // 先按点击的目录项高亮：靠近页尾的分区滚动后可能到不了视口上部。不支持观察时不高亮，免得滚动后高亮停在旧的分区上。
    if (observable()) setActive(item.id)
    document.getElementById(item.id)?.scrollIntoView({ block: 'start' })
    document.getElementById(item.titleId)?.focus({ preventScroll: true })
  }

  return (
    <Stub as="nav" aria-label={label} label={label} className={cn('w-[196px] overflow-x-hidden overflow-y-auto', className)}>
      <ol className="relative flex flex-col text-[13.5px] font-bold">
        <RulerTicks className="bg-[repeating-linear-gradient(to_bottom,transparent_0,transparent_10px,var(--ink)_10px,var(--ink)_12px,transparent_12px,transparent_23px)]" />
        {items.map((item, index) => {
          const current = item.id === active
          return (
            <li key={item.id} className="relative">
              {current && <RulerMark data-ruler-mark className="top-[5px] h-9" />}
              <a
                href={`#${item.id}`}
                aria-current={current ? 'location' : undefined}
                onClick={(event) => {
                  event.preventDefault()
                  jump(item)
                }}
                className={cn(
                  'relative flex h-[46px] items-center justify-end gap-2.5 pl-[18px] no-underline outline-none focus-visible:shadow-focus',
                  current ? 'font-black text-ink' : 'text-ink-700 hover:text-ink',
                  item.danger && 'text-red-text hover:text-red-text',
                )}
              >
                <span aria-hidden="true" className="order-2 font-mono text-[10.5px]">
                  {String(index + 1).padStart(2, '0')}
                </span>
                <span className="order-1 min-w-0 truncate">{item.title}</span>
                <span aria-hidden="true" className={cn('order-3 h-0.5 w-6 shrink-0', item.danger ? 'bg-red-text' : 'bg-ink')} />
              </a>
            </li>
          )
        })}
      </ol>
    </Stub>
  )
}
