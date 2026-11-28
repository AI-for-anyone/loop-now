import '@testing-library/jest-dom/vitest'
import { toastStore } from '@/lib/toast'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll, beforeEach } from 'vitest'
import { deviceAuth, reachability, serverVersionStore } from '@/api/client'
import { inboxConnection, inboxUnread } from '@/api/inbox-store'
import { useOwnEventStreams } from '@/api/events'
import { resetThemeForTest } from '@/theme/store'
import { FakeEventSource } from './fake-event-source'
import { FakeIntersectionObserver } from './intersection-observer'
import { server } from './server'

// Vitest 5.0.0's jsdom compatibility layer reads the content of a jsdom Blob through `_buffer`, but jsdom renamed it
// to `_bytes` in version 28, so Blob request bodies sent through fetch become "undefined". Fixed in Vitest 5.0.3;
// remove this block after upgrading to that version or later. Even with this patch, the content and file name of
// file parts in multipart requests are still unreliable in jsdom, so the related tests only check field names.
{
  const blob = new Blob()
  const implSymbol = Object.getOwnPropertySymbols(Object.getOwnPropertyDescriptors(blob))[0]
  const implProto = implSymbol ? Object.getPrototypeOf((blob as unknown as Record<symbol, object>)[implSymbol]) : null
  if (implProto && !('_buffer' in implProto)) {
    Object.defineProperty(implProto, '_buffer', {
      get(this: { _bytes: Uint8Array }) {
        return this._bytes
      },
    })
  }
}

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
// The views' tests drive each stream at its own endpoint (FakeEventSource); one GET /events for all is tested in api/shared-events.
beforeAll(() => useOwnEventStreams())
beforeEach(() => {
  FakeEventSource.reset()
  FakeIntersectionObserver.reset()
  globalThis.EventSource = FakeEventSource as unknown as typeof EventSource
})
afterEach(() => {
  cleanup()
  server.resetHandlers()
  reachability.reset()
  deviceAuth.reset()
  serverVersionStore.reset()
  inboxUnread.reset()
  inboxConnection.reset()
  toastStore.reset()
  // The recently used project or platform is remembered in browser storage and must not carry over to the next test.
  window.localStorage.clear()
  window.sessionStorage.clear()
  resetThemeForTest()
})
afterAll(() => server.close())

// Browser APIs missing from jsdom that Radix components use.
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
}
if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
// CodeMirror needs the browser's Range geometry APIs to measure selections in
// jsdom; returning a zero-size rectangle is enough for its layout calculations.
if (!Range.prototype.getClientRects) {
  Range.prototype.getClientRects = () => [new DOMRect()] as unknown as DOMRectList
  Range.prototype.getBoundingClientRect = () => new DOMRect()
}
if (!globalThis.ResizeObserver) {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}
if (!globalThis.IntersectionObserver) {
  globalThis.IntersectionObserver = FakeIntersectionObserver as unknown as typeof IntersectionObserver
}
if (!window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }) as MediaQueryList
}
