// Toast: a short message in the bottom right that disappears on its own after a few seconds (e.g. the result returned by a plugin action
// button). A link, when there is one, is shown with it.
export type Toast = { id: number; text: string; link?: { href: string; label: string } }

const LIFETIME_MS = 6000
let toasts: Toast[] = []
let nextId = 1
const listeners = new Set<() => void>()
const emit = () => listeners.forEach((listener) => listener())

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
    toasts = [...toasts, { id, text, ...(link ? { link } : {}) }]
    emit()
    setTimeout(() => toastStore.dismiss(id), LIFETIME_MS)
    return id
  },
  dismiss(id: number) {
    if (!toasts.some((toast) => toast.id === id)) return
    toasts = toasts.filter((toast) => toast.id !== id)
    emit()
  },
  // Only for tests, to reset between cases.
  reset() {
    toasts = []
    emit()
  },
}
