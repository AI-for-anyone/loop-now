import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// Task and node statuses (general rule 6 of the redesign): blocked is a slightly tilted red-outlined stamp; running and submitted are
// fluorescent-pink pills, completed a light-green pill,
// waiting for reply a light-yellow pill, and preparing, queued and cancelled light-blue pills. Used by the task list, the task detail
// header, the node header and the workflow ruler.
export function TaskBadge({ status, className }: { status: Schemas['TaskStatus'] | Schemas['TaskNodeStatus']; className?: string }) {
  const labels: Record<string, string> = { ...t.tasks.statuses, ...t.tasks.nodeStatuses }
  const label = labels[status]
  if (status === 'blocked') {
    return (
      <span
        data-task-state={status}
        className={cn(
          'riso-tilt inline-flex min-h-[22px] items-center rounded-[4px] px-2.5 text-[12.5px] font-black tracking-[0.06em] whitespace-nowrap text-red-text [--tilt-bg:transparent] [--tilt-shadow:inset_0_0_0_2px_var(--red-text)] [--tilt:-3deg]',
          className,
        )}
      >
        {label}
      </span>
    )
  }
  const tone =
    status === 'running' || status === 'submitted'
      ? 'bg-riso-pink text-ink'
      : status === 'completed'
        ? 'bg-tint-green text-green-text'
        : status === 'waiting'
          ? 'bg-tint-yellow text-ink'
          : 'bg-tint-blue text-ink-700'
  return (
    <span data-task-state={status} className={cn('inline-block rounded-full px-2.5 text-[12.5px] leading-[22px] font-black whitespace-nowrap', tone, className)}>
      {label}
    </span>
  )
}
