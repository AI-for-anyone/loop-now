// 页面经 preload 发来的内容（不可信）：系统通知与 Dock 未读数。不合规的一律丢掉。
export type DesktopNotification = { id: string; title: string; body: string; path: string }

const text = (value: unknown, max: number) => (typeof value === 'string' ? value.slice(0, max) : undefined)

// path 是控制台里的地址：以单个 / 开头，不含反斜杠，点通知后由页面自己的路由跳转。
export function parseNotification(value: unknown): DesktopNotification | undefined {
  if (!value || typeof value !== 'object') return undefined
  const v = value as Record<string, unknown>
  const id = text(v.id, 200)
  const title = text(v.title, 200)
  const body = text(v.body, 500)
  const path = v.path
  if (!id || !title || body === undefined || typeof path !== 'string') return undefined
  if (!path.startsWith('/') || path.startsWith('//') || path.includes('\\') || path.length > 2000) return undefined
  return { id, title, body, path }
}

export function parseBadge(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0 ? Math.min(value, 99_999) : undefined
}
