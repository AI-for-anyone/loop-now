import { useEffect, useReducer, useState } from 'react'
import { connectOverviewEvents } from '@/api/events'
import type { ScopeQuery } from '@/lib/scope'
import { initialOverviewState, overviewReducer } from './overview-state'

// Subscribe to the overview event stream. Overview events are only pushed when the run status changes, which is infrequent,
// so the UI is updated event by event.
// After a permanent disconnect it keeps reconnecting at backoff intervals; on a new connection the server pushes a snapshot first.
// The scope is fixed at mount: the overview page keys this block by scope, so changing the scope remounts and reconnects.
export function useOverviewLive(scope: ScopeQuery = {}) {
  const [state, dispatch] = useReducer(overviewReducer, initialOverviewState)
  const [initialScope] = useState(scope)
  useEffect(() => {
    return connectOverviewEvents({
      scope: initialScope,
      onEvent: (event) => dispatch({ type: 'event', event, at: new Date().toISOString() }),
      onState: (connection) => dispatch({ type: 'connection', state: connection }),
      onClosed: async () => true,
    })
  }, [initialScope])
  return state
}
