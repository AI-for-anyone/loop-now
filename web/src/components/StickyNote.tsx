import type { ComponentProps } from 'react'
import { InfoIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

// 黄色便签：页面里的提示条。略微倾斜（只画在背景层上）、淡投影，前面一个信息图标。
export function StickyNote({ className, children, ...props }: ComponentProps<'div'>) {
  return (
    <div
      role="note"
      className={cn(
        'riso-tilt flex items-center gap-2.5 self-start rounded-[3px] py-2 pr-4 pl-3 text-[13.5px] leading-[1.75] font-bold [--tilt-bg:var(--riso-yellow)] [--tilt-shadow:3px_3px_0_rgb(24_38_94/.18)] [--tilt:-0.5deg]',
        className,
      )}
      {...props}
    >
      <InfoIcon aria-hidden="true" className="size-4 shrink-0" strokeWidth={2.4} />
      <div className="min-w-0">{children}</div>
    </div>
  )
}
