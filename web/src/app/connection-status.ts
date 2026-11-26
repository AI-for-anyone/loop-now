import { useSyncExternalStore } from 'react'
import { reachability } from '@/api/client'
import { useHealth } from '@/api/queries'
import { absoluteApiBase } from '@/config'
import { t } from '@/i18n'

export type ConnectionState = 'checking' | 'connected' | 'degraded' | 'unreachable'

// Every state has text; color is only an aid.
const stateStyle: Record<ConnectionState, { label: string; chip: string; dot: string }> = {
  checking: {
    label: t.app.topbar.checking,
    chip: 'text-sidebar-muted [[data-skin=plate]_&]:text-white',
    dot: 'bg-sidebar-muted/60 [[data-skin=plate]_&]:bg-white/60',
  },
  connected: {
    label: t.app.topbar.connected,
    chip: 'text-sidebar-muted [[data-skin=plate]_&]:text-white',
    dot: 'bg-green [[data-skin=plate]_&]:bg-tint-green',
  },
  degraded: {
    label: t.settings.healthDegraded,
    chip: 'border border-pink-text/30 bg-tint-yellow px-2.5 text-ink [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:px-3 [[data-skin=plate]_&]:shadow-[inset_0_3px_0_var(--ink)]',
    dot: 'bg-pink-text [[data-skin=plate]_&]:bg-ink',
  },
  unreachable: {
    label: t.app.topbar.unreachable,
    chip: 'border border-red-text/25 bg-tint-red px-2.5 text-red-text [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:px-3',
    dot: 'bg-red',
  },
}

// Show only the host and port of the API URL; show the URL as is when it cannot be parsed.
function apiHost(apiBase: string): string {
  try {
    return new URL(apiBase).host
  } catch {
    return apiBase
  }
}

export function useConnectionStatus() {
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  const health = useHealth()
  const state: ConnectionState =
    !reachable || health.isError ? 'unreachable' : health.isPending ? 'checking' : health.data.ok ? 'connected' : 'degraded'
  const host = apiHost(absoluteApiBase())
  return { state, host, style: stateStyle[state] }
}
