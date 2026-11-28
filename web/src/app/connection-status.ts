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
    chip: 'text-sidebar-muted',
    dot: 'bg-sidebar-muted/60',
  },
  connected: {
    label: t.app.topbar.connected,
    chip: 'text-sidebar-muted',
    dot: 'bg-green',
  },
  degraded: {
    label: t.settings.healthDegraded,
    chip: 'border border-pink-text/30 bg-tint-yellow px-2.5 text-ink',
    dot: 'bg-pink-text',
  },
  unreachable: {
    label: t.app.topbar.unreachable,
    chip: 'border border-red-text/25 bg-tint-red px-2.5 text-red-text',
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

function isLoopbackHost(hostname: string): boolean {
  return hostname === '127.0.0.1' || hostname === 'localhost' || hostname === '::1' || hostname === '[::1]'
}

export function isRemoteDoorConnection(door: string | null | undefined, apiBase: string, origin: string): boolean {
  if (door === 'remote') return true
  if (door === 'local') return false
  // Before /context has answered (e.g. a phone opening https://<id>.loop-now.app while the host is already offline),
  // a same-origin console on a non-loopback host is served by the remote door through a tunnel.
  try {
    const pageUrl = new URL(origin)
    const apiUrl = new URL(apiBase)
    return pageUrl.origin === apiUrl.origin && !isLoopbackHost(pageUrl.hostname)
  } catch {
    return false
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
