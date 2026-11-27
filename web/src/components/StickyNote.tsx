import type { ComponentProps } from 'react'
import { InfoIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

// Callout note: subtle hairline banner; tone="red" uses a tinted red banner and red text for errors or blocked reasons.
export function StickyNote({ tone = 'yellow', className, children, ...props }: { tone?: 'yellow' | 'red' } & ComponentProps<'div'>) {
  return (
    <div
      role="note"
      className={cn(
        'flex items-center gap-2.5 self-start rounded-card border py-2 pr-4 pl-3 text-[13.5px] leading-[1.7] font-medium',
        tone === 'red'
          ? 'border-red-text/25 bg-tint-red text-red-text'
          : 'border-hairline bg-surface-subtle text-ink-700',
        className,
      )}
      {...props}
    >
      <InfoIcon aria-hidden="true" className="size-4 shrink-0" strokeWidth={2.2} />
      <div className="min-w-0">{children}</div>
    </div>
  )
}
