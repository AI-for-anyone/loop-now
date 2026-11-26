import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { sectionId, sectionTitleId, type SectionKey } from './sections'

interface SectionProps {
  id: SectionKey
  title: string
  // Item count next to the title, a visual hint only; the section's name is still just the title.
  count?: number
  actions?: ReactNode
  // A short inline note beside the title.
  meta?: ReactNode
  // danger: the Delete Agent section; title in red, content on a red diagonal-stripe background.
  tone?: 'default' | 'danger'
  children: ReactNode
}

// Compact settings section inside a config sub-tab: clean left-aligned heading with hairline separation
// between sibling sections, preserving id and aria-label landmarks for deep links and screen readers.
export function Section({ id, title, count, actions, meta, tone = 'default', children }: SectionProps) {
  const danger = tone === 'danger'
  return (
    <section
      id={sectionId(id)}
      aria-label={title}
      className="flex min-w-0 scroll-mt-4 flex-col gap-3 text-ink not-first:border-t not-first:border-hairline not-first:pt-5 [[data-skin=plate]_&]:gap-4 [[data-skin=plate]_&]:not-first:border-t-2 [[data-skin=plate]_&]:not-first:border-dashed [[data-skin=plate]_&]:not-first:border-ink/[0.22] [[data-skin=plate]_&]:not-first:pt-6"
    >
      <div className="flex flex-wrap items-center gap-2.5">
        <h2
          id={sectionTitleId(id)}
          tabIndex={-1}
          className={cn(
            'riso-marker self-start text-[14.5px] leading-snug font-semibold tracking-tight outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:px-1.5 [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[24px] [[data-skin=plate]_&]:leading-[1.25] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal',
            danger && 'text-red-text',
          )}
        >
          {title}
          {count !== undefined && (
            <span
              aria-hidden="true"
              className="ml-2 font-mono text-[12px] font-medium text-ink-500 tabular-nums [[data-skin=plate]_&]:font-display-latin [[data-skin=plate]_&]:text-[16px] [[data-skin=plate]_&]:font-extrabold"
            >
              {count}
            </span>
          )}
        </h2>
        {meta && <span className="text-[12.5px] text-ink-500">{meta}</span>}
        {actions && <div className="ml-auto flex flex-wrap gap-1.5">{actions}</div>}
      </div>
      <div
        data-section-body
        className={cn(
          'flex min-w-0 flex-col gap-3',
          danger && 'riso-stripes-red rounded-panel border border-red-text/25 bg-error-field px-5 py-4 [[data-skin=plate]_&]:border-0',
        )}
      >
        {children}
      </div>
    </section>
  )
}
