// Toast: a short message in the top right that disappears on its own after a few seconds (e.g. the result returned by a plugin action
// button). Hovering pauses auto-dismiss, and the stack is capped to avoid covering the viewport.
export type Toast = { id: number; text: string; link?: { href: string; label: string } }

const LIFETIME_MS = 6000
const MAX_VISIBLE = 5

type TimerEntry = {
  timer: ReturnType<typeof setTimeout> | null
  remaining: number
  startedAt: number
}

let toasts: Toast[] = []
let nextId = 1
const timers = new Map<number, TimerEntry>()
const listeners = new Set<() => void>()
const emit = () => listeners.forEach((listener) => listener())

function clearTimer(id: number) {
  const entry = timers.get(id)
  if (!entry) return
  if (entry.timer !== null) clearTimeout(entry.timer)
  timers.delete(id)
}

function scheduleTimer(id: number, delay: number) {
  timers.set(id, {
    timer: setTimeout(() => toastStore.dismiss(id), delay),
    remaining: delay,
    startedAt: Date.now(),
  })
}

export const toastStore = {
  get: () => toasts,
  subscribe(listener: () => void) {
    listeners.add(listener)
    return () => {
      listeners.delete(listener)
    }
  },
  show(text: string, link?: Toast['link']) {
    const id = nextId++
    const next = [...toasts, { id, text, ...(link ? { link } : {}) }]
    if (next.length > MAX_VISIBLE) {
      const evicted = next.slice(0, next.length - MAX_VISIBLE)
      for (const item of evicted) clearTimer(item.id)
      toasts = next.slice(-MAX_VISIBLE)
    } else {
      toasts = next
    }
    emit()
    scheduleTimer(id, LIFETIME_MS)
    return id
  },
  pause(id: number) {
    const entry = timers.get(id)
    if (!entry || entry.timer === null) return
    clearTimeout(entry.timer)
    entry.timer = null
    entry.remaining = Math.max(0, entry.remaining - (Date.now() - entry.startedAt))
  },
  resume(id: number) {
    const entry = timers.get(id)
    if (!entry || entry.timer !== null) return
    if (entry.remaining <= 0) {
      toastStore.dismiss(id)
      return
    }
    entry.startedAt = Date.now()
    entry.timer = setTimeout(() => toastStore.dismiss(id), entry.remaining)
  },
  dismiss(id: number) {
    clearTimer(id)
    if (!toasts.some((toast) => toast.id === id)) return
    toasts = toasts.filter((toast) => toast.id !== id)
    emit()
  },
  // Only for tests, to reset between cases.
  reset() {
    for (const id of [...timers.keys()]) clearTimer(id)
    toasts = []
    emit()
  },
}
