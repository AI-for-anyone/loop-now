import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// Task and node statuses: clean semantic pills in mono skin; in Plate skin, blocked is a tilted red-outlined stamp, running/submitted are
// fluorescent-pink pills, completed a light-green pill, waiting a light-yellow pill, and preparing/queued/cancelled light-blue pills.
export function TaskBadge({ status, className }: { status: Schemas['TaskStatus'] | Schemas['TaskNodeStatus']; className?: string }) {
  const labels: Record<string, string> = { ...t.tasks.statuses, ...t.tasks.nodeStatuses }
  const label = labels[status]
  if (status === 'blocked') {
    return (
      <span
        data-task-state={status}
        className={cn(
          'riso-tilt inline-flex min-h-[22px] items-center rounded-[4px] px-2.5 text-[12px] font-semibold whitespace-nowrap text-red-text [--tilt-bg:var(--tint-red)] [--tilt-shadow:inset_0_0_0_1px_var(--red-text)] [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:tracking-[0.06em] [[data-skin=plate]_&]:[--tilt-bg:transparent] [[data-skin=plate]_&]:[--tilt-shadow:inset_0_0_0_2px_var(--red-text)] [[data-skin=plate]_&]:[--tilt:-3deg]',
          className,
        )}
      >
        {label}
      </span>
    )
  }
  const tone =
    status === 'running' || status === 'submitted'
      ? 'bg-cta-bg text-cta-fg [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:text-ink'
      : status === 'completed'
        ? 'bg-tint-green text-green-text'
        : status === 'waiting'
          ? 'bg-tint-yellow text-ink'
          : 'border border-hairline bg-surface-subtle text-ink-700 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue'
  return (
    <span data-task-state={status} className={cn('inline-block rounded-full px-2.5 text-[12px] leading-[22px] font-semibold whitespace-nowrap [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:font-black', tone, className)}>
      {label}
    </span>
  )
}
