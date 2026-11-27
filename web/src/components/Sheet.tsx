import { useCallback, useRef, type ComponentProps, type ElementType, type Ref } from 'react'
import { PageRuler, ScrollRuler, type SectionRulerAnchor } from '@/components/PrecisionRuler'
import { cn } from '@/lib/utils'

// Sheet: clean full-bleed canvas sitting directly beneath PageHeader / SubNav, keeping data-sheet and riso-scrollbar.
export function Sheet({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      data-sheet
      className={cn(
        'relative riso-grain riso-scrollbar flex min-h-0 flex-1 flex-col overflow-hidden bg-surface-canvas',
        className,
      )}
      {...props}
    />
  )
}

export type PageBodyWidth = 'narrow' | 'default' | 'wide' | 'full'

export type PageBodyRuler =
  | 'scroll'
  | 'none'
  | {
      label: string
      sections: SectionRulerAnchor[]
    }

const PAGE_BODY_INNER: Record<PageBodyWidth, string> = {
  narrow: 'mx-auto w-full max-w-[780px] px-4 py-5 sm:px-6',
  default: 'mx-auto w-full max-w-[1080px] px-4 py-5 sm:px-6',
  wide: 'mx-auto w-full max-w-[1320px] px-4 py-5 sm:px-6',
  full: 'flex h-full min-h-0 w-full flex-col',
}

// Scrollable page body with consistent centered content width presets, or a zero-padding full-bleed column for split views.
// Automatically mounts the global vernier ScrollRuler (or semantic PageRuler when sections are provided) as a zero-width-tax
// floating rail anchored to the surrounding Sheet.
export function PageBody({
  width = 'default',
  ruler,
  ref,
  className,
  innerClassName,
  children,
  ...props
}: {
  width?: PageBodyWidth
  ruler?: PageBodyRuler
  innerClassName?: string
} & ComponentProps<'div'>) {
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const setRef = useCallback(
    (node: HTMLDivElement | null) => {
      scrollRef.current = node
      if (typeof ref === 'function') ref(node)
      else if (ref) ref.current = node
    },
    [ref],
  )
  const effectiveRuler: PageBodyRuler = ruler ?? (width === 'full' ? 'none' : 'scroll')

  return (
    <>
      <div
        ref={setRef}
        data-page-body
        className={cn(
          'relative min-h-0 flex-1 overflow-y-auto',
          width === 'full' && 'flex flex-col',
          effectiveRuler !== 'none' && 'nav:no-scrollbar',
          className,
        )}
        {...props}
      >
        <div className={cn(PAGE_BODY_INNER[width], innerClassName)}>{children}</div>
      </div>
      {effectiveRuler === 'scroll' && <ScrollRuler targetRef={scrollRef} />}
      {typeof effectiveRuler === 'object' && (
        <PageRuler sections={effectiveRuler.sections} label={effectiveRuler.label} rootRef={scrollRef} />
      )}
    </>
  )
}

// Stub: a strip on the right side of the sheet, set off by a hairline border, holding a ruler or summary
// figures. Not shown on narrow screens (when the sidebar is folded into the drawer). When it is a navigation region (as="nav") named
// with aria-label, the small heading is not read a second time.
export function Stub({
  as: Tag = 'div',
  label,
  labelClassName,
  className,
  children,
  ...props
}: { as?: ElementType; label: string; labelClassName?: string; ref?: Ref<HTMLElement> } & Omit<ComponentProps<'div'>, 'ref'>) {
  return (
    <Tag
      className={cn('flex shrink-0 flex-col gap-2.5 border-l border-hairline pt-4 pb-3.5 max-nav:hidden', className)}
      {...props}
    >
      <div
        aria-hidden={props['aria-label'] ? true : undefined}
        className={cn('shrink-0 text-center indent-[0.2em] text-[11px] leading-[1.75] font-semibold tracking-[0.2em] text-ink-500', labelClassName)}
      >
        {label}
      </div>
      {children}
    </Tag>
  )
}

// The row of ticks on the ruler's right edge, purely decorative; placed in a positioned container, it fills that container's height.
export function RulerTicks({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      aria-hidden="true"
      className={cn(
        'pointer-events-none absolute top-0 right-0 h-full w-2.5 bg-[repeating-linear-gradient(to_bottom,var(--hairline-strong)_0,var(--hairline-strong)_1px,transparent_1px,transparent_23px)]',
        className,
      )}
      {...props}
    />
  )
}

// The indicator marking the current position on the ruler: clean subtle highlight band and indicator bar. Purely decorative.
export function RulerMark({ className, ...props }: ComponentProps<'span'>) {
  return (
    <span aria-hidden="true" className={cn('pointer-events-none absolute right-0 left-0 block', className)} {...props}>
      <span data-ruler-band className="absolute inset-y-0 right-0 left-2 rounded-stamp bg-surface-active" />
      <span data-ruler-arrow className="absolute top-1/2 left-0.5 h-3.5 w-0.5 -translate-y-1/2 rounded-full bg-ink" />
    </span>
  )
}

// A summary figure in the stub (the file count on the folder page, the unread count in the inbox...), right-aligned.
const FIGURE_TONE = { blue: 'text-ink', ink: 'text-ink', pink: 'text-pink-text', red: 'text-red-text', green: 'text-green-text' } as const

export function StubFigure({ value, label, tone = 'blue' }: { value: string; label: string; tone?: keyof typeof FIGURE_TONE }) {
  return (
    <div className="flex flex-col items-end gap-0.5">
      <span className={cn('font-display-latin text-[28px] leading-none font-semibold tabular-nums', FIGURE_TONE[tone])}>
        {value}
      </span>
      <span className="text-[12px] font-medium text-ink-500">{label}</span>
    </div>
  )
}
