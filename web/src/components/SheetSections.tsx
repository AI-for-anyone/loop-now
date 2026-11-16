import { useEffect, useState, type ReactNode } from 'react'
import { useLocation } from 'react-router'
import { HollowNumber } from '@/components/NumberedSection'
import { RulerMark, RulerTicks, Stub } from '@/components/Sheet'
import { cn } from '@/lib/utils'

// The numbered sections of settings-type pages (Agent config, project settings) and the section ruler in the stub: one set, which find each
// other by id.
export interface SectionTocItem {
  // The id of the section element (the anchor of its ruler entry) and the id of the section title (receives focus after a jump).
  id: string
  titleId: string
  title: string
  // A delete-type section: its number, title and ruler entry are red.
  danger?: boolean
}

interface SheetSectionProps {
  id: string
  titleId: string
  // Which section this is (from 0); the number is shown as 01, 02…, matching the number on the section ruler.
  index: number
  title: string
  // The item count next to the title, only a visual hint; the section's name is still just the title.
  count?: number
  actions?: ReactNode
  // A one-line note after the title (small text in light ink).
  meta?: ReactNode
  // danger: a delete-type section, with a red number and title and the content on a red diagonal-stripe background.
  tone?: 'default' | 'danger'
  children: ReactNode
}

// Numbered section (rule 4 of the redesign's general rules): an outlined number on the left, and a ZCOOL 30px title with a stroke of yellow
// highlighter mark; sections sit directly on the sheet, separated by a 2px dashed line (none above the first one). The title can take
// focus, so focus can move to it after a jump from the ruler.
export function SheetSection({ id, titleId, index, title, count, actions, meta, tone = 'default', children }: SheetSectionProps) {
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
          {meta && <span className="text-[12.5px] text-ink-500">{meta}</span>}
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

// A section counts as current when it enters the upper part of the viewport (the top 45%); when several are visible at once, the first
// one wins.
// When the browser does not support IntersectionObserver nothing is highlighted, and everything else still works.
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

// Section contents: a section ruler in the stub on the right side of the sheet (rule 5 of the redesign's general rules). A row of ticks on
// the right edge; each entry is "name, number, short tick",
// and the current section's entry is marked with a slightly tilted yellow highlighter mark plus a small pink arrow; a delete-type
// section's entry is red. Not shown on narrow screens (when the sidebar is folded into the drawer).
// Clicking an entry scrolls to its section and moves focus to the section title; no smooth scrolling, as the console keeps only three
// animations.
// In the DOM the number comes before the name (screen readers read “01 基本信息” (01 Basics)), and order moves it after the name on screen.
// When a page is opened with a section in its URL (e.g. “去看它的提示词” (View its prompt) on the stats page), it scrolls to that section
// and moves focus to its title.
// footer: a line at the bottom of the ruler (e.g. the current version and update status on the plugin details page).
export function SectionToc({ label, items, className, footer }: { label: string; items: SectionTocItem[]; className?: string; footer?: ReactNode }) {
  const [active, setActive] = useActiveSection(items.map((item) => item.id))
  const { hash } = useLocation()

  useEffect(() => {
    const item = items.find((entry) => hash === `#${entry.id}`)
    if (!item) return
    document.getElementById(item.id)?.scrollIntoView({ block: 'start' })
    document.getElementById(item.titleId)?.focus({ preventScroll: true })
  }, [hash]) // eslint-disable-line react-hooks/exhaustive-deps

  const jump = (item: SectionTocItem) => {
    // Highlight the clicked entry first: a section near the end of the page may never reach the upper part of the viewport after scrolling.
    // Without observer support nothing is highlighted, so that the highlight does not stay on the old section after scrolling.
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
      {footer && <div className="mt-auto flex shrink-0 flex-col items-end gap-0.5 pr-[22px] pt-4">{footer}</div>}
    </Stub>
  )
}
