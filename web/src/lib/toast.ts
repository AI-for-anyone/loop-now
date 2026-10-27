// 轻提示：右下角的一条短消息，几秒后自动消失（例如插件操作按钮返回的结果）。带链接时一并显示。
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
  // 只供测试在用例之间复位。
  reset() {
    toasts = []
    emit()
  },
}
