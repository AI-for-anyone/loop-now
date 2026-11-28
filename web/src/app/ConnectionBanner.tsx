import { useEffect, useRef, useSyncExternalStore } from 'react'
import { useQueryClient, type Query, type QueryClient } from '@tanstack/react-query'
import { RotateCwIcon, WifiOffIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { api, deviceAuth, reachability } from '@/api/client'
import { useContextInfo } from '@/api/queries'
import { absoluteApiBase } from '@/config'
import { t } from '@/i18n'
import { isUnreachable } from '@/lib/errors'

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

const failedToConnect = (query: Query) => isUnreachable(query.state.error)

// Refetch the queries that failed because the server could not be reached. A request in progress is cancelled first and sent again: it may
// have been sent while disconnected, and reusing its result would carry the failure over to the next poll; both retry and reconnect must
// send new requests to the recovered server. Cancelled requests do not affect reachability.
const refetchFailed = (client: QueryClient) => client.refetchQueries({ type: 'active', predicate: failedToConnect }, { cancelRefetch: true })

// On retry, also probe the health check once, so we can tell whether the connection is back even when only a mutation failed.
async function retry(client: QueryClient) {
  await Promise.allSettled([refetchFailed(client), api.GET('/health')])
}

// Show the global connection notice when any request does not reach the server; it disappears as soon as any later request gets a response.
export function ConnectionBanner() {
  // Read the default context at startup, which also serves as the first connection probe.
  const context = useContextInfo()
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  const client = useQueryClient()
  const wasReachable = useRef(reachable)

  // Once the connection is back, refetch the queries that failed while disconnected, without waiting for the window to regain focus.
  useEffect(() => {
    if (reachable && !wasReachable.current) void refetchFailed(client)
    wasReachable.current = reachable
  }, [reachable, client])

  if (reachable) return null
  const apiBase = absoluteApiBase()
  const origin = window.location.origin
  const remoteDoor = isRemoteDoorConnection(context.data?.door ?? deviceAuth.door(), apiBase, origin)
  // Full-width unreachable banner driven by semantic theme tokens.
  return (
    <div
      role="alert"
      className="riso-halftone-red flex shrink-0 flex-wrap items-center gap-x-4 gap-y-3 border-b border-red-text/25 bg-tint-red px-4 py-3.5 text-ink sm:px-6"
    >
      <span
        aria-hidden="true"
        className="flex size-10 flex-none items-center justify-center rounded-ticket border border-red-text/25 bg-surface-control text-red-text"
      >
        <WifiOffIcon className="size-5" strokeWidth={2.2} />
      </span>
      <div className="flex min-w-0 flex-[1_1_520px] flex-col gap-0.5">
        <h2 className="text-[16px] leading-snug font-semibold text-red-text">
          {t.connection.unreachableTitle}
        </h2>
        <p className="text-[13.5px] leading-relaxed break-words text-ink-700">
          {remoteDoor ? t.connection.unreachableBodyRemote(origin) : t.connection.unreachableBody(apiBase, origin)}
        </p>
      </div>
      <Button variant="ink" size="lg" className="flex-none" onClick={() => void retry(client)}>
        <RotateCwIcon />
        {t.common.retry}
      </Button>
    </div>
  )
}
