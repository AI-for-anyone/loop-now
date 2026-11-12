import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// 左侧栏“任务”一项旁的运行数：项目里状态为 running 的任务个数（准备中、等待、阻塞的不算）；没读到或为 0 时不显示。
// 白底、藏青描边与藏青字的小胶囊，选中（黄底）与未选中（蓝底）时都看得清，也与粉色的“待你回复”贴纸区分开。
// 数字对读屏隐藏，改读“N 个任务正在运行”；悬停时显示同一句。
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
