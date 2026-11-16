import type { ConnectionState } from './events'

// Two pieces of global inbox state: the operator's total unread count (the number in the navigation) and the connection state of the
// inbox event stream (“实时更新中” (Live)).
// The total unread count follows the value that arrived most recently: events on the event stream, the thread list and the "mark as
// read" response all bring it back.
function createStore<T>(initial: T) {
  let value = initial
  const listeners = new Set<() => void>()
  return {
    get: () => value,
    set(next: T) {
      if (Object.is(value, next)) return
      value = next
      listeners.forEach((listener) => listener())
    },
    subscribe(listener: () => void) {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    // Only for tests, to reset between cases.
    reset() {
      value = initial
    },
  }
}

// null until the event stream's snapshot arrives, so the navigation shows no number.
export const inboxUnread = createStore<number | null>(null)
export const inboxConnection = createStore<ConnectionState>('connecting')
