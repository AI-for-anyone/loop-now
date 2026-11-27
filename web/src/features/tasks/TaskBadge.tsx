import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// Task and node statuses: clean semantic pills driven by theme tokens.
export function TaskBadge({ status, className }: { status: Schemas['TaskStatus'] | Schemas['TaskNodeStatus']; className?: string }) {
  const labels: Record<string, string> = { ...t.tasks.statuses, ...t.tasks.nodeStatuses }
  const label = labels[status]
  if (status === 'blocked') {
    return (
      <span
        data-task-state={status}
        className={cn(
          'inline-flex min-h-[22px] items-center rounded-stamp border border-red-text/30 bg-tint-red px-2.5 text-[12px] font-semibold whitespace-nowrap text-red-text',
          className,
        )}
      >
        {label}
      </span>
    )
  }
  const tone =
    status === 'running' || status === 'submitted'
      ? 'bg-cta-bg text-cta-fg'
      : status === 'completed'
        ? 'bg-tint-green text-green-text'
        : status === 'waiting'
          ? 'bg-tint-yellow text-ink'
          : 'border border-hairline bg-surface-subtle text-ink-700'
  return (
    <span data-task-state={status} className={cn('inline-block rounded-full px-2.5 text-[12px] leading-[22px] font-semibold whitespace-nowrap', tone, className)}>
      {label}
    </span>
  )
}
