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
          ? 'sr-only'
          : 'inline-flex min-h-7 items-center gap-2 rounded-stamp text-[12px] font-medium [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:font-black',
        !quiet && style.chip,
        !quiet && className,
      )}
    >
      <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full [[data-skin=plate]_&]:size-[9px]', style.dot)} />
      <span>{style.label}</span>
      <span className="font-mono text-[11px] font-normal opacity-80 [[data-skin=plate]_&]:text-[11.5px] [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:opacity-100">
        {host}
      </span>
    </div>
  )
}
