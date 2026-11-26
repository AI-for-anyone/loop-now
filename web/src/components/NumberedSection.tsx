import { useId, type ComponentProps, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

// Section index: compact muted tabular mono number in mono skin; Bricolage 800 riso-blue stroked outline in Plate skin.
// Purely decorative, hidden on narrow screens.
export function HollowNumber({ className, ...props }: ComponentProps<'span'>) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        'block shrink-0 font-mono text-[12px] leading-none font-medium tracking-wide text-ink-500 tabular-nums max-nav:hidden [[data-skin=plate]_&]:w-[46px] [[data-skin=plate]_&]:font-display-latin [[data-skin=plate]_&]:text-[34px] [[data-skin=plate]_&]:font-extrabold [[data-skin=plate]_&]:tracking-[-0.02em] [[data-skin=plate]_&]:text-transparent [[data-skin=plate]_&]:[-webkit-text-stroke:1.6px_var(--riso-blue)]',
        className,
      )}
      {...props}
    />
  )
}

// Numbered section: long pages are split into sections 01, 02... Clean typographic heading with hairline top rule in mono skin;
// outlined hollow number, ZCOOL display title with yellow highlighter mark and 2px dashed divider in Plate skin.
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
        'flex min-w-0 flex-col gap-4 [[data-skin=plate]_&]:gap-[18px]',
        number > 1 && 'border-t border-hairline pt-6 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/[0.22]',
        className,
      )}
      {...props}
    >
      <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-2 [[data-skin=plate]_&]:items-center [[data-skin=plate]_&]:gap-x-4 [[data-skin=plate]_&]:gap-y-2.5">
        <HollowNumber data-section-number>{String(number).padStart(2, '0')}</HollowNumber>
        <h2
          id={titleId}
          className="riso-marker text-[16px] leading-snug font-semibold tracking-tight text-ink [[data-skin=plate]_&]:px-1.5 [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[30px] [[data-skin=plate]_&]:leading-[1.25] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal"
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
