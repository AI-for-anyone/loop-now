// Messages between a console tab and the shared worker that owns the live event streams (one worker per browser and origin, so
// every tab shares its connection; see hub.ts). Each tab numbers its own subscriptions (sub); the worker keys streams by the
// endpoint and the stream's name, so tabs asking for the same stream share it, and all streams share one GET /events connection.

export type ConnectionState = 'connecting' | 'open' | 'reconnecting' | 'closed'

export interface StreamRequest {
  // The absolute URL of GET /events (the tab resolves config.apiBase against its own location), and the stream's name there
  // (agent:<agentId>, overview, overview:platform, overview:project:<projectId>, inbox, task:<taskId>, project-tasks:<projectId>).
  endpoint: string
  stream: string
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

// Streams are the same stream when both the endpoint and the name are.
export const streamKey = (request: Pick<StreamRequest, 'endpoint' | 'stream'>) => `${request.endpoint} ${request.stream}`
