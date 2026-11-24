// Messages between a console tab and the shared worker that owns the live event streams (one worker per browser and origin, so
// every tab shares its connections; see hub.ts). Each tab numbers its own subscriptions (sub); the worker keys streams by their
// absolute URL, so tabs asking for the same stream share one connection.

export type ConnectionState = 'connecting' | 'open' | 'reconnecting' | 'closed'

export interface StreamRequest {
  // The stream's absolute URL (the tab resolves config.apiBase against its own location).
  url: string
  // The event names to listen for (the same for every subscriber of a URL).
  names: readonly string[]
  // Backoff after the stream was closed for good and a subscriber asked to retry.
  retryMinMs: number
  retryMaxMs: number
}

export type TabMessage =
  | ({ kind: 'subscribe'; sub: number } & StreamRequest)
  | { kind: 'unsubscribe'; sub: number }
  // The answer to 'closed': whether this subscriber wants the stream opened again (StreamOptions.onClosed).
  | { kind: 'retry'; sub: number; retry: boolean }
  // The answer to 'ping'; also sent when a frozen page resumes, so a worker that gave up on this tab says 'reset'.
  | { kind: 'pong' }
  // The tab is going away (pagehide): forget its subscriptions now rather than after missed pings.
  | { kind: 'bye' }

export type WorkerMessage =
  | { kind: 'state'; sub: number; state: ConnectionState }
  // data is the event's raw text, parsed by the tab as before.
  | { kind: 'event'; sub: number; type: string; id: string; data: string }
  // The stream was closed for good (e.g. the server returned an error status): the tab asks its onClosed and answers 'retry'.
  | { kind: 'closed'; sub: number }
  | { kind: 'ping' }
  // The worker had given this tab up (no answer to its pings): the tab subscribes again to everything it still shows.
  | { kind: 'reset' }
