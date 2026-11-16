import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// The running count next to the “任务” (Tasks) item in the sidebar: the number of tasks in the project whose status is running
// (preparing, waiting and blocked don't count); not shown when it hasn't been read or is 0.
// A small pill with a white background, navy outline and navy text, readable whether the item is selected (yellow background) or not
// (blue background), and distinct from the pink “待你回复” (Needs your reply) sticker.
// The number is hidden from screen readers, which read “N 个任务正在运行” (N tasks running) instead; hovering shows the same sentence.
export function RunningTaskBadge({ count, className }: { count: number | undefined; className?: string }) {
  if (!count) return null
  const label = t.shell.runningTasks(count)
  return (
    <span title={label} className={cn('inline-flex shrink-0', className)}>
      <span className="sr-only">{label}</span>
      <span
        aria-hidden="true"
        className="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-white px-1.5 text-[11px] leading-none font-black text-ink tabular-nums shadow-[inset_0_0_0_1.5px_var(--ink)]"
      >
        {count}
      </span>
    </span>
  )
}
