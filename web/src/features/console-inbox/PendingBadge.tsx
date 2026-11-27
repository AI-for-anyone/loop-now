import { pendingTotal, type PendingCounts } from '@/api/console-inbox'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

const text = t.consoleInbox

// The "needs your reply" number: the sum of open questions and unread messages sent to you, on a fluorescent pink circle (the
// style of the unread count in the old navigation); hidden when 0.
// Screen readers and the hover tooltip say how many are questions and how many are new messages. The sidebar uses sticker: an
// outlined, tilted sticker (the tilt is drawn only on the background layer).
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
          'inline-flex items-center justify-center rounded-full font-semibold tabular-nums',
          sticker
            ? 'riso-tilt h-5 min-w-5 px-1.5 text-[11px] leading-none text-cta-fg [--tilt-bg:var(--cta-bg)] [--tilt-shadow:none]'
            : 'h-[21px] min-w-[21px] bg-cta-bg px-1.5 text-[12px] text-cta-fg',
        )}
      >
        {total}
      </span>
    </span>
  )
}
