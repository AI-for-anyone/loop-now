import type { ReactNode } from 'react'
import { HollowNumber } from '@/components/NumberedSection'
import { cn } from '@/lib/utils'
import { SECTION_KEYS, sectionId, sectionTitleId, type SectionKey } from './sections'

interface SectionProps {
  id: SectionKey
  title: string
  // 标题旁的条目数，只作视觉提示；区块名称仍是标题本身。
  count?: number
  actions?: ReactNode
  // danger：删除 Agent 一区，编号与标题红色，内容放在红色斜纹的底上。
  tone?: 'default' | 'danger'
  children: ReactNode
}

// 配置页的编号分区（改版通用规则第 4 条）：左侧空心编号（按 SECTION_KEYS 的顺序，01、02…，与目录尺上的编号一致），
// 标题是 ZCOOL 30px 加一笔黄色记号笔；区块直接排在纸上，彼此之间一道 2px 虚线（第一个上面没有）。
// 标题可以接收焦点，供目录跳转后移入焦点。
export function Section({ id, title, count, actions, tone = 'default', children }: SectionProps) {
  const index = SECTION_KEYS.indexOf(id)
  const danger = tone === 'danger'
  return (
    <section
      id={sectionId(id)}
      aria-label={title}
      className={cn('flex scroll-mt-6 gap-[18px] text-ink', index > 0 && 'border-t-2 border-dashed border-ink/[0.22] pt-6')}
    >
      <HollowNumber data-section-number className={cn('pt-0.5', danger && '[-webkit-text-stroke:1.6px_var(--red-text)]')}>
        {String(index + 1).padStart(2, '0')}
      </HollowNumber>
      <div className="flex min-w-0 flex-1 flex-col gap-[18px]">
        <div className="flex flex-wrap items-center gap-2.5">
          <h2
            id={sectionTitleId(id)}
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
