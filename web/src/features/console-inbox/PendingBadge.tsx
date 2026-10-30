import { pendingTotal, type PendingCounts } from '@/api/console-inbox'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

const text = t.consoleInbox

// 待你回复的数字：发给你的 open 问题与未读消息之和，荧光粉圆底（原导航上未读数的样式）；为 0 时不显示。
// 读屏与悬停提示说明其中几个是问题、几条是新消息。左侧栏用 sticker：描边、倾斜的贴纸（倾斜只画在底色层上）。
export function PendingBadge({ counts, sticker = false, className }: { counts: PendingCounts | undefined; sticker?: boolean; className?: string }) {
  const total = pendingTotal(counts)
  if (!counts || total === 0) return null
  const label = text.badgeLabel(text.badge(counts.questions, counts.unread))
  return (
    <span title={label} className={cn('inline-flex shrink-0', className)}>
      <span className="sr-only">{label}</span>
      <span
        aria-hidden="true"
        className={cn(
          'inline-flex items-center justify-center rounded-full font-black text-ink',
          sticker
            ? 'riso-tilt h-5 min-w-5 px-1.5 text-[11px] leading-none [--tilt-bg:var(--riso-pink)] [--tilt-shadow:inset_0_0_0_1.5px_var(--ink)] [--tilt:-6deg]'
            : 'h-[21px] min-w-[21px] bg-riso-pink px-1.5 text-[12px]',
        )}
      >
        {total}
      </span>
    </span>
  )
}
