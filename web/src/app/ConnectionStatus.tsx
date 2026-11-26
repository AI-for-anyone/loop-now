import { useSyncExternalStore } from 'react'
import { reachability } from '@/api/client'
import { useHealth } from '@/api/queries'
import { absoluteApiBase } from '@/config'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

type ConnectionState = 'checking' | 'connected' | 'degraded' | 'unreachable'

// Every state has text; color is only an aid.
const stateStyle: Record<ConnectionState, { label: string; chip: string; dot: string }> = {
  checking: { label: t.app.topbar.checking, chip: 'text-white', dot: 'bg-white/60' },
  connected: { label: t.app.topbar.connected, chip: 'text-white', dot: 'bg-tint-green' },
  degraded: { label: t.settings.healthDegraded, chip: 'bg-tint-yellow px-3 text-ink shadow-[inset_0_3px_0_var(--ink)]', dot: 'bg-ink' },
  unreachable: { label: t.app.topbar.unreachable, chip: 'bg-tint-red px-3 text-red-text', dot: 'bg-red' },
}

// Show only the host and port of the API URL; show the URL as is when it cannot be parsed.
function apiHost(apiBase: string): string {
  try {
    return new URL(apiBase).host
  } catch {
    return apiBase
  }
}

// The connection status at the bottom of the sidebar: unreachable when a request did not reach the server or the health endpoint is not
// available (an error other than 503); database unavailable when the health check reports the database is unavailable (503); checking
// while the first query has no result yet.
export function ConnectionStatus({ className }: { className?: string }) {
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  const health = useHealth()
  const state: ConnectionState =
    !reachable || health.isError ? 'unreachable' : health.isPending ? 'checking' : health.data.ok ? 'connected' : 'degraded'
  const style = stateStyle[state]
  const host = apiHost(absoluteApiBase())

  return (
    <div
      role="status"
      aria-label={t.settings.health}
      className={cn('inline-flex h-7 max-w-full min-w-0 items-center gap-2 rounded-full text-[12.5px] font-black whitespace-nowrap', style.chip, className)}
    >
      <span aria-hidden="true" className={cn('size-[9px] shrink-0 rounded-full', style.dot)} />
      <span className="shrink-0 whitespace-nowrap">{style.label}</span>
      <span title={host} className="min-w-0 truncate font-mono text-[11.5px] font-bold">
        {host}
      </span>
    </div>
  )
}
