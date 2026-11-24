import { config } from '@/config'
import type { StreamOptions } from './events'

// A stream this tab owns itself (where no shared worker can hold it, see shared-events/client.ts). The browser's automatic reconnect
// sends Last-Event-ID; after a permanent close the connection is rebuilt after a backoff interval, and
// on the new connection the server pushes a snapshot first.
export function connectLocalEventStream<E extends { type: string; id: string; data: unknown }>(
  url: string,
  names: readonly E['type'][],
  options: StreamOptions<E>,
): () => void {
  const create = options.createEventSource ?? ((target: string) => new EventSource(target))
  const retryMin = options.retryMinMs ?? config.eventRetryMinMs
  const retryMax = options.retryMaxMs ?? config.eventRetryMaxMs
  let source: EventSource | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let attempts = 0
  let stopped = false

  const open = () => {
    if (stopped) return
    options.onState(attempts === 0 ? 'connecting' : 'reconnecting')
    const es = create(url)
    source = es
    es.onopen = () => {
      attempts = 0
      options.onState('open')
    }
    es.onerror = () => {
      if (stopped || source !== es) return
      if (es.readyState !== EventSource.CLOSED) {
        options.onState('reconnecting')
        return
      }
      es.close()
      source = null
      options.onState('reconnecting')
      void options.onClosed().then((retry) => {
        if (stopped) return
        if (!retry) {
          options.onState('closed')
          return
        }
        const delay = Math.min(retryMax, retryMin * 2 ** attempts)
        attempts += 1
        timer = setTimeout(open, delay)
      })
    }
    for (const name of names) {
      es.addEventListener(name, (message) => {
        if (stopped || source !== es) return
        let data: unknown
        try {
          data = JSON.parse((message as MessageEvent<string>).data)
        } catch {
          return
        }
        options.onEvent({ type: name, id: (message as MessageEvent).lastEventId, data } as E)
      })
    }
  }

  open()
  return () => {
    stopped = true
    if (timer !== null) clearTimeout(timer)
    source?.close()
    source = null
  }
}
