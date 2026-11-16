import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// 任务与节点的状态（改版通用规则第 6 条）：阻塞是略微倾斜的红色描边印章；执行中与已提交是荧光粉胶囊，已完成浅绿胶囊，
// 等待回复浅黄胶囊，准备中、排队与已取消是浅蓝胶囊。任务列表、任务详情的页头、节点头和流程尺都用它。
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
