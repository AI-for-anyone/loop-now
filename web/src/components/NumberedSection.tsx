import { useId, type ComponentProps, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

// Section index: compact muted tabular mono number. Purely decorative, hidden on narrow screens.
export function HollowNumber({ className, ...props }: ComponentProps<'span'>) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        'block shrink-0 font-mono text-[12px] leading-none font-medium tracking-wide text-ink-500 tabular-nums max-nav:hidden',
        className,
      )}
      {...props}
    />
  )
}

// Numbered section: long pages are split into sections with a clean left-aligned heading and hairline top rule.
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
    <section
      aria-labelledby={titleId}
      className={cn(
        'flex min-w-0 flex-col gap-3.5',
        number > 1 && 'border-t border-hairline pt-5',
        className,
      )}
      {...props}
    >
      <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-2">
        <HollowNumber data-section-number className="hidden">
          {String(number).padStart(2, '0')}
        </HollowNumber>
        <h2
          id={titleId}
          className="text-[14.5px] leading-snug font-semibold tracking-tight text-ink"
        >
          {title}
        </h2>
        {meta}
        {actions && <div className="flex flex-wrap items-center gap-2 sm:ml-auto">{actions}</div>}
      </div>
      {children}
    </section>
  )
}
