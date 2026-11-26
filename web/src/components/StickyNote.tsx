import type { ComponentProps } from 'react'
import { InfoIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

// Callout note: subtle hairline banner in mono skin; slightly tilted yellow sticky note in Plate skin.
// tone="red": tinted red banner and red text for errors or blocked reasons.
export function StickyNote({ tone = 'yellow', className, children, ...props }: { tone?: 'yellow' | 'red' } & ComponentProps<'div'>) {
  return (
    <div
      role="note"
      className={cn(
        'riso-tilt flex items-center gap-2.5 self-start rounded-card border py-2 pr-4 pl-3 text-[13.5px] leading-[1.7] font-medium before:hidden [[data-skin=plate]_&]:rounded-[3px] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:leading-[1.75] [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:[--tilt:-0.5deg] [[data-skin=plate]_&]:[--tilt-shadow:3px_3px_0_rgb(24_38_94/.18)] [[data-skin=plate]_&]:before:block',
        tone === 'red'
          ? 'border-red-text/25 bg-tint-red text-red-text [[data-skin=plate]_&]:[--tilt-bg:var(--tint-red)]'
          : 'border-hairline bg-surface-subtle text-ink-700 [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:[--tilt-bg:var(--riso-yellow)]',
        className,
      )}
      {...props}
    >
      <InfoIcon aria-hidden="true" className="size-4 shrink-0" strokeWidth={2.2} />
      <div className="min-w-0">{children}</div>
    </div>
  )
}
