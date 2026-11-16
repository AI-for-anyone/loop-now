import { useId, type ComponentProps, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

// Outlined number: Bricolage 800, riso-blue outline, no fill. Used for the 01, 02 of sections and for the number of each turn in the
// conversation. Purely decorative, hidden on narrow screens.
export function HollowNumber({ className, ...props }: ComponentProps<'span'>) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        'block w-[46px] shrink-0 font-display-latin text-[34px] leading-none font-extrabold tracking-[-0.02em] text-transparent [-webkit-text-stroke:1.6px_var(--riso-blue)] max-nav:hidden',
        className,
      )}
      {...props}
    />
  )
}

// Numbered section: long pages are split into sections 01, 02… (rule 4 of the redesign's general rules). The title line: an outlined
// number on the left, and a ZCOOL 30px title with a stroke of yellow highlighter mark
// (a yellow bar over the lower half), which can be followed by a note (meta) and right-aligned actions (actions); the content sits below
// and fills the width.
// From the second section on, there is a 2px dashed line above.
export function NumberedSection({
  number,
  title,
  meta,
  actions,
  className,
  children,
  ...props
}: { number: number; title: ReactNode; meta?: ReactNode; actions?: ReactNode } & Omit<ComponentProps<'section'>, 'title'>) {
  const titleId = useId()
  return (
    <section aria-labelledby={titleId} className={cn('flex min-w-0 flex-col gap-[18px]', number > 1 && 'border-t-2 border-dashed border-ink/[0.22] pt-6', className)} {...props}>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5">
        <HollowNumber data-section-number>{String(number).padStart(2, '0')}</HollowNumber>
        <h2 id={titleId} className="riso-marker px-1.5 font-display-cn text-[30px] leading-[1.25] font-normal">
          {title}
        </h2>
        {meta}
        {actions && <div className="flex flex-wrap items-center gap-2 sm:ml-auto">{actions}</div>}
      </div>
      {children}
    </section>
  )
}
