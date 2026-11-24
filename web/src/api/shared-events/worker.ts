import { EventHub } from './hub'
import { EventSourceTransport } from './transport'

// The shared worker that owns the console's live event streams: one per browser and origin, so every console tab shares its
// connections (see hub.ts). Started by client.ts; the browser ends it when the last tab using it closes.

interface SharedWorkerScope {
  onconnect: ((event: MessageEvent) => void) | null
}

// How often the worker pings each tab; a tab that leaves DEFAULT_LIMITS.missedPings pings unanswered is forgotten.
export const PING_INTERVAL_MS = 15_000

const hub = new EventHub(new EventSourceTransport())
setInterval(() => hub.tick(), PING_INTERVAL_MS)
;(globalThis as unknown as SharedWorkerScope).onconnect = (event) => {
  const port = event.ports[0]
  if (port) hub.attach(port as unknown as Parameters<EventHub['attach']>[0])
}
