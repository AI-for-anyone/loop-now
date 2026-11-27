import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useConnectionStatus } from './connection-status'

// The connection status in the sidebar footer: unreachable when a request did not reach the server or the health endpoint is not
// available (an error other than 503); database unavailable when the health check reports the database is unavailable (503); checking
// while the first query has no result yet. In compact mode, healthy states stay visually quiet (screen-reader accessible, paired with a
// dot on the scope switcher) while degraded and unreachable states expand into a visible alert chip.
export function ConnectionStatus({ className, compact = false }: { className?: string; compact?: boolean }) {
  const { state, host, style } = useConnectionStatus()
  const quiet = compact && (state === 'connected' || state === 'checking')

  return (
    <div
      role="status"
      aria-label={t.settings.health}
      className={cn(
        quiet
          ? 'sr-only min-w-0'
          : 'inline-flex h-7 max-w-full min-w-0 items-center gap-2 rounded-stamp text-[12px] font-medium whitespace-nowrap',
        !quiet && style.chip,
        !quiet && className,
      )}
    >
      <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', style.dot)} />
      <span className="shrink-0 whitespace-nowrap">{style.label}</span>
      <span title={host} className="min-w-0 truncate font-mono text-[11px] font-normal opacity-80">
        {host}
      </span>
    </div>
  )
}
