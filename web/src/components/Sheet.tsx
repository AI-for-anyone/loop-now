import type { ComponentProps, ElementType, Ref } from 'react'
import { cn } from '@/lib/utils'

// Sheet: the page body sits flush under the header bar, meeting the sidebar and window edges directly (no outer margin, no thick
// border, no drop shadow). A 1.5px navy line separates it from the header above; light paper background with the very faint paper
// grain and thin translucent navy scrollbars (riso-scrollbar) stay.
export function Sheet({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      data-sheet
      className={cn('riso-grain riso-scrollbar flex min-h-0 flex-col overflow-hidden border-t-[1.5px] border-ink bg-paper-raised', className)}
      {...props}
    />
  )
}

// Stub: a strip on the right side of the sheet, set off by a 2px dashed line, holding a ruler (turns on the conversation page, the
// contents on the config page, a few big numbers for the folder and the inbox).
// A small heading at the top. Not shown on narrow screens (when the sidebar is folded into the drawer). When it is a navigation region
// (as="nav") named with aria-label, the small heading is not read a second time.
export function Stub({
  as: Tag = 'div',
  label,
  labelClassName,
  className,
  children,
  ...props
}: { as?: ElementType; label: string; labelClassName?: string; ref?: Ref<HTMLElement> } & Omit<ComponentProps<'div'>, 'ref'>) {
  return (
    <Tag className={cn('flex shrink-0 flex-col gap-2.5 border-l-2 border-dashed border-ink/40 pt-4 pb-3.5 max-nav:hidden', className)} {...props}>
      <div aria-hidden={props['aria-label'] ? true : undefined} className={cn('shrink-0 text-center indent-[0.3em] text-[11px] leading-[1.75] font-black tracking-[0.3em] text-ink-500', labelClassName)}>
        {label}
      </div>
      {children}
    </Tag>
  )
}

// The row of ticks on the ruler's right edge (a 2px short line every 23px), purely decorative; placed in a positioned container, it fills
// that container's height.
export function RulerTicks({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      aria-hidden="true"
      className={cn('pointer-events-none absolute top-0 right-0 h-full w-3 bg-[repeating-linear-gradient(to_bottom,var(--ink)_0,var(--ink)_2px,transparent_2px,transparent_23px)]', className)}
      {...props}
    />
  )
}

// The stroke marking the current position on the ruler: a slightly tilted yellow highlighter mark with a small pink arrow on its left.
// Purely decorative; the caller provides position and height (style or class names).
export function RulerMark({ className, ...props }: ComponentProps<'span'>) {
  return (
    <span aria-hidden="true" className={cn('pointer-events-none absolute right-0 left-0 block', className)} {...props}>
      <span data-ruler-band className="absolute inset-y-0 -right-0.5 left-2 -rotate-[1.5deg] bg-riso-yellow mix-blend-multiply" />
      <span data-ruler-arrow className="absolute top-1/2 left-0.5 size-0 -translate-y-1/2 border-y-[6px] border-l-8 border-y-transparent border-l-riso-pink" />
    </span>
  )
}

// A big number in the stub (the file count on the folder page, the unread count in the inbox…): a Bricolage 34px number on top, a line of
// small caption text below, right-aligned.
const FIGURE_TONE = { blue: 'text-riso-blue', ink: 'text-ink', pink: 'text-pink-text', red: 'text-red-text', green: 'text-green-text' } as const

export function StubFigure({ value, label, tone = 'blue' }: { value: string; label: string; tone?: keyof typeof FIGURE_TONE }) {
  return (
    <div className="flex flex-col items-end gap-0.5">
      <span className={cn('font-display-latin text-[34px] leading-none font-extrabold', FIGURE_TONE[tone])}>{value}</span>
      <span className="text-[12.5px] font-black text-ink-700">{label}</span>
    </div>
  )
}
