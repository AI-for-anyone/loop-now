import type { ComponentProps, ElementType, Ref } from 'react'
import { cn } from '@/lib/utils'

// Sheet: clean full-bleed canvas with hairline top separator in mono skin; raised paper sheet with 2px ink border, 18px radius and 6px
// offset shadow in Plate skin. Keeps data-sheet and riso-scrollbar across both skins.
export function Sheet({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      data-sheet
      className={cn(
        'riso-grain riso-scrollbar flex min-h-0 flex-col overflow-hidden border-t border-hairline bg-surface-canvas [[data-skin=plate]_&]:mr-1.5 [[data-skin=plate]_&]:rounded-panel [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-print-dialog',
        className,
      )}
      {...props}
    />
  )
}

// Stub: a strip on the right side of the sheet, set off by a hairline border (2px dashed in Plate skin), holding a ruler or summary
// figures. Not shown on narrow screens (when the sidebar is folded into the drawer). When it is a navigation region (as="nav") named
// with aria-label, the small heading is not read a second time.
export function Stub({
  as: Tag = 'div',
  label,
  className,
  children,
  ...props
}: { as?: ElementType; label: string; ref?: Ref<HTMLElement> } & Omit<ComponentProps<'div'>, 'ref'>) {
  return (
    <Tag
      className={cn(
        'flex shrink-0 flex-col gap-2.5 border-l border-hairline pt-4 pb-3.5 max-nav:hidden [[data-skin=plate]_&]:border-l-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/40',
        className,
      )}
      {...props}
    >
      <div
        aria-hidden={props['aria-label'] ? true : undefined}
        className="shrink-0 text-center indent-[0.2em] text-[11px] leading-[1.75] font-semibold tracking-[0.2em] text-ink-500 [[data-skin=plate]_&]:indent-[0.3em] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:tracking-[0.3em]"
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
        'pointer-events-none absolute top-0 right-0 h-full w-2.5 bg-[repeating-linear-gradient(to_bottom,var(--hairline-strong)_0,var(--hairline-strong)_1px,transparent_1px,transparent_23px)] [[data-skin=plate]_&]:w-3 [[data-skin=plate]_&]:bg-[repeating-linear-gradient(to_bottom,var(--ink)_0,var(--ink)_2px,transparent_2px,transparent_23px)]',
        className,
      )}
      {...props}
    />
  )
}

// The indicator marking the current position on the ruler: clean subtle highlight band and indicator bar in mono skin (no blend mode);
// tilted yellow highlighter mark with a pink arrow in Plate skin. Purely decorative.
export function RulerMark({ className, ...props }: ComponentProps<'span'>) {
  return (
    <span aria-hidden="true" className={cn('pointer-events-none absolute right-0 left-0 block', className)} {...props}>
      <span
        data-ruler-band
        className="absolute inset-y-0 right-0 left-2 rounded-stamp bg-surface-active [[data-skin=plate]_&]:-right-0.5 [[data-skin=plate]_&]:rounded-none [[data-skin=plate]_&]:-rotate-[1.5deg] [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:mix-blend-multiply"
      />
      <span
        data-ruler-arrow
        className="absolute top-1/2 left-0.5 h-3.5 w-0.5 -translate-y-1/2 rounded-full bg-ink [[data-skin=plate]_&]:size-0 [[data-skin=plate]_&]:rounded-none [[data-skin=plate]_&]:border-y-[6px] [[data-skin=plate]_&]:border-l-8 [[data-skin=plate]_&]:border-y-transparent [[data-skin=plate]_&]:border-l-riso-pink [[data-skin=plate]_&]:bg-transparent"
      />
    </span>
  )
}

// A summary figure in the stub (the file count on the folder page, the unread count in the inbox...), right-aligned.
const FIGURE_TONE = { blue: 'text-ink [[data-skin=plate]_&]:text-riso-blue', ink: 'text-ink', pink: 'text-pink-text', red: 'text-red-text', green: 'text-green-text' } as const

export function StubFigure({ value, label, tone = 'blue' }: { value: string; label: string; tone?: keyof typeof FIGURE_TONE }) {
  return (
    <div className="flex flex-col items-end gap-0.5">
      <span className={cn('font-display-latin text-[28px] leading-none font-semibold tabular-nums [[data-skin=plate]_&]:text-[34px] [[data-skin=plate]_&]:font-extrabold', FIGURE_TONE[tone])}>
        {value}
      </span>
      <span className="text-[12px] font-medium text-ink-500 [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink-700">{label}</span>
    </div>
  )
}
