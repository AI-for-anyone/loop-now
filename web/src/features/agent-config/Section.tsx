import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { sectionId, sectionTitleId, type SectionKey } from './sections'

interface SectionProps {
  id: SectionKey
  title: string
  // 标题旁的条目数，只作视觉提示；区块名称仍是标题本身。
  count?: number
  actions?: ReactNode
  // danger：删除 Agent 用的红色斜纹危险卡片。
  tone?: 'default' | 'danger'
  children: ReactNode
}

// 区块卡片：纸白底、18 圆角、轻投影，标题为 ZCOOL 30。标题可以接收焦点，供分区目录跳转后移入焦点。
export function Section({ id, title, count, actions, tone = 'default', children }: SectionProps) {
  return (
    <section
      id={sectionId(id)}
      aria-label={title}
      className={cn(
        'flex scroll-mt-6 flex-col gap-3.5 rounded-panel px-6 py-[22px] text-ink shadow-print-rest',
        tone === 'danger' ? 'riso-stripes-red bg-error-field' : 'bg-paper-raised',
      )}
    >
      <div className="flex flex-wrap items-center gap-2.5">
        <h2
          id={sectionTitleId(id)}
          tabIndex={-1}
          className={cn('font-display-cn text-[30px] leading-[1.15] font-normal', tone === 'danger' && 'text-red-text')}
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
      {children}
    </section>
  )
}
