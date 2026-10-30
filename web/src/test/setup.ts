import '@testing-library/jest-dom/vitest'
import { toastStore } from '@/lib/toast'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll, beforeEach } from 'vitest'
import { reachability, serverVersionStore } from '@/api/client'
import { inboxConnection, inboxUnread } from '@/api/inbox-store'
import { FakeEventSource } from './fake-event-source'
import { FakeIntersectionObserver } from './intersection-observer'
import { server } from './server'

// Vitest 5.0.0 的 jsdom 兼容层按 `_buffer` 读取 jsdom Blob 的内容，而 jsdom 28 起改名为 `_bytes`，
// 导致经 fetch 发送的 Blob 请求体变成 "undefined"。Vitest 5.0.3 已修复；升级到该版本或更新后删除这段。
// 补上之后，multipart 请求中文件部分的内容与文件名在 jsdom 中仍不可靠，相关测试只检查字段名。
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
beforeEach(() => {
  FakeEventSource.reset()
  FakeIntersectionObserver.reset()
  globalThis.EventSource = FakeEventSource as unknown as typeof EventSource
})
afterEach(() => {
  cleanup()
  server.resetHandlers()
  reachability.reset()
  serverVersionStore.reset()
  inboxUnread.reset()
  inboxConnection.reset()
  toastStore.reset()
  // 最近使用的项目或平台记在浏览器存储中，不能带到下一个测试。
  window.localStorage.clear()
})
afterAll(() => server.close())

// jsdom 缺少的浏览器接口，Radix 组件会用到。
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
}
if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
// CodeMirror 在 jsdom 中测量选区时需要浏览器的 Range 几何接口；返回一个零尺寸矩形即可满足布局计算。
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
