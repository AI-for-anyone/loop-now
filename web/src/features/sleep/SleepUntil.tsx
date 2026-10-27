import { useNow } from '@/lib/use-now'
import { t } from '@/i18n'

// View-only props. API objects remain generated from OpenAPI at the call sites.
// Reaching zero does not clear sleep, change workflow or issue a wake request.
export function SleepUntil({ at, reason, compact = false }: { at: string; reason?: string | null; compact?: boolean }) {
  const now = useNow(1000)
  const seconds = Math.max(0, Math.ceil((Date.parse(at) - now) / 1000))
  if (compact) return <section aria-label={t.sleep.title} className="grid min-w-0 gap-0.5 text-[11.5px] leading-[1.5] text-ink-700">
    <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
      <time dateTime={at} className="font-bold">{t.sleep.until(at)}</time>
      <span>{seconds > 0 ? t.sleep.remaining(seconds) : t.sleep.awaitingWake}</span>
    </div>
    {reason && <p className="min-w-0 truncate" title={reason}>{reason}</p>}
  </section>
  return <section aria-label={t.sleep.title} className="grid min-w-0 gap-1 text-[13px] leading-[1.6] text-ink-700 [overflow-wrap:anywhere]">
    <time dateTime={at} className="font-bold">{t.sleep.until(at)}</time>
    <span>{seconds > 0 ? t.sleep.remaining(seconds) : t.sleep.awaitingWake}</span>
    {reason && <p>{reason}</p>}
  </section>
}
