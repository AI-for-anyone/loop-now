import type { ConnectionState } from './events'

// 收件箱的两项全局状态：操作者的未读总数（导航上的数字）与收件箱事件流的连接状态（“实时更新中”）。
// 未读总数以最新到达的值为准：事件流中的事件、线索列表与“标为已读”的响应都会带回它。
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
    // 只供测试在用例之间复位。
    reset() {
      value = initial
    },
  }
}

// 事件流的快照到达之前为 null，导航上不显示数字。
export const inboxUnread = createStore<number | null>(null)
export const inboxConnection = createStore<ConnectionState>('connecting')
